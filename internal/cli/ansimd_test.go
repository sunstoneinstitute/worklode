package cli

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

var ansiCodes = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plain is what the reader actually sees: the rendered line with its escape
// codes removed.
func plain(s string) string { return ansiCodes.ReplaceAllString(s, "") }

func TestRenderStyledHeadingsDropTheirHashes(t *testing.T) {
	out := renderStyled("# Top\n\n### Third\n", 80, linker{})
	if got := plain(out); got != "Top\n\nThird" {
		t.Fatalf("headings not rendered as text: %q", got)
	}
	if !strings.Contains(out, sgrBold+sgrUnderline+"Top") {
		t.Fatalf("h1 should be bold and underlined: %q", out)
	}
	if !strings.Contains(out, sgrBold+"Third") {
		t.Fatalf("h3 not bold: %q", out)
	}
}

func TestRenderStyledNormalisesListMarkers(t *testing.T) {
	out := plain(renderStyled("- one\n* two\n+ three\n\n2. second\n3) third\n", 80, linker{}))
	want := "• one\n• two\n• three\n\n2. second\n3) third"
	if out != want {
		t.Fatalf("list markers:\ngot:  %q\nwant: %q", out, want)
	}
}

func TestRenderStyledListWrapsWithHangingIndent(t *testing.T) {
	out := plain(renderStyled("- "+strings.Repeat("word ", 20), 30, linker{}))
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected the item to wrap: %q", out)
	}
	if !strings.HasPrefix(lines[0], "• word") {
		t.Fatalf("first line lost its bullet: %q", lines[0])
	}
	for _, line := range lines[1:] {
		if !strings.HasPrefix(line, "  word") {
			t.Fatalf("continuation not aligned under the text: %q", line)
		}
	}
}

func TestRenderStyledWrapsOnVisibleWidth(t *testing.T) {
	// Every word carries escape codes, so a wrapper measuring rendered bytes
	// instead of visible runes would break far too early.
	body := strings.Repeat("`code` ", 20)
	lines := strings.Split(renderStyled(body, 40, linker{}), "\n")
	for i, line := range lines {
		w := utf8.RuneCountInString(plain(line))
		if w > 40 {
			t.Fatalf("visible width %d exceeds 40: %q", w, plain(line))
		}
		// Only the last line may be short; an earlier one that is means the
		// wrapper measured escape codes rather than visible runes.
		if i < len(lines)-1 && w < 35 {
			t.Fatalf("line %d wrapped short at width %d: %q", i, w, plain(line))
		}
	}
}

func TestRenderStyledCodeBlockIsVerbatimAndUnfenced(t *testing.T) {
	out := plain(renderStyled("text\n\n```go\nx := **not bold**\n```\n\nafter\n", 80, linker{}))
	want := "text\n\n  x := **not bold**\n\nafter"
	if out != want {
		t.Fatalf("code block:\ngot:  %q\nwant: %q", out, want)
	}
}

func TestRenderStyledUnterminatedFenceKeepsItsContent(t *testing.T) {
	out := plain(renderStyled("~~~\nstill here\n", 80, linker{}))
	if !strings.Contains(out, "still here") {
		t.Fatalf("unterminated fence lost its content: %q", out)
	}
}

func TestRenderStyledTableRowsPassThroughUntouched(t *testing.T) {
	body := "| a | b |\n| --- | --- |\n| 1 | 2 |\n"
	if got := plain(renderStyled(body, 80, linker{})); got != strings.TrimRight(body, "\n") {
		t.Fatalf("table was reflowed:\ngot:  %q\nwant: %q", got, body)
	}
}

func TestRenderStyledThematicBreakSpansTheWidth(t *testing.T) {
	if got := plain(renderStyled("---\n", 20, linker{})); got != strings.Repeat("─", 20) {
		t.Fatalf("thematic break: %q", got)
	}
}

func TestRenderStyledBlockQuoteIsBarred(t *testing.T) {
	if got := plain(renderStyled("> quoted\n", 80, linker{})); got != "│ quoted" {
		t.Fatalf("block quote: %q", got)
	}
}

func TestRenderStyledBlankLinesSurvive(t *testing.T) {
	if got := plain(renderStyled("a\n\n\nb\n", 80, linker{})); got != "a\n\n\nb" {
		t.Fatalf("blank lines: %q", got)
	}
}

func TestInlineSpans(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		plain string
		style string
	}{
		{"bold stars", "a **b** c", "a b c", sgrBold},
		{"bold underscores", "a __b__ c", "a b c", sgrBold},
		{"italic", "a *b* c", "a b c", sgrItalic},
		{"code", "a `b` c", "a b c", sgrCyan},
		{"strikethrough", "a ~~b~~ c", "a b c", sgrStrike},
		{"link keeps its url", "see [docs](http://x/y)", "see docs http://x/y", sgrUnderline},
		{"image degrades to alt plus url", "![shot](/blob/ab)", "shot /blob/ab", sgrDim},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := renderStyled(tc.in, 80, linker{})
			if got := plain(out); got != tc.plain {
				t.Fatalf("text: got %q, want %q", got, tc.plain)
			}
			if !strings.Contains(out, tc.style) {
				t.Fatalf("expected style %q in %q", tc.style, out)
			}
		})
	}
}

func TestInlineLeavesProseMarkersAlone(t *testing.T) {
	tests := []string{
		"snake_case_name stays whole",
		"2 * 3 * 4 is arithmetic",
		"an unclosed `backtick stays",
		"a [link with no target] stays",
	}
	for _, body := range tests {
		out := renderStyled(body, 80, linker{})
		if plain(out) != body {
			t.Errorf("body %q rendered as %q", body, plain(out))
		}
		if strings.Contains(out, "\x1b[") {
			t.Errorf("body %q should not have been styled: %q", body, out)
		}
	}
}

func TestRenderStyledReflowsHardWrappedParagraphs(t *testing.T) {
	// The source is wrapped at its own width; rendering wider must refill the
	// paragraph rather than leave each source line's remainder as an orphan.
	body := "one two three\nfour five six\nseven eight\n"
	if got := plain(renderStyled(body, 80, linker{})); got != "one two three four five six seven eight" {
		t.Fatalf("paragraph not reflowed: %q", got)
	}
}

func TestRenderStyledBlocksDoNotAbsorbTheNextBlock(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"heading then paragraph", "# Title\ntext\n", "Title\ntext"},
		{"paragraph then table", "text\n| a |\n", "text\n| a |"},
		{"paragraph then list", "text\n- item\n", "text\n• item"},
		{"list item keeps its continuation", "- one\n  two\n", "• one two"},
		{"paragraph then rule", "text\n***\n", "text\n" + strings.Repeat("─", 10)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := plain(renderStyled(tc.body, 10, linker{})); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderStyledFencesFollowCommonMark(t *testing.T) {
	if out := plain(renderStyled("```` ```{mermaid} ```` text\n", 80, linker{})); strings.HasPrefix(out, "  ") || !strings.HasSuffix(out, " text") {
		t.Errorf("a backtick info string with a backtick opened a fence: %q", out)
	}
	out := plain(renderStyled("````\n```\ninner\n````\n\nafter\n", 80, linker{}))
	if want := "  ```\n  inner\n\nafter"; out != want {
		t.Errorf("four-backtick fence:\ngot:  %q\nwant: %q", out, want)
	}
}

func TestRenderStyledLinksReferences(t *testing.T) {
	lk := linker{osc8: true, server: "https://wl.example", key: "WL"}
	out := renderStyled("See WL-7, WL-REQ-12 and OTHER-SPEC-3; not UTF-8 or `WL-9`. [docs](https://x.example/a)\n", 80, lk)
	for _, want := range []string{
		Hyperlink("https://wl.example/WL-7", "WL-7"),
		Hyperlink("https://wl.example/WL-REQ-12", "WL-REQ-12"),
		Hyperlink("https://wl.example/OTHER-SPEC-3", "OTHER-SPEC-3"),
		Hyperlink("https://x.example/a", sgrUnderline+"docs"+sgrReset),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	for _, not := range []string{"/UTF-8", "/WL-9", " https://x.example/a"} {
		if strings.Contains(out, not) {
			t.Errorf("unexpected %q in %q", not, out)
		}
	}
	if strings.Contains(renderStyled("WL-7\n", 80, linker{}), osc8Start) {
		t.Error("zero linker emitted a hyperlink")
	}
}

func TestLinkerTextLinksMetadataLines(t *testing.T) {
	lk := linker{osc8: true, server: "https://wl.example", key: "WL"}
	got := lk.text("  arranged: WL-SPEC-77#sec-2 governs WL-7, not UTF-8\n")
	want := "  arranged: " + Hyperlink("https://wl.example/WL-SPEC-77", "WL-SPEC-77") +
		"#sec-2 governs " + Hyperlink("https://wl.example/WL-7", "WL-7") + ", not UTF-8\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := (linker{}).text("WL-7"); got != "WL-7" {
		t.Fatalf("zero linker changed text: %q", got)
	}
}
