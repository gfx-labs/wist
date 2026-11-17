package tui

import (
	"bufio"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
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
	if got := desktopNotifyCmd("linux", "t", "b", have("notify-send")); len(got) == 0 || got[0] != "notify-send" || got[len(got)-1] != "b" {
		t.Fatalf("linux: %v", got)
	}
	if got := desktopNotifyCmd("linux", "t", "b", have()); got != nil {
		t.Fatalf("linux without tool: %v", got)
	}
	got := desktopNotifyCmd("darwin", `a"b`, "x", have("osascript"))
	if len(got) != 3 || got[0] != "osascript" || !strings.Contains(got[2], `with title "a\"b"`) {
		t.Fatalf("darwin: %v", got)
	}
}

func TestNotifyConfig(t *testing.T) {
	c, err := parseConfig(bufio.NewScanner(strings.NewReader(`notify = "desktop"`)))
	if err != nil || c.Notify != "desktop" || len(c.Warnings) != 0 {
		t.Fatalf("%+v %v", c, err)
	}
	c, _ = parseConfig(bufio.NewScanner(strings.NewReader(`notify = "bell"`)))
	if c.Notify != "" || len(c.Warnings) != 1 {
		t.Fatalf("bad mode accepted: %+v", c)
	}
}

func TestOscNotify(t *testing.T) {
	got := oscNotify("wist", "done\nok", false)
	want := "\x1b]9;wist: done ok\x1b\\\x1b]777;notify;wist;done ok\x1b\\"
	if got != want {
		t.Fatalf("got %q", got)
	}
	tm := oscNotify("wist", "x", true)
	if !strings.HasPrefix(tm, "\x1bPtmux;\x1b\x1b]9;wist: x\x1b\x1b\\\x1b\\") || strings.Count(tm, "\x1bPtmux;") != 2 {
		t.Fatalf("tmux %q", tm)
	}
}

func TestComposeRoundtrip(t *testing.T) {
	p, err := writeComposeFile("hi\nthere")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	if !strings.HasSuffix(p, ".md") {
		t.Fatalf("path %s", p)
	}
	os.WriteFile(p, []byte("hi\nthere\n\n"), 0o600)
	data, _ := os.ReadFile(p)
	if got := composeResult(data); got != "hi\nthere\n" {
		t.Fatalf("%q", got)
	}
	if composeResult([]byte("a\r\n")) != "a" {
		t.Fatal("crlf")
	}
}

func TestFrameWait(t *testing.T) {
	now := time.Now()
	if frameWait(time.Time{}, now) != 0 || frameWait(now.Add(-time.Second), now) != 0 {
		t.Fatal("old draw should not wait")
	}
	if w := frameWait(now.Add(-6*time.Millisecond), now); w != 10*time.Millisecond {
		t.Fatalf("wait %v", w)
	}
}
