package ask

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	acp "github.com/gfx-labs/go-acp"
)

// client answers the agent. Updates arrive on the connection goroutine in
// order; all runner state is touched under r.out.mu.
type client struct{ r *runner }

var _ acp.Client = (*client)(nil)

func (c *client) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	r := c.r
	p := r.out
	p.mu.Lock()
	defer p.mu.Unlock()
	if n.SessionId != r.sid || !r.inTurn {
		return nil // history replayed by session/load, or late updates
	}
	up := n.Update
	switch {
	case up.AgentMessageChunk != nil:
		if t := up.AgentMessageChunk.Content.Text; t != nil {
			r.text(t.Text)
		}
	case up.AgentThoughtChunk != nil:
		p.label = "thinking"
	case up.ToolCall != nil:
		t := up.ToolCall
		r.tool(t.ToolCallId, &t.Title, ptr(string(t.Kind)), ptr(string(t.Status)), t.Content)
	case up.ToolCallUpdate != nil:
		t := up.ToolCallUpdate
		var kind, status *string
		if t.Kind != nil {
			kind = ptr(string(*t.Kind))
		}
		if t.Status != nil {
			status = ptr(string(*t.Status))
		}
		r.tool(t.ToolCallId, t.Title, kind, status, t.Content)
	case up.Plan != nil:
		r.plan(up.Plan.Entries)
	case up.PlanUpdate != nil:
		if items := up.PlanUpdate.Plan.Items; items != nil {
			r.plan(items.Entries)
		}
	case up.ConfigOptionUpdate != nil:
		r.opts = up.ConfigOptionUpdate.ConfigOptions
	case up.Notice != nil:
		text := up.Notice.Title
		if d := up.Notice.Description; d != nil && *d != "" {
			text += ": " + *d
		}
		col := yellow
		if up.Notice.Severity == acp.NoticeSeverityError {
			col = red
		}
		r.diag(col, "[notice] "+text)
	case up.CompactionUpdate != nil:
		switch up.CompactionUpdate.Status {
		case acp.CompactionStatusCompleted:
			r.diag(dim, "context compacted")
		case acp.CompactionStatusFailed:
			r.diag(red, "context compaction failed")
		default:
			p.label = "compacting context"
		}
	}
	return nil
}

func ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// text streams a message chunk to stdout; mu is held.
func (r *runner) text(s string) {
	if s == "" {
		return
	}
	if r.last != 1 {
		r.steps++
		r.segment.Reset()
		if r.last == 2 && r.output.Len() > 0 && !strings.HasSuffix(r.output.String(), "\n\n") {
			// Separate messages around tool calls like paragraphs.
			sep := "\n\n"
			if strings.HasSuffix(r.output.String(), "\n") {
				sep = "\n"
			}
			r.output.WriteString(sep)
			if r.showText() {
				if r.midLine {
					sep = "\n\n"
				}
				fmt.Fprint(r.o.Stdout, sep)
			}
			r.midLine = false
		}
		r.last = 1
	}
	r.output.WriteString(s)
	r.segment.WriteString(s)
	p := r.out
	p.label = "writing"
	if r.showText() {
		if p.shared {
			p.clear()
		}
		fmt.Fprint(r.o.Stdout, s)
		r.midLine = !strings.HasSuffix(s, "\n")
		p.pause = p.shared && r.midLine
	}
}

// diag prints a stderr line, first ending a half-written stdout line when
// both share a terminal; mu is held.
func (r *runner) diag(color, s string) {
	if r.out.shared && r.midLine && r.showText() {
		fmt.Fprintln(r.o.Stdout)
		r.midLine = false
	}
	r.out.pause = false
	r.out.lineLocked(color, s)
}

func (r *runner) tool(id acp.ToolCallId, title, kind, status *string, content []acp.ToolCallContent) {
	t := r.tools[id]
	if t == nil {
		t = &tool{status: "pending"}
		r.tools[id] = t
		r.order = append(r.order, id)
		if r.last == 0 {
			r.steps++
		}
		r.last = 2
	}
	if title != nil && *title != "" {
		t.title = *title
	}
	if kind != nil {
		t.kind = *kind
	}
	if status != nil {
		t.status = *status
	}
	for _, c := range content {
		if d := c.Diff; d != nil {
			old := ""
			if d.OldText != nil {
				old = *d.OldText
			}
			t.plus, t.minus = DiffStat(old, d.NewText)
		}
	}
	name := t.title
	if name == "" {
		name = t.kind
	}
	switch t.status {
	case "pending", "in_progress":
		r.out.label = name
		if t.printed == "" && name != "" {
			t.printed = "start"
			r.diag(dim, "· "+name)
		}
	case "completed":
		if t.printed != "done" {
			t.printed = "done"
			s := "✓ " + name
			if t.plus+t.minus > 0 {
				s += fmt.Sprintf(" +%d -%d", t.plus, t.minus)
			}
			r.diag(green, s)
			r.out.label = "thinking"
		}
	case "failed":
		if t.printed != "done" {
			t.printed = "done"
			r.diag(red, "✗ "+name+": failed")
			r.out.label = "thinking"
		}
	}
}

func (r *runner) plan(entries []acp.PlanEntry) {
	var b strings.Builder
	for _, e := range entries {
		mark := "○"
		switch e.Status {
		case acp.PlanEntryStatusCompleted:
			mark = "●"
		case acp.PlanEntryStatusInProgress:
			mark = "◐"
		}
		fmt.Fprintf(&b, "  %s %s\n", mark, e.Content)
	}
	s := b.String()
	if s == "" || s == r.lastPlan {
		return
	}
	r.lastPlan = s
	r.diag(cyan, "plan:\n"+s)
}

// DiffStat counts added and removed lines between old and new, by line
// multiset (good enough for a one-line summary).
func DiffStat(old, new string) (plus, minus int) {
	count := map[string]int{}
	for _, l := range splitLines(old) {
		count[l]++
	}
	for _, l := range splitLines(new) {
		if count[l] > 0 {
			count[l]--
		} else {
			plus++
		}
	}
	for _, n := range count {
		minus += n
	}
	return plus, minus
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// RejectOutcome is the answer when nobody can be asked: the reject_once
// option, else any reject option, else cancelled.
func RejectOutcome(opts []acp.PermissionOption) acp.RequestPermissionOutcome {
	for _, k := range []acp.PermissionOptionKind{acp.PermissionOptionKindRejectOnce, acp.PermissionOptionKindRejectAlways} {
		for _, o := range opts {
			if o.Kind == k {
				return acp.NewRequestPermissionOutcomeSelected(o.OptionId)
			}
		}
	}
	return acp.NewRequestPermissionOutcomeCancelled()
}

// PickOption maps a typed answer to an option: a 1-based number, or y
// (allow once), a (allow always), n (reject once). ok is false for an
// answer that names nothing.
func PickOption(opts []acp.PermissionOption, answer string) (acp.PermissionOptionId, bool) {
	answer = strings.ToLower(strings.TrimSpace(answer))
	if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(opts) {
		return opts[n-1].OptionId, true
	}
	var kinds []acp.PermissionOptionKind
	switch answer {
	case "y", "yes":
		kinds = []acp.PermissionOptionKind{acp.PermissionOptionKindAllowOnce, acp.PermissionOptionKindAllowAlways}
	case "a", "always":
		kinds = []acp.PermissionOptionKind{acp.PermissionOptionKindAllowAlways}
	case "n", "no", "":
		kinds = []acp.PermissionOptionKind{acp.PermissionOptionKindRejectOnce, acp.PermissionOptionKindRejectAlways}
	}
	for _, k := range kinds {
		for _, o := range opts {
			if o.Kind == k {
				return o.OptionId, true
			}
		}
	}
	return "", false
}

func (r *runner) canPrompt() bool {
	if r.o.PromptPermissions {
		return true
	}
	return r.o.TTY.Stdin && r.o.TTY.Stderr && !r.o.JSON && !r.o.Quiet
}

func (c *client) RequestPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	r := c.r
	title := "a tool call"
	if req.ToolCall.Title != nil && *req.ToolCall.Title != "" {
		title = *req.ToolCall.Title
	}
	reject := func(why string) (acp.RequestPermissionResponse, error) {
		r.out.mu.Lock()
		r.diag(yellow, fmt.Sprintf("permission denied: %s (%s)", title, why))
		r.out.mu.Unlock()
		return acp.RequestPermissionResponse{Outcome: RejectOutcome(req.Options)}, nil
	}
	if !r.canPrompt() {
		return reject("no terminal to ask on; use --auto, or run in a terminal without --json/--quiet")
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return reject("cannot open /dev/tty")
	}
	defer tty.Close()

	r.out.mu.Lock()
	var b strings.Builder
	fmt.Fprintf(&b, "permission: %s\n", title)
	for i, o := range req.Options {
		fmt.Fprintf(&b, "  %d) %s\n", i+1, o.Name)
	}
	b.WriteString("allow? [y]es, [a]lways, [n]o or a number: ")
	r.diag(yellow, strings.TrimSuffix(b.String(), "allow? [y]es, [a]lways, [n]o or a number: "))
	fmt.Fprint(r.o.Stderr, "allow? [y]es, [a]lways, [n]o or a number: ")
	r.out.pause = true
	r.out.mu.Unlock()
	defer func() {
		r.out.mu.Lock()
		r.out.pause = false
		r.out.mu.Unlock()
	}()

	lines := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(tty).ReadString('\n')
		lines <- s
	}()
	for {
		select {
		case <-ctx.Done():
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
		case s := <-lines:
			if id, ok := PickOption(req.Options, s); ok {
				return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(id)}, nil
			}
			if s == "" { // EOF
				return reject("no answer")
			}
			fmt.Fprint(r.o.Stderr, "answer y, a, n or an option number: ")
			go func() {
				s, _ := bufio.NewReader(tty).ReadString('\n')
				lines <- s
			}()
		}
	}
}

// Elicitation, files and terminals are not advertised, so a conforming
// agent never calls them.

func (c *client) CompleteElicitation(context.Context, acp.CompleteElicitationNotification) error {
	return nil
}

func (c *client) CreateElicitation(context.Context, acp.CreateElicitationRequest) (acp.CreateElicitationResponse, error) {
	return acp.CreateElicitationResponse{}, acp.NewMethodNotFound(acp.ClientMethodElicitationCreate)
}

func (c *client) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsReadTextFile)
}

func (c *client) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsWriteTextFile)
}

func (c *client) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalCreate)
}

func (c *client) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalKill)
}

func (c *client) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalOutput)
}

func (c *client) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalRelease)
}

func (c *client) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalWaitForExit)
}
