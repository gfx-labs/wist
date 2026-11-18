package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gfx-labs/wist/pkg/sessions"
)

// TestFilterWorkdir checks --workdir keeps the sessions its pattern matches
// and that no pattern keeps every session.
func TestFilterWorkdir(t *testing.T) {
	list := []sessions.Session{
		{ID: "a", Cwd: "/home/a/repo/github.com/gfx-labs/wist"},
		{ID: "b", Cwd: "/home/a/repo/github.com/gfx-labs/towa"},
		{ID: "c", Cwd: "/tmp"},
	}
	re, err := compileWorkdir(`gfx-labs/(wist|towa)$`)
	if err != nil {
		t.Fatal(err)
	}
	got := filterWorkdir(list, re)
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("filtered = %+v", got)
	}
	if kept := filterWorkdir(list, nil); len(kept) != 3 {
		t.Fatalf("no pattern kept %d sessions", len(kept))
	}
	if _, err := compileWorkdir("("); err == nil {
		t.Fatal("a broken pattern compiled")
	}
}

// TestPrintSessions checks the table has a header, one row per session and
// the age of each session.
func TestPrintSessions(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	list := []sessions.Session{
		{ID: "01a1", Title: "fix the picker", Cwd: "/repo/wist", UpdatedAt: "2026-03-01T10:30:00Z"},
		{ID: "01a2", Cwd: "/repo/towa", Archived: true},
	}
	var buf bytes.Buffer
	printSessions(&buf, list, now)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	for _, want := range []string{"UPDATED", "/repo/towa", "01a1", "fix the picker", "1h ago", "(archived)", "-"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, buf.String())
		}
	}
}
