package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// The image store keeps the last few images seen in the session (agent
// messages, tool output and the user's own attachments) so the side panel
// can show them. Placeholders in the transcript carry the image's number
// ("🖼 #3 …"), which is how clicks find their image.

const (
	maxStoredImages     = 8
	maxStoredImageBytes = 10 << 20
)

type storedImage struct {
	num  int    // shown as #num, 1-based for the session
	id   uint32 // kitty image id (also the placeholder foreground color)
	name string
	mime string
	data []byte
	w, h int
}

type imageStore struct {
	list  []*storedImage
	total int
	seq   int
	cur   int // index into list of the panel's image
	seen  map[string]int // image key -> number, kept after eviction
	base  uint32
}

var imgs imageStore

// imageKey identifies base64 data cheaply so re-rendered tool output does
// not decode the same image again.
func imageKey(mime, data string) string {
	if len(data) <= 128 {
		return mime + "\x00" + data
	}
	return mime + "\x00" + strconv.Itoa(len(data)) + "\x00" + data[:64] + data[len(data)-64:]
}

// addB64 stores base64 image data and returns its number. Data seen
// before (re-rendered blocks) keeps its number and is not stored again,
// even after eviction.
func (s *imageStore) addB64(name, mime, data string) int {
	key := imageKey(mime, data)
	if n, ok := s.seen[key]; ok {
		return n
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(data)
		if err != nil {
			return 0
		}
	}
	im := s.add(name, mime, raw)
	s.seen[key] = im.num
	return im.num
}

func (s *imageStore) add(name, mime string, data []byte) *storedImage {
	if s.seen == nil {
		s.seen = map[string]int{}
	}
	if s.base == 0 {
		// Spread ids by process so two wists in one terminal do not clash.
		s.base = uint32(os.Getpid()%4000+1) << 12
	}
	s.seq++
	im := &storedImage{num: s.seq, id: s.base | uint32(s.seq&0xfff), name: name, mime: mime, data: data}
	im.w, im.h = imageSizeBytes(data)
	if mime == "" {
		im.mime = imageMime(data)
	}
	s.list = append(s.list, im)
	s.total += len(data)
	for len(s.list) > 1 && (len(s.list) > maxStoredImages || s.total > maxStoredImageBytes) {
		old := s.list[0]
		s.total -= len(old.data)
		s.list = s.list[1:]
		imageForget(old)
	}
	// The newest image becomes the panel's, like the newest diagram.
	s.cur = len(s.list) - 1
	if rs.panel.mode != "tool" {
		rs.panel.mode = "image"
	}
	return im
}

func (s *imageStore) byNum(n int) int {
	for i, im := range s.list {
		if im.num == n {
			return i
		}
	}
	return -1
}

func (s *imageStore) current() *storedImage {
	if len(s.list) == 0 {
		return nil
	}
	s.cur = min(max(s.cur, 0), len(s.list)-1)
	return s.list[s.cur]
}

func imageSizeBytes(b []byte) (int, int) {
	return imageSize(base64.StdEncoding.EncodeToString(b[:min(len(b), 3*1024)]))
}

func sizeLabel(n int) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.0f KB", float64(n)/1024)
}

// imageMarker is the token placeholders start with.
const imageMarker = "🖼 #"

// storeAttachment registers a prompt attachment and returns its line for
// the user block.
func (u *UI) storeAttachment(a attachment) string {
	im := imgs.add(a.name, a.mime, a.data)
	s := fmt.Sprintf("%s%d %s", imageMarker, im.num, a.name)
	if im.w > 0 {
		s += fmt.Sprintf(" %dx%d", im.w, im.h)
	}
	return s + " " + sizeLabel(len(a.data)) + " · click or alt+i"
}

// imageNumIn finds an image placeholder number in a rendered line.
func imageNumIn(l line) int {
	for _, sp := range l {
		i := strings.Index(sp.text, imageMarker)
		if i < 0 {
			continue
		}
		rest := sp.text[i+len(imageMarker):]
		j := 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if n, err := strconv.Atoi(rest[:j]); err == nil {
			return n
		}
	}
	return 0
}

// openImage shows image number n in the side panel.
func (u *UI) openImage(n int) {
	i := imgs.byNum(n)
	if i < 0 {
		u.notify(fmt.Sprintf("image #%d is no longer kept (last %d)", n, maxStoredImages))
		return
	}
	imgs.cur = i
	rs.panel.open, rs.panel.mode, rs.panel.scroll = true, "image", 0
}

func (u *UI) cycleImage(d int) {
	if len(imgs.list) == 0 {
		u.notify("no images")
		return
	}
	imgs.cur = (imgs.cur + d + len(imgs.list)) % len(imgs.list)
}

// imageForget drops terminal-side state for an evicted image.
func imageForget(im *storedImage) { gfx.forget(im) }

// renderSynced tells the image code the screen was fully repainted
// (Sync, resize, suspend/resume), so pixel overlays must be drawn again.
func renderSynced() { gfx.invalidate() }
