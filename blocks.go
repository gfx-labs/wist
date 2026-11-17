package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	acp "github.com/gfx-labs/go-acp"
)

type blockKind int

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
	cacheLines []line
}

func (b *block) dirty() { b.cacheW = -1 }

// lines renders the block for width. showThinking and expandAll are UI
// toggles that change how thoughts and tool output are shown.
func (b *block) lines(width int, expandAll bool, showThinking bool) []line {
	exp := b.expanded || expandAll
	if b.cacheW == width && b.cacheExp == exp && b.kind != kindThought && b.status != acp.ToolCallStatusInProgress {
		return b.cacheLines
	}
	var out []line
	switch b.kind {
	case kindUser:
		st := stUser
		if b.queued {
			st = stDim
		}
		prefix := line{{"❯ ", st.Bold(true)}}
		body := tcell.StyleDefault.Foreground(cText).Bold(true)
		if b.queued {
			body = stDim.Italic(true)
			prefix = line{{"⏳ ", stDim}}
		}
		out = wrap([]span{{clean(b.text), body}}, width, prefix, line{{"  ", st}})
	case kindAgent:
		out = markdown(clean(b.text), width, stBase)
	case kindThought:
		text := strings.TrimSpace(clean(b.text))
		if !showThinking && !exp {
			first, _, _ := strings.Cut(text, "\n")
			out = []line{{{"✻ ", stDim}, {truncate("thinking: "+first, width-2), stDim.Italic(true)}}}
		} else {
			bar := line{{"┊ ", stFaint}}
			out = append([]line{{{"✻ thinking", stDim}}}, wrap([]span{{text, stDim.Italic(true)}}, width, bar, bar)...)
		}
	case kindTool:
		out = b.toolLines(width, exp)
	case kindNotice:
		out = wrap([]span{{clean(b.text), stYellow}}, width, line{{"⚑ ", stYellow}}, line{{"  ", stYellow}})
	case kindError:
		out = wrap([]span{{clean(b.text), stRed}}, width, line{{"✗ ", stRed}}, line{{"  ", stRed}})
	case kindInfo:
		out = wrap([]span{{clean(b.text), stDim}}, width, line{{"· ", stDim}}, line{{"  ", stDim}})
	case kindCompaction:
		out = wrap([]span{{clean(b.text), stCyan}}, width, line{{"⇣ ", stCyan}}, line{{"  ", stCyan}})
	}
	b.cacheW, b.cacheExp, b.cacheLines = width, exp, out
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

func (b *block) toolLines(width int, exp bool) []line {
	var mark span
	switch b.status {
	case acp.ToolCallStatusCompleted:
		mark = span{"✓", stGreen}
	case acp.ToolCallStatusFailed:
		mark = span{"✗", stRed}
	case acp.ToolCallStatusInProgress:
		mark = span{spinner(), stYellow}
	default:
		mark = span{"○", stDim}
	}
	title := clean(b.title)
	if title == "" {
		title = "tool"
	}
	head := line{mark, {" " + toolIcon(b.toolKind) + " ", stAccent}}
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
	room := width - head.width() - len(dur) - 2
	t := truncate(title, max(room, 8))
	head = append(head, span{t, stBase.Bold(true)})
	if s := truncate(summary, room-len([]rune(t))-1); s != "" && summary != "" {
		head = append(head, span{" " + s, stDim})
	}
	head = append(head, span{dur, stFaint})
	out := []line{head}

	body := b.bodyLines(width - 4)
	limit := 6
	if b.status == acp.ToolCallStatusFailed {
		limit = 12
	}
	if exp {
		limit = len(body)
	}
	gutter := line{{"  │ ", stFaint}}
	for i, l := range body {
		if i == limit {
			out = append(out, line{{"  ╰ ", stFaint}, {fmt.Sprintf("… %d more lines (ctrl+o to expand)", len(body)-limit), stFaint.Italic(true)}})
			break
		}
		out = append(out, append(append(line{}, gutter...), l...))
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

func (b *block) bodyLines(width int) []line {
	var out []line
	for _, c := range b.content {
		switch {
		case c.Diff != nil:
			out = append(out, diffLines(c.Diff, width)...)
		case c.Content != nil:
			text := contentText(c.Content.Content)
			text = strings.TrimRight(clean(text), "\n")
			if text == "" {
				continue
			}
			for _, raw := range strings.Split(text, "\n") {
				out = append(out, wrap([]span{{raw, stDim}}, width, nil, nil)...)
			}
		case c.Terminal != nil:
			out = append(out, line{{"[terminal " + string(c.Terminal.TerminalId) + "]", stDim}})
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
		return "[image " + c.Image.MimeType + "]"
	case c.Audio != nil:
		return "[audio]"
	}
	return ""
}

// diffLines renders an edit as a unified line diff with colored
// backgrounds, like jcode's diff cards.
func diffLines(d *acp.ToolCallContentDiff, width int) []line {
	old := ""
	if d.OldText != nil {
		old = clean(*d.OldText)
	}
	ops := lineDiff(splitLines(old), splitLines(clean(d.NewText)))
	adds, dels := 0, 0
	for _, o := range ops {
		switch o.kind {
		case '+':
			adds++
		case '-':
			dels++
		}
	}
	out := []line{{{d.Path + " ", stAccent}, {fmt.Sprintf("+%d", adds), stGreen}, {" ", stDim}, {fmt.Sprintf("-%d", dels), stRed}}}
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
			out = append(out, line{{"  ⋯", stFaint}})
			skipped = false
		}
		var st tcell.Style
		var sign string
		switch o.kind {
		case '+':
			st, sign = tcell.StyleDefault.Foreground(cGreen).Background(cAddBg), "+ "
		case '-':
			st, sign = tcell.StyleDefault.Foreground(cRed).Background(cDelBg), "- "
		default:
			st, sign = stDim, "  "
		}
		text := truncate(sign+o.text, width)
		if o.kind != ' ' {
			text += strings.Repeat(" ", max(0, width-len([]rune(text))))
		}
		out = append(out, line{{text, st}})
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
