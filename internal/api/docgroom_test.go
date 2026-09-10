package api_test

import (
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestWithdrawDoc is 025 §8.7's close verb over HTTP: an accepted document
// withdraws with a justification, the event carries it, and the statuses that
// cannot be withdrawn come back as 422 rather than a silent no-op.
func TestWithdrawDoc(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	spec := acceptedSpec(t, h, token, "proj", "025-w", 25)

	rr := doReq(t, h, "POST", docPath(spec.ID, "/withdraw"), token,
		model.WithdrawDocInput{Justification: "the approach was abandoned"})
	if rr.Code != http.StatusOK {
		t.Fatalf("withdraw status = %d, want 200, body %s", rr.Code, rr.Body.String())
	}
	var after model.Doc
	decodeInto(t, rr, &after)
	if after.Status != "withdrawn" {
		t.Errorf("status = %q, want withdrawn", after.Status)
	}
	if after.Ref == "" {
		t.Errorf("doc = %+v, want the response stamped with the project key", after)
	}

	// Already withdrawn, and a draft: both refused by the store's rule.
	if rr := doReq(t, h, "POST", docPath(spec.ID, "/withdraw"), token,
		model.WithdrawDocInput{Justification: "again"}); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("second withdraw status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}
	draft := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 26, Slug: "026-draft", Body: docSpecBody,
	})
	if rr := doReq(t, h, "POST", docPath(draft.ID, "/withdraw"), token,
		model.WithdrawDocInput{Justification: "never mind"}); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("withdraw a draft status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}

	// The event type is the handler's own string, so nothing else in the
	// suite would catch a typo in it.
	pollEvents(t, h, token, "?type=doc.withdrawn", 1)
}

// TestListDocsUnresolved is 025 §8.7's `--unresolved` selector: the accepted
// specs and plans nothing has executed, with withdrawal taking one out of the
// set. The day bound is refused on its own, and a contradicting kind is
// refused rather than answered with nothing.
func TestListDocsUnresolved(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	keep := acceptedSpec(t, h, token, "proj", "027-keep", 27)
	drop := acceptedSpec(t, h, token, "proj", "028-drop", 28)

	if got := unresolvedSlugs(t, h, token, "?unresolved=true&project=proj"); len(got) != 2 {
		t.Fatalf("unresolved = %v, want both accepted specs", got)
	}

	if rr := doReq(t, h, "POST", docPath(drop.ID, "/withdraw"), token,
		model.WithdrawDocInput{Justification: "closed"}); rr.Code != http.StatusOK {
		t.Fatalf("withdraw status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := unresolvedSlugs(t, h, token, "?unresolved=true&project=proj")
	if len(got) != 1 || got[0] != keep.Slug {
		t.Errorf("unresolved after a withdrawal = %v, want only %s", got, keep.Slug)
	}

	// Nothing here is 30 days old, so the bound empties the answer rather
	// than being ignored.
	if got := unresolvedSlugs(t, h, token, "?unresolved=true&project=proj&older_than_days=30"); len(got) != 0 {
		t.Errorf("unresolved older_than_days=30 = %v, want none", got)
	}

	for _, q := range []string{
		"?older_than_days=30",                  // the bound without the selector
		"?unresolved=true&kind=adr",            // an ADR is executed by nothing
		"?unresolved=true&status=draft",        // the selector implies accepted
		"?unresolved=true&needs_planning=true", // two selectors at once
		"?unresolved=true&older_than_days=x",
	} {
		if rr := doReq(t, h, "GET", "/api/v1/docs"+q, token, nil); rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("GET /api/v1/docs%s status = %d, want 422, body %s", q, rr.Code, rr.Body.String())
		}
	}
}

func unresolvedSlugs(t *testing.T, h http.Handler, token, query string) []string {
	t.Helper()
	rr := doReq(t, h, "GET", "/api/v1/docs"+query, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/docs%s status = %d, body %s", query, rr.Code, rr.Body.String())
	}
	var resp model.DocListResponse
	decodeInto(t, rr, &resp)
	out := make([]string, len(resp.Docs))
	for i, d := range resp.Docs {
		out[i] = d.Slug
	}
	return out
}
