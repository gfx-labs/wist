package tui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
)

// Hand-rolled per-language syntax highlighting. Each language family gets a
// small spec; the tokenizer is shared and works line by line, carrying only
// "inside a block comment/string" as state between lines.

var stKeyword, stType, stFunc, stString, stNumber, stComment, stConst, stMeta, stKey, stOrangeNum tcell.Style

func init() { refreshStyles() }

// refreshStyles rebuilds the styles derived from theme colors. applyTheme
// calls it after changing the palette.
func refreshStyles() {
	fitPalette()
	stKeyword = tcell.StyleDefault.Foreground(cAccent)
	stType = tcell.StyleDefault.Foreground(cCyan)
	stFunc = tcell.StyleDefault.Foreground(cUser)
	stString = tcell.StyleDefault.Foreground(cGreen)
	stNumber = tcell.StyleDefault.Foreground(cYellow)
	stOrangeNum = stNumber
	stComment = tcell.StyleDefault.Foreground(cFaint).Italic(true)
	stConst = tcell.StyleDefault.Foreground(cOrange)
	stMeta = tcell.StyleDefault.Foreground(cUser)
	stKey = tcell.StyleDefault.Foreground(cAccent)
	mmStyles = [...]tcell.Style{0: stBase, mmEdge: stDim, mmBox: stAccent, mmText: stBase, mmLabel: stYellow, mmArrow: stAccent}
}

type langSpec struct {
	kw, types, consts map[string]bool
	lineComment       []string
	blockOpen         string
	blockClose        string
	blockStyle        tcell.Style
	quotes            string
	caseFold          bool
	family            string
}

func set(s string) map[string]bool {
	m := map[string]bool{}
	for _, f := range strings.Fields(s) {
		m[f] = true
	}
	return m
}

var langs = map[string]*langSpec{}

func init() {
	cBlock := func(s *langSpec) *langSpec {
		s.lineComment, s.blockOpen, s.blockClose, s.blockStyle = []string{"//"}, "/*", "*/", stComment
		return s
	}
	goL := cBlock(&langSpec{family: "go", quotes: "\"'`",
		kw:     set("break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var"),
		types:  set("bool byte complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr any comparable"),
		consts: set("true false nil iota"),
	})
	rustL := cBlock(&langSpec{family: "rust", quotes: "\"",
		kw:     set("as async await break const continue crate dyn else enum extern fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait type unsafe use where while"),
		types:  set("i8 i16 i32 i64 i128 isize u8 u16 u32 u64 u128 usize f32 f64 bool char str String Vec Option Result Box"),
		consts: set("true false None Some Ok Err"),
	})
	jsL := cBlock(&langSpec{family: "js", quotes: "\"'`",
		kw:     set("async await break case catch class const continue debugger default delete do else export extends finally for from function if import in instanceof let new of return static super switch this throw try typeof var void while with yield interface type enum implements public private protected readonly as declare namespace keyof"),
		types:  set("string number boolean any unknown never object void Promise Array Map Set Record"),
		consts: set("true false null undefined NaN Infinity"),
	})
	cL := cBlock(&langSpec{family: "c", quotes: "\"'",
		kw:     set("auto break case const continue default do else enum extern for goto if inline register return sizeof static struct switch typedef union volatile while class namespace template typename public private protected virtual override new delete using try catch throw constexpr nullptr"),
		types:  set("char double float int long short signed unsigned void bool size_t int8_t int16_t int32_t int64_t uint8_t uint16_t uint32_t uint64_t std string vector auto"),
		consts: set("true false NULL nullptr"),
	})
	pyL := &langSpec{family: "python", quotes: "\"'", lineComment: []string{"#"}, blockOpen: `"""`, blockClose: `"""`, blockStyle: stString,
		kw:     set("and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield match case"),
		types:  set("int float str bool list dict set tuple bytes object type self cls"),
		consts: set("True False None"),
	}
	shL := &langSpec{family: "sh", quotes: "\"'", lineComment: []string{"#"},
		kw:     set("if then else elif fi for while until do done case esac in function return local export readonly declare set unset shift exit break continue source alias"),
		types:  set("echo printf cd ls grep sed awk cat rm cp mv mkdir go git make curl test sudo"),
		consts: set("true false"),
	}
	jsonL := &langSpec{family: "json", quotes: "\"", consts: set("true false null")}
	yamlL := &langSpec{family: "yaml", quotes: "\"'", lineComment: []string{"#"}, consts: set("true false null yes no on off ~")}
	tomlL := &langSpec{family: "toml", quotes: "\"'", lineComment: []string{"#"}, consts: set("true false")}
	sqlL := &langSpec{family: "sql", quotes: "'\"", lineComment: []string{"--"}, blockOpen: "/*", blockClose: "*/", blockStyle: stComment, caseFold: true,
		kw:     set("select from where and or not insert into values update set delete create table index view drop alter add column primary key foreign references join left right inner outer full on as group by order having limit offset union all distinct case when then else end is null in like between exists returning with begin commit rollback default unique constraint if"),
		types:  set("int integer bigint smallint text varchar char boolean bool date timestamp timestamptz numeric decimal real float serial uuid jsonb json blob"),
		consts: set("true false null"),
	}
	htmlL := &langSpec{family: "html"}
	for names, s := range map[string]*langSpec{
		"go golang": goL,
		"rust rs":   rustL,
		"js javascript jsx ts typescript tsx mjs cjs":            jsL,
		"c h cpp c++ cc hpp cxx java cs csharp kotlin swift zig": cL,
		"python py python3":                    pyL,
		"sh bash zsh shell console fish":       shL,
		"json jsonc json5":                     jsonL,
		"yaml yml":                             yamlL,
		"toml ini":                             tomlL,
		"sql postgres postgresql mysql sqlite": sqlL,
		"html xml svg vue":                     htmlL,
	} {
		for _, n := range strings.Fields(names) {
			langs[n] = s
		}
	}
}

// generic is used for unknown languages: a little of everything.
var generic = &langSpec{family: "generic", quotes: "\"'`", lineComment: []string{"//"},
	kw:     set("func fn def function class return if else for while let const var import from package use pub struct type interface match in"),
	consts: set("true false nil null None True False"),
}

// highlight colors one line of code without cross-line state.
func highlight(src, lang string) []span {
	sp, _ := highlightLine(src, lang, 0)
	return sp
}

func specFor(lang string) *langSpec {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(lang, " {,"); i >= 0 {
		lang = lang[:i]
	}
	if s, ok := langs[lang]; ok {
		return s
	}
	return generic
}

// highlightLine colors src. state 1 means a block comment (or multi-line
// string) is open at the start of the line; the returned state says
// whether it is still open at the end.
func highlightLine(src, lang string, state int) ([]span, int) {
	src = clean(src)
	switch strings.ToLower(lang) {
	case "diff", "patch":
		switch {
		case strings.HasPrefix(src, "+++"), strings.HasPrefix(src, "---"):
			return []span{{src, stBase.Bold(true)}}, 0
		case strings.HasPrefix(src, "+"):
			return []span{{src, stGreen}}, 0
		case strings.HasPrefix(src, "-"):
			return []span{{src, stRed}}, 0
		case strings.HasPrefix(src, "@@"):
			return []span{{src, stCyan}}, 0
		}
		return []span{{src, stDim}}, 0
	}
	s := specFor(lang)
	if s.family == "html" {
		return highlightHTML(src, state)
	}
	var out []span
	add := func(t string, st tcell.Style) {
		if t == "" {
			return
		}
		if n := len(out); n > 0 && out[n-1].st == st {
			out[n-1].text += t
			return
		}
		out = append(out, span{t, st})
	}
	i := 0
	if state == 1 && s.blockClose != "" {
		end := strings.Index(src, s.blockClose)
		if end < 0 {
			return []span{{src, s.blockStyle}}, 1
		}
		add(src[:end+len(s.blockClose)], s.blockStyle)
		i = end + len(s.blockClose)
	}
	// Line-start constructs.
	lead := len(src) - len(strings.TrimLeft(src, " "))
	switch s.family {
	case "c":
		if strings.HasPrefix(src[lead:], "#") {
			add(src[i:], stMeta)
			return out, 0
		}
	case "toml":
		if strings.HasPrefix(src[lead:], "[") {
			add(src[i:], stKey.Bold(true))
			return out, 0
		}
	}
	for i < len(src) {
		c := src[i]
		rest := src[i:]
		if s.blockOpen != "" && strings.HasPrefix(rest, s.blockOpen) {
			end := strings.Index(rest[len(s.blockOpen):], s.blockClose)
			if end < 0 {
				add(rest, s.blockStyle)
				return out, 1
			}
			n := len(s.blockOpen) + end + len(s.blockClose)
			add(rest[:n], s.blockStyle)
			i += n
			continue
		}
		if isLineComment(s, src, i) {
			add(rest, stComment)
			return out, 0
		}
		switch {
		case strings.IndexByte(s.quotes, c) >= 0:
			j := i + 1
			for j < len(src) && src[j] != c {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(src))
			st := stString
			// Object keys in JSON/YAML: a string followed by a colon.
			if s.family == "json" || s.family == "yaml" {
				if k := strings.TrimLeft(src[j:], " "); strings.HasPrefix(k, ":") {
					st = stKey
				}
			}
			add(src[i:j], st)
			i = j
		case c == '$' && s.family == "sh":
			j := i + 1
			if j < len(src) && src[j] == '{' {
				if e := strings.IndexByte(src[j:], '}'); e >= 0 {
					j += e + 1
				}
			} else {
				for j < len(src) && (isWordByte(src[j]) || (j == i+1 && strings.IndexByte("@#?*!$0123456789", src[j]) >= 0)) {
					j++
				}
			}
			add(src[i:j], stType)
			i = j
		case c == '@' && (s.family == "python" || s.family == "js"):
			j := i + 1
			for j < len(src) && (isWordByte(src[j]) || src[j] == '.') {
				j++
			}
			add(src[i:j], stMeta)
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && (isWordByte(src[j]) || src[j] == '.') {
				j++
			}
			add(src[i:j], stNumber)
			i = j
		case isWordByte(c) || c >= 0x80:
			j := i
			for j < len(src) && (isWordByte(src[j]) || src[j] >= 0x80 || (s.family == "sh" && src[j] == '-' && j > i)) {
				j++
			}
			w := src[i:j]
			key := w
			if s.caseFold {
				key = strings.ToLower(w)
			}
			st := stBase
			switch {
			case (s.family == "yaml" || s.family == "toml") && i == lead && keyFollows(src[j:], s.family):
				st = stKey
			case s.kw[key]:
				st = stKeyword
			case s.consts[key]:
				st = stConst
			case s.types[key]:
				st = stType
			case j < len(src) && src[j] == '!' && s.family == "rust":
				st = stMeta
				j++
				w = src[i:j]
			case j < len(src) && src[j] == '(':
				st = stFunc
			case s.family != "sh" && len(w) > 1 && w[0] >= 'A' && w[0] <= 'Z' && s.family != "sql":
				st = stType
			}
			add(w, st)
			i = j
		default:
			add(string(c), stBase)
			i++
		}
	}
	return out, 0
}

func isLineComment(s *langSpec, src string, i int) bool {
	for _, lc := range s.lineComment {
		if !strings.HasPrefix(src[i:], lc) {
			continue
		}
		// '#' only starts a comment at a word boundary (not in a$#b or url#frag).
		if lc == "#" && i > 0 && src[i-1] != ' ' {
			return false
		}
		return true
	}
	return false
}

func keyFollows(rest, family string) bool {
	rest = strings.TrimLeft(rest, " ")
	if family == "yaml" {
		return strings.HasPrefix(rest, ":")
	}
	return strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, ".")
}

func isWordByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// highlightHTML colors tags, attributes, strings and comments.
func highlightHTML(src string, state int) ([]span, int) {
	var out []span
	i := 0
	if state == 1 {
		end := strings.Index(src, "-->")
		if end < 0 {
			return []span{{src, stComment}}, 1
		}
		out = append(out, span{src[:end+3], stComment})
		i = end + 3
	}
	for i < len(src) {
		rest := src[i:]
		switch {
		case strings.HasPrefix(rest, "<!--"):
			end := strings.Index(rest, "-->")
			if end < 0 {
				out = append(out, span{rest, stComment})
				return out, 1
			}
			out = append(out, span{rest[:end+3], stComment})
			i += end + 3
		case rest[0] == '<':
			end := strings.IndexByte(rest, '>')
			if end < 0 {
				end = len(rest) - 1
			}
			tag := rest[:end+1]
			j := 1
			for j < len(tag) && (isWordByte(tag[j]) || tag[j] == '/' || tag[j] == '!' || tag[j] == '-' || tag[j] == ':') {
				j++
			}
			out = append(out, span{tag[:j], stKeyword})
			for j < len(tag) {
				c := tag[j]
				switch {
				case c == '"' || c == '\'':
					k := strings.IndexByte(tag[j+1:], c)
					if k < 0 {
						k = len(tag) - j - 2
					}
					out = append(out, span{tag[j : j+k+2], stString})
					j += k + 2
				case isWordByte(c) || c == '-':
					k := j
					for k < len(tag) && (isWordByte(tag[k]) || tag[k] == '-' || tag[k] == ':') {
						k++
					}
					out = append(out, span{tag[j:k], stType})
					j = k
				default:
					st := stDim
					if c == '>' || c == '/' {
						st = stKeyword
					}
					out = append(out, span{string(c), st})
					j++
				}
			}
			i += end + 1
		default:
			end := strings.IndexByte(rest, '<')
			if end < 0 {
				end = len(rest)
			}
			out = append(out, span{rest[:end], stBase})
			i += end
		}
	}
	return out, 0
}
