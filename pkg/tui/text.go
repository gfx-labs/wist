package tui

import (
	"os"
	"regexp"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// span is a run of text with one style. A line is what one screen row shows.
type span struct {
	text string
	st   tcell.Style
}

type line []span

func (l line) width() int {
	w := 0
	for _, s := range l {
		w += uniseg.StringWidth(s.text)
	}
	return w
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// clean makes tool or model text safe to put on the screen: no escape
// sequences, no carriage returns, tabs as spaces.
func clean(s string) string {
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

// wrap breaks spans into lines no wider than width. Breaks happen at spaces
// when a word fits on a line, otherwise inside the word. first is put at the
// start of the first line and indent at the start of every later one.
func wrap(spans []span, width int, first, indent line) []line {
	if width < 4 {
		width = 4
	}
	var out []line
	cur := append(line{}, first...)
	curW := first.width()
	indentW := indent.width()
	// pending word: spans that must not be split unless longer than a line.
	type piece struct {
		text string
		st   tcell.Style
		w    int
	}
	var word []piece
	wordW := 0

	push := func(p piece) {
		if n := len(cur); n > 0 && cur[n-1].st == p.st {
			cur[n-1].text += p.text
		} else {
			cur = append(cur, span{p.text, p.st})
		}
		curW += p.w
	}
	newline := func() {
		out = append(out, trimRight(cur))
		cur = append(line{}, indent...)
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
		rest := s.text
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
				push(piece{" ", s.st, 1})
				continue
			}
			word = append(word, piece{g, s.st, w})
			wordW += w
		}
	}
	flushWord()
	out = append(out, trimRight(cur))
	return out
}

func trimRight(l line) line {
	for len(l) > 0 {
		last := &l[len(l)-1]
		t := strings.TrimRight(last.text, " ")
		_, bg, _ := last.st.Decompose()
		if t != "" || bg != tcell.ColorDefault {
			if bg == tcell.ColorDefault {
				last.text = t
			}
			return l
		}
		l = l[:len(l)-1]
	}
	return l
}

// truncate cuts s to at most w cells, ending in an ellipsis when cut.
func truncate(s string, w int) string {
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

func drawStr(sc tcell.Screen, x, y, maxX int, s string, st tcell.Style) int {
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

func drawLine(sc tcell.Screen, x, y, maxX int, l line) int {
	for _, s := range l {
		x = drawStr(sc, x, y, maxX, s.text, s.st)
	}
	return x
}

func fill(sc tcell.Screen, x0, y, x1 int, st tcell.Style) {
	for x := x0; x < x1; x++ {
		sc.SetContent(x, y, ' ', nil, st)
	}
}

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}
