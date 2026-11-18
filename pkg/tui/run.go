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

	proc, err := agent.Spawn(ctx, agent.Options{Bin: o.Towa, Args: o.Args, Env: o.Env, Cwd: o.Cwd, Log: o.Log})
	if err != nil {
		return err
	}
	stdin, stdout := proc.Stdin, proc.Stdout

	ui, err := newUI(o, cfg)
	if err != nil {
		proc.Stop(2 * time.Second)
		return err
	}
	client := &client{ui: ui}
	conn := acp.NewClientSideConnection(client, stdin, stdout, nil)
	ui.conn = conn
	go func() {
		err := conn.Serve(ctx)
		ui.post(func(u *UI) {
			if ctx.Err() == nil {
				u.addBlock(&block{kind: kindError, text: fmt.Sprintf("connection to towa closed: %v (see %s)", errOrEOF(err), o.Log)})
				u.busy = false
			}
		})
	}()
	go ui.connect(ctx)

	runErr := ui.loop()
	cancel()
	proc.Stop(2 * time.Second)
	if ui.lastSession != "" {
		agent := ""
		if o.Agent != "" {
			agent = " --agent " + o.Agent
		}
		fmt.Printf("session %s\nresume with: wist%s --resume %s\n", ui.lastSession, agent, ui.lastSession)
	}
	return runErr
}

func errOrEOF(err error) error {
	if err == nil {
		return io.EOF
	}
	return err
}

var errQuit = errors.New("quit")
