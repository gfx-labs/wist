package daemon

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/gfx-labs/go-acp"
	"github.com/gfx-labs/wist/pkg/agent"
)

const fakeAgentEnv = "WIST_DAEMON_TEST_AGENT"

func TestMain(m *testing.M) {
	if os.Getenv(fakeAgentEnv) != "" {
		runFakeAgent()
		return
	}
	os.Exit(m.Run())
}

// fakeAgent echoes each prompt after asking for permission, and replays a
// session's prompts and answers on session/load.
type fakeAgent struct {
	conn *acp.AgentSideConnection
	mu   sync.Mutex
	logs map[acp.SessionId][]acp.SessionUpdate
	n    int
}

func runFakeAgent() {
	a := &fakeAgent{logs: map[acp.SessionId][]acp.SessionUpdate{}}
	a.conn = acp.NewAgentSideConnection(a, os.Stdout, os.Stdin, nil)
	_ = a.conn.Serve(context.Background())
}

func (a *fakeAgent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{ProtocolVersion: acp.ProtocolVersionNumber, AgentCapabilities: acp.AgentCapabilities{LoadSession: true}}, nil
}

func (a *fakeAgent) NewSession(context.Context, acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.n++
	return acp.NewSessionResponse{SessionId: acp.SessionId(fmt.Sprintf("s%d-%d", os.Getpid(), a.n))}, nil
}

func (a *fakeAgent) LoadSession(ctx context.Context, p acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	a.mu.Lock()
	log := a.logs[p.SessionId]
	a.mu.Unlock()
	for _, u := range log {
		if err := a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: p.SessionId, Update: u}); err != nil {
			return acp.LoadSessionResponse{}, err
		}
	}
	return acp.LoadSessionResponse{}, nil
}

func (a *fakeAgent) Prompt(ctx context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	text := p.Prompt[0].Text.Text
	title := "run " + text
	perm, err := a.conn.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionId: p.SessionId,
		ToolCall:  acp.ToolCallUpdate{ToolCallId: "t", Title: &title},
		Options:   []acp.PermissionOption{{OptionId: "allow", Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce}},
	})
	if err != nil {
		return acp.PromptResponse{}, err
	}
	answer := "denied"
	if perm.Outcome.Selected != nil {
		answer = "echo " + text
	}
	u := acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: answer}}}}
	a.mu.Lock()
	a.logs[p.SessionId] = append(a.logs[p.SessionId],
		acp.SessionUpdate{UserMessageChunk: &acp.SessionUpdateUserMessageChunk{Content: p.Prompt[0]}}, u)
	a.mu.Unlock()
	if err := a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: p.SessionId, Update: u}); err != nil {
		return acp.PromptResponse{}, err
	}
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (a *fakeAgent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}
func (a *fakeAgent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}
func (a *fakeAgent) Cancel(context.Context, acp.CancelNotification) error { return nil }
func (a *fakeAgent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}
func (a *fakeAgent) DeleteSession(context.Context, acp.DeleteSessionRequest) (acp.DeleteSessionResponse, error) {
	return acp.DeleteSessionResponse{}, nil
}
func (a *fakeAgent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{Sessions: []acp.SessionInfo{}}, nil
}
func (a *fakeAgent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, nil
}
func (a *fakeAgent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, nil
}
func (a *fakeAgent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

// testViewer is a viewer that records updates and answers permission
// requests with allow when answer is set, and never otherwise.
type testViewer struct {
	nopClient
	conn   *acp.ClientSideConnection
	answer bool
	asked  chan acp.SessionId
	mu     sync.Mutex
	texts  []string
}

func (v *testViewer) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	var text string
	switch u := n.Update; {
	case u.UserMessageChunk != nil:
		text = "user: " + u.UserMessageChunk.Content.Text.Text
	case u.AgentMessageChunk != nil:
		text = "agent: " + u.AgentMessageChunk.Content.Text.Text
	default:
		return nil
	}
	v.mu.Lock()
	v.texts = append(v.texts, text)
	v.mu.Unlock()
	return nil
}

func (v *testViewer) RequestPermission(ctx context.Context, _ acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	v.asked <- ""
	if !v.answer {
		<-ctx.Done()
		return acp.RequestPermissionResponse{}, ctx.Err()
	}
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected("allow")}, nil
}

func (v *testViewer) seen() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.texts...)
}

func (v *testViewer) waitFor(t *testing.T, want ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := v.seen()
		if strings.Join(got, "\n") == strings.Join(want, "\n") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("updates:\n got %q\nwant %q", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func startDaemon(t *testing.T, dir string) (socket string, stop func()) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	socket = filepath.Join(dir, "d.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Agent:  agent.Options{Bin: self, Env: []string{fakeAgentEnv + "=1"}, Cwd: dir, Log: filepath.Join(dir, "agent.log")},
			Socket: socket,
			DB:     filepath.Join(dir, "d.db"),
		})
	}()
	for range 500 {
		if c, err := net.Dial("unix", socket); err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return socket, func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

func connectViewer(t *testing.T, socket string, answer bool) *testViewer {
	t.Helper()
	c, _, err := Dial(t.Context(), socket, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	v := &testViewer{answer: answer, asked: make(chan acp.SessionId, 4)}
	v.conn = acp.NewClientSideConnection(v, c, c, nil)
	go func() { _ = v.conn.Serve(t.Context()) }()
	if _, err := v.conn.Initialize(t.Context(), acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); err != nil {
		t.Fatal(err)
	}
	return v
}

func prompt(text string) []acp.ContentBlock {
	return []acp.ContentBlock{{Text: &acp.ContentBlockText{Text: text}}}
}

func TestViewersShareSession(t *testing.T) {
	dir := t.TempDir()
	socket, stop := startDaemon(t, dir)
	ctx := t.Context()

	a := connectViewer(t, socket, false)
	b := connectViewer(t, socket, true)
	ns, err := a.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: dir, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	sid := ns.SessionId
	if _, err := b.conn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: sid, Cwd: dir, McpServers: []acp.McpServer{}}); err != nil {
		t.Fatal(err)
	}

	// a never answers the permission request; b's answer wins and a's
	// request is cancelled.
	resp, err := a.conn.Prompt(ctx, acp.PromptRequest{SessionId: sid, Prompt: prompt("hello")})
	if err != nil || resp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("prompt: %v %v", resp, err)
	}
	<-a.asked
	<-b.asked
	a.waitFor(t, "agent: echo hello")
	b.waitFor(t, "user: hello", "agent: echo hello")

	// A viewer that attaches later gets the session from the store.
	c := connectViewer(t, socket, true)
	if _, err := c.conn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: sid, Cwd: dir, McpServers: []acp.McpServer{}}); err != nil {
		t.Fatal(err)
	}
	c.waitFor(t, "user: hello", "agent: echo hello")

	raw, err := Request(ctx, socket, MethodSearch, SearchRequest{Query: "echo", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var found struct {
		Hits []Hit `json:"hits"`
	}
	if err := json.Unmarshal(raw, &found); err != nil {
		t.Fatal(err)
	}
	if len(found.Hits) != 1 || found.Hits[0].Session != string(sid) || found.Hits[0].Kind != "agent" {
		t.Fatalf("search: %+v", found.Hits)
	}

	raw, err = Request(ctx, socket, MethodSessions, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Sessions []StoredSession `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Sessions) != 1 || listed.Sessions[0].Viewers != 3 || listed.Sessions[0].Cwd != dir {
		t.Fatalf("sessions: %+v", listed.Sessions)
	}
	stop()
}

func TestReloadKeepsSessions(t *testing.T) {
	dir := t.TempDir()
	socket, stop := startDaemon(t, dir)
	defer stop()
	ctx := t.Context()

	a := connectViewer(t, socket, true)
	ns, err := a.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: dir, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.conn.Prompt(ctx, acp.PromptRequest{SessionId: ns.SessionId, Prompt: prompt("one")}); err != nil {
		t.Fatal(err)
	}
	<-a.asked
	if _, err := Request(ctx, socket, MethodReload, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	// The new agent has no record of the session, so the daemon's load
	// of it replays nothing, and a prompt still reaches the new agent.
	if _, err := a.conn.Prompt(ctx, acp.PromptRequest{SessionId: ns.SessionId, Prompt: prompt("two")}); err != nil {
		t.Fatal(err)
	}
	<-a.asked
	a.waitFor(t, "agent: echo one", "agent: echo two")
}

func TestWatchReportsSessionState(t *testing.T) {
	dir := t.TempDir()
	socket, stop := startDaemon(t, dir)
	defer stop()
	ctx := t.Context()

	w := connectViewer(t, socket, true)
	changes := make(chan StoredSession, 16)
	if err := w.conn.RegisterExtensionNotification(MethodSessionChanged, func(_ context.Context, _ string, params jsontext.Value) (any, *acp.RequestError) {
		var s StoredSession
		if err := json.Unmarshal(params, &s); err != nil {
			t.Error(err)
		}
		changes <- s
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.conn.SendExtensionRequest(ctx, MethodWatch, map[string]any{}); err != nil {
		t.Fatal(err)
	}

	a := connectViewer(t, socket, true)
	ns, err := a.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: dir, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.conn.Prompt(ctx, acp.PromptRequest{SessionId: ns.SessionId, Prompt: prompt("x")}); err != nil {
		t.Fatal(err)
	}
	<-a.asked
	var sawBusy, sawWaiting bool
	for {
		select {
		case s := <-changes:
			if s.ID != string(ns.SessionId) {
				t.Fatalf("change for %s", s.ID)
			}
			sawBusy = sawBusy || s.Busy
			sawWaiting = sawWaiting || s.Waiting == 1
			if sawBusy && sawWaiting && !s.Busy && s.Waiting == 0 {
				if s.Viewers != 1 || s.Cwd != dir {
					t.Fatalf("final state %+v", s)
				}
				return
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("busy %v waiting %v", sawBusy, sawWaiting)
		}
	}
}
