package main

import (
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// Theme colors loosely follow jcode's dark palette.
var (
	cText    = tcell.NewRGBColor(0xd8, 0xdb, 0xe2)
	cDim     = tcell.NewRGBColor(0x7a, 0x80, 0x8c)
	cFaint   = tcell.NewRGBColor(0x4a, 0x4f, 0x5a)
	cAccent  = tcell.NewRGBColor(0x8a, 0xb4, 0xf8)
	cUser    = tcell.NewRGBColor(0xc3, 0xa6, 0xff)
	cGreen   = tcell.NewRGBColor(0x8b, 0xd5, 0x8b)
	cRed     = tcell.NewRGBColor(0xf2, 0x8b, 0x82)
	cYellow  = tcell.NewRGBColor(0xf0, 0xc6, 0x74)
	cCyan    = tcell.NewRGBColor(0x7d, 0xd3, 0xe0)
	cOrange  = tcell.NewRGBColor(0xf5, 0xa9, 0x7f)
	cCodeBg  = tcell.NewRGBColor(0x1e, 0x21, 0x27)
	cPanelBg = tcell.NewRGBColor(0x23, 0x26, 0x2d)
	cSelBg   = tcell.NewRGBColor(0x33, 0x3a, 0x4a)
	cAddBg   = tcell.NewRGBColor(0x1c, 0x33, 0x23)
	cDelBg   = tcell.NewRGBColor(0x3b, 0x1f, 0x22)
)

var (
	stBase   = tcell.StyleDefault.Foreground(cText)
	stDim    = tcell.StyleDefault.Foreground(cDim)
	stFaint  = tcell.StyleDefault.Foreground(cFaint)
	stAccent = tcell.StyleDefault.Foreground(cAccent)
	stUser   = tcell.StyleDefault.Foreground(cUser)
	stGreen  = tcell.StyleDefault.Foreground(cGreen)
	stRed    = tcell.StyleDefault.Foreground(cRed)
	stYellow = tcell.StyleDefault.Foreground(cYellow)
	stCyan   = tcell.StyleDefault.Foreground(cCyan)
	stCode   = tcell.StyleDefault.Foreground(cOrange)
)

// markdown renders md into lines of at most width cells. It is a line
// oriented renderer that copes with partial input, so it can be run on a
// message while it streams.
func markdown(md string, width int, base tcell.Style) []line {
	var out []line
	lines := strings.Split(md, "\n")
	inCode := false
	lang := ""
	var para []string

	flushPara := func() {
		if len(para) == 0 {
			return
		}
		// Model output means its single newlines, so each source line
		// starts a new screen line.
		for _, text := range para {
			out = append(out, wrap(inline(text, base), width, nil, nil)...)
		}
		para = para[:0]
	}

	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)

		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			flushPara()
			if !inCode {
				inCode = true
				lang = strings.TrimSpace(strings.TrimLeft(trimmed, "`~"))
				label := " " + lang
				if lang == "" {
					label = ""
				}
				out = append(out, line{{"╭─" + label, stFaint}})
			} else {
				inCode = false
				out = append(out, line{{"╰─", stFaint}})
			}
			continue
		}
		if inCode {
			hl := highlight(raw, lang)
			out = append(out, wrap(hl, width, line{{"│ ", stFaint}}, line{{"│ ", stFaint}})...)
			continue
		}

		if trimmed == "" {
			flushPara()
			if len(out) > 0 && len(out[len(out)-1]) != 0 {
				out = append(out, line{})
			}
			continue
		}

		// Headings.
		if h := headingLevel(trimmed); h > 0 {
			flushPara()
			text := strings.TrimSpace(trimmed[h:])
			st := base.Bold(true).Foreground(cAccent)
			if h > 2 {
				st = base.Bold(true)
			}
			out = append(out, wrap(inline(text, st), width, nil, nil)...)
			continue
		}

		// Horizontal rule.
		if isRule(trimmed) {
			flushPara()
			out = append(out, line{{strings.Repeat("─", min(width, 40)), stFaint}})
			continue
		}

		// Table: render rows with aligned columns when the block is complete.
		if strings.HasPrefix(trimmed, "|") {
			flushPara()
			j := i
			for j < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[j]), "|") {
				j++
			}
			out = append(out, table(lines[i:j], width, base)...)
			i = j - 1
			continue
		}

		// Block quote.
		if strings.HasPrefix(trimmed, ">") {
			flushPara()
			text := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			bar := line{{"▎ ", stFaint}}
			out = append(out, wrap(inline(text, base.Italic(true).Foreground(cDim)), width, bar, bar)...)
			continue
		}

		// Lists.
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		pad := strings.Repeat(" ", min(indent, 12))
		if marker, rest, ok := listItem(trimmed); ok {
			flushPara()
			first := line{{pad + marker, stAccent}}
			next := line{{pad + strings.Repeat(" ", uniseg.StringWidth(marker)), base}}
			if strings.HasPrefix(rest, "[ ] ") {
				first = line{{pad + "☐ ", stDim}}
				rest = rest[4:]
			} else if strings.HasPrefix(rest, "[x] ") || strings.HasPrefix(rest, "[X] ") {
				first = line{{pad + "☑ ", stGreen}}
				rest = rest[4:]
			}
			out = append(out, wrap(inline(rest, base), width, first, next)...)
			continue
		}

		para = append(para, trimmed)
	}
	flushPara()
	for len(out) > 0 && len(out[len(out)-1]) == 0 {
		out = out[:len(out)-1]
	}
	return out
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
func inline(s string, base tcell.Style) []span {
	var out []span
	var b strings.Builder
	st := base
	bold, italic, strike := false, false, false
	emit := func() {
		if b.Len() > 0 {
			out = append(out, span{b.String(), st})
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
			out = append(out, span{s[i+1 : i+1+end], stCode})
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
			out = append(out, span{text, base.Foreground(cAccent).Underline(true)})
			if url != text {
				out = append(out, span{" (" + url + ")", stFaint})
			}
			i += close + end
		default:
			b.WriteByte(c)
		}
	}
	emit()
	return out
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

func table(rows []string, width int, base tcell.Style) []line {
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
		var out []line
		for _, r := range cells {
			if isSep(r) {
				continue
			}
			out = append(out, wrap(inline(strings.Join(r, " │ "), base), width, nil, nil)...)
		}
		return out
	}
	var out []line
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
			out = append(out, line{{b.String(), stFaint}})
			continue
		}
		l := line{{"│", stFaint}}
		for k := 0; k < cols; k++ {
			c := ""
			if k < len(r) {
				c = r[k]
			}
			st := base
			if ri == 0 {
				st = base.Bold(true)
			}
			l = append(l, span{" ", base})
			l = append(l, inline(c, st)...)
			l = append(l, span{strings.Repeat(" ", widths[k]-uniseg.StringWidth(plain(c))+1), base}, span{"│", stFaint})
		}
		out = append(out, l)
	}
	return out
}

// plain is the visible text of inline markdown.
func plain(s string) string {
	var b strings.Builder
	for _, sp := range inline(s, tcell.StyleDefault) {
		b.WriteString(sp.text)
	}
	return b.String()
}

var keywords = map[string]bool{}

func init() {
	for _, k := range strings.Fields(`func package import return if else for range switch case default
		go defer type struct interface map chan var const break continue select nil true false
		fn let mut pub impl use mod match loop while trait enum where self Self async await
		def class from as with try except finally raise lambda None True False yield pass
		function export new this typeof instanceof extends implements public private protected
		static void int string bool float double char long do then fi esac done in echo local`) {
		keywords[k] = true
	}
}

// highlight is a small, language agnostic code colorizer: keywords,
// strings, numbers and comments.
func highlight(src, lang string) []span {
	src = clean(src)
	switch lang {
	case "diff", "patch":
		switch {
		case strings.HasPrefix(src, "+"):
			return []span{{src, stGreen}}
		case strings.HasPrefix(src, "-"):
			return []span{{src, stRed}}
		case strings.HasPrefix(src, "@@"):
			return []span{{src, stCyan}}
		}
		return []span{{src, stBase}}
	}
	var out []span
	codeBase := stBase
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case strings.HasPrefix(src[i:], "//") || (c == '#' && lang != "" && lang != "go" && lang != "rust" && lang != "c" && lang != "cpp"):
			out = append(out, span{src[i:], stFaint.Italic(true)})
			return out
		case c == '"' || c == '\'' || c == '`':
			j := i + 1
			for j < len(src) && src[j] != c {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(src))
			out = append(out, span{src[i:j], stGreen})
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && (isWord(rune(src[j])) || src[j] == '.' || src[j] == '_') {
				j++
			}
			out = append(out, span{src[i:j], stOrangeNum})
			i = j
		case isWord(rune(c)) || c == '_':
			j := i
			for j < len(src) && (isWord(rune(src[j])) || src[j] == '_') {
				j++
			}
			w := src[i:j]
			st := codeBase
			if keywords[w] {
				st = stAccent
			} else if j < len(src) && src[j] == '(' {
				st = stCyan
			}
			out = append(out, span{w, st})
			i = j
		default:
			out = append(out, span{string(c), codeBase})
			i++
		}
	}
	return out
}

var stOrangeNum = tcell.StyleDefault.Foreground(cYellow)
