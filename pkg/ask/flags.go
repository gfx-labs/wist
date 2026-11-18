package ask

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gfx-labs/wist/pkg/agent"
	"github.com/gfx-labs/wist/pkg/config"
)

// Exit codes of wist ask.
const (
	ExitOK          = 0   // the turn ended with end_turn
	ExitFailure     = 1   // error, agent died, timeout or a stop reason other than end_turn
	ExitUsage       = 2   // bad flags or an empty prompt
	ExitInterrupted = 130 // SIGINT
	ExitTerminated  = 143 // SIGTERM
)

// UsageError is a command line mistake; it maps to exit status 2.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// ErrHelp is returned by ParseFlags for -h/--help after printing usage.
var ErrHelp = flag.ErrHelp

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }

const usageHead = `usage: wist ask [flags] [--] <prompt...>

Run one prompt against the agent without the terminal UI and exit.
The prompt comes from the arguments, from stdin when there are none, or
both (arguments, a blank line, then stdin) when stdin is piped. Every
argument after -- is prompt text, so agent arguments go in --towa-args.

stdout gets the assistant's raw markdown as it streams; stderr gets tool
calls, plan updates, notices and usage.

exit status: 0 end_turn, 1 failure (error, refusal, max_tokens, any other
stop reason, agent died, timeout), 2 usage error, 130 interrupted (143 on
SIGTERM).

flags:
`

// ParseFlags parses the arguments after "ask". stdin supplies the prompt
// when stdinPiped is true. Usage text and flag errors go to errOut.
func ParseFlags(args []string, cfg config.Config, stdin io.Reader, stdinPiped bool, errOut io.Writer) (Options, error) {
	var o Options
	var extra, mode string
	var images multi
	var ask, auto bool
	fs := flag.NewFlagSet("wist ask", flag.ContinueOnError)
	fs.SetOutput(errOut)
	defTowa := cfg.Towa
	if defTowa == "" {
		defTowa = "towa"
	}
	fs.StringVar(&o.Bin, "towa", defTowa, "path of the towa binary")
	fs.StringVar(&o.Agent, "agent", cfg.Agent, "run this ACP registry agent instead of towa")
	fs.StringVar(&extra, "towa-args", "", "extra arguments for the agent, split on spaces")
	fs.StringVar(&o.Cwd, "cwd", "", "working directory of the session (default: current directory)")
	fs.StringVar(&o.Log, "log", "", "file that receives the agent's stderr (default: $XDG_STATE_HOME/wist/towa.log)")
	fs.StringVar(&o.Resume, "resume", "", "continue the session with this id, or `last` for the latest one in cwd")
	fs.BoolVar(&o.NoSave, "no-save", false, "do not keep the session (deleted after the run)")
	fs.StringVar(&o.Model, "model", "", "model for this run (value or name of the agent's model option)")
	fs.StringVar(&mode, "mode", "", "permission mode for this run: ask or auto")
	fs.StringVar(&mode, "permission-mode", "", "same as --mode")
	fs.BoolVar(&ask, "ask", false, "shorthand for --mode ask")
	fs.BoolVar(&auto, "auto", false, "shorthand for --mode auto")
	fs.Var(&images, "image", "attach this image file (repeatable)")
	fs.StringVar(&o.System, "system", "", "replace the agent's system prompt for this run")
	fs.DurationVar(&o.Timeout, "timeout", 0, "overall time limit, e.g. 2m (0: none)")
	fs.BoolVar(&o.JSON, "json", false, "print one JSON result object to stdout at the end")
	fs.BoolVar(&o.Quiet, "quiet", false, "print no assistant text (exit status only)")
	fs.BoolVar(&o.NoColor, "no-color", false, "no colors on stderr (also NO_COLOR or TERM=dumb)")
	fs.BoolVar(&o.PromptPermissions, "prompt-permissions", false, "ask on the terminal for permissions even with --json or --quiet")
	fs.Usage = func() {
		fmt.Fprint(errOut, usageHead)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return o, ErrHelp
		}
		return o, &UsageError{err.Error()}
	}
	if ask && auto {
		return o, &UsageError{"--ask and --auto conflict"}
	}
	if ask {
		if mode != "" && mode != "ask" {
			return o, &UsageError{"--ask conflicts with --mode " + mode}
		}
		mode = "ask"
	}
	if auto {
		if mode != "" && mode != "auto" {
			return o, &UsageError{"--auto conflicts with --mode " + mode}
		}
		mode = "auto"
	}
	o.Mode = mode
	if o.NoSave && o.Resume != "" {
		return o, &UsageError{"--no-save cannot be combined with --resume"}
	}
	if o.Timeout < 0 {
		return o, &UsageError{"--timeout must not be negative"}
	}
	o.Images = images
	var in io.Reader
	if stdinPiped || fs.NArg() == 0 {
		in = stdin
	}
	p, err := BuildPrompt(fs.Args(), in)
	if err != nil {
		return o, err
	}
	o.Prompt = p
	if strings.TrimSpace(o.Prompt) == "" {
		return o, &UsageError{"empty prompt"}
	}

	if o.Agent == "towa" {
		o.Agent = ""
	}
	if extra != "" {
		o.Args = strings.Fields(extra)
	} else if o.Agent == "" {
		o.Args = append(o.Args, cfg.TowaArgs...)
	}
	if o.Cwd == "" {
		o.Cwd, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(o.Cwd); err == nil {
		o.Cwd = abs
	}
	if o.Log == "" {
		o.Log = agent.DefaultLog(o.Agent)
	}
	if v := os.Getenv("NO_COLOR"); v != "" || os.Getenv("TERM") == "dumb" {
		o.NoColor = true
	}
	return o, nil
}

// BuildPrompt joins the prompt arguments with spaces and, when stdin is
// not nil, appends a blank line and stdin's content (trailing newlines
// trimmed). Either part may be empty.
func BuildPrompt(args []string, stdin io.Reader) (string, error) {
	p := strings.Join(args, " ")
	if stdin == nil {
		return p, nil
	}
	b, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	s := strings.TrimRight(string(b), "\r\n")
	switch {
	case strings.TrimSpace(s) == "":
		return p, nil
	case p == "":
		return s, nil
	}
	return p + "\n\n" + s, nil
}

// ExitCode maps a run's outcome to the process exit status.
func ExitCode(stop string, err error) int {
	var ue *UsageError
	switch {
	case errors.As(err, &ue):
		return ExitUsage
	case err != nil:
		return ExitFailure
	case stop == "end_turn":
		return ExitOK
	}
	return ExitFailure
}
