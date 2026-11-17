// Package config reads ~/.config/wist/config.toml.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gfx-labs/wist/pkg/notify"
)

// Config is ~/.config/wist/config.toml. Only a small TOML subset is
// understood: [sections], key = "string" | 'string' | number | bool |
// ["array", "of", "strings"], and # comments.
//
//	towa = "towa"
//	towa_args = ["--models=claude:max/claude-opus-5-5"]
//	agent = "towa"            # or a registry id such as "gemini"
//	spawn_hook = "tmux split-window -h {cmd}"
//
//	[theme]
//	preset = "tokyonight"
//	accent = "#7aa2f7"
//
//	[keys]
//	model_picker = "ctrl+g"
//	theme_picker = ["f2", "alt+t"]
type Config struct {
	Towa      string              // path of the towa binary
	TowaArgs  []string            // default towa arguments
	Agent     string              // default registry agent id
	SpawnHook string              // shell command that opens {cmd} in a new pane
	Theme     string              // theme preset name
	Notify    string              // auto, off, desktop or terminal
	Colors    map[string]string   // theme color overrides
	Keys      map[string][]string // key bindings by action name
	Warnings  []string            // problems found while loading
}

// Path is the path of the config file, under $XDG_CONFIG_HOME.
func Path() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "wist", "config.toml")
}

// Load reads the config file. Problems are reported in Warnings,
// never as an error, so a bad config still starts wist.
func Load() Config {
	p := Path()
	f, err := os.Open(p)
	if err != nil {
		if yml := strings.TrimSuffix(p, ".toml") + ".yaml"; fileExists(yml) {
			return Config{Warnings: []string{"config: " + yml + " found but only config.toml is supported"}}
		}
		return Config{}
	}
	defer f.Close()
	c, err := parse(bufio.NewScanner(f))
	if err != nil {
		c.Warnings = append(c.Warnings, fmt.Sprintf("config %s: %v", p, err))
	}
	return c
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func parse(sc *bufio.Scanner) (Config, error) {
	c := Config{Colors: map[string]string{}, Keys: map[string][]string{}}
	section := ""
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(stripComment(sc.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return c, fmt.Errorf("line %d: expected key = value", n)
		}
		k = strings.Trim(strings.TrimSpace(k), `"'`)
		vals, err := parseValue(strings.TrimSpace(v))
		if err != nil {
			return c, fmt.Errorf("line %d: %v", n, err)
		}
		one := strings.Join(vals, " ")
		switch section {
		case "":
			switch k {
			case "towa":
				c.Towa = one
			case "towa_args":
				c.TowaArgs = vals
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
					c.Warnings = append(c.Warnings, fmt.Sprintf("config line %d: notify must be auto, off, desktop or terminal", n))
				}
			default:
				c.Warnings = append(c.Warnings, fmt.Sprintf("config line %d: unknown key %q", n, k))
			}
		case "theme":
			if k == "preset" || k == "name" {
				c.Theme = one
			} else {
				c.Colors[k] = one
			}
		case "keys", "keybindings":
			c.Keys[k] = vals
		default:
			c.Warnings = append(c.Warnings, fmt.Sprintf("config line %d: unknown section [%s]", n, section))
		}
	}
	return c, sc.Err()
}

// stripComment drops a # comment that is not inside quotes.
func stripComment(s string) string {
	var q rune
	for i, r := range s {
		switch {
		case q != 0 && r == q:
			q = 0
		case q == 0 && (r == '"' || r == '\''):
			q = r
		case q == 0 && r == '#':
			return s[:i]
		}
	}
	return s
}

func parseValue(v string) ([]string, error) {
	if strings.HasPrefix(v, "[") {
		if !strings.HasSuffix(v, "]") {
			return nil, fmt.Errorf("unterminated array (arrays must fit on one line)")
		}
		var out []string
		rest := strings.TrimSpace(v[1 : len(v)-1])
		for rest != "" {
			var item string
			var err error
			item, rest, err = scalar(rest)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
			rest = strings.TrimSpace(rest)
			rest = strings.TrimSpace(strings.TrimPrefix(rest, ","))
		}
		return out, nil
	}
	s, rest, err := scalar(v)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rest) != "" {
		return nil, fmt.Errorf("unexpected %q after value", rest)
	}
	return []string{s}, nil
}

// scalar reads one quoted or bare value from the front of s.
func scalar(s string) (val, rest string, err error) {
	switch {
	case strings.HasPrefix(s, `"`):
		for i := 1; i < len(s); i++ {
			if s[i] == '\\' {
				i++
				continue
			}
			if s[i] == '"' {
				v, err := strconv.Unquote(s[:i+1])
				return v, s[i+1:], err
			}
		}
		return "", "", fmt.Errorf("unterminated string")
	case strings.HasPrefix(s, "'"):
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", "", fmt.Errorf("unterminated string")
		}
		return s[1 : end+1], s[end+2:], nil
	}
	end := strings.IndexAny(s, ", ")
	if end < 0 {
		end = len(s)
	}
	return s[:end], s[end:], nil
}

// --- themes -------------------------------------------------------------
