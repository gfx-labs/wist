package tui

import (
	"os"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/gfx-labs/wist/pkg/editor"
	"github.com/gfx-labs/wist/pkg/render"
)

// Terminal capability handling: rune fallbacks, 256-color palette mapping
// and the mode-tinted cursor. Done lazily on the first draw.

var termState struct {
	inited    bool
	cursorKey string
	w, h      int
}

// runeFallbacks are ASCII stand-ins for the glyphs wist draws, used when
// the terminal's encoding cannot show them.
var runeFallbacks = map[rune]string{
	'⧉': "#", '⠋': "|", '⠙': "/", '⠹': "-", '⠸': "\\", '⠼': "|", '⠴': "/", '⠦': "-", '⠧': "\\", '⠇': "|", '⠏': "/",
	'▰': "#", '▱': "-", '❯': ">", '🖼': "I", '✓': "v", '✗': "x", '✻': "*", '⎇': "b",
	'╭': "+", '╮': "+", '╰': "+", '╯': "+", '│': "|", '─': "-", '┊': ":", '▎': "|",
	'⚑': "!", '⏳': "~", '⌕': "?", '●': "*", '○': "o", '▸': ">", '☐': "[ ]", '☑': "[x]",
	'◆': "*", '⚠': "!", '↑': "^", '↓': "v", '…': "...", '⋯': "...", '•': "*", '⇣': "v",
	'⇄': "<>", '✎': "e", '⚙': "*", '📖': "r", '🗑': "d", '🌐': "w", '▀': "#", '▏': "|",
	'┬': "+", '┌': "+", '┐': "+", '└': "+", '┘': "+", '┴': "+", '·': ".",
}

func (u *UI) termInit() {
	sc := u.sc
	if !termState.inited {
		termState.inited = true
		for r, s := range runeFallbacks {
			if !sc.CanDisplay(r, false) {
				sc.RegisterRuneFallback(r, s)
			}
		}
	}
	render.SetColors(sc.Colors())
	if w, h := sc.Size(); w != termState.w || h != termState.h {
		termState.w, termState.h = w, h
		gfx.invalidate()
	}
}

// cursorTint colors the cursor by the session mode: accent when the
// agent asks, yellow in auto mode. It is a bar, or a block in vim normal
// and visual mode.
func (u *UI) cursorTint() {
	c := render.CAccent
	if strings.EqualFold(currentName(u.modeOption()), "auto") {
		c = render.CYellow
	}
	shape := tcell.CursorStyleSteadyBar
	if u.ed.Mode() >= editor.ModeNormal {
		shape = tcell.CursorStyleSteadyBlock
	}
	key := strconv.Itoa(int(c)) + "/" + strconv.Itoa(int(shape))
	if key == termState.cursorKey {
		return
	}
	termState.cursorKey = key
	u.sc.SetCursorStyle(shape, c)
}

// errorStyle is a red curly underline for the key text of failures.
func errorStyle(st tcell.Style) tcell.Style {
	return st.Underline(tcell.UnderlineStyleCurly, render.CRed)
}

// fileURL makes a file:// link for a tool location path.
func fileURL(p string) string {
	if p == "" || !strings.HasPrefix(p, "/") {
		return ""
	}
	host, _ := os.Hostname()
	return "file://" + host + p
}
