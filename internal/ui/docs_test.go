package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// renderDoc renders Doc(v) into a string, failing the test on error.
func renderDoc(t *testing.T, v DocView) string {
	t.Helper()
	var b strings.Builder
	if err := Doc(v).Render(context.Background(), &b); err != nil {
		t.Fatalf("render Doc: %v", err)
	}
	return b.String()
}

// TestDocBodyIsRenderedNotDumped: the page emits the HTML internal/api handed
// it, rather than escaping the markdown source into a <pre>. BodyHTML is the
// one value this package emits unescaped; mdrender's allowlist is what makes
// that safe, and this only pins that the template does emit it.
func TestDocBodyIsRenderedNotDumped(t *testing.T) {
	body := renderDoc(t, DocView{
		Page:     PageProps{Title: "doc"},
		Doc:      model.Doc{ID: 25, Slug: "025-documents", Title: "Documents", Body: "## 3. Anchors {#sec-3}\n"},
		BodyHTML: `<h2 id="sec-3">3. Anchors</h2>`,
	})
	if !strings.Contains(body, `<h2 id="sec-3">3. Anchors</h2>`) {
		t.Fatalf("rendered body missing from the page:\n%s", body)
	}
	// The markdown source must not also be dumped: two copies of the body is
	// what the <pre> this replaced amounted to.
	if strings.Contains(body, "{#sec-3}") {
		t.Fatalf("markdown source still rendered alongside the HTML:\n%s", body)
	}
}

// TestDocSectionsLinkAtTheirAnchors is the payoff for parsing the anchors: the
// Sections table is a table of contents, so each row links at the heading it
// names in the body below.
func TestDocSectionsLinkAtTheirAnchors(t *testing.T) {
	body := renderDoc(t, DocView{
		Page: PageProps{Title: "doc"},
		Doc:  model.Doc{ID: 25, Slug: "025-documents", Title: "Documents"},
		Sections: []model.DocSection{
			{Anchor: "sec-1", Heading: "1. Purpose"},
			{Anchor: "sec-3.2", Heading: "3.2 Numbering"},
		},
		BodyHTML: `<h2 id="sec-1">1. Purpose</h2><h3 id="sec-3.2">3.2 Numbering</h3>`,
	})
	for _, want := range []string{`href="#sec-1"`, `href="#sec-3.2"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("Sections table missing %q:\n%s", want, body)
		}
	}
}
