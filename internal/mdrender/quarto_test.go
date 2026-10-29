package mdrender

import (
	"strings"
	"testing"
)

// TestCalloutFenceMatchesAlert checks Quarto's callout fence and GitHub's
// alert render the same markup (WL-REQ-1871).
func TestCalloutFenceMatchesAlert(t *testing.T) {
	fence := Body(ProjectKeys{}, "::: {.callout-note}\nMind the gap.\n:::\n")
	alert := Body(ProjectKeys{}, "> [!NOTE]\n> Mind the gap.\n")
	if fence != alert {
		t.Fatalf("fence:\n%s\nalert:\n%s", fence, alert)
	}
	if !strings.Contains(string(fence), `<aside class="callout callout-note">`) {
		t.Fatalf("no callout markup: %s", fence)
	}
	if doc := DocBody(ProjectKeys{}, "::: {.callout-note}\nMind the gap.\n:::\n"); doc != fence {
		t.Fatalf("doc flavour: %s", doc)
	}
}

func TestCalloutFenceNested(t *testing.T) {
	out := renderTaskRaw(t, "::: {.callout-tip}\n- a\n- b\n\nAfter.\n:::\n\nOutside.\n")
	want := "<aside class=\"callout callout-tip\">\n<p class=\"callout-title\">Tip</p>\n<ul>\n<li>a</li>\n<li>b</li>\n</ul>\n<p>After.</p>\n</aside>\n<p>Outside.</p>\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestBraceMermaid(t *testing.T) {
	out := renderTaskRaw(t, "```{mermaid}\ngraph TD;\nA-->B;\n```")
	if !strings.Contains(out, `<pre class="mermaid">`) {
		t.Fatalf("```{mermaid} did not render as pre.mermaid: %s", out)
	}
}

func TestCalloutFenceNestedFences(t *testing.T) {
	out := renderTaskRaw(t, ":::: {.callout-note}\n::: {.callout-tip}\nIn.\n:::\nOut.\n::::\n")
	want := "<aside class=\"callout callout-note\">\n<p class=\"callout-title\">Note</p>\n<aside class=\"callout callout-tip\">\n<p class=\"callout-title\">Tip</p>\n<p>In.</p>\n</aside>\n<p>Out.</p>\n</aside>\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}
