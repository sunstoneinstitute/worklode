package mdrender

import (
	"strings"
	"testing"
)

// TestDocRefAutolink covers WL-301: the three reference spellings link to
// the root shortcut /<ref>, a bare number read as a spec on the home
// project's sequence (WL-723), and the fragment rides the href.
func TestDocRefAutolink(t *testing.T) {
	const body = "See WL-SPEC-42#sec-10 and spec 042 §10 plus 025 §14.3.\nADR 048 too, and Spec 25."
	got := string(Body(NewProjectKeys([]string{"WL"}).For("WL"), body))
	for _, want := range []string{
		`<a class="ref-link" href="/WL-SPEC-42#sec-10" rel="nofollow">WL-SPEC-42#sec-10</a>`,
		`<a class="ref-link" href="/WL-SPEC-42#sec-10" rel="nofollow">spec 042 §10</a>`,
		`<a class="ref-link" href="/WL-SPEC-25#sec-14.3" rel="nofollow">025 §14.3</a>`,
		`<a class="ref-link" href="/WL-SPEC-48" rel="nofollow">ADR 048</a>`,
		`<a class="ref-link" href="/WL-SPEC-25" rel="nofollow">Spec 25</a>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
	// Without a home project a bare number names no sequence: it stays text.
	if got := string(Body(ProjectKeys{}, body)); strings.Count(got, "<a ") != 1 {
		t.Errorf("want only the shorthand linked without a home:\n%s", got)
	}
}

// TestRuleRefAutolink covers S20: a rule ref links to the resolving
// redirect /rules/<ref>.
func TestRuleRefAutolink(t *testing.T) {
	got := string(Body(ProjectKeys{}, "see WL-RULE-12 and WL-REQ-13."))
	for _, want := range []string{
		`<a class="ref-link" href="/rules/WL-RULE-12" rel="nofollow">WL-RULE-12</a>`,
		`<a class="ref-link" href="/rules/WL-REQ-13" rel="nofollow">WL-REQ-13</a>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
}

// TestEntityRefAutolink: a milestone or deliverable ref links to the root
// shortcut, which redirects to its page.
func TestEntityRefAutolink(t *testing.T) {
	got := string(Body(ProjectKeys{}, "see WL-MILE-2 and WL-DEL-4."))
	for _, want := range []string{
		`<a class="ref-link" href="/WL-MILE-2" rel="nofollow">WL-MILE-2</a>`,
		`<a class="ref-link" href="/WL-DEL-4" rel="nofollow">WL-DEL-4</a>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
}

// TestDocRefAutolinkLeavesCodeAndLinks pins the skip contexts.
// TestRuleRefAutolinkInPage covers WL-REQ-1633: on a spec page, a ref to a
// rule the spec arranges links to its section in the same page; any other
// rule ref keeps the redirect.
func TestRuleRefAutolinkInPage(t *testing.T) {
	keys := ProjectKeys{}.InPage(map[string]string{"WL-REQ-13": "sec-4"})
	got := string(DocBody(keys, "see WL-REQ-13 and WL-RULE-12."))
	for _, want := range []string{
		`<a class="ref-link" href="#sec-4" rel="nofollow">WL-REQ-13</a>`,
		`<a class="ref-link" href="/rules/WL-RULE-12" rel="nofollow">WL-RULE-12</a>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
	if keys.fingerprint == (ProjectKeys{}).fingerprint {
		t.Error("in-page anchors must change the cache fingerprint")
	}
}

func TestDocRefAutolinkLeavesCodeAndLinks(t *testing.T) {
	got := string(Body(NewProjectKeys([]string{"WL"}).For("WL"), "Keep `WL-SPEC-9` and [text](/docs/3) with spec 5 §1 inside: [spec 7 §2](/x).\n\n```\nspec 042 §10\n```"))
	if strings.Contains(got, "/WL-SPEC-9") {
		t.Errorf("linkified inside a code span:\n%s", got)
	}
	if strings.Contains(got, "/WL-SPEC-42") {
		t.Errorf("linkified inside a code fence:\n%s", got)
	}
	if strings.Contains(got, `href="/WL-SPEC-7`) {
		t.Errorf("linkified inside an existing link's text:\n%s", got)
	}
	if !strings.Contains(got, `/WL-SPEC-5#sec-1`) {
		t.Errorf("plain-text ref before the link was not linkified:\n%s", got)
	}
}

// liveKeys is the project-key set the task-id tests render under: two real
// keys, and deliberately none of the acronyms below.
var liveKeys = NewProjectKeys([]string{"WL", "COW"})

// TestTaskRefAutolink covers WL-305: a bare task id whose key is a live
// project key links to the task's page.
func TestTaskRefAutolink(t *testing.T) {
	got := string(Body(liveKeys, "Follows WL-129, blocked by COW-7 and WL-1."))
	for _, want := range []string{
		`<a class="ref-link" href="/tasks/WL-129" rel="nofollow">WL-129</a>`,
		`<a class="ref-link" href="/tasks/COW-7" rel="nofollow">COW-7</a>`,
		`<a class="ref-link" href="/tasks/WL-1" rel="nofollow">WL-1</a>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
}

// TestTaskRefAutolinkRejectsAcronyms is the reason the key set exists: every
// token here has a task id's exact shape and none of them is one.
func TestTaskRefAutolinkRejectsAcronyms(t *testing.T) {
	body := "Encoded UTF-8, hashed SHA-256, dated ISO-8601, over HTTP-2 with AES-256 and RFC-7519."
	got := string(Body(liveKeys, body))
	if strings.Contains(got, "/tasks/") {
		t.Errorf("an acronym was linked as a task id:\n%s", got)
	}
	// A key-shaped token that is not a live key stays text too, which is what
	// separates this from a hand-maintained denylist.
	if got := string(Body(liveKeys, "See ZZQ-42 and MOO-1.")); strings.Contains(got, "/tasks/") {
		t.Errorf("an unknown project key was linked:\n%s", got)
	}
	// And with no key set at all — a caller whose store read failed — nothing
	// links.
	if got := string(Body(ProjectKeys{}, "Follows WL-129.")); strings.Contains(got, "/tasks/") {
		t.Errorf("linked with an empty key set:\n%s", got)
	}
}

// TestTaskRefAutolinkSkipContexts pins the same skip contexts the document
// refs use, plus the two hyphen cases: a branch name is one identifier, and a
// document shorthand must stay a document link.
func TestTaskRefAutolinkSkipContexts(t *testing.T) {
	got := string(Body(liveKeys, "Keep `WL-11` and [COW-12](/x), branch WL-13-fix-the-thing, doc WL-SPEC-14.\n\n```\nWL-15\n```\n\nBut WL-16 links."))
	for _, unwanted := range []string{
		"/tasks/WL-11",  // code span
		"/tasks/COW-12", // existing link's text
		"/tasks/WL-13",  // branch name
		"/tasks/WL-15",  // code fence
		"/tasks/SPEC-14",
	} {
		if strings.Contains(got, unwanted) {
			t.Errorf("linked %s, which is in a skip context:\n%s", unwanted, got)
		}
	}
	if !strings.Contains(got, `<a class="ref-link" href="/tasks/WL-16" rel="nofollow">WL-16</a>`) {
		t.Errorf("plain-text task id was not linked:\n%s", got)
	}
	// The document shorthand still wins its own span.
	if !strings.Contains(got, `href="/WL-SPEC-14"`) {
		t.Errorf("document shorthand lost its link:\n%s", got)
	}
}

// TestProjectKeysFingerprintIsOrderIndependent: the fingerprint is the cache
// key's share of the key set, so it must not change when ListProjects returns
// the same projects in another order — that would evict the whole render
// cache for nothing.
func TestProjectKeysFingerprintIsOrderIndependent(t *testing.T) {
	a := NewProjectKeys([]string{"WL", "COW", "WL"})
	b := NewProjectKeys([]string{"COW", "WL"})
	if a.fingerprint != b.fingerprint {
		t.Errorf("fingerprints differ for the same set: %q vs %q", a.fingerprint, b.fingerprint)
	}
	if c := NewProjectKeys([]string{"WL"}); c.fingerprint == a.fingerprint {
		t.Error("a different key set produced the same fingerprint")
	}
	if got := (ProjectKeys{}).fingerprint; got != "" {
		t.Errorf("empty set fingerprint = %q, want the zero value's", got)
	}
	if NewProjectKeys(nil).fingerprint != "" || NewProjectKeys([]string{""}).fingerprint != "" {
		t.Error("an empty key set must fingerprint like the zero value")
	}
}
