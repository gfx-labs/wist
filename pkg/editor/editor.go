// Package editor is the multi-line prompt editor: a rune buffer with emacs
// style motions, an optional vim mode (vim.go), a one entry kill ring and
// prompt history.
package editor

import (
	"slices"
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

// Editor is the prompt input: a rune buffer with a cursor, emacs style
// motions, a kill ring of one entry and prompt history.
type Editor struct {
	Buf    []rune // the text
	Cur    int    // cursor index into Buf
	kill   []rune
	Hist   []string // submitted prompts, oldest first
	histAt int      // len(hist) when not browsing
	draft  string   // what was typed before browsing history

	// killLines marks a kill from a vim linewise command, which p pastes
	// as whole lines.
	killLines bool
	vim       vimState
}

// Text returns the buffer as a string.
func (e *Editor) Text() string { return string(e.Buf) }

// Set replaces the buffer and puts the cursor at the end.
func (e *Editor) Set(s string) {
	e.Buf = []rune(s)
	e.Cur = len(e.Buf)
}

// Clear empties the buffer and stops browsing history.
func (e *Editor) Clear() {
	e.Buf = e.Buf[:0]
	e.Cur = 0
	e.histAt = len(e.Hist)
	if v := &e.vim; v.mode != ModeOff {
		// A new prompt starts in insert mode, as in a shell's vi mode.
		v.pending, v.undo, v.redo, v.change = nil, nil, nil, nil
		e.startInsert()
	}
}

// Insert inserts s at the cursor.
func (e *Editor) Insert(s string) {
	r := []rune(s)
	e.Buf = append(e.Buf[:e.Cur], append(r, e.Buf[e.Cur:]...)...)
	e.Cur += len(r)
}

// Backspace deletes the rune before the cursor.
func (e *Editor) Backspace() {
	if e.Cur > 0 {
		e.Buf = append(e.Buf[:e.Cur-1], e.Buf[e.Cur:]...)
		e.Cur--
	}
}

// Delete deletes the rune under the cursor.
func (e *Editor) Delete() {
	if e.Cur < len(e.Buf) {
		e.Buf = append(e.Buf[:e.Cur], e.Buf[e.Cur+1:]...)
	}
}

// Left moves the cursor one rune left.
func (e *Editor) Left() {
	if e.Cur > 0 {
		e.Cur--
	}
}

// Right moves the cursor one rune right.
func (e *Editor) Right() {
	if e.Cur < len(e.Buf) {
		e.Cur++
	}
}

// WordLeft returns the start of the word before the cursor.
func (e *Editor) WordLeft() int {
	i := e.Cur
	for i > 0 && unicode.IsSpace(e.Buf[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(e.Buf[i-1]) {
		i--
	}
	return i
}

// WordRight returns the end of the word after the cursor.
func (e *Editor) WordRight() int {
	i := e.Cur
	for i < len(e.Buf) && unicode.IsSpace(e.Buf[i]) {
		i++
	}
	for i < len(e.Buf) && !unicode.IsSpace(e.Buf[i]) {
		i++
	}
	return i
}

// LineStart returns the start of the cursor's line.
func (e *Editor) LineStart() int {
	i := e.Cur
	for i > 0 && e.Buf[i-1] != '\n' {
		i--
	}
	return i
}

// LineEnd returns the end of the cursor's line.
func (e *Editor) LineEnd() int {
	i := e.Cur
	for i < len(e.Buf) && e.Buf[i] != '\n' {
		i++
	}
	return i
}

// Cut removes Buf[a:b] into the kill ring.
func (e *Editor) Cut(a, b int) {
	if a > b {
		a, b = b, a
	}
	if a == b {
		return
	}
	e.kill = append([]rune{}, e.Buf[a:b]...)
	e.killLines = false
	e.Buf = append(e.Buf[:a], e.Buf[b:]...)
	e.Cur = a
}

// Yank inserts the kill ring at the cursor.
func (e *Editor) Yank() { e.Insert(string(e.kill)) }

// Up moves the cursor a line up; at the first line it browses history.
func (e *Editor) Up() {
	if s := e.LineStart(); s > 0 {
		col := e.Cur - s
		prevStart := s - 1
		for prevStart > 0 && e.Buf[prevStart-1] != '\n' {
			prevStart--
		}
		e.Cur = min(prevStart+col, s-1)
		return
	}
	e.HistPrev()
}

// Down moves the cursor a line down; at the last line it browses history.
func (e *Editor) Down() {
	if end := e.LineEnd(); end < len(e.Buf) {
		col := e.Cur - e.LineStart()
		nextStart := end + 1
		nextEnd := nextStart
		for nextEnd < len(e.Buf) && e.Buf[nextEnd] != '\n' {
			nextEnd++
		}
		e.Cur = min(nextStart+col, nextEnd)
		return
	}
	e.HistNext()
}

// HistPrev shows the previous history entry.
func (e *Editor) HistPrev() {
	if e.histAt == 0 {
		return
	}
	if e.histAt == len(e.Hist) {
		e.draft = e.Text()
	}
	e.histAt--
	e.Set(e.Hist[e.histAt])
}

// HistNext shows the next history entry, or the draft after the last.
func (e *Editor) HistNext() {
	if e.histAt >= len(e.Hist) {
		return
	}
	e.histAt++
	if e.histAt == len(e.Hist) {
		e.Set(e.draft)
	} else {
		e.Set(e.Hist[e.histAt])
	}
}

// Remember adds s to the history, skipping a repeat of the last entry,
// and stops browsing.
func (e *Editor) Remember(s string) {
	if s == "" || (len(e.Hist) > 0 && e.Hist[len(e.Hist)-1] == s) {
		e.histAt = len(e.Hist)
		return
	}
	e.Hist = append(e.Hist, s)
	e.histAt = len(e.Hist)
}

// Layout wraps the buffer to width and returns the visual rows and the
// cursor's row and column.
func (e *Editor) Layout(width int) (rows []string, cy, cx int) {
	rows, pos := e.LayoutMarks(width, e.Cur)
	return rows, pos[0][0], pos[0][1]
}

// LayoutMarks wraps the buffer to width and returns the visual rows and
// the row and column of each buffer index in marks.
func (e *Editor) LayoutMarks(width int, marks ...int) (rows []string, pos [][2]int) {
	if width < 2 {
		width = 2
	}
	var row strings.Builder
	rowW := 0
	pos = make([][2]int, len(marks))
	placed := make([]bool, len(marks))
	mark := func(i int, wrapped bool) {
		for k, m := range marks {
			if m == i && (!placed[k] || wrapped) {
				pos[k], placed[k] = [2]int{len(rows), rowW}, true
			}
		}
	}
	for i, r := range e.Buf {
		mark(i, false)
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
			mark(i, true)
		}
		row.WriteRune(r)
		rowW += w
	}
	if slices.Contains(placed, false) {
		if rowW >= width {
			rows = append(rows, row.String())
			row.Reset()
			rowW = 0
		}
		for k := range marks {
			if !placed[k] {
				pos[k] = [2]int{len(rows), rowW}
			}
		}
	}
	rows = append(rows, row.String())
	return rows, pos
}
