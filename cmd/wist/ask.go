package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gfx-labs/wist/pkg/ask"
	"github.com/gfx-labs/wist/pkg/config"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

func askCommand(cfg config.Config) *cli.Command {
	var f ask.Flags
	defTowa := cfg.Towa
	if defTowa == "" {
		defTowa = "towa"
	}
	return &cli.Command{
		Name:         "ask",
		Usage:        "run one prompt without the terminal UI and exit",
		UsageText:    "wist ask [flags] [--] <prompt...>",
		Description:  ask.Help,
		OnUsageError: usageError,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "towa", Value: defTowa, Destination: &f.Bin, Usage: "path of the towa binary"},
			&cli.StringFlag{Name: "agent", Value: cfg.Agent, Destination: &f.Agent, Usage: "run this ACP registry agent instead of towa"},
			&cli.StringFlag{Name: "towa-args", Destination: &f.TowaArgs, Usage: "extra arguments for the agent, split on spaces"},
			&cli.StringFlag{Name: "cwd", Destination: &f.Cwd, Usage: "working directory of the session (default: current directory)"},
			&cli.StringFlag{Name: "log", Destination: &f.Log, Usage: "file that receives the agent's stderr (default: $XDG_STATE_HOME/wist/towa.log)"},
			&cli.StringFlag{Name: "resume", Destination: &f.Resume, Usage: "continue the session with this id, or \"last\" for the latest one in cwd"},
			&cli.BoolFlag{Name: "no-save", Destination: &f.NoSave, Usage: "do not keep the session (deleted after the run)"},
			&cli.StringFlag{Name: "model", Destination: &f.Model, Usage: "model for this run (value or name of the agent's model option)"},
			&cli.StringFlag{Name: "mode", Aliases: []string{"permission-mode"}, Destination: &f.Mode, Usage: "permission mode for this run: ask or auto"},
			&cli.BoolFlag{Name: "ask", Destination: &f.Ask, Usage: "shorthand for --mode ask"},
			&cli.BoolFlag{Name: "auto", Destination: &f.Auto, Usage: "shorthand for --mode auto"},
			&cli.StringSliceFlag{Name: "image", Destination: &f.Images, Usage: "attach this image file (repeatable)"},
			&cli.StringFlag{Name: "system", Destination: &f.System, Usage: "replace the agent's system prompt for this run"},
			&cli.DurationFlag{Name: "timeout", Destination: &f.Timeout, Usage: "overall time limit, e.g. 2m (0: none)"},
			&cli.BoolFlag{Name: "json", Destination: &f.JSON, Usage: "print one JSON result object to stdout at the end"},
			&cli.BoolFlag{Name: "quiet", Destination: &f.Quiet, Usage: "print no assistant text (exit status only)"},
			&cli.BoolFlag{Name: "no-color", Destination: &f.NoColor, Usage: "no colors on stderr (also NO_COLOR or TERM=dumb)"},
			&cli.BoolFlag{Name: "prompt-permissions", Destination: &f.PromptPermissions, Usage: "ask on the terminal for permissions even with --json or --quiet"},
		},
		DisableSliceFlagSeparator: true,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			f.Args = cmd.Args().Slice()
			return cli.Exit("", runAsk(ctx, f, cfg))
		},
	}
}

// runAsk runs `wist ask` and returns the exit status.
func runAsk(ctx context.Context, f ask.Flags, cfg config.Config) int {
	piped := !term.IsTerminal(int(os.Stdin.Fd()))
	o, err := ask.Prepare(f, cfg, os.Stdin, piped)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wist ask:", err)
		return ask.ExitCode("", err)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		s := <-sigs
		cancel(ask.Interrupted{Signal: s})
		<-sigs // a second signal exits at once
		os.Exit(ask.Interrupted{Signal: s}.Code())
	}()

	res, err := ask.Run(ctx, o)
	var intr ask.Interrupted
	if errors.As(err, &intr) {
		fmt.Fprintln(os.Stderr, "wist ask: interrupted")
		return intr.Code()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "wist ask:", err)
	}
	if o.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(res)
	}
	return res.ExitCode
}
