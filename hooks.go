package main

import "github.com/gdamore/tcell/v2"

// Hooks owned by the rendering code (code blocks, panels, images). The input
// code calls them first and stops when they report the event was handled.

// renderKey handles a key meant for rendering features.
func (u *UI) renderKey(ev *tcell.EventKey) bool { return false }

// renderMouse handles a mouse event meant for rendering features, such as a
// click on a code block's copy button.
func (u *UI) renderMouse(ev *tcell.EventMouse) bool { return false }
