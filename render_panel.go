package main

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// The side panel shows, at full height on the right, the pinned or most
// recent mermaid diagram, or else the last tool's output. A plan, when
// present, takes the top part of the panel.

type panelState struct {
	open           bool
	mode           string // "", "diagram", "tool" or "image"
	pinned         string // mermaid source clicked last
	scroll         int
	x, top, bottom int
	cacheKey       string
	cacheLines     []line
}

func (u *UI) togglePanel(mode string) {
	switch mode {
	case "diagram", "tool", "image":
		rs.panel.open, rs.panel.mode = true, mode
	case "", "toggle":
		rs.panel.open = !rs.panel.open
	case "off", "close":
		rs.panel.open = false
	default:
		u.notify("usage: /panel [diagram|tool|image|off]")
		return
	}
	rs.panel.scroll = 0
	if !rs.panel.open {
		rs.panel.cacheLines, rs.panel.cacheKey = nil, ""
	}
}

// panelContent picks what the panel shows and renders it for width w.
func (u *UI) panelContent(w int) (string, []line) {
	mode := rs.panel.mode
	src := rs.panel.pinned
	if src == "" {
		for i := len(u.blocks) - 1; i >= 0 && src == ""; i-- {
			b := u.blocks[i]
			if b.kind != kindAgent {
				continue
			}
			for j := len(b.codes) - 1; j >= 0; j-- {
				if b.codes[j].closed && strings.EqualFold(b.codes[j].lang, "mermaid") {
					src = b.codes[j].text
					break
				}
			}
		}
	}
	if mode != "tool" && src != "" {
		key := "d\x00" + src
		if rs.panel.cacheKey != key {
			ls := mermaid(src)
			if ls == nil {
				for _, s := range strings.Split(src, "\n") {
					ls = append(ls, line{{s, stDim}})
				}
			}
			rs.panel.cacheKey, rs.panel.cacheLines = key, ls
		}
		return "diagram", rs.panel.cacheLines
	}
	for i := len(u.blocks) - 1; i >= 0; i-- {
		if b := u.blocks[i]; b.kind == kindTool {
			body := b.bodyLines(w)
			head := b.toolLines(w, false)[0]
			return "tool", append([]line{head, {}}, body...)
		}
	}
	return "panel", []line{{{"nothing to show yet: ask for a ```mermaid diagram or run a tool", stFaint.Italic(true)}}}
}

func (u *UI) drawSidePanel(x, top, w, bottom int) {
	sc := u.sc
	rs.panel.x, rs.panel.top, rs.panel.bottom = x-1, top, bottom
	for y := top; y <= bottom; y++ {
		sc.SetContent(x-1, y, '│', nil, stFaint)
	}
	if len(u.plan) > 0 && !u.planHidden {
		pl := u.planLines(w - 2)
		pl = pl[:min(len(pl), (bottom-top)/3)]
		for _, l := range pl {
			drawLine(sc, x+1, top, x+w, l)
			top++
		}
		for i := x; i < x+w; i++ {
			sc.SetContent(i, top, '─', nil, stFaint)
		}
		top++
	}
	if rs.panel.mode == "image" && imgs.current() != nil {
		drawStr(sc, x+1, top, x+w, " image · alt+i ", stAccent.Bold(true))
		u.drawImagePanel(x+1, top+1, w-2, bottom)
		return
	}
	title, body := u.panelContent(w - 2)
	head := " " + title + " · alt+i "
	drawStr(sc, x+1, top, x+w, head, stAccent.Bold(true))
	top++
	h := bottom - top + 1
	if h <= 0 {
		return
	}
	rs.panel.scroll = min(rs.panel.scroll, max(0, len(body)-h))
	for i, l := range body[rs.panel.scroll:] {
		if i >= h {
			break
		}
		drawLine(sc, x+1, top+i, x+w, l)
	}
	if more := len(body) - rs.panel.scroll - h; more > 0 {
		msg := " ↓ " + itoa(more) + " "
		drawStr(sc, x+w-uniseg.StringWidth(msg)-1, bottom, x+w, msg, tcell.StyleDefault.Foreground(cText).Background(cSelBg))
	}
}
