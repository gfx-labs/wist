// Package agent launches the ACP agent child process (towa or a registry
// agent) for wist. The TUI and wist ask share it.
package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gfx-labs/wist/pkg/registry"
)

// Options describes the agent process.
type Options struct {
	Bin   string   // agent binary (towa unless Agent is set)
	Agent string   // registry agent id; empty means towa
	Args  []string // argument list for the agent process
	Env   []string // extra environment
	Cwd   string   // working directory
	Log   string   // file that receives the agent's stderr
}

// Resolve resolves a registry agent (downloading it if needed) into o.Bin,
// o.Args and o.Env. It does nothing when o.Agent is empty.
func Resolve(o *Options, progress func(string)) error {
	if o.Agent == "" {
		return nil
	}
	c, err := registry.Resolve(o.Agent, progress)
	if err != nil {
		return err
	}
	o.Bin = c.Path
	o.Args = append(append([]string{}, c.Args...), o.Args...)
	o.Env = c.Env
	return nil
}

// DefaultLog is the default stderr log path for the agent id ("" = towa).
func DefaultLog(agentID string) string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "state")
	}
	name := "towa.log"
	if agentID != "" {
		name = agentID + ".log"
	}
	return filepath.Join(dir, "wist", name)
}

// Process is a running agent.
type Process struct {
	Cmd    *exec.Cmd
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Log    *os.File
	Exited chan error // receives cmd.Wait's result once
}

// Spawn opens the log file and starts the agent. The process is killed
// when ctx is cancelled. Close the process with Stop.
func Spawn(ctx context.Context, o Options) (*Process, error) {
	if err := os.MkdirAll(filepath.Dir(o.Log), 0o755); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(o.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(logFile, "\n--- wist %s: %s %s\n", time.Now().Format(time.RFC3339), o.Bin, strings.Join(o.Args, " "))
	cmd := exec.CommandContext(ctx, o.Bin, o.Args...)
	cmd.Dir = o.Cwd
	if len(o.Env) > 0 {
		cmd.Env = append(os.Environ(), o.Env...)
	}
	cmd.Stderr = logFile
	// Keep the agent out of the terminal's process group so Ctrl+C reaches only us.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	p := &Process{Cmd: cmd, Log: logFile, Exited: make(chan error, 1)}
	if p.Stdin, err = cmd.StdinPipe(); err != nil {
		logFile.Close()
		return nil, err
	}
	if p.Stdout, err = cmd.StdoutPipe(); err != nil {
		logFile.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("start %s: %w", o.Bin, err)
	}
	go func() { p.Exited <- cmd.Wait() }()
	return p, nil
}

// Stop closes the agent's stdin, waits up to grace for it to exit, kills
// it otherwise and closes the log.
func (p *Process) Stop(grace time.Duration) {
	_ = p.Stdin.Close()
	select {
	case <-p.Exited:
	case <-time.After(grace):
		_ = p.Cmd.Process.Kill()
	}
	p.Log.Close()
}
