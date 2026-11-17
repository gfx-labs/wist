// Package notify builds desktop and terminal (OSC 9 and OSC 777)
// notifications.
package notify

import (
	"fmt"
	"strings"
)

// ValidMode reports whether s is a notify mode: auto, off, desktop or
// terminal.
func ValidMode(s string) bool {
	switch s {
	case "auto", "off", "desktop", "terminal":
		return true
	}
	return false
}

// DesktopCmd returns the argv that shows a desktop notification on
// goos, or nil when no tool is available. look stands in for exec.LookPath.
func DesktopCmd(goos, title, body string, look func(string) (string, error)) []string {
	switch goos {
	case "darwin":
		if _, err := look("osascript"); err == nil {
			return []string{"osascript", "-e", fmt.Sprintf("display notification %s with title %s", appleQuote(body), appleQuote(title))}
		}
	default:
		if _, err := look("notify-send"); err == nil {
			return []string{"notify-send", "--app-name=wist", title, body}
		}
	}
	return nil
}

func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// OSC builds the OSC 9 and OSC 777 notification escapes, each
// wrapped for tmux passthrough when inTmux.
func OSC(title, body string, inTmux bool) string {
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return ' '
			}
			return r
		}, s)
	}
	title, body = clean(title), clean(body)
	seqs := []string{
		"\x1b]9;" + title + ": " + body + "\x1b\\",
		"\x1b]777;notify;" + strings.ReplaceAll(title, ";", ",") + ";" + body + "\x1b\\",
	}
	var b strings.Builder
	for _, s := range seqs {
		if inTmux {
			s = "\x1bPtmux;" + strings.ReplaceAll(s, "\x1b", "\x1b\x1b") + "\x1b\\"
		}
		b.WriteString(s)
	}
	return b.String()
}
