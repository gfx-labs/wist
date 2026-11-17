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
	towa    string
	args    []string
	cwd     string
	resume  string
	log     string
	prompt  string
	noMouse bool
}

func parseFlags() options {
	var o options
	var extra string
	flag.StringVar(&o.towa, "towa", "towa", "path of the towa binary")
	flag.StringVar(&extra, "towa-args", "", "extra arguments for towa, split on spaces (e.g. \"--models=claude:max/claude-opus-5-5\")")
	flag.StringVar(&o.cwd, "cwd", "", "working directory of the session (default: current directory)")
	flag.StringVar(&o.resume, "resume", "", "load the session with this id instead of starting a new one")
	flag.StringVar(&o.log, "log", "", "file that receives towa's stderr (default: $XDG_STATE_HOME/wist/towa.log)")
	flag.StringVar(&o.prompt, "p", "", "send this prompt as soon as the session is ready")
	flag.BoolVar(&o.noMouse, "no-mouse", false, "do not capture the mouse (keeps the terminal's own selection)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: wist [flags] [-- towa args...]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if extra != "" {
		o.args = append(o.args, strings.Fields(extra)...)
	}
	o.args = append(o.args, flag.Args()...)
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
		o.log = filepath.Join(dir, "wist", "towa.log")
	}
	return o
}

func main() {
	o := parseFlags()
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "wist:", err)
		os.Exit(1)
	}
}

func run(o options) error {
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

	ui, err := newUI(o)
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
		fmt.Printf("session %s\nresume with: wist --resume %s\n", ui.lastSession, ui.lastSession)
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
