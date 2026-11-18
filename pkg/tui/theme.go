package tui

import (
	"strings"

	"github.com/gfx-labs/wist/pkg/render"
)

func (u *UI) pickTheme() {
	var items []pickItem
	sel := 0
	for i, n := range render.ThemeNames() {
		if n == u.themeName {
			sel = i
		}
		items = append(items, pickItem{label: n, detail: themeDetail[n], value: n, current: n == u.themeName})
	}
	u.openPicker("theme", items, sel, func(it pickItem) { u.setTheme(it.value) })
}

func (u *UI) setTheme(name string) {
	if !render.HasTheme(name) {
		u.errorf("unknown theme %q (have %s)", name, strings.Join(render.ThemeNames(), ", "))
		return
	}
	// Explicit color overrides from the config still apply on top.
	for _, w := range render.ApplyTheme(name, u.cfg.Colors) {
		u.notify(w)
	}
	u.themeName = name
	for _, b := range u.blocks {
		b.dirty()
	}
	u.sc.Sync()
	renderSynced()
	u.notify("theme → " + name)
}

var themeDetail = map[string]string{
	"xresources": "colors from xrdb -query, ~/.Xresources or ~/.Xdefaults ($WIST_XRESOURCES)",
	"terminal":   "ANSI 0-15 and the terminal's default fg/bg",
}
