package api_test

// clausepage_test.go covers the cockpit clause page and the S20 canonical
// URLs and redirects (task 5): GET /projects/{proj}/clause/{n}, its version
// sibling, the project-key and document-kind redirects, and the /clauses/{ref}
// resolving redirect.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestClausePageAndRedirects covers S20's clause page and both redirect
// forms: an uppercase project key, and a document-kind path.
func TestClausePageAndRedirects(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "spec", Slug: "t", Body: clauseDocV1})

	rr := doReq(t, h, http.MethodGet, "/projects/"+projID+"/clause/2", "", nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "WL-CL-2") || !strings.Contains(rr.Body.String(), "Sub") {
		t.Errorf("clause page = %d\n%s", rr.Code, rr.Body.String()[:min(400, rr.Body.Len())])
	}
	if strings.Contains(rr.Body.String(), "back to current") {
		t.Errorf("the current clause page carries the older-version banner")
	}
	rr = doReq(t, h, http.MethodGet, "/projects/"+projID+"/clause/2/1", "", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("clause version page = %d", rr.Code)
	}
	for path, want := range map[string]string{
		"/clauses/WL-CL-2":                "/projects/" + projID + "/clause/2",
		"/projects/WL/clause/2":           "/projects/" + projID + "/clause/2",
		"/projects/" + projID + "/spec/1": "/docs/WL-SPEC-1",
	} {
		rr := doReq(t, h, http.MethodGet, path, "", nil)
		if rr.Code != http.StatusFound || rr.Header().Get("Location") != want {
			t.Errorf("%s = %d %q, want 302 %q", path, rr.Code, rr.Header().Get("Location"), want)
		}
	}
	for _, path := range []string{
		"/projects/" + projID + "/clause/99",
		"/projects/" + projID + "/feature/1",
		"/clauses/nope",
		"/projects/nowhere/clause/2",
	} {
		if rr := doReq(t, h, http.MethodGet, path, "", nil); rr.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rr.Code)
		}
	}
}

// TestClausePageOlderVersionLinksBack: an older version's page says which
// version it is showing and links back to the current one, the way a
// document version's page does.
func TestClausePageOlderVersionLinksBack(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	doc := createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "spec", Slug: "t", Body: clauseDocV1})
	acceptDocViaAPI(t, h, token, doc.ID)
	rr := doReq(t, h, http.MethodPut, "/api/v1/clauses/WL-CL-2", token,
		model.EditClauseInput{Heading: "Sub", Body: "\nB changed.\n\n"})
	if rr.Code != http.StatusOK {
		t.Fatalf("edit clause = %d %s", rr.Code, rr.Body)
	}
	if rr := doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/docs/%d/revision/accept", doc.ID), token, nil); rr.Code != http.StatusOK {
		t.Fatalf("accept revision = %d %s", rr.Code, rr.Body)
	}

	rr = doReq(t, h, http.MethodGet, "/projects/"+projID+"/clause/2/1", "", nil)
	body := rr.Body.String()
	if rr.Code != http.StatusOK || !strings.Contains(body, "Viewing version 1") ||
		!strings.Contains(body, `href="/projects/`+projID+`/clause/2"`) {
		t.Errorf("version 1 page = %d, want the back-to-current banner\n%s", rr.Code, body[:min(1200, len(body))])
	}
	rr = doReq(t, h, http.MethodGet, "/projects/"+projID+"/clause/2/2", "", nil)
	if strings.Contains(rr.Body.String(), "back to current") {
		t.Errorf("the current version's own page carries the banner")
	}
}

// TestClausePageUppercaseProjectID: createProject checks the shape of the
// key and only that the id is non-empty, so an uppercase project id is
// legal. Choosing the lookup from the case of {proj} routed those to
// ProjectByKey, which no key can match, and the page was unreachable.
func TestClausePageUppercaseProjectID(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	if err := st.CreateProject(context.Background(), "MyProj", "MyProj", "MP"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	createDocViaAPI(t, h, token, model.CreateDocInput{Project: "MyProj", Kind: "spec", Slug: "t", Body: clauseDocV1})
	rr := doReq(t, h, http.MethodGet, "/projects/MyProj/clause/2", "", nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "MP-CL-2") {
		t.Errorf("clause page for the uppercase project id MyProj = %d, want 200 with MP-CL-2", rr.Code)
	}
}

// TestClausePageShowsOwnerTagsAndEdges covers task 8: the owner chip, tag
// chips and the Edges card on the clause page, plus the card's honest empty
// state when a clause has no edges.
func TestClausePageShowsOwnerTagsAndEdges(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "spec", Slug: "t", Body: clauseDocV1}) // WL-CL-1..3

	rr := doReq(t, h, http.MethodPost, "/api/v1/clauses/WL-CL-1/edges", token, model.ClauseEdgeInput{Type: "constrains", To: "WL-CL-3"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("link: %d %s", rr.Code, rr.Body)
	}
	rr = doReq(t, h, http.MethodPatch, "/api/v1/clauses/WL-CL-3", token,
		model.ClauseMetaInput{Owner: strPtr("stig"), Tags: &[]string{"security"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("set meta: %d %s", rr.Code, rr.Body)
	}

	rr = doReq(t, h, http.MethodGet, "/projects/"+projID+"/clause/3", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("clause page = %d %s", rr.Code, rr.Body)
	}
	body := rr.Body.String()
	for _, want := range []string{"constrains", "WL-CL-1", "owner stig", "security"} {
		if !strings.Contains(body, want) {
			t.Errorf("clause page missing %q:\n%s", want, body)
		}
	}

	// WL-CL-2 has no edges: the card says so rather than rendering empty.
	rr = doReq(t, h, http.MethodGet, "/projects/"+projID+"/clause/2", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("clause page = %d %s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "No edges.") {
		t.Errorf("clause page missing empty edges state:\n%s", rr.Body.String())
	}
}
