package tui

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	acp "github.com/gfx-labs/go-acp"
)

// --- soft interrupt -----------------------------------------------------

// interruptNote prefixes an urgent message so the model knows its previous
// turn was cut short on purpose.
const interruptNote = "[The user interrupted your previous turn to send this message. Take it into account and continue from where you were if it still makes sense.]\n\n"

// queuedPrompt merges the messages typed during a turn into one prompt.
// ACP has no steering field (towa's design notes say so too), so the
// best a client can do is send them together as soon as the turn ends.
func queuedPrompt(queue []string, urgent bool) string {
	var parts []string
	for _, q := range queue {
		if q = strings.TrimSpace(q); q != "" {
			parts = append(parts, q)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	text := strings.Join(parts, "\n\n")
	if urgent {
		text = interruptNote + text
	}
	return text
}

// submitEditor sends the editor's text. urgent, during a turn, cancels
// the turn and sends right after it stops.
func (u *UI) submitEditor(urgent bool) {
	// A trailing backslash continues the line, as in a shell.
	if n := len(u.ed.Buf); !urgent && n > 0 && u.ed.Cur == n && u.ed.Buf[n-1] == '\\' {
		u.ed.Buf = u.ed.Buf[:n-1]
		u.ed.Cur--
		u.ed.Insert("\n")
		return
	}
	text := u.ed.Text()
	u.ed.Clear()
	if urgent && strings.TrimSpace(text) == "" && len(u.queue) > 0 {
		// Promote what is already queued.
		u.urgent = true
		u.cancelTurn()
		return
	}
	u.submit(text)
	if urgent && u.busy && len(u.queue) > 0 {
		u.urgent = true
		u.cancelTurn()
		u.notify("interrupting to send now…")
	}
}

// sendQueued runs after a turn ends: everything queued goes out as one
// prompt.
func (u *UI) sendQueued() {
	if len(u.queue) == 0 {
		return
	}
	text := queuedPrompt(u.queue, u.urgent)
	u.queue, u.urgent = nil, false
	var moved []*block
	kept := u.blocks[:0]
	for _, b := range u.blocks {
		if b.queued {
			b.queued = false
			b.dirty()
			moved = append(moved, b)
			continue
		}
		kept = append(kept, b)
	}
	// Move them to the end so the transcript reads in order.
	u.blocks = append(kept, moved...)
	if text != "" {
		u.startTurn(text)
	}
}

// --- session operations -------------------------------------------------

func (u *UI) needIdle() bool {
	if u.session == "" {
		u.notify("no session yet")
		return false
	}
	if u.busy {
		u.notify("wait for the turn to end first")
		return false
	}
	return true
}

// fork copies the current session and calls done with the new id on the
// UI goroutine.
func (u *UI) fork(done func(id acp.SessionId)) {
	sid, cwd := u.session, u.opts.Cwd
	go func() {
		resp, err := u.conn.UnstableForkSession(u.ctx, acp.UnstableForkSessionRequest{
			SessionId: acp.UnstableSessionId(sid), Cwd: cwd, McpServers: []acp.UnstableMcpServer{},
		})
		u.post(func(u *UI) {
			if err != nil {
				u.errorf("fork: %v", err)
				return
			}
			done(acp.SessionId(resp.SessionId))
		})
	}()
}

// forkSession forks and switches this window to the fork.
func (u *UI) forkSession() {
	if !u.needIdle() {
		return
	}
	from := u.session
	u.fork(func(id acp.SessionId) {
		u.prevSession = from
		go func() {
			u.loadSession(id)
			u.post(func(u *UI) { u.info(fmt.Sprintf("forked from %s (/back returns)", from)) })
		}()
	})
}

func (u *UI) renameSession(id acp.SessionId, title string) {
	if title == "" {
		u.notify("usage: /rename <title>")
		return
	}
	go func() {
		_, err := u.conn.UnstableSetSessionTitle(u.ctx, acp.UnstableSetSessionTitleRequest{SessionId: acp.UnstableSessionId(id), Title: title})
		u.post(func(u *UI) {
			if err != nil {
				u.errorf("rename: %v", err)
				return
			}
			if id == u.session {
				u.title = title
				u.sc.SetTitle("wist · " + title)
			}
			u.notify("renamed → " + title)
		})
	}()
}

func (u *UI) archiveSession(id acp.SessionId) {
	go func() {
		_, err := u.conn.UnstableArchiveSession(u.ctx, acp.UnstableArchiveSessionRequest{SessionId: acp.UnstableSessionId(id)})
		u.post(func(u *UI) {
			if err != nil {
				u.errorf("archive: %v", err)
				return
			}
			u.notify("archived " + string(id))
			if id == u.session {
				u.info(fmt.Sprintf("session %s archived; /new to start another", id))
			}
		})
	}()
}

// confirmDelete asks yes/no in a picker before deleting.
func (u *UI) confirmDelete(id acp.SessionId, label string) {
	items := []pickItem{{label: "no, keep it", value: "no"}, {label: "yes, delete " + label, value: "yes"}}
	u.openPicker("delete session "+label+"?", items, 0, func(it pickItem) {
		if it.value != "yes" {
			return
		}
		go func() {
			_, err := u.conn.DeleteSession(u.ctx, acp.DeleteSessionRequest{SessionId: id})
			u.post(func(u *UI) {
				if err != nil {
					u.errorf("delete: %v", err)
					return
				}
				u.notify("deleted " + label)
				if id == u.session {
					u.lastSession = ""
					go u.newSession()
				}
			})
		}()
	})
}

// --- /btw and /split ----------------------------------------------------

// childArgs are the wist arguments that recreate this window's agent setup
// in another process.
func (u *UI) childArgs(extra ...string) []string {
	o := u.opts
	args := []string{"--cwd", o.Cwd}
	if o.Agent != "" {
		args = append(args, "--agent", o.Agent)
	} else if o.Towa != "" && o.Towa != "towa" {
		args = append(args, "--towa", o.Towa)
	}
	if o.NoMouse {
		args = append(args, "--no-mouse")
	}
	args = append(args, extra...)
	if len(o.PassArgs) > 0 {
		args = append(append(args, "--"), o.PassArgs...)
	}
	return args
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:@,+%", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = shellQuote(a)
	}
	return strings.Join(q, " ")
}

// spawnCommand builds the shell command that opens cmd in a new pane:
// the configured spawn_hook, or a tmux split next to this pane.
func spawnCommand(hook, cmd, tmux, pane string) (string, error) {
	if hook != "" {
		if strings.Contains(hook, "{cmd}") {
			return strings.ReplaceAll(hook, "{cmd}", shellQuote(cmd)), nil
		}
		return hook + " " + shellQuote(cmd), nil
	}
	if tmux == "" {
		return "", fmt.Errorf("not inside tmux and no spawn_hook configured")
	}
	target := ""
	if pane != "" {
		target = " -t " + shellQuote(pane)
	}
	return "tmux split-window -h" + target + " " + shellQuote(cmd), nil
}

// spawnPane starts another wist with args in a new pane.
func (u *UI) spawnPane(args []string) error {
	self, err := os.Executable()
	if err != nil {
		self = "wist"
	}
	cmd := shellJoin(append([]string{self}, args...))
	sh, err := spawnCommand(u.cfg.SpawnHook, cmd, os.Getenv("TMUX"), os.Getenv("TMUX_PANE"))
	if err != nil {
		return err
	}
	out, err := exec.Command("sh", "-c", sh).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v %s", sh, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (u *UI) canSpawn() bool { return u.cfg.SpawnHook != "" || os.Getenv("TMUX") != "" }

// btw asks a side question in a fork of the session, so the main session
// is left alone. With tmux (or a spawn_hook) the fork opens in a new pane;
// otherwise this window switches to the fork and /back returns.
func (u *UI) btw(question string) {
	if question == "" {
		u.notify("usage: /btw <question>")
		return
	}
	if u.session == "" {
		u.notify("no session yet")
		return
	}
	from := u.session
	u.fork(func(id acp.SessionId) {
		if u.canSpawn() {
			if err := u.spawnPane(u.childArgs("--resume", string(id), "-p", question)); err != nil {
				u.errorf("btw: %v", err)
				return
			}
			u.notify("side question opened in a new pane")
			return
		}
		if u.busy {
			u.errorf("btw: not in tmux; wait for the turn to end to ask in this window")
			return
		}
		u.prevSession = from
		go func() {
			u.loadSession(id)
			u.post(func(u *UI) {
				u.info(fmt.Sprintf("side session forked from %s · /back returns", from))
				u.submit(question)
			})
		}()
	})
}

// split opens a fork of this session, or a fresh one, in a new pane.
func (u *UI) split(fresh bool) {
	if !u.canSpawn() {
		u.errorf("split: not inside tmux and no spawn_hook configured")
		return
	}
	if fresh {
		if err := u.spawnPane(u.childArgs()); err != nil {
			u.errorf("split: %v", err)
		}
		return
	}
	if u.session == "" {
		u.notify("no session yet")
		return
	}
	u.fork(func(id acp.SessionId) {
		if err := u.spawnPane(u.childArgs("--resume", string(id))); err != nil {
			u.errorf("split: %v", err)
			return
		}
		u.notify("fork " + string(id) + " opened in a new pane")
	})
}

func (u *UI) back() {
	if u.prevSession == "" {
		u.notify("nothing to go back to")
		return
	}
	if !u.needIdle() {
		return
	}
	id := u.prevSession
	u.prevSession = ""
	go u.loadSession(id)
}

// --- edit stats ---------------------------------------------------------

type fileDelta struct{ adds, dels int }

// editStats counts lines changed per file over the session. Each tool
// call's diffs are kept separately so repeated updates replace, not add.
type editStats struct {
	perTool map[acp.ToolCallId]map[string]fileDelta
	files   map[string]fileDelta
	adds    int
	dels    int
}

func (s *editStats) record(id acp.ToolCallId, content []acp.ToolCallContent) {
	cur := map[string]fileDelta{}
	for _, c := range content {
		d := c.Diff
		if d == nil {
			continue
		}
		old := ""
		if d.OldText != nil {
			old = *d.OldText
		}
		fd := cur[d.Path]
		a, r := diffCounts(old, d.NewText)
		fd.adds += a
		fd.dels += r
		cur[d.Path] = fd
	}
	if len(cur) == 0 {
		return
	}
	if s.perTool == nil {
		s.perTool = map[acp.ToolCallId]map[string]fileDelta{}
	}
	s.perTool[id] = cur
	s.files = map[string]fileDelta{}
	s.adds, s.dels = 0, 0
	for _, m := range s.perTool {
		for p, fd := range m {
			t := s.files[p]
			t.adds += fd.adds
			t.dels += fd.dels
			s.files[p] = t
			s.adds += fd.adds
			s.dels += fd.dels
		}
	}
}

func diffCounts(a, b string) (adds, dels int) {
	for _, op := range lineDiff(splitLines(a), splitLines(b)) {
		switch op.kind {
		case '+':
			adds++
		case '-':
			dels++
		}
	}
	return
}

// summary is a short form for the status bar, e.g. "3 files +42 -7".
func (s *editStats) summary() string {
	if len(s.files) == 0 {
		return ""
	}
	noun := "files"
	if len(s.files) == 1 {
		noun = "file"
	}
	return fmt.Sprintf("%d %s +%d -%d", len(s.files), noun, s.adds, s.dels)
}

func (s *editStats) report(cwd string) string {
	if len(s.files) == 0 {
		return "no edits in this session yet"
	}
	paths := make([]string, 0, len(s.files))
	for p := range s.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	fmt.Fprintf(&b, "edits: %s\n", s.summary())
	for _, p := range paths {
		fd := s.files[p]
		rel := strings.TrimPrefix(strings.TrimPrefix(p, cwd), "/")
		fmt.Fprintf(&b, "  +%-5d -%-5d %s\n", fd.adds, fd.dels, rel)
	}
	return strings.TrimRight(b.String(), "\n")
}
