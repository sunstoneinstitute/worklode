package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestUpdateDocBodyIfVersion: the compare-and-swap over HTTP. A stale
// expected version is a 409 — the caller's own state is out of date, and the
// fix is to re-read and retry — and the revision endpoint refuses the field
// outright rather than accept one it does not honour.
func TestUpdateDocBodyIfVersion(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	plan := seedDoc(t, st, store.DocInput{
		Project: "proj", Kind: "plan", Slug: "025-cas", Body: docPlanBody,
		CreatedBy: "alice", Status: "accepted",
	})
	first := strings.Replace(docPlanBody, "Do the thing.", "Writer A.", 1)
	rr := doReq(t, h, "PUT", docPath(plan.ID, "/body"), token,
		model.UpdateDocBodyInput{Body: first, IfVersion: plan.Version})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body %s", rr.Code, rr.Body.String())
	}

	second := strings.Replace(docPlanBody, "Do the thing.", "Writer B.", 1)
	rr = doReq(t, h, "PUT", docPath(plan.ID, "/body"), token,
		model.UpdateDocBodyInput{Body: second, IfVersion: plan.Version})
	if rr.Code != http.StatusConflict {
		t.Fatalf("stale write status = %d, want 409, body %s", rr.Code, rr.Body.String())
	}
	if msg, _ := decodeMap(t, rr)["error"].(string); !strings.Contains(msg, "expected") {
		t.Errorf("error = %q, want it to name the expected version", msg)
	}

	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 26, Slug: "026-cas", Body: docSpecBody,
	})
	if rr := doReq(t, h, "POST", docPath(spec.ID, "/accept"), token, nil); rr.Code != http.StatusOK {
		t.Fatalf("accept status = %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, h, "POST", docPath(spec.ID, "/revise"), token, nil); rr.Code != http.StatusOK {
		t.Fatalf("open revision status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "PUT", docPath(spec.ID, "/revision"), token,
		model.UpdateDocBodyInput{Body: docSpecBody + "\nmore\n", IfVersion: 1})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("revision if_version status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}
}
