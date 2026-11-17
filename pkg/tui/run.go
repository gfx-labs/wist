// Package tui is the wist terminal user interface: it runs an ACP agent as
// a child process and drives the transcript, input, panels and overlays.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	acp "github.com/gfx-labs/go-acp"
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
}

// ListAgents writes the agents of the ACP registry to w.
func ListAgents(w io.Writer) error { return listAgents(w) }

// Run starts the agent process described by o, speaks ACP to it and runs
// the terminal UI until the user quits. When o.Agent names a registry
// agent it is resolved (and downloaded if needed) first.
func Run(o Options, cfg Config) error {
	if o.Agent != "" {
		c, err := resolveAgent(o.Agent, func(s string) { fmt.Fprintln(os.Stderr, "wist:", s) })
		if err != nil {
			return err
		}
		o.Towa = c.path
		o.Args = append(append([]string{}, c.args...), o.Args...)
		o.Env = c.env
	}
	return run(o, cfg)
}

func run(o Options, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(o.Log), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(o.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "\n--- wist %s: %s %s\n", time.Now().Format(time.RFC3339), o.Towa, strings.Join(o.Args, " "))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, o.Towa, o.Args...)
	cmd.Dir = o.Cwd
	if len(o.Env) > 0 {
		cmd.Env = append(os.Environ(), o.Env...)
	}
	cmd.Stderr = logFile
	// Keep towa out of the terminal's process group so Ctrl+C reaches only us.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", o.Towa, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	ui, err := newUI(o, cfg)
	if err != nil {
		_ = stdin.Close()
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
	_ = stdin.Close()
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
	}
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
