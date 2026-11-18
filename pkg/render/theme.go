package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// themeColors points at the color variables declared in markdown.go so a
// theme can replace them. The style variables built from them are rebuilt
// by applyTheme.
var themeColors = map[string]*tcell.Color{
	"text": &CText, "dim": &CDim, "faint": &CFaint, "accent": &CAccent, "user": &CUser,
	"green": &CGreen, "red": &CRed, "yellow": &CYellow, "cyan": &CCyan, "orange": &COrange,
	"code_bg": &CCodeBg, "panel_bg": &CPanelBg, "sel_bg": &CSelBg, "add_bg": &CAddBg, "del_bg": &CDelBg,
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
	// terminal uses only the terminal's own palette (ANSI 0-15) and its
	// default foreground/background, so it follows the terminal's colors.
	"terminal": {
		"text": "default", "dim": "gray", "faint": "gray", "accent": "navy", "user": "purple",
		"green": "green", "red": "maroon", "yellow": "olive", "cyan": "teal", "orange": "red",
		"code_bg": "default", "panel_bg": "default", "sel_bg": "black", "add_bg": "default", "del_bg": "default",
	},
	// xresources is filled at apply time from LoadXresources.
	"xresources": nil,
	"tokyonight": {
		"text": "#c0caf5", "dim": "#737aa2", "faint": "#414868", "accent": "#7aa2f7", "user": "#bb9af7",
		"green": "#9ece6a", "red": "#f7768e", "yellow": "#e0af68", "cyan": "#7dcfff", "orange": "#ff9e64",
		"code_bg": "#16161e", "panel_bg": "#1f2335", "sel_bg": "#2e3c64", "add_bg": "#20303b", "del_bg": "#37222c",
	},
}

// HasTheme reports whether name is a theme preset.
func HasTheme(name string) bool { _, ok := themePresets[name]; return ok }

// ThemeNames lists the theme presets, sorted.
func ThemeNames() []string {
	var out []string
	for n := range themePresets {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ApplyTheme sets the color variables from a preset and overrides and
// rebuilds the derived styles. It returns warnings for bad entries.
func ApplyTheme(preset string, overrides map[string]string) []string {
	var warn []string
	if preset == "" {
		preset = "dark"
	}
	base, ok := themePresets[preset]
	if !ok {
		warn = append(warn, fmt.Sprintf("theme: unknown preset %q (have %s)", preset, strings.Join(ThemeNames(), ", ")))
		base = themePresets["dark"]
	}
	if preset == "xresources" {
		x, _, err := LoadXresources()
		if err != nil {
			warn = append(warn, "theme: "+err.Error()+", using dark")
			base = themePresets["dark"]
		} else {
			base = XresourcesTheme(x)
		}
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
	StBase = tcell.StyleDefault.Foreground(CText)
	StDim = tcell.StyleDefault.Foreground(CDim)
	StFaint = tcell.StyleDefault.Foreground(CFaint)
	StAccent = tcell.StyleDefault.Foreground(CAccent)
	StUser = tcell.StyleDefault.Foreground(CUser)
	StGreen = tcell.StyleDefault.Foreground(CGreen)
	StRed = tcell.StyleDefault.Foreground(CRed)
	StYellow = tcell.StyleDefault.Foreground(CYellow)
	StCyan = tcell.StyleDefault.Foreground(CCyan)
	StCode = tcell.StyleDefault.Foreground(COrange)
	refreshStyles()
	return warn
}
