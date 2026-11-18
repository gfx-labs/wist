// Package attach loads image files for ACP image prompt blocks. It has no
// UI dependencies so both the TUI and wist ask can use it.
package attach

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	acp "github.com/gfx-labs/go-acp"
)

// MaxBytes bounds one image. Providers reject much larger images anyway.
const MaxBytes = 8 << 20

// Exts are the file extensions treated as images.
var Exts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

// Image is a loaded image file.
type Image struct {
	Name string
	Mime string
	Data []byte
}

// Block returns the image as an ACP prompt block.
func (i Image) Block() acp.ContentBlock {
	return acp.ContentBlock{Image: &acp.ContentBlockImage{MimeType: i.Mime, Data: base64.StdEncoding.EncodeToString(i.Data)}}
}

// Mime reports the image type of data from its magic bytes, or "".
func Mime(data []byte) string {
	switch ct := http.DetectContentType(data); ct {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return ct
	}
	return ""
}

// File loads p (a leading ~/ is expanded) as an image. It fails for files
// that are not png, jpeg, gif or webp images or are over MaxBytes.
func File(p string) (Image, error) {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, p[2:])
	}
	st, err := os.Stat(p)
	if err != nil {
		return Image{}, err
	}
	if st.IsDir() {
		return Image{}, fmt.Errorf("%s is a directory", p)
	}
	if st.Size() > MaxBytes {
		return Image{}, fmt.Errorf("%s is %d MB, over the %d MB limit", filepath.Base(p), st.Size()>>20, MaxBytes>>20)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Image{}, err
	}
	mime := Mime(data)
	if mime == "" {
		return Image{}, fmt.Errorf("%s is not a png, jpeg, gif or webp image", filepath.Base(p))
	}
	return Image{Name: filepath.Base(p), Mime: mime, Data: data}, nil
}
