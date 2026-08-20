package cli

import (
	"strings"
	"testing"
)

func TestRenderMarkdownNonTerminalIsRaw(t *testing.T) {
	var b strings.Builder
	Markdown(&b, "# Heading\n\n- one\n- two\n")
	if got, want := b.String(), "# Heading\n\n- one\n- two\n"; got != want {
		t.Fatalf("non-terminal writer should get raw markdown:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRenderMarkdownEmptyWritesNothing(t *testing.T) {
	var b strings.Builder
	Markdown(&b, "")
	if b.String() != "" {
		t.Fatalf("empty body should write nothing, got %q", b.String())
	}
}

func TestRenderMarkdownRawEndsInExactlyOneNewline(t *testing.T) {
	for _, body := range []string{"body", "body\n", "body\n\n\n"} {
		var b strings.Builder
		Markdown(&b, body)
		if got := b.String(); got != "body\n" {
			t.Fatalf("body %q: got %q, want %q", body, got, "body\n")
		}
	}
}

func TestRenderStyledFormatsMarkdown(t *testing.T) {
	out := renderStyled("# Heading\n\ntext\n", 80)
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected ANSI styling in output:\n%q", out)
	}
	if !strings.Contains(out, "Heading") || !strings.Contains(out, "text") {
		t.Fatalf("rendered output lost its content:\n%q", out)
	}
}

func TestRenderStyledWrapsToWidth(t *testing.T) {
	body := strings.Repeat("word ", 60)
	for _, line := range strings.Split(renderStyled(body, 40), "\n") {
		if len(line) > 40 {
			t.Fatalf("line exceeds wrap width 40 (%d): %q", len(line), line)
		}
	}
}

func TestMarkdownWidthClamps(t *testing.T) {
	tests := []struct {
		name string
		term int
		want int
	}{
		{"unknown falls back", 0, defaultMarkdownWidth},
		{"narrow clamps up", 10, minMarkdownWidth},
		{"wide clamps down", 300, maxMarkdownWidth},
		{"typical passes through", 90, 90},
	}
	for _, tc := range tests {
		if got := clampWidth(tc.term); got != tc.want {
			t.Errorf("%s: clampWidth(%d) = %d, want %d", tc.name, tc.term, got, tc.want)
		}
	}
}
