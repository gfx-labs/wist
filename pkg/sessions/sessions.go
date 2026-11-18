// Package sessions lists an agent's stored sessions over ACP. It is the
// connection behind `wist sessions`.
package sessions

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	acp "github.com/gfx-labs/go-acp"
	"github.com/gfx-labs/wist/pkg/agent"
)

// Options describes how to run the agent for a listing.
type Options struct {
	Bin   string   // agent binary (towa unless Agent is set)
	Agent string   // registry agent id; empty means towa
	Args  []string // agent arguments
	Cwd   string   // agent working directory
	Log   string   // file that receives the agent's stderr
	// Archived selects the archive state to report: "exclude" (also the
	// empty string), "include" or "only".
	Archived string
	// Progress receives setup lines, if set.
	Progress func(string)
}

// Session is one stored session.
type Session struct {
	ID        string `json:"session_id"`
	Title     string `json:"title,omitempty"`
	Cwd       string `json:"cwd"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Archived  bool   `json:"archived,omitempty"`
}

// List lists the agent's stored sessions of every working directory, most
// recently updated first. All pages are fetched.
func List(ctx context.Context, o Options) ([]Session, error) {
	progress := o.Progress
	if progress == nil {
		progress = func(string) {}
	}
	ao := agent.Options{Bin: o.Bin, Agent: o.Agent, Args: o.Args, Cwd: o.Cwd, Log: o.Log}
	if err := agent.Resolve(&ao, progress); err != nil {
		return nil, err
	}
	connCtx, stop := context.WithCancel(context.Background())
	defer stop()
	progress("starting agent")
	proc, err := agent.Spawn(connCtx, ao)
	if err != nil {
		return nil, err
	}
	defer proc.Stop(2 * time.Second)
	conn := acp.NewClientSideConnection(client{}, proc.Stdin, proc.Stdout, nil)
	served := make(chan error, 1)
	go func() { served <- conn.Serve(connCtx) }()
	// call runs one request, giving up when ctx ends or the agent dies.
	call := func(f func(context.Context) error) error {
		cctx, cancel := context.WithCancel(connCtx)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- f(cctx) }()
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case err := <-served:
			return fmt.Errorf("agent exited: %v", err)
		}
	}
	title := "wist"
	var init acp.InitializeResponse
	err = call(func(c context.Context) (err error) {
		init, err = conn.Initialize(c, acp.InitializeRequest{
			ProtocolVersion: acp.ProtocolVersionNumber,
			ClientInfo:      &acp.Implementation{Name: "wist", Title: &title, Version: "0.1.0"},
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("initialize: %w (agent log: %s)", err, o.Log)
	}
	caps := init.AgentCapabilities.SessionCapabilities
	if caps.List == nil {
		return nil, fmt.Errorf("this agent cannot list sessions")
	}
	req := acp.ListSessionsRequest{}
	switch o.Archived {
	case "", "exclude":
		if o.Archived != "" {
			if caps.Archive == nil {
				return nil, fmt.Errorf("--archived: this agent cannot list archived sessions")
			}
			req.Archived = new(acp.SessionArchiveFilterExclude)
		}
	case "include", "only":
		if caps.Archive == nil {
			return nil, fmt.Errorf("--archived: this agent cannot list archived sessions")
		}
		f := acp.SessionArchiveFilter(o.Archived)
		req.Archived = &f
	default:
		return nil, fmt.Errorf("--archived: %q is not exclude, include or only", o.Archived)
	}
	var out []Session
	for {
		var resp acp.ListSessionsResponse
		if err := call(func(c context.Context) (err error) {
			resp, err = conn.ListSessions(c, req)
			return err
		}); err != nil {
			return nil, fmt.Errorf("list sessions: %w", err)
		}
		for _, s := range resp.Sessions {
			out = append(out, Session{
				ID:        string(s.SessionId),
				Title:     deref(s.Title),
				Cwd:       s.Cwd,
				UpdatedAt: deref(s.UpdatedAt),
				Archived:  s.Archived != nil && *s.Archived,
			})
		}
		if resp.NextCursor == nil {
			slices.SortStableFunc(out, newestFirst)
			return out, nil
		}
		req.Cursor = resp.NextCursor
	}
}

// newestFirst orders sessions by update time and leaves the ones without a
// time last.
func newestFirst(a, b Session) int {
	at, aerr := time.Parse(time.RFC3339, a.UpdatedAt)
	bt, berr := time.Parse(time.RFC3339, b.UpdatedAt)
	switch {
	case aerr != nil && berr != nil:
		return strings.Compare(a.UpdatedAt, b.UpdatedAt)
	case aerr != nil:
		return 1
	case berr != nil:
		return -1
	}
	return bt.Compare(at)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// client answers the agent. Listing only reads, so nothing is advertised
// and a conforming agent asks for nothing else.
type client struct{}

var _ acp.Client = client{}

func (client) SessionUpdate(context.Context, acp.SessionNotification) error { return nil }

func (client) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, acp.NewMethodNotFound(acp.ClientMethodSessionRequestPermission)
}

// Elicitation, files and terminals are not advertised.

func (client) CompleteElicitation(context.Context, acp.CompleteElicitationNotification) error {
	return nil
}

func (client) CreateElicitation(context.Context, acp.CreateElicitationRequest) (acp.CreateElicitationResponse, error) {
	return acp.CreateElicitationResponse{}, acp.NewMethodNotFound(acp.ClientMethodElicitationCreate)
}

func (client) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsReadTextFile)
}

func (client) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsWriteTextFile)
}

func (client) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalCreate)
}

func (client) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalKill)
}

func (client) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalOutput)
}

func (client) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalRelease)
}

func (client) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalWaitForExit)
}
