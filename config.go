package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// config is ~/.config/wist/config.toml. Only a small TOML subset is
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
type config struct {
	towa      string
	towaArgs  []string
	agent     string
	spawnHook string
	theme     string
	colors    map[string]string
	keys      map[string][]string
	warnings  []string
}

func configPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "wist", "config.toml")
}

func loadConfig() config {
	p := configPath()
	f, err := os.Open(p)
	if err != nil {
		if yml := strings.TrimSuffix(p, ".toml") + ".yaml"; fileExists(yml) {
			return config{warnings: []string{"config: " + yml + " found but only config.toml is supported"}}
		}
		return config{}
	}
	defer f.Close()
	c, err := parseConfig(bufio.NewScanner(f))
	if err != nil {
		c.warnings = append(c.warnings, fmt.Sprintf("config %s: %v", p, err))
	}
	return c
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func parseConfig(sc *bufio.Scanner) (config, error) {
	c := config{colors: map[string]string{}, keys: map[string][]string{}}
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
				c.towa = one
			case "towa_args":
				c.towaArgs = vals
			case "agent":
				c.agent = one
			case "spawn_hook":
				c.spawnHook = one
			case "theme":
				c.theme = one
			default:
				c.warnings = append(c.warnings, fmt.Sprintf("config line %d: unknown key %q", n, k))
			}
		case "theme":
			if k == "preset" || k == "name" {
				c.theme = one
			} else {
				c.colors[k] = one
			}
		case "keys", "keybindings":
			c.keys[k] = vals
		default:
			c.warnings = append(c.warnings, fmt.Sprintf("config line %d: unknown section [%s]", n, section))
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

// themeColors points at the color variables declared in markdown.go so a
// theme can replace them. The style variables built from them are rebuilt
// by applyTheme.
var themeColors = map[string]*tcell.Color{
	"text": &cText, "dim": &cDim, "faint": &cFaint, "accent": &cAccent, "user": &cUser,
	"green": &cGreen, "red": &cRed, "yellow": &cYellow, "cyan": &cCyan, "orange": &cOrange,
	"code_bg": &cCodeBg, "panel_bg": &cPanelBg, "sel_bg": &cSelBg, "add_bg": &cAddBg, "del_bg": &cDelBg,
}

var themePresets = map[string]map[string]string{
	"dark": {
		"text": "#d8dbe2", "dim": "#7a808c", "faint": "#4a4f5a", "accent": "#8ab4f8", "user": "#c3a6ff",
		"green": "#8bd58b", "red": "#f28b82", "yellow": "#f0c674", "cyan": "#7dd3e0", "orange": "#f5a97f",
		"code_bg": "#1e2127", "panel_bg": "#23262d", "sel_bg": "#333a4a", "add_bg": "#1c3323", "del_bg": "#3b1f22",
	},
	"light": {
		"text": "#24292f", "dim": "#6e7781", "faint": "#afb8c1", "accent": "#0969da", "user": "#8250df",
		"green": "#1a7f37", "red": "#cf222e", "yellow": "#9a6700", "cyan": "#1b7c83", "orange": "#bc4c00",
		"code_bg": "#f6f8fa", "panel_bg": "#eaeef2", "sel_bg": "#d0d7de", "add_bg": "#dafbe1", "del_bg": "#ffebe9",
	},
	"catppuccin-mocha": {
		"text": "#cdd6f4", "dim": "#9399b2", "faint": "#585b70", "accent": "#89b4fa", "user": "#cba6f7",
		"green": "#a6e3a1", "red": "#f38ba8", "yellow": "#f9e2af", "cyan": "#94e2d5", "orange": "#fab387",
		"code_bg": "#181825", "panel_bg": "#313244", "sel_bg": "#45475a", "add_bg": "#2a3a2e", "del_bg": "#3e2a33",
	},
	"gruvbox": {
		"text": "#ebdbb2", "dim": "#a89984", "faint": "#665c54", "accent": "#83a598", "user": "#d3869b",
		"green": "#b8bb26", "red": "#fb4934", "yellow": "#fabd2f", "cyan": "#8ec07c", "orange": "#fe8019",
		"code_bg": "#1d2021", "panel_bg": "#3c3836", "sel_bg": "#504945", "add_bg": "#32361a", "del_bg": "#3c1f1e",
	},
	"tokyonight": {
		"text": "#c0caf5", "dim": "#737aa2", "faint": "#414868", "accent": "#7aa2f7", "user": "#bb9af7",
		"green": "#9ece6a", "red": "#f7768e", "yellow": "#e0af68", "cyan": "#7dcfff", "orange": "#ff9e64",
		"code_bg": "#16161e", "panel_bg": "#1f2335", "sel_bg": "#2e3c64", "add_bg": "#20303b", "del_bg": "#37222c",
	},
}

func themeNames() []string {
	var out []string
	for n := range themePresets {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// applyTheme sets the color variables from a preset and overrides and
// rebuilds the derived styles. It returns warnings for bad entries.
func applyTheme(preset string, overrides map[string]string) []string {
	var warn []string
	if preset == "" {
		preset = "dark"
	}
	base, ok := themePresets[preset]
	if !ok {
		warn = append(warn, fmt.Sprintf("theme: unknown preset %q (have %s)", preset, strings.Join(themeNames(), ", ")))
		base = themePresets["dark"]
	}
	set := func(k, v string) {
		p, ok := themeColors[k]
		if !ok {
			warn = append(warn, fmt.Sprintf("theme: unknown color %q", k))
			return
		}
		c := tcell.GetColor(v)
		if c == tcell.ColorDefault && v != "default" {
			warn = append(warn, fmt.Sprintf("theme: bad color %q for %s", v, k))
			return
		}
		*p = c
	}
	for k, v := range base {
		set(k, v)
	}
	for k, v := range overrides {
		set(k, v)
	}
	stBase = tcell.StyleDefault.Foreground(cText)
	stDim = tcell.StyleDefault.Foreground(cDim)
	stFaint = tcell.StyleDefault.Foreground(cFaint)
	stAccent = tcell.StyleDefault.Foreground(cAccent)
	stUser = tcell.StyleDefault.Foreground(cUser)
	stGreen = tcell.StyleDefault.Foreground(cGreen)
	stRed = tcell.StyleDefault.Foreground(cRed)
	stYellow = tcell.StyleDefault.Foreground(cYellow)
	stCyan = tcell.StyleDefault.Foreground(cCyan)
	stCode = tcell.StyleDefault.Foreground(cOrange)
	return warn
}

func (u *UI) pickTheme() {
	var items []pickItem
	sel := 0
	for i, n := range themeNames() {
		if n == u.themeName {
			sel = i
		}
		items = append(items, pickItem{label: n, value: n, current: n == u.themeName})
	}
	u.openPicker("theme", items, sel, func(it pickItem) { u.setTheme(it.value) })
}

func (u *UI) setTheme(name string) {
	if _, ok := themePresets[name]; !ok {
		u.errorf("unknown theme %q (have %s)", name, strings.Join(themeNames(), ", "))
		return
	}
	// Explicit color overrides from the config still apply on top.
	for _, w := range applyTheme(name, u.cfg.colors) {
		u.notify(w)
	}
	u.themeName = name
	for _, b := range u.blocks {
		b.dirty()
	}
	u.sc.Sync()
	u.notify("theme → " + name)
}
