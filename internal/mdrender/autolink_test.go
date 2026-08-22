package mdrender

import (
	"strings"
	"testing"
)

// TestDocRefAutolink covers WL-301: the three reference spellings become
// links to the resolving redirect, references inside code spans and explicit
// links are untouched, and the fragment rides the href.
func TestDocRefAutolink(t *testing.T) {
	got := string(Body("See WL-SPEC-42#sec-10 and spec 042 §10 plus 025 §14.3.\nADR 048 too, and Spec 25."))
	for _, want := range []string{
		`<a href="/docs/ref/WL-SPEC-42#sec-10" rel="nofollow">WL-SPEC-42#sec-10</a>`,
		`<a href="/docs/ref/042#sec-10" rel="nofollow">spec 042 §10</a>`,
		`<a href="/docs/ref/025#sec-14.3" rel="nofollow">025 §14.3</a>`,
		`<a href="/docs/ref/048" rel="nofollow">ADR 048</a>`,
		`<a href="/docs/ref/25" rel="nofollow">Spec 25</a>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
}

// TestDocRefAutolinkLeavesCodeAndLinks pins the skip contexts.
func TestDocRefAutolinkLeavesCodeAndLinks(t *testing.T) {
	got := string(Body("Keep `WL-SPEC-9` and [text](/docs/3) with spec 5 §1 inside: [spec 7 §2](/x).\n\n```\nspec 042 §10\n```"))
	if strings.Contains(got, "/docs/ref/9") {
		t.Errorf("linkified inside a code span:\n%s", got)
	}
	if strings.Contains(got, "/docs/ref/042") {
		t.Errorf("linkified inside a code fence:\n%s", got)
	}
	if strings.Contains(got, `href="/docs/ref/7`) {
		t.Errorf("linkified inside an existing link's text:\n%s", got)
	}
	if !strings.Contains(got, `/docs/ref/5#sec-1`) {
		t.Errorf("plain-text ref before the link was not linkified:\n%s", got)
	}
}

// TestDocBodyStripsFrontmatter pins WL-301's other half: the doc flavour
// drops the leading frontmatter fence, which the page shows structurally.
func TestDocBodyStripsFrontmatter(t *testing.T) {
	got := string(DocBody("---\nstatus: draft\nrequires:\n- 004-execution-backbone.md\n---\n# Title\n\nBody text."))
	if strings.Contains(got, "status: draft") || strings.Contains(got, "requires") {
		t.Errorf("frontmatter leaked into the rendered body:\n%s", got)
	}
	if !strings.Contains(got, "Body text.") || !strings.Contains(got, "Title") {
		t.Errorf("body content lost:\n%s", got)
	}
	// A body with no frontmatter is untouched, and a task body keeps its
	// leading thematic break.
	if got := string(DocBody("# Plain\n\ntext")); !strings.Contains(got, "Plain") {
		t.Errorf("plain body mangled: %s", got)
	}
}
