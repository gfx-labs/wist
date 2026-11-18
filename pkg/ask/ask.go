// Package ask is wist's non-interactive single-turn mode: it runs one
// prompt against the agent, streams the reply to stdout and progress to
// stderr, and exits. It never draws a terminal screen.
package ask

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"

	acp "github.com/gfx-labs/go-acp"
	"github.com/gfx-labs/wist/pkg/agent"
	"github.com/gfx-labs/wist/pkg/attach"
)

// Options configures one wist ask run.
type Options struct {
	Bin    string   // agent binary (towa unless Agent is set)
	Agent  string   // registry agent id; empty means towa
	Args   []string // agent arguments
	Cwd    string   // session working directory
	Log    string   // file that receives the agent's stderr
	Prompt string   // prompt text
	Images []string // image files to attach
	Resume string   // session id, or "last"
	NoSave bool     // delete the session afterwards
	Model  string   // model option value or name
	Mode   string   // mode option value ("ask", "auto")
	System string   // system prompt override
	// Timeout is the overall wall clock limit; 0 means none.
	Timeout           time.Duration
	JSON              bool // print one JSON object at the end
	Quiet             bool // print no assistant text
	NoColor           bool // plain stderr
	PromptPermissions bool // ask for permissions on the terminal even with JSON/Quiet

	Stdout, Stderr io.Writer // default os.Stdout, os.Stderr
	// TTY overrides terminal detection (tests). nil means detect.
	TTY *TTYInfo
}

// TTYInfo says which standard streams are terminals.
type TTYInfo struct{ Stdin, Stdout, Stderr bool }

// Usage is the token usage of the turn.
type Usage struct {
	InputTokens      int  `json:"input_tokens"`
	OutputTokens     int  `json:"output_tokens"`
	CacheReadTokens  *int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int `json:"cache_write_tokens,omitempty"`
}

// ToolCall summarizes one tool call of the turn.
type ToolCall struct {
	Name   string `json:"name"`
	Kind   string `json:"kind,omitempty"`
	Status string `json:"status"`
}

// Result is the outcome of a run; it is also the --json object.
//
// Steps counts the agent's model responses in the turn: a step starts with
// the first message text or tool call, and again with every message text
// that follows a tool call. A reply without tools is 1 step; text, tools,
// then a final answer is 2.
type Result struct {
	Output      string     `json:"output"`
	FinalOutput string     `json:"final_output"`
	ExitCode    int        `json:"exit_code"`
	Model       string     `json:"model"`
	SessionID   string     `json:"session_id"`
	StopReason  string     `json:"stop_reason"`
	Steps       int        `json:"steps"`
	Usage       Usage      `json:"usage"`
	ToolCalls   []ToolCall `json:"tool_calls"`
	Error       string     `json:"error,omitempty"`
}

// Interrupted is the context cause the caller sets when a signal stops
// the run.
type Interrupted struct{ Signal os.Signal }

func (i Interrupted) Error() string { return "interrupted by " + i.Signal.String() }

// Code is the exit status for the signal: 143 for SIGTERM, else 130.
func (i Interrupted) Code() int {
	if i.Signal == syscall.SIGTERM {
		return ExitTerminated
	}
	return ExitInterrupted
}

var errTimeout = errors.New("timed out")

// Run runs the prompt. Cancel ctx with an Interrupted cause
// (context.WithCancelCause) to interrupt: Run cancels the turn, waits
// briefly and returns that Interrupted as the error. Other failures are
// returned as errors and recorded in the Result.
func Run(ctx context.Context, o Options) (Result, error) {
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.TTY == nil {
		o.TTY = &TTYInfo{Stdin: isTerminal(os.Stdin), Stdout: isTerminal(os.Stdout), Stderr: isTerminal(os.Stderr)}
	}
	r := &runner{o: o, tools: map[acp.ToolCallId]*tool{}}
	r.out = newProgress(o.Stderr, o.TTY.Stderr, !o.NoColor && o.TTY.Stderr, o.TTY.Stdout && !o.JSON && !o.Quiet)
	defer r.out.stop()
	res, err := r.run(ctx)
	res.ExitCode = ExitCode(res.StopReason, err)
	if err != nil && res.Error == "" {
		res.Error = err.Error()
	}
	return res, err
}

type tool struct {
	title, kind, status string
	plus, minus         int
	printed             string // status already printed
}

type runner struct {
	o     Options
	out   *progress
	conn  *acp.ClientSideConnection
	init  acp.InitializeResponse
	sid   acp.SessionId
	opts  []acp.SessionConfigOption
	tools map[acp.ToolCallId]*tool
	order []acp.ToolCallId

	output   strings.Builder
	segment  strings.Builder // text since the last tool call
	last     int             // 0 none, 1 text, 2 tool
	steps    int
	midLine  bool // stdout text does not end in a newline
	inTurn   bool
	lastPlan string
}

func (r *runner) run(ctx context.Context) (Result, error) {
	var res Result
	var images []attach.Image
	for _, p := range r.o.Images {
		im, err := attach.File(p)
		if err != nil {
			return res, &UsageError{"--image: " + err.Error()}
		}
		images = append(images, im)
	}

	ao := agent.Options{Bin: r.o.Bin, Agent: r.o.Agent, Args: r.o.Args, Cwd: r.o.Cwd, Log: r.o.Log}
	if err := agent.Resolve(&ao, func(s string) { r.out.line(dim, s) }); err != nil {
		return res, err
	}
	// The connection outlives ctx so a cancelled run can still send
	// session/cancel and wait for the turn to end.
	connCtx, stopConn := context.WithCancel(context.Background())
	defer stopConn()
	proc, err := agent.Spawn(connCtx, ao)
	if err != nil {
		return res, err
	}
	defer proc.Stop(2 * time.Second)
	r.conn = acp.NewClientSideConnection(&client{r: r}, proc.Stdin, proc.Stdout, nil)
	served := make(chan error, 1)
	go func() { served <- r.conn.Serve(connCtx) }()

	if r.o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, r.o.Timeout, errTimeout)
		defer cancel()
	}
	// call runs a setup request, aborting it when ctx ends or the agent dies.
	call := func(f func(context.Context) error) error {
		cctx, cancel := context.WithCancel(connCtx)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- f(cctx) }()
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return context.Cause(ctx)
		case err := <-served:
			return r.died(err)
		}
	}

	r.out.spin("starting agent")
	title := "wist"
	err = call(func(c context.Context) (err error) {
		r.init, err = r.conn.Initialize(c, acp.InitializeRequest{
			ProtocolVersion: acp.ProtocolVersionNumber,
			ClientInfo:      &acp.Implementation{Name: "wist", Title: &title, Version: "0.1.0"},
			ClientCapabilities: acp.ClientCapabilities{
				Plan: &acp.PlanCapabilities{},
				Session: &acp.ClientSessionCapabilities{
					ConfigOptions: &acp.SessionConfigOptionsCapabilities{Boolean: &acp.BooleanConfigOptionCapabilities{}},
					Notices:       &acp.NoticeCapabilities{},
				},
			},
		})
		return err
	})
	if err != nil {
		return res, fmt.Errorf("initialize: %w (agent log: %s)", err, r.o.Log)
	}
	caps := r.init.AgentCapabilities
	if len(images) > 0 && !caps.PromptCapabilities.Image {
		return res, errors.New("this agent does not accept images")
	}
	var sys *acp.SystemPrompt
	if r.o.System != "" {
		sp := caps.SessionCapabilities.SystemPrompt
		if sp == nil || sp.Mode == nil || sp.Mode.Override == nil {
			return res, errors.New("--system: this agent does not support replacing the system prompt")
		}
		sys = &acp.SystemPrompt{Mode: acp.SystemPromptModeOverride, Content: []acp.ContentBlock{{Text: &acp.ContentBlockText{Text: r.o.System}}}}
	}

	if r.o.Resume != "" {
		if !caps.LoadSession {
			return res, errors.New("--resume: this agent cannot load sessions")
		}
		id := r.o.Resume
		if id == "last" {
			var list acp.ListSessionsResponse
			cwd := r.o.Cwd
			if err := call(func(c context.Context) (err error) {
				list, err = r.conn.ListSessions(c, acp.ListSessionsRequest{Cwd: &cwd})
				return err
			}); err != nil {
				return res, fmt.Errorf("list sessions: %w", err)
			}
			id = LatestSession(list.Sessions)
			if id == "" {
				return res, fmt.Errorf("--resume last: no sessions for %s", cwd)
			}
		}
		r.sid = acp.SessionId(id)
		// Replayed history is ignored until the prompt is sent.
		var resp acp.LoadSessionResponse
		if err := call(func(c context.Context) (err error) {
			resp, err = r.conn.LoadSession(c, acp.LoadSessionRequest{SessionId: r.sid, Cwd: r.o.Cwd, McpServers: []acp.McpServer{}, SystemPrompt: sys})
			return err
		}); err != nil {
			return res, fmt.Errorf("load session %s: %w", id, err)
		}
		r.opts = resp.ConfigOptions
	} else {
		var resp acp.NewSessionResponse
		if err := call(func(c context.Context) (err error) {
			resp, err = r.conn.NewSession(c, acp.NewSessionRequest{Cwd: r.o.Cwd, McpServers: []acp.McpServer{}, SystemPrompt: sys})
			return err
		}); err != nil {
			return res, fmt.Errorf("new session: %w", err)
		}
		r.sid = resp.SessionId
		r.opts = resp.ConfigOptions
	}
	res.SessionID = string(r.sid)
	if r.o.NoSave {
		defer r.deleteSession(&res)
	}

	for _, set := range []struct{ flag, value string }{{"model", r.o.Model}, {"mode", r.o.Mode}} {
		if set.value == "" {
			continue
		}
		opt := findOption(r.opts, set.flag)
		if opt == nil {
			return res, fmt.Errorf("--%s: this agent has no %s option", set.flag, set.flag)
		}
		v, err := matchChoice(opt, set.value)
		if err != nil {
			return res, &UsageError{"--" + set.flag + ": " + err.Error()}
		}
		if err := call(func(c context.Context) error {
			resp, err := r.conn.SetSessionConfigOption(c, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{SessionId: r.sid, ConfigId: opt.Id, Value: v}})
			if err == nil {
				r.setOpts(resp.ConfigOptions)
			}
			return err
		}); err != nil {
			return res, fmt.Errorf("set %s: %w", set.flag, err)
		}
	}
	res.Model = r.model()

	prompt := []acp.ContentBlock{{Text: &acp.ContentBlockText{Text: r.o.Prompt}}}
	for _, im := range images {
		prompt = append(prompt, im.Block())
	}
	r.out.mu.Lock()
	r.inTurn = true
	r.out.mu.Unlock()
	r.out.spin("thinking")
	start := time.Now()
	type answer struct {
		resp acp.PromptResponse
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		resp, err := r.conn.Prompt(connCtx, acp.PromptRequest{SessionId: r.sid, Prompt: prompt})
		done <- answer{resp, err}
	}()
	var a answer
	var stopErr error
	select {
	case a = <-done:
	case err := <-served:
		r.finish(&res)
		return res, r.died(err)
	case <-ctx.Done():
		stopErr = context.Cause(ctx)
		r.out.line(yellow, "cancelling…")
		_ = r.conn.Cancel(connCtx, acp.CancelNotification{SessionId: r.sid})
		select {
		case a = <-done:
		case <-served:
		case <-time.After(3 * time.Second):
		}
	}
	r.finish(&res)
	if stopErr != nil {
		res.StopReason = string(a.resp.StopReason)
		if errors.Is(stopErr, errTimeout) {
			stopErr = fmt.Errorf("timed out after %s", r.o.Timeout)
		}
		return res, stopErr
	}
	if a.err != nil {
		return res, a.err
	}
	res.StopReason = string(a.resp.StopReason)
	if u := a.resp.Usage; u != nil {
		res.Usage = Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CachedReadTokens, CacheWriteTokens: u.CachedWriteTokens}
	}
	if a.resp.StopReason == acp.StopReasonEndTurn {
		res.FinalOutput = strings.TrimSpace(r.segmentText())
	}
	msg := fmt.Sprintf("%s in %.1fs", a.resp.StopReason, time.Since(start).Seconds())
	if a.resp.Usage != nil {
		msg += fmt.Sprintf(" · ↑%d ↓%d", res.Usage.InputTokens, res.Usage.OutputTokens)
		if c := res.Usage.CacheReadTokens; c != nil && *c > 0 {
			msg += fmt.Sprintf(" · cache %d", *c)
		}
	}
	if res.Model != "" {
		msg += " · " + res.Model
	}
	if a.resp.StopReason != acp.StopReasonEndTurn {
		r.out.line(red, msg)
		return res, fmt.Errorf("stopped: %s", a.resp.StopReason)
	}
	r.out.line(dim, msg)
	return res, nil
}

func (r *runner) died(err error) error {
	if err == nil {
		err = io.EOF
	}
	return fmt.Errorf("connection to the agent closed: %v (see %s)", err, r.o.Log)
}

// finish ends the turn: the stdout line is closed and tool calls copied
// into the result.
func (r *runner) finish(res *Result) {
	r.out.mu.Lock()
	defer r.out.mu.Unlock()
	r.inTurn = false
	if r.midLine && r.showText() {
		fmt.Fprintln(r.o.Stdout)
		r.midLine = false
	}
	res.Output = r.output.String()
	res.Steps = r.steps
	res.ToolCalls = []ToolCall{}
	for _, id := range r.order {
		t := r.tools[id]
		res.ToolCalls = append(res.ToolCalls, ToolCall{Name: t.title, Kind: t.kind, Status: t.status})
	}
}

func (r *runner) segmentText() string {
	r.out.mu.Lock()
	defer r.out.mu.Unlock()
	return r.segment.String()
}

func (r *runner) deleteSession(res *Result) {
	if r.init.AgentCapabilities.SessionCapabilities.Delete == nil {
		r.out.line(yellow, "--no-save: this agent cannot delete sessions; session "+string(r.sid)+" was kept")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := r.conn.DeleteSession(ctx, acp.DeleteSessionRequest{SessionId: r.sid}); err != nil {
		r.out.line(yellow, "--no-save: delete session: "+err.Error())
		return
	}
	res.SessionID = ""
}

func (r *runner) setOpts(o []acp.SessionConfigOption) {
	r.out.mu.Lock()
	r.opts = o
	r.out.mu.Unlock()
}

func (r *runner) model() string {
	r.out.mu.Lock()
	defer r.out.mu.Unlock()
	if m := findOption(r.opts, "model"); m != nil {
		return string(m.CurrentValue)
	}
	return ""
}

func (r *runner) showText() bool { return !r.o.JSON && !r.o.Quiet }

// LatestSession returns the id of the most recently updated session, or
// "" for none. Sessions without updatedAt sort last.
func LatestSession(s []acp.SessionInfo) string {
	if len(s) == 0 {
		return ""
	}
	s = slices.Clone(s)
	slices.SortStableFunc(s, func(a, b acp.SessionInfo) int {
		return strings.Compare(deref(b.UpdatedAt), deref(a.UpdatedAt))
	})
	return string(s[0].SessionId)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// findOption returns the select option for kind ("model" or "mode") by
// category, falling back to the id.
func findOption(opts []acp.SessionConfigOption, kind string) *acp.SessionConfigOptionSelect {
	var byID *acp.SessionConfigOptionSelect
	for _, o := range opts {
		s := o.Select
		if s == nil {
			continue
		}
		if s.Category != nil && string(*s.Category) == kind {
			return s
		}
		if string(s.Id) == kind && byID == nil {
			byID = s
		}
	}
	return byID
}

func choices(s *acp.SessionConfigOptionSelect) []acp.SessionConfigSelectOption {
	if s.Options.Ungrouped != nil {
		return *s.Options.Ungrouped
	}
	var out []acp.SessionConfigSelectOption
	if s.Options.Grouped != nil {
		for _, g := range *s.Options.Grouped {
			out = append(out, g.Options...)
		}
	}
	return out
}

// matchChoice finds want among the option's values (exact, then
// case-insensitive value or name, then a unique value suffix such as
// "gpt-6.2-sol" for "codex:pro/gpt-6.2-sol").
func matchChoice(s *acp.SessionConfigOptionSelect, want string) (acp.SessionConfigValueId, error) {
	cs := choices(s)
	for _, c := range cs {
		if string(c.Value) == want {
			return c.Value, nil
		}
	}
	for _, c := range cs {
		if strings.EqualFold(string(c.Value), want) || strings.EqualFold(c.Name, want) {
			return c.Value, nil
		}
	}
	var hits []acp.SessionConfigValueId
	for _, c := range cs {
		if strings.HasSuffix(strings.ToLower(string(c.Value)), "/"+strings.ToLower(want)) {
			hits = append(hits, c.Value)
		}
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	var names []string
	for _, c := range cs {
		names = append(names, string(c.Value))
	}
	return "", fmt.Errorf("unknown value %q; choices: %s", want, strings.Join(names, ", "))
}
