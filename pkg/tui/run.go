// Package tui is the wist terminal user interface: it runs an ACP agent as
// a child process and drives the transcript, input, panels and overlays.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	acp "github.com/gfx-labs/go-acp"
	"github.com/gfx-labs/wist/pkg/agent"
	"github.com/gfx-labs/wist/pkg/config"
)

// Options holds the command line settings for one wist session.
type Options struct {
	Towa     string   // agent binary (towa unless Agent is set)
	Agent    string   // registry agent id; empty means towa
	Args     []string // full argument list for the agent process
	PassArgs []string // the user's extra agent args, forwarded to child windows
	Env      []string // extra environment for the agent process
	Cwd      string   // session working directory
	Resume   string   // session id to load instead of starting a new one
	Log      string   // file that receives the agent's stderr
	Prompt   string   // prompt sent as soon as the session is ready
	NoMouse  bool     // leave the mouse to the terminal
	Theme    string   // theme preset overriding the config
}

// Run starts the agent process described by o, speaks ACP to it and runs
// the terminal UI until the user quits. When o.Agent names a registry
// agent it is resolved (and downloaded if needed) first.
func Run(o Options, cfg config.Config) error {
	ao := agent.Options{Bin: o.Towa, Agent: o.Agent, Args: o.Args}
	if err := agent.Resolve(&ao, func(s string) { fmt.Fprintln(os.Stderr, "wist:", s) }); err != nil {
		return err
	}
	o.Towa, o.Args, o.Env = ao.Bin, ao.Args, ao.Env
	return run(o, cfg)
}

func run(o Options, cfg config.Config) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ui, err := newUI(o, cfg)
	if err != nil {
		return err
	}
	ac, err := startAgent(ctx, o, ui)
	if err != nil {
		return err
	}
	ui.conn = ac.conn

	// /reload restarts the agent so it reads its config again. The reload
	// goroutine owns ac: run never touches it after this point.
	reloads := make(chan acp.SessionId, 1)
	ui.reload = func(sid acp.SessionId) {
		select {
		case reloads <- sid:
		default:
		}
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case sid := <-reloads:
				ac.cancel()
				ac.proc.Stop(2 * time.Second)
				nac, err := startAgent(ctx, o, ui)
				if err != nil {
					ui.post(func(u *UI) {
						u.reloading = false
						u.errorf("reload: %v", err)
					})
					continue
				}
				ac = nac
				done := make(chan struct{})
				ui.post(func(u *UI) { u.conn = nac.conn; close(done) })
				<-done
				ui.connect(ctx, sid, "")
				ui.post(func(u *UI) { u.reloading = false })
			case <-ctx.Done():
				ac.cancel()
				ac.proc.Stop(2 * time.Second)
				return
			}
		}
	}()
	go ui.connect(ctx, acp.SessionId(o.Resume), o.Prompt)

	runErr := ui.loop()
	cancel()
	<-stopped
	if ui.lastSession != "" {
		agent := ""
		if o.Agent != "" {
			agent = " --agent " + o.Agent
		}
		fmt.Printf("session %s\nresume with: wist%s --resume %s\n", ui.lastSession, agent, ui.lastSession)
	}
	return runErr
}

// agentConn is one agent process and the ACP connection to it.
type agentConn struct {
	proc   *agent.Process
	conn   *acp.ClientSideConnection
	cancel context.CancelFunc
}

// startAgent spawns the agent and serves ACP on its stdio. The connection
// stops when cancel is called or ctx ends.
func startAgent(ctx context.Context, o Options, ui *UI) (*agentConn, error) {
	proc, err := agent.Spawn(ctx, agent.Options{Bin: o.Towa, Args: o.Args, Env: o.Env, Cwd: o.Cwd, Log: o.Log})
	if err != nil {
		return nil, err
	}
	cctx, ccancel := context.WithCancel(ctx)
	conn := acp.NewClientSideConnection(&client{ui: ui}, proc.Stdin, proc.Stdout, nil)
	go func() {
		err := conn.Serve(cctx)
		ui.post(func(u *UI) {
			if cctx.Err() == nil {
				u.addBlock(&block{kind: kindError, text: fmt.Sprintf("connection to towa closed: %v (see %s)", errOrEOF(err), o.Log)})
				u.busy = false
			}
		})
	}()
	return &agentConn{proc: proc, conn: conn, cancel: ccancel}, nil
}

func errOrEOF(err error) error {
	if err == nil {
		return io.EOF
	}
	return err
}

var errQuit = errors.New("quit")
