package tui

import (
	"os"
	"path/filepath"
	"testing"
)

var tinyPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde\x00\x00\x00\x0cIDATx\x9cc\xf8\xcf\xc0\x00\x00\x03\x01\x01\x00\xc9\xfe\x92\xef\x00\x00\x00\x00IEND\xaeB`\x82")

func TestPastedImagePath(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "my shot.png")
	if err := os.WriteFile(img, tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{img, "file://" + img, "'" + img + "'", filepath.Join(dir, `my\ shot.png`), "my shot.png"} {
		if got := pastedImagePath(in, dir); got != img {
			t.Errorf("pastedImagePath(%q) = %q, want %q", in, got, img)
		}
	}
	for _, in := range []string{"hello world", img + "\nmore", filepath.Join(dir, "missing.png"), dir} {
		if got := pastedImagePath(in, dir); got != "" {
			t.Errorf("pastedImagePath(%q) = %q, want none", in, got)
		}
	}
}

func TestImageFile(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "a.png")
	txt := filepath.Join(dir, "b.png")
	_ = os.WriteFile(img, tinyPNG, 0o644)
	_ = os.WriteFile(txt, []byte("not an image"), 0o644)
	a, err := imageFile(img)
	if err != nil || a.mime != "image/png" || a.name != "a.png" {
		t.Fatalf("imageFile(png) = %+v, %v", a, err)
	}
	if _, err := imageFile(txt); err == nil {
		t.Fatal("imageFile accepted a text file named .png")
	}
}
