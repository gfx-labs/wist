// Command wist is an interactive terminal client for towa. Its windows
// connect to the wist daemon, which runs towa as a child process and
// speaks the Agent Client Protocol to it over the child's standard input
// and output.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gfx-labs/wist/pkg/agent"
	"github.com/gfx-labs/wist/pkg/ask"
	"github.com/gfx-labs/wist/pkg/config"
	"github.com/gfx-labs/wist/pkg/registry"
	"github.com/gfx-labs/wist/pkg/tui"
	"github.com/urfave/cli/v3"
)

// usageError marks a command line mistake (exit status 2).
func usageError(_ context.Context, cmd *cli.Command, err error, _ bool) error {
	fmt.Fprintf(os.Stderr, "%s: %v\n\n", cmd.FullName(), err)
	_ = cli.ShowSubcommandHelp(cmd)
	return cli.Exit("", ask.ExitUsage)
}

func rootCommand(cfg config.Config) *cli.Command {
	var o tui.Options
	var extra string
	defTowa := cfg.Towa
	if defTowa == "" {
		defTowa = "towa"
	}
	return &cli.Command{
		Name:            "wist",
		Usage:           "interactive terminal client for ACP agents (towa by default)",
		UsageText:       "wist [flags] [-- agent args...]\nwist ask [flags] [--] <prompt...>\nwist sessions [flags]\nwist agents\nwist daemon [flags] [-- agent args...]",
		Description:     "config: " + config.Path(),
		HideHelpCommand: true,
		// main reports errors and picks the exit status.
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		OnUsageError:   usageError,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "towa", Local: true, Value: defTowa, Destination: &o.Towa, Usage: "path of the towa binary"},
			&cli.StringFlag{Name: "agent", Local: true, Value: cfg.Agent, Destination: &o.Agent, Usage: "run this ACP registry agent instead of towa (see: wist agents)"},
			&cli.StringFlag{Name: "towa-args", Local: true, Destination: &extra, Usage: "extra arguments for towa, split on spaces (e.g. \"--models=claude:max/claude-opus-5-5\")"},
			&cli.StringFlag{Name: "cwd", Local: true, Destination: &o.Cwd, Usage: "working directory of the session (default: current directory)"},
			&cli.StringFlag{Name: "resume", Local: true, Destination: &o.Resume, Usage: "load the session with this id instead of starting a new one"},
			&cli.StringFlag{Name: "log", Local: true, Destination: &o.Log, Usage: "file that receives towa's stderr (default: $XDG_STATE_HOME/wist/towa.log)"},
			&cli.StringFlag{Name: "p", Local: true, Destination: &o.Prompt, Usage: "send this prompt as soon as the session is ready"},
			&cli.StringFlag{Name: "theme", Local: true, Destination: &o.Theme, Usage: "color theme preset (e.g. dark, terminal, xresources)"},
			&cli.BoolFlag{Name: "no-mouse", Local: true, Destination: &o.NoMouse, Usage: "do not capture the mouse (keeps the terminal's own selection)"},
			socketFlag(&o.Socket),
		},
		Commands: []*cli.Command{askCommand(cfg), sessionsCommand(cfg), agentsCommand(), daemonCommand(cfg)},
		Action: func(_ context.Context, cmd *cli.Command) error {
			o.AgentSet = cmd.IsSet("towa") || cmd.IsSet("agent") || cmd.IsSet("towa-args") || cmd.Args().Len() > 0
			finishOptions(&o, extra, cmd.Args().Slice(), cfg)
			if err := tui.Run(o, cfg); err != nil {
				return cli.Exit("wist: "+err.Error(), 1)
			}
			return nil
		},
	}
}

// finishOptions resolves agent args and defaults exactly as the original
// flag based parser did.
func finishOptions(o *tui.Options, extra string, rest []string, cfg config.Config) {
	if o.Agent == "towa" {
		o.Agent = ""
	}
	if extra != "" {
		o.PassArgs = append(o.PassArgs, strings.Fields(extra)...)
	}
	o.PassArgs = append(o.PassArgs, rest...)
	if o.Agent == "" && extra == "" && len(rest) == 0 {
		o.Args = append(o.Args, cfg.TowaArgs...)
	}
	o.Args = append(o.Args, o.PassArgs...)
	if o.Cwd == "" {
		o.Cwd, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(o.Cwd); err == nil {
		o.Cwd = abs
	}
	if o.Log == "" {
		o.Log = agent.DefaultLog(o.Agent)
	}
}

func agentsCommand() *cli.Command {
	return &cli.Command{
		Name:  "agents",
		Usage: "list agents in the ACP registry",
		Action: func(context.Context, *cli.Command) error {
			if err := registry.List(os.Stdout); err != nil {
				return cli.Exit("wist: "+err.Error(), 1)
			}
			return nil
		},
	}
}

func main() {
	cfg := config.Load()
	if err := rootCommand(cfg).Run(context.Background(), os.Args); err != nil {
		if ec, ok := err.(cli.ExitCoder); ok {
			if msg := err.Error(); msg != "" {
				fmt.Fprintln(os.Stderr, msg)
			}
			os.Exit(ec.ExitCode())
		}
		// Flag errors surfaced outside OnUsageError.
		fmt.Fprintln(os.Stderr, "wist:", err)
		os.Exit(ask.ExitUsage)
	}
}
