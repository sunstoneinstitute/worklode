package api_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestDocEdgeRoutes: POST and DELETE /api/v1/docs/{id}/edges add and remove
// one edge and answer with the DocDetail; an inverse spelling is 422 naming
// the declared type, a duplicate 409, an absent edge 404.
func TestDocEdgeRoutes(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	target := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 26, Slug: "026-target", Body: docSpecBody,
	})
	doc := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x", Body: docSpecBody,
	})
	edge := model.DocEdgeInput{Type: "requires", To: "026-target#sec-1"}
	isEdge := func(e model.DocEdge) bool {
		return e.Type == "requires" && e.ToDoc == target.ID && e.ToAnchor == "sec-1"
	}

	rr := doReq(t, h, "POST", docPath(doc.ID, "/edges"), token, edge)
	if rr.Code != http.StatusOK {
		t.Fatalf("link status = %d, body %s", rr.Code, rr.Body.String())
	}
	var detail model.DocDetail
	decodeInto(t, rr, &detail)
	if !slices.ContainsFunc(detail.Edges, isEdge) {
		t.Errorf("edges = %+v, want requires 026-target#sec-1", detail.Edges)
	}
	if rr := doReq(t, h, "POST", docPath(doc.ID, "/edges"), token, edge); rr.Code != http.StatusConflict {
		t.Errorf("duplicate link status = %d, want 409, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", docPath(doc.ID, "/edges"), token, model.DocEdgeInput{Type: "isRequiredBy", To: "026-target"})
	if msg, _ := decodeMap(t, rr)["error"].(string); rr.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(msg, "requires") || !strings.Contains(msg, "WL-SPEC-77 §8.1") {
		t.Errorf("inverse link: status = %d, error = %q; want 422 naming requires and WL-SPEC-77 §8.1", rr.Code, msg)
	}
	if rr := doReq(t, h, "POST", docPath(doc.ID, "/edges"), token, model.DocEdgeInput{Type: "bogus", To: "x"}); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("undeclared type status = %d, want 422", rr.Code)
	}

	rr = doReq(t, h, "DELETE", docPath(doc.ID, "/edges"), token, edge)
	if rr.Code != http.StatusOK {
		t.Fatalf("unlink status = %d, body %s", rr.Code, rr.Body.String())
	}
	detail = model.DocDetail{}
	decodeInto(t, rr, &detail)
	if slices.ContainsFunc(detail.Edges, isEdge) {
		t.Errorf("edges = %+v, want requires 026-target gone", detail.Edges)
	}
	if rr := doReq(t, h, "DELETE", docPath(doc.ID, "/edges"), token, edge); rr.Code != http.StatusNotFound {
		t.Errorf("second unlink status = %d, want 404", rr.Code)
	}
}

// TestSetDocColumnsRoute: PATCH /api/v1/docs/{id} sets title and issued and
// answers with the DocDetail; a bad date is 422.
func TestSetDocColumnsRoute(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	doc := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x", Body: docSpecBody,
	})
	title, issued := "Renamed", "2026-09-01"
	rr := doReq(t, h, "PATCH", docPath(doc.ID, ""), token, model.DocColumnsInput{Title: &title, Issued: &issued})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var detail model.DocDetail
	decodeInto(t, rr, &detail)
	if detail.Title != title || detail.Issued != issued || detail.Version != doc.Version {
		t.Errorf("doc = {title:%q issued:%q version:%d}, want {%q %q %d}",
			detail.Title, detail.Issued, detail.Version, title, issued, doc.Version)
	}
	bad := "tomorrow"
	if rr := doReq(t, h, "PATCH", docPath(doc.ID, ""), token, model.DocColumnsInput{Issued: &bad}); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad issued status = %d, want 422", rr.Code)
	}
}
