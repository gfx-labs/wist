package sessions

import (
	"testing"

	acp "github.com/gfx-labs/go-acp"
)

// TestFind checks the order of precedence: id, then whole title, then part
// of a title, all ignoring case for titles.
func TestFind(t *testing.T) {
	info := func(id, title string) acp.SessionInfo {
		return acp.SessionInfo{SessionId: acp.SessionId(id), Title: &title}
	}
	list := []acp.SessionInfo{
		info("s1", "Fix the parser"),
		info("s2", "parser"),
		info("s3", "Parser docs"),
		info("s4", ""),
		{SessionId: "s5"},
	}
	ids := func(ref string) (out []string) {
		for _, s := range Find(list, ref) {
			out = append(out, string(s.SessionId))
		}
		return out
	}
	for ref, want := range map[string][]string{
		"s3":       {"s3"},
		"PARSER":   {"s2"},
		" fix the": {"s1"},
		"pars":     {"s1", "s2", "s3"},
		"docs":     {"s3"},
		"nothing":  nil,
		"":         nil,
	} {
		got := ids(ref)
		if len(got) != len(want) {
			t.Errorf("Find(%q) = %v, want %v", ref, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("Find(%q) = %v, want %v", ref, got, want)
				break
			}
		}
	}
}
