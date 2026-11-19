package editor

import (
	"slices"
	"strings"
	"unicode"
)

// Mode is the vim mode of the editor. ModeOff means vim keys are disabled
// and the editor only takes emacs keys.
type Mode int

const (
	ModeOff Mode = iota
	ModeInsert
	ModeNormal
	ModeVisual
	ModeVisualLine
)

func (m Mode) String() string {
	return [...]string{"", "insert", "normal", "visual", "v-line"}[m]
}

type snapshot struct {
	buf []rune
	cur int
}

type vimState struct {
	mode    Mode
	pending []rune // keys of an unfinished command, such as "2d"
	anchor  int    // the other end of a visual selection
	find    [2]rune

	undo, redo []snapshot
	base       *snapshot // the buffer before the command that entered insert mode

	// What insert mode started from, to recover the typed text for ".".
	insAt  int
	insBuf []rune
	change []rune // keys of the change being recorded
	last   struct {
		keys []rune
		text string
	}
}

// Motion kinds: an exclusive motion stops before its target, an inclusive
// one takes it, a linewise one takes whole lines.
const (
	exclusive = iota
	inclusive
	linewise
)

// SetVim turns vim keys on, starting in insert mode, or off.
func (e *Editor) SetVim(on bool) {
	e.vim = vimState{}
	if on {
		e.startInsert()
	}
}

// Mode reports the vim mode, ModeOff when vim keys are disabled.
func (e *Editor) Mode() Mode { return e.vim.mode }

// Pending returns the keys of an unfinished normal mode command.
func (e *Editor) Pending() string { return string(e.vim.pending) }

func (e *Editor) snap() snapshot { return snapshot{slices.Clone(e.Buf), e.Cur} }

func (e *Editor) restore(s snapshot) {
	e.Buf = slices.Clone(s.buf)
	e.Cur = min(s.cur, len(e.Buf))
}

func (e *Editor) pushUndo(s snapshot) {
	e.vim.undo = append(e.vim.undo, s)
	e.vim.redo = nil
}

func (e *Editor) startInsert() {
	v := &e.vim
	v.mode = ModeInsert
	s := e.snap()
	v.base = &s
	v.insAt = e.Cur
	v.insBuf = s.buf
}

// VimEscape handles esc in vim mode: insert and visual mode return to
// normal mode and a pending command is dropped. It reports false when
// there was nothing to leave, so esc can do its usual job.
func (e *Editor) VimEscape() bool {
	v := &e.vim
	switch v.mode {
	case ModeInsert:
		e.finishInsert()
		v.mode = ModeNormal
		if e.Cur > e.lineStart(e.Cur) {
			e.Cur--
		}
		return true
	case ModeVisual, ModeVisualLine:
		v.mode, v.pending = ModeNormal, nil
		e.VimClamp()
		return true
	case ModeNormal:
		if len(v.pending) > 0 {
			v.pending = nil
			return true
		}
	}
	return false
}

func (e *Editor) finishInsert() {
	v := &e.vim
	if v.base != nil && !slices.Equal(v.base.buf, e.Buf) {
		e.pushUndo(*v.base)
	}
	v.base = nil
	if v.change == nil {
		return
	}
	// The typed text is what sits between the unchanged prefix and suffix.
	text := ""
	end := len(e.Buf) - (len(v.insBuf) - v.insAt)
	if end >= v.insAt && e.Cur == end && slices.Equal(e.Buf[:v.insAt], v.insBuf[:v.insAt]) && slices.Equal(e.Buf[end:], v.insBuf[v.insAt:]) {
		text = string(e.Buf[v.insAt:end])
	}
	v.last.keys, v.last.text = v.change, text
	v.change = nil
}

// VimClamp keeps the cursor on a character in normal and visual mode,
// where it cannot sit after the end of a line.
func (e *Editor) VimClamp() {
	if e.vim.mode < ModeNormal {
		return
	}
	e.Cur = min(e.Cur, len(e.Buf))
	if e.Cur == e.lineEnd(e.Cur) && e.Cur > e.lineStart(e.Cur) {
		e.Cur--
	}
}

// Selection returns the visual selection as Buf[a:b].
func (e *Editor) Selection() (a, b int, ok bool) {
	v := &e.vim
	lo, hi := min(v.anchor, e.Cur), max(v.anchor, e.Cur)
	switch v.mode {
	case ModeVisual:
		return lo, min(hi+1, len(e.Buf)), true
	case ModeVisualLine:
		return e.lineStart(lo), e.lineEnd(hi), true
	}
	return 0, 0, false
}

// VimRedo redoes the last undone change.
func (e *Editor) VimRedo() {
	v := &e.vim
	if v.mode < ModeNormal || len(v.redo) == 0 {
		return
	}
	v.undo = append(v.undo, e.snap())
	e.restore(v.redo[len(v.redo)-1])
	v.redo = v.redo[:len(v.redo)-1]
	e.VimClamp()
}

func (e *Editor) undoStep() {
	v := &e.vim
	if len(v.undo) == 0 {
		return
	}
	v.redo = append(v.redo, e.snap())
	e.restore(v.undo[len(v.undo)-1])
	v.undo = v.undo[:len(v.undo)-1]
}

// VimKey handles a typed character in normal or visual mode.
func (e *Editor) VimKey(r rune) {
	v := &e.vim
	if v.mode < ModeNormal {
		return
	}
	v.pending = append(v.pending, r)
	c, st := parseVim(v.pending, v.mode != ModeNormal)
	if st == parseMore {
		return
	}
	keys := v.pending
	v.pending = nil
	if st == parseBad {
		return
	}
	before := e.snap()
	record := v.mode == ModeNormal && c.isChange()
	if record {
		v.change = keys
	}
	e.exec(c)
	if v.mode == ModeInsert {
		// The undo point and the text for "." are taken at esc.
		v.base = &before
		return
	}
	if c.key != "u" && !slices.Equal(before.buf, e.Buf) {
		e.pushUndo(before)
	}
	if record {
		v.last.keys, v.last.text = v.change, ""
		v.change = nil
	}
	e.VimClamp()
}

// --- parsing ------------------------------------------------------------

type vimCmd struct {
	count int    // 0 when none was typed
	op    rune   // d, c or y; 0 for none
	key   string // motion, text object or command: "w", "gg", "iw", "x"
	arg   rune   // the character after f, t, F, T or r
}

const (
	parseMore = iota
	parseDone
	parseBad
)

func (c vimCmd) isChange() bool {
	return c.op == 'd' || c.op == 'c' || strings.Contains("xXDCsSpPr~JoOiaIA", c.key) && len(c.key) == 1
}

// parseVim parses the keys of a normal or visual mode command.
func parseVim(keys []rune, visual bool) (vimCmd, int) {
	var c vimCmd
	i := 0
	count := func() int {
		n := 0
		for i < len(keys) && (keys[i] >= '1' && keys[i] <= '9' || n > 0 && keys[i] == '0') {
			n = n*10 + int(keys[i]-'0')
			i++
		}
		return n
	}
	n1 := count()
	if i == len(keys) {
		return c, parseMore
	}
	n2 := 0
	if !visual && strings.ContainsRune("dcy", keys[i]) {
		c.op = keys[i]
		i++
		n2 = count()
		if i == len(keys) {
			return c, parseMore
		}
	}
	if n1 > 0 || n2 > 0 {
		c.count = max(n1, 1) * max(n2, 1)
	}
	k := keys[i]
	i++
	c.key = string(k)
	need := 0 // keys still to come
	switch {
	case k == c.op:
		// dd, cc, yy
	case k == 'g':
		need = 1
	case strings.ContainsRune("fFtT", k), k == 'r' && c.op == 0:
		need = 1
	case (k == 'i' || k == 'a') && (c.op != 0 || visual):
		need = 1
	}
	if len(keys)-i < need {
		return c, parseMore
	}
	if len(keys)-i > need {
		return c, parseBad
	}
	if need == 0 {
		if c.op != 0 && k != c.op && !motions[c.key] {
			return c, parseBad
		}
		return c, parseDone
	}
	next := keys[i]
	switch {
	case k == 'g':
		c.key = "g" + string(next)
		if !motions[c.key] {
			return c, parseBad
		}
	case k == 'i' || k == 'a':
		c.key += string(next)
		if !strings.ContainsRune("wW\"'`()b[]{}B<>", next) {
			return c, parseBad
		}
	default:
		c.arg = next
	}
	return c, parseDone
}

var motions = map[string]bool{
	"h": true, "l": true, "j": true, "k": true, " ": true, "w": true, "W": true, "b": true, "B": true,
	"e": true, "E": true, "ge": true, "gE": true, "0": true, "^": true, "$": true, "f": true, "F": true,
	"t": true, "T": true, ";": true, ",": true, "G": true, "gg": true,
}

func isTextObject(key string) bool { return len(key) == 2 && (key[0] == 'i' || key[0] == 'a') }

// --- execution ----------------------------------------------------------

func (e *Editor) exec(c vimCmd) {
	v := &e.vim
	n := max(c.count, 1)
	if v.mode == ModeVisual || v.mode == ModeVisualLine {
		e.execVisual(c, n)
		return
	}
	cur := e.Cur
	ls, le := e.lineStart(cur), e.lineEnd(cur)
	if c.op != 0 {
		switch {
		case c.key == string(c.op):
			to, ok := e.lineMove(cur, n-1)
			if ok {
				e.operate(c.op, cur, to, linewise)
			}
		case isTextObject(c.key):
			if a, b, ok := e.textObject(c.key, n); ok {
				e.operate(c.op, a, b, exclusive)
			}
		default:
			if to, kind, ok := e.motion(c, n); ok {
				e.operate(c.op, cur, to, kind)
			}
		}
		return
	}
	switch c.key {
	case "j", "k":
		// Past the first or last line these browse history, like Up and Down.
		for range n {
			if c.key == "j" {
				e.Down()
			} else {
				e.Up()
			}
		}
		return
	}
	if motions[c.key] {
		if to, _, ok := e.motion(c, n); ok {
			e.Cur = to
			if c.key == "G" || c.key == "gg" {
				e.Cur = e.firstNonBlank(e.lineStart(to))
			}
		}
		return
	}
	switch c.key {
	case "i":
		e.startInsert()
	case "a":
		if cur < le {
			e.Cur++
		}
		e.startInsert()
	case "I":
		e.Cur = e.firstNonBlank(ls)
		e.startInsert()
	case "A":
		e.Cur = le
		e.startInsert()
	case "o":
		e.Cur = le
		e.Insert("\n")
		e.startInsert()
	case "O":
		e.Cur = ls
		e.Insert("\n")
		e.Cur = ls
		e.startInsert()
	case "x":
		if cur < le {
			e.operate('d', cur, min(cur+n, le), exclusive)
		}
	case "X":
		if cur > ls {
			e.operate('d', max(ls, cur-n), cur, exclusive)
		}
	case "D":
		e.operate('d', cur, le, exclusive)
	case "C":
		e.operate('c', cur, le, exclusive)
	case "s":
		e.operate('c', cur, min(cur+n, le), exclusive)
	case "S":
		to, _ := e.lineMove(cur, n-1)
		e.operate('c', cur, to, linewise)
	case "Y":
		to, _ := e.lineMove(cur, n-1)
		e.operate('y', cur, to, linewise)
	case "p", "P":
		e.put(c.key == "p", n)
	case "r":
		if cur+n <= le {
			for i := cur; i < cur+n; i++ {
				e.Buf[i] = c.arg
			}
			e.Cur = cur + n - 1
		}
	case "~":
		end := min(cur+n, le)
		toggleCase(e.Buf[cur:end])
		e.Cur = end
	case "J":
		e.join(max(n, 2) - 1)
	case "u":
		for range n {
			e.undoStep()
		}
	case ".":
		e.repeat(n)
	case "v":
		v.mode, v.anchor = ModeVisual, cur
	case "V":
		v.mode, v.anchor = ModeVisualLine, cur
	}
}

func (e *Editor) execVisual(c vimCmd, n int) {
	v := &e.vim
	lo, hi := min(v.anchor, e.Cur), max(v.anchor, e.Cur)
	kind := inclusive
	if v.mode == ModeVisualLine {
		kind = linewise
	}
	op := func(op rune, kind int) {
		v.mode = ModeNormal
		e.operate(op, lo, hi, kind)
	}
	switch {
	case isTextObject(c.key):
		if a, b, ok := e.textObject(c.key, n); ok && b > a {
			v.anchor, e.Cur = a, b-1
		}
		return
	case motions[c.key]:
		if to, _, ok := e.motion(c, n); ok {
			e.Cur = to
		}
		return
	}
	switch c.key {
	case "d", "x":
		op('d', kind)
	case "X", "D":
		op('d', linewise)
	case "c", "s":
		op('c', kind)
	case "C", "S", "R":
		op('c', linewise)
	case "y":
		op('y', kind)
	case "Y":
		op('y', linewise)
	case "p", "P":
		reg, lines := e.kill, e.killLines
		op('d', kind)
		e.kill, e.killLines = reg, lines
		e.put(false, 1)
	case "~", "u", "U":
		a, b, _ := e.Selection()
		switch c.key {
		case "~":
			toggleCase(e.Buf[a:b])
		case "u":
			copy(e.Buf[a:b], []rune(strings.ToLower(string(e.Buf[a:b]))))
		case "U":
			copy(e.Buf[a:b], []rune(strings.ToUpper(string(e.Buf[a:b]))))
		}
		v.mode, e.Cur = ModeNormal, a
	case "r":
		a, b, _ := e.Selection()
		for i := a; i < b; i++ {
			if e.Buf[i] != '\n' {
				e.Buf[i] = c.arg
			}
		}
		v.mode, e.Cur = ModeNormal, a
	case "J":
		e.Cur = lo
		e.join(max(e.lineOf(hi)-e.lineOf(lo), 1))
		v.mode = ModeNormal
	case "o":
		v.anchor, e.Cur = e.Cur, v.anchor
	case "v", "V":
		m := map[string]Mode{"v": ModeVisual, "V": ModeVisualLine}[c.key]
		if v.mode == m {
			v.mode = ModeNormal
		} else {
			v.mode = m
		}
	}
}

// operate applies d, c or y to the text between from and to.
func (e *Editor) operate(op rune, from, to, kind int) {
	a, b := min(from, to), max(from, to)
	if kind == inclusive {
		b = min(b+1, len(e.Buf))
	}
	if kind == linewise {
		a, b = e.lineStart(a), e.lineEnd(b)
		e.setReg(string(e.Buf[a:b])+"\n", true)
		switch op {
		case 'y':
			e.Cur = min(from, to)
		case 'c':
			e.Buf = slices.Delete(e.Buf, a, b)
			e.Cur = a
			e.startInsert()
		case 'd':
			if b < len(e.Buf) {
				b++
			} else if a > 0 {
				a--
			}
			e.Buf = slices.Delete(e.Buf, a, b)
			e.Cur = e.firstNonBlank(e.lineStart(min(a, len(e.Buf))))
		}
		return
	}
	if a < b {
		e.setReg(string(e.Buf[a:b]), false)
	}
	switch op {
	case 'y':
		e.Cur = a
	case 'd':
		e.Buf = slices.Delete(e.Buf, a, b)
		e.Cur = a
	case 'c':
		e.Buf = slices.Delete(e.Buf, a, b)
		e.Cur = a
		e.startInsert()
	}
}

func (e *Editor) setReg(s string, lines bool) {
	e.kill, e.killLines = []rune(s), lines
}

// put pastes the register n times after or before the cursor, or below or
// above the line for a linewise register.
func (e *Editor) put(after bool, n int) {
	if len(e.kill) == 0 {
		return
	}
	text := strings.Repeat(string(e.kill), n)
	if e.killLines {
		var at int
		if !after {
			at = e.lineStart(e.Cur)
		} else if le := e.lineEnd(e.Cur); le < len(e.Buf) {
			at = le + 1
		} else {
			// Below the last line the newline goes in front instead.
			at = le
			text = "\n" + strings.TrimSuffix(text, "\n")
		}
		e.Cur = at
		e.Insert(text)
		if text[0] == '\n' {
			at++
		}
		e.Cur = e.firstNonBlank(at)
		return
	}
	at := e.Cur
	if after && at < e.lineEnd(at) {
		at++
	}
	e.Cur = at
	e.Insert(text)
	e.Cur--
}

// repeat replays the last change n times, for ".".
func (e *Editor) repeat(n int) {
	v := &e.vim
	if v.last.keys == nil {
		return
	}
	for range n {
		c, st := parseVim(v.last.keys, false)
		if st != parseDone {
			return
		}
		e.exec(c)
		if v.mode == ModeInsert {
			e.Insert(v.last.text)
			v.mode, v.base = ModeNormal, nil
			if e.Cur > e.lineStart(e.Cur) {
				e.Cur--
			}
		}
	}
}

// join joins count lines below onto the cursor's line, as J does.
func (e *Editor) join(count int) {
	for range count {
		le := e.lineEnd(e.Cur)
		if le >= len(e.Buf) {
			return
		}
		j := le + 1
		for j < len(e.Buf) && isBlank(e.Buf[j]) {
			j++
		}
		sep := []rune{' '}
		if j == len(e.Buf) || e.Buf[j] == '\n' || e.Buf[j] == ')' || le > e.lineStart(le) && isBlank(e.Buf[le-1]) || le == e.lineStart(le) {
			sep = nil
		}
		e.Buf = slices.Replace(e.Buf, le, j, sep...)
		e.Cur = le
	}
}

func toggleCase(rs []rune) {
	for i, r := range rs {
		if unicode.IsUpper(r) {
			rs[i] = unicode.ToLower(r)
		} else {
			rs[i] = unicode.ToUpper(r)
		}
	}
}

// --- motions ------------------------------------------------------------

// motion returns where a motion from the cursor lands and its kind.
func (e *Editor) motion(c vimCmd, n int) (int, int, bool) {
	cur := e.Cur
	ls, le := e.lineStart(cur), e.lineEnd(cur)
	switch c.key {
	case "h":
		return max(ls, cur-n), exclusive, cur > ls
	case "l", " ":
		return min(le, cur+n), exclusive, cur < le
	case "0":
		return ls, exclusive, true
	case "^":
		return e.firstNonBlank(ls), exclusive, true
	case "$":
		return le, exclusive, true
	case "j", "k":
		d := n
		if c.key == "k" {
			d = -n
		}
		to, ok := e.lineMove(cur, d)
		return to, linewise, ok
	case "G", "gg":
		line := e.lineOf(len(e.Buf))
		if c.key == "gg" {
			line = 0
		}
		if c.count > 0 {
			line = c.count - 1
		}
		return e.lineIndex(line), linewise, true
	case "w", "W":
		big := c.key == "W"
		if c.op == 'c' && cur < len(e.Buf) && e.class(cur, big) != 0 {
			// cw changes to the end of the word, like ce.
			p := cur - 1
			for range n {
				p = e.wordEnd(p, big)
			}
			return p, inclusive, true
		}
		p := cur
		for range n {
			p = e.wordNext(p, big)
		}
		if c.op != 0 && p > le && cur < le {
			// An operator stops at the end of the line, as in vim.
			p = le
		}
		return p, exclusive, p != cur
	case "b", "B":
		p := cur
		for range n {
			p = e.wordPrev(p, c.key == "B")
		}
		return p, exclusive, p != cur
	case "e", "E":
		p := cur
		for range n {
			p = e.wordEnd(p, c.key == "E")
		}
		return p, inclusive, p != cur && p < len(e.Buf)
	case "ge", "gE":
		p := cur
		for range n {
			p = e.wordEndPrev(p, c.key == "gE")
		}
		return p, inclusive, p != cur
	case "f", "F", "t", "T":
		e.vim.find = [2]rune{rune(c.key[0]), c.arg}
		return e.findChar(e.vim.find[0], c.arg, n)
	case ";", ",":
		k, ch := e.vim.find[0], e.vim.find[1]
		if k == 0 {
			return cur, exclusive, false
		}
		if c.key == "," {
			k = map[rune]rune{'f': 'F', 'F': 'f', 't': 'T', 'T': 't'}[k]
		}
		return e.findChar(k, ch, n)
	}
	return cur, exclusive, false
}

func (e *Editor) findChar(kind, ch rune, n int) (int, int, bool) {
	cur := e.Cur
	ls, le := e.lineStart(cur), e.lineEnd(cur)
	i := cur
	if kind == 'f' || kind == 't' {
		if kind == 't' && i+1 < le && e.Buf[i+1] == ch {
			i++ // so ";" after t moves past the target
		}
		for range n {
			i++
			for i < le && e.Buf[i] != ch {
				i++
			}
			if i >= le {
				return cur, inclusive, false
			}
		}
		if kind == 't' {
			i--
		}
		return i, inclusive, true
	}
	if kind == 'T' && i-1 > ls && e.Buf[i-1] == ch {
		i--
	}
	for range n {
		i--
		for i >= ls && e.Buf[i] != ch {
			i--
		}
		if i < ls {
			return cur, exclusive, false
		}
	}
	if kind == 'T' {
		i++
	}
	return i, exclusive, true
}

// class sorts runes for word motions: 0 blank, 1 word characters, 2 other
// punctuation. For WORD motions (big) everything not blank is 1.
func (e *Editor) class(i int, big bool) int {
	r := e.Buf[i]
	switch {
	case unicode.IsSpace(r):
		return 0
	case big, r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
		return 1
	}
	return 2
}

func (e *Editor) emptyLine(i int) bool {
	return i < len(e.Buf) && e.Buf[i] == '\n' && (i == 0 || e.Buf[i-1] == '\n')
}

func (e *Editor) wordNext(i int, big bool) int {
	n := len(e.Buf)
	if i >= n {
		return n
	}
	if c := e.class(i, big); c != 0 {
		for i < n && e.class(i, big) == c {
			i++
		}
	}
	for i < n && e.class(i, big) == 0 {
		i++
		if e.emptyLine(i) {
			return i
		}
	}
	return i
}

func (e *Editor) wordEnd(i int, big bool) int {
	n := len(e.Buf)
	i++
	for i < n && e.class(i, big) == 0 {
		i++
	}
	if i >= n {
		return max(n-1, 0)
	}
	c := e.class(i, big)
	for i+1 < n && e.class(i+1, big) == c {
		i++
	}
	return i
}

func (e *Editor) wordPrev(i int, big bool) int {
	if i == 0 {
		return 0
	}
	i--
	for i > 0 && e.class(i, big) == 0 {
		if e.emptyLine(i) {
			return i
		}
		i--
	}
	if c := e.class(i, big); c != 0 {
		for i > 0 && e.class(i-1, big) == c {
			i--
		}
	}
	return i
}

func (e *Editor) wordEndPrev(i int, big bool) int {
	if i >= len(e.Buf) {
		i = len(e.Buf) - 1
	} else if c := e.class(i, big); c != 0 {
		for i >= 0 && e.class(i, big) == c {
			i--
		}
	}
	for i > 0 && e.class(i, big) == 0 && !e.emptyLine(i) {
		i--
	}
	return max(i, 0)
}

// --- text objects -------------------------------------------------------

// textObject returns the range Buf[a:b] of iw, a", i( and the like.
func (e *Editor) textObject(key string, n int) (int, int, bool) {
	inner := key[0] == 'i'
	switch k := rune(key[1]); k {
	case 'w', 'W':
		return e.wordObject(inner, k == 'W', n)
	case '"', '\'', '`':
		return e.quoteObject(inner, k)
	case '(', ')', 'b':
		return e.pairObject(inner, '(', ')', n)
	case '[', ']':
		return e.pairObject(inner, '[', ']', n)
	case '{', '}', 'B':
		return e.pairObject(inner, '{', '}', n)
	case '<', '>':
		return e.pairObject(inner, '<', '>', n)
	}
	return 0, 0, false
}

func (e *Editor) wordObject(inner, big bool, n int) (int, int, bool) {
	i := min(e.Cur, len(e.Buf)-1)
	if i < 0 || e.Buf[i] == '\n' {
		return 0, 0, false
	}
	run := func(c int, j int) int {
		for j < len(e.Buf) && e.Buf[j] != '\n' && e.class(j, big) == c {
			j++
		}
		return j
	}
	c := e.class(i, big)
	a := i
	for a > 0 && e.Buf[a-1] != '\n' && e.class(a-1, big) == c {
		a--
	}
	b := run(c, i)
	for k := 1; k < n && b < len(e.Buf) && e.Buf[b] != '\n'; k++ {
		b = run(e.class(b, big), b)
	}
	if inner {
		return a, b, true
	}
	if c == 0 {
		if b < len(e.Buf) && e.Buf[b] != '\n' {
			b = run(e.class(b, big), b)
		}
		return a, b, true
	}
	if t := run(0, b); t > b {
		return a, t, true
	}
	for a > 0 && isBlank(e.Buf[a-1]) {
		a--
	}
	return a, b, true
}

func (e *Editor) quoteObject(inner bool, q rune) (int, int, bool) {
	cur := e.Cur
	ls, le := e.lineStart(cur), e.lineEnd(cur)
	var qs []int
	for i := ls; i < le; i++ {
		if e.Buf[i] == q && (i == ls || e.Buf[i-1] != '\\') {
			qs = append(qs, i)
		}
	}
	for j := 0; j+1 < len(qs); j += 2 {
		if qs[j+1] < cur {
			continue
		}
		a, b := qs[j], qs[j+1]
		if inner {
			return a + 1, b, true
		}
		b++
		for b < le && isBlank(e.Buf[b]) {
			b++
		}
		return a, b, true
	}
	return 0, 0, false
}

func (e *Editor) pairObject(inner bool, open, close rune, n int) (int, int, bool) {
	j := min(e.Cur, len(e.Buf)-1)
	if j < 0 {
		return 0, 0, false
	}
	if e.Buf[j] == close {
		j--
	}
	// Walk back to the n-th unmatched opening bracket.
	for found := 0; ; j-- {
		depth := 0
		for ; j >= 0; j-- {
			if e.Buf[j] == close {
				depth++
			} else if e.Buf[j] == open {
				if depth == 0 {
					break
				}
				depth--
			}
		}
		if j < 0 {
			return 0, 0, false
		}
		if found++; found == n {
			break
		}
	}
	a, depth := j, 0
	for b := a + 1; b < len(e.Buf); b++ {
		switch e.Buf[b] {
		case open:
			depth++
		case close:
			if depth > 0 {
				depth--
				continue
			}
			if inner {
				return a + 1, b, true
			}
			return a, b + 1, true
		}
	}
	return 0, 0, false
}

// --- lines --------------------------------------------------------------

func isBlank(r rune) bool { return r == ' ' || r == '\t' }

func (e *Editor) lineStart(i int) int {
	for i > 0 && e.Buf[i-1] != '\n' {
		i--
	}
	return i
}

func (e *Editor) lineEnd(i int) int {
	for i < len(e.Buf) && e.Buf[i] != '\n' {
		i++
	}
	return i
}

func (e *Editor) firstNonBlank(ls int) int {
	for ls < len(e.Buf) && isBlank(e.Buf[ls]) {
		ls++
	}
	return ls
}

// lineOf returns the 0-based line number of index i.
func (e *Editor) lineOf(i int) int {
	return strings.Count(string(e.Buf[:min(i, len(e.Buf))]), "\n")
}

// lineIndex returns the start of line n, or of the last line.
func (e *Editor) lineIndex(n int) int {
	i := 0
	for ; n > 0; n-- {
		end := e.lineEnd(i)
		if end == len(e.Buf) {
			break
		}
		i = end + 1
	}
	return i
}

// lineMove returns the position d lines below (or above, for negative d)
// the cursor in the same column, and false when there is no such line.
func (e *Editor) lineMove(i, d int) (int, bool) {
	line := e.lineOf(i) + d
	if line < 0 || line > e.lineOf(len(e.Buf)) {
		return i, false
	}
	s := e.lineIndex(line)
	return min(s+i-e.lineStart(i), e.lineEnd(s)), true
}
