package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gfx-labs/wist/pkg/agent"
	"github.com/gfx-labs/wist/pkg/config"
	"github.com/gfx-labs/wist/pkg/daemon"
	"github.com/urfave/cli/v3"
)

// daemonHelp is the long description of `wist daemon`.
const daemonHelp = `Run the wist daemon in the foreground. wist windows start it on demand,
so running it by hand is only needed to watch its log or pick its agent.

The daemon runs one agent process and serves ACP on a unix socket. Every
wist window (and any other ACP client, such as a web UI) connects to it:
turns keep running after a window closes, several windows can drive one
session, and every session's updates are kept in SQLite for replay and
full text search. The socket is --socket, else $WIST_SOCKET, else
$XDG_RUNTIME_DIR/wist/daemon.sock.`

func socketFlag(dest *string) *cli.StringFlag {
	return &cli.StringFlag{
		Name: "socket", Value: daemon.SocketPath(), Destination: dest,
		Sources: cli.EnvVars(daemon.SocketEnv),
		Usage:   "unix socket of the wist daemon",
	}
}

func daemonCommand(cfg config.Config) *cli.Command {
	var (
		ao     agent.Options
		extra  string
		socket string
	)
	defTowa := cfg.Towa
	if defTowa == "" {
		defTowa = "towa"
	}
	return &cli.Command{
		Name:         "daemon",
		Usage:        "run the daemon that holds the agent and serves wist windows",
		UsageText:    "wist daemon [flags] [-- agent args...]\nwist daemon stop",
		Description:  daemonHelp,
		OnUsageError: usageError,
		Flags: []cli.Flag{
			socketFlag(&socket),
			&cli.StringFlag{Name: "towa", Value: defTowa, Destination: &ao.Bin, Usage: "path of the towa binary"},
			&cli.StringFlag{Name: "agent", Value: cfg.Agent, Destination: &ao.Agent, Usage: "run this ACP registry agent instead of towa"},
			&cli.StringFlag{Name: "towa-args", Destination: &extra, Usage: "extra arguments for the agent, split on spaces"},
			&cli.StringFlag{Name: "towa-socket", Value: cfg.TowaSocket, Destination: &ao.Socket, Usage: "connect to the towa daemon on this unix socket, starting towa serve on it when none listens"},
			&cli.StringFlag{Name: "log", Destination: &ao.Log, Usage: "file that receives the agent's stderr (default: $XDG_STATE_HOME/wist/towa.log)"},
		},
		Commands: []*cli.Command{{
			Name:  "stop",
			Usage: "stop the running daemon and its agent",
			Flags: []cli.Flag{socketFlag(&socket)},
			Action: func(ctx context.Context, _ *cli.Command) error {
				if err := stopDaemon(ctx, socket); err != nil {
					return cli.Exit("wist daemon stop: "+err.Error(), 1)
				}
				return nil
			},
		}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if ao.Agent == "towa" {
				ao.Agent = ""
			}
			rest := cmd.Args().Slice()
			if ao.Agent == "" && extra == "" && len(rest) == 0 {
				ao.Args = append(ao.Args, cfg.TowaArgs...)
			}
			ao.Args = append(append(ao.Args, strings.Fields(extra)...), rest...)
			if ao.Log == "" {
				ao.Log = agent.DefaultLog(ao.Agent)
			}
			if err := agent.Resolve(&ao, func(s string) { fmt.Fprintln(os.Stderr, "wist daemon:", s) }); err != nil {
				return cli.Exit("wist daemon: "+err.Error(), 1)
			}
			ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()
			err := daemon.Run(ctx, daemon.Options{
				Agent:  ao,
				Socket: socket,
				DB:     daemon.DBPath(),
				Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
			})
			if err != nil {
				return cli.Exit("wist daemon: "+err.Error(), 1)
			}
			return nil
		},
	}
}

func stopDaemon(ctx context.Context, socket string) error {
	_, err := daemon.Request(ctx, socket, daemon.MethodShutdown, map[string]any{})
	return err
}
