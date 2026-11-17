package main

import (
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// Side panel image display. The protocol is picked from the environment
// (WIST_IMAGES=kitty|sixel|blocks|off overrides):
//
//   - kitty graphics for kitty, ghostty and WezTerm. Outside tmux the image
//     is placed over locked cells; inside tmux every command goes through
//     DCS passthrough and the image shows through unicode placeholder
//     cells, which tmux treats as text.
//   - sixel for foot, xterm, mlterm and contour, over locked cells.
//   - half blocks ("▀" with two colors) everywhere else.

type gfxProto int

const (
	gfxOff gfxProto = iota
	gfxBlocks
	gfxKitty
	gfxSixel
)

type rect struct{ x, y, w, h int }

type gfxState struct {
	inited bool
	proto  gfxProto
	tmux   bool
	sc     tcell.Screen

	uploaded map[uint32]bool // kitty images sent to the terminal
	placed   map[uint32]string

	// What is on screen over locked cells (kitty direct / sixel).
	locked rect
	shown  string // key of the overlay drawn there
	want   string // key requested this frame
	wantAt rect
	wantFn func(io.Writer)

	// Decoded cache for blocks and sixel.
	cacheKey string
	cacheImg *image.RGBA
	cacheSix string
	cacheErr error
}

var gfx gfxState

func detectProto() (gfxProto, bool) {
	tmux := os.Getenv("TMUX") != ""
	switch strings.ToLower(os.Getenv("WIST_IMAGES")) {
	case "off", "none", "0":
		return gfxOff, tmux
	case "blocks", "halfblocks", "ansi":
		return gfxBlocks, tmux
	case "kitty":
		return gfxKitty, tmux
	case "sixel":
		return gfxSixel, tmux
	}
	term, prog := os.Getenv("TERM"), strings.ToLower(os.Getenv("TERM_PROGRAM"))
	if tmux {
		// TERM and TERM_PROGRAM name tmux here: ask it for the outer terminal.
		out, _ := exec.Command("tmux", "display", "-p", "#{client_termname}\t#{client_termtype}").Output()
		name, typ, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
		term, prog = name, strings.ToLower(strings.Fields(typ + " ")[0])
	}
	switch {
	case strings.Contains(term, "kitty"), strings.Contains(term, "ghostty"),
		prog == "ghostty", prog == "wezterm",
		os.Getenv("KITTY_WINDOW_ID") != "", os.Getenv("GHOSTTY_RESOURCES_DIR") != "",
		os.Getenv("WEZTERM_EXECUTABLE") != "":
		return gfxKitty, tmux
	case strings.HasPrefix(term, "foot"), strings.Contains(term, "mlterm"), strings.Contains(term, "contour"),
		os.Getenv("XTERM_VERSION") != "":
		return gfxSixel, tmux
	}
	return gfxBlocks, tmux
}

func (g *gfxState) init(sc tcell.Screen) {
	g.sc = sc
	if g.inited {
		return
	}
	g.inited = true
	g.proto, g.tmux = detectProto()
	g.uploaded, g.placed = map[uint32]bool{}, map[uint32]string{}
	if g.proto != gfxOff && g.proto != gfxBlocks {
		if _, ok := sc.Tty(); !ok {
			g.proto = gfxBlocks
		}
	}
}

func (g *gfxState) protoName() string {
	switch g.proto {
	case gfxKitty:
		if g.tmux {
			return "kitty (tmux placeholders)"
		}
		return "kitty"
	case gfxSixel:
		return "sixel"
	case gfxBlocks:
		return "half blocks"
	}
	return "off"
}

// write sends escape sequences straight to the terminal, through tmux
// passthrough when needed.
func (g *gfxState) write(seqs ...string) {
	if g.sc == nil {
		return
	}
	tty, ok := g.sc.Tty()
	if !ok {
		return
	}
	var b strings.Builder
	for _, s := range seqs {
		if g.tmux {
			s = tmuxWrap(s)
		}
		b.WriteString(s)
	}
	_, _ = io.WriteString(tty, b.String())
}

func (g *gfxState) cellSize() (int, int) {
	if tty, ok := g.sc.Tty(); ok {
		if ws, err := tty.WindowSize(); err == nil {
			if cw, ch := ws.CellDimensions(); cw > 0 && ch > 0 {
				return cw, ch
			}
		}
	}
	return 8, 16
}

// forget frees an evicted image in the terminal.
func (g *gfxState) forget(im *storedImage) {
	if g.uploaded[im.id] {
		g.write(kittyDelete(im.id, true))
		delete(g.uploaded, im.id)
		delete(g.placed, im.id)
	}
	if strings.HasPrefix(g.cacheKey, fmt.Sprintf("%d:", im.num)) {
		g.cacheKey, g.cacheImg, g.cacheSix = "", nil, ""
	}
}

// beginFrame resets the per-frame overlay request.
func (g *gfxState) beginFrame(sc tcell.Screen) {
	g.init(sc)
	g.want, g.wantFn = "", nil
}

// endFrame runs after Show: it removes an overlay that is no longer wanted
// and draws the requested one where it changed. Locked cells keep tcell
// from painting over it.
func (g *gfxState) endFrame() {
	if g.shown != "" && (g.want != g.shown || g.wantAt != g.locked) {
		g.clearOverlay()
	}
	if g.want == "" || g.want == g.shown {
		return
	}
	r := g.wantAt
	g.sc.LockRegion(r.x, r.y, r.w, r.h, true)
	tty, ok := g.sc.Tty()
	if !ok {
		return
	}
	var b strings.Builder
	b.WriteString("\x1b7")
	fmt.Fprintf(&b, "\x1b[%d;%dH", r.y+1, r.x+1)
	g.wantFn(&b)
	b.WriteString("\x1b8")
	_, _ = io.WriteString(tty, b.String())
	g.locked, g.shown = r, g.want
}

func (g *gfxState) clearOverlay() {
	if g.shown == "" {
		return
	}
	if g.proto == gfxKitty {
		for id := range g.uploaded {
			g.write(kittyDelete(id, false))
		}
		clear(g.placed)
	}
	r := g.locked
	// Unlocking marks the cells dirty so the next Show repaints them,
	// which also wipes sixel pixels.
	g.sc.LockRegion(r.x, r.y, r.w, r.h, false)
	g.shown, g.locked = "", rect{}
	g.sc.Show()
}

// invalidate forces the overlay to be drawn again (after Sync or resize).
func (g *gfxState) invalidate() {
	if g.shown != "" && g.sc != nil {
		g.sc.LockRegion(g.locked.x, g.locked.y, g.locked.w, g.locked.h, false)
	}
	g.shown, g.locked = "", rect{}
	clear(g.placed)
}

// decoded returns im scaled to fit maxW x maxH pixels, cached.
func (g *gfxState) decoded(im *storedImage, maxW, maxH int) (*image.RGBA, error) {
	key := fmt.Sprintf("%d:%dx%d", im.num, maxW, maxH)
	if g.cacheKey != key {
		g.cacheImg, g.cacheErr = decodeScaled(im.data, maxW, maxH)
		g.cacheKey, g.cacheSix = key, ""
	}
	return g.cacheImg, g.cacheErr
}

// drawImage shows im in the cells of r. covered means an overlay (picker,
// completion) is on top, so pixel overlays must stay hidden.
func (g *gfxState) drawImage(im *storedImage, r rect, covered bool) error {
	if r.w < 2 || r.h < 1 {
		return nil
	}
	sc := g.sc
	switch g.proto {
	case gfxOff:
		drawStr(sc, r.x, r.y, r.x+r.w, "image display is off (WIST_IMAGES)", stFaint.Italic(true))
		return nil
	case gfxBlocks:
		img, err := g.decoded(im, r.w, r.h*2)
		if err != nil {
			return err
		}
		for y, row := range halfBlocks(img, r.w, r.h) {
			for x, c := range row {
				st := tcell.StyleDefault.Foreground(rgbaColor(c.top)).Background(rgbaColor(c.bot))
				sc.SetContent(r.x+x, r.y+y, '▀', nil, st)
			}
		}
		return nil
	}
	cw, ch := g.cellSize()
	iw, ih := im.w, im.h
	if iw <= 0 || ih <= 0 {
		cfg, _, err := image.DecodeConfig(strings.NewReader(string(im.data)))
		if err != nil {
			return err
		}
		iw, ih = cfg.Width, cfg.Height
		im.w, im.h = iw, ih
	}
	fw, fh := fitSize(iw, ih, r.w*cw, r.h*ch)
	if g.proto == gfxKitty {
		// The terminal scales placements for free: let small images grow
		// up to 4x to use the panel.
		fw, fh = fitSize(iw*4, ih*4, r.w*cw, r.h*ch)
	}
	cols, rows := min(r.w, (fw+cw-1)/cw), min(r.h, (fh+ch-1)/ch)
	if g.proto == gfxSixel {
		if covered {
			return nil
		}
		img, err := g.decoded(im, fw, fh)
		if err != nil {
			return err
		}
		if g.cacheSix == "" {
			g.cacheSix = sixelEncode(img)
		}
		six := g.cacheSix
		g.want, g.wantAt = "s"+g.cacheKey, rect{r.x, r.y, cols, rows}
		g.wantFn = func(w io.Writer) { _, _ = io.WriteString(w, six) }
		return nil
	}
	// kitty
	if !g.uploaded[im.id] {
		data := im.data
		if im.mime != "image/png" || iw*ih > r.w*cw*r.h*ch*2 {
			img, err := decodeScaled(im.data, r.w*cw, r.h*ch)
			if err != nil {
				return err
			}
			data = encodePNG(img)
		}
		g.write(kittyTransmit(im.id, data)...)
		g.uploaded[im.id] = true
	}
	if g.tmux {
		key := fmt.Sprintf("%dx%d", cols, rows)
		if g.placed[im.id] != key {
			g.write(kittyPlace(im.id, cols, rows, true))
			g.placed[im.id] = key
		}
		cr, cg, cb := idColor(im.id)
		st := tcell.StyleDefault.Foreground(tcell.NewRGBColor(cr, cg, cb))
		for y := 0; y < rows; y++ {
			for x := 0; x < cols; x++ {
				c := placeholderCell(y, x)
				sc.SetContent(r.x+x, r.y+y, c[0], c[1:], st)
			}
		}
		return nil
	}
	if covered {
		return nil
	}
	id := im.id
	g.want, g.wantAt = fmt.Sprintf("k%d:%dx%d", id, cols, rows), rect{r.x, r.y, cols, rows}
	g.wantFn = func(w io.Writer) { _, _ = io.WriteString(w, kittyPlace(id, cols, rows, false)) }
	return nil
}

func rgbaColor(c interface{ RGBA() (r, g, b, a uint32) }) tcell.Color {
	r, gg, b, a := c.RGBA()
	if a == 0 {
		return tcell.ColorDefault
	}
	return fitColor(tcell.NewRGBColor(int32(r>>8), int32(gg>>8), int32(b>>8)))
}

// drawImagePanel fills the panel area with the current image and its
// caption.
func (u *UI) drawImagePanel(x, top, w, bottom int) {
	im := imgs.current()
	if im == nil {
		drawStr(u.sc, x, top, x+w, "no images yet: paste one with ctrl+v", stFaint.Italic(true))
		return
	}
	name := im.name
	if name == "" {
		name = fmt.Sprintf("image #%d", im.num)
	}
	cap := fmt.Sprintf("%s · %s", name, im.mime)
	if im.w > 0 {
		cap += fmt.Sprintf(" · %dx%d", im.w, im.h)
	}
	cap += fmt.Sprintf(" · %s · %d/%d", sizeLabel(len(im.data)), imgs.cur+1, len(imgs.list))
	drawStr(u.sc, x, top, x+w, truncate(cap, w), stDim)
	if len(imgs.list) > 1 && top+1 <= bottom {
		drawStr(u.sc, x, top+1, x+w, "[ ] or alt+[ ] cycle", stFaint)
	}
	r := rect{x, top + 2, w, bottom - top - 1}
	covered := u.picker != nil || u.comp != nil
	if err := gfx.drawImage(im, r, covered); err != nil {
		drawStr(u.sc, x, r.y, x+w, truncate("cannot show image: "+err.Error(), w), stRed)
	}
}
