package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Dial connects to the daemon at socket. When nothing is listening and
// spawn is set, it starts spawn (the daemon command) detached, logging to
// LogPath, and waits for the socket. spawned reports whether this call
// started the daemon.
func Dial(ctx context.Context, socket string, spawn []string) (conn net.Conn, spawned bool, err error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", socket)
	if err == nil || len(spawn) == 0 {
		return c, false, err
	}
	if !errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.ECONNREFUSED) {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(LogPath()), 0o755); err != nil {
		return nil, false, err
	}
	logFile, err := os.OpenFile(LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = logFile.Close() }()
	cmd := exec.Command(spawn[0], spawn[1:]...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Env = append(os.Environ(), SocketEnv+"="+socket)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, false, fmt.Errorf("start daemon: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if c, err := d.DialContext(ctx, "unix", socket); err == nil {
			return c, true, nil
		}
		select {
		case err := <-exited:
			// A daemon started by another window at the same time wins
			// the lock; this one exits and the other's socket appears.
			if c, derr := d.DialContext(ctx, "unix", socket); derr == nil {
				return c, false, nil
			}
			return nil, false, fmt.Errorf("daemon exited: %v (see %s)", err, LogPath())
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-tick.C:
		}
	}
}
