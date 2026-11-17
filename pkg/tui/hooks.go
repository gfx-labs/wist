package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// Hooks owned by the rendering code (code blocks, selection, search,
// panels). The input code calls them first and stops when they report the
// event was handled. State lives in rs (render_state.go); there is one UI.

// renderKey handles a key meant for rendering features.
func (u *UI) renderKey(ev *tcell.EventKey) bool {
	if rs.search.open && u.searchKey(ev) {
		return true
	}
	if ev.Modifiers()&tcell.ModAlt != 0 && ev.Key() == tcell.KeyRune {
		switch r := ev.Rune(); {
		case r >= '1' && r <= '9':
			u.copyCode(int(r - '0'))
			return true
		case r == '/':
			u.openSearch("")
			return true
		case r == 'i':
			u.togglePanel("")
			return true
		case r == '[' || r == ']':
			if rs.panel.open && rs.panel.mode == "image" {
				u.cycleImage(map[rune]int{'[': -1, ']': 1}[r])
				return true
			}
		}
	}
	if ev.Key() == tcell.KeyRune && ev.Modifiers() == 0 && (ev.Rune() == '[' || ev.Rune() == ']') &&
		rs.panel.open && rs.panel.mode == "image" && len(u.ed.Buf) == 0 && len(imgs.list) > 1 {
		u.cycleImage(map[rune]int{'[': -1, ']': 1}[ev.Rune()])
		return true
	}
	if ev.Key() == tcell.KeyEscape && rs.sel.active {
		rs.sel = selection{}
		return true
	}
	return false
}

// renderMouse handles clicks on code block copy buttons, transcript
// selection and wheel scrolling over the side panel.
func (u *UI) renderMouse(ev *tcell.EventMouse) bool {
	x, y := ev.Position()
	btn := ev.Buttons()
	v := rs.view
	if rs.panel.open && x >= rs.panel.x && y >= rs.panel.top && y <= rs.panel.bottom {
		switch btn {
		case tcell.WheelUp:
			rs.panel.scroll = max(0, rs.panel.scroll-3)
			return true
		case tcell.WheelDown:
			rs.panel.scroll += 3
			return true
		}
		return btn == tcell.Button1 || btn == tcell.ButtonNone && !rs.sel.dragging
	}
	switch {
	case btn&tcell.Button1 != 0:
		inside := y >= v.top && y <= v.bottom && x < v.x+v.w
		if !rs.sel.dragging {
			if !inside {
				return false
			}
			li := v.start + y - v.top
			if li < len(rs.lines) {
				if n := imageNumIn(rs.lines[li]); n > 0 {
					u.openImage(n)
					return true
				}
			}
			if ce := rs.codeAt(li); ce != nil {
				if ce.ref.closed {
					u.copyCodeEntry(ce)
					if strings.EqualFold(ce.ref.lang, "mermaid") {
						rs.panel.pinned = ce.ref.text
					}
				}
				return true
			}
			rs.sel = selection{active: true, dragging: true, a: pos{li, max(0, x-v.x)}}
			rs.sel.b = rs.sel.a
			return true
		}
		// Dragging: autoscroll at the edges.
		if y < v.top {
			u.scroll++
			y = v.top
		} else if y > v.bottom {
			u.scroll = max(0, u.scroll-1)
			y = v.bottom
		}
		rs.sel.b = pos{v.start + y - v.top, max(0, x-v.x)}
		return true
	case btn == tcell.ButtonNone && rs.sel.dragging:
		rs.sel.dragging = false
		text := selectedText(rs.lines, rs.sel.a, rs.sel.b)
		if rs.sel.a == rs.sel.b || strings.TrimSpace(text) == "" {
			rs.sel = selection{}
			return true
		}
		u.sc.SetClipboard([]byte(text))
		u.notify(fmt.Sprintf("copied %d chars", len([]rune(text))))
		return true
	}
	return false
}

// renderCommands are slash commands owned by the rendering code. They are
// listed in completion and /help next to the local commands.
var renderCommands = []localCmd{
	{"copy-code", "copy code block n (alt+1..9, 1 = newest on screen) or click ⧉ copy"},
	{"search", "search the transcript (alt+/, enter/n/N next/prev, esc closes)"},
	{"panel", "toggle the side panel: diagram, tool output or image (alt+i; [ ] cycle images)"},
}

// renderCommand runs a slash command owned by the rendering code and
// reports whether it handled it.
func (u *UI) renderCommand(name, arg string) bool {
	switch name {
	case "copy-code":
		n := 1
		if arg != "" {
			v, err := strconv.Atoi(strings.TrimSpace(arg))
			if err != nil || v < 1 {
				u.notify("usage: /copy-code [n]")
				return true
			}
			n = v
		}
		u.copyCode(n)
	case "search", "find":
		u.openSearch(arg)
	case "panel":
		u.togglePanel(strings.TrimSpace(arg))
	default:
		return false
	}
	return true
}
