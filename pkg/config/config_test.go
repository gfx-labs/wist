package config

import (
	"bufio"
	"strings"
	"testing"
)

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
	c, err := parse(bufio.NewScanner(strings.NewReader(src)))
	if err != nil {
		t.Fatal(err)
	}
	if c.Towa != "/opt/towa" || c.Agent != "gemini" || c.Theme != "gruvbox" || c.SpawnHook != "tmux split-window -v {cmd}" {
		t.Fatalf("%+v", c)
	}
	if len(c.TowaArgs) != 2 || c.TowaArgs[1] != "--x" {
		t.Fatalf("towa_args %v", c.TowaArgs)
	}
	if c.Colors["accent"] != "#ff0000" || len(c.Keys["theme_picker"]) != 2 {
		t.Fatalf("%v %v", c.Colors, c.Keys)
	}
	if len(c.Warnings) != 0 {
		t.Fatal(c.Warnings)
	}
	if _, err := parse(bufio.NewScanner(strings.NewReader("x = \"open"))); err == nil {
		t.Fatal("unterminated string should fail")
	}
	if _, err := parse(bufio.NewScanner(strings.NewReader("junk"))); err == nil {
		t.Fatal("line without = should fail")
	}
}

func TestNotifyConfig(t *testing.T) {
	c, err := parse(bufio.NewScanner(strings.NewReader(`notify = "desktop"`)))
	if err != nil || c.Notify != "desktop" || len(c.Warnings) != 0 {
		t.Fatalf("%+v %v", c, err)
	}
	c, _ = parse(bufio.NewScanner(strings.NewReader(`notify = "bell"`)))
	if c.Notify != "" || len(c.Warnings) != 1 {
		t.Fatalf("bad mode accepted: %+v", c)
	}
}
