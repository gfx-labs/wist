package tui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	acp "github.com/gfx-labs/go-acp"
	"github.com/gfx-labs/wist/pkg/render"
)

type blockKind int

// toolOutputMode is how much of a tool call's output the transcript shows.
// A card the user expanded always shows all of it.
type toolOutputMode int

const (
	toolOutputPreview toolOutputMode = iota // a few lines per call
	toolOutputHidden                        // the head line only
	toolOutputFull                          // every line
)

func (m toolOutputMode) String() string {
	switch m {
	case toolOutputHidden:
		return "hidden"
	case toolOutputFull:
		return "full"
	}
	return "preview"
}

// next is the order the tool_output key and command cycle through.
func (m toolOutputMode) next() toolOutputMode {
	switch m {
	case toolOutputHidden:
		return toolOutputPreview
	case toolOutputPreview:
		return toolOutputFull
	}
	return toolOutputHidden
}

// parseToolOutput reads a tool_output value, empty meaning the default.
func parseToolOutput(s string) (toolOutputMode, bool) {
	switch s {
	case "":
		return toolOutputPreview, true
	case "hidden":
		return toolOutputHidden, true
	case "preview":
		return toolOutputPreview, true
	case "full":
		return toolOutputFull, true
	}
	return toolOutputPreview, false
}

const (
	kindUser blockKind = iota
	kindAgent
	kindThought
	kindTool
	kindNotice
	kindError
	kindInfo
	kindCompaction
)

// block is one entry of the transcript. Its rendered lines are cached by
// width and dropped whenever the block changes.
type block struct {
	kind blockKind
	text string

	// tool calls
	toolID    acp.ToolCallId
	title     string
	toolKind  acp.ToolKind
	status    acp.ToolCallStatus
	content   []acp.ToolCallContent
	locations []acp.ToolCallLocation
	rawInput  any
	started   time.Time
	finished  time.Time

	expanded bool
	queued   bool

	cacheW     int
	cacheExp   bool
	cacheMode  toolOutputMode
	cacheLines []render.Line
	codes      []render.CodeRef // fenced code blocks of an agent message, by rendered line
}

func (b *block) dirty() { b.cacheW = -1 }

// lines renders the block for width. showThinking and expandAll are UI
// toggles that change how thoughts and tool output are shown; mode sets
// how much of a tool call's body is drawn.
func (b *block) lines(width int, expandAll bool, showThinking bool, mode toolOutputMode) []render.Line {
	exp := b.expanded || expandAll
	if b.cacheW == width && b.cacheExp == exp && b.cacheMode == mode && b.kind != kindThought && b.status != acp.ToolCallStatusInProgress {
		return b.cacheLines
	}
	var out []render.Line
	switch b.kind {
	case kindUser:
		st := render.StUser
		if b.queued {
			st = render.StDim
		}
		prefix := render.Line{{Text: "❯ ", St: st.Bold(true)}}
		body := tcell.StyleDefault.Foreground(render.CText).Bold(true)
		if b.queued {
			body = render.StDim.Italic(true)
			prefix = render.Line{{Text: "⏳ ", St: render.StDim}}
		}
		out = render.Wrap([]render.Span{{Text: render.Clean(b.text), St: body}}, width, prefix, render.Line{{Text: "  ", St: st}})
		if b.queued {
			out = append(out, render.Line{{Text: "  will send after this turn", St: render.StFaint.Italic(true)}})
		}
	case kindAgent:
		out, b.codes = render.MarkdownDoc(render.Clean(b.text), width, render.StBase)
	case kindThought:
		text := strings.TrimSpace(render.Clean(b.text))
		if !showThinking && !exp {
			first, _, _ := strings.Cut(text, "\n")
			out = []render.Line{{{Text: "✻ ", St: render.StDim}, {Text: render.Truncate("thinking: "+first, width-2), St: render.StDim.Italic(true)}}}
		} else {
			bar := render.Line{{Text: "┊ ", St: render.StFaint}}
			out = []render.Line{{{Text: "✻ thinking", St: render.StDim}}}
			for _, l := range render.Markdown(text, width-2, render.StDim.Italic(true)) {
				out = append(out, append(append(render.Line{}, bar...), dimLine(l)...))
			}
		}
	case kindTool:
		out = b.toolLines(width, exp, mode)
	case kindNotice:
		out = render.Wrap([]render.Span{{Text: render.Clean(b.text), St: render.StYellow}}, width, render.Line{{Text: "⚑ ", St: render.StYellow}}, render.Line{{Text: "  ", St: render.StYellow}})
	case kindError:
		msg := render.Clean(b.text)
		key, rest, _ := strings.Cut(msg, ":")
		sp := []render.Span{{Text: key, St: errorStyle(render.StRed)}}
		if rest != "" || strings.HasSuffix(msg, ":") {
			sp = append(sp, render.Span{Text: ":" + rest, St: render.StRed})
		}
		out = render.Wrap(sp, width, render.Line{{Text: "✗ ", St: render.StRed}}, render.Line{{Text: "  ", St: render.StRed}})
	case kindInfo:
		out = render.Wrap([]render.Span{{Text: render.Clean(b.text), St: render.StDim}}, width, render.Line{{Text: "· ", St: render.StDim}}, render.Line{{Text: "  ", St: render.StDim}})
	case kindCompaction:
		out = render.Wrap([]render.Span{{Text: render.Clean(b.text), St: render.StCyan}}, width, render.Line{{Text: "⇣ ", St: render.StCyan}}, render.Line{{Text: "  ", St: render.StCyan}})
	}
	b.cacheW, b.cacheExp, b.cacheMode, b.cacheLines = width, exp, mode, out
	return out
}

func toolIcon(kind acp.ToolKind) string {
	switch kind {
	case acp.ToolKindRead:
		return "📖"
	case acp.ToolKindEdit:
		return "✎"
	case acp.ToolKindDelete:
		return "🗑"
	case acp.ToolKindMove:
		return "⇄"
	case acp.ToolKindSearch:
		return "⌕"
	case acp.ToolKindExecute:
		return "$"
	case acp.ToolKindThink:
		return "✻"
	case acp.ToolKindFetch:
		return "🌐"
	}
	return "⚙"
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func spinner() string {
	return spinnerFrames[int(time.Now().UnixMilli()/80)%len(spinnerFrames)]
}

func (b *block) toolLines(width int, exp bool, mode toolOutputMode) []render.Line {
	var mark render.Span
	switch b.status {
	case acp.ToolCallStatusCompleted:
		mark = render.Span{Text: "✓", St: render.StGreen}
	case acp.ToolCallStatusFailed:
		mark = render.Span{Text: "✗", St: render.StRed}
	case acp.ToolCallStatusInProgress:
		mark = render.Span{Text: spinner(), St: render.StYellow}
	default:
		mark = render.Span{Text: "○", St: render.StDim}
	}
	title := render.Clean(b.title)
	if title == "" {
		title = "tool"
	}
	head := render.Line{mark, {Text: " " + toolIcon(b.toolKind) + " ", St: render.StAccent}}
	var dur string
	if !b.started.IsZero() {
		end := b.finished
		if end.IsZero() {
			end = time.Now()
		}
		if d := end.Sub(b.started); d >= 500*time.Millisecond {
			dur = " " + fmtDuration(d)
		}
	}
	summary := b.summary()
	room := width - head.Width() - len(dur) - 2
	t := render.Truncate(title, max(room, 8))
	tst := render.StBase.Bold(true)
	if b.status == acp.ToolCallStatusFailed {
		tst = errorStyle(tst)
	}
	head = append(head, render.Span{Text: t, St: tst})
	if s := render.Truncate(summary, room-len([]rune(t))-1); s != "" && summary != "" {
		head = append(head, render.Span{Text: " ", St: render.StDim})
		sst := render.StDim
		if len(b.locations) > 0 {
			if u := fileURL(b.locations[0].Path); u != "" {
				sst = sst.Url(u)
			}
		}
		head = append(head, render.Span{Text: s, St: sst})
	}
	head = append(head, render.Span{Text: dur, St: render.StFaint})
	out := []render.Line{head}
	if exp {
		mode = toolOutputFull
	}
	if mode == toolOutputHidden {
		return out
	}

	body := b.bodyLines(width - 4)
	limit := 6
	if b.status == acp.ToolCallStatusFailed {
		limit = 12
	}
	if mode == toolOutputFull {
		limit = len(body)
	}
	gutter := render.Line{{Text: "  │ ", St: render.StFaint}}
	for i, l := range body {
		if i == limit {
			out = append(out, render.Line{{Text: "  ╰ ", St: render.StFaint}, {Text: fmt.Sprintf("… %d more lines (ctrl+o to expand)", len(body)-limit), St: render.StFaint.Italic(true)}})
			break
		}
		out = append(out, append(append(render.Line{}, gutter...), l...))
	}
	return out
}

// summary is a one line digest of the tool's input or result, shown next
// to the title.
func (b *block) summary() string {
	if len(b.locations) > 0 {
		loc := b.locations[0]
		s := loc.Path
		if loc.Line != nil {
			s = fmt.Sprintf("%s:%d", s, *loc.Line)
		}
		if strings.Contains(b.title, s) {
			return ""
		}
		return s
	}
	return ""
}

func (b *block) bodyLines(width int) []render.Line {
	var out []render.Line
	for _, c := range b.content {
		switch {
		case c.Diff != nil:
			out = append(out, diffLines(c.Diff, width)...)
		case c.Content != nil:
			text := contentText(c.Content.Content)
			text = strings.TrimRight(render.Clean(text), "\n")
			if text == "" {
				continue
			}
			for _, raw := range strings.Split(text, "\n") {
				out = append(out, render.Wrap(render.Linkify([]render.Span{{Text: raw, St: render.StDim}}), width, nil, nil)...)
			}
		case c.Terminal != nil:
			out = append(out, render.Line{{Text: "[terminal " + string(c.Terminal.TerminalId) + "]", St: render.StDim}})
		}
	}
	return out
}

func contentText(c acp.ContentBlock) string {
	switch {
	case c.Text != nil:
		return c.Text.Text
	case c.ResourceLink != nil:
		return "@" + c.ResourceLink.Uri
	case c.Resource != nil:
		if r := c.Resource.Resource.TextResourceContents; r != nil {
			return r.Text
		}
		return "[resource]"
	case c.Image != nil:
		return imagePlaceholder(c.Image.MimeType, c.Image.Data)
	case c.Audio != nil:
		return "[audio]"
	}
	return ""
}

// diffLines renders an edit as a unified line diff with colored
// backgrounds, like jcode's diff cards.
func diffLines(d *acp.ToolCallContentDiff, width int) []render.Line {
	old := ""
	if d.OldText != nil {
		old = render.Clean(*d.OldText)
	}
	ops := lineDiff(splitLines(old), splitLines(render.Clean(d.NewText)))
	adds, dels := 0, 0
	for _, o := range ops {
		switch o.kind {
		case '+':
			adds++
		case '-':
			dels++
		}
	}
	out := []render.Line{{{Text: d.Path + " ", St: render.StAccent}, {Text: fmt.Sprintf("+%d", adds), St: render.StGreen}, {Text: " ", St: render.StDim}, {Text: fmt.Sprintf("-%d", dels), St: render.StRed}}}
	// Show changes with three lines of context, eliding the rest.
	const ctx = 3
	keep := make([]bool, len(ops))
	for i, o := range ops {
		if o.kind != ' ' {
			for j := max(0, i-ctx); j <= min(len(ops)-1, i+ctx); j++ {
				keep[j] = true
			}
		}
	}
	skipped := false
	for i, o := range ops {
		if !keep[i] {
			skipped = true
			continue
		}
		if skipped {
			out = append(out, render.Line{{Text: "  ⋯", St: render.StFaint}})
			skipped = false
		}
		var st tcell.Style
		var sign string
		switch o.kind {
		case '+':
			st, sign = tcell.StyleDefault.Foreground(render.CGreen).Background(render.CAddBg), "+ "
		case '-':
			st, sign = tcell.StyleDefault.Foreground(render.CRed).Background(render.CDelBg), "- "
		default:
			st, sign = render.StDim, "  "
		}
		text := render.Truncate(sign+o.text, width)
		if o.kind != ' ' {
			text += strings.Repeat(" ", max(0, width-len([]rune(text))))
		}
		out = append(out, render.Line{{Text: text, St: st}})
	}
	return out
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

type diffOp struct {
	kind byte
	text string
}

// lineDiff is an LCS line diff. Large inputs fall back to a plain
// remove-all/add-all so a huge file cannot stall the UI.
func lineDiff(a, b []string) []diffOp {
	// Trim common prefix and suffix first; most edits are small.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var ops []diffOp
	for _, s := range a[:pre] {
		ops = append(ops, diffOp{' ', s})
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	if len(ma)*len(mb) > 4_000_000 {
		for _, s := range ma {
			ops = append(ops, diffOp{'-', s})
		}
		for _, s := range mb {
			ops = append(ops, diffOp{'+', s})
		}
	} else {
		n, m := len(ma), len(mb)
		dp := make([][]int32, n+1)
		for i := range dp {
			dp[i] = make([]int32, m+1)
		}
		for i := n - 1; i >= 0; i-- {
			for j := m - 1; j >= 0; j-- {
				if ma[i] == mb[j] {
					dp[i][j] = dp[i+1][j+1] + 1
				} else {
					dp[i][j] = max(dp[i+1][j], dp[i][j+1])
				}
			}
		}
		i, j := 0, 0
		for i < n && j < m {
			switch {
			case ma[i] == mb[j]:
				ops = append(ops, diffOp{' ', ma[i]})
				i++
				j++
			case dp[i+1][j] >= dp[i][j+1]:
				ops = append(ops, diffOp{'-', ma[i]})
				i++
			default:
				ops = append(ops, diffOp{'+', mb[j]})
				j++
			}
		}
		for ; i < n; i++ {
			ops = append(ops, diffOp{'-', ma[i]})
		}
		for ; j < m; j++ {
			ops = append(ops, diffOp{'+', mb[j]})
		}
	}
	for _, s := range a[len(a)-suf:] {
		ops = append(ops, diffOp{' ', s})
	}
	return ops
}

func fmtDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func fmtTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

// formatRaw renders a tool's raw input as "key: value" lines. go-acp keeps
// it as raw JSON bytes, which fmt would print as a byte slice.
func formatRaw(v any) string {
	var data []byte
	switch t := v.(type) {
	case []byte:
		data = t
	case json.RawMessage:
		data = t
	case string:
		data = []byte(t)
		// towa sends raw JSON bytes, which encode as a base64 string.
		if dec, err := base64.StdEncoding.DecodeString(t); err == nil && json.Valid(dec) {
			data = dec
		}
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		data = b
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return string(data)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		val := m[k]
		s, ok := val.(string)
		if !ok {
			enc, _ := json.Marshal(val)
			s = string(enc)
		}
		fmt.Fprintf(&b, "%s: %s\n", k, s)
	}
	return strings.TrimRight(b.String(), "\n")
}
