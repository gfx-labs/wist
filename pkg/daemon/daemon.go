package daemon

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	acp "github.com/gfx-labs/go-acp"
	"github.com/gfx-labs/wist/pkg/agent"
)

// Options configures a daemon.
type Options struct {
	Agent  agent.Options // the agent process; already resolved
	Socket string        // unix socket to listen on
	DB     string        // SQLite database path
	Logger *slog.Logger
}

// Daemon multiplexes one agent connection between viewers.
type Daemon struct {
	opts  Options
	store *Store
	log   *slog.Logger
	ctx   context.Context
	// shutdown ends ctx, which stops the daemon.
	shutdown context.CancelFunc

	mu       sync.Mutex
	up       *upstream
	sessions map[acp.SessionId]*session
}

// upstream is one agent process and the ACP connection to it.
type upstream struct {
	proc   *agent.Process
	conn   *acp.ClientSideConnection
	init   acp.InitializeResponse
	cancel context.CancelFunc
}

// session is the daemon's view of one agent session.
type session struct {
	id  acp.SessionId
	cwd string
	// up is the agent connection the session is loaded in; a session is
	// live only while up is the daemon's current upstream.
	up      *upstream
	config  []acp.SessionConfigOption
	modes   *acp.SessionModeState
	busy    bool
	viewers map[*viewer]struct{}
	perms   map[*permission]struct{}
	// quiet is set while the agent replays the session into the store;
	// attached viewers already have that transcript.
	quiet bool
	text  textIndex
	turn  sync.Mutex // orders prompts
	load  sync.Mutex // orders loads
}

// Run listens on o.Socket and serves viewers until ctx ends. It fails when
// another daemon holds the socket.
func Run(ctx context.Context, o Options) error {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if err := os.MkdirAll(filepath.Dir(o.Socket), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(o.Socket+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another wist daemon is serving %s", o.Socket)
	}
	if err := os.Remove(o.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	ln, err := net.Listen("unix", o.Socket)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(o.Socket) }()
	store, err := OpenStore(o.DB)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer func() { _ = store.Close() }()
	ctx, shutdown := context.WithCancel(ctx)
	defer shutdown()
	d := &Daemon{opts: o, store: store, log: log, ctx: ctx, shutdown: shutdown, sessions: map[acp.SessionId]*session{}}
	if _, err := d.agent(); err != nil {
		_ = ln.Close()
		return err
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	log.Info("listening", "socket", o.Socket, "agent", o.Agent.Bin)
	var wg sync.WaitGroup
	for {
		c, err := ln.Accept()
		if err != nil {
			break
		}
		wg.Go(func() { d.serve(c) })
	}
	wg.Wait()
	d.mu.Lock()
	up := d.up
	d.up = nil
	d.mu.Unlock()
	if up != nil {
		up.stop()
	}
	return nil
}

// agent returns the running agent, starting it when there is none.
func (d *Daemon) agent() (*upstream, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.up != nil {
		return d.up, nil
	}
	up, err := d.start()
	if err != nil {
		return nil, err
	}
	d.up = up
	return up, nil
}

func (d *Daemon) start() (*upstream, error) {
	ctx, cancel := context.WithCancel(d.ctx)
	proc, err := agent.Spawn(ctx, d.opts.Agent)
	if err != nil {
		cancel()
		return nil, err
	}
	up := &upstream{proc: proc, cancel: cancel}
	up.conn = acp.NewClientSideConnection(&client{d: d, up: up}, proc.Stdin, proc.Stdout, d.log)
	go func() {
		err := up.conn.Serve(ctx)
		d.log.Info("agent connection closed", "err", err)
		d.mu.Lock()
		if d.up == up {
			d.up = nil
		}
		d.mu.Unlock()
	}()
	title := "wist"
	up.init, err = up.conn.Initialize(ctx, acp.InitializeRequest{
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
		up.stop()
		return nil, fmt.Errorf("initialize agent: %w (log: %s)", err, d.opts.Agent.Log)
	}
	return up, nil
}

func (u *upstream) stop() {
	u.cancel()
	u.proc.Stop(2 * time.Second)
}

// reload restarts the agent so it reads its config again. Viewers load
// their sessions again afterwards.
func (d *Daemon) reload() error {
	d.mu.Lock()
	for _, s := range d.sessions {
		if s.busy {
			d.mu.Unlock()
			return fmt.Errorf("a turn is running in session %s", s.id)
		}
	}
	old := d.up
	d.up = nil
	d.mu.Unlock()
	if old != nil {
		old.stop()
	}
	_, err := d.agent()
	return err
}

// sessionLocked returns the session, creating it. d.mu must be held.
func (d *Daemon) sessionLocked(id acp.SessionId, cwd string) *session {
	s := d.sessions[id]
	if s == nil {
		s = &session{id: id, viewers: map[*viewer]struct{}{}, perms: map[*permission]struct{}{}}
		d.sessions[id] = s
	}
	if cwd != "" {
		s.cwd = cwd
	}
	return s
}

// attachLocked adds v to s's viewers and asks it any pending permission
// question. d.mu must be held.
func (d *Daemon) attachLocked(s *session, v *viewer) {
	s.viewers[v] = struct{}{}
	v.sessions[s.id] = s
	for p := range s.perms {
		go p.ask(v)
	}
}

// broadcastLocked sends an update to the session's viewers except skip.
// d.mu must be held.
func (d *Daemon) broadcastLocked(s *session, skip *viewer, u acp.SessionUpdate) {
	if s.quiet {
		return
	}
	n := acp.SessionNotification{SessionId: s.id, Update: u}
	for v := range s.viewers {
		if v != skip {
			v.notify(n)
		}
	}
}

// recordLocked stores an update and indexes its text. d.mu must be held.
func (d *Daemon) recordLocked(s *session, u acp.SessionUpdate) {
	switch {
	case u.ConfigOptionUpdate != nil:
		s.config = u.ConfigOptionUpdate.ConfigOptions
	case u.CurrentModeUpdate != nil:
		if s.modes != nil {
			s.modes.CurrentModeId = u.CurrentModeUpdate.CurrentModeId
		}
	}
	if err := d.store.Append(d.ctx, s.id, u); err != nil {
		d.log.Warn("store update", "session", s.id, "err", err)
	}
	if kind, text, ok := messageText(u); ok {
		if done, flush := s.text.add(kind, text); flush {
			d.index(s.id, done)
		}
	} else if done, flush := s.text.flush(); flush {
		d.index(s.id, done)
	}
}

func (d *Daemon) flushLocked(s *session) {
	if done, flush := s.text.flush(); flush {
		d.index(s.id, done)
	}
}

func (d *Daemon) index(id acp.SessionId, m message) {
	if err := d.store.Index(d.ctx, id, m.kind, m.text); err != nil {
		d.log.Warn("index message", "session", id, "err", err)
	}
}

// serve speaks ACP to one viewer until it disconnects.
func (d *Daemon) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	v := &viewer{d: d, sessions: map[acp.SessionId]*session{}}
	v.conn = acp.NewAgentSideConnection(v, c, c, d.log)
	for method, h := range map[string]acp.MethodHandler{
		MethodSessions: v.listStored,
		MethodSearch:   v.search,
		MethodReload:   v.reload,
		MethodShutdown: v.stopDaemon,
	} {
		if err := v.conn.RegisterExtensionRequest(method, h); err != nil {
			d.log.Error("register extension", "method", method, "err", err)
			return
		}
	}
	err := v.conn.Serve(d.ctx)
	d.log.Debug("viewer closed", "err", err)
	d.mu.Lock()
	for _, s := range v.sessions {
		delete(s.viewers, v)
	}
	d.mu.Unlock()
}

// permission is one session/request_permission from the agent, asked of
// every viewer of the session. The first answer wins; the others are
// cancelled.
type permission struct {
	req    acp.RequestPermissionRequest
	ctx    context.Context
	answer chan acp.RequestPermissionOutcome
}

func (p *permission) ask(v *viewer) {
	resp, err := v.conn.RequestPermission(p.ctx, p.req)
	if err != nil {
		return
	}
	select {
	case p.answer <- resp.Outcome:
	default:
	}
}

// client is the daemon's ACP client end of the agent connection.
type client struct {
	nopClient
	d  *Daemon
	up *upstream
}

var _ acp.Client = (*client)(nil)

func (c *client) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	d := c.d
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.sessionLocked(n.SessionId, "")
	d.recordLocked(s, n.Update)
	d.broadcastLocked(s, nil, n.Update)
	return nil
}

func (c *client) RequestPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	d := c.d
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p := &permission{req: req, ctx: ctx, answer: make(chan acp.RequestPermissionOutcome, 1)}
	d.mu.Lock()
	s := d.sessionLocked(req.SessionId, "")
	s.perms[p] = struct{}{}
	for v := range s.viewers {
		go p.ask(v)
	}
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(s.perms, p)
		d.mu.Unlock()
	}()
	select {
	case out := <-p.answer:
		return acp.RequestPermissionResponse{Outcome: out}, nil
	case <-ctx.Done():
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
	}
}

// message is one finished user or agent message for the search index.
type message struct {
	kind, text string
}

// textIndex joins streamed chunks into whole messages.
type textIndex struct {
	kind string
	buf  strings.Builder
}

func (t *textIndex) add(kind, text string) (message, bool) {
	var done message
	var flushed bool
	if kind != t.kind {
		done, flushed = t.flush()
		t.kind = kind
	}
	t.buf.WriteString(text)
	return done, flushed
}

func (t *textIndex) flush() (message, bool) {
	if t.buf.Len() == 0 {
		return message{}, false
	}
	m := message{kind: t.kind, text: t.buf.String()}
	t.buf.Reset()
	t.kind = ""
	return m, true
}

func messageText(u acp.SessionUpdate) (kind, text string, ok bool) {
	switch {
	case u.UserMessageChunk != nil && u.UserMessageChunk.Content.Text != nil:
		return "user", u.UserMessageChunk.Content.Text.Text, true
	case u.AgentMessageChunk != nil && u.AgentMessageChunk.Content.Text != nil:
		return "agent", u.AgentMessageChunk.Content.Text.Text, true
	}
	return "", "", false
}

func decode[T any](params jsontext.Value) (T, *acp.RequestError) {
	var v T
	if len(params) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(params, &v); err != nil {
		return v, acp.NewInvalidParams(map[string]any{"error": err.Error()})
	}
	return v, nil
}
