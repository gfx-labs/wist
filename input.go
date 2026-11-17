package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	acp "github.com/gfx-labs/go-acp"
)

// --- picker -------------------------------------------------------------

type pickItem struct {
	label, detail, value string
	current              bool
}

// picker is a filterable list overlay, like jcode's model and session
// pickers.
type picker struct {
	title  string
	items  []pickItem
	filter []rune
	shown  []int
	sel    int
	choose func(pickItem)
	// keys, when set, gets keys first with the selected item and reports
	// whether it handled them.
	keys func(*tcell.EventKey, pickItem) bool
}

func (u *UI) openPicker(title string, items []pickItem, sel int, choose func(pickItem)) {
	p := &picker{title: title, items: items, choose: choose}
	p.refilter()
	p.sel = min(sel, max(len(p.shown)-1, 0))
	u.picker = p
}

func (p *picker) refilter() {
	p.shown = p.shown[:0]
	q := strings.ToLower(string(p.filter))
	type scored struct{ i, s int }
	var hits []scored
	for i, it := range p.items {
		if s, ok := fuzzy(strings.ToLower(it.label+" "+it.detail), q); ok {
			hits = append(hits, scored{i, s})
		}
	}
	if q != "" {
		sort.SliceStable(hits, func(a, b int) bool { return hits[a].s < hits[b].s })
	}
	for _, h := range hits {
		p.shown = append(p.shown, h.i)
	}
	p.sel = 0
}

// fuzzy matches q as a subsequence of s. Lower scores are better: a
// contiguous or early match beats a scattered one.
func fuzzy(s, q string) (int, bool) {
	if q == "" {
		return 0, true
	}
	if i := strings.Index(s, q); i >= 0 {
		return i, true
	}
	score, si, last := 0, 0, -1
	for _, qc := range q {
		found := false
		for si < len(s) {
			c := rune(s[si])
			si++
			if c == qc {
				if last >= 0 {
					score += si - last - 1
				}
				last = si
				found = true
				break
			}
		}
		if !found {
			return 0, false
		}
	}
	return 1000 + score, true
}

func (u *UI) pickerKey(ev *tcell.EventKey) {
	p := u.picker
	if p.keys != nil && p.sel < len(p.shown) && p.keys(ev, p.items[p.shown[p.sel]]) {
		return
	}
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		u.picker = nil
	case tcell.KeyEnter:
		u.picker = nil
		if p.sel < len(p.shown) {
			p.choose(p.items[p.shown[p.sel]])
		}
	case tcell.KeyUp, tcell.KeyCtrlP:
		if p.sel > 0 {
			p.sel--
		}
	case tcell.KeyDown, tcell.KeyCtrlN, tcell.KeyTab:
		if p.sel < len(p.shown)-1 {
			p.sel++
		}
	case tcell.KeyPgUp:
		p.sel = max(0, p.sel-10)
	case tcell.KeyPgDn:
		p.sel = min(len(p.shown)-1, p.sel+10)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(p.filter) > 0 {
			p.filter = p.filter[:len(p.filter)-1]
			p.refilter()
		}
	case tcell.KeyRune:
		p.filter = append(p.filter, ev.Rune())
		p.refilter()
	}
}

// --- completion ---------------------------------------------------------

// completion is the popup under the input for /commands and @files.
type completion struct {
	start int // rune index of the token being completed
	items []pickItem
	sel   int
}

type localCmd struct {
	name, desc string
}

var localCommands = []localCmd{
	{"help", "show keys and commands"},
	{"new", "start a new session"},
	{"resume", "pick a stored session to load"},
	{"sessions", "pick a stored session to load"},
	{"model", "pick the model"},
	{"mode", "pick the permission mode"},
	{"config", "show and change session options"},
	{"clear", "clear the screen (the session keeps its history)"},
	{"compact", "ask towa to compact the context"},
	{"cancel", "interrupt the running turn"},
	{"expand", "toggle expanding all tool output"},
	{"thinking", "toggle showing reasoning"},
	{"plan", "toggle the plan panel"},
	{"copy", "copy the last answer to the clipboard"},
	{"fork", "fork the session and switch to the fork"},
	{"back", "return to the session before /fork or /btw"},
	{"rename", "<title> rename the session"},
	{"archive", "archive the session"},
	{"delete", "delete the session (asks first)"},
	{"btw", "<question> ask a side question in a fork (new tmux pane)"},
	{"split", "open a fork in a new tmux pane (/split new: fresh session)"},
	{"stats", "files edited and lines changed this session"},
	{"theme", "pick a color theme"},
	{"keys", "list key bindings"},
	{"agents", "pick an agent from the ACP registry"},
	{"cwd", "show the working directory"},
	{"info", "show session details"},
	{"log", "show where towa's log is"},
	{"quit", "exit"},
}

func (u *UI) updateCompletion() {
	u.comp = nil
	buf := u.ed.buf[:u.ed.cur]
	start := len(buf)
	for start > 0 && buf[start-1] != ' ' && buf[start-1] != '\n' {
		start--
	}
	tok := string(buf[start:])
	switch {
	case strings.HasPrefix(tok, "/") && start == 0:
		q := strings.ToLower(tok[1:])
		var items []pickItem
		seen := map[string]bool{}
		for _, c := range append(localCommands[:len(localCommands):len(localCommands)], renderCommands...) {
			if strings.HasPrefix(c.name, q) {
				items = append(items, pickItem{label: "/" + c.name, detail: c.desc, value: "/" + c.name + " "})
				seen[c.name] = true
			}
		}
		for _, c := range u.commands {
			if strings.HasPrefix(strings.ToLower(c.Name), q) && !seen[c.Name] {
				hint := c.Description
				if c.Input != nil && c.Input.Unstructured != nil {
					hint = "<" + c.Input.Unstructured.Hint + "> " + hint
				}
				items = append(items, pickItem{label: "/" + c.Name, detail: hint, value: "/" + c.Name + " "})
			}
		}
		if len(items) > 0 && !(len(items) == 1 && items[0].label == tok) {
			u.comp = &completion{start: start, items: items}
		}
	case strings.HasPrefix(tok, "@"):
		q := strings.ToLower(tok[1:])
		files := u.fileList()
		type hit struct {
			f string
			s int
		}
		var hits []hit
		for _, f := range files {
			if s, ok := fuzzy(strings.ToLower(f), q); ok {
				hits = append(hits, hit{f, s})
			}
		}
		sort.SliceStable(hits, func(a, b int) bool { return hits[a].s < hits[b].s })
		var items []pickItem
		for i, h := range hits {
			if i == 50 {
				break
			}
			items = append(items, pickItem{label: h.f, value: "@" + h.f + " "})
		}
		if len(items) > 0 {
			u.comp = &completion{start: start, items: items}
		}
	}
}

// fileList lists files under cwd once, preferring git's view so ignored
// files stay out.
func (u *UI) fileList() []string {
	if u.files != nil {
		return u.files
	}
	u.files = []string{}
	out, err := exec.Command("git", "-C", u.opts.cwd, "ls-files", "--cached", "--others", "--exclude-standard").Output()
	if err == nil {
		for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if f != "" {
				u.files = append(u.files, f)
			}
		}
		return u.files
	}
	_ = filepath.WalkDir(u.opts.cwd, func(p string, d fs.DirEntry, err error) error {
		if err != nil || len(u.files) > 20000 {
			return filepath.SkipDir
		}
		name := d.Name()
		if d.IsDir() && p != u.opts.cwd && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "target") {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(u.opts.cwd, p)
			u.files = append(u.files, rel)
		}
		return nil
	})
	return u.files
}

func (u *UI) acceptCompletion() {
	c := u.comp
	if c == nil || c.sel >= len(c.items) {
		return
	}
	ins := c.items[c.sel].value
	u.ed.buf = append(u.ed.buf[:c.start], append([]rune(ins), u.ed.buf[u.ed.cur:]...)...)
	u.ed.cur = c.start + len([]rune(ins))
	u.comp = nil
}

// --- keys ---------------------------------------------------------------

func (u *UI) handleEvent(ev tcell.Event) {
	switch ev := ev.(type) {
	case *tcell.EventResize:
		u.sc.Sync()
	case *tcell.EventPaste:
		u.pasting = ev.Start()
		if u.pasting {
			u.pasteBuf.Reset()
		} else {
			u.finishPaste()
		}
	case *tcell.EventMouse:
		if u.renderMouse(ev) {
			return
		}
		switch ev.Buttons() {
		case tcell.WheelUp:
			u.scroll += 3
		case tcell.WheelDown:
			u.scroll = max(0, u.scroll-3)
		}
	case *tcell.EventKey:
		u.handleKey(ev)
	}
}

func (u *UI) handleKey(ev *tcell.EventKey) {
	if u.pasting {
		switch ev.Key() {
		case tcell.KeyRune:
			u.pasteBuf.WriteRune(ev.Rune())
		case tcell.KeyEnter, tcell.KeyCtrlJ:
			u.pasteBuf.WriteByte('\n')
		case tcell.KeyTab:
			u.pasteBuf.WriteString("    ")
		}
		return
	}
	if ev.Key() == tcell.KeyBackspace || ev.Key() == tcell.KeyBackspace2 {
		if len(u.ed.buf) == 0 && len(u.images) > 0 && u.picker == nil {
			u.images = u.images[:len(u.images)-1]
			u.notify("removed image")
			return
		}
	}
	if u.picker != nil {
		u.pickerKey(ev)
		return
	}
	if u.renderKey(ev) {
		return
	}
	if len(u.perms) > 0 && u.permKey(ev) {
		return
	}

	mod := ev.Modifiers()
	if u.comp != nil && u.comp.sel >= len(u.comp.items) {
		u.comp = nil
	}
	if u.comp != nil {
		switch ev.Key() {
		case tcell.KeyUp, tcell.KeyCtrlP:
			u.comp.sel = (u.comp.sel + len(u.comp.items) - 1) % len(u.comp.items)
			return
		case tcell.KeyDown, tcell.KeyCtrlN:
			u.comp.sel = (u.comp.sel + 1) % len(u.comp.items)
			return
		case tcell.KeyTab:
			u.acceptCompletion()
			u.updateCompletion()
			return
		case tcell.KeyEnter:
			if mod == 0 {
				// Enter on a /command runs it straight away.
				u.acceptCompletion()
				if t := strings.TrimSpace(u.ed.text()); strings.HasPrefix(t, "/") && !strings.Contains(t, " ") {
					u.ed.clear()
					u.submit(t)
					return
				}
				u.updateCompletion()
				return
			}
		case tcell.KeyEscape:
			u.comp = nil
			return
		}
	}

	// Bound actions first, then the fixed editor keys.
	if name, ok := u.keymap.keys[eventKey(ev)]; ok {
		if a := actionByName(name); a != nil {
			a.run(u)
			if name == "cancel" {
				u.comp = nil
				return
			}
			u.updateCompletion()
			return
		}
	}

	switch ev.Key() {
	case tcell.KeyTab:
		if len(u.ed.buf) == 0 {
			u.cycleMode()
		}
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if mod&(tcell.ModAlt|tcell.ModCtrl) != 0 {
			u.ed.cut(u.ed.wordLeft(), u.ed.cur)
		} else {
			u.ed.backspace()
		}
	case tcell.KeyDelete:
		u.ed.del()
	case tcell.KeyLeft:
		if mod&(tcell.ModCtrl|tcell.ModAlt) != 0 {
			u.ed.cur = u.ed.wordLeft()
		} else {
			u.ed.left()
		}
	case tcell.KeyRight:
		if mod&(tcell.ModCtrl|tcell.ModAlt) != 0 {
			u.ed.cur = u.ed.wordRight()
		} else {
			u.ed.right()
		}
	case tcell.KeyUp:
		u.ed.up()
	case tcell.KeyDown:
		u.ed.down()
	case tcell.KeyHome, tcell.KeyCtrlA:
		u.ed.cur = u.ed.lineStart()
	case tcell.KeyEnd, tcell.KeyCtrlE:
		u.ed.cur = u.ed.lineEnd()
	case tcell.KeyCtrlB:
		u.ed.left()
	case tcell.KeyCtrlF:
		u.ed.right()
	case tcell.KeyCtrlW:
		u.ed.cut(u.ed.wordLeft(), u.ed.cur)
	case tcell.KeyCtrlU:
		u.ed.cut(u.ed.lineStart(), u.ed.cur)
	case tcell.KeyCtrlK:
		u.ed.cut(u.ed.cur, u.ed.lineEnd())
	case tcell.KeyCtrlY:
		u.ed.yank()
	case tcell.KeyCtrlP:
		u.ed.histPrev()
	case tcell.KeyCtrlN:
		u.ed.histNext()
	case tcell.KeyRune:
		if mod&tcell.ModAlt != 0 {
			switch ev.Rune() {
			case 'b':
				u.ed.cur = u.ed.wordLeft()
			case 'f':
				u.ed.cur = u.ed.wordRight()
			case 'd':
				u.ed.cut(u.ed.cur, u.ed.wordRight())
			}
			break
		}
		u.ed.insert(string(ev.Rune()))
	}
	u.updateCompletion()
}

func (u *UI) ctrlCAction() {
	switch {
	case u.busy:
		u.cancelTurn()
	case len(u.ed.buf) > 0:
		u.ed.clear()
	case time.Since(u.ctrlC) < 2*time.Second:
		u.quit = true
	default:
		u.ctrlC = time.Now()
		u.notify("press ctrl+c again to quit")
	}
}

func (u *UI) pageSize() int {
	_, h := u.sc.Size()
	return max(1, h/2)
}

func (u *UI) toggleLastTool() {
	for i := len(u.blocks) - 1; i >= 0; i-- {
		if b := u.blocks[i]; b.kind == kindTool || b.kind == kindThought {
			b.expanded = !b.expanded
			b.dirty()
			return
		}
	}
}

func (u *UI) pickHistory() {
	var items []pickItem
	for i := len(u.ed.hist) - 1; i >= 0; i-- {
		h := u.ed.hist[i]
		items = append(items, pickItem{label: strings.ReplaceAll(h, "\n", " ⏎ "), value: h})
	}
	if len(items) == 0 {
		u.notify("no history yet")
		return
	}
	u.openPicker("prompt history", items, 0, func(it pickItem) { u.ed.set(it.value) })
}

func (u *UI) permKey(ev *tcell.EventKey) bool {
	p := u.perms[0]
	n := len(p.req.Options)
	switch ev.Key() {
	case tcell.KeyUp, tcell.KeyLeft:
		p.sel = (p.sel + n - 1) % n
	case tcell.KeyDown, tcell.KeyRight, tcell.KeyTab:
		p.sel = (p.sel + 1) % n
	case tcell.KeyEnter:
		u.answerPermission(p.sel)
	case tcell.KeyEscape:
		u.answerPermission(-1)
	case tcell.KeyRune:
		r := ev.Rune()
		if r >= '1' && r <= '9' && int(r-'1') < n {
			u.answerPermission(int(r - '1'))
			return true
		}
		want := map[rune][]acp.PermissionOptionKind{
			'y': {acp.PermissionOptionKindAllowOnce},
			'a': {acp.PermissionOptionKindAllowAlways},
			'n': {acp.PermissionOptionKindRejectOnce, acp.PermissionOptionKindRejectAlways},
		}[r]
		for _, k := range want {
			for i, o := range p.req.Options {
				if o.Kind == k {
					u.answerPermission(i)
					return true
				}
			}
		}
		return false
	default:
		return false
	}
	return true
}

// --- slash commands -----------------------------------------------------

// localCommand runs a client side command. It reports false for commands
// the agent should receive.
func (u *UI) localCommand(text string) bool {
	name, arg, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
	arg = strings.TrimSpace(arg)
	switch name {
	case "help", "?":
		u.addBlock(&block{kind: kindAgent, text: helpText(u)})
	case "new":
		if u.busy {
			u.notify("wait for the turn to end first")
			return true
		}
		go u.newSession()
	case "resume", "sessions", "load":
		if arg != "" {
			go u.loadSession(acp.SessionId(arg))
		} else {
			u.pickSession()
		}
	case "model":
		m := u.modelOption()
		switch {
		case m == nil:
			u.notify("towa offers no model option")
		case arg != "":
			u.setOption(m.Id, acp.SessionConfigValueId(arg))
		default:
			u.pickOption(m)
		}
	case "mode":
		m := u.modeOption()
		switch {
		case m == nil:
			u.notify("towa offers no mode option")
		case arg != "":
			u.setOption(m.Id, acp.SessionConfigValueId(arg))
		default:
			u.pickOption(m)
		}
	case "config", "settings":
		u.pickConfig()
	case "clear":
		u.blocks = nil
		u.tools = map[acp.ToolCallId]*block{}
		u.scroll = 0
	case "cancel", "stop":
		u.cancelTurn()
	case "expand":
		u.expandAll = !u.expandAll
	case "thinking":
		u.showThinking = !u.showThinking
	case "plan":
		u.planHidden = !u.planHidden
	case "copy":
		for i := len(u.blocks) - 1; i >= 0; i-- {
			if u.blocks[i].kind == kindAgent {
				u.sc.SetClipboard([]byte(u.blocks[i].text))
				u.notify("copied last answer")
				return true
			}
		}
		u.notify("nothing to copy")
	case "fork":
		u.forkSession()
	case "back":
		u.back()
	case "rename", "title":
		id := u.session
		if rest, ok := strings.CutPrefix(arg, "--id "); ok {
			sid, title, _ := strings.Cut(strings.TrimSpace(rest), " ")
			id, arg = acp.SessionId(sid), strings.TrimSpace(title)
		}
		if id == "" {
			u.notify("no session yet")
			return true
		}
		u.renameSession(id, arg)
	case "archive":
		if u.needIdle() {
			u.archiveSession(u.session)
		}
	case "delete":
		if u.needIdle() {
			label := string(u.session)
			if u.title != "" {
				label = u.title
			}
			u.confirmDelete(u.session, label)
		}
	case "btw":
		u.btw(arg)
	case "split":
		u.split(arg == "new")
	case "stats":
		u.info(u.editStats.report(u.opts.cwd))
	case "theme":
		if arg != "" {
			u.setTheme(arg)
		} else {
			u.pickTheme()
		}
	case "keys", "keybindings":
		u.addBlock(&block{kind: kindAgent, text: u.keysText()})
	case "agents", "agent":
		u.pickAgent()
	case "cwd":
		u.info(u.opts.cwd)
	case "info":
		u.info(u.sessionInfo())
	case "log":
		u.info("towa stderr goes to " + u.opts.log)
	case "quit", "exit", "q":
		u.quit = true
	default:
		if u.renderCommand(name, arg) {
			return true
		}
		for _, c := range u.commands {
			if c.Name == name {
				return false
			}
		}
		if name == "compact" {
			return false
		}
		u.errorf("unknown command /%s (try /help)", name)
	}
	return true
}

func (u *UI) sessionInfo() string {
	var b strings.Builder
	fmt.Fprintf(&b, "agent: %s\nsession: %s\ncwd: %s\n", u.agentName, u.session, u.opts.cwd)
	if u.title != "" {
		fmt.Fprintf(&b, "title: %s\n", u.title)
	}
	for _, o := range u.options {
		if o.Select != nil {
			fmt.Fprintf(&b, "%s: %s\n", o.Select.Name, currentName(o.Select))
		}
		if o.Boolean != nil {
			fmt.Fprintf(&b, "%s: %v\n", o.Boolean.Name, o.Boolean.CurrentValue)
		}
	}
	if u.ctxSize > 0 {
		fmt.Fprintf(&b, "context: %s / %s\n", fmtTokens(u.ctxUsed), fmtTokens(u.ctxSize))
	}
	if u.cost != nil {
		fmt.Fprintf(&b, "cost: %.4f %s\n", u.cost.Amount, u.cost.Currency)
	}
	return strings.TrimRight(b.String(), "\n")
}

func helpText(u *UI) string {
	var b strings.Builder
	b.WriteString("## keys\n\n")
	b.WriteString("| key | action |\n|---|---|\n")
	for _, a := range actions {
		if keys := u.keymap.bindings(a.name); len(keys) > 0 {
			fmt.Fprintf(&b, "| %s | %s |\n", strings.Join(keys, ", "), a.desc)
		}
	}
	for _, k := range [][2]string{
		{"`\\`+enter", "newline"},
		{"tab", "complete, or cycle mode on an empty line"},
		{"up/down, ctrl+p/n", "history"},
		{"wheel", "scroll"},
		{"ctrl+w/u/k/y", "kill word/line, yank"},
		{"@path", "attach a file"},
		{"session picker", "ctrl+d delete, ctrl+a archive, ctrl+r rename"},
	} {
		fmt.Fprintf(&b, "| %s | %s |\n", k[0], k[1])
	}
	b.WriteString("\n## commands\n\n")
	for _, c := range append(localCommands[:len(localCommands):len(localCommands)], renderCommands...) {
		fmt.Fprintf(&b, "- `/%s` %s\n", c.name, c.desc)
	}
	if len(u.commands) > 0 {
		b.WriteString("\n## towa commands\n\n")
		for _, c := range u.commands {
			fmt.Fprintf(&b, "- `/%s` %s\n", c.Name, c.Description)
		}
	}
	return b.String()
}

func homeRel(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// finishPaste inserts a bracketed paste. A paste that is just the path of
// an image file, as dragging a file into the terminal gives, attaches the
// image instead.
func (u *UI) finishPaste() {
	text := u.pasteBuf.String()
	u.pasteBuf.Reset()
	if p := pastedImagePath(text, u.opts.cwd); p != "" && u.imagesOK {
		if a, err := imageFile(p); err == nil {
			u.attach(a)
			return
		}
	}
	u.ed.insert(text)
	u.updateCompletion()
}
