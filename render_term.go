package main

import (
	"os"
	"regexp"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// Terminal capability handling: rune fallbacks, 256-color palette mapping
// and the mode-tinted cursor. Done lazily on the first draw.

var termState struct {
	inited    bool
	colors    int // sc.Colors() at init; 0 before
	cursorKey string
	w, h      int
}

// runeFallbacks are ASCII stand-ins for the glyphs wist draws, used when
// the terminal's encoding cannot show them.
var runeFallbacks = map[rune]string{
	'⧉': "#", '⠋': "|", '⠙': "/", '⠹': "-", '⠸': "\\", '⠼': "|", '⠴': "/", '⠦': "-", '⠧': "\\", '⠇': "|", '⠏': "/",
	'▰': "#", '▱': "-", '❯': ">", '🖼': "I", '✓': "v", '✗': "x", '✻': "*", '⎇': "b",
	'╭': "+", '╮': "+", '╰': "+", '╯': "+", '│': "|", '─': "-", '┊': ":", '▎': "|",
	'⚑': "!", '⏳': "~", '⌕': "?", '●': "*", '○': "o", '▸': ">", '☐': "[ ]", '☑': "[x]",
	'◆': "*", '⚠': "!", '↑': "^", '↓': "v", '…': "...", '⋯': "...", '•': "*", '⇣': "v",
	'⇄': "<>", '✎': "e", '⚙': "*", '📖': "r", '🗑': "d", '🌐': "w", '▀': "#", '▏': "|",
	'┬': "+", '┌': "+", '┐': "+", '└': "+", '┘': "+", '┴': "+", '·': ".",
}

func (u *UI) termInit() {
	sc := u.sc
	if !termState.inited {
		termState.inited = true
		for r, s := range runeFallbacks {
			if !sc.CanDisplay(r, false) {
				sc.RegisterRuneFallback(r, s)
			}
		}
	}
	if c := sc.Colors(); c != termState.colors {
		termState.colors = c
		refreshStyles()
	}
	if w, h := sc.Size(); w != termState.w || h != termState.h {
		termState.w, termState.h = w, h
		gfx.invalidate()
	}
}

// cursorTint colors the bar cursor by the session mode: accent when the
// agent asks, yellow in auto mode.
func (u *UI) cursorTint() {
	c := cAccent
	if strings.EqualFold(currentName(u.modeOption()), "auto") {
		c = cYellow
	}
	key := strings.Join([]string{itoa(int(c))}, "")
	if key == termState.cursorKey {
		return
	}
	termState.cursorKey = key
	u.sc.SetCursorStyle(tcell.CursorStyleSteadyBar, c)
}

// truecolor reports whether theme colors can go out as 24-bit RGB.
func truecolor() bool {
	if termState.colors == 0 {
		return true // not known yet (tests): leave colors alone
	}
	return termState.colors >= 1<<24
}

// fitColor maps an RGB color to the nearest xterm-256 palette entry when
// the terminal has no truecolor.
func fitColor(c tcell.Color) tcell.Color {
	if truecolor() || c&tcell.ColorIsRGB == 0 || !c.Valid() {
		return c
	}
	r, g, b := c.RGB()
	return tcell.PaletteColor(nearest256(int(r), int(g), int(b)))
}

var cubeLevels = [6]int{0, 95, 135, 175, 215, 255}

// nearest256 returns the xterm-256 index (16..255) closest to r, g, b,
// choosing between the 6x6x6 cube and the 24 step gray ramp.
func nearest256(r, g, b int) int {
	near := func(v int) int {
		best := 0
		for i, l := range cubeLevels {
			if abs(v-l) < abs(v-cubeLevels[best]) {
				best = i
			}
		}
		return best
	}
	ri, gi, bi := near(r), near(g), near(b)
	cube := 16 + 36*ri + 6*gi + bi
	cd := dist2(r, g, b, cubeLevels[ri], cubeLevels[gi], cubeLevels[bi])
	avg := (r + g + b) / 3
	gi2 := min(23, max(0, (avg-8+5)/10))
	gv := 8 + 10*gi2
	if gd := dist2(r, g, b, gv, gv, gv); gd < cd {
		return 232 + gi2
	}
	return cube
}

func dist2(r, g, b, r2, g2, b2 int) int {
	dr, dg, db := r-r2, g-g2, b-b2
	// Weighted for perceived brightness.
	return 3*dr*dr + 4*dg*dg + 2*db*db
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// fitStyle maps a style's colors with fitColor.
func fitStyle(st tcell.Style) tcell.Style {
	if truecolor() {
		return st
	}
	fg, bg, _ := st.Decompose()
	return st.Foreground(fitColor(fg)).Background(fitColor(bg))
}

// fitPalette maps the theme colors and the base styles to the 256 palette.
// refreshStyles calls it after a theme change.
func fitPalette() {
	if truecolor() {
		return
	}
	for _, p := range []*tcell.Color{&cText, &cDim, &cFaint, &cAccent, &cUser, &cGreen, &cRed, &cYellow, &cCyan, &cOrange, &cCodeBg, &cPanelBg, &cSelBg, &cAddBg, &cDelBg} {
		*p = fitColor(*p)
	}
	for _, p := range []*tcell.Style{&stBase, &stDim, &stFaint, &stAccent, &stUser, &stGreen, &stRed, &stYellow, &stCyan, &stCode} {
		*p = fitStyle(*p)
	}
}

// linkStyle marks text as a hyperlink to url with a dotted accent underline.
func linkStyle(st tcell.Style, url string) tcell.Style {
	st = st.Underline(tcell.UnderlineStyleDotted, cAccent)
	if url != "" {
		st = st.Url(url)
	}
	return st
}

// errorStyle is a red curly underline for the key text of failures.
func errorStyle(st tcell.Style) tcell.Style {
	return st.Underline(tcell.UnderlineStyleCurly, cRed)
}

// fileURL makes a file:// link for a tool location path.
func fileURL(p string) string {
	if p == "" || !strings.HasPrefix(p, "/") {
		return ""
	}
	host, _ := os.Hostname()
	return "file://" + host + p
}

var urlRE = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `]+`)

// linkify turns bare http(s) URLs in spans into hyperlinks, keeping the
// visible text. Trailing punctuation stays out of the link.
func linkify(spans []span) []span {
	var out []span
	for _, s := range spans {
		if s.st.GetUnderlineStyle() != tcell.UnderlineStyleNone || !strings.Contains(s.text, "://") {
			out = append(out, s)
			continue
		}
		rest := s.text
		for {
			loc := urlRE.FindStringIndex(rest)
			if loc == nil {
				break
			}
			u := strings.TrimRight(rest[loc[0]:loc[1]], ".,;:!?)]}*_")
			if strings.Count(u, "(") > strings.Count(u, ")") && loc[0]+len(u) < len(rest) && rest[loc[0]+len(u)] == ')' {
				u += ")"
			}
			if loc[0] > 0 {
				out = append(out, span{rest[:loc[0]], s.st})
			}
			out = append(out, span{u, linkStyle(s.st, u)})
			rest = rest[loc[0]+len(u):]
		}
		if rest != "" {
			out = append(out, span{rest, s.st})
		}
	}
	return out
}
