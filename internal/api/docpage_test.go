package api_test

// docpage_test.go covers the read-only cockpit doc pages split out of
// docs_test.go to stay under the file-size ceiling (CLAUDE.md, Conventions):
// GET /docs, GET /docs/{ref}, and their version-history siblings.

import (
	"context"
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
		`href="/projects/proj/spec/25">WL-SPEC-25</a>`,
		`href="/projects/proj/spec/25">Documents in the backbone</a>`,
		`href="/projects/proj/plan/1">Documents in the backbone, part 2</a>`,
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

	rr := getCanonical(t, h, "/docs/WL-SPEC-25")
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
	rr = getCanonical(t, h, "/docs/WL-PLAN-1")
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

	rr := getCanonical(t, h, "/docs/WL-SPEC-25")
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

	rr := getCanonical(t, h, fmt.Sprintf("/docs/versions/%d/2", plan.ID))
	if rr.Code != http.StatusOK {
		t.Fatalf("current version status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	bodyContains(t, body, "Do it now.")
	if strings.Contains(body, "back to current") {
		t.Errorf("current version shows the back-to-current banner:\n%s", body)
	}

	rr = getCanonical(t, h, fmt.Sprintf("/docs/versions/%d/1", plan.ID))
	if rr.Code != http.StatusOK {
		t.Fatalf("superseded version status = %d, body %s", rr.Code, rr.Body.String())
	}
	body = rr.Body.String()
	bodyContains(t, body, "back to current", "Do the thing.")
	if strings.Contains(body, "Do it now.") {
		t.Errorf("superseded version shows the edited body:\n%s", body)
	}

	if rr := getCanonical(t, h, fmt.Sprintf("/docs/versions/%d/3", plan.ID)); rr.Code != http.StatusNotFound {
		t.Errorf("unknown version status = %d, want 404", rr.Code)
	}
	if rr := doReq(t, h, "GET", fmt.Sprintf("/docs/versions/%d/x", plan.ID), "", nil); rr.Code != http.StatusBadRequest {
		t.Errorf("non-numeric version status = %d, want 400", rr.Code)
	}
}

// TestDocPageVersionQuery covers /docs/<ref>?v=<n>: a redirect to the
// canonical version path, the same page /docs/versions/{id}/{n} reaches.
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

	rr := getCanonical(t, h, ref+"?v=1")
	if rr.Code != http.StatusOK {
		t.Fatalf("superseded version status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	bodyContains(t, body, "back to current", "Do the thing.")

	if rr := getCanonical(t, h, ref+"?v=3"); rr.Code != http.StatusNotFound {
		t.Errorf("unknown version status = %d, want 404", rr.Code)
	}
	if rr := getCanonical(t, h, ref+"?v=x"); rr.Code != http.StatusNotFound {
		t.Errorf("non-numeric version status = %d, want 404", rr.Code)
	}
	// No ?v= is still the document itself.
	if rr := getCanonical(t, h, ref); rr.Code != http.StatusOK {
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

// TestDocPageShowsNotes is WL-716's second gap: the anchored notes 025 §8.5
// stores are rendered instead of invisible. ?body=source is the escape hatch
// back to the stored text. Rule amendment folding is
// TestDocPageFoldsRuleAmendments.
func TestDocPageShowsNotes(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	base := acceptedSpec(t, h, token, "proj", "025-documents-in-the-backbone", 25)
	if rr := doReq(t, h, "POST", docPath(base.ID, "/notes"), token,
		model.AddDocNoteInput{Anchor: "sec-2", Body: "this needs an example"}); rr.Code != http.StatusOK {
		t.Fatalf("add note: status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr := getCanonical(t, h, "/docs/WL-SPEC-25")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	bodyContains(t, body,
		"Model body.",           // the document's own text
		"this needs an example", // the note
		`href="#sec-2"`,         // anchored where it applies
	)

	// The stored source stays reachable, and is what it says it is.
	rr = getCanonical(t, h, "/docs/WL-SPEC-25?body=source")
	if rr.Code != http.StatusOK {
		t.Fatalf("source status = %d, body %s", rr.Code, rr.Body.String())
	}
	source := rr.Body.String()
	bodyContains(t, source, "Model body.", "this needs an example")
}

// TestDocPageFoldsRuleAmendments: a rule amends edge onto a section's rule
// folds the amending rule's text beneath that section, attributed to the
// amending rule (WL-SPEC-77 §4).
func TestDocPageFoldsRuleAmendments(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	acceptedSpec(t, h, token, "proj", "025-documents-in-the-backbone", 25) // WL-RULE-1, WL-RULE-2 (§2)
	amender := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 46, Slug: "046-tighter",
		Body: "---\nstatus: draft\n---\n\n# Tighter\n\n## 1. Tighter model {#sec-1}\n\nThe model now says ten.\n",
	}) // WL-RULE-3
	if rr := doReq(t, h, "POST", docPath(amender.ID, "/accept"), token, nil); rr.Code != http.StatusOK {
		t.Fatalf("accept amender: status = %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, h, http.MethodPost, "/api/v1/rules/WL-RULE-3/edges", token,
		model.RuleEdgeInput{Type: "amends", To: "WL-RULE-2"}); rr.Code != http.StatusCreated {
		t.Fatalf("link amends: %d %s", rr.Code, rr.Body)
	}

	rr := getCanonical(t, h, "/docs/WL-SPEC-25")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	bodyContains(t, body, "Model body.", "The model now says ten.", "WL-RULE-3", "Tighter model")
	if i, j := strings.Index(body, "Model body."), strings.Index(body, "The model now says ten."); j < i {
		t.Errorf("amendment rendered above the section it amends")
	}
}

// TestDocPageShowsReviewState is WL-716's third gap: the reviewer roster
// (025 §7.3) and the open approval rows (029 §7) are rendered on the document
// itself, each with the decide form the Reviews queue uses — carrying this
// page as its return, so deciding here does not throw the reviewer to /reviews.
func TestDocPageShowsReviewState(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	if err := st.CreateActor(context.Background(), "bob", "human", "bob", false); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	d := draftSpecForReview(t, h, token, "proj", "025-documents-in-the-backbone", 25)
	setReviewers(t, h, token, d.ID, []string{"bob"})
	if rr := doReq(t, h, "POST", docPath(d.ID, "/request-approval"), token, nil); rr.Code != http.StatusOK {
		t.Fatalf("request approval: status = %d, body %s", rr.Code, rr.Body.String())
	}
	open := awaitingFor(t, st, d.ID)
	if len(open) != 1 {
		t.Fatalf("awaiting rows = %d, want 1", len(open))
	}

	rr := getCanonical(t, h, "/docs/WL-SPEC-25")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	bodyContains(t, body,
		"<li>bob",   // the roster
		">awaiting", // and who still owes a verdict
		fmt.Sprintf(`action="/approvals/%d/decide"`, open[0].ID),
		`value="approve"`,
		`name="return" value="/projects/proj/spec/25"`,
	)
}

// TestDocPageNamesMintedTasks: a plan page's `### Task N` headings carry the
// id of the task that declaration minted, linked to the task's page, so a
// reader does not have to match titles by hand. The source view stays the
// stored text.
func TestDocPageNamesMintedTasks(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "mint-plan", Body: docPlanMintBody,
	})
	rr := doReq(t, h, "POST", docPath(plan.ID, "/accept"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("accept plan status = %d, body %s", rr.Code, rr.Body.String())
	}
	var accepted model.AcceptDocResponse
	decodeInto(t, rr, &accepted)
	if len(accepted.Tasks) != 2 {
		t.Fatalf("tasks = %v, want 2 minted tasks", accepted.Tasks)
	}

	ref := fmt.Sprintf("/docs/WL-PLAN-%d", plan.Number)
	page := getCanonical(t, h, ref)
	if page.Code != http.StatusOK {
		t.Fatalf("plan page status = %d, body %s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	for _, task := range accepted.Tasks {
		bodyContains(t, body, fmt.Sprintf(`(<a href="/tasks/%s" rel="nofollow">%s</a>)`, task.ID, task.ID))
	}

	src := getCanonical(t, h, ref+"?body=source")
	if src.Code != http.StatusOK {
		t.Fatalf("source view status = %d, body %s", src.Code, src.Body.String())
	}
	if strings.Contains(src.Body.String(), accepted.Tasks[0].ID) {
		t.Errorf("source view names %s; it must show the stored body", accepted.Tasks[0].ID)
	}
}
