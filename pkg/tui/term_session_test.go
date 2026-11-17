package tui

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"
)

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
