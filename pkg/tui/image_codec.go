package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"strings"

	_ "golang.org/x/image/webp"
)

// Pure image encoding helpers for the side panel: decoding and scaling,
// kitty graphics commands, tmux passthrough, unicode placeholders, sixel
// and half-block cells. They do no terminal I/O so they are easy to test.

// maxDecodePixels bounds images wist decodes itself. A decoded RGBA image
// takes 4 bytes a pixel, and wist must stay small.
const maxDecodePixels = 4 << 20

// decodeScaled decodes data and scales it to fit in maxW x maxH pixels,
// keeping the aspect ratio. It never scales up.
func decodeScaled(data []byte, maxW, maxH int) (*image.RGBA, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width*cfg.Height > maxDecodePixels {
		return nil, fmt.Errorf("image too large to preview (%dx%d)", cfg.Width, cfg.Height)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	w, h := fitSize(cfg.Width, cfg.Height, maxW, maxH)
	return scaleBox(src, w, h), nil
}

// fitSize scales w x h down to fit in maxW x maxH, keeping the aspect.
func fitSize(w, h, maxW, maxH int) (int, int) {
	if w <= 0 || h <= 0 {
		return 1, 1
	}
	if maxW <= 0 || maxH <= 0 {
		return w, h
	}
	if w <= maxW && h <= maxH {
		return w, h
	}
	if w*maxH > h*maxW { // width bound
		return maxW, max(1, h*maxW/w)
	}
	return max(1, w*maxH/h), maxH
}

// scaleBox downscales src to w x h by averaging the source pixels that fall
// in each destination pixel.
func scaleBox(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sw, sh := b.Dx(), b.Dy()
	for y := 0; y < h; y++ {
		y0, y1 := b.Min.Y+y*sh/h, b.Min.Y+max((y+1)*sh/h, y*sh/h+1)
		for x := 0; x < w; x++ {
			x0, x1 := b.Min.X+x*sw/w, b.Min.X+max((x+1)*sw/w, x*sw/w+1)
			var r, g, bl, a, n uint32
			// Sample at most 4x4 points per pixel so huge downscales stay fast.
			sx, sy := max(1, (x1-x0)/4), max(1, (y1-y0)/4)
			for yy := y0; yy < y1; yy += sy {
				for xx := x0; xx < x1; xx += sx {
					cr, cg, cb, ca := src.At(xx, yy).RGBA()
					r, g, bl, a, n = r+cr, g+cg, bl+cb, a+ca, n+1
				}
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = uint8(r/n>>8), uint8(g/n>>8), uint8(bl/n>>8), uint8(a/n>>8)
		}
	}
	return dst
}

func encodePNG(img image.Image) []byte {
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	_ = enc.Encode(&b, img)
	return b.Bytes()
}

// --- kitty graphics ----------------------------------------------------------

const kittyChunk = 4096

// kittyTransmit returns the APC commands that upload png data as image id,
// base64 encoded in 4096 byte chunks, with responses suppressed.
func kittyTransmit(id uint32, pngData []byte) []string {
	enc := base64.StdEncoding.EncodeToString(pngData)
	var out []string
	for first := true; first || enc != ""; first = false {
		n := min(len(enc), kittyChunk)
		chunk := enc[:n]
		enc = enc[n:]
		more := 0
		if enc != "" {
			more = 1
		}
		ctl := fmt.Sprintf("m=%d", more)
		if first {
			ctl = fmt.Sprintf("a=t,f=100,t=d,i=%d,q=2,m=%d", id, more)
		}
		out = append(out, "\x1b_G"+ctl+";"+chunk+"\x1b\\")
	}
	return out
}

// kittyPlace places image id over cols x rows cells at the cursor without
// moving it. virtual makes a unicode placeholder placement.
func kittyPlace(id uint32, cols, rows int, virtual bool) string {
	if virtual {
		return fmt.Sprintf("\x1b_Ga=p,U=1,i=%d,c=%d,r=%d,q=2\x1b\\", id, cols, rows)
	}
	return fmt.Sprintf("\x1b_Ga=p,i=%d,p=1,c=%d,r=%d,C=1,q=2\x1b\\", id, cols, rows)
}

// kittyDelete removes the placements of id (keeping its data) or, with
// free, the image itself.
func kittyDelete(id uint32, free bool) string {
	d := "i"
	if free {
		d = "I"
	}
	return fmt.Sprintf("\x1b_Ga=d,d=%s,i=%d,q=2\x1b\\", d, id)
}

// tmuxWrap wraps an escape sequence in tmux's DCS passthrough, doubling
// its escapes.
func tmuxWrap(s string) string {
	return "\x1bPtmux;" + strings.ReplaceAll(s, "\x1b", "\x1b\x1b") + "\x1b\\"
}

const placeholderRune = 0x10EEEE

// placeholderCell returns the runes of the unicode placeholder cell at row,
// col: U+10EEEE followed by the row and column diacritics.
func placeholderCell(row, col int) []rune {
	n := len(kittyDiacritics)
	return []rune{placeholderRune, kittyDiacritics[min(row, n-1)], kittyDiacritics[min(col, n-1)]}
}

// idColor is the 24-bit foreground that names image id for placeholders.
func idColor(id uint32) (r, g, b int32) {
	return int32(id >> 16 & 0xff), int32(id >> 8 & 0xff), int32(id & 0xff)
}

// --- sixel -------------------------------------------------------------------

// sixelEncode encodes img as sixel with the 6x6x6 color cube. Pixels with
// alpha below half are left transparent.
func sixelEncode(img *image.RGBA) string {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var sb strings.Builder
	fmt.Fprintf(&sb, "\x1bP0;1;0q\"1;1;%d;%d", w, h)
	idx := make([]int16, w*h)
	used := [216]bool{}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(b.Min.X+x, b.Min.Y+y)
			p := img.Pix[i : i+4]
			if p[3] < 128 {
				idx[y*w+x] = -1
				continue
			}
			c := cube6(p[0])*36 + cube6(p[1])*6 + cube6(p[2])
			idx[y*w+x] = int16(c)
			used[c] = true
		}
	}
	for c, ok := range used {
		if ok {
			r, g, bl := c/36, c/6%6, c%6
			fmt.Fprintf(&sb, "#%d;2;%d;%d;%d", c, r*20, g*20, bl*20)
		}
	}
	row := make([]byte, w)
	for top := 0; top < h; top += 6 {
		var bandUsed [216]bool
		for y := top; y < min(top+6, h); y++ {
			for x := 0; x < w; x++ {
				if c := idx[y*w+x]; c >= 0 {
					bandUsed[c] = true
				}
			}
		}
		firstColor := true
		for c, ok := range bandUsed {
			if !ok {
				continue
			}
			for x := 0; x < w; x++ {
				var bits byte
				for k := 0; k < 6 && top+k < h; k++ {
					if idx[(top+k)*w+x] == int16(c) {
						bits |= 1 << k
					}
				}
				row[x] = 63 + bits
			}
			if !firstColor {
				sb.WriteByte('$')
			}
			firstColor = false
			fmt.Fprintf(&sb, "#%d", c)
			sixelRLE(&sb, row)
		}
		sb.WriteByte('-')
	}
	sb.WriteString("\x1b\\")
	return sb.String()
}

func cube6(v uint8) int { return (int(v)*5 + 127) / 255 }

func sixelRLE(sb *strings.Builder, row []byte) {
	for i := 0; i < len(row); {
		j := i
		for j < len(row) && row[j] == row[i] {
			j++
		}
		if n := j - i; n > 3 {
			fmt.Fprintf(sb, "!%d%c", n, row[i])
		} else {
			for ; i < j; i++ {
				sb.WriteByte(row[i])
			}
		}
		i = j
	}
}

// --- half blocks -------------------------------------------------------------

// halfCell is one "▀" cell: top pixel as foreground, bottom as background.
type halfCell struct{ top, bot color.RGBA }

// halfBlocks downscales data to at most cols x rows cells of two pixels
// each, keeping the aspect ratio for cells about twice as tall as wide.
func halfBlocks(img *image.RGBA, cols, rows int) [][]halfCell {
	b := img.Bounds()
	w, h := fitSize(b.Dx(), b.Dy(), cols, rows*2)
	small := img
	if w != b.Dx() || h != b.Dy() {
		small = scaleBox(img, w, h)
	}
	out := make([][]halfCell, (h+1)/2)
	px := func(x, y int) color.RGBA {
		if y >= h {
			return color.RGBA{}
		}
		i := small.PixOffset(x, y)
		p := small.Pix[i : i+4]
		return color.RGBA{p[0], p[1], p[2], p[3]}
	}
	for r := range out {
		out[r] = make([]halfCell, w)
		for x := 0; x < w; x++ {
			out[r][x] = halfCell{px(x, 2*r), px(x, 2*r+1)}
		}
	}
	return out
}
