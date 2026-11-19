package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	acp "github.com/gfx-labs/go-acp"
	"github.com/gfx-labs/wist/pkg/editor"
	"github.com/gfx-labs/wist/pkg/render"
	"github.com/rivo/uniseg"
)

func (u *UI) draw() {
	sc := u.sc
	u.termInit()
	gfx.beginFrame(sc)
	sc.Clear()
	w, h := sc.Size()
	if w < 20 || h < 8 {
		render.DrawStr(sc, 0, 0, w, "terminal too small", render.StRed)
		sc.Show()
		return
	}

	// Bottom up: status, input, permission card, completion, then transcript.
	y := h - 1
	u.drawStatus(y, w)
	y--
	if !u.opts.NoMouse && !rs.dragMouse {
		rs.dragMouse = true
		sc.EnableMouse(tcell.MouseDragEvents)
	}

	inputW := w - 4
	marks := []int{u.ed.Cur}
	selA, selB, sel := u.ed.Selection()
	if sel {
		marks = append(marks, selA, selB)
	}
	all, pos := u.ed.LayoutMarks(inputW, marks...)
	cy, cx := pos[0][0], pos[0][1]
	rows := all
	maxRows := max(3, h/3)
	first := 0
	if len(rows) > maxRows {
		first = min(max(0, cy-maxRows+1), len(rows)-maxRows)
		rows = rows[first : first+maxRows]
	}
	inputTop := y - len(rows) // border line
	u.drawInput(inputTop, w, rows)
	if sel {
		u.drawSelection(inputTop+1-first, inputTop+1, inputTop+len(rows), w, all, pos[1], pos[2])
	}
	if u.picker == nil {
		u.cursorTint()
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
			render.Fill(sc, 0, top+i, w, tcell.StyleDefault.Background(render.CPanelBg))
			render.DrawLine(sc, 1, top+i, w-1, withBg(l, render.CPanelBg))
		}
		y = top - 1
	}

	if rs.search.open {
		u.drawSearchBar(y, w)
		y--
	}

	// Side panel (diagram / tool output) and the plan share the right side
	// when wide enough; otherwise the plan goes above the input.
	transcriptW := w
	if rs.panel.open && w >= 60 {
		pw := min(max(w*2/5, 40), w-30)
		transcriptW = w - pw - 1
		u.drawSidePanel(transcriptW+1, 1, pw, y)
	} else if len(u.plan) > 0 && !u.planHidden {
		if w >= 110 {
			pw := min(44, w/3)
			transcriptW = w - pw - 1
			u.drawPlanSide(transcriptW+1, 1, pw, y)
		} else {
			pl := u.planLines(w - 2)
			pl = pl[:min(len(pl), 8)]
			top := y - len(pl) + 1
			for i, l := range pl {
				render.DrawLine(sc, 1, top+i, w-1, l)
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
	gfx.endFrame()
}

func withBg(l render.Line, bg tcell.Color) render.Line {
	out := make(render.Line, len(l))
	for i, s := range l {
		st := s.St
		if _, b, _ := st.Decompose(); b == tcell.ColorDefault {
			st = st.Background(bg)
		}
		out[i] = render.Span{Text: s.Text, St: st}
	}
	return out
}

func (u *UI) drawHeader(w int) {
	sc := u.sc
	l := render.Line{{Text: " wist ", St: tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(render.CAccent).Bold(true)}, {Text: " ", St: render.StBase}}
	title := u.title
	if title == "" {
		title = "new session"
		if !u.connected {
			title = "connecting…"
		}
	}
	l = append(l, render.Span{Text: title, St: render.StBase.Bold(true)})
	agent := u.opts.Agent
	if agent == "" {
		agent = "towa"
	}
	l = append(l, render.Span{Text: "  " + agent, St: render.StDim})
	right := homeRel(u.opts.Cwd)
	if u.themeName != "" && u.themeName != "dark" {
		right = u.themeName + "  " + right
	}
	if br := gitBranch(u.opts.Cwd); br != "" {
		right += "  " + br
	}
	x := render.DrawLine(sc, 0, 0, w, l)
	rw := uniseg.StringWidth(right)
	if x+rw+2 < w {
		render.DrawStr(sc, w-rw-1, 0, w, right, render.StDim)
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
	var all []render.Line
	rs.codes = rs.codes[:0]
	for i, b := range u.blocks {
		ls := b.lines(cw, u.expandAll, u.showThinking)
		if len(ls) == 0 {
			continue
		}
		if i > 0 && spaced(u.blocks[i-1], b) {
			all = append(all, render.Line{})
		}
		for _, c := range b.codes {
			if b.kind == kindAgent {
				rs.codes = append(rs.codes, codeEntry{line: len(all) + c.Line, ref: c})
			}
		}
		all = append(all, ls...)
	}
	if u.busy {
		all = append(all, render.Line{}, u.workingLine())
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
	rs.view = viewGeom{top: y, bottom: bottom, x: 1, w: w - 1, start: start, end: end, height: height, total: len(all)}
	rs.lines = all
	for _, l := range all[start:end] {
		render.DrawLine(u.sc, 1, y, w-1, l)
		y++
	}
	u.decorateTranscript()
	if u.scroll > 0 {
		msg := fmt.Sprintf(" ↓ %d more lines (esc / pgdn) ", u.scroll)
		render.DrawStr(u.sc, max(0, w-uniseg.StringWidth(msg)-1), bottom, w, msg, tcell.StyleDefault.Foreground(render.CText).Background(render.CSelBg))
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

func (u *UI) workingLine() render.Line {
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
	if u.urgent {
		verb = "interrupting"
	}
	l := render.Line{{Text: spinner() + " ", St: render.StAccent}, {Text: verb + "…", St: render.StAccent.Italic(true)}, {Text: " " + fmtDuration(el), St: render.StDim}, {Text: "  esc to interrupt", St: render.StFaint}}
	if n := len(u.queue); n > 0 {
		l = append(l, render.Span{Text: fmt.Sprintf("  %d queued", n), St: render.StYellow})
	}
	return l
}

func welcome(w int) []render.Line {
	logo := []string{
		"  ┬ ┬┬┌─┐┌┬┐",
		"  │││││└─┐ │ ",
		"  └┴┘┴└─┘ ┴ ",
	}
	var out []render.Line
	for _, s := range logo {
		out = append(out, render.Line{{Text: s, St: render.StAccent.Bold(true)}})
	}
	out = append(out, render.Line{},
		render.Line{{Text: "  ask anything. ", St: render.StBase}, {Text: "/help", St: render.StCode}, {Text: " for keys and commands, ", St: render.StDim}, {Text: "@file", St: render.StCode}, {Text: " to attach.", St: render.StDim}},
	)
	_ = w
	return out
}

func (u *UI) drawInput(top, w int, rows []string) {
	sc := u.sc
	border := render.StFaint
	label := ""
	switch {
	case len(u.perms) > 0:
		border, label = render.StYellow, " permission needed "
	case u.urgent:
		border, label = render.StYellow, " interrupting… "
	case u.busy && u.ed.Mode() == editor.ModeInsert:
		border, label = render.StAccent, " enter queues · ctrl+c interrupts "
	case u.busy:
		border, label = render.StAccent, " enter queues · esc interrupts "
	}
	if att := u.attachmentLabels(); att != "" {
		label += att
	}
	sc.SetContent(0, top, '╭', nil, border)
	for x := 1; x < w-1; x++ {
		sc.SetContent(x, top, '─', nil, border)
	}
	sc.SetContent(w-1, top, '╮', nil, border)
	if label != "" {
		render.DrawStr(sc, 2, top, w-2, label, border)
	}
	right := w - 2
	if mode := currentName(u.modeOption()); mode != "" {
		tag := " " + strings.ToLower(mode) + " "
		st := render.StDim
		if strings.EqualFold(mode, "auto") {
			st = render.StYellow
		}
		right -= uniseg.StringWidth(tag)
		render.DrawStr(sc, right, top, w-1, tag, st)
	}
	if m := u.ed.Mode(); m != editor.ModeOff {
		tag := " " + strings.TrimSpace(m.String()+" "+u.ed.Pending()) + " "
		st := map[editor.Mode]tcell.Style{editor.ModeInsert: render.StFaint, editor.ModeNormal: render.StAccent}[m]
		if m >= editor.ModeVisual {
			st = render.StYellow
		}
		right -= uniseg.StringWidth(tag)
		render.DrawStr(sc, right, top, w-1, tag, st)
	}
	for i, r := range rows {
		y := top + 1 + i
		prompt := "  "
		if i == 0 {
			prompt = "❯ "
		}
		render.DrawStr(sc, 0, y, w, prompt, render.StUser.Bold(true))
		render.DrawStr(sc, 2, y, w-1, r, render.StBase)
	}
	if len(u.ed.Buf) == 0 {
		hint := "message towa…"
		if u.busy {
			hint = "type to queue a follow-up…"
		}
		render.DrawStr(sc, 2, top+1, w-1, hint, render.StFaint.Italic(true))
	}
}

// drawSelection shades the vim visual selection, which runs from input
// row and column a to b. y0 is the screen row of input row 0; rows outside
// minY..maxY are scrolled away.
func (u *UI) drawSelection(y0, minY, maxY, w int, rows []string, a, b [2]int) {
	for r := a[0]; r <= b[0] && r < len(rows); r++ {
		y := y0 + r
		if y < minY || y > maxY {
			continue
		}
		x0, x1 := 0, uniseg.StringWidth(rows[r])+1 // +1 shades the line break
		if r == a[0] {
			x0 = a[1]
		}
		if r == b[0] {
			x1 = b[1]
		}
		if r == a[0] && x1 <= x0 {
			x1 = x0 + 1 // an empty line
		}
		for x := 2 + x0; x < 2+x1 && x < w-1; x++ {
			ch, comb, st, _ := u.sc.GetContent(x, y)
			u.sc.SetContent(x, y, ch, comb, st.Background(render.CSelBg))
		}
	}
}

func (u *UI) drawStatus(y, w int) {
	sc := u.sc
	var l render.Line
	if m := currentName(u.modelOption()); m != "" {
		l = append(l, render.Span{Text: "◆ " + m, St: render.StAccent})
	}
	if u.ctxSize > 0 {
		pct := float64(u.ctxUsed) / float64(u.ctxSize)
		st := render.StGreen
		switch {
		case pct > 0.85:
			st = render.StRed
		case pct > 0.6:
			st = render.StYellow
		}
		l = append(l, render.Span{Text: "  ", St: render.StDim}, render.Span{Text: meter(pct, 8), St: st}, render.Span{Text: fmt.Sprintf(" %s/%s", fmtTokens(u.ctxUsed), fmtTokens(u.ctxSize)), St: render.StDim})
	}
	if u.cost != nil && u.cost.Amount > 0 {
		l = append(l, render.Span{Text: fmt.Sprintf("  $%.2f", u.cost.Amount), St: render.StDim})
	}
	if u.statsSummary != "" {
		l = append(l, render.Span{Text: "  " + u.statsSummary, St: render.StCyan})
	}
	if u.turnUsage != nil {
		l = append(l, render.Span{Text: fmt.Sprintf("  last ↑%s ↓%s", fmtTokens(u.turnUsage.InputTokens), fmtTokens(u.turnUsage.OutputTokens)), St: render.StFaint})
	}
	render.DrawLine(sc, 1, y, w, l)

	var right string
	var rst tcell.Style
	switch {
	case u.flash != "":
		right, rst = u.flash, render.StYellow
	case u.expandAll:
		right, rst = "expanded · alt+e", render.StDim
	default:
		right, rst = "/help", render.StFaint
	}
	rw := uniseg.StringWidth(right)
	if l.Width()+rw+4 < w {
		render.DrawStr(sc, w-rw-1, y, w, right, rst)
	}
}

func meter(p float64, n int) string {
	p = min(max(p, 0), 1)
	full := int(p * float64(n))
	return strings.Repeat("▰", full) + strings.Repeat("▱", n-full)
}

func (u *UI) planLines(w int) []render.Line {
	done := 0
	for _, e := range u.plan {
		if e.Status == acp.PlanEntryStatusCompleted {
			done++
		}
	}
	out := []render.Line{{{Text: "plan ", St: render.StAccent.Bold(true)}, {Text: fmt.Sprintf("%d/%d", done, len(u.plan)), St: render.StDim}}}
	for _, e := range u.plan {
		var mark render.Span
		st := render.StBase
		switch e.Status {
		case acp.PlanEntryStatusCompleted:
			mark, st = render.Span{Text: "✓ ", St: render.StGreen}, render.StDim.StrikeThrough(true)
		case acp.PlanEntryStatusInProgress:
			mark, st = render.Span{Text: "▸ ", St: render.StYellow}, render.StBase.Bold(true)
		default:
			mark = render.Span{Text: "○ ", St: render.StDim}
		}
		out = append(out, render.Wrap([]render.Span{{Text: render.Clean(e.Content), St: st}}, w, render.Line{mark}, render.Line{{Text: "  ", St: st}})...)
	}
	return out
}

func (u *UI) drawPlanSide(x, top, w, bottom int) {
	sc := u.sc
	for y := top; y <= bottom; y++ {
		sc.SetContent(x-1, y, '│', nil, render.StFaint)
	}
	for i, l := range u.planLines(w - 2) {
		if top+i > bottom {
			break
		}
		render.DrawLine(sc, x+1, top+i, x+w, l)
	}
}

func (u *UI) permLines(w int) []render.Line {
	p := u.perms[0]
	tc := p.req.ToolCall
	title := "tool call"
	if tc.Title != nil {
		title = *tc.Title
	}
	out := []render.Line{{{Text: "⚠ allow ", St: render.StYellow.Bold(true)}, {Text: render.Truncate(render.Clean(title), w-10), St: render.StBase.Bold(true)}, {Text: "?", St: render.StYellow.Bold(true)}}}
	// Show what the tool will do: its diff or input.
	b := &block{kind: kindTool, content: tc.Content}
	body := b.bodyLines(w - 4)
	if len(body) == 0 && tc.RawInput != nil {
		for _, r := range strings.Split(render.Clean(formatRaw(tc.RawInput)), "\n") {
			body = append(body, render.Wrap([]render.Span{{Text: r, St: render.StDim}}, w-4, nil, nil)...)
		}
	}
	for i, l := range body {
		if i == 10 {
			out = append(out, render.Line{{Text: "  … ", St: render.StFaint}})
			break
		}
		out = append(out, append(render.Line{{Text: "  ", St: render.StDim}}, l...))
	}
	var opts render.Line
	for i, o := range p.req.Options {
		st := render.StDim
		if i == p.sel {
			st = tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(render.CAccent).Bold(true)
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
		opts = append(opts, render.Span{Text: " " + o.Name + " [" + key + "] ", St: st}, render.Span{Text: " ", St: render.StDim})
	}
	out = append(out, opts)
	if n := len(u.perms) - 1; n > 0 {
		out = append(out, render.Line{{Text: fmt.Sprintf("  +%d more waiting", n), St: render.StFaint}})
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
	bg := tcell.StyleDefault.Background(render.CPanelBg)
	for i := 0; i < n; i++ {
		it := c.items[first+i]
		y := top + i
		st := bg.Foreground(render.CText)
		if first+i == c.sel {
			st = tcell.StyleDefault.Background(render.CSelBg).Foreground(render.CText).Bold(true)
		}
		render.Fill(u.sc, 1, y, w-1, st)
		x := render.DrawStr(u.sc, 2, y, w-1, render.Truncate(it.label, lw), st.Foreground(render.CAccent))
		x = max(x, 2+lw) + 2
		render.DrawStr(u.sc, x, y, w-2, render.Truncate(it.detail, w-x-2), st.Foreground(render.CDim).Bold(false))
	}
}

func (u *UI) drawPicker(w, h int) {
	p := u.picker
	sc := u.sc
	pw := min(w-4, 90)
	ph := min(h-4, 20)
	x0 := (w - pw) / 2
	y0 := (h - ph) / 2
	bg := tcell.StyleDefault.Background(render.CPanelBg).Foreground(render.CText)
	border := bg.Foreground(render.CAccent)
	for y := y0; y < y0+ph; y++ {
		render.Fill(sc, x0, y, x0+pw, bg)
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
	render.DrawStr(sc, x0+2, y0, x0+pw-2, " "+p.title+" ", border.Bold(true))
	render.DrawStr(sc, x0+pw-24, y0+ph-1, x0+pw-2, " ↑↓ enter esc ", border)

	fx := render.DrawStr(sc, x0+2, y0+1, x0+pw-2, "⌕ ", bg.Foreground(render.CDim))
	fx = render.DrawStr(sc, fx, y0+1, x0+pw-2, string(p.filter), bg.Bold(true))
	sc.SetContent(fx, y0+1, '▏', nil, bg.Foreground(render.CAccent))
	render.DrawStr(sc, x0+pw-12, y0+1, x0+pw-2, fmt.Sprintf("%d/%d", len(p.shown), len(p.items)), bg.Foreground(render.CFaint))

	listTop := y0 + 2
	rows := ph - 3
	first := max(0, p.sel-rows+1)
	for i := 0; i < rows && first+i < len(p.shown); i++ {
		it := p.items[p.shown[first+i]]
		y := listTop + i
		st := bg
		if first+i == p.sel {
			st = tcell.StyleDefault.Background(render.CSelBg).Foreground(render.CText).Bold(true)
			render.Fill(sc, x0+1, y, x0+pw-1, st)
		}
		mark := "  "
		if it.current {
			mark = "● "
		}
		x := render.DrawStr(sc, x0+2, y, x0+pw-2, mark, st.Foreground(render.CGreen))
		lw := min(uniseg.StringWidth(it.label), pw/2)
		x = render.DrawStr(sc, x, y, x0+pw-2, render.Truncate(it.label, lw), st)
		render.DrawStr(sc, x+2, y, x0+pw-2, render.Truncate(it.detail, x0+pw-x-5), st.Foreground(render.CDim).Bold(false))
	}
}

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}
