package api_test

import (
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestSupersedeClausesAPI: applied withdraws the old clause, a dry run
// resolves and reports without writing, an empty map is 422, and an unknown
// project is 404.
func TestSupersedeClausesAPI(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: projID, Kind: "spec", Slug: "t", Body: clauseDocV1,
	}) // WL-CL-1..3

	path := "/api/v1/projects/" + projID + "/clauses/supersede"
	body := model.SupersedeInput{Entries: []model.SupersedeEntry{{Old: "WL-CL-2"}}, DryRun: true}
	rr := doReq(t, h, http.MethodPost, path, token, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("dry run: %d %s", rr.Code, rr.Body)
	}
	var res model.SupersedeResult
	decodeInto(t, rr, &res)
	if !res.DryRun || res.Withdrawn != 1 {
		t.Errorf("dry run result = %+v, want DryRun and Withdrawn 1", res)
	}

	var c model.Clause
	rr = doReq(t, h, http.MethodGet, "/api/v1/clauses/WL-CL-2", token, nil)
	decodeInto(t, rr, &c)
	if c.Status != "draft" {
		t.Errorf("dry run wrote: status = %s, want draft", c.Status)
	}

	body.DryRun = false
	rr = doReq(t, h, http.MethodPost, path, token, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rr.Code, rr.Body)
	}
	// A fresh var: DryRun carries json:",omitempty", so a stale true from the
	// dry-run decode above would survive an in-place decode of the false case.
	var applied model.SupersedeResult
	decodeInto(t, rr, &applied)
	if applied.DryRun || applied.Withdrawn != 1 {
		t.Errorf("apply result = %+v, want !DryRun and Withdrawn 1", applied)
	}
	rr = doReq(t, h, http.MethodGet, "/api/v1/clauses/WL-CL-2", token, nil)
	decodeInto(t, rr, &c)
	if c.Status != "withdrawn" {
		t.Errorf("apply did not persist: status = %s, want withdrawn", c.Status)
	}

	rr = doReq(t, h, http.MethodPost, path, token, model.SupersedeInput{})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty map: %d, want 422", rr.Code)
	}

	rr = doReq(t, h, http.MethodPost, "/api/v1/projects/nope/clauses/supersede", token, body)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d, want 404", rr.Code)
	}
}
