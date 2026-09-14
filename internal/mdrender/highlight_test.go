package mdrender_test

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/mdrender"
)

// render is a task body rendered through the full pipeline, sanitiser included.
func render(t *testing.T, body string) string {
	t.Helper()
	return string(mdrender.Body(mdrender.NewProjectKeys(nil), body))
}

// TestHighlightsEveryRequiredLanguage pins the language list the cockpit
// promises. A fence in any of these must come out coloured: at least one
// Chroma token class, surviving the sanitiser. Losing a lexer to a Chroma
// upgrade is otherwise silent — the block still renders, just grey.
func TestHighlightsEveryRequiredLanguage(t *testing.T) {
	for _, tc := range []struct{ lang, src string }{
		{"sql", "SELECT id FROM task WHERE state = 'ready';"},
		{"turtle", "@prefix wl: <https://example/> .\nwl:a wl:b \"c\" ."},
		{"python", "def f(x: int) -> str:\n    return f\"{x}\"  # note"},
		{"go", "func f() string { return \"x\" } // note"},
		{"rust", "fn f() -> String { String::from(\"x\") } // note"},
		{"terraform", "resource \"aws_s3_bucket\" \"b\" {\n  bucket = \"x\"\n}"},
		{"swift", "func f() -> String { return \"x\" } // note"},
		{"json", "{\"a\": 1, \"b\": [true, null]}"},
		{"yaml", "a: 1\nb:\n  - c  # note"},
		{"toml", "[table]\na = 1 # note"},
		{"tex", "\\section{Title} % note"},
		{"bibtex", "@article{k, title = {T}, year = 2026}"},
		{"xml", "<root a=\"1\"><child/></root>"},
		{"hujson", "{\n  // line\n  /* block */\n  \"a\": 1,\n}"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			out := render(t, "```"+tc.lang+"\n"+tc.src+"\n```")
			if !strings.Contains(out, `<pre class="hl-chroma">`) {
				t.Fatalf("%s: not highlighted, got %s", tc.lang, out)
			}
			if !strings.Contains(out, `<span class="hl-`) {
				t.Fatalf("%s: no token classes survived the sanitiser, got %s", tc.lang, out)
			}
		})
	}
}

// TestHujsonLexesBothCommentForms is why hujson is aliased to JavaScript and
// not to Chroma's JSON lexer: that one has no block-comment rule and takes
// "/* x */" apart into one error token per character.
func TestHujsonLexesBothCommentForms(t *testing.T) {
	out := render(t, "```hujson\n{\n  // line\n  /* block */\n  \"a\": 1,\n}\n```")
	if strings.Contains(out, `class="hl-err"`) {
		t.Errorf("hujson produced error tokens, so the alias is not lexing it:\n%s", out)
	}
	if n := strings.Count(out, `class="hl-c`); n < 2 {
		t.Errorf("want both comment forms tokenised as comments, got %d comment tokens:\n%s", n, out)
	}
}

// TestMermaidFenceIsNotHighlighted guards the one fence the highlighter must
// keep its hands off: mermaid.js finds its diagrams by pre.mermaid, and a
// highlighted block would be pre.hl-chroma full of spans instead.
func TestMermaidFenceIsNotHighlighted(t *testing.T) {
	out := render(t, "```mermaid\ngraph TD;\nA-->B;\n```")
	if !strings.Contains(out, `<pre class="mermaid">`) {
		t.Fatalf("mermaid fence no longer renders as pre.mermaid: %s", out)
	}
}

// TestUnknownLanguageFallsBackToPlain: a fence naming a language Chroma has no
// lexer for, or naming none at all, must render as an ordinary code block
// rather than being guessed at.
func TestUnknownLanguageFallsBackToPlain(t *testing.T) {
	for _, body := range []string{
		"```zzznotalanguage\nplain text\n```",
		"```\nplain text\n```",
	} {
		out := render(t, body)
		if strings.Contains(out, "hl-") {
			t.Errorf("%q was highlighted anyway: %s", body, out)
		}
	}
}

// TestSanitiserKeepsOnlyHighlightClasses: the class attribute stays closed to
// everything the highlighter does not emit. A body that writes its own class
// must still lose it, or the reopening in buildPolicy has become a general
// styling hook.
func TestSanitiserKeepsOnlyHighlightClasses(t *testing.T) {
	out := render(t, `<span class="evil">x</span> <span class="hl-notatoken">y</span> <p class="hl-k">z</p>`)
	for _, unwanted := range []string{"evil", "hl-notatoken"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("sanitiser kept class %q: %s", unwanted, out)
		}
	}
	// p is not one of the three elements the highlight rule names.
	if strings.Contains(out, `<p class="hl-k">`) {
		t.Errorf("sanitiser kept a highlight class on p: %s", out)
	}
}
