package main

import (
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// A small mermaid renderer: flowchart/graph as layered box-and-arrow
// diagrams and sequenceDiagram as lanes. Anything else returns nil and the
// caller shows the source.

const (
	mmEdge = iota + 1
	mmBox
	mmText
	mmLabel
	mmArrow
)

var mmStyles [mmArrow + 1]tcell.Style

// canvas is a grid of runes with line-junction bits and a style class.
type canvas struct {
	w, h  int
	runes [][]rune
	bits  [][]uint8
	cls   [][]uint8
}

const (
	bU = 1 << iota
	bD
	bL
	bR
)

func newCanvas(w, h int) *canvas {
	c := &canvas{w: w, h: h}
	c.runes = make([][]rune, h)
	c.bits = make([][]uint8, h)
	c.cls = make([][]uint8, h)
	for y := range h {
		c.runes[y] = make([]rune, w)
		c.bits[y] = make([]uint8, w)
		c.cls[y] = make([]uint8, w)
	}
	return c
}

func (c *canvas) ok(x, y int) bool { return x >= 0 && y >= 0 && x < c.w && y < c.h }

func (c *canvas) put(x, y int, r rune, cls uint8) {
	if c.ok(x, y) {
		c.runes[y][x], c.cls[y][x] = r, cls
	}
}

func (c *canvas) text(x, y int, s string, cls uint8) {
	for _, r := range s {
		c.put(x, y, r, cls)
		x += max(1, uniseg.StringWidth(string(r)))
	}
}

func (c *canvas) bit(x, y int, b uint8) {
	if c.ok(x, y) {
		c.bits[y][x] |= b
	}
}

// hline and vline add line bits so crossings and corners join up.
func (c *canvas) hline(x1, x2, y int) {
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	for x := x1; x <= x2; x++ {
		if x > x1 {
			c.bit(x, y, bL)
		}
		if x < x2 {
			c.bit(x, y, bR)
		}
	}
	if x1 == x2 {
		c.bit(x1, y, bL|bR)
	}
}

func (c *canvas) vline(x, y1, y2 int) {
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	for y := y1; y <= y2; y++ {
		if y > y1 {
			c.bit(x, y, bU)
		}
		if y < y2 {
			c.bit(x, y, bD)
		}
	}
	if y1 == y2 {
		c.bit(x, y1, bU|bD)
	}
}

var junction = map[uint8]rune{
	bU: '│', bD: '│', bU | bD: '│',
	bL: '─', bR: '─', bL | bR: '─',
	bD | bR: '╭', bD | bL: '╮', bU | bR: '╰', bU | bL: '╯',
	bU | bD | bR: '├', bU | bD | bL: '┤', bL | bR | bD: '┬', bL | bR | bU: '┴',
	bU | bD | bL | bR: '┼',
}

func (c *canvas) lines() []line {
	var out []line
	for y := range c.h {
		var l line
		var b strings.Builder
		cur := uint8(255)
		flush := func() {
			if b.Len() > 0 {
				l = append(l, span{b.String(), mmStyles[cur]})
				b.Reset()
			}
		}
		for x := range c.w {
			r, cl := c.runes[y][x], c.cls[y][x]
			if r == 0 {
				if bits := c.bits[y][x]; bits != 0 {
					r, cl = junction[bits], mmEdge
				} else {
					r, cl = ' ', 0
				}
			}
			if r == -1 { // second cell of a wide rune
				continue
			}
			if cl != cur {
				flush()
				cur = cl
			}
			b.WriteRune(r)
		}
		flush()
		out = append(out, trimRight(l))
	}
	for len(out) > 0 && len(out[len(out)-1]) == 0 {
		out = out[:len(out)-1]
	}
	for len(out) > 0 && len(out[0]) == 0 {
		out = out[1:]
	}
	return out
}

// mermaid renders src, or returns nil when the diagram type is unknown or
// the source does not parse into anything.
func mermaid(src string) []line {
	lines := strings.Split(clean(src), "\n")
	first := ""
	for len(lines) > 0 {
		first = strings.TrimSpace(lines[0])
		lines = lines[1:]
		if first != "" && !strings.HasPrefix(first, "%%") {
			break
		}
	}
	f := strings.Fields(first)
	if len(f) == 0 {
		return nil
	}
	switch f[0] {
	case "graph", "flowchart":
		dir := "TD"
		if len(f) > 1 {
			dir = strings.ToUpper(strings.TrimSuffix(f[1], ";"))
		}
		return flowchart(lines, dir)
	case "sequenceDiagram":
		return sequence(lines)
	}
	return nil
}

type fnode struct {
	id, label string
	shape     byte // '[', '(', '{'
	layer     int
	order     int
	cross     int // position along the cross axis
	x, y, w   int
}

type fedge struct {
	from, to int
	label    string
	dashed   bool
	arrow    bool
}

type graphParse struct {
	nodes []*fnode
	index map[string]int
	edges []fedge
}

func (g *graphParse) node(id, label string, shape byte) int {
	if i, ok := g.index[id]; ok {
		if label != "" {
			g.nodes[i].label, g.nodes[i].shape = label, shape
		}
		return i
	}
	if label == "" {
		label, shape = id, '['
	}
	g.index[id] = len(g.nodes)
	g.nodes = append(g.nodes, &fnode{id: id, label: label, shape: shape})
	return len(g.nodes) - 1
}

// parseNode reads `id`, `id[label]`, `id(label)`, `id{label}` and friends.
func (g *graphParse) parseNode(s string) (int, string, bool) {
	s = strings.TrimLeft(s, " ")
	j := 0
	for j < len(s) && (isWordByte(s[j]) || s[j] == '-' && j+1 < len(s) && isWordByte(s[j+1]) || s[j] >= 0x80) {
		j++
	}
	if j == 0 {
		return 0, s, false
	}
	id := s[:j]
	s = s[j:]
	label, shape := "", byte(0)
	if s != "" {
		pairs := []struct{ open, close string }{
			{"((", "))"}, {"([", "])"}, {"[(", ")]"}, {"[[", "]]"}, {"{{", "}}"}, {"[/", "/]"}, {"[\\", "\\]"},
			{"[", "]"}, {"(", ")"}, {"{", "}"}, {">", "]"},
		}
		for _, p := range pairs {
			if strings.HasPrefix(s, p.open) {
				end := strings.Index(s[len(p.open):], p.close)
				if end < 0 {
					break
				}
				label = strings.Trim(strings.TrimSpace(s[len(p.open):len(p.open)+end]), `"`)
				shape = p.open[0]
				if p.open == "((" || p.open == "([" {
					shape = '('
				}
				s = s[len(p.open)+end+len(p.close):]
				break
			}
		}
	}
	label = strings.ReplaceAll(label, "<br>", " ")
	label = strings.ReplaceAll(label, "<br/>", " ")
	return g.node(id, label, shape), s, true
}

// parseEdge reads an arrow and an optional |label| or inline "-- text -->".
func parseEdge(s string) (e fedge, rest string, ok bool) {
	s = strings.TrimLeft(s, " ")
	j := 0
	for j < len(s) && strings.IndexByte("-=.<>ox", s[j]) >= 0 {
		if (s[j] == 'o' || s[j] == 'x') && j > 0 && j+1 < len(s) && isWordByte(s[j+1]) {
			break
		}
		j++
	}
	op := s[:j]
	if len(op) < 2 || strings.Trim(op, "-=.") == op && !strings.ContainsAny(op, "-=") {
		return e, s, false
	}
	e.dashed = strings.Contains(op, ".")
	e.arrow = strings.ContainsAny(op, ">ox")
	s = s[j:]
	if !e.arrow && (op == "--" || op == "==" || op == "-.") {
		// -- text --> form
		for _, closer := range []string{"-->", "==>", ".->", "---", "==="} {
			if k := strings.Index(s, closer); k >= 0 {
				e.label = strings.TrimSpace(s[:k])
				e.arrow = strings.HasSuffix(closer, ">")
				s = s[k+len(closer):]
				break
			}
		}
	}
	s = strings.TrimLeft(s, " ")
	if strings.HasPrefix(s, "|") {
		if k := strings.IndexByte(s[1:], '|'); k >= 0 {
			e.label = strings.Trim(strings.TrimSpace(s[1:1+k]), `"`)
			s = s[k+2:]
		}
	}
	return e, s, true
}

func parseFlow(lines []string) *graphParse {
	g := &graphParse{index: map[string]int{}}
	for _, raw := range lines {
		for _, stmt := range strings.Split(raw, ";") {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" || strings.HasPrefix(stmt, "%%") {
				continue
			}
			if f := strings.Fields(stmt); len(f) > 0 {
				switch f[0] {
				case "subgraph", "end", "classDef", "class", "style", "linkStyle", "click", "direction":
					continue
				}
			}
			froms, rest, ok := g.parseGroup(stmt)
			if !ok {
				continue
			}
			for {
				e, r, ok := parseEdge(rest)
				if !ok {
					break
				}
				tos, r2, ok := g.parseGroup(r)
				if !ok {
					break
				}
				for _, a := range froms {
					for _, b := range tos {
						ee := e
						ee.from, ee.to = a, b
						g.edges = append(g.edges, ee)
					}
				}
				froms, rest = tos, r2
			}
		}
	}
	return g
}

// parseGroup reads `a & b & c`.
func (g *graphParse) parseGroup(s string) ([]int, string, bool) {
	var ids []int
	for {
		i, rest, ok := g.parseNode(s)
		if !ok {
			return ids, s, len(ids) > 0
		}
		// strip :::class
		if strings.HasPrefix(rest, ":::") {
			k := 3
			for k < len(rest) && isWordByte(rest[k]) {
				k++
			}
			rest = rest[k:]
		}
		ids = append(ids, i)
		t := strings.TrimLeft(rest, " ")
		if !strings.HasPrefix(t, "&") {
			return ids, rest, true
		}
		s = t[1:]
	}
}

const mmMaxLabel = 28

func flowchart(lines []string, dir string) []line {
	g := parseFlow(lines)
	n := len(g.nodes)
	if n == 0 || n > 200 {
		return nil
	}
	// Layers: longest path over forward (DFS tree/cross) edges.
	adj := make([][]int, n)
	for k, e := range g.edges {
		adj[e.from] = append(adj[e.from], k)
	}
	back := make([]bool, len(g.edges))
	color := make([]int, n)
	var dfs func(int)
	dfs = func(v int) {
		color[v] = 1
		for _, k := range adj[v] {
			w := g.edges[k].to
			switch color[w] {
			case 0:
				dfs(w)
			case 1:
				back[k] = true
			}
		}
		color[v] = 2
	}
	for v := range n {
		if color[v] == 0 {
			dfs(v)
		}
	}
	for range n {
		changed := false
		for k, e := range g.edges {
			if back[k] || e.from == e.to {
				continue
			}
			if l := g.nodes[e.from].layer + 1; l > g.nodes[e.to].layer {
				g.nodes[e.to].layer, changed = l, true
			}
		}
		if !changed {
			break
		}
	}
	nl := 0
	for _, nd := range g.nodes {
		nl = max(nl, nd.layer+1)
		nd.label = truncate(nd.label, mmMaxLabel)
		nd.w = uniseg.StringWidth(nd.label) + 4
	}
	layers := make([][]*fnode, nl)
	for _, nd := range g.nodes {
		layers[nd.layer] = append(layers[nd.layer], nd)
	}
	// One barycenter pass to reduce crossings.
	for li := 1; li < nl; li++ {
		for i, nd := range layers[li-1] {
			nd.order = i
		}
		bary := map[*fnode]float64{}
		for _, nd := range layers[li] {
			sum, cnt := 0.0, 0
			for k, e := range g.edges {
				if !back[k] && g.nodes[e.to] == nd && g.nodes[e.from].layer == li-1 {
					sum += float64(g.nodes[e.from].order)
					cnt++
				}
			}
			if cnt > 0 {
				bary[nd] = sum / float64(cnt)
			} else {
				bary[nd] = 1e9
			}
		}
		sort.SliceStable(layers[li], func(a, b int) bool { return bary[layers[li][a]] < bary[layers[li][b]] })
	}

	lr := dir == "LR" || dir == "RL"
	maxLabel := 0
	hasBack := false
	for k, e := range g.edges {
		maxLabel = max(maxLabel, uniseg.StringWidth(truncate(e.label, 16)))
		hasBack = hasBack || back[k]
	}
	// Main axis: positions of layers. Cross axis: positions within a layer.
	gap := 3
	if maxLabel > 0 {
		gap = 5
	}
	crossGap := 3
	if lr {
		gap = max(6, maxLabel+5)
		crossGap = 1
	}
	mainSize := func(nd *fnode) int {
		if lr {
			return nd.w
		}
		return 3
	}
	crossSize := func(nd *fnode) int {
		if lr {
			return 3
		}
		return nd.w
	}
	pad := 0
	if hasBack {
		pad = 2
	}
	mainPos := make([]int, nl+1)
	mainPos[0] = pad
	layerSize := make([]int, nl)
	for li, l := range layers {
		for _, nd := range l {
			layerSize[li] = max(layerSize[li], mainSize(nd))
		}
		mainPos[li+1] = mainPos[li] + layerSize[li] + gap
	}
	crossExt := 0
	for _, l := range layers {
		t := 0
		for _, nd := range l {
			t += crossSize(nd) + crossGap
		}
		crossExt = max(crossExt, t-crossGap)
	}
	for li, l := range layers {
		t := 0
		for _, nd := range l {
			t += crossSize(nd) + crossGap
		}
		c := (crossExt - (t - crossGap)) / 2
		for _, nd := range l {
			nd.cross = c
			c += crossSize(nd) + crossGap
			if lr {
				nd.x, nd.y = mainPos[li], nd.cross
			} else {
				nd.x, nd.y = nd.cross, mainPos[li]
			}
		}
	}
	nBack := 0
	for _, b := range back {
		if b {
			nBack++
		}
	}
	mainExt := mainPos[nl] - gap
	if hasBack {
		mainExt += gap
	}
	crossTotal := crossExt + 2*nBack + 2
	var cv *canvas
	if lr {
		cv = newCanvas(mainExt+1, crossTotal+1)
	} else {
		cv = newCanvas(crossTotal+maxLabel+2, mainExt+1)
	}
	// P maps (main, cross) to canvas (x, y).
	P := func(m, c int) (int, int) {
		if lr {
			return m, c
		}
		return c, m
	}
	mline := func(m1, m2, c int) {
		x1, y1 := P(m1, c)
		x2, y2 := P(m2, c)
		if lr {
			cv.hline(x1, x2, y1)
		} else {
			cv.vline(x1, y1, y2)
		}
	}
	cline := func(m, c1, c2 int) {
		if c1 == c2 {
			return
		}
		x1, y1 := P(m, c1)
		x2, y2 := P(m, c2)
		if lr {
			cv.vline(x1, y1, y2)
		} else {
			cv.hline(x1, x2, y1)
		}
	}
	center := func(nd *fnode) int { return nd.cross + crossSize(nd)/2 }
	nodeMainEnd := func(nd *fnode) int { return mainPos[nd.layer] + mainSize(nd) }
	arrowRune := '▼'
	if lr {
		arrowRune = '▶'
	}
	bi := 0
	for k, e := range g.edges {
		a, b := g.nodes[e.from], g.nodes[e.to]
		if a == b {
			continue
		}
		sc, dc := center(a), center(b)
		m0 := nodeMainEnd(a)
		gm := mainPos[a.layer] + layerSize[a.layer] + gap/2
		mEnd := mainPos[b.layer] - 1
		if back[k] {
			side := crossExt + 2 + 2*bi
			bi++
			top := mainPos[b.layer] - 2
			mline(m0, gm, sc)
			cline(gm, sc, side)
			mline(gm, top, side)
			cline(top, side, dc)
			mline(top, mEnd, dc)
		} else {
			mline(m0, gm, sc)
			cline(gm, sc, dc)
			mline(gm, mEnd, dc)
		}
		if e.arrow {
			x, y := P(mEnd, dc)
			cv.put(x, y, arrowRune, mmArrow)
		}
		if e.label != "" {
			lab := truncate(e.label, 16)
			if lr {
				x, y := P(m0+1, sc)
				cv.text(x, y-1, lab, mmLabel)
			} else {
				x, y := P(mEnd-1, dc)
				cv.text(x+2, y, lab, mmLabel)
			}
		}
	}
	for _, nd := range g.nodes {
		drawBox(cv, nd.x, nd.y, nd.w, nd.label, nd.shape)
	}
	out := cv.lines()
	if dir == "BT" || dir == "RL" {
		// Close enough: keep top-down / left-right; note the direction.
		out = append(out, line{{"(" + dir + " drawn as " + map[string]string{"BT": "TD", "RL": "LR"}[dir] + ")", stFaint}})
	}
	return out
}

func drawBox(cv *canvas, x, y, w int, label string, shape byte) {
	tl, tr, bl, br := '┌', '┐', '└', '┘'
	switch shape {
	case '(':
		tl, tr, bl, br = '╭', '╮', '╰', '╯'
	case '{':
		tl, tr, bl, br = '◆', '◆', '◆', '◆'
	}
	cv.put(x, y, tl, mmBox)
	cv.put(x+w-1, y, tr, mmBox)
	cv.put(x, y+2, bl, mmBox)
	cv.put(x+w-1, y+2, br, mmBox)
	for i := x + 1; i < x+w-1; i++ {
		cv.put(i, y, '─', mmBox)
		cv.put(i, y+2, '─', mmBox)
		cv.put(i, y+1, ' ', mmText)
	}
	cv.put(x, y+1, '│', mmBox)
	cv.put(x+w-1, y+1, '│', mmBox)
	cv.text(x+2, y+1, label, mmText)
}

// --- sequence diagrams ---------------------------------------------------

type seqStep struct {
	kind     byte // 'm' message, 'n' note, 'g' group label
	from, to int
	text     string
	dashed   bool
}

func sequence(lines []string) []line {
	var names, labels []string
	idx := map[string]int{}
	part := func(id, label string) int {
		if i, ok := idx[id]; ok {
			if label != "" {
				labels[i] = label
			}
			return i
		}
		if label == "" {
			label = id
		}
		idx[id] = len(names)
		names = append(names, id)
		labels = append(labels, label)
		return len(names) - 1
	}
	var steps []seqStep
	arrows := []string{"-->>", "->>", "-->", "--x", "-x", "--)", "-)", "->"}
	for _, raw := range lines {
		s := strings.TrimSpace(raw)
		if s == "" || strings.HasPrefix(s, "%%") {
			continue
		}
		f := strings.Fields(s)
		switch f[0] {
		case "participant", "actor":
			rest := strings.TrimSpace(s[len(f[0]):])
			id, label, _ := strings.Cut(rest, " as ")
			part(strings.TrimSpace(id), strings.TrimSpace(label))
			continue
		case "autonumber", "activate", "deactivate", "title":
			continue
		case "loop", "alt", "else", "opt", "par", "and", "critical", "break", "rect":
			steps = append(steps, seqStep{kind: 'g', text: s})
			continue
		case "end":
			steps = append(steps, seqStep{kind: 'g', text: "end"})
			continue
		case "Note", "note":
			// Note over A,B: text / Note right of A: text
			head, text, _ := strings.Cut(s, ":")
			hf := strings.Fields(head)
			who := hf[len(hf)-1]
			a, _, _ := strings.Cut(who, ",")
			steps = append(steps, seqStep{kind: 'n', from: part(strings.TrimSpace(a), ""), text: strings.TrimSpace(text)})
			continue
		}
		head, text, _ := strings.Cut(s, ":")
		for _, ar := range arrows {
			if k := strings.Index(head, ar); k > 0 {
				a := strings.TrimSpace(head[:k])
				b := strings.TrimLeft(strings.TrimSpace(head[k+len(ar):]), "+-")
				a = strings.TrimRight(a, "+-")
				steps = append(steps, seqStep{kind: 'm', from: part(a, ""), to: part(b, ""), text: strings.TrimSpace(text), dashed: strings.HasPrefix(ar, "--")})
				break
			}
		}
	}
	if len(names) == 0 || len(names) > 20 {
		return nil
	}
	maxW := 0
	for i := range labels {
		labels[i] = truncate(labels[i], 20)
		maxW = max(maxW, uniseg.StringWidth(labels[i])+4)
	}
	maxMsg := 0
	for i := range steps {
		steps[i].text = truncate(steps[i].text, 36)
		if steps[i].kind == 'm' {
			maxMsg = max(maxMsg, uniseg.StringWidth(steps[i].text)+4)
		}
	}
	gap := min(max(maxW+2, maxMsg), 44)
	cols := make([]int, len(names))
	for i := range cols {
		cols[i] = maxW/2 + i*gap
	}
	width := cols[len(cols)-1] + maxW/2 + 24
	height := 3 + 2*len(steps) + 1
	cv := newCanvas(width, height)
	for i, l := range labels {
		w := uniseg.StringWidth(l) + 4
		drawBox(cv, cols[i]-w/2, 0, w, l, '[')
		cv.vline(cols[i], 3, height-1)
	}
	y := 3
	for _, st := range steps {
		switch st.kind {
		case 'g':
			cv.text(1, y+1, "┄ "+st.text, mmLabel)
		case 'n':
			cv.text(cols[st.from]+2, y+1, "▏"+st.text, mmLabel)
		case 'm':
			a, b := cols[st.from], cols[st.to]
			if a == b {
				cv.put(a+1, y, '─', mmEdge)
				cv.put(a+2, y, '─', mmEdge)
				cv.put(a+3, y, '╮', mmEdge)
				cv.text(a+5, y, st.text, mmText)
				cv.put(a+1, y+1, '◀', mmArrow)
				cv.put(a+2, y+1, '─', mmEdge)
				cv.put(a+3, y+1, '╯', mmEdge)
				break
			}
			lo, hi := min(a, b), max(a, b)
			for x := lo + 1; x < hi; x++ {
				r := '─'
				if st.dashed && x%2 == 0 {
					r = ' '
				}
				cv.put(x, y+1, r, mmEdge)
			}
			if b > a {
				cv.put(b-1, y+1, '▶', mmArrow)
			} else {
				cv.put(b+1, y+1, '◀', mmArrow)
			}
			tw := uniseg.StringWidth(st.text)
			cv.text(lo+max(1, (hi-lo-tw)/2), y, st.text, mmText)
		}
		y += 2
	}
	return cv.lines()
}
