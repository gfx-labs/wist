package daemon

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"sync"

	acp "github.com/gfx-labs/go-acp"
)

// Extension methods a viewer can call besides ACP itself.
const (
	// MethodSessions lists the sessions the daemon has stored, with whether
	// each is running a turn and how many viewers it has.
	MethodSessions = "_wist/sessions"
	// MethodSearch searches the stored messages: {"query": "...", "limit": n}.
	MethodSearch = "_wist/search"
	// MethodReload restarts the agent so it reads its config again.
	MethodReload = "_wist/reload"
	// MethodWatch subscribes the connection to MethodSessionChanged.
	MethodWatch = "_wist/watch"
	// MethodSessionChanged is a notification carrying a StoredSession
	// whenever a session's title, busy state, viewers or pending
	// permission requests change.
	MethodSessionChanged = "_wist/session"
)

// StoredSession is one entry of MethodSessions.
type StoredSession struct {
	SessionRow
	Busy    bool `json:"busy"`
	Viewers int  `json:"viewers"`
	// Waiting counts permission requests no viewer has answered.
	Waiting int `json:"waiting"`
}

// SearchRequest is the parameter of MethodSearch.
type SearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

// viewer is one connected client of the daemon.
type viewer struct {
	d    *Daemon
	conn *acp.AgentSideConnection

	// sessions is guarded by d.mu.
	sessions map[acp.SessionId]*session

	// out queues messages so a slow viewer never blocks the agent: an
	// acp.SessionNotification or a StoredSession for MethodSessionChanged.
	outMu sync.Mutex
	out   []any
	wake  chan struct{}
	once  sync.Once
}

var (
	_ acp.Agent       = (*viewer)(nil)
	_ acp.AgentLoader = (*viewer)(nil)
)

// notify queues an update for the viewer.
func (v *viewer) notify(n acp.SessionNotification) { v.enqueue(n) }

func (v *viewer) enqueue(m any) {
	v.once.Do(func() {
		v.wake = make(chan struct{}, 1)
		go v.pump()
	})
	v.outMu.Lock()
	v.out = append(v.out, m)
	v.outMu.Unlock()
	select {
	case v.wake <- struct{}{}:
	default:
	}
}

func (v *viewer) pump() {
	for {
		select {
		case <-v.wake:
		case <-v.conn.Done():
			return
		}
		v.outMu.Lock()
		batch := v.out
		v.out = nil
		v.outMu.Unlock()
		for _, m := range batch {
			var err error
			switch m := m.(type) {
			case acp.SessionNotification:
				err = v.conn.SessionUpdate(v.d.ctx, m)
			case StoredSession:
				err = v.conn.SendExtensionNotification(v.d.ctx, MethodSessionChanged, m)
			}
			if err != nil {
				return
			}
		}
	}
}

func (v *viewer) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	up, err := v.d.agent()
	if err != nil {
		return acp.InitializeResponse{}, err
	}
	return up.init, nil
}

func (v *viewer) Authenticate(ctx context.Context, p acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	up, err := v.d.agent()
	if err != nil {
		return acp.AuthenticateResponse{}, err
	}
	return up.conn.Authenticate(ctx, p)
}

func (v *viewer) Logout(ctx context.Context, p acp.LogoutRequest) (acp.LogoutResponse, error) {
	up, err := v.d.agent()
	if err != nil {
		return acp.LogoutResponse{}, err
	}
	return up.conn.Logout(ctx, p)
}

func (v *viewer) NewSession(ctx context.Context, p acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	d := v.d
	up, err := d.agent()
	if err != nil {
		return acp.NewSessionResponse{}, err
	}
	resp, err := up.conn.NewSession(ctx, p)
	if err != nil {
		return resp, err
	}
	if err := d.store.Touch(d.ctx, resp.SessionId, p.Cwd); err != nil {
		d.log.Warn("store session", "session", resp.SessionId, "err", err)
	}
	d.mu.Lock()
	s := d.sessionLocked(resp.SessionId, p.Cwd)
	s.up, s.config, s.modes = up, resp.ConfigOptions, resp.Modes
	d.attachLocked(s, v)
	d.mu.Unlock()
	return resp, nil
}

// LoadSession attaches the viewer to a session. A session the agent
// already has loaded is replayed from the daemon's store; otherwise the
// agent loads it (refreshing the store) first.
func (v *viewer) LoadSession(ctx context.Context, p acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	d := v.d
	d.mu.Lock()
	s := d.sessionLocked(p.SessionId, p.Cwd)
	d.mu.Unlock()
	if err := d.ensureLive(ctx, s, p.McpServers); err != nil {
		return acp.LoadSessionResponse{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	err := d.store.Replay(ctx, s.id, func(u acp.SessionUpdate) {
		v.notify(acp.SessionNotification{SessionId: s.id, Update: u})
	})
	if err != nil {
		return acp.LoadSessionResponse{}, err
	}
	d.attachLocked(s, v)
	return acp.LoadSessionResponse{ConfigOptions: s.config, Modes: s.modes}, nil
}

// ensureLive loads s in the current agent when it is not loaded there,
// replacing its stored updates with the agent's replay. Viewers already
// attached see none of the replay: they have the transcript.
func (d *Daemon) ensureLive(ctx context.Context, s *session, mcp []acp.McpServer) error {
	s.load.Lock()
	defer s.load.Unlock()
	up, err := d.agent()
	if err != nil {
		return err
	}
	d.mu.Lock()
	live, cwd := s.up == up, s.cwd
	d.mu.Unlock()
	if live {
		return nil
	}
	if !up.init.AgentCapabilities.LoadSession {
		return acp.NewMethodNotFound(acp.AgentMethodSessionLoad)
	}
	if cwd == "" {
		row, err := d.store.Session(ctx, s.id)
		if errors.Is(err, sql.ErrNoRows) {
			return acp.NewResourceNotFound(map[string]any{"sessionId": s.id, "error": "unknown working directory"})
		}
		if err != nil {
			return err
		}
		cwd = row.Cwd
	}
	if mcp == nil {
		mcp = []acp.McpServer{}
	}
	if err := d.store.Reset(ctx, s.id); err != nil {
		return err
	}
	d.mu.Lock()
	s.text = textIndex{}
	s.quiet = true
	d.mu.Unlock()
	resp, err := up.conn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: s.id, Cwd: cwd, McpServers: mcp})
	d.mu.Lock()
	defer d.mu.Unlock()
	s.quiet = false
	d.flushLocked(s)
	if err != nil {
		return err
	}
	s.up, s.cwd, s.config, s.modes = up, cwd, resp.ConfigOptions, resp.Modes
	if err := d.store.Touch(d.ctx, s.id, cwd); err != nil {
		d.log.Warn("store session", "session", s.id, "err", err)
	}
	return nil
}

func (v *viewer) ResumeSession(ctx context.Context, p acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	d := v.d
	up, err := d.agent()
	if err != nil {
		return acp.ResumeSessionResponse{}, err
	}
	resp, err := up.conn.ResumeSession(ctx, p)
	if err != nil {
		return resp, err
	}
	d.mu.Lock()
	s := d.sessionLocked(p.SessionId, p.Cwd)
	s.up, s.config, s.modes = up, resp.ConfigOptions, resp.Modes
	d.attachLocked(s, v)
	d.mu.Unlock()
	return resp, nil
}

// Prompt runs a turn. Prompts to one session run in arrival order, and a
// turn keeps running when the viewer that sent it disconnects.
func (v *viewer) Prompt(ctx context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	d := v.d
	s, err := v.session(p.SessionId)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	s.turn.Lock()
	defer s.turn.Unlock()
	if err := d.ensureLive(ctx, s, nil); err != nil {
		return acp.PromptResponse{}, err
	}
	d.mu.Lock()
	up := s.up
	s.busy = true
	d.changedLocked(s)
	// The agent does not echo the prompt, so the other viewers see it
	// here, and the store keeps it for replay.
	for _, b := range p.Prompt {
		u := acp.SessionUpdate{UserMessageChunk: &acp.SessionUpdateUserMessageChunk{Content: b}}
		d.recordLocked(s, u)
		d.broadcastLocked(s, v, u)
	}
	d.flushLocked(s)
	d.mu.Unlock()
	resp, err := up.conn.Prompt(context.WithoutCancel(ctx), p)
	d.mu.Lock()
	s.busy = false
	d.flushLocked(s)
	d.mu.Unlock()
	if err := d.store.Touch(d.ctx, s.id, s.cwd); err != nil {
		d.log.Warn("store session", "session", s.id, "err", err)
	}
	d.mu.Lock()
	d.changedLocked(s)
	d.mu.Unlock()
	return resp, err
}

func (v *viewer) Cancel(ctx context.Context, p acp.CancelNotification) error {
	d := v.d
	d.mu.Lock()
	s := d.sessions[p.SessionId]
	d.mu.Unlock()
	if s == nil || s.up == nil {
		return nil
	}
	return s.up.conn.Cancel(ctx, p)
}

func (v *viewer) SetSessionConfigOption(ctx context.Context, p acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	var id acp.SessionId
	switch {
	case p.ValueId != nil:
		id = p.ValueId.SessionId
	case p.Boolean != nil:
		id = p.Boolean.SessionId
	}
	s, err := v.session(id)
	if err != nil {
		return acp.SetSessionConfigOptionResponse{}, err
	}
	if err := v.d.ensureLive(ctx, s, nil); err != nil {
		return acp.SetSessionConfigOptionResponse{}, err
	}
	resp, err := s.up.conn.SetSessionConfigOption(ctx, p)
	if err != nil {
		return resp, err
	}
	u := acp.SessionUpdate{ConfigOptionUpdate: &acp.SessionConfigOptionUpdate{ConfigOptions: resp.ConfigOptions}}
	v.d.mu.Lock()
	v.d.recordLocked(s, u)
	v.d.broadcastLocked(s, v, u)
	v.d.mu.Unlock()
	return resp, nil
}

func (v *viewer) SetSessionMode(ctx context.Context, p acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	s, err := v.session(p.SessionId)
	if err != nil {
		return acp.SetSessionModeResponse{}, err
	}
	if err := v.d.ensureLive(ctx, s, nil); err != nil {
		return acp.SetSessionModeResponse{}, err
	}
	resp, err := s.up.conn.SetSessionMode(ctx, p)
	if err != nil {
		return resp, err
	}
	u := acp.SessionUpdate{CurrentModeUpdate: &acp.SessionCurrentModeUpdate{CurrentModeId: p.ModeId}}
	v.d.mu.Lock()
	v.d.recordLocked(s, u)
	v.d.broadcastLocked(s, v, u)
	v.d.mu.Unlock()
	return resp, nil
}

func (v *viewer) ListSessions(ctx context.Context, p acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	up, err := v.d.agent()
	if err != nil {
		return acp.ListSessionsResponse{}, err
	}
	resp, err := up.conn.ListSessions(ctx, p)
	if err != nil {
		return resp, err
	}
	for _, info := range resp.Sessions {
		if err := v.d.store.Remember(ctx, info); err != nil {
			v.d.log.Warn("store session", "session", info.SessionId, "err", err)
		}
	}
	return resp, nil
}

func (v *viewer) CloseSession(ctx context.Context, p acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	up, err := v.d.agent()
	if err != nil {
		return acp.CloseSessionResponse{}, err
	}
	return up.conn.CloseSession(ctx, p)
}

func (v *viewer) DeleteSession(ctx context.Context, p acp.DeleteSessionRequest) (acp.DeleteSessionResponse, error) {
	up, err := v.d.agent()
	if err != nil {
		return acp.DeleteSessionResponse{}, err
	}
	return up.conn.DeleteSession(ctx, p)
}

func (v *viewer) UnstableForkSession(ctx context.Context, p acp.UnstableForkSessionRequest) (acp.UnstableForkSessionResponse, error) {
	up, err := v.d.agent()
	if err != nil {
		return acp.UnstableForkSessionResponse{}, err
	}
	resp, err := up.conn.UnstableForkSession(ctx, p)
	if err != nil {
		return resp, err
	}
	if err := v.d.store.Touch(v.d.ctx, acp.SessionId(resp.SessionId), p.Cwd); err != nil {
		v.d.log.Warn("store session", "session", resp.SessionId, "err", err)
	}
	return resp, nil
}

func (v *viewer) UnstableSetSessionTitle(ctx context.Context, p acp.UnstableSetSessionTitleRequest) (acp.UnstableSetSessionTitleResponse, error) {
	up, err := v.d.agent()
	if err != nil {
		return acp.UnstableSetSessionTitleResponse{}, err
	}
	return up.conn.UnstableSetSessionTitle(ctx, p)
}

func (v *viewer) UnstableArchiveSession(ctx context.Context, p acp.UnstableArchiveSessionRequest) (acp.UnstableArchiveSessionResponse, error) {
	up, err := v.d.agent()
	if err != nil {
		return acp.UnstableArchiveSessionResponse{}, err
	}
	return up.conn.UnstableArchiveSession(ctx, p)
}

func (v *viewer) UnstableUnarchiveSession(ctx context.Context, p acp.UnstableUnarchiveSessionRequest) (acp.UnstableUnarchiveSessionResponse, error) {
	up, err := v.d.agent()
	if err != nil {
		return acp.UnstableUnarchiveSessionResponse{}, err
	}
	return up.conn.UnstableUnarchiveSession(ctx, p)
}

// session returns a session the viewer may drive: one it created or
// loaded on this connection.
func (v *viewer) session(id acp.SessionId) (*session, error) {
	v.d.mu.Lock()
	defer v.d.mu.Unlock()
	s := v.sessions[id]
	if s == nil {
		return nil, acp.NewInvalidParams(map[string]any{"error": fmt.Sprintf("session %s is not loaded on this connection", id)})
	}
	return s, nil
}

func (v *viewer) listStored(ctx context.Context, _ string, _ jsontext.Value) (any, *acp.RequestError) {
	rows, err := v.d.store.Sessions(ctx)
	if err != nil {
		return nil, acp.NewInternalError(map[string]any{"error": err.Error()})
	}
	out := make([]StoredSession, len(rows))
	v.d.mu.Lock()
	for i, r := range rows {
		out[i].SessionRow = r
		if s := v.d.sessions[acp.SessionId(r.ID)]; s != nil {
			s.fill(&out[i])
		}
	}
	v.d.mu.Unlock()
	return map[string]any{"sessions": out}, nil
}

func (v *viewer) search(ctx context.Context, _ string, params jsontext.Value) (any, *acp.RequestError) {
	req, rerr := decode[SearchRequest](params)
	if rerr != nil {
		return nil, rerr
	}
	if req.Limit <= 0 {
		return nil, acp.NewInvalidParams(map[string]any{"error": "limit must be positive"})
	}
	hits, err := v.d.store.Search(ctx, req.Query, req.Limit)
	if err != nil {
		return nil, acp.NewInternalError(map[string]any{"error": err.Error()})
	}
	return map[string]any{"hits": hits}, nil
}

func (v *viewer) reload(context.Context, string, jsontext.Value) (any, *acp.RequestError) {
	if err := v.d.reload(); err != nil {
		return nil, acp.NewInternalError(map[string]any{"error": err.Error()})
	}
	return map[string]any{}, nil
}

func (v *viewer) stopDaemon(ctx context.Context, _ string, _ jsontext.Value) (any, *acp.RequestError) {
	if !acp.AfterResponse(ctx, v.d.shutdown) {
		v.d.shutdown()
	}
	return map[string]any{}, nil
}

func (v *viewer) watch(context.Context, string, jsontext.Value) (any, *acp.RequestError) {
	v.d.mu.Lock()
	v.d.watchers[v] = struct{}{}
	v.d.mu.Unlock()
	return map[string]any{}, nil
}
