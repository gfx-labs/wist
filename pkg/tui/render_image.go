package tui

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"

	"github.com/gfx-labs/wist/pkg/render"
)

// imagePlaceholder registers an image content block in the image store and
// describes it as a one line card: "🖼 #N mime WxH · click or alt+i".
func imagePlaceholder(mime, data string) string {
	w, h := imageSize(data)
	s := "\n" + render.ImageMarker
	if n := imgs.addB64("", mime, data); n > 0 {
		s += strconv.Itoa(n)
	} else {
		s += "?"
	}
	s += " " + mime
	if w > 0 && h > 0 {
		s += fmt.Sprintf(" %dx%d", w, h)
	}
	return s + " · click or alt+i\n"
}

func imageSize(data string) (int, int) {
	head := data[:min(len(data), 4096)]
	head = head[:len(head)/4*4]
	b, err := base64.StdEncoding.DecodeString(head)
	if err != nil || len(b) < 24 {
		return 0, 0
	}
	switch {
	case strings.HasPrefix(string(b), "\x89PNG"):
		return int(binary.BigEndian.Uint32(b[16:20])), int(binary.BigEndian.Uint32(b[20:24]))
	case strings.HasPrefix(string(b), "GIF8"):
		return int(binary.LittleEndian.Uint16(b[6:8])), int(binary.LittleEndian.Uint16(b[8:10]))
	case b[0] == 0xff && b[1] == 0xd8:
		for i := 2; i+9 < len(b); {
			if b[i] != 0xff {
				i++
				continue
			}
			m := b[i+1]
			if m >= 0xc0 && m <= 0xcf && m != 0xc4 && m != 0xc8 && m != 0xcc {
				return int(binary.BigEndian.Uint16(b[i+7 : i+9])), int(binary.BigEndian.Uint16(b[i+5 : i+7]))
			}
			i += 2 + int(binary.BigEndian.Uint16(b[i+2:i+4]))
		}
	}
	return 0, 0
}

// dimLine fades a rendered line for reasoning text, keeping code colors.
func dimLine(l render.Line) render.Line {
	out := make(render.Line, len(l))
	for i, s := range l {
		fg, _, _ := s.St.Decompose()
		st := s.St
		if fg == render.CText {
			st = st.Foreground(render.CDim)
		}
		out[i] = render.Span{Text: s.Text, St: st}
	}
	return out
}
