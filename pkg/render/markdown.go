// Package render turns text into styled screen lines: spans and lines,
// wrapping and truncation, a streaming markdown renderer, syntax
// highlighting, an ASCII mermaid renderer, and the color theme.
package render

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// Theme colors loosely follow jcode's dark palette. ApplyTheme replaces
// them; the C prefix marks a color, St a style built from one.
var (
	CText    = tcell.NewRGBColor(0xd8, 0xdb, 0xe2)
	CDim     = tcell.NewRGBColor(0x7a, 0x80, 0x8c)
	CFaint   = tcell.NewRGBColor(0x4a, 0x4f, 0x5a)
	CAccent  = tcell.NewRGBColor(0x8a, 0xb4, 0xf8)
	CUser    = tcell.NewRGBColor(0xc3, 0xa6, 0xff)
	CGreen   = tcell.NewRGBColor(0x8b, 0xd5, 0x8b)
	CRed     = tcell.NewRGBColor(0xf2, 0x8b, 0x82)
	CYellow  = tcell.NewRGBColor(0xf0, 0xc6, 0x74)
	CCyan    = tcell.NewRGBColor(0x7d, 0xd3, 0xe0)
	COrange  = tcell.NewRGBColor(0xf5, 0xa9, 0x7f)
	CCodeBg  = tcell.NewRGBColor(0x1e, 0x21, 0x27)
	CPanelBg = tcell.NewRGBColor(0x23, 0x26, 0x2d)
	CSelBg   = tcell.NewRGBColor(0x33, 0x3a, 0x4a)
	CAddBg   = tcell.NewRGBColor(0x1c, 0x33, 0x23)
	CDelBg   = tcell.NewRGBColor(0x3b, 0x1f, 0x22)
)

// Base styles derived from the theme colors, rebuilt by ApplyTheme.
var (
	StBase   = tcell.StyleDefault.Foreground(CText)
	StDim    = tcell.StyleDefault.Foreground(CDim)
	StFaint  = tcell.StyleDefault.Foreground(CFaint)
	StAccent = tcell.StyleDefault.Foreground(CAccent)
	StUser   = tcell.StyleDefault.Foreground(CUser)
	StGreen  = tcell.StyleDefault.Foreground(CGreen)
	StRed    = tcell.StyleDefault.Foreground(CRed)
	StYellow = tcell.StyleDefault.Foreground(CYellow)
	StCyan   = tcell.StyleDefault.Foreground(CCyan)
	StCode   = tcell.StyleDefault.Foreground(COrange)
)

// CodeRef locates a fenced code block in a rendered message: the index of
// its header line, its language and its source. Only closed blocks get a
// copy button.
type CodeRef struct {
	Line   int    // index of the header line
	Lang   string // fence info string
	Text   string // code without fences
	Closed bool   // the closing fence has arrived
}

// CopyLabel is the clickable copy button on a code block header.
const CopyLabel = "⧉ copy"

// Markdown renders md into lines of at most width cells. It is a line
// oriented renderer that copes with partial input, so it can be run on a
// message while it streams.
func Markdown(md string, width int, base tcell.Style) []Line {
	out, _ := MarkdownDoc(md, width, base)
	return out
}

// listCtx tracks one level of an open list so numbering continues across
// blank lines and code blocks.
type listCtx struct {
	indent  int
	num     int
	ordered bool
}

var bullets = []string{"• ", "◦ ", "▪ ", "‣ "}

// MarkdownDoc is Markdown that also reports the fenced code blocks.
func MarkdownDoc(md string, width int, base tcell.Style) ([]Line, []CodeRef) {
	var out []Line
	var codes []CodeRef
	lines := strings.Split(md, "\n")
	refs := linkRefs(lines)
	inCode := false
	fence := ""
	lang := ""
	var code []string
	codeStart := 0
	hlState := 0
	var lists []listCtx
	contIndent := -1 // indent of text continuing the last list item
	contPad := 0

	codeBar := Line{{"│ ", StFaint}}
	header := func(lang string, closed bool) Line {
		label := "╭─"
		if lang != "" {
			label += " " + lang + " "
		}
		l := Line{{label, StFaint}}
		if closed {
			pad := width - l.Width() - uniseg.StringWidth(CopyLabel)
			if pad < 1 {
				return l
			}
			l = append(l, Span{strings.Repeat("─", pad-1) + " ", StFaint}, Span{CopyLabel, StAccent})
		}
		return l
	}
	closeCode := func(closed bool) {
		src := strings.Join(code, "\n")
		if closed {
			out[codeStart] = header(lang, true)
			if strings.EqualFold(lang, "mermaid") {
				if dia := Mermaid(src); dia != nil {
					out = out[:codeStart+1]
					for _, d := range dia {
						out = append(out, cutLine(append(append(Line{}, codeBar...), d...), width))
					}
				}
			}
			out = append(out, Line{{"╰─", StFaint}})
		}
		codes = append(codes, CodeRef{Line: codeStart, Lang: lang, Text: src, Closed: closed})
		code = nil
	}

	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)

		if inCode {
			if strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, fence[:1]) == "" {
				inCode = false
				closeCode(true)
				continue
			}
			// Strip the fence's own indentation (list-nested code).
			code = append(code, raw)
			var hl []Span
			hl, hlState = highlightLine(raw, lang, hlState)
			out = append(out, Wrap(hl, width, codeBar, codeBar)...)
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			n := len(trimmed) - len(strings.TrimLeft(trimmed, trimmed[:1]))
			fence = trimmed[:n]
			inCode, hlState = true, 0
			lang = strings.TrimSpace(trimmed[n:])
			codeStart = len(out)
			out = append(out, header(lang, false))
			continue
		}

		if trimmed == "" {
			if len(out) > 0 && len(out[len(out)-1]) != 0 {
				out = append(out, Line{})
			}
			continue
		}
		if _, ok := refDef(trimmed); ok {
			continue
		}

		indent := len(raw) - len(strings.TrimLeft(raw, " "))

		// Headings, ATX and setext.
		if h := headingLevel(trimmed); h > 0 {
			lists, contIndent = nil, -1
			out = append(out, headingLines(strings.TrimRight(strings.TrimSpace(trimmed[h:]), "# "), h, width, base, refs)...)
			continue
		}
		if i+1 < len(lines) && indent < 4 && !strings.HasPrefix(trimmed, ">") && !strings.HasPrefix(trimmed, "|") {
			if _, _, isList := listItem(trimmed); !isList {
				if lvl := setextLevel(strings.TrimSpace(lines[i+1])); lvl > 0 {
					lists, contIndent = nil, -1
					out = append(out, headingLines(trimmed, lvl, width, base, refs)...)
					i++
					continue
				}
			}
		}

		if isRule(trimmed) {
			out = append(out, Line{{strings.Repeat("─", min(width, 40)), StFaint}})
			continue
		}

		if strings.HasPrefix(trimmed, "|") {
			j := i
			for j < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[j]), "|") {
				j++
			}
			out = append(out, table(lines[i:j], width, base)...)
			i = j - 1
			continue
		}

		if strings.HasPrefix(trimmed, ">") {
			text := strings.TrimSpace(strings.TrimLeft(trimmed, "> "))
			bar := Line{{"▎ ", StFaint}}
			if text == "" {
				out = append(out, bar)
				continue
			}
			out = append(out, Wrap(inlineRefs(text, base.Italic(true).Foreground(CDim), refs), width, bar, bar)...)
			continue
		}

		if card, ok := imageCard(trimmed); ok {
			out = append(out, card)
			continue
		}

		if marker, rest, ok := listItem(trimmed); ok {
			for len(lists) > 0 && lists[len(lists)-1].indent > indent {
				lists = lists[:len(lists)-1]
			}
			if len(lists) == 0 || lists[len(lists)-1].indent < indent {
				lists = append(lists, listCtx{indent: indent})
			}
			lv := len(lists) - 1
			cur := &lists[lv]
			ordered := marker != "• "
			if ordered {
				n := atoiPrefix(marker)
				if cur.ordered && cur.num > 0 && n <= cur.num {
					n = cur.num + 1 // models often write 1. 1. 1. or restart after code
				}
				cur.num, cur.ordered = n, true
				marker = itoa(n) + ". "
			} else {
				cur.ordered = false
				marker = bullets[min(lv, len(bullets)-1)]
			}
			pad := strings.Repeat("  ", min(lv, 6))
			mst := StAccent
			if lv > 0 {
				mst = StDim
			}
			first := Line{{pad + marker, mst}}
			next := Line{{pad + strings.Repeat(" ", uniseg.StringWidth(marker)), base}}
			if strings.HasPrefix(rest, "[ ] ") {
				first = Line{{pad + "☐ ", StDim}}
				rest = rest[4:]
			} else if strings.HasPrefix(rest, "[x] ") || strings.HasPrefix(rest, "[X] ") {
				first = Line{{pad + "☑ ", StGreen}}
				rest = rest[4:]
			}
			contIndent, contPad = indent+1, next.Width()
			out = append(out, Wrap(inlineRefs(rest, base, refs), width, first, next)...)
			continue
		}

		// Paragraph text. Indented text after a list item continues it.
		if contIndent >= 0 && indent >= contIndent {
			p := Line{{strings.Repeat(" ", contPad), base}}
			out = append(out, Wrap(inlineRefs(trimmed, base, refs), width, p, p)...)
			continue
		}
		if indent == 0 {
			lists, contIndent = nil, -1
		}
		// Model output means its single newlines, so each source line
		// starts a new screen line.
		out = append(out, Wrap(inlineRefs(trimmed, base, refs), width, nil, nil)...)
	}
	if inCode {
		closeCode(false)
	}
	for len(out) > 0 && len(out[len(out)-1]) == 0 {
		out = out[:len(out)-1]
	}
	return out, codes
}

func headingLines(text string, level, width int, base tcell.Style, refs map[string]string) []Line {
	st := base.Bold(true).Foreground(CAccent)
	if level > 2 {
		st = base.Bold(true)
	}
	if level == 1 {
		st = st.Underline(true)
	}
	return Wrap(inlineRefs(text, st, refs), width, nil, nil)
}

func setextLevel(s string) int {
	if len(s) < 2 {
		return 0
	}
	switch {
	case strings.Trim(s, "=") == "":
		return 1
	case strings.Trim(s, "-") == "":
		return 2
	}
	return 0
}

// cutLine truncates a rendered line to w cells.
func cutLine(l Line, w int) Line {
	var out Line
	used := 0
	for _, s := range l {
		sw := uniseg.StringWidth(s.Text)
		if used+sw <= w {
			out = append(out, s)
			used += sw
			continue
		}
		if t := Truncate(s.Text, w-used); t != "" {
			out = append(out, Span{t, s.St})
		}
		break
	}
	return out
}

func atoiPrefix(s string) int {
	n := 0
	for i := 0; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }

// refDef parses a reference link definition `[id]: url "title"`.
func refDef(s string) ([2]string, bool) {
	if !strings.HasPrefix(s, "[") || strings.HasPrefix(s, "[^") {
		return [2]string{}, false
	}
	end := strings.Index(s, "]:")
	if end < 2 {
		return [2]string{}, false
	}
	url := strings.TrimSpace(s[end+2:])
	if url == "" {
		return [2]string{}, false
	}
	if f := strings.Fields(url); len(f) > 0 {
		url = strings.Trim(f[0], "<>")
	}
	return [2]string{strings.ToLower(s[1:end]), url}, true
}

func linkRefs(lines []string) map[string]string {
	var refs map[string]string
	for _, l := range lines {
		if d, ok := refDef(strings.TrimSpace(l)); ok {
			if refs == nil {
				refs = map[string]string{}
			}
			refs[d[0]] = d[1]
		}
	}
	return refs
}

// imageCard renders the image placeholder produced for image content
// blocks as a clickable card.
func imageCard(s string) (Line, bool) {
	if !strings.HasPrefix(s, ImageMarker) {
		return nil, false
	}
	return Line{{" " + s + " ", tcell.StyleDefault.Foreground(CText).Background(CPanelBg)}}, true
}

func headingLevel(s string) int {
	n := 0
	for n < len(s) && n < 6 && s[n] == '#' {
		n++
	}
	if n > 0 && n < len(s) && s[n] == ' ' {
		return n
	}
	return 0
}

func isRule(s string) bool {
	if len(s) < 3 {
		return false
	}
	c := s[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != c && s[i] != ' ' {
			return false
		}
	}
	return true
}

func listItem(s string) (marker, rest string, ok bool) {
	if len(s) >= 2 && (s[0] == '-' || s[0] == '*' || s[0] == '+') && s[1] == ' ' {
		return "• ", s[2:], true
	}
	i := 0
	for i < len(s) && i < 4 && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(s) && (s[i] == '.' || s[i] == ')') && s[i+1] == ' ' {
		return s[:i+1] + " ", s[i+2:], true
	}
	return "", "", false
}

// inline styles `code`, **bold**, *italic*, ~~strike~~ and [links](url).
func inline(s string, base tcell.Style) []Span {
	var out []Span
	var b strings.Builder
	st := base
	bold, italic, strike := false, false, false
	emit := func() {
		if b.Len() > 0 {
			out = append(out, Span{b.String(), st})
			b.Reset()
		}
	}
	restyle := func() {
		st = base.Bold(bold).Italic(italic).StrikeThrough(strike)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && strings.IndexByte("\\`*_[]()#+-.!~|", s[i+1]) >= 0:
			b.WriteByte(s[i+1])
			i++
		case c == '`':
			end := strings.IndexByte(s[i+1:], '`')
			if end < 0 {
				b.WriteByte(c)
				continue
			}
			emit()
			out = append(out, Span{s[i+1 : i+1+end], StCode})
			i += end + 1
		case c == '*' && i+1 < len(s) && s[i+1] == '*', c == '_' && i+1 < len(s) && s[i+1] == '_':
			emit()
			bold = !bold
			restyle()
			i++
		case c == '~' && i+1 < len(s) && s[i+1] == '~':
			emit()
			strike = !strike
			restyle()
			i++
		case (c == '*' || c == '_') && emphasisEdge(s, i):
			emit()
			italic = !italic
			restyle()
		case c == '[':
			close := strings.Index(s[i:], "](")
			if close < 0 {
				b.WriteByte(c)
				continue
			}
			end := strings.IndexByte(s[i+close:], ')')
			if end < 0 {
				b.WriteByte(c)
				continue
			}
			emit()
			text := s[i+1 : i+close]
			url := s[i+close+2 : i+close+end]
			out = append(out, Span{text, linkStyle(base.Foreground(CAccent), url)})
			if url != text {
				out = append(out, Span{" (" + url + ")", StFaint})
			}
			i += close + end
		default:
			b.WriteByte(c)
		}
	}
	emit()
	return Linkify(out)
}

// emphasisEdge reports whether a single * or _ at i opens or closes
// emphasis rather than sitting inside a word or standing alone.
func emphasisEdge(s string, i int) bool {
	prevSpace := i == 0 || s[i-1] == ' '
	nextSpace := i+1 >= len(s) || s[i+1] == ' '
	if prevSpace && nextSpace {
		return false
	}
	if s[i] == '_' && !prevSpace && !nextSpace {
		return false // snake_case
	}
	return prevSpace || nextSpace || i+1 >= len(s) || !isWord(rune(s[i+1])) || !isWord(rune(s[i-1]))
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func table(rows []string, width int, base tcell.Style) []Line {
	var cells [][]string
	for _, r := range rows {
		r = strings.TrimSpace(r)
		r = strings.TrimPrefix(r, "|")
		r = strings.TrimSuffix(r, "|")
		parts := strings.Split(r, "|")
		for k := range parts {
			parts[k] = strings.TrimSpace(parts[k])
		}
		cells = append(cells, parts)
	}
	cols := 0
	for _, r := range cells {
		cols = max(cols, len(r))
	}
	widths := make([]int, cols)
	isSep := func(r []string) bool {
		for _, c := range r {
			if strings.Trim(c, "-: ") != "" {
				return false
			}
		}
		return true
	}
	for _, r := range cells {
		if isSep(r) {
			continue
		}
		for k, c := range r {
			widths[k] = max(widths[k], uniseg.StringWidth(plain(c)))
		}
	}
	total := cols*3 + 1
	for _, w := range widths {
		total += w
	}
	if total > width {
		// Too wide to align: fall back to wrapped rows.
		var out []Line
		for _, r := range cells {
			if isSep(r) {
				continue
			}
			out = append(out, Wrap(inline(strings.Join(r, " │ "), base), width, nil, nil)...)
		}
		return out
	}
	var out []Line
	for ri, r := range cells {
		if isSep(r) {
			var b strings.Builder
			b.WriteString("├")
			for k, w := range widths {
				b.WriteString(strings.Repeat("─", w+2))
				if k < cols-1 {
					b.WriteString("┼")
				}
			}
			b.WriteString("┤")
			out = append(out, Line{{b.String(), StFaint}})
			continue
		}
		l := Line{{"│", StFaint}}
		for k := 0; k < cols; k++ {
			c := ""
			if k < len(r) {
				c = r[k]
			}
			st := base
			if ri == 0 {
				st = base.Bold(true)
			}
			l = append(l, Span{" ", base})
			l = append(l, inline(c, st)...)
			l = append(l, Span{strings.Repeat(" ", widths[k]-uniseg.StringWidth(plain(c))+1), base}, Span{"│", StFaint})
		}
		out = append(out, l)
	}
	return out
}

// plain is the visible text of inline markdown.
func plain(s string) string {
	var b strings.Builder
	for _, sp := range inline(s, tcell.StyleDefault) {
		b.WriteString(sp.Text)
	}
	return b.String()
}

var (
	htmlTagRE = regexp.MustCompile(`</?[a-zA-Z][a-zA-Z0-9-]*(\s[^<>]*)?/?>|<!--.*?-->`)
	refLinkRE = regexp.MustCompile(`\[([^\[\]]+)\]\[([^\[\]]*)\]`)
	brRE      = regexp.MustCompile(`(?i)<br\s*/?>`)
)

// inlineRefs is inline with reference links resolved and inline HTML tags
// stripped (outside code spans).
func inlineRefs(s string, base tcell.Style, refs map[string]string) []Span {
	if strings.IndexByte(s, '<') >= 0 || strings.IndexByte(s, '[') >= 0 {
		s = mapOutsideCode(s, func(t string) string {
			if strings.IndexByte(t, '<') >= 0 {
				t = brRE.ReplaceAllString(t, " ")
				t = htmlTagRE.ReplaceAllStringFunc(t, func(tag string) string {
					// Keep autolinks like <https://x> visible.
					if strings.Contains(tag, "://") && !strings.Contains(tag, " ") {
						return tag[1 : len(tag)-1]
					}
					return ""
				})
			}
			if len(refs) > 0 && strings.IndexByte(t, '[') >= 0 {
				t = refLinkRE.ReplaceAllStringFunc(t, func(m string) string {
					sm := refLinkRE.FindStringSubmatch(m)
					id := sm[2]
					if id == "" {
						id = sm[1]
					}
					if u, ok := refs[strings.ToLower(id)]; ok {
						return "[" + sm[1] + "](" + u + ")"
					}
					return m
				})
				// Shortcut form [text] with a definition.
				for id, u := range refs {
					t = strings.ReplaceAll(t, "["+id+"]", "["+id+"]("+u+")")
				}
				t = strings.ReplaceAll(t, ")(", ") (")
			}
			return t
		})
	}
	return inline(s, base)
}

// mapOutsideCode applies f to the parts of s that are not `code spans`.
func mapOutsideCode(s string, f func(string) string) string {
	if strings.IndexByte(s, '`') < 0 {
		return f(s)
	}
	var b strings.Builder
	for s != "" {
		i := strings.IndexByte(s, '`')
		if i < 0 {
			b.WriteString(f(s))
			break
		}
		j := strings.IndexByte(s[i+1:], '`')
		if j < 0 {
			b.WriteString(f(s))
			break
		}
		b.WriteString(f(s[:i]))
		b.WriteString(s[i : i+j+2])
		s = s[i+j+2:]
	}
	return b.String()
}

// ImageMarker is the token placeholders start with.
const ImageMarker = "🖼 #"
