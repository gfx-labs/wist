package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestParseXresources(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "colors"), []byte("*color2: #00ff00\n"), 0o644)
	data := `! a comment
#define BLUE #0000ff
#ifdef FOO
#include "colors"
*.color0: #000000
*color1:  #ff0000
URxvt*color1: #aa0000
*color4: BLUE
XTerm*background: #101010
*.background: #202020
*.foreground: rgb:ff/ff/ff
*.cursorColor: #abc
!*color5: #123456
`
	x := ParseXresources(data, dir)
	cases := map[string]string{
		"color0": "#000000", "color1": "#aa0000", "color2": "#00ff00", "color4": "#0000ff",
		"background": "#101010", "foreground": "rgb:ff/ff/ff", "cursorColor": "#abc",
	}
	for k, want := range cases {
		if got, _ := x.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if _, ok := x.Get("color5"); ok {
		t.Error("commented color5 was parsed")
	}
	if c, ok := parseXColor("rgb:ff/ff/ff"); !ok || c.Hex() != 0xffffff {
		t.Errorf("rgb: parse = %x", c.Hex())
	}
	if c, _ := parseXColor("#abc"); c.Hex() != 0xaabbcc {
		t.Errorf("#abc = %x", c.Hex())
	}
}

func TestXresourcesThemeFallback(t *testing.T) {
	th := XresourcesTheme(ParseXresources("*color4: #112233\n", ""))
	if th["accent"] != "#112233" {
		t.Errorf("accent = %s", th["accent"])
	}
	if th["red"] != themePresets["dark"]["red"] {
		t.Errorf("red did not fall back: %s", th["red"])
	}
}

func TestApplyXresourcesEnv(t *testing.T) {
	p := filepath.Join(t.TempDir(), "Xresources")
	os.WriteFile(p, []byte("*.foreground: #eeeeee\n*.background: #111111\n*.color4: #3366ff\n*.color2: #22cc22\n*.color1: #cc2222\n"), 0o644)
	t.Setenv("WIST_XRESOURCES", p)
	defer ApplyTheme("dark", nil)
	if w := ApplyTheme("xresources", nil); len(w) != 0 {
		t.Fatal(w)
	}
	if !truecolor() {
		return
	}
	if CAccent != tcell.GetColor("#3366ff") || CText != tcell.GetColor("#eeeeee") {
		t.Errorf("accent %x text %x", CAccent.Hex(), CText.Hex())
	}
}

func TestTerminalTheme(t *testing.T) {
	defer ApplyTheme("dark", nil)
	if w := ApplyTheme("terminal", nil); len(w) != 0 {
		t.Fatal(w)
	}
	if CText != tcell.ColorDefault || CCodeBg != tcell.ColorDefault {
		t.Error("terminal theme should use default fg/bg")
	}
	for _, c := range []tcell.Color{CAccent, CGreen, CRed, CYellow, CDim} {
		if c&tcell.ColorIsRGB != 0 || c-tcell.ColorValid > 15 {
			t.Errorf("non-ANSI color %v", c)
		}
	}
}
