package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/gfx-labs/wist/pkg/agent"
	"github.com/gfx-labs/wist/pkg/ask"
	"github.com/gfx-labs/wist/pkg/config"
	"github.com/gfx-labs/wist/pkg/sessions"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

// sessionsHelp is the long description of `wist sessions`.
const sessionsHelp = `List the stored sessions the agent reports, newest first, with their id,
title and working directory. The listing covers sessions of every working
directory; --workdir narrows it to the ones whose directory matches a Go
regular expression. Pass an id to wist --resume or wist ask --resume to
continue one.`

func sessionsCommand(cfg config.Config) *cli.Command {
	var (
		o       sessions.Options
		extra   string
		timeout time.Duration
		jsonOut bool
	)
	defTowa := cfg.Towa
	if defTowa == "" {
		defTowa = "towa"
	}
	return &cli.Command{
		Name:         "sessions",
		Usage:        "list stored sessions, optionally filtered by working directory",
		UsageText:    "wist sessions [flags]",
		Description:  sessionsHelp,
		OnUsageError: usageError,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "workdir", Aliases: []string{"w"}, Value: "", Usage: "only sessions whose working directory matches this regular expression"},
			&cli.StringFlag{Name: "archived", Value: "exclude", Usage: "archived sessions to list: exclude, include or only"},
			&cli.BoolFlag{Name: "json", Destination: &jsonOut, Usage: "print a JSON array instead of a table"},
			&cli.DurationFlag{Name: "timeout", Value: 30 * time.Second, Destination: &timeout, Usage: "overall time limit for starting the agent and listing"},
			&cli.StringFlag{Name: "towa", Value: defTowa, Destination: &o.Bin, Usage: "path of the towa binary"},
			&cli.StringFlag{Name: "agent", Value: cfg.Agent, Destination: &o.Agent, Usage: "run this ACP registry agent instead of towa"},
			&cli.StringFlag{Name: "towa-args", Destination: &extra, Usage: "extra arguments for the agent, split on spaces"},
			&cli.StringFlag{Name: "log", Destination: &o.Log, Usage: "file that receives the agent's stderr"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			re, err := compileWorkdir(cmd.String("workdir"))
			if err != nil {
				fmt.Fprintln(os.Stderr, "wist sessions:", err)
				return cli.Exit("", ask.ExitUsage)
			}
			o.Archived = cmd.String("archived")
			if !validArchived(o.Archived) {
				fmt.Fprintf(os.Stderr, "wist sessions: --archived: %q is not exclude, include or only\n", o.Archived)
				return cli.Exit("", ask.ExitUsage)
			}
			finishSessions(&o, extra, cfg)
			return cli.Exit("", runSessions(ctx, o, re, jsonOut, timeout))
		},
	}
}

// compileWorkdir compiles the --workdir pattern; an empty pattern matches
// every session.
func compileWorkdir(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("--workdir: %w", err)
	}
	return re, nil
}

// validArchived reports whether v names an archive filter.
func validArchived(v string) bool {
	switch v {
	case "exclude", "include", "only":
		return true
	}
	return false
}

// finishSessions fills the agent defaults the way wist and wist ask do.
func finishSessions(o *sessions.Options, extra string, cfg config.Config) {
	if o.Agent == "towa" {
		o.Agent = ""
	}
	if extra != "" {
		o.Args = strings.Fields(extra)
	} else if o.Agent == "" {
		o.Args = append([]string{}, cfg.TowaArgs...)
	}
	if o.Cwd == "" {
		o.Cwd, _ = os.Getwd()
	}
	if o.Log == "" {
		o.Log = agent.DefaultLog(o.Agent)
	}
}

// runSessions lists, filters and prints the sessions; it returns the exit
// status.
func runSessions(ctx context.Context, o sessions.Options, re *regexp.Regexp, jsonOut bool, timeout time.Duration) int {
	plain := !term.IsTerminal(int(os.Stderr.Fd())) || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb"
	if !plain {
		o.Progress = func(s string) { fmt.Fprintln(os.Stderr, s) }
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	list, err := sessions.List(ctx, o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wist sessions:", err)
		return ask.ExitFailure
	}
	list = filterWorkdir(list, re)
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(list); err != nil {
			fmt.Fprintln(os.Stderr, "wist sessions:", err)
			return ask.ExitFailure
		}
		return ask.ExitOK
	}
	if len(list) == 0 {
		if !plain {
			fmt.Fprintln(os.Stderr, "wist sessions: no stored sessions match")
		}
		return ask.ExitOK
	}
	printSessions(os.Stdout, list, time.Now())
	return ask.ExitOK
}

// filterWorkdir keeps the sessions whose working directory matches re; a
// nil re keeps all of them.
func filterWorkdir(list []sessions.Session, re *regexp.Regexp) []sessions.Session {
	if re == nil {
		return list
	}
	kept := make([]sessions.Session, 0, len(list))
	for _, s := range list {
		if re.MatchString(s.Cwd) {
			kept = append(kept, s)
		}
	}
	return kept
}

// printSessions writes one row per session as a table.
func printSessions(w io.Writer, list []sessions.Session, now time.Time) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "UPDATED\tSESSION\tTITLE\tWORKDIR")
	for _, s := range list {
		title := s.Title
		if s.Archived {
			title += " (archived)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", agoString(now, s.UpdatedAt), s.ID, title, s.Cwd)
	}
	tw.Flush()
}

// agoString renders an ISO 8601 timestamp as a short age.
func agoString(now time.Time, updated string) string {
	t, err := time.Parse(time.RFC3339, updated)
	if err != nil {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return t.UTC().Format("2006-01-02")
}
