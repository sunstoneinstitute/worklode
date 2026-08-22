package mdrender_test

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/mdrender"
)

// TestDocBodyKeepsSectionAnchors is the point of the document flavour: the
// corpus writes "## 3.1 Title {#sec-3.1}", and the anchor has to reach the
// page as an id or the Sections table above the body links at nothing.
func TestDocBodyKeepsSectionAnchors(t *testing.T) {
	body := "## 1. Purpose {#sec-1}\n\nprose\n\n" +
		"### 1.1 Detail {#sec-1.1}\n\nmore\n\n" +
		"## Rationale {#sec-rationale}\n\n" +
		"#### 2.1a Amended {#sec-2.1a}\n"
	got := string(mdrender.DocBody(body))
	for _, want := range []string{
		`<h2 id="sec-1">`, `<h3 id="sec-1.1">`,
		`<h2 id="sec-rationale">`, `<h4 id="sec-2.1a">`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	// The anchor is markup, not text: leaving "{#sec-1}" in the heading would
	// mean the parser option never took effect.
	if strings.Contains(got, "{#") {
		t.Fatalf("anchor syntax survived as text:\n%s", got)
	}
}

// TestDocBodyDropsOtherIDs is the other half. buildPolicy drops the global id
// because an attacker-chosen one enables DOM clobbering; the document flavour
// re-admits it on headings only, and only for a value that is a section
// anchor. An id that could name the page's own chrome — or sit on an element
// a form control could be confused with — must still not survive.
func TestDocBodyDropsOtherIDs(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"page landmark", "## Heading {#main-content}"},
		{"page nav", "## Heading {#global-nav}"},
		{"no sec prefix", "## Heading {#introduction}"},
		{"uppercase anchor", "## Heading {#Sec-1}"},
		{"leading dash", "## Heading {#sec--1}"},
		{"empty tail", "## Heading {#sec-}"},
		{"raw heading id", `<h2 id="config">x</h2>`},
		{"div", `<div id="sec-1">x</div>`},
		{"paragraph attribute", "text\n{#sec-1}\n"},
		{"anchor element", `<a id="sec-1" href="#x">t</a>`},
		{"list item", "- item {#sec-1}\n"},
		{"code fence", "``` {#sec-1}\nx\n```\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(mdrender.DocBody(tc.body)); strings.Contains(got, "id=") {
				t.Fatalf("id survived %q:\n%s", tc.body, got)
			}
		})
	}
}

// TestDocBodyDropsOtherAttributes: goldmark's attribute syntax writes whatever
// the author names, not just an id. Everything but the anchor is the
// allowlist's problem, and this is what says the allowlist is still the thing
// deciding.
func TestDocBodyDropsOtherAttributes(t *testing.T) {
	body := "## Heading {#sec-1 .danger onclick=\"alert(1)\" style=\"color:red\"}\n"
	got := string(mdrender.DocBody(body))
	if !strings.Contains(got, `id="sec-1"`) {
		t.Fatalf("anchor lost:\n%s", got)
	}
	for _, bad := range []string{"class=", "onclick", "style=", "alert"} {
		if strings.Contains(got, bad) {
			t.Fatalf("output contains %q:\n%s", bad, got)
		}
	}
}

// TestTaskBodiesHaveNoAnchorSyntax pins that the parser option is scoped to
// the document flavour. A task body has no anchor convention, so "{#sec-1}"
// there is prose that must come back as prose — and must never become an id.
func TestTaskBodiesHaveNoAnchorSyntax(t *testing.T) {
	got := string(mdrender.Body("## Heading {#sec-1}\n"))
	if strings.Contains(got, "id=") {
		t.Fatalf("task body grew an id:\n%s", got)
	}
	if !strings.Contains(got, "{#sec-1}") {
		t.Fatalf("task body lost the literal text:\n%s", got)
	}
}

// TestDocBodyIsSanitisedLikeATaskBody: a document body arrives through the
// doc.write-gated docs API rather than through spec 020's issue import, so it
// is more trusted than a task body. That is not a reason to run a different
// pipeline, and this replays every hostile shape TestHostileBodies pins
// through DocBody to say so.
func TestDocBodyIsSanitisedLikeATaskBody(t *testing.T) {
	for _, tc := range hostileBodies {
		t.Run(tc.name, func(t *testing.T) {
			got := string(mdrender.DocBody(tc.body))
			for _, bad := range tc.absent {
				if strings.Contains(got, bad) {
					t.Fatalf("output contains %q:\n%s", bad, got)
				}
			}
		})
	}
}

// TestDocPolicyIsNotUGCPolicy: the document policy is built from the task
// policy, so buildPolicy's reason for not starting from UGCPolicy has to hold
// here too — an image source still has to be a blob served by this server.
// Doc-body images are 025's problem; widening this is not part of rendering.
func TestDocPolicyIsNotUGCPolicy(t *testing.T) {
	for _, body := range []string{
		`<img src="https://example.com/tracker.png">`,
		"![](https://example.com/tracker.png)",
		`<img src="/not-a-blob/x.png">`,
	} {
		got := string(mdrender.DocBody(body))
		if strings.Contains(got, "src=") || strings.Contains(got, "example.com") {
			t.Fatalf("non-blob image source survived %q:\n%s", body, got)
		}
	}
}

// TestDocBodySafeMarkupSurvives: a spec is long-form prose, which is the whole
// reason the page stopped dumping it in a <pre>.
func TestDocBodySafeMarkupSurvives(t *testing.T) {
	body := "## 1. Heading {#sec-1}\n\n**bold** text\n\n" +
		"| a | b |\n|---|---|\n| 1 | 2 |\n\n" +
		"```go\nfunc main() {}\n```\n\n" +
		"[link](https://example.com) and [section](#sec-1)\n"
	got := string(mdrender.DocBody(body))
	for _, want := range []string{
		`<h2 id="sec-1">`, "<strong>", "<table", "<code",
		`href="https://example.com"`, `href="#sec-1"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}
