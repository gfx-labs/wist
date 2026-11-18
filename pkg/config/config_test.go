package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfig(t *testing.T) {
	src := `# wist config
towa: /opt/towa   # path
towa_args: ["--models=a:b", '--x']
agent: gemini
spawn_hook: "tmux split-window -v {cmd}"
theme:
  preset: gruvbox
  accent: "#ff0000"
keys:
  model_picker: ctrl+g
  theme_picker: [f2, alt+t]
`
	c, err := parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if c.Towa != "/opt/towa" || c.Agent != "gemini" || c.Theme != "gruvbox" || c.SpawnHook != "tmux split-window -v {cmd}" {
		t.Fatalf("%+v", c)
	}
	if len(c.TowaArgs) != 2 || c.TowaArgs[1] != "--x" {
		t.Fatalf("towa_args %v", c.TowaArgs)
	}
	if c.Colors["accent"] != "#ff0000" || len(c.Keys["theme_picker"]) != 2 || c.Keys["model_picker"][0] != "ctrl+g" {
		t.Fatalf("%v %v", c.Colors, c.Keys)
	}
	if len(c.Warnings) != 0 {
		t.Fatal(c.Warnings)
	}
	if c, _ := parse([]byte("theme: terminal\nbogus: 1\n")); c.Theme != "terminal" || len(c.Warnings) != 1 {
		t.Fatalf("%+v", c)
	}
	if _, err := parse([]byte("x: \"open")); err == nil {
		t.Fatal("unterminated string should fail")
	}
	if _, err := parse([]byte("- a\n- b\n")); err == nil {
		t.Fatal("non-mapping should fail")
	}
}

func TestNotifyConfig(t *testing.T) {
	c, err := parse([]byte(`notify: desktop`))
	if err != nil || c.Notify != "desktop" || len(c.Warnings) != 0 {
		t.Fatalf("%+v %v", c, err)
	}
	c, _ = parse([]byte(`notify: bell`))
	if c.Notify != "" || len(c.Warnings) != 1 {
		t.Fatalf("bad mode accepted: %+v", c)
	}
}

func TestLoadPaths(t *testing.T) {
	d := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", d)
	w := filepath.Join(d, "wist")
	os.MkdirAll(w, 0o755)
	os.WriteFile(filepath.Join(w, "config.toml"), []byte(`agent = "x"`), 0o644)
	if c := Load(); len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "no longer read") {
		t.Fatalf("toml warning: %v", c.Warnings)
	}
	os.WriteFile(filepath.Join(w, "config.yml"), []byte("agent: yml\n"), 0o644)
	if c := Load(); c.Agent != "yml" {
		t.Fatalf("yml: %+v", c)
	}
	os.WriteFile(filepath.Join(w, "config.yaml"), []byte("agent: yaml\n"), 0o644)
	if c := Load(); c.Agent != "yaml" || !strings.HasSuffix(Path(), "config.yaml") {
		t.Fatalf("yaml: %+v", c)
	}
}
