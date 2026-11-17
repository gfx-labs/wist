package render

import (
	"regexp"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// Span is a run of text with one style. A line is what one screen row shows.
type Span struct {
	Text string      // text, already Clean
	St   tcell.Style // its style
}

// Line is one screen row of spans.
type Line []Span

// Width is the line's width in cells.
func (l Line) Width() int {
	w := 0
	for _, s := range l {
		w += uniseg.StringWidth(s.Text)
	}
	return w
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// Clean makes tool or model text safe to put on the screen: no escape
// sequences, no carriage returns, tabs as spaces.
func Clean(s string) string {
	if strings.IndexByte(s, 0x1b) >= 0 {
		s = ansiRE.ReplaceAllString(s, "")
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\t", "    ")
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' {
			return -1
		}
		return r
	}, s)
}

// Wrap breaks spans into lines no wider than width. Breaks happen at spaces
// when a word fits on a line, otherwise inside the word. first is put at the
// start of the first line and indent at the start of every later one.
func Wrap(spans []Span, width int, first, indent Line) []Line {
	if width < 4 {
		width = 4
	}
	var out []Line
	cur := append(Line{}, first...)
	curW := first.Width()
	indentW := indent.Width()
	// pending word: spans that must not be split unless longer than a line.
	type piece struct {
		text string
		st   tcell.Style
		w    int
	}
	var word []piece
	wordW := 0

	push := func(p piece) {
		if n := len(cur); n > 0 && cur[n-1].St == p.st {
			cur[n-1].Text += p.text
		} else {
			cur = append(cur, Span{p.text, p.st})
		}
		curW += p.w
	}
	newline := func() {
		out = append(out, trimRight(cur))
		cur = append(Line{}, indent...)
		curW = indentW
	}
	flushWord := func() {
		if len(word) == 0 {
			return
		}
		if curW+wordW > width && wordW <= width-indentW && curW > indentW {
			newline()
		}
		for _, p := range word {
			if curW+p.w > width && curW > indentW {
				newline()
			}
			push(p)
		}
		word, wordW = word[:0], 0
	}

	for _, s := range spans {
		state := -1
		rest := s.Text
		for rest != "" {
			var g string
			var w int
			g, rest, w, state = uniseg.FirstGraphemeClusterInString(rest, state)
			switch g {
			case "\n":
				flushWord()
				newline()
				continue
			case " ":
				flushWord()
				if curW+1 > width {
					newline()
					continue
				}
				push(piece{" ", s.St, 1})
				continue
			}
			word = append(word, piece{g, s.St, w})
			wordW += w
		}
	}
	flushWord()
	out = append(out, trimRight(cur))
	return out
}

func trimRight(l Line) Line {
	for len(l) > 0 {
		last := &l[len(l)-1]
		t := strings.TrimRight(last.Text, " ")
		_, bg, _ := last.St.Decompose()
		if t != "" || bg != tcell.ColorDefault {
			if bg == tcell.ColorDefault {
				last.Text = t
			}
			return l
		}
		l = l[:len(l)-1]
	}
	return l
}

// Truncate cuts s to at most w cells, ending in an ellipsis when cut.
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if uniseg.StringWidth(s) <= w {
		return s
	}
	var b strings.Builder
	used := 0
	state := -1
	for s != "" {
		var g string
		var gw int
		g, s, gw, state = uniseg.FirstGraphemeClusterInString(s, state)
		if used+gw > w-1 {
			break
		}
		b.WriteString(g)
		used += gw
	}
	b.WriteString("…")
	return b.String()
}

// DrawStr draws s at x, y, clipped at maxX, and returns the next x.
func DrawStr(sc tcell.Screen, x, y, maxX int, s string, st tcell.Style) int {
	state := -1
	for s != "" && x < maxX {
		var g string
		var w int
		g, s, w, state = uniseg.FirstGraphemeClusterInString(s, state)
		if w == 0 {
			continue
		}
		if x+w > maxX {
			break
		}
		rs := []rune(g)
		sc.SetContent(x, y, rs[0], rs[1:], st)
		for i := 1; i < w; i++ {
			sc.SetContent(x+i, y, ' ', nil, st)
		}
		x += w
	}
	return x
}

// DrawLine draws l at x, y, clipped at maxX, and returns the next x.
func DrawLine(sc tcell.Screen, x, y, maxX int, l Line) int {
	for _, s := range l {
		x = DrawStr(sc, x, y, maxX, s.Text, s.St)
	}
	return x
}

// Fill paints cells x0 to x1 (exclusive) of row y with st.
func Fill(sc tcell.Screen, x0, y, x1 int, st tcell.Style) {
	for x := x0; x < x1; x++ {
		sc.SetContent(x, y, ' ', nil, st)
	}
}
