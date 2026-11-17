package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	acp "github.com/gfx-labs/go-acp"
)

// UI owns all state. Only the loop goroutine touches it; other goroutines
// hand it work with post.
type UI struct {
	opts  options
	sc    tcell.Screen
	conn  *acp.ClientSideConnection
	ctx   context.Context
	funcs chan func(*UI)
	quit  bool

	// connection and session
	agentName   string
	canLoad     bool
	session     acp.SessionId
	lastSession string
	title       string
	options     []acp.SessionConfigOption
	modeID      string
	commands    []acp.AvailableCommand
	plan        []acp.PlanEntry
	ctxUsed     int
	ctxSize     int
	cost        *acp.Cost
	turnUsage   *acp.Usage

	// transcript
	blocks []*block
	tools  map[acp.ToolCallId]*block
	scroll int // lines scrolled up from the bottom; 0 follows output

	// turn
	busy      bool
	turnStart time.Time
	queue     []string // soft interrupts, sent as one prompt when the turn ends
	urgent    bool     // the queue is sent with an interruption note after a cancel
	lastStop  string

	// config, keys and theme
	cfg       config
	keymap    keymap
	themeName string

	// session ops
	prevSession acp.SessionId // where /back returns after /fork or an in-window /btw

	// edit stats; statsSummary is a short form for the status bar
	editStats    editStats
	statsSummary string

	// input
	ed       editor
	pasting  bool
	pasteBuf strings.Builder // a paste, inserted when it ends
	images   []attachment    // images sent with the next prompt
	imagesOK bool            // the agent takes image prompts
	comp     *completion
	files    []string

	// overlays
	picker  *picker
	perms   []*permission
	ctrlC   time.Time
	flash   string
	flashAt time.Time

	expandAll    bool
	showThinking bool
	planHidden   bool

	// terminal
	focused  bool
	tick     *time.Ticker
	clipWant string // "text" or "image" while an OSC 52 read is pending
	clipAt   time.Time
}

func newUI(o options, cfg config) (*UI, error) {
	sc, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}
	if err := sc.Init(); err != nil {
		return nil, err
	}
	sc.EnablePaste()
	sc.EnableFocus()
	if !o.noMouse {
		sc.EnableMouse(tcell.MouseButtonEvents)
	}
	sc.SetCursorStyle(tcell.CursorStyleSteadyBar)
	sc.SetTitle("wist")
	u := &UI{
		opts:   o,
		sc:     sc,
		funcs:  make(chan func(*UI), 4096),
		tools:  map[acp.ToolCallId]*block{},
		cfg:    cfg,
		keymap: buildKeymap(cfg.keys),
		// Terminals without focus reporting never send events, so assume focus.
		focused: true,
	}
	u.themeName = cfg.theme
	if u.themeName == "" {
		u.themeName = "dark"
	}
	warnings := append(append([]string{}, cfg.warnings...), applyTheme(cfg.theme, cfg.colors)...)
	warnings = append(warnings, u.keymap.warnings...)
	for _, w := range warnings {
		u.addBlock(&block{kind: kindNotice, text: w})
	}
	return u, nil
}

func (u *UI) post(f func(*UI)) { u.funcs <- f }

func (u *UI) loop() error {
	defer u.sc.Fini()
	events := make(chan tcell.Event, 256)
	stop := make(chan struct{})
	defer close(stop)
	go u.sc.ChannelEvents(events, stop)
	u.tick = time.NewTicker(u.tickInterval())
	defer u.tick.Stop()
	// Streaming updates draw at most once a frame. A pending frame timer
	// fires the deferred draw.
	var lastDraw time.Time
	frame := time.NewTimer(time.Hour)
	frame.Stop()
	framePending := false
	u.draw()
	for !u.quit {
		fromFuncs := false
		select {
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			u.handleEvent(ev)
			// Drain whatever else is pending before drawing once.
			// HasPendingEvent catches events tcell has parsed but not yet
			// handed to the channel; the wait for them is bounded.
			for waits := 0; ; {
				select {
				case ev := <-events:
					u.handleEvent(ev)
					continue
				default:
				}
				if waits >= 5 || u.quit || !u.sc.HasPendingEvent() {
					break
				}
				waits++
				time.Sleep(time.Millisecond)
			}
		case f := <-u.funcs:
			fromFuncs = true
			f(u)
			for drained := false; !drained; {
				select {
				case f := <-u.funcs:
					f(u)
				default:
					drained = true
				}
			}
		case <-frame.C:
			framePending = false
		case <-u.tick.C:
			if !u.busy && len(u.perms) == 0 && u.flash == "" {
				continue
			}
			if u.flash != "" && time.Since(u.flashAt) > 3*time.Second {
				u.flash = ""
			}
		}
		if u.quit {
			break
		}
		if fromFuncs {
			if framePending {
				continue
			}
			if wait := frameWait(lastDraw, time.Now()); wait > 0 {
				framePending = true
				frame.Reset(wait)
				continue
			}
		}
		u.draw()
		lastDraw = time.Now()
	}
	return nil
}

// frameInterval caps streaming redraws at about 60 per second.
const frameInterval = 16 * time.Millisecond

// frameWait is how long to defer a draw so draws stay frameInterval apart.
func frameWait(last, now time.Time) time.Duration {
	if d := now.Sub(last); d < frameInterval {
		return frameInterval - d
	}
	return 0
}

func (u *UI) notify(msg string) {
	u.flash, u.flashAt = msg, time.Now()
}

// --- connection ---------------------------------------------------------

func (u *UI) connect(ctx context.Context) {
	u.ctx = ctx
	title := "wist"
	init, err := u.conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo:      &acp.Implementation{Name: "wist", Title: &title, Version: "0.1.0"},
		ClientCapabilities: acp.ClientCapabilities{
			Plan: &acp.PlanCapabilities{},
			Session: &acp.ClientSessionCapabilities{
				Compaction:    &acp.CompactionCapabilities{},
				ConfigOptions: &acp.SessionConfigOptionsCapabilities{Boolean: &acp.BooleanConfigOptionCapabilities{}},
				Notices:       &acp.NoticeCapabilities{},
			},
		},
	})
	if err != nil {
		u.post(func(u *UI) { u.errorf("initialize: %v (towa log: %s)", err, u.opts.log) })
		return
	}
	u.post(func(u *UI) {
		u.canLoad = init.AgentCapabilities.LoadSession
		u.imagesOK = init.AgentCapabilities.PromptCapabilities.Image
		if init.AgentInfo != nil {
			u.agentName = init.AgentInfo.Name + " " + init.AgentInfo.Version
		}
	})
	if u.opts.resume != "" {
		u.loadSession(acp.SessionId(u.opts.resume))
	} else {
		u.newSession()
	}
	if u.opts.prompt != "" {
		p := u.opts.prompt
		u.post(func(u *UI) { u.submit(p) })
	}
}

// newSession and loadSession run off the UI goroutine.
func (u *UI) newSession() {
	resp, err := u.conn.NewSession(u.ctx, acp.NewSessionRequest{Cwd: u.opts.cwd, McpServers: []acp.McpServer{}})
	u.post(func(u *UI) {
		if err != nil {
			u.errorf("new session: %v", err)
			return
		}
		u.resetSession(resp.SessionId)
		u.options = resp.ConfigOptions
		if resp.Modes != nil {
			u.modeID = string(resp.Modes.CurrentModeId)
		}
		u.info(fmt.Sprintf("new session %s in %s", resp.SessionId, u.opts.cwd))
	})
}

func (u *UI) loadSession(id acp.SessionId) {
	// Updates replayed during load arrive before the response, so the
	// transcript is reset first.
	done := make(chan struct{})
	u.post(func(u *UI) { u.resetSession(id); close(done) })
	<-done
	resp, err := u.conn.LoadSession(u.ctx, acp.LoadSessionRequest{SessionId: id, Cwd: u.opts.cwd, McpServers: []acp.McpServer{}})
	u.post(func(u *UI) {
		if err != nil {
			u.errorf("load session %s: %v", id, err)
			return
		}
		u.options = resp.ConfigOptions
		if resp.Modes != nil {
			u.modeID = string(resp.Modes.CurrentModeId)
		}
		u.info(fmt.Sprintf("resumed session %s", id))
	})
}

func (u *UI) resetSession(id acp.SessionId) {
	u.session = id
	u.lastSession = string(id)
	u.blocks = nil
	u.tools = map[acp.ToolCallId]*block{}
	u.plan = nil
	u.title = ""
	u.ctxUsed, u.ctxSize, u.cost, u.turnUsage = 0, 0, nil, nil
	u.scroll = 0
	u.editStats, u.statsSummary = editStats{}, ""
	u.sc.SetTitle("wist")
}

// --- transcript ---------------------------------------------------------

func (u *UI) addBlock(b *block) *block {
	b.dirty()
	u.blocks = append(u.blocks, b)
	return b
}

func (u *UI) info(s string) { u.addBlock(&block{kind: kindInfo, text: s}) }
func (u *UI) errorf(f string, a ...any) {
	u.addBlock(&block{kind: kindError, text: fmt.Sprintf(f, a...)})
}
func (u *UI) last() *block { return u.lastOf(-1) }
func (u *UI) lastOf(kind blockKind) *block {
	if len(u.blocks) == 0 {
		return nil
	}
	b := u.blocks[len(u.blocks)-1]
	if kind >= 0 && b.kind != kind {
		return nil
	}
	return b
}

// appendText streams a chunk into the trailing block of kind, or starts one.
func (u *UI) appendText(kind blockKind, c acp.ContentBlock) {
	text := contentText(c)
	if text == "" {
		return
	}
	b := u.lastOf(kind)
	if b == nil || b.queued {
		b = u.addBlock(&block{kind: kind})
	}
	b.text += text
	b.dirty()
}

func (u *UI) applyUpdate(n acp.SessionNotification) {
	if n.SessionId != u.session {
		return
	}
	up := n.Update
	switch {
	case up.AgentMessageChunk != nil:
		u.appendText(kindAgent, up.AgentMessageChunk.Content)
	case up.AgentThoughtChunk != nil:
		u.appendText(kindThought, up.AgentThoughtChunk.Content)
	case up.UserMessageChunk != nil:
		u.appendText(kindUser, up.UserMessageChunk.Content)
	case up.ToolCall != nil:
		t := up.ToolCall
		b := u.tools[t.ToolCallId]
		if b == nil {
			b = u.addBlock(&block{kind: kindTool, toolID: t.ToolCallId, started: time.Now()})
			u.tools[t.ToolCallId] = b
		}
		b.title, b.toolKind, b.status = t.Title, t.Kind, t.Status
		b.content, b.locations, b.rawInput = t.Content, t.Locations, t.RawInput
		if b.status == "" {
			b.status = acp.ToolCallStatusPending
		}
		u.finishTool(b)
		b.dirty()
	case up.ToolCallUpdate != nil:
		t := up.ToolCallUpdate
		b := u.tools[t.ToolCallId]
		if b == nil {
			b = u.addBlock(&block{kind: kindTool, toolID: t.ToolCallId, started: time.Now(), status: acp.ToolCallStatusPending})
			u.tools[t.ToolCallId] = b
		}
		if t.Title != nil {
			b.title = *t.Title
		}
		if t.Kind != nil {
			b.toolKind = *t.Kind
		}
		if t.Status != nil {
			b.status = *t.Status
		}
		if t.Content != nil {
			b.content = t.Content
		}
		if t.Locations != nil {
			b.locations = t.Locations
		}
		if t.RawInput != nil {
			b.rawInput = t.RawInput
		}
		u.finishTool(b)
		b.dirty()
	case up.Plan != nil:
		u.plan = up.Plan.Entries
	case up.PlanUpdate != nil:
		if items := up.PlanUpdate.Plan.Items; items != nil {
			u.plan = items.Entries
		}
	case up.PlanRemoved != nil:
		u.plan = nil
	case up.AvailableCommandsUpdate != nil:
		u.commands = up.AvailableCommandsUpdate.AvailableCommands
	case up.CurrentModeUpdate != nil:
		u.modeID = string(up.CurrentModeUpdate.CurrentModeId)
	case up.ConfigOptionUpdate != nil:
		u.options = up.ConfigOptionUpdate.ConfigOptions
	case up.SessionInfoUpdate != nil:
		if t := up.SessionInfoUpdate.Title; t != nil {
			u.title = *t
			u.sc.SetTitle("wist · " + *t)
		}
	case up.UsageUpdate != nil:
		u.ctxUsed, u.ctxSize, u.cost = up.UsageUpdate.Used, up.UsageUpdate.Size, up.UsageUpdate.Cost
	case up.Notice != nil:
		text := up.Notice.Title
		if d := up.Notice.Description; d != nil && *d != "" {
			text += ": " + *d
		}
		kind := kindNotice
		if up.Notice.Severity == acp.NoticeSeverityError {
			kind = kindError
		}
		u.addBlock(&block{kind: kind, text: text})
	case up.CompactionUpdate != nil:
		c := up.CompactionUpdate
		text := "compacting context…"
		switch c.Status {
		case acp.CompactionStatusCompleted:
			text = "context compacted"
		case acp.CompactionStatusFailed:
			text = "compaction failed"
			if c.Error != nil {
				text += ": " + *c.Error
			}
		case acp.CompactionStatusCancelled:
			text = "compaction cancelled"
		}
		u.addBlock(&block{kind: kindCompaction, text: text})
	}
}

func (u *UI) finishTool(b *block) {
	if (b.status == acp.ToolCallStatusCompleted || b.status == acp.ToolCallStatusFailed) && b.finished.IsZero() {
		b.finished = time.Now()
	}
	// Count diffs once the edit has landed, so streaming updates do not
	// rerun the line diff.
	if b.status == acp.ToolCallStatusCompleted {
		u.editStats.record(b.toolID, b.content)
		u.statsSummary = u.editStats.summary()
	}
}

// --- prompting ----------------------------------------------------------

func (u *UI) submit(text string) {
	text = strings.TrimRight(text, " \n")
	if strings.TrimSpace(text) == "" {
		if len(u.images) == 0 {
			return
		}
		text = "(image)"
	}
	u.ed.remember(text)
	if strings.HasPrefix(text, "/") && u.localCommand(text) {
		return
	}
	if u.session == "" {
		u.errorf("no session yet")
		return
	}
	if u.busy {
		u.queue = append(u.queue, text)
		u.addBlock(&block{kind: kindUser, text: text, queued: true})
		return
	}
	shown := text
	for _, a := range u.images {
		shown += "\n" + u.storeAttachment(a)
	}
	u.addBlock(&block{kind: kindUser, text: shown})
	u.startTurn(text)
}

func (u *UI) startTurn(text string) {
	u.busy = true
	u.turnStart = time.Now()
	u.scroll = 0
	u.lastStop = ""
	prompt := u.promptBlocks(text)
	id := u.session
	go func() {
		resp, err := u.conn.Prompt(u.ctx, acp.PromptRequest{SessionId: id, Prompt: prompt})
		u.post(func(u *UI) { u.endTurn(id, resp, err) })
	}()
}

// promptBlocks turns the text into a prompt, attaching @mentioned files
// as resource links.
func (u *UI) promptBlocks(text string) []acp.ContentBlock {
	blocks := []acp.ContentBlock{{Text: &acp.ContentBlockText{Text: text}}}
	blocks = append(blocks, u.imageBlocks(text)...)
	seen := map[string]bool{}
	for _, f := range strings.Fields(text) {
		if !strings.HasPrefix(f, "@") || len(f) < 2 {
			continue
		}
		p := strings.TrimRight(f[1:], ".,;:!?)\"'")
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(u.opts.cwd, p)
		}
		if seen[abs] {
			continue
		}
		if imageExts[strings.ToLower(filepath.Ext(abs))] {
			continue // sent as an image block
		}
		if st, err := os.Stat(abs); err == nil && !st.IsDir() {
			seen[abs] = true
			blocks = append(blocks, acp.ContentBlock{ResourceLink: &acp.ContentBlockResourceLink{Name: p, Uri: "file://" + abs}})
		}
	}
	return blocks
}

func (u *UI) endTurn(id acp.SessionId, resp acp.PromptResponse, err error) {
	if id != u.session {
		return
	}
	u.busy = false
	elapsed := time.Since(u.turnStart)
	for _, b := range u.tools {
		if b.status == acp.ToolCallStatusInProgress || b.status == acp.ToolCallStatusPending {
			b.status = acp.ToolCallStatusFailed
			b.finished = time.Now()
			b.dirty()
		}
	}
	if err != nil {
		u.errorf("%v", err)
	} else {
		u.lastStop = string(resp.StopReason)
		u.turnUsage = resp.Usage
		msg := fmt.Sprintf("%s in %s", resp.StopReason, fmtDuration(elapsed))
		if us := resp.Usage; us != nil {
			msg += fmt.Sprintf(" · ↑%s ↓%s", fmtTokens(us.InputTokens), fmtTokens(us.OutputTokens))
			if us.CachedReadTokens != nil && *us.CachedReadTokens > 0 {
				msg += " · cache " + fmtTokens(*us.CachedReadTokens)
			}
		}
		if resp.StopReason != acp.StopReasonEndTurn {
			u.addBlock(&block{kind: kindNotice, text: msg})
		} else {
			u.info(msg)
		}
		if len(u.queue) == 0 {
			u.alert("turn finished: " + msg)
		}
	}
	u.sendQueued()
}

func (u *UI) cancelTurn() {
	if !u.busy {
		return
	}
	id := u.session
	go func() { _ = u.conn.Cancel(u.ctx, acp.CancelNotification{SessionId: id}) }()
	// Answer pending permission prompts as cancelled.
	for _, p := range u.perms {
		p.answer <- acp.NewRequestPermissionOutcomeCancelled()
	}
	u.perms = nil
	u.notify("interrupting…")
}

// --- permissions --------------------------------------------------------

type permission struct {
	req    acp.RequestPermissionRequest
	answer chan acp.RequestPermissionOutcome
	sel    int
}

func (u *UI) askPermission(p acp.RequestPermissionRequest, answer chan acp.RequestPermissionOutcome) {
	u.perms = append(u.perms, &permission{req: p, answer: answer})
	title := "permission requested"
	if p.ToolCall.Title != nil {
		title += ": " + *p.ToolCall.Title
	}
	u.alert(title)
}

func (u *UI) dropPermission(answer chan acp.RequestPermissionOutcome) {
	u.perms = slices.DeleteFunc(u.perms, func(p *permission) bool { return p.answer == answer })
}

func (u *UI) answerPermission(i int) {
	p := u.perms[0]
	if i < 0 {
		p.answer <- acp.NewRequestPermissionOutcomeCancelled()
	} else {
		p.answer <- acp.NewRequestPermissionOutcomeSelected(p.req.Options[i].OptionId)
	}
	u.perms = u.perms[1:]
}

// --- config options -----------------------------------------------------

func (u *UI) option(match func(id string, cat acp.SessionConfigOptionCategory) bool) *acp.SessionConfigOptionSelect {
	for _, o := range u.options {
		if s := o.Select; s != nil {
			var cat acp.SessionConfigOptionCategory
			if s.Category != nil {
				cat = *s.Category
			}
			if match(string(s.Id), cat) {
				return s
			}
		}
	}
	return nil
}

func (u *UI) modelOption() *acp.SessionConfigOptionSelect {
	return u.option(func(id string, c acp.SessionConfigOptionCategory) bool {
		return c == acp.SessionConfigOptionCategoryModel || id == "model"
	})
}

func (u *UI) modeOption() *acp.SessionConfigOptionSelect {
	return u.option(func(id string, c acp.SessionConfigOptionCategory) bool {
		return c == acp.SessionConfigOptionCategoryMode || id == "mode"
	})
}

func selectChoices(s *acp.SessionConfigOptionSelect) []acp.SessionConfigSelectOption {
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

func currentName(s *acp.SessionConfigOptionSelect) string {
	if s == nil {
		return ""
	}
	for _, c := range selectChoices(s) {
		if c.Value == s.CurrentValue {
			return c.Name
		}
	}
	return string(s.CurrentValue)
}

func (u *UI) setOption(id acp.SessionConfigId, value acp.SessionConfigValueId) {
	sid := u.session
	go func() {
		resp, err := u.conn.SetSessionConfigOption(u.ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: sid, ConfigId: id, Value: value,
		}})
		u.post(func(u *UI) {
			if err != nil {
				u.errorf("set %s: %v", id, err)
				return
			}
			u.options = resp.ConfigOptions
			u.notify(fmt.Sprintf("%s → %s", id, value))
		})
	}()
}

func (u *UI) setBool(id acp.SessionConfigId, value bool) {
	sid := u.session
	go func() {
		resp, err := u.conn.SetSessionConfigOption(u.ctx, acp.SetSessionConfigOptionRequest{Boolean: &acp.SetSessionConfigOptionBoolean{
			SessionId: sid, ConfigId: id, Value: value,
		}})
		u.post(func(u *UI) {
			if err != nil {
				u.errorf("set %s: %v", id, err)
				return
			}
			u.options = resp.ConfigOptions
			u.notify(fmt.Sprintf("%s → %v", id, value))
		})
	}()
}

func (u *UI) cycleMode() {
	m := u.modeOption()
	if m == nil {
		u.notify("this agent has no modes")
		return
	}
	choices := selectChoices(m)
	for i, c := range choices {
		if c.Value == m.CurrentValue {
			u.setOption(m.Id, choices[(i+1)%len(choices)].Value)
			return
		}
	}
}

func (u *UI) pickOption(s *acp.SessionConfigOptionSelect) {
	var items []pickItem
	sel := 0
	for i, c := range selectChoices(s) {
		detail := ""
		if c.Description != nil {
			detail = *c.Description
		}
		if c.Value == s.CurrentValue {
			sel = i
		}
		items = append(items, pickItem{label: c.Name, detail: detail, value: string(c.Value), current: c.Value == s.CurrentValue})
	}
	id := s.Id
	u.openPicker(s.Name, items, sel, func(it pickItem) { u.setOption(id, acp.SessionConfigValueId(it.value)) })
}

func (u *UI) pickConfig() {
	var items []pickItem
	for _, o := range u.options {
		switch {
		case o.Select != nil:
			items = append(items, pickItem{label: o.Select.Name, detail: currentName(o.Select), value: "s:" + string(o.Select.Id)})
		case o.Boolean != nil:
			items = append(items, pickItem{label: o.Boolean.Name, detail: fmt.Sprint(o.Boolean.CurrentValue), value: "b:" + string(o.Boolean.Id)})
		}
	}
	if len(items) == 0 {
		u.notify("no config options")
		return
	}
	u.openPicker("session options", items, 0, func(it pickItem) {
		kind, id, _ := strings.Cut(it.value, ":")
		for _, o := range u.options {
			if kind == "s" && o.Select != nil && string(o.Select.Id) == id {
				u.pickOption(o.Select)
			}
			if kind == "b" && o.Boolean != nil && string(o.Boolean.Id) == id {
				u.setBool(o.Boolean.Id, !o.Boolean.CurrentValue)
			}
		}
	})
}

func (u *UI) pickSession() {
	cwd := u.opts.cwd
	go func() {
		resp, err := u.conn.ListSessions(u.ctx, acp.ListSessionsRequest{Cwd: &cwd})
		u.post(func(u *UI) {
			if err != nil {
				u.errorf("list sessions: %v", err)
				return
			}
			var items []pickItem
			sessions := resp.Sessions
			slices.SortFunc(sessions, func(a, b acp.SessionInfo) int {
				return strings.Compare(deref(b.UpdatedAt), deref(a.UpdatedAt))
			})
			for _, s := range sessions {
				label := string(s.SessionId)
				if s.Title != nil && *s.Title != "" {
					label = *s.Title
				}
				detail := ""
				if s.UpdatedAt != nil {
					if t, err := time.Parse(time.RFC3339Nano, *s.UpdatedAt); err == nil {
						detail = ago(t)
					}
				}
				items = append(items, pickItem{label: label, detail: detail, value: string(s.SessionId), current: s.SessionId == u.session})
			}
			if len(items) == 0 {
				u.notify("no stored sessions for " + cwd)
				return
			}
			u.openPicker("resume session · ctrl+d delete · ctrl+a archive · ctrl+r rename", items, 0, func(it pickItem) {
				if u.busy {
					u.notify("wait for the turn to end first")
					return
				}
				go u.loadSession(acp.SessionId(it.value))
			})
			u.picker.keys = func(ev *tcell.EventKey, it pickItem) bool {
				id := acp.SessionId(it.value)
				switch ev.Key() {
				case tcell.KeyCtrlD:
					u.picker = nil
					u.confirmDelete(id, it.label)
				case tcell.KeyCtrlA:
					u.picker = nil
					u.archiveSession(id)
				case tcell.KeyCtrlR:
					u.picker = nil
					u.ed.set("/rename --id " + it.value + " ")
				default:
					return false
				}
				return true
			}
		})
	}()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return t.Format("2006-01-02")
}
