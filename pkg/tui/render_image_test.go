package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

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
