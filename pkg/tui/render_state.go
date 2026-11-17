package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// renderState is the rendering features' state. There is one UI, so it is
// a package-level value rather than a field on UI.
type renderState struct {
	dragMouse bool

	// Rebuilt on every drawTranscript.
	view  viewGeom
	lines []line // the whole rendered transcript (shared with block caches)
	codes []codeEntry

	sel    selection
	search searchState
	panel  panelState
}

var rs renderState

type viewGeom struct {
	top, bottom   int // screen rows of the transcript area
	x, w          int // first column and right edge
	start, end    int // visible range of rs.lines
	height, total int
}

type codeEntry struct {
	line int // index into rs.lines of the header
	ref  codeRef
	num  int // hotkey number when visible, else 0
}

type pos struct{ line, col int }

func (p pos) less(q pos) bool { return p.line < q.line || p.line == q.line && p.col < q.col }

type selection struct {
	active, dragging bool
	a, b             pos
}

const maxCopyBytes = 1 << 20

func (r *renderState) codeAt(li int) *codeEntry {
	for i := range r.codes {
		if r.codes[i].line == li {
			return &r.codes[i]
		}
	}
	return nil
}

// --- cells ---------------------------------------------------------------

// cells splits a rendered line into one string per screen cell. The second
// cell of a wide grapheme is "".
func cells(l line) []string {
	var out []string
	for _, s := range l {
		state := -1
		rest := s.text
		for rest != "" {
			var g string
			var w int
			g, rest, w, state = uniseg.FirstGraphemeClusterInString(rest, state)
			if w == 0 {
				continue
			}
			out = append(out, g)
			for i := 1; i < w; i++ {
				out = append(out, "")
			}
		}
	}
	return out
}

// gutterCells is how many leading cells of l are decoration (code block,
// quote, thinking or tool-output gutters) that a copy should leave out.
func gutterCells(l line) int {
	if len(l) == 0 {
		return 0
	}
	switch strings.TrimRight(l[0].text, " ") {
	case "│", "┊", "▎", "  │":
		return len(cells(line{l[0]}))
	}
	return 0
}

// selectedText returns the text between a and b (cell positions, b
// exclusive on its line), trailing spaces trimmed per line.
func selectedText(lines []line, a, b pos) string {
	if b.less(a) {
		a, b = b, a
	}
	var sb strings.Builder
	for li := max(0, a.line); li <= b.line && li < len(lines); li++ {
		cs := cells(lines[li])
		skip := gutterCells(lines[li])
		from, to := skip, len(cs)
		if li == a.line {
			from = max(skip, min(a.col, len(cs)))
		}
		if li == b.line {
			to = min(b.col+1, len(cs))
		}
		var lb strings.Builder
		for _, c := range cs[from:max(from, to)] {
			lb.WriteString(c)
		}
		if li > a.line {
			sb.WriteByte('\n')
		}
		sb.WriteString(strings.TrimRight(lb.String(), " "))
		if sb.Len() > maxCopyBytes {
			break
		}
	}
	return sb.String()
}

// --- code blocks -----------------------------------------------------------

// numberCodes assigns hotkey numbers to closed code blocks whose header is
// on screen, newest first. With none on screen, all closed blocks count.
func numberCodes() []*codeEntry {
	var vis, all []*codeEntry
	for i := len(rs.codes) - 1; i >= 0; i-- {
		c := &rs.codes[i]
		c.num = 0
		if !c.ref.closed {
			continue
		}
		all = append(all, c)
		if c.line >= rs.view.start && c.line < rs.view.end {
			vis = append(vis, c)
		}
	}
	for i, c := range vis {
		if i < 9 {
			c.num = i + 1
		}
	}
	if len(vis) == 0 {
		return all
	}
	return vis
}

func (u *UI) copyCode(n int) {
	list := numberCodes()
	if n < 1 || n > len(list) {
		if len(list) == 0 {
			u.notify("no finished code blocks")
		} else {
			u.notify(fmt.Sprintf("no code block %d (have %d)", n, len(list)))
		}
		return
	}
	u.copyCodeEntry(list[n-1])
}

func (u *UI) copyCodeEntry(c *codeEntry) {
	text := c.ref.text
	if len(text) > maxCopyBytes {
		text = text[:maxCopyBytes]
	}
	u.sc.SetClipboard([]byte(text))
	what := "code"
	if c.ref.lang != "" {
		what = c.ref.lang
	}
	u.notify(fmt.Sprintf("copied %s block (%d lines)", what, strings.Count(text, "\n")+1))
}

// decorateTranscript draws overlays on the visible transcript: code block
// numbers, search hits and the mouse selection.
func (u *UI) decorateTranscript() {
	sc := u.sc
	v := rs.view
	numberCodes()
	for _, c := range rs.codes {
		if c.num == 0 || c.line < v.start || c.line >= v.end {
			continue
		}
		y := v.top + c.line - v.start
		badge := fmt.Sprintf(" alt+%d ", c.num)
		x := v.x + rs.lines[c.line].width() - uniseg.StringWidth(copyLabel) - uniseg.StringWidth(badge)
		if x > v.x+12 {
			drawStr(sc, x, y, v.w, badge, stFaint)
		}
	}
	if rs.search.open {
		u.searchMatches()
		for i, m := range rs.search.matches {
			if m.line < v.start || m.line >= v.end {
				continue
			}
			cur := i == rs.search.cur
			restyle(sc, v.x+m.col, v.top+m.line-v.start, m.n, func(old tcell.Style) tcell.Style {
				if cur {
					return tcell.StyleDefault.Background(cYellow).Foreground(tcell.ColorBlack).Bold(true).Underline(tcell.UnderlineStyleDouble, tcell.ColorBlack)
				}
				// Other hits keep their colors, underlined in yellow on the selection tint.
				return old.Background(cSelBg).Underline(tcell.UnderlineStyleSolid, cYellow)
			})
		}
	}
	if rs.sel.active {
		a, b := rs.sel.a, rs.sel.b
		if b.less(a) {
			a, b = b, a
		}
		for li := max(a.line, v.start); li <= b.line && li < v.end; li++ {
			from, to := 0, v.w-v.x
			if li == a.line {
				from = a.col
			}
			if li == b.line {
				to = b.col + 1
			}
			restyle(sc, v.x+from, v.top+li-v.start, to-from, func(st tcell.Style) tcell.Style { return st.Background(cSelBg) })
		}
	}
}

func restyle(sc tcell.Screen, x, y, n int, f func(tcell.Style) tcell.Style) {
	for i := 0; i < n; i++ {
		r, comb, st, _ := sc.GetContent(x+i, y)
		sc.SetContent(x+i, y, r, comb, f(st))
	}
}

// --- search ------------------------------------------------------------------

type match struct{ line, col, n int }

type searchState struct {
	open, editing bool
	query         []rune
	cur           int
	matches       []match
	key           string // query/size the matches were computed for
}

const maxMatches = 2000

func (u *UI) openSearch(q string) {
	rs.search.open, rs.search.editing = true, true
	if q = strings.TrimSpace(q); q != "" {
		rs.search.query = []rune(q)
		rs.search.editing = false
		rs.search.key = ""
		u.searchMatches()
		u.jumpMatch(0, true)
	}
}

// searchMatches recomputes hits when the query or transcript changed.
func (u *UI) searchMatches() {
	q := strings.ToLower(string(rs.search.query))
	key := fmt.Sprintf("%s\x00%d\x00%d", q, len(rs.lines), rs.view.w)
	if key == rs.search.key {
		return
	}
	rs.search.key = key
	rs.search.matches = rs.search.matches[:0]
	if q == "" {
		return
	}
	for li, l := range rs.lines {
		var b strings.Builder
		for _, s := range l {
			b.WriteString(s.text)
		}
		text := strings.ToLower(b.String())
		off := 0
		for {
			i := strings.Index(text[off:], q)
			if i < 0 {
				break
			}
			col := uniseg.StringWidth(text[:off+i])
			rs.search.matches = append(rs.search.matches, match{li, col, uniseg.StringWidth(q)})
			off += i + len(q)
			if len(rs.search.matches) >= maxMatches {
				return
			}
		}
	}
	if rs.search.cur >= len(rs.search.matches) {
		rs.search.cur = max(0, len(rs.search.matches)-1)
	}
}

// jumpMatch moves to the next (dir 1) or previous (-1) match and scrolls it
// into view. fromBottom starts at the newest match.
func (u *UI) jumpMatch(dir int, fromBottom bool) {
	ms := rs.search.matches
	if len(ms) == 0 {
		u.notify("no matches")
		return
	}
	switch {
	case fromBottom:
		rs.search.cur = len(ms) - 1
	default:
		rs.search.cur = (rs.search.cur + dir + len(ms)) % len(ms)
	}
	m := ms[rs.search.cur]
	v := rs.view
	if m.line < v.start || m.line >= v.end || fromBottom {
		start := max(0, m.line-v.height/2)
		u.scroll = max(0, v.total-v.height-start)
	}
}

func (u *UI) searchKey(ev *tcell.EventKey) bool {
	s := &rs.search
	switch ev.Key() {
	case tcell.KeyEscape:
		*s = searchState{matches: s.matches[:0]}
		return true
	case tcell.KeyEnter:
		if s.editing {
			s.editing = false
			s.key = ""
			u.searchMatches()
			u.jumpMatch(0, true)
			return true
		}
		u.jumpMatch(-1, false)
		return true
	case tcell.KeyUp, tcell.KeyCtrlP:
		u.jumpMatch(-1, false)
		return true
	case tcell.KeyDown, tcell.KeyCtrlN:
		u.jumpMatch(1, false)
		return true
	}
	if s.editing {
		switch ev.Key() {
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if len(s.query) > 0 {
				s.query = s.query[:len(s.query)-1]
			}
			return true
		case tcell.KeyCtrlU:
			s.query = s.query[:0]
			return true
		case tcell.KeyRune:
			if ev.Modifiers()&tcell.ModAlt == 0 && len(s.query) < 200 {
				s.query = append(s.query, ev.Rune())
				// Live: show the newest match while typing.
				s.key = ""
				u.searchMatches()
				if len(s.matches) > 0 {
					u.jumpMatch(0, true)
				}
				return true
			}
		}
		return false
	}
	if ev.Key() == tcell.KeyRune && ev.Modifiers()&tcell.ModAlt == 0 && len(u.ed.Buf) == 0 {
		switch ev.Rune() {
		case 'n': // older (up the transcript), like less after a ? search
			u.jumpMatch(-1, false)
			return true
		case 'N':
			u.jumpMatch(1, false)
			return true
		case '/':
			s.editing = true
			return true
		}
	}
	return false
}

func (u *UI) drawSearchBar(y, w int) {
	sc := u.sc
	bg := tcell.StyleDefault.Background(cPanelBg).Foreground(cText)
	fill(sc, 0, y, w, bg)
	x := drawStr(sc, 1, y, w, "⌕ ", bg.Foreground(cAccent))
	x = drawStr(sc, x, y, w-30, string(rs.search.query), bg.Bold(true))
	if rs.search.editing {
		sc.SetContent(x, y, '▏', nil, bg.Foreground(cAccent))
	}
	info := "no matches"
	if n := len(rs.search.matches); n > 0 {
		info = fmt.Sprintf("%d/%d", rs.search.cur+1, n)
		if n >= maxMatches {
			info += "+"
		}
	}
	hint := "  enter next · esc close"
	if !rs.search.editing {
		hint = "  n/N enter ↑↓ · / edit · esc"
	}
	if len(rs.search.query) == 0 {
		info = "type to search"
	}
	r := info + hint
	drawStr(sc, max(x+2, w-uniseg.StringWidth(r)-1), y, w, r, bg.Foreground(cDim))
}
