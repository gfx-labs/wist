package ask

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"syscall"
	"testing"

	acp "github.com/gfx-labs/go-acp"
	"github.com/gfx-labs/wist/pkg/config"
)

func TestBuildPrompt(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		stdin io.Reader
		want  string
	}{
		{"args only", []string{"hello", "world"}, nil, "hello world"},
		{"stdin only", nil, strings.NewReader("from stdin\n"), "from stdin"},
		{"both", []string{"summarize"}, strings.NewReader("a\nb\n"), "summarize\n\na\nb"},
		{"empty stdin", []string{"x"}, strings.NewReader("\n\n"), "x"},
		{"nothing", nil, strings.NewReader(""), ""},
	}
	for _, c := range cases {
		got, err := BuildPrompt(c.args, c.stdin)
		if err != nil || got != c.want {
			t.Errorf("%s: BuildPrompt = %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func parse(t *testing.T, args []string, stdin string, piped bool) (Options, error) {
	t.Helper()
	return ParseFlags(args, config.Config{}, strings.NewReader(stdin), piped, io.Discard)
}

func TestParseFlagsPrompt(t *testing.T) {
	o, err := parse(t, []string{"--json", "--", "--not-a-flag", "text"}, "", false)
	if err != nil || o.Prompt != "--not-a-flag text" || !o.JSON {
		t.Fatalf("-- handling: %+v %v", o, err)
	}
	o, err = parse(t, []string{"review"}, "diff here", true)
	if err != nil || o.Prompt != "review\n\ndiff here" {
		t.Fatalf("args+stdin: %q %v", o.Prompt, err)
	}
	// stdin is a terminal: with args it is not read.
	o, err = parse(t, []string{"hi"}, "ignored", false)
	if err != nil || o.Prompt != "hi" {
		t.Fatalf("tty stdin: %q %v", o.Prompt, err)
	}
	o, err = parse(t, nil, "only stdin", false)
	if err != nil || o.Prompt != "only stdin" {
		t.Fatalf("no args reads stdin: %q %v", o.Prompt, err)
	}
}

func TestParseFlagsUsageErrors(t *testing.T) {
	bad := [][]string{
		{"--bogus", "x"},
		{"--no-save", "--resume", "last", "x"},
		{"--ask", "--auto", "x"},
		{"--ask", "--mode", "auto", "x"},
		{},
		{"--timeout", "-1s", "x"},
	}
	for _, args := range bad {
		_, err := parse(t, args, "", false)
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("%v: err = %v, want UsageError", args, err)
		}
		if ExitCode("", err) != ExitUsage {
			t.Errorf("%v: exit %d, want 2", args, ExitCode("", err))
		}
	}
	o, err := parse(t, []string{"--auto", "--image", "a.png", "--image", "b.png", "x"}, "", false)
	if err != nil || o.Mode != "auto" || len(o.Images) != 2 {
		t.Fatalf("good flags: %+v %v", o, err)
	}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		stop string
		err  error
		want int
	}{
		{"end_turn", nil, 0},
		{"refusal", nil, 1},
		{"max_tokens", nil, 1},
		{"cancelled", nil, 1},
		{"", errors.New("boom"), 1},
		{"", &UsageError{"x"}, 2},
	}
	for _, c := range cases {
		if got := ExitCode(c.stop, c.err); got != c.want {
			t.Errorf("ExitCode(%q, %v) = %d, want %d", c.stop, c.err, got, c.want)
		}
	}
	if (Interrupted{Signal: syscall.SIGTERM}).Code() != 143 {
		t.Error("SIGTERM should exit 143")
	}
}

func TestResultJSONShape(t *testing.T) {
	n := 5
	res := Result{Output: "hi", FinalOutput: "hi", SessionID: "s1", StopReason: "end_turn", Steps: 1,
		Usage: Usage{InputTokens: 10, OutputTokens: 2, CacheReadTokens: &n}, ToolCalls: []ToolCall{{Name: "Read a.go", Kind: "read", Status: "completed"}}}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"output", "final_output", "exit_code", "model", "session_id", "stop_reason", "steps", "usage", "tool_calls"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %q in %s", k, b)
		}
	}
	if _, ok := m["error"]; ok {
		t.Errorf("error should be omitted when empty: %s", b)
	}
	u := m["usage"].(map[string]any)
	if u["cache_read_tokens"] != float64(5) {
		t.Errorf("cache_read_tokens: %v", u)
	}
	if _, ok := u["cache_write_tokens"]; ok {
		t.Errorf("cache_write_tokens should be omitted: %v", u)
	}
	empty, _ := json.Marshal(Result{ToolCalls: []ToolCall{}})
	if !bytes.Contains(empty, []byte(`"tool_calls":[]`)) {
		t.Errorf("tool_calls should be [] not null: %s", empty)
	}
}

func TestRejectOutcome(t *testing.T) {
	opts := []acp.PermissionOption{
		{OptionId: "allow", Kind: acp.PermissionOptionKindAllowOnce},
		{OptionId: "never", Kind: acp.PermissionOptionKindRejectAlways},
		{OptionId: "no", Kind: acp.PermissionOptionKindRejectOnce},
	}
	if o := RejectOutcome(opts); o.Selected == nil || o.Selected.OptionId != "no" {
		t.Errorf("want reject_once, got %+v", o)
	}
	if o := RejectOutcome(opts[:2]); o.Selected == nil || o.Selected.OptionId != "never" {
		t.Errorf("want reject_always fallback, got %+v", o)
	}
	if o := RejectOutcome(opts[:1]); o.Cancelled == nil {
		t.Errorf("want cancelled, got %+v", o)
	}
	for in, want := range map[string]acp.PermissionOptionId{"y": "allow", "n": "no", "2": "never", "": "no"} {
		if got, ok := PickOption(opts, in); !ok || got != want {
			t.Errorf("PickOption(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	if _, ok := PickOption(opts, "a"); ok {
		t.Error("no allow_always option, 'a' should not match")
	}
}

func TestLatestSession(t *testing.T) {
	s := func(id, at string) acp.SessionInfo {
		i := acp.SessionInfo{SessionId: acp.SessionId(id)}
		if at != "" {
			i.UpdatedAt = &at
		}
		return i
	}
	got := LatestSession([]acp.SessionInfo{s("old", "2026-01-01T00:00:00Z"), s("none", ""), s("new", "2026-03-01T00:00:00Z"), s("mid", "2026-02-01T00:00:00Z")})
	if got != "new" {
		t.Errorf("LatestSession = %q", got)
	}
	if LatestSession(nil) != "" {
		t.Error("empty list")
	}
}

func TestMatchChoice(t *testing.T) {
	opts := []acp.SessionConfigSelectOption{{Value: "claude:max/claude-opus-5-5#medium", Name: "Opus"}, {Value: "codex:pro/gpt-6.2-sol", Name: "Sol"}}
	s := &acp.SessionConfigOptionSelect{Id: "model", Options: acp.SessionConfigSelectOptions{Ungrouped: (*acp.SessionConfigSelectOptionsUngrouped)(&opts)}}
	for in, want := range map[string]string{"codex:pro/gpt-6.2-sol": "codex:pro/gpt-6.2-sol", "opus": "claude:max/claude-opus-5-5#medium", "gpt-6.2-sol": "codex:pro/gpt-6.2-sol"} {
		if v, err := matchChoice(s, in); err != nil || string(v) != want {
			t.Errorf("matchChoice(%q) = %q %v", in, v, err)
		}
	}
	if _, err := matchChoice(s, "nope"); err == nil || !strings.Contains(err.Error(), "choices: claude:max") {
		t.Errorf("invalid choice error should list choices: %v", err)
	}
}

func TestDiffStat(t *testing.T) {
	if p, m := DiffStat("a\nb\nc\n", "a\nB\nc\nd\n"); p != 2 || m != 1 {
		t.Errorf("DiffStat = +%d -%d", p, m)
	}
}

func TestStreamingAndSteps(t *testing.T) {
	var out bytes.Buffer
	r := &runner{o: Options{Stdout: &out}, tools: map[acp.ToolCallId]*tool{}, inTurn: true, sid: "s"}
	r.out = newProgress(io.Discard, false, false, false)
	c := &client{r: r}
	send := func(u acp.SessionUpdate) {
		_ = c.SessionUpdate(t.Context(), acp.SessionNotification{SessionId: "s", Update: u})
	}
	text := func(s string) acp.SessionUpdate {
		return acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: s}}}}
	}
	send(text("Let me look."))
	send(acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{ToolCallId: "t1", Title: "Read a.go", Kind: acp.ToolKindRead, Status: acp.ToolCallStatusCompleted}})
	send(text("It is "))
	send(text("demo."))
	var res Result
	r.finish(&res)
	if res.Steps != 2 {
		t.Errorf("steps = %d, want 2", res.Steps)
	}
	if got := r.segmentText(); got != "It is demo." {
		t.Errorf("final segment = %q", got)
	}
	if out.String() != "Let me look.\n\nIt is demo.\n" || res.Output != "Let me look.\n\nIt is demo." {
		t.Errorf("stdout %q output %q", out.String(), res.Output)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Status != "completed" || res.ToolCalls[0].Kind != "read" {
		t.Errorf("tool calls %+v", res.ToolCalls)
	}
}
