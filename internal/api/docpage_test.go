package api_test

// docpage_test.go covers the read-only cockpit doc pages split out of
// docs_test.go to stay under the file-size ceiling (CLAUDE.md, Conventions):
// GET /docs, GET /docs/{ref}, and their version-history siblings.

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestDocsPage(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	acceptedSpec(t, h, token, "proj", "025-documents-in-the-backbone", 25)
	// The plan is the point: since 029 §4 it carries a number, so the index
	// links it by shorthand like every other kind rather than by database id.
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "025-part-2", Body: docPlanBody,
	})
	if plan.Number == 0 {
		t.Fatal("plan created without a number; 029 §4 allocates one")
	}

	rr := doReq(t, h, "GET", "/docs", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	bodyContains(t, body,
		`href="/docs/WL-SPEC-25">WL-SPEC-25</a>`,
		`href="/docs/WL-SPEC-25">Documents in the backbone</a>`,
		`href="/docs/WL-PLAN-1">Documents in the backbone, part 2</a>`,
		"accepted",
		"draft",
	)
}

func TestDocPage(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	spec := acceptedSpec(t, h, token, "proj", "025-documents-in-the-backbone", 25)
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "025-part-2", Body: docPlanBody,
	})

	rr := doReq(t, h, "GET", "/docs/WL-SPEC-25", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	bodyContains(t, body,
		"Documents in the backbone", // title
		"accepted",                  // status chip
		"sec-1",                     // section table
		"Scope",
		"isCoveredBy", // the plan's covers, read backward
		"plan 1",      // the far end's corpus reference
		"025-part-2",  // named by slug, not as "document 42"
		"Model body.", // the body, rendered verbatim in a <pre>
	)
	if strings.Contains(body, "document "+strconv.FormatInt(plan.ID, 10)) {
		t.Errorf("relation names the far end by id rather than by slug:\n%s", body)
	}

	// The far end's corpus reference tells a plan from the spec it covers.
	// The plan's own page is at its shorthand now that it carries a number.
	rr = doReq(t, h, "GET", "/docs/WL-PLAN-1", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("plan page status = %d, body %s", rr.Code, rr.Body.String())
	}
	bodyContains(t, rr.Body.String(),
		"025-documents-in-the-backbone#sec-1", // the covered section, by slug
		">spec 25<",                           // and what kind of document that is
	)

	if rr := doReq(t, h, "GET", "/docs/"+strconv.FormatInt(spec.ID, 10), "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("numeric URL status = %d, want 404", rr.Code)
	}
	if rr := doReq(t, h, "GET", "/docs/025-x", "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("non-numeric id status = %d, want 404", rr.Code)
	}
}

// TestDocPageDegradesWithoutVersions pins WL-345 (I3): a ListDocVersions
// failure must not take down the whole doc page, the same call
// projectKeyByID already makes for its own dependency. Forces the failure by
// dropping doc_versions out from under a live store, the cross-package
// DBForTests seam other packages already use for this (e.g.
// internal/hooks/github_test.go).
func TestDocPageDegradesWithoutVersions(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	acceptedSpec(t, h, token, "proj", "025-documents-in-the-backbone", 25)

	if _, err := st.DBForTests().Exec(`DROP TABLE doc_versions`); err != nil {
		t.Fatalf("drop doc_versions: %v", err)
	}

	rr := doReq(t, h, "GET", "/docs/WL-SPEC-25", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	bodyContains(t, body, "Documents in the backbone")
}

// TestDocVersionPage covers GET /docs/{id}/versions/{n} (025 §4.5): a plan
// stays freely mutable (025 §9), so editing its body once leaves version 1
// superseded and version 2 current, and only the superseded one shows the
// "back to current" banner.
func TestDocVersionPage(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "025-part-2", Body: docPlanBody,
	})
	edited := strings.Replace(docPlanBody, "Do the thing.", "Do it now.", 1)
	if rr := doReq(t, h, "PUT", docPath(plan.ID, "/body"), token, model.UpdateDocBodyInput{Body: edited}); rr.Code != http.StatusOK {
		t.Fatalf("update body status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr := doReq(t, h, "GET", fmt.Sprintf("/docs/versions/%d/2", plan.ID), "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("current version status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	bodyContains(t, body, "Do it now.")
	if strings.Contains(body, "back to current") {
		t.Errorf("current version shows the back-to-current banner:\n%s", body)
	}

	rr = doReq(t, h, "GET", fmt.Sprintf("/docs/versions/%d/1", plan.ID), "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("superseded version status = %d, body %s", rr.Code, rr.Body.String())
	}
	body = rr.Body.String()
	bodyContains(t, body, "back to current", "Do the thing.")
	if strings.Contains(body, "Do it now.") {
		t.Errorf("superseded version shows the edited body:\n%s", body)
	}

	if rr := doReq(t, h, "GET", fmt.Sprintf("/docs/versions/%d/3", plan.ID), "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown version status = %d, want 404", rr.Code)
	}
	if rr := doReq(t, h, "GET", fmt.Sprintf("/docs/versions/%d/x", plan.ID), "", nil); rr.Code != http.StatusBadRequest {
		t.Errorf("non-numeric version status = %d, want 400", rr.Code)
	}
}

// TestDocPageVersionQuery covers /docs/<ref>?v=<n>: the same version page as
// /docs/versions/{id}/{n}, reached through the canonical KEY-KIND-n URL.
func TestDocPageVersionQuery(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "025-part-2", Body: docPlanBody,
	})
	edited := strings.Replace(docPlanBody, "Do the thing.", "Do it now.", 1)
	if rr := doReq(t, h, "PUT", docPath(plan.ID, "/body"), token, model.UpdateDocBodyInput{Body: edited}); rr.Code != http.StatusOK {
		t.Fatalf("update body status = %d, body %s", rr.Code, rr.Body.String())
	}
	ref := fmt.Sprintf("/docs/WL-PLAN-%d", plan.Number)

	rr := doReq(t, h, "GET", ref+"?v=1", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("superseded version status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	bodyContains(t, body, "back to current", "Do the thing.")

	if rr := doReq(t, h, "GET", ref+"?v=3", "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown version status = %d, want 404", rr.Code)
	}
	if rr := doReq(t, h, "GET", ref+"?v=x", "", nil); rr.Code != http.StatusBadRequest {
		t.Errorf("non-numeric version status = %d, want 400", rr.Code)
	}
	// No ?v= is still the document itself.
	if rr := doReq(t, h, "GET", ref, "", nil); rr.Code != http.StatusOK {
		t.Fatalf("doc page status = %d, body %s", rr.Code, rr.Body.String())
	} else if strings.Contains(rr.Body.String(), "back to current") {
		t.Error("the document page rendered as a version page")
	}
}

// TestDocVersionPageRejectsInt32Overflow is the web-route sibling of
// TestGetDocVersionRejectsInt32Overflow (WL-345 I1): docVersionPage guards
// the same int4 column and must refuse the same way.
func TestDocVersionPageRejectsInt32Overflow(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "025-part-2", Body: docPlanBody,
	})

	if rr := doReq(t, h, "GET", fmt.Sprintf("/docs/versions/%d/99999999999", plan.ID), "", nil); rr.Code != http.StatusBadRequest {
		t.Errorf("version above int32 max status = %d, want 400, body %s", rr.Code, rr.Body.String())
	}
}

// docPageURL is the cockpit page path retained for plans, which have no
// cross-corpus shorthand.
func docPageURL(id int64) string { return "/docs/" + strconv.FormatInt(id, 10) }
