// The document-reference redirect and the doc page's linkified rendering
// (WL-301).

package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestDocRefRedirect resolves the ref grammar's forms to a 302 at the
// document's page, tier-2 style for a foreign-key shorthand, and answers 404
// for what nothing resolves.
func TestDocRefRedirect(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 45, Slug: "per-project-workflows",
		Body: "---\nstatus: draft\n---\n# Spec 45 — Workflows\n\n## 1. One {#sec-1}\n\nText.\n",
	})

	for _, ref := range []string{
		"per-project-workflows",     // slug
		"045-per-project-workflows", // number form
		"45",                        // bare number
		"WL-SPEC-45",                // shorthand, resolved via the project key
		"docs/specs/045-per-project-workflows.md", // corpus path
	} {
		rr := doReq(t, h, "GET", "/docs/ref/"+ref, "", nil)
		if rr.Code != http.StatusFound {
			t.Fatalf("ref %q status = %d, want 302; body %s", ref, rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("Location"); got != "/docs/WL-SPEC-45" {
			t.Fatalf("ref %q Location = %q, want %q", ref, got, "/docs/WL-SPEC-45")
		}
	}

	for _, ref := range []string{"no-such-doc", "99", "ZZ-SPEC-1"} {
		if rr := doReq(t, h, "GET", "/docs/ref/"+ref, "", nil); rr.Code != http.StatusNotFound {
			t.Fatalf("ref %q status = %d, want 404", ref, rr.Code)
		}
	}
}

// TestDocPageLinksAndStripsFrontmatter pins the page half of WL-301: the
// rendered body carries no frontmatter and its plain-text references are
// links; a resolved relation link carries its #fragment.
func TestDocPageLinksAndStripsFrontmatter(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 4, Slug: "004-backbone",
		Body: "---\nstatus: draft\n---\n# Spec 4 — Backbone\n\n## 2. Two {#sec-2}\n\nText.\n",
	})
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 9, Slug: "009-amender",
		Body: "---\nstatus: draft\nrequires:\n- 004-backbone\namends:\n  \"#sec-1\":\n  - 004-backbone#sec-2\n---\n# Spec 9 — Amender\n\n## 1. One {#sec-1}\n\nPer 004 §2, and spec 004 §2 again.\n",
	})

	page := doReq(t, h, "GET", "/docs/WL-SPEC-9", "", nil).Body.String()
	if strings.Contains(page, "status: draft") {
		t.Errorf("frontmatter leaked into the rendered body:\n%s", page)
	}
	// The link carries ?p=WL: a bare corpus number is on the referring
	// project's sequence, so the redirect resolves it there (WL-723).
	if !strings.Contains(page, `href="/docs/ref/004?p=WL#sec-2"`) {
		t.Errorf("body reference not autolinked to its project:\n%s", page)
	}
	if !strings.Contains(page, "/docs/ref/004-backbone#sec-2") {
		t.Errorf("relation link carries no #fragment:\n%s", page)
	}
}

// TestRefShortcut pins the root-level shortcut (WL-721): a bare task id or
// document reference 302s to its page, a literal route still beats the
// wildcard, and an unresolvable ref is a 404.
func TestRefShortcut(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	created := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Shortcut me", "kind": "feature", "priority": "medium",
	})
	taskID, _ := created["id"].(string)
	if taskID == "" {
		t.Fatalf("created task has no id: %v", created)
	}
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 45, Slug: "per-project-workflows",
		Body: "---\nstatus: draft\n---\n# Spec 45 — Workflows\n\n## 1. One {#sec-1}\n\nText.\n",
	})

	for _, tc := range []struct{ ref, want string }{
		{taskID, "/tasks/" + taskID},
		{"WL-SPEC-45", "/docs/WL-SPEC-45"},
		{"per-project-workflows", "/docs/WL-SPEC-45"},
		{"45", "/docs/WL-SPEC-45"},
	} {
		rr := doReq(t, h, "GET", "/"+tc.ref, "", nil)
		if rr.Code != http.StatusFound {
			t.Fatalf("ref %q status = %d, want 302; body %s", tc.ref, rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("Location"); got != tc.want {
			t.Fatalf("ref %q Location = %q, want %q", tc.ref, got, tc.want)
		}
	}

	// A literal route is more specific than /{ref} and keeps answering with
	// its page; an unresolvable ref is the only thing the shortcut 404s.
	if rr := doReq(t, h, "GET", "/docs", "", nil); rr.Code != http.StatusOK {
		t.Fatalf("/docs status = %d, want 200 (the wildcard must not shadow it)", rr.Code)
	}
	if rr := doReq(t, h, "GET", "/no-such-ref", "", nil); rr.Code != http.StatusNotFound {
		t.Fatalf("/no-such-ref status = %d, want 404", rr.Code)
	}
}

// TestDocRefHomeProjectDisambiguates pins WL-723: two projects each hold a
// spec 29, so /docs/ref/029 alone is ambiguous, and ?p=<KEY> resolves it
// against the project whose corpus sequence the number is on.
func TestDocRefHomeProjectDisambiguates(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createProject(t, st, "other")
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 29, Slug: "029-here",
		Body: "# Spec 29 — Here\n",
	})
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "other", Kind: "spec", Number: 29, Slug: "029-there",
		Body: "# Spec 29 — There\n",
	})

	if rr := doReq(t, h, "GET", "/docs/ref/029", "", nil); rr.Code != http.StatusNotFound {
		t.Fatalf("unscoped ref status = %d, want 404 (ambiguous)", rr.Code)
	}
	for key, want := range map[string]string{"WL": "/docs/WL-SPEC-29", "OTHER": "/docs/OTHER-SPEC-29"} {
		rr := doReq(t, h, "GET", "/docs/ref/029?p="+key, "", nil)
		if rr.Code != http.StatusFound || rr.Header().Get("Location") != want {
			t.Errorf("ref 029?p=%s = %d %q, want 302 %q", key, rr.Code, rr.Header().Get("Location"), want)
		}
	}
}

// TestRefShortcutBareNumberAcrossProjects pins the seam between WL-721 and
// WL-723: the root shortcut delegates to docRefRedirect, which scopes a bare
// corpus number to the ?p=<KEY> a referring body carried. A number typed at
// the root carries no referrer, so it gets the org-wide resolution — a 404
// naming the candidates rather than an arbitrary pick — while the same number
// with an explicit ?p= resolves inside that project.
func TestRefShortcutBareNumberAcrossProjects(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createProject(t, st, "other")
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 29, Slug: "029-here",
		Body: "# Spec 29 — Here\n",
	})
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "other", Kind: "spec", Number: 29, Slug: "029-there",
		Body: "# Spec 29 — There\n",
	})

	// Rootless and ambiguous: 404, and the body names both candidates so the
	// reader can pick. Silently landing on one of the two would be the bug.
	rr := doReq(t, h, "GET", "/029", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("/029 status = %d, want 404 (ambiguous across projects)", rr.Code)
	}
	// Candidates are named by citable id (025 §14.3): the project key is
	// exactly what a bare number does not carry, so it is what the reader
	// needs back.
	for _, want := range []string{"WL-SPEC-29", "OTHER-SPEC-29"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("/029 body does not name candidate %s: %s", want, rr.Body.String())
		}
	}

	// The same number with an explicit home resolves there, so the shortcut
	// carries the query string through to the resolver rather than dropping it.
	for key, want := range map[string]string{"WL": "/docs/WL-SPEC-29", "OTHER": "/docs/OTHER-SPEC-29"} {
		rr := doReq(t, h, "GET", "/029?p="+key, "", nil)
		if rr.Code != http.StatusFound || rr.Header().Get("Location") != want {
			t.Errorf("/029?p=%s = %d %q, want 302 %q", key, rr.Code, rr.Header().Get("Location"), want)
		}
	}
}
