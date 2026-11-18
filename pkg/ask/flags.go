package ask

import (
	"errors"
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

// Help is the help text shown above the flag list.
const Help = `Run one prompt against the agent without the terminal UI and exit.
The prompt comes from the arguments, from stdin when there are none, or
both (arguments, a blank line, then stdin) when stdin is piped. Every
argument after -- is prompt text, so agent arguments go in --towa-args.

stdout gets the assistant's raw markdown as it streams; stderr gets tool
calls, plan updates, notices and usage.

exit status: 0 end_turn, 1 failure (error, refusal, max_tokens, any other
stop reason, agent died, timeout), 2 usage error, 130 interrupted (143 on
SIGTERM).`

// Flags are the raw command line values that Prepare turns into Options.
type Flags struct {
	Options            // fields set directly from flags
	TowaArgs  string   // --towa-args, split on spaces
	Ask, Auto bool     // --ask, --auto shorthands
	Args      []string // positional prompt arguments
}

// Prepare validates f, assembles the prompt (reading stdin when it is
// piped or there are no arguments) and fills defaults. Mistakes are
// *UsageError.
func Prepare(f Flags, cfg config.Config, stdin io.Reader, stdinPiped bool) (Options, error) {
	o := f.Options
	if o.Bin == "" {
		o.Bin = cfg.Towa
		if o.Bin == "" {
			o.Bin = "towa"
		}
	}
	if f.Ask && f.Auto {
		return o, &UsageError{"--ask and --auto conflict"}
	}
	for _, sh := range []struct {
		on   bool
		mode string
	}{{f.Ask, "ask"}, {f.Auto, "auto"}} {
		if !sh.on {
			continue
		}
		if o.Mode != "" && o.Mode != sh.mode {
			return o, &UsageError{"--" + sh.mode + " conflicts with --mode " + o.Mode}
		}
		o.Mode = sh.mode
	}
	if o.NoSave && o.Resume != "" {
		return o, &UsageError{"--no-save cannot be combined with --resume"}
	}
	if o.Timeout < 0 {
		return o, &UsageError{"--timeout must not be negative"}
	}
	var in io.Reader
	if stdinPiped || len(f.Args) == 0 {
		in = stdin
	}
	p, err := BuildPrompt(f.Args, in)
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
	if f.TowaArgs != "" {
		o.Args = strings.Fields(f.TowaArgs)
	} else if o.Agent == "" {
		o.Args = append([]string{}, cfg.TowaArgs...)
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
