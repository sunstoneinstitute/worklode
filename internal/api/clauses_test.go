package api_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// clauseDocV1 mirrors internal/store/clauses_test.go's fixture of the same
// name: a spec whose first anchored section mints WL-CL-1.
const clauseDocV1 = "---\nstatus: draft\n---\n# T\n\nIntro.\n\n## 1. One {#sec-1}\n\nA.\n\n### 1.1 Sub {#sec-1.1}\n\nB.\n\n## 2. Two {#sec-2}\n\nC.\n"

// TestGetClause reads a clause over the API by its ref, and answers 400 for
// a malformed ref and 404 for one that doesn't resolve.
func TestGetClause(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: projID, Kind: "spec", Slug: "t", Body: clauseDocV1,
	})

	rr := doReq(t, h, http.MethodGet, "/api/v1/clauses/WL-CL-1", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var c model.Clause
	decodeInto(t, rr, &c)
	if c.Ref != "WL-CL-1" || c.Heading != "One" {
		t.Errorf("clause = %+v", c)
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/clauses/nonsense", token, nil); rr.Code != http.StatusBadRequest {
		t.Errorf("malformed ref: status = %d", rr.Code)
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/clauses/WL-CL-999", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("missing clause: status = %d", rr.Code)
	}
}

// TestClauseEditAndVersionsAPI edits a clause through the API on a draft
// document, again after acceptance (landing through the revision path), then
// reads its version history back (S14, S35).
func TestClauseEditAndVersionsAPI(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	doc := createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "spec", Slug: "t", Body: clauseDocV1})

	rr := doReq(t, h, http.MethodPut, "/api/v1/clauses/WL-CL-2", token, model.EditClauseInput{Heading: "Subsection", Body: "\nB changed.\n\n"})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rr.Code, rr.Body)
	}
	var c model.Clause
	decodeInto(t, rr, &c)
	if c.Heading != "Subsection" || c.Version != 1 || c.Body != "\nB changed.\n\n" {
		t.Errorf("edited clause = %+v", c)
	}
	rr = doReq(t, h, http.MethodGet, fmt.Sprintf("/api/v1/docs/%d", doc.ID), token, nil)
	var d model.DocDetail
	decodeInto(t, rr, &d)
	if !strings.Contains(d.Body, "### 1.1 Subsection {#sec-1.1}\n\nB changed.\n") {
		t.Errorf("doc body not regenerated:\n%s", d.Body)
	}

	acceptDocViaAPI(t, h, token, doc.ID)
	rr = doReq(t, h, http.MethodPut, "/api/v1/clauses/WL-CL-2", token, model.EditClauseInput{Heading: "Subsection", Body: "\nB again.\n\n"})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT on accepted = %d %s", rr.Code, rr.Body)
	}
	decodeInto(t, rr, &c)
	if c.Body != "\nB changed.\n\n" || c.Status != "accepted" {
		t.Errorf("accepted clause moved before the revision landed: %+v", c)
	}
	rr = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/docs/%d/revision/accept", doc.ID), token, nil)
	if rr.Code/100 != 2 {
		t.Fatalf("accept revision = %d %s", rr.Code, rr.Body)
	}

	rr = doReq(t, h, http.MethodGet, "/api/v1/clauses/WL-CL-2/versions", token, nil)
	var vs []model.ClauseVersion
	decodeInto(t, rr, &vs)
	if rr.Code != http.StatusOK || len(vs) != 2 || vs[0].Version != 2 {
		t.Errorf("versions = %d %+v", rr.Code, vs)
	}
	rr = doReq(t, h, http.MethodGet, "/api/v1/clauses/WL-CL-2/versions/1", token, nil)
	decodeInto(t, rr, &c)
	if rr.Code != http.StatusOK || c.Version != 1 || c.Body != "\nB changed.\n\n" {
		t.Errorf("v1 = %d %+v", rr.Code, c)
	}
	rr = doReq(t, h, http.MethodGet, "/api/v1/clauses/WL-CL-2/versions/2", token, nil)
	decodeInto(t, rr, &c)
	if rr.Code != http.StatusOK || c.Version != 2 || c.Body != "\nB again.\n\n" {
		t.Errorf("v2 = %d %+v", rr.Code, c)
	}

	for _, tc := range []struct {
		method, path string
		body         any
		want         int
	}{
		{http.MethodPut, "/api/v1/clauses/WL-CL-99", model.EditClauseInput{Heading: "x", Body: "y"}, http.StatusNotFound},
		{http.MethodPut, "/api/v1/clauses/nope", model.EditClauseInput{Heading: "x", Body: "y"}, http.StatusBadRequest},
		{http.MethodPut, "/api/v1/clauses/WL-CL-2", model.EditClauseInput{Heading: "", Body: "y"}, http.StatusUnprocessableEntity},
		{http.MethodGet, "/api/v1/clauses/WL-CL-2/versions/9", nil, http.StatusNotFound},
		{http.MethodGet, "/api/v1/clauses/WL-CL-2/versions/x", nil, http.StatusBadRequest},
		{http.MethodGet, "/api/v1/clauses/WL-CL-99/versions", nil, http.StatusNotFound},
	} {
		rr := doReq(t, h, tc.method, tc.path, token, tc.body)
		if rr.Code != tc.want {
			t.Errorf("%s %s = %d, want %d: %s", tc.method, tc.path, rr.Code, tc.want, rr.Body)
		}
	}
}

// TestClauseMetaAPI sets owner and tags through PATCH, checks the recorded
// clause.updated event names the patched clause (not just the request body,
// I1 of the Task 5 pattern), refuses an empty body with 422, and answers 404
// for an unknown clause (S15).
func TestClauseMetaAPI(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{Project: projID, Kind: "spec", Slug: "t", Body: clauseDocV1})

	rr := doReq(t, h, http.MethodPatch, "/api/v1/clauses/WL-CL-1", token,
		model.ClauseMetaInput{Owner: strPtr("stig"), Tags: &[]string{"a", "b"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH = %d %s", rr.Code, rr.Body)
	}
	var c model.Clause
	decodeInto(t, rr, &c)
	if c.Owner != "stig" || len(c.Tags) != 2 || c.Tags[0] != "a" || c.Tags[1] != "b" {
		t.Errorf("patched clause = %+v", c)
	}

	events := pollEvents(t, h, token, "?type=clause.updated", 1)
	checkPayloadProps(t, eventPayload(t, events[0].(map[string]any)), map[string]string{"clause": "WL-CL-1"})

	if rr := doReq(t, h, http.MethodPatch, "/api/v1/clauses/WL-CL-1", token, model.ClauseMetaInput{}); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty body: status = %d, body %s", rr.Code, rr.Body)
	}
	if rr := doReq(t, h, http.MethodPatch, "/api/v1/clauses/WL-CL-999", token, model.ClauseMetaInput{Owner: strPtr("stig")}); rr.Code != http.StatusNotFound {
		t.Errorf("unknown clause: status = %d", rr.Code)
	}
}

// TestListClauses lists a document's arrangement by its WL-SPEC ref, in
// order, and answers 404 for an unknown document and 422 for a bad status.
func TestListClauses(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: projID, Kind: "spec", Slug: "t", Body: clauseDocV1,
	})

	rr := doReq(t, h, http.MethodGet, "/api/v1/clauses?doc=WL-SPEC-1", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var cs []model.Clause
	decodeInto(t, rr, &cs)
	if len(cs) != 3 || cs[0].Ref != "WL-CL-1" || cs[1].Heading != "Sub" || cs[2].Heading != "Two" {
		t.Errorf("clauses = %+v", cs)
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/clauses?doc=WL-SPEC-99", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown doc: status = %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/clauses?status=bogus", token, nil); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad status: status = %d", rr.Code)
	}
}
