package render

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func text(ls []Line) string {
	var b strings.Builder
	for _, l := range ls {
		for _, s := range l {
			b.WriteString(s.Text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func styleOf(sp []Span, word string) (tcell.Style, bool) {
	for _, s := range sp {
		if strings.TrimSpace(s.Text) == word {
			return s.St, true
		}
	}
	return tcell.Style{}, false
}

func TestCodeBlockCopyLabelOnlyWhenClosed(t *testing.T) {
	open, codes := MarkdownDoc("```go\nfmt.Println(1)", 60, StBase)
	if strings.Contains(text(open), CopyLabel) || len(codes) != 1 || codes[0].Closed {
		t.Fatalf("streaming block should have no copy label: %q %+v", text(open), codes)
	}
	done, codes := MarkdownDoc("hi\n\n```go\nfmt.Println(1)\nx := 2\n```\n", 60, StBase)
	if len(codes) != 1 || !codes[0].Closed || codes[0].Text != "fmt.Println(1)\nx := 2" {
		t.Fatalf("codes: %+v", codes)
	}
	hdr := text(done[codes[0].Line : codes[0].Line+1])
	if !strings.Contains(hdr, "╭─ go") || !strings.HasSuffix(strings.TrimSpace(hdr), CopyLabel) {
		t.Fatalf("header %q", hdr)
	}
	if w := done[codes[0].Line].Width(); w != 60 {
		t.Fatalf("header should span the width, got %d", w)
	}
}

func TestNestedAndNumberedLists(t *testing.T) {
	out := text(Markdown("1. one\n   - sub\n     - subsub\n1. two\n\n```\nx\n```\n1. three", 60, StBase))
	for _, want := range []string{"1. one", "  ◦ sub", "    ▪ subsub", "2. two", "3. three"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestSetextRefLinksAndHTML(t *testing.T) {
	out := text(Markdown("Title\n=====\nsee [docs][d] and <b>bold</b><br>x\n\n[d]: https://e.com", 80, StBase))
	if strings.Contains(out, "=====") || !strings.Contains(out, "Title") {
		t.Errorf("setext: %s", out)
	}
	if !strings.Contains(out, "docs (https://e.com)") {
		t.Errorf("ref link: %s", out)
	}
	if strings.Contains(out, "<b>") || strings.Contains(out, "[d]:") || !strings.Contains(out, "bold x") {
		t.Errorf("html/refdef: %s", out)
	}
	code := text(Markdown("use `<b>` here", 80, StBase))
	if !strings.Contains(code, "<b>") {
		t.Errorf("html inside code span stripped: %s", code)
	}
}

func TestHighlightLanguages(t *testing.T) {
	cases := []struct{ lang, src, word string }{
		{"go", "func main() { return nil }", "func"},
		{"rust", "let mut x = 5;", "let"},
		{"python", "def f(): pass", "def"},
		{"ts", "const x: string = 'a'", "const"},
		{"bash", "if [ -f x ]; then echo hi; fi", "then"},
		{"sql", "SELECT * FROM t", "SELECT"},
		{"c", "int main(void) { return 0; }", "return"},
	}
	for _, c := range cases {
		st, ok := styleOf(highlight(c.src, c.lang), c.word)
		if !ok || st != stKeyword {
			t.Errorf("%s: %q not a keyword", c.lang, c.word)
		}
	}
	if st, _ := styleOf(highlight(`  "name": "x",`, "json"), `"name"`); st != stKey {
		t.Errorf("json key style")
	}
	if st, _ := styleOf(highlight("# hi", "yaml"), "# hi"); st != stComment {
		t.Errorf("yaml comment")
	}
	if st, _ := styleOf(highlight("x = 1 # c", "python"), "# c"); st != stComment {
		t.Errorf("python comment")
	}
	// Go has no # comments.
	if _, ok := styleOf(highlight("a # b", "go"), "# b"); ok {
		t.Errorf("go treated # as comment")
	}
	if sp := highlight("+added", "diff"); sp[0].St != StGreen {
		t.Errorf("diff add")
	}
	sp, st := highlightLine("/* open", "go", 0)
	if st != 1 || sp[0].St != stComment {
		t.Errorf("block comment open")
	}
	sp, st = highlightLine("still */ x", "go", 1)
	if st != 0 || sp[0].Text != "still */" {
		t.Errorf("block comment close: %+v", sp)
	}
	if st, _ := styleOf(highlight(`<div class="a">`, "html"), "class"); st != stType {
		t.Errorf("html attr")
	}
}

func TestMermaidFlowchart(t *testing.T) {
	out := text(Mermaid("graph TD\n  A[Start] --> B{Ok?}\n  B -->|yes| C[Done]\n  B -->|no| A"))
	for _, want := range []string{"Start", "Ok?", "Done", "▼", "yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// Layers: Start above Ok? above Done.
	if !(strings.Index(out, "Start") < strings.Index(out, "Ok?") && strings.Index(out, "Ok?") < strings.Index(out, "Done")) {
		t.Errorf("bad layering:\n%s", out)
	}
	lr := text(Mermaid("flowchart LR\n  a --> b & c"))
	first := strings.Split(lr, "\n")
	if !strings.Contains(lr, "▶") || len(first) > 12 {
		t.Errorf("LR:\n%s", lr)
	}
	if Mermaid("pie title x\n  \"a\": 1") != nil {
		t.Errorf("unknown diagram should return nil")
	}
}

func TestMermaidSequence(t *testing.T) {
	out := text(Mermaid("sequenceDiagram\n  participant A as Alice\n  A->>Bob: hello\n  Bob-->>A: hi back"))
	for _, want := range []string{"Alice", "Bob", "hello", "hi back", "▶", "◀"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestMermaidInMarkdown(t *testing.T) {
	out := text(Markdown("```mermaid\ngraph LR\n  x --> y\n```", 80, StBase))
	if strings.Contains(out, "-->") || !strings.Contains(out, "▶") {
		t.Errorf("mermaid not drawn:\n%s", out)
	}
}
