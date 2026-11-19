// Package config reads ~/.config/wist/config.yaml.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gfx-labs/wist/pkg/notify"
	"go.yaml.in/yaml/v3"
)

// Config is ~/.config/wist/config.yaml (config.yml is also accepted).
//
//	towa: towa
//	towa_args: ["--models=claude:max/claude-opus-5-5"]
//	towa_socket: /run/user/1000/towa.sock  # connect to a towa daemon, starting it when absent
//	agent: towa              # or a registry id such as gemini
//	spawn_hook: "tmux split-window -h {cmd}"
//	input_mode: vim         # or emacs (default)
//	theme:
//	  preset: tokyonight     # or theme: tokyonight
//	  accent: "#7aa2f7"
//	keys:
//	  model_picker: ctrl+g
//	  theme_picker: [f2, alt+t]
type Config struct {
	Towa       string              // path of the towa binary
	TowaArgs   []string            // default towa arguments
	TowaSocket string              // towa daemon socket to connect to, started when absent
	Agent      string              // default registry agent id
	SpawnHook  string              // shell command that opens {cmd} in a new pane
	Theme      string              // theme preset name
	Notify     string              // auto, off, desktop or terminal
	InputMode  string              // emacs or vim
	Colors     map[string]string   // theme color overrides
	Keys       map[string][]string // key bindings by action name
	Warnings   []string            // problems found while loading
}

func dir() string {
	d := os.Getenv("XDG_CONFIG_HOME")
	if d == "" {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".config")
	}
	return filepath.Join(d, "wist")
}

// Path is the path of the config file, under $XDG_CONFIG_HOME:
// config.yaml, or config.yml when only that exists.
func Path() string {
	p := filepath.Join(dir(), "config.yaml")
	if yml := filepath.Join(dir(), "config.yml"); !fileExists(p) && fileExists(yml) {
		return yml
	}
	return p
}

// Load reads the config file. Problems are reported in Warnings,
// never as an error, so a bad config still starts wist.
func Load() Config {
	p := Path()
	b, err := os.ReadFile(p)
	if err != nil {
		if toml := filepath.Join(dir(), "config.toml"); fileExists(toml) {
			return Config{Warnings: []string{"config: " + toml + " is no longer read, convert it to " + p}}
		}
		return Config{}
	}
	c, err := parse(b)
	if err != nil {
		c.Warnings = append(c.Warnings, fmt.Sprintf("config %s: %v", p, err))
	}
	return c
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// strs accepts a scalar or a sequence of scalars.
func strs(n *yaml.Node) ([]string, error) {
	switch n.Kind {
	case yaml.ScalarNode:
		return []string{n.Value}, nil
	case yaml.SequenceNode:
		var out []string
		for _, e := range n.Content {
			if e.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("line %d: expected a string", e.Line)
			}
			out = append(out, e.Value)
		}
		return out, nil
	}
	return nil, fmt.Errorf("line %d: expected a string or list", n.Line)
}

func parse(b []byte) (Config, error) {
	c := Config{Colors: map[string]string{}, Keys: map[string][]string{}}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return c, err
	}
	if len(doc.Content) == 0 {
		return c, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return c, fmt.Errorf("top level must be a mapping")
	}
	warn := func(n *yaml.Node, f string, a ...any) {
		c.Warnings = append(c.Warnings, fmt.Sprintf("config line %d: ", n.Line)+fmt.Sprintf(f, a...))
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		kn, v := root.Content[i], root.Content[i+1]
		k := kn.Value
		if k == "theme" && v.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(v.Content); j += 2 {
				tk, tv := v.Content[j].Value, v.Content[j+1]
				if tk == "preset" || tk == "name" {
					c.Theme = tv.Value
				} else {
					c.Colors[tk] = tv.Value
				}
			}
			continue
		}
		if k == "keys" || k == "keybindings" {
			if v.Kind != yaml.MappingNode {
				warn(v, "keys must be a mapping")
				continue
			}
			for j := 0; j+1 < len(v.Content); j += 2 {
				vals, err := strs(v.Content[j+1])
				if err != nil {
					return c, err
				}
				c.Keys[v.Content[j].Value] = vals
			}
			continue
		}
		vals, err := strs(v)
		if err != nil {
			return c, err
		}
		one := strings.Join(vals, " ")
		switch k {
		case "towa":
			c.Towa = one
		case "towa_args":
			c.TowaArgs = vals
		case "towa_socket":
			c.TowaSocket = one
		case "agent":
			c.Agent = one
		case "spawn_hook":
			c.SpawnHook = one
		case "theme":
			c.Theme = one
		case "notify":
			if notify.ValidMode(one) {
				c.Notify = one
			} else {
				warn(v, "notify must be auto, off, desktop or terminal")
			}
		case "input_mode":
			if one == "emacs" || one == "vim" {
				c.InputMode = one
			} else {
				warn(v, "input_mode must be emacs or vim")
			}
		default:
			warn(kn, "unknown key %q", k)
		}
	}
	return c, nil
}

// --- themes -------------------------------------------------------------
