package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	acp "github.com/gfx-labs/go-acp"
	"github.com/rivo/uniseg"
)

func (u *UI) draw() {
	sc := u.sc
	sc.Clear()
	w, h := sc.Size()
	if w < 20 || h < 8 {
		drawStr(sc, 0, 0, w, "terminal too small", stRed)
		sc.Show()
		return
	}

	// Bottom up: status, input, permission card, completion, then transcript.
	y := h - 1
	u.drawStatus(y, w)
	y--

	inputW := w - 4
	rows, cy, cx := u.ed.layout(inputW)
	maxRows := max(3, h/3)
	first := 0
	if len(rows) > maxRows {
		first = min(max(0, cy-maxRows+1), len(rows)-maxRows)
		rows = rows[first : first+maxRows]
	}
	inputTop := y - len(rows) // border line
	u.drawInput(inputTop, w, rows)
	if u.picker == nil {
		sc.ShowCursor(2+cx, inputTop+1+cy-first)
	} else {
		sc.HideCursor()
	}
	y = inputTop - 1

	if len(u.perms) > 0 {
		card := u.permLines(w - 2)
		top := max(1, y-len(card)+1)
		for i, l := range card {
			if top+i > y {
				break
			}
			fill(sc, 0, top+i, w, tcell.StyleDefault.Background(cPanelBg))
			drawLine(sc, 1, top+i, w-1, withBg(l, cPanelBg))
		}
		y = top - 1
	}

	// Plan panel on the right when wide enough, otherwise above the input.
	transcriptW := w
	if len(u.plan) > 0 && !u.planHidden {
		if w >= 110 {
			pw := min(44, w/3)
			transcriptW = w - pw - 1
			u.drawPlanSide(transcriptW+1, 1, pw, y)
		} else {
			pl := u.planLines(w - 2)
			pl = pl[:min(len(pl), 8)]
			top := y - len(pl) + 1
			for i, l := range pl {
				drawLine(sc, 1, top+i, w-1, l)
			}
			y = top - 1
		}
	}

	u.drawHeader(w)
	u.drawTranscript(1, y, transcriptW)

	if u.comp != nil && u.picker == nil {
		u.drawCompletion(inputTop, w)
	}
	if u.picker != nil {
		u.drawPicker(w, h)
	}
	sc.Show()
}

func withBg(l line, bg tcell.Color) line {
	out := make(line, len(l))
	for i, s := range l {
		st := s.st
		if _, b, _ := st.Decompose(); b == tcell.ColorDefault {
			st = st.Background(bg)
		}
		out[i] = span{s.text, st}
	}
	return out
}

func (u *UI) drawHeader(w int) {
	sc := u.sc
	l := line{{" towa ", tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(cAccent).Bold(true)}, {" ", stBase}}
	title := u.title
	if title == "" {
		title = "new session"
		if u.session == "" {
			title = "connecting…"
		}
	}
	l = append(l, span{title, stBase.Bold(true)})
	right := homeRel(u.opts.cwd)
	if br := gitBranch(u.opts.cwd); br != "" {
		right += "  " + br
	}
	x := drawLine(sc, 0, 0, w, l)
	rw := uniseg.StringWidth(right)
	if x+rw+2 < w {
		drawStr(sc, w-rw-1, 0, w, right, stDim)
	}
}

var branchCache struct {
	dir, branch string
	at          time.Time
}

// gitBranch reads HEAD directly so drawing never spawns a process.
func gitBranch(dir string) string {
	if branchCache.dir == dir && time.Since(branchCache.at) < 5*time.Second {
		return branchCache.branch
	}
	branchCache.dir, branchCache.at, branchCache.branch = dir, time.Now(), ""
	for d := dir; ; d = filepath.Dir(d) {
		head, err := readFile(filepath.Join(d, ".git", "HEAD"))
		if err == nil {
			s := strings.TrimSpace(head)
			if ref, ok := strings.CutPrefix(s, "ref: refs/heads/"); ok {
				branchCache.branch = "⎇ " + ref
			} else if len(s) >= 7 {
				branchCache.branch = "⎇ " + s[:7]
			}
			break
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	return branchCache.branch
}

func (u *UI) drawTranscript(top, bottom, w int) {
	height := bottom - top + 1
	if height <= 0 {
		return
	}
	cw := w - 2 // one column margin each side
	var all []line
	for i, b := range u.blocks {
		ls := b.lines(cw, u.expandAll, u.showThinking)
		if len(ls) == 0 {
			continue
		}
		if i > 0 && spaced(u.blocks[i-1], b) {
			all = append(all, line{})
		}
		all = append(all, ls...)
	}
	if u.busy {
		all = append(all, line{}, u.workingLine())
	}
	if len(u.blocks) == 0 && !u.busy {
		all = append(all, welcome(cw)...)
	}

	maxScroll := max(0, len(all)-height)
	u.scroll = min(u.scroll, maxScroll)
	start := max(0, len(all)-height-u.scroll)
	end := min(len(all), start+height)
	y := top
	if len(all) < height {
		y = bottom - len(all) + 1 // stick to the bottom like a chat
	}
	for _, l := range all[start:end] {
		drawLine(u.sc, 1, y, w-1, l)
		y++
	}
	if u.scroll > 0 {
		msg := fmt.Sprintf(" ↓ %d more lines (esc / pgdn) ", u.scroll)
		drawStr(u.sc, max(0, w-uniseg.StringWidth(msg)-1), bottom, w, msg, tcell.StyleDefault.Foreground(cText).Background(cSelBg))
	}
}

// spaced reports whether a blank line goes between a and b. Consecutive
// tool calls sit together, like jcode's compact tool list.
func spaced(a, b *block) bool {
	if a.kind == kindTool && b.kind == kindTool {
		return false
	}
	return true
}

func (u *UI) workingLine() line {
	el := time.Since(u.turnStart)
	verb := "working"
	running := 0
	for _, b := range u.tools {
		if b.status == acp.ToolCallStatusInProgress {
			running++
		}
	}
	if running > 0 {
		verb = fmt.Sprintf("running %d tool", running)
		if running > 1 {
			verb += "s"
		}
	} else if b := u.last(); b != nil && b.kind == kindThought {
		verb = "thinking"
	} else if b != nil && b.kind == kindAgent {
		verb = "writing"
	}
	l := line{{spinner() + " ", stAccent}, {verb + "…", stAccent.Italic(true)}, {" " + fmtDuration(el), stDim}, {"  esc to interrupt", stFaint}}
	if n := len(u.queue); n > 0 {
		l = append(l, span{fmt.Sprintf("  %d queued", n), stYellow})
	}
	return l
}

func welcome(w int) []line {
	logo := []string{
		"  ┌┬┐┌─┐┬ ┬┌─┐",
		"   │ │ ││││├─┤",
		"   ┴ └─┘└┴┘┴ ┴",
	}
	var out []line
	for _, s := range logo {
		out = append(out, line{{s, stAccent.Bold(true)}})
	}
	out = append(out, line{},
		line{{"  ask anything. ", stBase}, {"/help", stCode}, {" for keys and commands, ", stDim}, {"@file", stCode}, {" to attach.", stDim}},
	)
	_ = w
	return out
}

func (u *UI) drawInput(top, w int, rows []string) {
	sc := u.sc
	border := stFaint
	label := ""
	switch {
	case len(u.perms) > 0:
		border, label = stYellow, " permission needed "
	case u.busy:
		border, label = stAccent, " enter queues · esc interrupts "
	}
	sc.SetContent(0, top, '╭', nil, border)
	for x := 1; x < w-1; x++ {
		sc.SetContent(x, top, '─', nil, border)
	}
	sc.SetContent(w-1, top, '╮', nil, border)
	if label != "" {
		drawStr(sc, 2, top, w-2, label, border)
	}
	if mode := currentName(u.modeOption()); mode != "" {
		tag := " " + strings.ToLower(mode) + " "
		st := stDim
		if strings.EqualFold(mode, "auto") {
			st = stYellow
		}
		drawStr(sc, w-uniseg.StringWidth(tag)-2, top, w-1, tag, st)
	}
	for i, r := range rows {
		y := top + 1 + i
		prompt := "  "
		if i == 0 {
			prompt = "❯ "
		}
		drawStr(sc, 0, y, w, prompt, stUser.Bold(true))
		drawStr(sc, 2, y, w-1, r, stBase)
	}
	if len(u.ed.buf) == 0 {
		hint := "message towa…"
		if u.busy {
			hint = "type to queue a follow-up…"
		}
		drawStr(sc, 2, top+1, w-1, hint, stFaint.Italic(true))
	}
}

func (u *UI) drawStatus(y, w int) {
	sc := u.sc
	var l line
	if m := currentName(u.modelOption()); m != "" {
		l = append(l, span{"◆ " + m, stAccent})
	}
	if u.ctxSize > 0 {
		pct := float64(u.ctxUsed) / float64(u.ctxSize)
		st := stGreen
		switch {
		case pct > 0.85:
			st = stRed
		case pct > 0.6:
			st = stYellow
		}
		l = append(l, span{"  ", stDim}, span{meter(pct, 8), st}, span{fmt.Sprintf(" %s/%s", fmtTokens(u.ctxUsed), fmtTokens(u.ctxSize)), stDim})
	}
	if u.cost != nil && u.cost.Amount > 0 {
		l = append(l, span{fmt.Sprintf("  $%.2f", u.cost.Amount), stDim})
	}
	if u.turnUsage != nil {
		l = append(l, span{fmt.Sprintf("  last ↑%s ↓%s", fmtTokens(u.turnUsage.InputTokens), fmtTokens(u.turnUsage.OutputTokens)), stFaint})
	}
	drawLine(sc, 1, y, w, l)

	var right string
	var rst tcell.Style
	switch {
	case u.flash != "":
		right, rst = u.flash, stYellow
	case u.expandAll:
		right, rst = "expanded · alt+e", stDim
	default:
		right, rst = "/help", stFaint
	}
	rw := uniseg.StringWidth(right)
	if l.width()+rw+4 < w {
		drawStr(sc, w-rw-1, y, w, right, rst)
	}
}

func meter(p float64, n int) string {
	p = min(max(p, 0), 1)
	full := int(p * float64(n))
	return strings.Repeat("▰", full) + strings.Repeat("▱", n-full)
}

func (u *UI) planLines(w int) []line {
	done := 0
	for _, e := range u.plan {
		if e.Status == acp.PlanEntryStatusCompleted {
			done++
		}
	}
	out := []line{{{"plan ", stAccent.Bold(true)}, {fmt.Sprintf("%d/%d", done, len(u.plan)), stDim}}}
	for _, e := range u.plan {
		var mark span
		st := stBase
		switch e.Status {
		case acp.PlanEntryStatusCompleted:
			mark, st = span{"✓ ", stGreen}, stDim.StrikeThrough(true)
		case acp.PlanEntryStatusInProgress:
			mark, st = span{"▸ ", stYellow}, stBase.Bold(true)
		default:
			mark = span{"○ ", stDim}
		}
		out = append(out, wrap([]span{{clean(e.Content), st}}, w, line{mark}, line{{"  ", st}})...)
	}
	return out
}

func (u *UI) drawPlanSide(x, top, w, bottom int) {
	sc := u.sc
	for y := top; y <= bottom; y++ {
		sc.SetContent(x-1, y, '│', nil, stFaint)
	}
	for i, l := range u.planLines(w - 2) {
		if top+i > bottom {
			break
		}
		drawLine(sc, x+1, top+i, x+w, l)
	}
}

func (u *UI) permLines(w int) []line {
	p := u.perms[0]
	tc := p.req.ToolCall
	title := "tool call"
	if tc.Title != nil {
		title = *tc.Title
	}
	out := []line{{{"⚠ allow ", stYellow.Bold(true)}, {truncate(clean(title), w-10), stBase.Bold(true)}, {"?", stYellow.Bold(true)}}}
	// Show what the tool will do: its diff or input.
	b := &block{kind: kindTool, content: tc.Content}
	body := b.bodyLines(w - 4)
	if len(body) == 0 && tc.RawInput != nil {
		for _, r := range strings.Split(clean(formatRaw(tc.RawInput)), "\n") {
			body = append(body, wrap([]span{{r, stDim}}, w-4, nil, nil)...)
		}
	}
	for i, l := range body {
		if i == 10 {
			out = append(out, line{{"  … ", stFaint}})
			break
		}
		out = append(out, append(line{{"  ", stDim}}, l...))
	}
	var opts line
	for i, o := range p.req.Options {
		st := stDim
		if i == p.sel {
			st = tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(cAccent).Bold(true)
		}
		key := fmt.Sprintf("%d", i+1)
		switch o.Kind {
		case acp.PermissionOptionKindAllowOnce:
			key = "y"
		case acp.PermissionOptionKindAllowAlways:
			key = "a"
		case acp.PermissionOptionKindRejectOnce:
			key = "n"
		}
		opts = append(opts, span{" " + o.Name + " [" + key + "] ", st}, span{" ", stDim})
	}
	out = append(out, opts)
	if n := len(u.perms) - 1; n > 0 {
		out = append(out, line{{fmt.Sprintf("  +%d more waiting", n), stFaint}})
	}
	return out
}

func (u *UI) drawCompletion(inputTop, w int) {
	c := u.comp
	n := min(len(c.items), 8)
	first := max(0, c.sel-n+1)
	top := inputTop - n
	if top < 1 {
		return
	}
	lw := 0
	for _, it := range c.items {
		lw = max(lw, uniseg.StringWidth(it.label))
	}
	lw = min(lw, w/2)
	bg := tcell.StyleDefault.Background(cPanelBg)
	for i := 0; i < n; i++ {
		it := c.items[first+i]
		y := top + i
		st := bg.Foreground(cText)
		if first+i == c.sel {
			st = tcell.StyleDefault.Background(cSelBg).Foreground(cText).Bold(true)
		}
		fill(u.sc, 1, y, w-1, st)
		x := drawStr(u.sc, 2, y, w-1, truncate(it.label, lw), st.Foreground(cAccent))
		x = max(x, 2+lw) + 2
		drawStr(u.sc, x, y, w-2, truncate(it.detail, w-x-2), st.Foreground(cDim).Bold(false))
	}
}

func (u *UI) drawPicker(w, h int) {
	p := u.picker
	sc := u.sc
	pw := min(w-4, 90)
	ph := min(h-4, 20)
	x0 := (w - pw) / 2
	y0 := (h - ph) / 2
	bg := tcell.StyleDefault.Background(cPanelBg).Foreground(cText)
	border := bg.Foreground(cAccent)
	for y := y0; y < y0+ph; y++ {
		fill(sc, x0, y, x0+pw, bg)
		sc.SetContent(x0, y, '│', nil, border)
		sc.SetContent(x0+pw-1, y, '│', nil, border)
	}
	for x := x0; x < x0+pw; x++ {
		sc.SetContent(x, y0, '─', nil, border)
		sc.SetContent(x, y0+ph-1, '─', nil, border)
	}
	sc.SetContent(x0, y0, '╭', nil, border)
	sc.SetContent(x0+pw-1, y0, '╮', nil, border)
	sc.SetContent(x0, y0+ph-1, '╰', nil, border)
	sc.SetContent(x0+pw-1, y0+ph-1, '╯', nil, border)
	drawStr(sc, x0+2, y0, x0+pw-2, " "+p.title+" ", border.Bold(true))
	drawStr(sc, x0+pw-24, y0+ph-1, x0+pw-2, " ↑↓ enter esc ", border)

	fx := drawStr(sc, x0+2, y0+1, x0+pw-2, "⌕ ", bg.Foreground(cDim))
	fx = drawStr(sc, fx, y0+1, x0+pw-2, string(p.filter), bg.Bold(true))
	sc.SetContent(fx, y0+1, '▏', nil, bg.Foreground(cAccent))
	drawStr(sc, x0+pw-12, y0+1, x0+pw-2, fmt.Sprintf("%d/%d", len(p.shown), len(p.items)), bg.Foreground(cFaint))

	listTop := y0 + 2
	rows := ph - 3
	first := max(0, p.sel-rows+1)
	for i := 0; i < rows && first+i < len(p.shown); i++ {
		it := p.items[p.shown[first+i]]
		y := listTop + i
		st := bg
		if first+i == p.sel {
			st = tcell.StyleDefault.Background(cSelBg).Foreground(cText).Bold(true)
			fill(sc, x0+1, y, x0+pw-1, st)
		}
		mark := "  "
		if it.current {
			mark = "● "
		}
		x := drawStr(sc, x0+2, y, x0+pw-2, mark, st.Foreground(cGreen))
		lw := min(uniseg.StringWidth(it.label), pw/2)
		x = drawStr(sc, x, y, x0+pw-2, truncate(it.label, lw), st)
		drawStr(sc, x+2, y, x0+pw-2, truncate(it.detail, x0+pw-x-5), st.Foreground(cDim).Bold(false))
	}
}
