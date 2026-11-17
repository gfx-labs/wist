package tui

import (
	"strings"
	"testing"

	"github.com/gfx-labs/wist/pkg/render"
)

func TestSelectedText(t *testing.T) {
	ls := []render.Line{
		{{Text: "hello ", St: render.StBase}, {Text: "world", St: render.StDim}},
		{{Text: "界x", St: render.StBase}},
		{{Text: "third line", St: render.StBase}},
	}
	if got := selectedText(ls, pos{0, 6}, pos{0, 10}); got != "world" {
		t.Errorf("single line: %q", got)
	}
	// Reversed drag, wide rune at line 1 takes two cells.
	if got := selectedText(ls, pos{2, 4}, pos{0, 6}); got != "world\n界x\nthird" {
		t.Errorf("multi line: %q", got)
	}
	if got := selectedText(ls, pos{1, 2}, pos{1, 2}); got != "x" {
		t.Errorf("after wide rune: %q", got)
	}
}

func TestImageSize(t *testing.T) {
	// 1x1 PNG.
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	if s := imagePlaceholder("image/png", png); !strings.Contains(s, "🖼 #") || !strings.Contains(s, "image/png 1x1") {
		t.Errorf("placeholder %q", s)
	}
}
