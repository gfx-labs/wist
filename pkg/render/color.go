package render

import (
	"regexp"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// colors is the terminal's color count, 0 until SetColors is called.
var colors int

// SetColors records how many colors the terminal shows and refits the
// theme when that changed.
func SetColors(n int) {
	if n != colors {
		colors = n
		refreshStyles()
	}
}

// truecolor reports whether theme colors can go out as 24-bit RGB.
func truecolor() bool {
	if colors == 0 {
		return true // not known yet (tests): leave colors alone
	}
	return colors >= 1<<24
}

// FitColor maps an RGB color to the nearest xterm-256 palette entry when
// the terminal has no truecolor.
func FitColor(c tcell.Color) tcell.Color {
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
	return st.Foreground(FitColor(fg)).Background(FitColor(bg))
}

// fitPalette maps the theme colors and the base styles to the 256 palette.
// refreshStyles calls it after a theme change.
func fitPalette() {
	if truecolor() {
		return
	}
	for _, p := range []*tcell.Color{&CText, &CDim, &CFaint, &CAccent, &CUser, &CGreen, &CRed, &CYellow, &CCyan, &COrange, &CCodeBg, &CPanelBg, &CSelBg, &CAddBg, &CDelBg} {
		*p = FitColor(*p)
	}
	for _, p := range []*tcell.Style{&StBase, &StDim, &StFaint, &StAccent, &StUser, &StGreen, &StRed, &StYellow, &StCyan, &StCode} {
		*p = fitStyle(*p)
	}
}

// linkStyle marks text as a hyperlink to url with a dotted accent underline.
func linkStyle(st tcell.Style, url string) tcell.Style {
	st = st.Underline(tcell.UnderlineStyleDotted, CAccent)
	if url != "" {
		st = st.Url(url)
	}
	return st
}

var urlRE = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `]+`)

// Linkify turns bare http(s) URLs in spans into hyperlinks, keeping the
// visible text. Trailing punctuation stays out of the link.
func Linkify(spans []Span) []Span {
	var out []Span
	for _, s := range spans {
		if s.St.GetUnderlineStyle() != tcell.UnderlineStyleNone || !strings.Contains(s.Text, "://") {
			out = append(out, s)
			continue
		}
		rest := s.Text
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
				out = append(out, Span{rest[:loc[0]], s.St})
			}
			out = append(out, Span{u, linkStyle(s.St, u)})
			rest = rest[loc[0]+len(u):]
		}
		if rest != "" {
			out = append(out, Span{rest, s.St})
		}
	}
	return out
}
