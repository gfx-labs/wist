// Command wist is an interactive terminal client for towa. It starts
// towa as a child process and speaks the Agent Client Protocol to it over
// the child's standard input and output.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gfx-labs/wist/pkg/agent"
	"github.com/gfx-labs/wist/pkg/config"
	"github.com/gfx-labs/wist/pkg/registry"
	"github.com/gfx-labs/wist/pkg/tui"
)

func parseFlags(cfg config.Config) tui.Options {
	var o tui.Options
	var extra string
	defTowa := cfg.Towa
	if defTowa == "" {
		defTowa = "towa"
	}
	flag.StringVar(&o.Towa, "towa", defTowa, "path of the towa binary")
	flag.StringVar(&o.Agent, "agent", cfg.Agent, "run this ACP registry agent instead of towa (see `wist agents`)")
	flag.StringVar(&extra, "towa-args", "", "extra arguments for towa, split on spaces (e.g. \"--models=claude:max/claude-opus-5-5\")")
	flag.StringVar(&o.Cwd, "cwd", "", "working directory of the session (default: current directory)")
	flag.StringVar(&o.Resume, "resume", "", "load the session with this id instead of starting a new one")
	flag.StringVar(&o.Log, "log", "", "file that receives towa's stderr (default: $XDG_STATE_HOME/wist/towa.log)")
	flag.StringVar(&o.Prompt, "p", "", "send this prompt as soon as the session is ready")
	flag.BoolVar(&o.NoMouse, "no-mouse", false, "do not capture the mouse (keeps the terminal's own selection)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: wist [flags] [-- agent args...]\n       wist ask [flags] [--] <prompt...>    run one prompt and exit (see wist ask -h)\n       wist agents    list agents in the ACP registry\n\nconfig: %s\n\n", config.Path())
		flag.PrintDefaults()
	}
	flag.Parse()
	if o.Agent == "towa" {
		o.Agent = ""
	}
	if extra != "" {
		o.PassArgs = append(o.PassArgs, strings.Fields(extra)...)
	}
	o.PassArgs = append(o.PassArgs, flag.Args()...)
	if o.Agent == "" && extra == "" && flag.NArg() == 0 {
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
	return o
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "agents" {
		if err := registry.List(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "wist:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "ask" {
		os.Exit(runAsk(os.Args[2:], config.Load()))
	}
	cfg := config.Load()
	o := parseFlags(cfg)
	if err := tui.Run(o, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "wist:", err)
		os.Exit(1)
	}
}
