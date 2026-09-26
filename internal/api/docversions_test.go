package api_test

// docversions_test.go covers GET /api/v1/docs/{id}/versions and /versions/{n}.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestDocVersions covers GET /api/v1/docs/{id}/versions and
// GET /api/v1/docs/{id}/versions/{n} (025 §4.5): a plan stays freely mutable
// (025 §9), so editing its body snapshots the version it leaves before
// serving the new one.
func TestDocVersions(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "025-part-2", Body: docPlanBody,
	})
	edited := strings.Replace(docPlanBody, "Do the thing.", "Do it now.", 1)
	if rr := doReq(t, h, "PUT", docPath(plan.ID, "/body"), token, model.UpdateDocBodyInput{Body: noHeader(t, edited)}); rr.Code != http.StatusOK {
		t.Fatalf("update body status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr := doReq(t, h, "GET", docPath(plan.ID, "/versions"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list versions status = %d, body %s", rr.Code, rr.Body.String())
	}
	var versions []model.DocVersionSummary
	decodeInto(t, rr, &versions)
	if len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("versions = %+v, want [2, 1]", versions)
	}

	rr = doReq(t, h, "GET", docPath(plan.ID, "/versions/1"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get version 1 status = %d, body %s", rr.Code, rr.Body.String())
	}
	var v1 model.DocVersion
	decodeInto(t, rr, &v1)
	if v1.Body != noHeader(t, docPlanBody) {
		t.Errorf("version 1 body = %q, want the pre-edit body", v1.Body)
	}
	if len(v1.Edges) == 0 {
		t.Errorf("version 1 edges = %+v, want the snapshotted covers edge", v1.Edges)
	}

	rr = doReq(t, h, "GET", docPath(plan.ID, "/versions/2"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get version 2 status = %d, body %s", rr.Code, rr.Body.String())
	}
	var v2 model.DocVersion
	decodeInto(t, rr, &v2)
	if v2.Body != noHeader(t, edited) {
		t.Errorf("version 2 body = %q, want the edited body", v2.Body)
	}

	if rr := doReq(t, h, "GET", docPath(plan.ID, "/versions/3"), token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown version status = %d, want 404", rr.Code)
	}
	if rr := doReq(t, h, "GET", docPath(plan.ID, "/versions/x"), token, nil); rr.Code != http.StatusBadRequest {
		t.Errorf("non-numeric version status = %d, want 400", rr.Code)
	}

	// An unknown document's version list is empty (ListDocVersions never
	// errors for a missing doc), which the handler treats the same as
	// getDoc's 404 for an unknown id.
	if rr := doReq(t, h, "GET", "/api/v1/docs/4711/versions", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown doc versions status = %d, want 404", rr.Code)
	}
}

// TestGetDocVersionCarriesEdgesAndRules: a version read returns the
// document's edges and rule arrangement as they stood at that version.
func TestGetDocVersionCarriesEdgesAndRules(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 30, Slug: "030-x",
		Body: "---\nstatus: draft\nrequires: 999-elsewhere.md\n---\n\n# X\n\n## 1. One {#sec-1}\n\nA.\n\n## 2. Two {#sec-2}\n\nB.\n",
	})
	rr := doReq(t, h, "GET", docPath(spec.ID, "/versions/1"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get version 1 status = %d, body %s", rr.Code, rr.Body.String())
	}
	var v struct {
		Edges []model.DocEdge        `json:"edges"`
		Rules []model.DocVersionRule `json:"rules"`
	}
	decodeInto(t, rr, &v)
	if len(v.Edges) != 1 || v.Edges[0].Type != "requires" {
		t.Errorf("edges = %+v, want the one requires edge", v.Edges)
	}
	if len(v.Rules) != 2 || v.Rules[0].Anchor != "sec-1" || v.Rules[1].Anchor != "sec-2" {
		t.Errorf("rules = %+v, want sec-1 and sec-2", v.Rules)
	}
}

// TestGetDocVersionRejectsInt32Overflow pins WL-345 (I1): docs.version is a
// Postgres int4, so a version above math.MaxInt32 must be rejected by the
// handler's own guard rather than reaching pgx's parameter encoder, which
// fails the query and would otherwise surface as a 500.
func TestGetDocVersionRejectsInt32Overflow(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "025-part-2", Body: docPlanBody,
	})

	if rr := doReq(t, h, "GET", docPath(plan.ID, "/versions/99999999999"), token, nil); rr.Code != http.StatusBadRequest {
		t.Errorf("version above int32 max status = %d, want 400, body %s", rr.Code, rr.Body.String())
	}
}
