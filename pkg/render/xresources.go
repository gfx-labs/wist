package render

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

// Xresources holds resource values keyed by their full resource name
// (e.g. "URxvt*color4"), in definition order.
type Xresources struct {
	keys []string
	vals map[string]string
}

// ParseXresources parses resource lines. It understands "!" comments,
// "#define NAME value" substitution and `#include "file"` (relative to dir),
// and ignores every other preprocessor directive.
func ParseXresources(data, dir string) *Xresources {
	x := &Xresources{vals: map[string]string{}}
	defs := map[string]string{}
	x.parse(data, dir, defs, 0)
	return x
}

func (x *Xresources) parse(data, dir string, defs map[string]string, depth int) {
	sc := bufio.NewScanner(strings.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '!' {
			continue
		}
		if line[0] == '#' {
			f := strings.Fields(strings.TrimSpace(line[1:]))
			if len(f) == 0 {
				continue
			}
			switch f[0] {
			case "define":
				if len(f) >= 3 {
					defs[f[1]] = strings.Join(f[2:], " ")
				}
			case "include":
				if len(f) >= 2 && depth < 8 {
					p := strings.Trim(f[1], `"<>`)
					if !filepath.IsAbs(p) {
						p = filepath.Join(dir, p)
					}
					if b, err := os.ReadFile(p); err == nil {
						x.parse(string(b), filepath.Dir(p), defs, depth+1)
					}
				}
			}
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if d, ok := defs[v]; ok {
			v = d
		}
		if _, seen := x.vals[k]; !seen {
			x.keys = append(x.keys, k)
		}
		x.vals[k] = v
	}
}

// Get returns the value for a resource like "color4" or "background".
// Class/instance-specific entries (URxvt*color4) beat bare wildcards
// (*color4, *.color4); among equals the last definition wins.
func (x *Xresources) Get(name string) (string, bool) {
	best, bestScore := "", 0
	for _, k := range x.keys {
		i := strings.LastIndexAny(k, ".*")
		if k[i+1:] != name {
			continue
		}
		score := 1
		if strings.Trim(k[:i+1], ".*") != "" {
			score = 2
		}
		if i < 0 {
			score = 1
		}
		if score >= bestScore {
			best, bestScore = x.vals[k], score
		}
	}
	return best, bestScore > 0
}

// parseXColor accepts #rgb, #rrggbb and rgb:r/g/b.
func parseXColor(v string) (tcell.Color, bool) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "rgb:") {
		p := strings.Split(v[4:], "/")
		if len(p) != 3 {
			return 0, false
		}
		var c [3]int32
		for i, s := range p {
			n, err := strconv.ParseUint(s, 16, 16)
			if err != nil || len(s) == 0 || len(s) > 4 {
				return 0, false
			}
			c[i] = int32(n * 255 / (1<<(4*len(s)) - 1))
		}
		return tcell.NewRGBColor(c[0], c[1], c[2]), true
	}
	if strings.HasPrefix(v, "#") && (len(v) == 4 || len(v) == 7) {
		if len(v) == 4 {
			v = "#" + string([]byte{v[1], v[1], v[2], v[2], v[3], v[3]})
		}
		if _, err := strconv.ParseUint(v[1:], 16, 32); err != nil {
			return 0, false
		}
		return tcell.GetColor(v), true
	}
	return 0, false
}

// LoadXresources finds resources: $WIST_XRESOURCES if set, else
// `xrdb -query` (short timeout), else ~/.Xresources then ~/.Xdefaults.
// The returned string names the source.
func LoadXresources() (*Xresources, string, error) {
	if p := os.Getenv("WIST_XRESOURCES"); p != "" {
		return loadXrFile(p)
	}
	if path, err := exec.LookPath("xrdb"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		out, err := exec.CommandContext(ctx, path, "-query").Output()
		cancel()
		if err == nil && len(strings.TrimSpace(string(out))) > 0 {
			return ParseXresources(string(out), ""), "xrdb -query", nil
		}
	}
	home, _ := os.UserHomeDir()
	for _, n := range []string{".Xresources", ".Xdefaults"} {
		if x, src, err := loadXrFile(filepath.Join(home, n)); err == nil {
			return x, src, nil
		}
	}
	return nil, "", fmt.Errorf("no Xresources found (xrdb, ~/.Xresources, ~/.Xdefaults)")
}

func loadXrFile(p string) (*Xresources, string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}
	return ParseXresources(string(b), filepath.Dir(p)), p, nil
}

func blend(a, b tcell.Color, t float64) tcell.Color {
	ar, ag, ab := a.RGB()
	br, bg, bb := b.RGB()
	m := func(x, y int32) int32 { return int32(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return tcell.NewRGBColor(m(ar, br), m(ag, bg), m(ab, bb))
}

func hex(c tcell.Color) string { return fmt.Sprintf("#%06x", c.Hex()) }

// XresourcesTheme maps resources onto theme roles. Roles whose sources are
// missing keep the dark preset value.
func XresourcesTheme(x *Xresources) map[string]string {
	out := map[string]string{}
	for k, v := range themePresets["dark"] {
		out[k] = v
	}
	get := func(name string) (tcell.Color, bool) {
		v, ok := x.Get(name)
		if !ok {
			return 0, false
		}
		return parseXColor(v)
	}
	put := func(role, name string) {
		if c, ok := get(name); ok {
			out[role] = hex(c)
		}
	}
	put("text", "foreground")
	put("accent", "color4")
	put("user", "color5")
	put("green", "color2")
	put("red", "color1")
	put("yellow", "color3")
	put("cyan", "color6")
	put("dim", "color8")
	if c, ok := get("color9"); ok {
		out["orange"] = hex(c)
	}
	if r, ok := get("color1"); ok {
		if y, ok := get("color3"); ok {
			out["orange"] = hex(blend(r, y, 0.5))
		}
	}
	bg, okb := get("background")
	fg, okf := get("foreground")
	if okb && okf {
		if _, ok := get("color8"); !ok {
			out["dim"] = hex(blend(fg, bg, 0.45))
		}
		out["faint"] = hex(blend(fg, bg, 0.7))
		out["code_bg"] = hex(blend(bg, fg, 0.05))
		out["panel_bg"] = hex(blend(bg, fg, 0.09))
		out["sel_bg"] = hex(blend(bg, fg, 0.2))
	}
	if okb {
		if c, ok := get("color2"); ok {
			out["add_bg"] = hex(blend(bg, c, 0.2))
		}
		if c, ok := get("color1"); ok {
			out["del_bg"] = hex(blend(bg, c, 0.2))
		}
	}
	return out
}
