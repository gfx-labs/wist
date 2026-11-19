// Package tui is the wist terminal user interface: it speaks ACP to the
// wist daemon, which runs the agent, and drives the transcript, input,
// panels and overlays.
package tui

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"

	acp "github.com/gfx-labs/go-acp"
	"github.com/gfx-labs/wist/pkg/config"
	"github.com/gfx-labs/wist/pkg/daemon"
)

// Options holds the command line settings for one wist session.
type Options struct {
	Towa     string   // agent binary (towa unless Agent is set)
	Agent    string   // registry agent id; empty means towa
	Args     []string // full argument list for the agent process
	PassArgs []string // the user's extra agent args, forwarded to child windows
	AgentSet bool     // the agent was chosen on the command line
	Socket   string   // daemon socket
	// TowaSocket is a towa daemon socket the wist daemon connects to;
	// empty runs towa as the daemon's child.
	TowaSocket string
	Cwd        string // session working directory
	Resume     string // session id to load instead of starting a new one
	Log        string // file that receives the agent's stderr
	Prompt     string // prompt sent as soon as the session is ready
	NoMouse    bool   // leave the mouse to the terminal
	Theme      string // theme preset overriding the config
}

// DaemonArgs are the wist arguments that start a daemon running the agent
// o describes.
func (o Options) DaemonArgs() []string {
	args := []string{"daemon", "--socket", o.Socket, "--log", o.Log}
	if o.TowaSocket != "" {
		args = append(args, "--towa-socket", o.TowaSocket)
	}
	if o.Agent != "" {
		args = append(args, "--agent", o.Agent)
	} else {
		args = append(args, "--towa", o.Towa)
	}
	return append(append(args, "--"), o.Args...)
}

// Run connects to the daemon on o.Socket, starting it when none is
// running, and runs the terminal UI until the user quits. A running
// daemon keeps its agent: the agent options only apply to a daemon this
// call starts.
func Run(o Options, cfg config.Config) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	conn, spawned, err := daemon.Dial(ctx, o.Socket, append([]string{self}, o.DaemonArgs()...))
	if err != nil {
		return fmt.Errorf("connect to daemon %s: %w", o.Socket, err)
	}
	defer func() { _ = conn.Close() }()

	ui, err := newUI(o, cfg)
	if err != nil {
		return err
	}
	if o.AgentSet && !spawned {
		ui.info("the daemon on " + o.Socket + " was already running, so its agent is used and the agent flags are ignored (restart it with: wist daemon stop)")
	}
	ui.conn = startConn(ctx, conn, ui)
	ui.reload = func(sid acp.SessionId) {
		go func() {
			_, err := ui.conn.SendExtensionRequest(ctx, daemon.MethodReload, map[string]any{})
			if err != nil {
				ui.post(func(u *UI) {
					u.reloading = false
					u.errorf("reload: %v", err)
				})
				return
			}
			ui.loadSession(sid)
			ui.post(func(u *UI) { u.reloading = false })
		}()
	}
	go ui.connect(ctx, acp.SessionId(o.Resume), o.Prompt)

	runErr := ui.loop()
	cancel()
	if ui.lastSession != "" {
		fmt.Printf("session %s\nresume with: wist --resume %s\n", ui.lastSession, ui.lastSession)
	}
	return runErr
}

// startConn serves ACP on the daemon connection until ctx ends.
func startConn(ctx context.Context, c net.Conn, ui *UI) *acp.ClientSideConnection {
	conn := acp.NewClientSideConnection(&client{ui: ui}, c, c, nil)
	go func() {
		err := conn.Serve(ctx)
		ui.post(func(u *UI) {
			if ctx.Err() == nil {
				u.addBlock(&block{kind: kindError, text: fmt.Sprintf("connection to the wist daemon closed: %v (see %s)", errOrEOF(err), daemon.LogPath())})
				u.busy = false
			}
		})
	}()
	return conn
}

func errOrEOF(err error) error {
	if err == nil {
		return io.EOF
	}
	return err
}
