package termimg

import (
	"image"
	"image/color"
	"strings"
	"testing"
)

func TestKittyTransmitChunks(t *testing.T) {
	data := make([]byte, 7000) // 9336 base64 chars: 3 chunks
	cmds := KittyTransmit(42, data)
	if len(cmds) != 3 {
		t.Fatalf("chunks = %d", len(cmds))
	}
	if !strings.HasPrefix(cmds[0], "\x1b_Ga=t,f=100,t=d,i=42,q=2,m=1;") {
		t.Errorf("first %q", cmds[0][:40])
	}
	if !strings.HasPrefix(cmds[1], "\x1b_Gm=1;") || !strings.HasPrefix(cmds[2], "\x1b_Gm=0;") {
		t.Errorf("later chunks %q %q", cmds[1][:10], cmds[2][:10])
	}
	for _, c := range cmds {
		payload := c[strings.IndexByte(c, ';')+1 : len(c)-2]
		if len(payload) > kittyChunk || !strings.HasSuffix(c, "\x1b\\") {
			t.Errorf("bad chunk len %d", len(payload))
		}
	}
	if one := KittyTransmit(1, []byte("x")); len(one) != 1 || !strings.Contains(one[0], "m=0;eA==") {
		t.Errorf("single %q", one)
	}
}

func TestTmuxWrap(t *testing.T) {
	got := TmuxWrap("\x1b_Ga=p\x1b\\")
	want := "\x1bPtmux;\x1b\x1b_Ga=p\x1b\x1b\\\x1b\\"
	if got != want {
		t.Errorf("got %q", got)
	}
	if p := KittyPlace(7, 10, 5, true); p != "\x1b_Ga=p,U=1,i=7,c=10,r=5,q=2\x1b\\" {
		t.Errorf("virtual place %q", p)
	}
}

func TestPlaceholderDiacritics(t *testing.T) {
	if len(kittyDiacritics) != 297 || kittyDiacritics[0] != 0x305 || kittyDiacritics[1] != 0x30D || kittyDiacritics[296] != 0x1D244 {
		t.Fatalf("table %d %x", len(kittyDiacritics), kittyDiacritics[0])
	}
	c := PlaceholderCell(2, 1)
	if c[0] != 0x10EEEE || c[1] != kittyDiacritics[2] || c[2] != kittyDiacritics[1] {
		t.Errorf("cell %x", c)
	}
	if r, g, b := IDColor(0x12345); r != 0x01 || g != 0x23 || b != 0x45 {
		t.Errorf("id color %x %x %x", r, g, b)
	}
}

func TestSixelEncode(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 7))
	for y := 0; y < 7; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	s := SixelEncode(img)
	// Red is cube index 180; first band full (6 rows, "~"), second one row ("@").
	want := "\x1bP0;1;0q\"1;1;4;7#180;2;100;0;0#180!4~-#180!4@-\x1b\\"
	if s != want {
		t.Errorf("sixel\n got %q\nwant %q", s, want)
	}
}

func TestHalfBlocks(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			c := color.RGBA{0, 0, 0, 255}
			if y < 2 {
				c = color.RGBA{255, 255, 255, 255}
			}
			img.Set(x, y, c)
		}
	}
	cells := HalfBlocks(img, 2, 1) // 2x2 pixels: top white, bottom black
	if len(cells) != 1 || len(cells[0]) != 2 {
		t.Fatalf("size %dx%d", len(cells), len(cells[0]))
	}
	if c := cells[0][0]; c.Top.R != 255 || c.Bot.R != 0 {
		t.Errorf("cell %+v", c)
	}
	if w, h := FitSize(800, 600, 40, 40); w != 40 || h != 30 {
		t.Errorf("fit %d %d", w, h)
	}
}
