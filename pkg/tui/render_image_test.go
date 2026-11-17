package tui

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestKittyTransmitChunks(t *testing.T) {
	data := make([]byte, 7000) // 9336 base64 chars: 3 chunks
	cmds := kittyTransmit(42, data)
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
	if one := kittyTransmit(1, []byte("x")); len(one) != 1 || !strings.Contains(one[0], "m=0;eA==") {
		t.Errorf("single %q", one)
	}
}

func TestTmuxWrap(t *testing.T) {
	got := tmuxWrap("\x1b_Ga=p\x1b\\")
	want := "\x1bPtmux;\x1b\x1b_Ga=p\x1b\x1b\\\x1b\\"
	if got != want {
		t.Errorf("got %q", got)
	}
	if p := kittyPlace(7, 10, 5, true); p != "\x1b_Ga=p,U=1,i=7,c=10,r=5,q=2\x1b\\" {
		t.Errorf("virtual place %q", p)
	}
}

func TestPlaceholderDiacritics(t *testing.T) {
	if len(kittyDiacritics) != 297 || kittyDiacritics[0] != 0x305 || kittyDiacritics[1] != 0x30D || kittyDiacritics[296] != 0x1D244 {
		t.Fatalf("table %d %x", len(kittyDiacritics), kittyDiacritics[0])
	}
	c := placeholderCell(2, 1)
	if c[0] != 0x10EEEE || c[1] != kittyDiacritics[2] || c[2] != kittyDiacritics[1] {
		t.Errorf("cell %x", c)
	}
	if r, g, b := idColor(0x12345); r != 0x01 || g != 0x23 || b != 0x45 {
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
	s := sixelEncode(img)
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
	cells := halfBlocks(img, 2, 1) // 2x2 pixels: top white, bottom black
	if len(cells) != 1 || len(cells[0]) != 2 {
		t.Fatalf("size %dx%d", len(cells), len(cells[0]))
	}
	if c := cells[0][0]; c.top.R != 255 || c.bot.R != 0 {
		t.Errorf("cell %+v", c)
	}
	if w, h := fitSize(800, 600, 40, 40); w != 40 || h != 30 {
		t.Errorf("fit %d %d", w, h)
	}
}

func TestLinkify(t *testing.T) {
	sp := linkify([]span{{"see https://x.io/a_(b). ok", stDim}})
	var text strings.Builder
	var url string
	for _, s := range sp {
		text.WriteString(s.text)
		if s.st.GetUnderlineStyle() == tcell.UnderlineStyleDotted {
			url = s.text
		}
	}
	if text.String() != "see https://x.io/a_(b). ok" || url != "https://x.io/a_(b)" {
		t.Errorf("text %q url %q", text.String(), url)
	}
	md := inline("[docs](https://go.dev)", stBase)
	if md[0].text != "docs" || md[0].st.GetUnderlineStyle() != tcell.UnderlineStyleDotted {
		t.Errorf("md link %+v", md)
	}
}

func TestRuneFallbacks(t *testing.T) {
	for _, r := range []rune{'⧉', '⠋', '⠏', '▰', '▱', '❯', '🖼', '✓', '✗', '✻', '⎇', '╭', '╮', '╰', '╯', '│', '─', '┊', '▎', '⚑', '⏳', '⌕', '●', '○', '▸', '☐', '☑'} {
		s, ok := runeFallbacks[r]
		if !ok || s == "" {
			t.Errorf("no fallback for %q", r)
		}
		for _, c := range s {
			if c > 0x7e {
				t.Errorf("fallback for %q not ASCII: %q", r, s)
			}
		}
	}
}

func TestNearest256(t *testing.T) {
	cases := []struct{ r, g, b, want int }{
		{0, 0, 0, 16}, {255, 255, 255, 231}, {255, 0, 0, 196}, {128, 128, 128, 244},
		{0x8a, 0xb4, 0xf8, 111}, {0x1e, 0x21, 0x27, 235},
	}
	for _, c := range cases {
		if got := nearest256(c.r, c.g, c.b); got != c.want {
			t.Errorf("nearest256(%d,%d,%d) = %d, want %d", c.r, c.g, c.b, got, c.want)
		}
	}
	termState.colors = 256
	defer func() { termState.colors = 0 }()
	if c := fitColor(tcell.NewRGBColor(255, 0, 0)); c != tcell.PaletteColor(196) {
		t.Errorf("fitColor %v", c)
	}
}

func TestImageStoreEviction(t *testing.T) {
	old := imgs
	defer func() { imgs = old }()
	imgs = imageStore{}
	for i := 0; i < maxStoredImages+2; i++ {
		imgs.add("x", "image/png", make([]byte, 10))
	}
	if len(imgs.list) != maxStoredImages || imgs.list[0].num != 3 || imgs.byNum(1) != -1 {
		t.Errorf("eviction: %d first #%d", len(imgs.list), imgs.list[0].num)
	}
	imgs.add("big", "image/png", make([]byte, maxStoredImageBytes))
	if len(imgs.list) != 1 || imgs.list[0].name != "big" {
		t.Errorf("byte cap: %d", len(imgs.list))
	}
	if n := imageNumIn(line{{" 🖼 #12 image/png", stBase}}); n != 12 {
		t.Errorf("imageNumIn %d", n)
	}
}
