package main

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
)

// An action is something a key can be bound to. The table is the source of
// truth for the default bindings, /keys and /help.
type action struct {
	name     string
	desc     string
	defaults []string
	run      func(u *UI)
}

// actions is filled in init because the actions reach /help, which lists
// them.
var actions []action

func init() {
	actions = []action{
		{"submit", "send (queued until the turn ends if one is running)", []string{"enter"}, func(u *UI) { u.submitEditor(false) }},
		{"urgent_submit", "newline, or during a turn: interrupt and send now", []string{"shift+enter", "alt+enter"}, func(u *UI) {
			if u.busy {
				u.submitEditor(true)
			} else {
				u.ed.insert("\n")
			}
		}},
		{"newline", "insert a newline", []string{"ctrl+j"}, func(u *UI) { u.ed.insert("\n") }},
		{"paste_image", "attach an image from the clipboard", []string{"ctrl+v", "alt+v"}, (*UI).pasteImage},
		{"paste_text", "paste text from the terminal clipboard (OSC 52)", nil, func(u *UI) { u.requestClipboard(false) }},
		{"edit_in_editor", "compose the input in $VISUAL/$EDITOR", []string{"alt+x"}, (*UI).editInEditor},
		{"suspend", "suspend wist to the shell", []string{"ctrl+z"}, (*UI).suspend},
		{"interrupt", "interrupt the turn, or scroll to the bottom", []string{"esc"}, func(u *UI) {
			if u.busy {
				u.cancelTurn()
			} else {
				u.scroll = 0
			}
		}},
		{"cancel", "interrupt, clear input, or quit when pressed twice", []string{"ctrl+c"}, (*UI).ctrlCAction},
		{"quit", "quit when the input is empty, else delete forward", []string{"ctrl+d"}, func(u *UI) {
			if len(u.ed.buf) == 0 {
				u.quit = true
			} else {
				u.ed.del()
			}
		}},
		{"cycle_mode", "cycle the permission mode", []string{"shift+tab"}, (*UI).cycleMode},
		{"history_search", "search prompt history", []string{"ctrl+r"}, (*UI).pickHistory},
		{"session_picker", "pick a stored session", []string{"ctrl+s"}, (*UI).pickSession},
		{"model_picker", "pick the model", []string{"ctrl+g", "alt+m"}, func(u *UI) {
			if m := u.modelOption(); m != nil {
				u.pickOption(m)
			}
		}},
		{"theme_picker", "pick a color theme", nil, (*UI).pickTheme},
		{"expand_last", "expand the last tool output", []string{"ctrl+o"}, (*UI).toggleLastTool},
		{"expand_all", "expand all tool output", []string{"alt+e"}, func(u *UI) { u.expandAll = !u.expandAll }},
		{"toggle_thinking", "show reasoning", []string{"ctrl+t"}, func(u *UI) {
			u.showThinking = !u.showThinking
			u.notify(fmt.Sprintf("show thinking: %v", u.showThinking))
		}},
		{"toggle_plan", "toggle the plan panel", []string{"alt+p"}, func(u *UI) { u.planHidden = !u.planHidden }},
		{"redraw", "redraw the screen", []string{"ctrl+l"}, func(u *UI) { u.sc.Sync() }},
		{"scroll_up", "scroll up half a page", []string{"pgup"}, func(u *UI) { u.scroll += u.pageSize() }},
		{"scroll_down", "scroll down half a page", []string{"pgdn"}, func(u *UI) { u.scroll = max(0, u.scroll-u.pageSize()) }},
		{"fork", "fork the session", nil, func(u *UI) { u.forkSession() }},
		{"split", "fork the session into a new pane", nil, func(u *UI) { u.split(false) }},
	}
}

func actionByName(name string) *action {
	for i := range actions {
		if actions[i].name == name {
			return &actions[i]
		}
	}
	return nil
}

// keymap maps a normalized key name to an action name.
type keymap struct {
	keys     map[string]string
	warnings []string
}

var keyAliases = map[string]string{
	"escape": "esc", "return": "enter", "ret": "enter", "pageup": "pgup", "pagedown": "pgdn",
	"pgdown": "pgdn", "del": "delete", "bs": "backspace", "spc": "space", "control": "ctrl",
	"meta": "alt", "opt": "alt", "option": "alt", "backtab": "shift+tab", "ins": "insert",
}

// normKey turns a user key spec such as "C-g", "Ctrl+G" or "alt+Enter"
// into the canonical "ctrl+alt+shift+base" form.
func normKey(spec string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(spec))
	if s == "" {
		return "", fmt.Errorf("empty key")
	}
	if a, ok := keyAliases[s]; ok {
		s = a
	}
	s = strings.NewReplacer("c-", "ctrl+", "m-", "alt+", "s-", "shift+").Replace(s)
	parts := strings.Split(s, "+")
	if strings.HasSuffix(s, "++") { // "ctrl++"
		parts = append(parts[:len(parts)-2], "+")
	}
	var ctrl, alt, shift bool
	base := parts[len(parts)-1]
	for _, m := range parts[:len(parts)-1] {
		if a, ok := keyAliases[m]; ok {
			m = a
		}
		switch m {
		case "ctrl":
			ctrl = true
		case "alt":
			alt = true
		case "shift":
			shift = true
		default:
			return "", fmt.Errorf("unknown modifier %q in %q", m, spec)
		}
	}
	if a, ok := keyAliases[base]; ok {
		base = a
		if strings.Contains(base, "+") { // backtab
			shift, base = true, "tab"
		}
	}
	if !validBase(base) {
		return "", fmt.Errorf("unknown key %q in %q", base, spec)
	}
	return joinKey(ctrl, alt, shift, base), nil
}

func joinKey(ctrl, alt, shift bool, base string) string {
	var b strings.Builder
	if ctrl {
		b.WriteString("ctrl+")
	}
	if alt {
		b.WriteString("alt+")
	}
	if shift {
		b.WriteString("shift+")
	}
	b.WriteString(base)
	return b.String()
}

var namedKeys = map[tcell.Key]string{
	tcell.KeyEnter: "enter", tcell.KeyTab: "tab", tcell.KeyBacktab: "shift+tab", tcell.KeyEsc: "esc",
	tcell.KeyBackspace: "backspace", tcell.KeyBackspace2: "backspace", tcell.KeyDelete: "delete",
	tcell.KeyInsert: "insert", tcell.KeyUp: "up", tcell.KeyDown: "down", tcell.KeyLeft: "left",
	tcell.KeyRight: "right", tcell.KeyHome: "home", tcell.KeyEnd: "end", tcell.KeyPgUp: "pgup",
	tcell.KeyPgDn: "pgdn",
}

func validBase(b string) bool {
	if len([]rune(b)) == 1 || b == "space" {
		return true
	}
	for _, n := range namedKeys {
		if n == b {
			return true
		}
	}
	var f int
	if _, err := fmt.Sscanf(b, "f%d", &f); err == nil && f >= 1 && f <= 24 {
		return true
	}
	return false
}

// eventKey names a key event in the canonical form used by normKey.
func eventKey(ev *tcell.EventKey) string {
	mod := ev.Modifiers()
	ctrl, alt, shift := mod&tcell.ModCtrl != 0, mod&tcell.ModAlt != 0, mod&tcell.ModShift != 0
	k := ev.Key()
	switch {
	case k == tcell.KeyRune:
		r := ev.Rune()
		if r == ' ' {
			return joinKey(ctrl, alt, false, "space")
		}
		return joinKey(ctrl, alt, false, string(unicode.ToLower(r)))
	case k == tcell.KeyBacktab:
		return joinKey(ctrl, alt, true, "tab")
	case namedKeys[k] != "":
		return joinKey(ctrl, alt, shift, namedKeys[k])
	case k >= tcell.KeyCtrlA && k <= tcell.KeyCtrlZ:
		return joinKey(true, alt, shift, string(rune('a'+k-tcell.KeyCtrlA)))
	case k >= tcell.KeyF1 && k <= tcell.KeyF24:
		return joinKey(ctrl, alt, shift, fmt.Sprintf("f%d", k-tcell.KeyF1+1))
	}
	return ""
}

// buildKeymap combines the defaults with user overrides. A user entry for
// an action replaces its defaults; "none" or "" unbinds it. A key claimed
// by two actions is reported and goes to the user's binding, then to the
// action listed first.
func buildKeymap(user map[string][]string) keymap {
	km := keymap{keys: map[string]string{}}
	var names []string
	for name := range user {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if actionByName(name) == nil {
			km.warnings = append(km.warnings, fmt.Sprintf("keys: unknown action %q", name))
		}
	}
	// User bindings first so they win conflicts against defaults. A user
	// binding silently shadows a default; anything else is a conflict.
	fromUser := map[string]bool{}
	for _, pass := range []bool{true, false} {
		for _, a := range actions {
			specs, isUser := user[a.name]
			if isUser != pass {
				continue
			}
			if !isUser {
				specs = a.defaults
			}
			for _, spec := range specs {
				if s := strings.ToLower(strings.TrimSpace(spec)); s == "" || s == "none" {
					continue
				}
				k, err := normKey(spec)
				if err != nil {
					km.warnings = append(km.warnings, "keys: "+err.Error())
					continue
				}
				if prev, ok := km.keys[k]; ok && prev != a.name {
					if isUser || !fromUser[k] {
						km.warnings = append(km.warnings, fmt.Sprintf("keys: %s is bound to both %s and %s; using %s", k, prev, a.name, prev))
					}
					continue
				}
				km.keys[k] = a.name
				fromUser[k] = isUser
			}
		}
	}
	return km
}

// bindings lists the keys bound to an action.
func (km keymap) bindings(name string) []string {
	var out []string
	for k, a := range km.keys {
		if a == name {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func (u *UI) keysText() string {
	var b strings.Builder
	b.WriteString("## key bindings\n\n| action | keys | description |\n|---|---|---|\n")
	for _, a := range actions {
		keys := strings.Join(u.keymap.bindings(a.name), ", ")
		if keys == "" {
			keys = "-"
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", a.name, keys, a.desc)
	}
	b.WriteString("\nEditor keys (ctrl+a/e/b/f/w/u/k/y, alt+b/f/d, arrows) apply when a key is not bound above.\n")
	fmt.Fprintf(&b, "Rebind in `%s` under `[keys]`, e.g. `model_picker = \"ctrl+g\"`.\n", homeRel(configPath()))
	if len(u.keymap.warnings) > 0 {
		b.WriteString("\n## warnings\n\n")
		for _, w := range u.keymap.warnings {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}
	return b.String()
}
