package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	acp "github.com/gfx-labs/go-acp"
	"github.com/gfx-labs/wist/pkg/attach"
)

// Image attachments. Terminals only paste text, so an image comes in one of
// three ways: ctrl+v reads it from the system clipboard, a pasted or typed
// path to an image file, or @path to one. Attachments wait in u.images and
// go out with the next prompt as ACP image blocks.

// maxImageBytes bounds one attachment. Providers reject much larger images
// anyway, and wist keeps the bytes in memory until the prompt is sent.
const maxImageBytes = attach.MaxBytes

type attachment struct {
	name string
	mime string
	data []byte
}

func (a attachment) label() string {
	w, h := imageSize(base64.StdEncoding.EncodeToString(a.data[:min(len(a.data), 64*1024)]))
	size := fmt.Sprintf("%.0f KB", float64(len(a.data))/1024)
	if w > 0 {
		return fmt.Sprintf("%s %dx%d %s", a.name, w, h, size)
	}
	return fmt.Sprintf("%s %s", a.name, size)
}

func imageMime(data []byte) string { return attach.Mime(data) }

// clipboardImage reads an image from the system clipboard with wl-paste on
// Wayland or xclip on X11.
func clipboardImage() (attachment, error) {
	type tool struct {
		list, get func(mime string) []string
		name      string
	}
	var tools []tool
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		tools = append(tools, tool{
			name: "wl-paste",
			list: func(string) []string { return []string{"wl-paste", "--list-types"} },
			get:  func(m string) []string { return []string{"wl-paste", "--no-newline", "--type", m} },
		})
	}
	if os.Getenv("DISPLAY") != "" {
		tools = append(tools, tool{
			name: "xclip",
			list: func(string) []string { return []string{"xclip", "-selection", "clipboard", "-t", "TARGETS", "-o"} },
			get:  func(m string) []string { return []string{"xclip", "-selection", "clipboard", "-t", m, "-o"} },
		})
	}
	if len(tools) == 0 {
		return attachment{}, fmt.Errorf("no display: clipboard images need wl-paste (Wayland) or xclip (X11)")
	}
	var lastErr error
	for _, t := range tools {
		if _, err := exec.LookPath(t.name); err != nil {
			lastErr = fmt.Errorf("%s is not installed", t.name)
			continue
		}
		types, err := runTool(t.list(""))
		if err != nil {
			lastErr = err
			continue
		}
		mime := ""
		for _, want := range []string{"image/png", "image/jpeg", "image/webp", "image/gif"} {
			if bytes.Contains(types, []byte(want)) {
				mime = want
				break
			}
		}
		if mime == "" {
			// A copied file from a file manager arrives as a uri list.
			if bytes.Contains(types, []byte("text/uri-list")) {
				if list, err := runTool(t.get("text/uri-list")); err == nil {
					for _, l := range strings.Split(string(list), "\n") {
						if p, ok := strings.CutPrefix(strings.TrimSpace(l), "file://"); ok {
							return imageFile(p)
						}
					}
				}
			}
			lastErr = fmt.Errorf("the clipboard holds no image")
			continue
		}
		data, err := runTool(t.get(mime))
		if err != nil {
			lastErr = err
			continue
		}
		if len(data) > maxImageBytes {
			return attachment{}, fmt.Errorf("clipboard image is %d MB, over the %d MB limit", len(data)>>20, maxImageBytes>>20)
		}
		if m := imageMime(data); m != "" {
			mime = m
		}
		return attachment{name: "clipboard", mime: mime, data: data}, nil
	}
	return attachment{}, lastErr
}

func runTool(argv []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", argv[0], err)
	}
	return out, nil
}

// imageFile loads p as an image attachment. It fails for files that are
// not images.
func imageFile(p string) (attachment, error) {
	im, err := attach.File(p)
	if err != nil {
		return attachment{}, err
	}
	return attachment{name: im.Name, mime: im.Mime, data: im.Data}, nil
}

var imageExts = attach.Exts

// pastedImagePath returns the image file a paste names, when the whole
// paste is a single path or file:// uri, as a drag and drop from a file
// manager produces.
func pastedImagePath(text, cwd string) string {
	s := strings.TrimSpace(text)
	if s == "" || strings.Contains(s, "\n") {
		return ""
	}
	s = strings.Trim(s, `'"`)
	s = strings.TrimPrefix(s, "file://")
	s = strings.ReplaceAll(s, `\ `, " ")
	if !imageExts[strings.ToLower(filepath.Ext(s))] {
		return ""
	}
	if strings.HasPrefix(s, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			s = filepath.Join(home, s[2:])
		}
	}
	if !filepath.IsAbs(s) {
		s = filepath.Join(cwd, s)
	}
	if st, err := os.Stat(s); err != nil || st.IsDir() {
		return ""
	}
	return s
}

func (u *UI) attach(a attachment) {
	u.images = append(u.images, a)
	u.notify("attached " + a.label() + " · backspace on empty input removes")
}

// pasteImage runs off the UI goroutine because the clipboard tools can be
// slow to answer.
func (u *UI) pasteImage() {
	if !u.imagesOK {
		u.notify("this agent does not accept images")
		return
	}
	go func() {
		a, err := clipboardImage()
		u.post(func(u *UI) {
			if err != nil {
				// No display (SSH) or no image there: ask the terminal.
				u.notify("reading the terminal clipboard (OSC 52)…")
				u.requestClipboard(true)
				return
			}
			u.attach(a)
		})
	}()
}

// imageBlocks turns the pending attachments, and any @path in text that
// names an image, into prompt blocks.
func (u *UI) imageBlocks(text string) []acp.ContentBlock {
	var out []acp.ContentBlock
	for _, a := range u.images {
		out = append(out, acp.ContentBlock{Image: &acp.ContentBlockImage{MimeType: a.mime, Data: base64.StdEncoding.EncodeToString(a.data)}})
	}
	u.images = nil
	if !u.imagesOK {
		return out
	}
	for _, f := range strings.Fields(text) {
		p, ok := strings.CutPrefix(f, "@")
		if !ok || !imageExts[strings.ToLower(filepath.Ext(strings.TrimRight(p, ".,;:!?)\"'")))] {
			continue
		}
		p = strings.TrimRight(p, ".,;:!?)\"'")
		if !filepath.IsAbs(p) && !strings.HasPrefix(p, "~/") {
			p = filepath.Join(u.opts.Cwd, p)
		}
		if a, err := imageFile(p); err == nil {
			out = append(out, acp.ContentBlock{Image: &acp.ContentBlockImage{MimeType: a.mime, Data: base64.StdEncoding.EncodeToString(a.data)}})
		}
	}
	return out
}

// attachmentLabels is the text shown on the input border for pending
// attachments.
func (u *UI) attachmentLabels() string {
	if len(u.images) == 0 {
		return ""
	}
	var names []string
	for _, a := range u.images {
		names = append(names, "🖼 "+a.name)
	}
	return " " + strings.Join(names, " ") + " "
}
