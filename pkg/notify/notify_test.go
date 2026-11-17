package notify

import (
	"errors"
	"strings"
	"testing"
)

func TestDesktopNotifyCmd(t *testing.T) {
	have := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			for _, h := range names {
				if h == n {
					return "/bin/" + n, nil
				}
			}
			return "", errors.New("missing")
		}
	}
	if got := DesktopCmd("linux", "t", "b", have("notify-send")); len(got) == 0 || got[0] != "notify-send" || got[len(got)-1] != "b" {
		t.Fatalf("linux: %v", got)
	}
	if got := DesktopCmd("linux", "t", "b", have()); got != nil {
		t.Fatalf("linux without tool: %v", got)
	}
	got := DesktopCmd("darwin", `a"b`, "x", have("osascript"))
	if len(got) != 3 || got[0] != "osascript" || !strings.Contains(got[2], `with title "a\"b"`) {
		t.Fatalf("darwin: %v", got)
	}
}

func TestOscNotify(t *testing.T) {
	got := OSC("wist", "done\nok", false)
	want := "\x1b]9;wist: done ok\x1b\\\x1b]777;notify;wist;done ok\x1b\\"
	if got != want {
		t.Fatalf("got %q", got)
	}
	tm := OSC("wist", "x", true)
	if !strings.HasPrefix(tm, "\x1bPtmux;\x1b\x1b]9;wist: x\x1b\x1b\\\x1b\\") || strings.Count(tm, "\x1bPtmux;") != 2 {
		t.Fatalf("tmux %q", tm)
	}
}
