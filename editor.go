package main

import (
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

// editor is the prompt input: a rune buffer with a cursor, emacs style
// motions, a kill ring of one entry and prompt history.
type editor struct {
	buf    []rune
	cur    int
	kill   []rune
	hist   []string
	histAt int    // len(hist) when not browsing
	draft  string // what was typed before browsing history
}

func (e *editor) text() string { return string(e.buf) }

func (e *editor) set(s string) {
	e.buf = []rune(s)
	e.cur = len(e.buf)
}

func (e *editor) clear() {
	e.buf = e.buf[:0]
	e.cur = 0
	e.histAt = len(e.hist)
}

func (e *editor) insert(s string) {
	r := []rune(s)
	e.buf = append(e.buf[:e.cur], append(r, e.buf[e.cur:]...)...)
	e.cur += len(r)
}

func (e *editor) backspace() {
	if e.cur > 0 {
		e.buf = append(e.buf[:e.cur-1], e.buf[e.cur:]...)
		e.cur--
	}
}

func (e *editor) del() {
	if e.cur < len(e.buf) {
		e.buf = append(e.buf[:e.cur], e.buf[e.cur+1:]...)
	}
}

func (e *editor) left() {
	if e.cur > 0 {
		e.cur--
	}
}

func (e *editor) right() {
	if e.cur < len(e.buf) {
		e.cur++
	}
}

func (e *editor) wordLeft() int {
	i := e.cur
	for i > 0 && unicode.IsSpace(e.buf[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(e.buf[i-1]) {
		i--
	}
	return i
}

func (e *editor) wordRight() int {
	i := e.cur
	for i < len(e.buf) && unicode.IsSpace(e.buf[i]) {
		i++
	}
	for i < len(e.buf) && !unicode.IsSpace(e.buf[i]) {
		i++
	}
	return i
}

func (e *editor) lineStart() int {
	i := e.cur
	for i > 0 && e.buf[i-1] != '\n' {
		i--
	}
	return i
}

func (e *editor) lineEnd() int {
	i := e.cur
	for i < len(e.buf) && e.buf[i] != '\n' {
		i++
	}
	return i
}

// cut removes buf[a:b] into the kill ring.
func (e *editor) cut(a, b int) {
	if a > b {
		a, b = b, a
	}
	if a == b {
		return
	}
	e.kill = append([]rune{}, e.buf[a:b]...)
	e.buf = append(e.buf[:a], e.buf[b:]...)
	e.cur = a
}

func (e *editor) yank() { e.insert(string(e.kill)) }

// up moves the cursor a line up; at the first line it browses history.
func (e *editor) up() {
	if s := e.lineStart(); s > 0 {
		col := e.cur - s
		prevStart := s - 1
		for prevStart > 0 && e.buf[prevStart-1] != '\n' {
			prevStart--
		}
		e.cur = min(prevStart+col, s-1)
		return
	}
	e.histPrev()
}

func (e *editor) down() {
	if end := e.lineEnd(); end < len(e.buf) {
		col := e.cur - e.lineStart()
		nextStart := end + 1
		nextEnd := nextStart
		for nextEnd < len(e.buf) && e.buf[nextEnd] != '\n' {
			nextEnd++
		}
		e.cur = min(nextStart+col, nextEnd)
		return
	}
	e.histNext()
}

func (e *editor) histPrev() {
	if e.histAt == 0 {
		return
	}
	if e.histAt == len(e.hist) {
		e.draft = e.text()
	}
	e.histAt--
	e.set(e.hist[e.histAt])
}

func (e *editor) histNext() {
	if e.histAt >= len(e.hist) {
		return
	}
	e.histAt++
	if e.histAt == len(e.hist) {
		e.set(e.draft)
	} else {
		e.set(e.hist[e.histAt])
	}
}

func (e *editor) remember(s string) {
	if s == "" || (len(e.hist) > 0 && e.hist[len(e.hist)-1] == s) {
		e.histAt = len(e.hist)
		return
	}
	e.hist = append(e.hist, s)
	e.histAt = len(e.hist)
}

// layout wraps the buffer to width and returns the visual rows and the
// cursor's row and column.
func (e *editor) layout(width int) (rows []string, cy, cx int) {
	if width < 2 {
		width = 2
	}
	var row strings.Builder
	rowW := 0
	cy, cx = -1, 0
	for i, r := range e.buf {
		if i == e.cur {
			cy, cx = len(rows), rowW
		}
		if r == '\n' {
			rows = append(rows, row.String())
			row.Reset()
			rowW = 0
			continue
		}
		w := uniseg.StringWidth(string(r))
		if rowW+w > width {
			rows = append(rows, row.String())
			row.Reset()
			rowW = 0
			if i == e.cur {
				cy, cx = len(rows), 0
			}
		}
		row.WriteRune(r)
		rowW += w
	}
	if cy < 0 {
		if rowW >= width {
			rows = append(rows, row.String())
			row.Reset()
			rowW = 0
		}
		cy, cx = len(rows), rowW
	}
	rows = append(rows, row.String())
	return rows, cy, cx
}
