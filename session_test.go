package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	acp "github.com/gfx-labs/go-acp"
)

func TestNormKey(t *testing.T) {
	for in, want := range map[string]string{
		"ctrl+g": "ctrl+g", "Ctrl+G": "ctrl+g", "C-g": "ctrl+g", "alt+Enter": "alt+enter",
		"M-x": "alt+x", "shift+tab": "shift+tab", "backtab": "shift+tab", "Escape": "esc",
		"alt+ctrl+k": "ctrl+alt+k", "F2": "f2", "pagedown": "pgdn", "ctrl++": "ctrl++",
	} {
		got, err := normKey(in)
		if err != nil || got != want {
			t.Errorf("normKey(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "hyper+x", "ctrl+banana", "f99"} {
		if _, err := normKey(bad); err == nil {
			t.Errorf("normKey(%q) should fail", bad)
		}
	}
}

func TestKeymapDefaultsHaveNoConflicts(t *testing.T) {
	km := buildKeymap(nil)
	if len(km.warnings) > 0 {
		t.Fatalf("default keymap warnings: %v", km.warnings)
	}
	if km.keys["ctrl+g"] != "model_picker" || km.keys["enter"] != "submit" || km.keys["shift+enter"] != "urgent_submit" {
		t.Fatalf("unexpected defaults: %v", km.keys)
	}
}

func TestKeymapOverridesAndConflicts(t *testing.T) {
	// A user binding shadows a default quietly and replaces the action's defaults.
	km := buildKeymap(map[string][]string{"theme_picker": {"ctrl+g"}})
	if km.keys["ctrl+g"] != "theme_picker" || len(km.warnings) != 0 {
		t.Fatalf("override: %v %v", km.keys["ctrl+g"], km.warnings)
	}
	if km.keys["alt+m"] != "model_picker" {
		t.Fatal("model_picker should keep alt+m")
	}
	// Two user bindings on one key conflict.
	km = buildKeymap(map[string][]string{"theme_picker": {"f2"}, "fork": {"F2"}})
	if len(km.warnings) != 1 || !strings.Contains(km.warnings[0], "f2") {
		t.Fatalf("want one f2 conflict, got %v", km.warnings)
	}
	// Unbinding and unknown actions.
	km = buildKeymap(map[string][]string{"model_picker": {"none"}, "nope": {"x"}})
	if _, ok := km.keys["ctrl+g"]; ok {
		t.Fatal("ctrl+g should be unbound")
	}
	if len(km.warnings) != 1 || !strings.Contains(km.warnings[0], "nope") {
		t.Fatalf("warnings: %v", km.warnings)
	}
}

func TestParseConfig(t *testing.T) {
	src := `# wist config
towa = "/opt/towa"   # path
towa_args = ["--models=a:b", '--x']
agent = gemini
spawn_hook = "tmux split-window -v {cmd}"

[theme]
preset = "gruvbox"
accent = "#ff0000"

[keys]
model_picker = "ctrl+g"
theme_picker = ["f2", "alt+t"]
`
	c, err := parseConfig(bufio.NewScanner(strings.NewReader(src)))
	if err != nil {
		t.Fatal(err)
	}
	if c.towa != "/opt/towa" || c.agent != "gemini" || c.theme != "gruvbox" || c.spawnHook != "tmux split-window -v {cmd}" {
		t.Fatalf("%+v", c)
	}
	if len(c.towaArgs) != 2 || c.towaArgs[1] != "--x" {
		t.Fatalf("towa_args %v", c.towaArgs)
	}
	if c.colors["accent"] != "#ff0000" || len(c.keys["theme_picker"]) != 2 {
		t.Fatalf("%v %v", c.colors, c.keys)
	}
	if len(c.warnings) != 0 {
		t.Fatal(c.warnings)
	}
	if _, err := parseConfig(bufio.NewScanner(strings.NewReader("x = \"open"))); err == nil {
		t.Fatal("unterminated string should fail")
	}
	if _, err := parseConfig(bufio.NewScanner(strings.NewReader("junk"))); err == nil {
		t.Fatal("line without = should fail")
	}
}

func TestApplyTheme(t *testing.T) {
	defer applyTheme("dark", nil)
	if w := applyTheme("light", map[string]string{"accent": "#123456"}); len(w) != 0 {
		t.Fatal(w)
	}
	if r, g, b := cAccent.RGB(); r != 0x12 || g != 0x34 || b != 0x56 {
		t.Fatalf("accent %x %x %x", r, g, b)
	}
	if w := applyTheme("nope", map[string]string{"bogus": "#fff", "text": "notacolor"}); len(w) != 3 {
		t.Fatalf("want 3 warnings, got %v", w)
	}
	for name := range themePresets {
		if len(themePresets[name]) != len(themeColors) {
			t.Errorf("preset %s sets %d of %d colors", name, len(themePresets[name]), len(themeColors))
		}
	}
}

func TestQueuedPrompt(t *testing.T) {
	if got := queuedPrompt([]string{"one", "  ", "two\n"}, false); got != "one\n\ntwo" {
		t.Fatalf("%q", got)
	}
	if got := queuedPrompt(nil, true); got != "" {
		t.Fatalf("%q", got)
	}
	got := queuedPrompt([]string{"stop, use bun"}, true)
	if !strings.HasPrefix(got, interruptNote) || !strings.HasSuffix(got, "stop, use bun") {
		t.Fatalf("%q", got)
	}
}

func TestSpawnCommand(t *testing.T) {
	if _, err := spawnCommand("", "wist", "", ""); err == nil {
		t.Fatal("no tmux and no hook should fail")
	}
	got, _ := spawnCommand("", "wist -p 'hi'", "/tmp/tmux", "%3")
	if got != `tmux split-window -h -t %3 'wist -p '\''hi'\'''` {
		t.Fatalf("%s", got)
	}
	got, _ = spawnCommand("kitty @ launch {cmd}", "wist x", "", "")
	if got != "kitty @ launch 'wist x'" {
		t.Fatalf("%s", got)
	}
	if shellJoin([]string{"a", "b c", "it's"}) != `a 'b c' 'it'\''s'` {
		t.Fatal(shellJoin([]string{"a", "b c", "it's"}))
	}
}

func TestEditStats(t *testing.T) {
	var s editStats
	old := "a\nb\nc\n"
	diff := func(p, o, n string) acp.ToolCallContent {
		return acp.ToolCallContent{Diff: &acp.ToolCallContentDiff{Path: p, OldText: &o, NewText: n}}
	}
	s.record("t1", []acp.ToolCallContent{diff("/r/x.go", old, "a\nB\nc\nd\n")})
	// The same tool call reported again must not double count.
	s.record("t1", []acp.ToolCallContent{diff("/r/x.go", old, "a\nB\nc\nd\n")})
	s.record("t2", []acp.ToolCallContent{{Diff: &acp.ToolCallContentDiff{Path: "/r/new.go", NewText: "1\n2\n"}}})
	if s.adds != 4 || s.dels != 1 || len(s.files) != 2 {
		t.Fatalf("%+v", s)
	}
	if s.summary() != "2 files +4 -1" {
		t.Fatal(s.summary())
	}
	if r := s.report("/r"); !strings.Contains(r, "x.go") || strings.Contains(r, "/r/") {
		t.Fatal(r)
	}
}

func TestRegistryResolve(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	a := regAgent{ID: "demo", Version: "1.0", Distribution: regDistribution{
		Binary: map[string]regBinary{"linux-x86_64": {Archive: "https://x/demo.tar.gz", Sha256: "ab", Cmd: "./bin/demo", Args: []string{"acp"}}},
		Npx:    &regPackage{Package: "@demo/acp", Args: []string{"--acp"}},
	}}
	c, err := a.resolve("linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if c.fetch == nil || filepath.Base(c.path) != "demo" || !strings.Contains(c.path, filepath.Join("agents", "demo", "1.0")) {
		t.Fatalf("%+v", c)
	}
	c, _ = a.resolve("darwin", "arm64")
	if c.path != "npx" || strings.Join(c.args, " ") != "-y @demo/acp --acp" {
		t.Fatalf("%+v", c)
	}
	a.Distribution.Npx = nil
	a.Distribution.Uvx = &regPackage{Package: "demo-acp"}
	if c, _ = a.resolve("windows", "amd64"); c.path != "uvx" {
		t.Fatalf("%+v", c)
	}
	a.Distribution.Uvx = nil
	if _, err := a.resolve("plan9", "386"); err == nil {
		t.Fatal("no distribution should fail")
	}
	a.Distribution.Binary["linux-x86_64"] = regBinary{Archive: "x", Cmd: "../../evil"}
	if _, err := a.resolve("linux", "amd64"); err == nil {
		t.Fatal("cmd escaping the install dir should fail")
	}
}

func TestRegistryParseAndVerify(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "r.json")
	os.WriteFile(p, []byte(`{"version":"1","agents":[{"id":"b","name":"B","version":"2","description":"d","distribution":{"uvx":{"package":"b"}}},{"id":"a","name":"A","version":"1","distribution":{"npx":{"package":"a"}}}]}`), 0o644)
	r, err := readRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Agents[0].ID != "a" || r.find("b") == nil || r.find("zz") != nil || r.find("b").kinds() != "uvx" {
		t.Fatalf("%+v", r)
	}
	f := filepath.Join(dir, "f")
	os.WriteFile(f, []byte("hello"), 0o644)
	if err := verifySha256(f, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"); err != nil {
		t.Fatal(err)
	}
	if verifySha256(f, "00") == nil || verifySha256(f, "") == nil {
		t.Fatal("bad hash should fail")
	}
	if _, err := safeJoin(dir, "../x"); err == nil {
		t.Fatal("traversal should fail")
	}
}
