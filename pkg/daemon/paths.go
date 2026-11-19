// Package daemon is the wist daemon: one process per user that holds the
// ACP connection to the agent and serves ACP to any number of viewers
// (wist windows, a web UI) over a unix socket. Turns keep running when a
// viewer closes, several viewers can watch and drive one session, and the
// daemon keeps every session's update stream in SQLite for replay and full
// text search.
package daemon

import (
	"os"
	"path/filepath"
)

// SocketEnv names the environment variable that overrides the socket path.
const SocketEnv = "WIST_SOCKET"

// SocketPath is the daemon socket: $WIST_SOCKET, else wist/daemon.sock
// under $XDG_RUNTIME_DIR, else under the state directory.
func SocketPath() string {
	if p := os.Getenv(SocketEnv); p != "" {
		return p
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "wist", "daemon.sock")
	}
	return filepath.Join(StateDir(), "daemon.sock")
}

// StateDir is $XDG_STATE_HOME/wist (~/.local/state/wist by default).
func StateDir() string {
	d := os.Getenv("XDG_STATE_HOME")
	if d == "" {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(d, "wist")
}

// DBPath is the daemon's database.
func DBPath() string { return filepath.Join(StateDir(), "daemon.db") }

// LogPath is the daemon's own log.
func LogPath() string { return filepath.Join(StateDir(), "daemon.log") }
