package tui

import (
	"testing"

	"github.com/gfx-labs/wist/pkg/render"
)

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
	if n := imageNumIn(render.Line{{Text: " 🖼 #12 image/png", St: render.StBase}}); n != 12 {
		t.Errorf("imageNumIn %d", n)
	}
}
