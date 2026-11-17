package main

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
)

// imagePlaceholder describes an image content block as "[image mime WxH]".
// Only the first bytes of the base64 data are decoded to read the size.
func imagePlaceholder(mime, data string) string {
	w, h := imageSize(data)
	if w > 0 && h > 0 {
		return fmt.Sprintf("\n[image %s %dx%d]\n", mime, w, h)
	}
	return "\n[image " + mime + "]\n"
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
func dimLine(l line) line {
	out := make(line, len(l))
	for i, s := range l {
		fg, _, _ := s.st.Decompose()
		st := s.st
		if fg == cText {
			st = st.Foreground(cDim)
		}
		out[i] = span{s.text, st}
	}
	return out
}
