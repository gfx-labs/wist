package editor

import (
	"strings"
	"testing"
)

// feed types keys into a vim editor: \x1b is esc, \x12 is ctrl+r, and
// other keys in insert mode are typed text.
func feed(e *Editor, keys string) {
	for _, r := range keys {
		switch {
		case r == '\x1b':
			e.VimEscape()
		case r == '\x12':
			e.VimRedo()
		case e.Mode() == ModeInsert:
			e.Insert(string(r))
		default:
			e.VimKey(r)
		}
	}
}

// TestVim checks commands on a buffer written with | before the cursor's
// character.
func TestVim(t *testing.T) {
	for _, tc := range []struct{ start, keys, want string }{
		{"ab|c", "i\x1b", "a|bc"},
		{"|foo bar baz", "w", "foo |bar baz"},
		{"|foo.bar baz", "w", "foo|.bar baz"},
		{"|foo.bar baz", "W", "foo.bar |baz"},
		{"foo bar |baz", "2b", "|foo bar baz"},
		{"|foo bar", "e", "fo|o bar"},
		{"foo ba|r", "ge", "fo|o bar"},
		{"|one\n\ntwo", "w", "one\n|\ntwo"},
		{"  ab|c", "0", "|  abc"},
		{"  ab|c", "^", "  |abc"},
		{"|abc", "$", "ab|c"},
		{"|a,b,c", "2f,", "a,b|,c"},
		{"|a,b,c", "t,;", "a,|b,c"},
		{"a,b,|c", "F,,", "a,b|,c"},
		{"|foo bar baz", "dw", "|bar baz"},
		{"|foo bar baz", "d2w", "|baz"},
		{"foo |bar\nbaz", "dw", "foo| \nbaz"},
		{"|foo bar", "cwxy\x1b", "x|y bar"},
		{"foo b|ar baz", "diw", "foo | baz"},
		{"foo b|ar baz", "daw", "foo |baz"},
		{"say \"hi |there\" ok", "ci\"yo\x1b", "say \"y|o\" ok"},
		{"f(a, (|b), c)", "di(", "f(a, (|), c)"},
		{"f(a, (|b), c)", "d2i(", "f(|)"},
		{"f(a, |b)", "da)", "|f"},
		{"one\nt|wo\nthree", "dd", "one\n|three"},
		{"one\ntwo\nthr|ee", "dd", "one\n|two"},
		{"o|ne\ntwo\nthree", "2dd", "|three"},
		{"one\nt|wo\nthree", "dk", "|three"},
		{"one\nt|wo", "ccx\x1b", "one\n|x"},
		{"|one\ntwo", "yyp", "one\n|one\ntwo"},
		{"one\n|two", "yyP", "one\n|two\ntwo"},
		{"one\n|two", "ddp", "one\n|two"},
		{"|one\ntwo", "ddp", "two\n|one"},
		{"|ab", "xp", "b|a"},
		{"a|bcd", "2x", "a|d"},
		{"ab|c", "X", "a|c"},
		{"a|bcd", "D", "|a"},
		{"a|bcd", "Cx\x1b", "a|x"},
		{"|abc", "3rx", "xx|x"},
		{"|abC", "3~", "AB|c"},
		{"a|b\n   cd", "J", "ab| cd"},
		{"a|b", "ox\x1b", "ab\n|x"},
		{"a|b", "Ox\x1b", "|x\nab"},
		{"|ab", "Ax\x1b", "ab|x"},
		{"ab|c", "Ix\x1b", "|xabc"},
		{"a|bc", "ax\x1b", "ab|xc"},
		{"|one two three", "dw.", "|three"},
		{"|ab", "ix\x1bl.", "x|xab"},
		{"|foo bar", "dwu", "|foo bar"},
		{"|foo bar", "dwu\x12", "|bar"},
		{"|ab", "ixy\x1bu", "|ab"},
		{"|foo bar baz", "vey", "|foo bar baz"},
		{"|foo bar baz", "vewd", "|ar baz"},
		{"foo b|ar baz", "viwcx\x1b", "foo |x baz"},
		{"one\nt|wo\nthree", "Vjd", "|one"},
		{"|foo bar", "yiwwviwp", "foo fo|o"},
		{"|foo bar", "veU", "|FOO bar"},
		{"|abc", "d", "|abc"},
		{"|abc", "dq", "|abc"},
	} {
		e := &Editor{}
		e.SetVim(true)
		e.Set(strings.Replace(tc.start, "|", "", 1))
		e.VimEscape()
		e.Cur = len([]rune(tc.start[:strings.Index(tc.start, "|")]))
		feed(e, tc.keys)
		got := string(e.Buf[:e.Cur]) + "|" + string(e.Buf[e.Cur:])
		if got != tc.want {
			t.Errorf("%q + %q = %q, want %q", tc.start, tc.keys, got, tc.want)
		}
	}
}

// TestVimClear checks that a submitted prompt leaves the next one in
// insert mode.
func TestVimClear(t *testing.T) {
	e := &Editor{}
	e.SetVim(true)
	feed(e, "hi\x1b")
	e.Clear()
	if e.Mode() != ModeInsert {
		t.Fatalf("mode %v after Clear", e.Mode())
	}
}
