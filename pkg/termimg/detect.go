package termimg

import (
	"os"
	"os/exec"
	"strings"
)

// Protocol is a way to show images in a terminal.
type Protocol int

const (
	// Off disables image display.
	Off Protocol = iota
	// Blocks draws half-block cells with two colors each.
	Blocks
	// Kitty uses the kitty graphics protocol.
	Kitty
	// Sixel uses DEC sixel graphics.
	Sixel
)

// Detect picks the image protocol for the current terminal from the
// environment (WIST_IMAGES=kitty|sixel|blocks|off overrides) and reports
// whether wist runs inside tmux.
func Detect() (Protocol, bool) {
	tmux := os.Getenv("TMUX") != ""
	switch strings.ToLower(os.Getenv("WIST_IMAGES")) {
	case "off", "none", "0":
		return Off, tmux
	case "blocks", "halfblocks", "ansi":
		return Blocks, tmux
	case "kitty":
		return Kitty, tmux
	case "sixel":
		return Sixel, tmux
	}
	term, prog := os.Getenv("TERM"), strings.ToLower(os.Getenv("TERM_PROGRAM"))
	if tmux {
		// TERM and TERM_PROGRAM name tmux here: ask it for the outer terminal.
		out, _ := exec.Command("tmux", "display", "-p", "#{client_termname}\t#{client_termtype}").Output()
		name, typ, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
		term, prog = name, strings.ToLower(strings.Fields(typ + " ")[0])
	}
	switch {
	case strings.Contains(term, "kitty"), strings.Contains(term, "ghostty"),
		prog == "ghostty", prog == "wezterm",
		os.Getenv("KITTY_WINDOW_ID") != "", os.Getenv("GHOSTTY_RESOURCES_DIR") != "",
		os.Getenv("WEZTERM_EXECUTABLE") != "":
		return Kitty, tmux
	case strings.HasPrefix(term, "foot"), strings.Contains(term, "mlterm"), strings.Contains(term, "contour"),
		os.Getenv("XTERM_VERSION") != "":
		return Sixel, tmux
	}
	return Blocks, tmux
}
