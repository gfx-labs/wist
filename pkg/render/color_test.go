package render

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestLinkify(t *testing.T) {
	sp := Linkify([]Span{{"see https://x.io/a_(b). ok", StDim}})
	var text strings.Builder
	var url string
	for _, s := range sp {
		text.WriteString(s.Text)
		if s.St.GetUnderlineStyle() == tcell.UnderlineStyleDotted {
			url = s.Text
		}
	}
	if text.String() != "see https://x.io/a_(b). ok" || url != "https://x.io/a_(b)" {
		t.Errorf("text %q url %q", text.String(), url)
	}
	md := inline("[docs](https://go.dev)", StBase)
	if md[0].Text != "docs" || md[0].St.GetUnderlineStyle() != tcell.UnderlineStyleDotted {
		t.Errorf("md link %+v", md)
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
	colors = 256
	defer func() { colors = 0 }()
	if c := FitColor(tcell.NewRGBColor(255, 0, 0)); c != tcell.PaletteColor(196) {
		t.Errorf("fitColor %v", c)
	}
}

func TestApplyTheme(t *testing.T) {
	defer ApplyTheme("dark", nil)
	if w := ApplyTheme("light", map[string]string{"accent": "#123456"}); len(w) != 0 {
		t.Fatal(w)
	}
	if r, g, b := CAccent.RGB(); r != 0x12 || g != 0x34 || b != 0x56 {
		t.Fatalf("accent %x %x %x", r, g, b)
	}
	if w := ApplyTheme("nope", map[string]string{"bogus": "#fff", "text": "notacolor"}); len(w) != 3 {
		t.Fatalf("want 3 warnings, got %v", w)
	}
	for name := range themePresets {
		if len(themePresets[name]) != len(themeColors) {
			t.Errorf("preset %s sets %d of %d colors", name, len(themePresets[name]), len(themeColors))
		}
	}
}
