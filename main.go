// Command wist is an interactive terminal client for towa. It starts
// towa as a child process and speaks the Agent Client Protocol to it over
// the child's standard input and output.
package main

import (
	"context"
	"errors"
	"flag"
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

type options struct {
	towa     string
	agent    string   // registry agent id; empty means towa
	args     []string // full argument list for the agent process
	passArgs []string // the user's extra agent args, forwarded to child windows
	env      []string
	cwd      string
	resume   string
	log      string
	prompt   string
	noMouse  bool
}

func parseFlags(cfg config) options {
	var o options
	var extra string
	defTowa := cfg.towa
	if defTowa == "" {
		defTowa = "towa"
	}
	flag.StringVar(&o.towa, "towa", defTowa, "path of the towa binary")
	flag.StringVar(&o.agent, "agent", cfg.agent, "run this ACP registry agent instead of towa (see `wist agents`)")
	flag.StringVar(&extra, "towa-args", "", "extra arguments for towa, split on spaces (e.g. \"--models=claude:max/claude-opus-5-5\")")
	flag.StringVar(&o.cwd, "cwd", "", "working directory of the session (default: current directory)")
	flag.StringVar(&o.resume, "resume", "", "load the session with this id instead of starting a new one")
	flag.StringVar(&o.log, "log", "", "file that receives towa's stderr (default: $XDG_STATE_HOME/wist/towa.log)")
	flag.StringVar(&o.prompt, "p", "", "send this prompt as soon as the session is ready")
	flag.BoolVar(&o.noMouse, "no-mouse", false, "do not capture the mouse (keeps the terminal's own selection)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: wist [flags] [-- agent args...]\n       wist agents    list agents in the ACP registry\n\nconfig: %s\n\n", configPath())
		flag.PrintDefaults()
	}
	flag.Parse()
	if o.agent == "towa" {
		o.agent = ""
	}
	if extra != "" {
		o.passArgs = append(o.passArgs, strings.Fields(extra)...)
	}
	o.passArgs = append(o.passArgs, flag.Args()...)
	if o.agent == "" && extra == "" && flag.NArg() == 0 {
		o.args = append(o.args, cfg.towaArgs...)
	}
	o.args = append(o.args, o.passArgs...)
	if o.cwd == "" {
		o.cwd, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(o.cwd); err == nil {
		o.cwd = abs
	}
	if o.log == "" {
		dir := os.Getenv("XDG_STATE_HOME")
		if dir == "" {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, ".local", "state")
		}
		name := "towa.log"
		if o.agent != "" {
			name = o.agent + ".log"
		}
		o.log = filepath.Join(dir, "wist", name)
	}
	return o
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "agents" {
		if err := listAgents(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "wist:", err)
			os.Exit(1)
		}
		return
	}
	cfg := loadConfig()
	o := parseFlags(cfg)
	if o.agent != "" {
		c, err := resolveAgent(o.agent, func(s string) { fmt.Fprintln(os.Stderr, "wist:", s) })
		if err != nil {
			fmt.Fprintln(os.Stderr, "wist:", err)
			os.Exit(1)
		}
		o.towa = c.path
		o.args = append(append([]string{}, c.args...), o.args...)
		o.env = c.env
	}
	if err := run(o, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "wist:", err)
		os.Exit(1)
	}
}

func run(o options, cfg config) error {
	if err := os.MkdirAll(filepath.Dir(o.log), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(o.log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "\n--- wist %s: %s %s\n", time.Now().Format(time.RFC3339), o.towa, strings.Join(o.args, " "))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, o.towa, o.args...)
	cmd.Dir = o.cwd
	if len(o.env) > 0 {
		cmd.Env = append(os.Environ(), o.env...)
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
		return fmt.Errorf("start %s: %w", o.towa, err)
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
				u.addBlock(&block{kind: kindError, text: fmt.Sprintf("connection to towa closed: %v (see %s)", errOrEOF(err), o.log)})
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
		if o.agent != "" {
			agent = " --agent " + o.agent
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
