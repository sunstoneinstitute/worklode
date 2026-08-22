// The document-reference redirect and the doc page's linkified rendering
// (WL-301).

package api_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestDocRefRedirect resolves the ref grammar's forms to a 302 at the
// document's page, tier-2 style for a foreign-key shorthand, and answers 404
// for what nothing resolves.
func TestDocRefRedirect(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	d := createDocViaAPI(t, h, token, model.CreateDocInput{
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
		if got := rr.Header().Get("Location"); got != docPageURLForTest(d.ID) {
			t.Fatalf("ref %q Location = %q, want %q", ref, got, docPageURLForTest(d.ID))
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
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	base := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 4, Slug: "004-backbone",
		Body: "---\nstatus: draft\n---\n# Spec 4 — Backbone\n\n## 2. Two {#sec-2}\n\nText.\n",
	})
	amending := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 9, Slug: "009-amender",
		Body: "---\nstatus: draft\nrequires:\n- 004-backbone\namends:\n  \"#sec-1\":\n  - 004-backbone#sec-2\n---\n# Spec 9 — Amender\n\n## 1. One {#sec-1}\n\nPer 004 §2, and spec 004 §2 again.\n",
	})

	page := doReq(t, h, "GET", docPageURLForTest(amending.ID), "", nil).Body.String()
	if strings.Contains(page, "status: draft") {
		t.Errorf("frontmatter leaked into the rendered body:\n%s", page)
	}
	if !strings.Contains(page, `href="/docs/ref/004#sec-2"`) {
		t.Errorf("body reference not autolinked:\n%s", page)
	}
	if !strings.Contains(page, docPageURLForTest(base.ID)+"#sec-2") {
		t.Errorf("relation link carries no #fragment:\n%s", page)
	}
}

// docPageURLForTest mirrors api's docPageURL for assertions from the test
// package.
func docPageURLForTest(id int64) string {
	return "/docs/" + strconv.FormatInt(id, 10)
}
