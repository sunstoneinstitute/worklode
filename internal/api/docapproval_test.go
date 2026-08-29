package api_test

// docapproval_test.go covers the two approval routes a CLI token may reach:
// POST /api/v1/docs/{id}/request-approval and GET /api/v1/approvals. What
// they must NOT reach — a decide route — is covered by
// TestNoDecideRouteOnTheJSONAPI below. POST /api/v1/docs/{id}/reviewers, the
// durable reviewer set request-approval reads (WL-359), is covered in
// docreviewers_test.go — this file's setReviewers helper calls it, and
// TestSetDocReviewersUnknownActor stays here since it is really about
// request-approval's actor-validation contract having moved, not about the
// reviewers route's own behavior.

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// draftSpecForReview creates a draft spec assigned to the caller, the state a
// review request is made from.
func draftSpecForReview(t *testing.T, h http.Handler, token, project, slug string, number int) model.Doc {
	t.Helper()
	return createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: project, Kind: "spec", Number: number, Slug: slug, Body: docSpecBody,
	})
}

// awaitingFor returns the queue rows whose entity is the given document.
func awaitingFor(t *testing.T, st *store.Store, docID int64) []model.AwaitingApproval {
	t.Helper()
	rows, err := st.ListAwaitingApprovals(context.Background())
	if err != nil {
		t.Fatalf("list awaiting approvals: %v", err)
	}
	var out []model.AwaitingApproval
	for _, row := range rows {
		if row.EntityID == store.DocEntityID(docID) {
			out = append(out, row)
		}
	}
	return out
}

// setReviewers PUTs a document's durable reviewer set (WL-359) and fails the
// test if the call does not succeed.
func setReviewers(t *testing.T, h http.Handler, token string, docID int64, reviewers []string) {
	t.Helper()
	rr := doReq(t, h, "POST", docPath(docID, "/reviewers"), token,
		model.SetDocReviewersInput{Reviewers: reviewers})
	if rr.Code != http.StatusOK {
		t.Fatalf("set reviewers %v: status = %d, body %s", reviewers, rr.Code, rr.Body.String())
	}
}

// TestRequestDocApprovalOpensOneLanePerReviewer: 025 §7.3's reviewer set is
// several open rows on one revision, not one row with several names.
func TestRequestDocApprovalOpensOneLanePerReviewer(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	ctx := context.Background()
	for _, id := range []string{"bob", "carol"} {
		if err := st.CreateActor(ctx, id, "human", id, false); err != nil {
			t.Fatalf("create actor %s: %v", id, err)
		}
	}
	d := draftSpecForReview(t, h, token, "proj", "req-lanes", 61)
	setReviewers(t, h, token, d.ID, []string{"bob", "carol"})

	rr := doReq(t, h, "POST", docPath(d.ID, "/request-approval"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got model.Doc
	decodeInto(t, rr, &got)
	if got.ID != d.ID || got.Version != d.Version {
		t.Errorf("response = doc %d v%d, want the requested doc %d v%d",
			got.ID, got.Version, d.ID, d.Version)
	}

	rows := awaitingFor(t, st, d.ID)
	if len(rows) != 2 {
		t.Fatalf("awaiting rows = %d, want one per reviewer", len(rows))
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if row.RequiredActor == nil {
			t.Fatalf("row %d names no required actor", row.ID)
		}
		seen[*row.RequiredActor] = true
		if row.SubjectRevision != strconv.Itoa(d.Version) {
			t.Errorf("row %d subject_revision = %q, want the doc version %d",
				row.ID, row.SubjectRevision, d.Version)
		}
		if row.EntityKind != "doc" {
			t.Errorf("row %d entity_kind = %q, want doc", row.ID, row.EntityKind)
		}
		// A document hangs off its project directly; there is no task.
		if row.Project != "proj" || row.Task != "" {
			t.Errorf("row %d project/task = %q/%q, want proj/\"\"",
				row.ID, row.Project, row.Task)
		}
		if row.Title != d.Title {
			t.Errorf("row %d title = %q, want the document title %q", row.ID, row.Title, d.Title)
		}
	}
	if !seen["bob"] || !seen["carol"] {
		t.Errorf("lanes opened for %v, want bob and carol", seen)
	}
}

// TestRequestDocApprovalIsIdempotentAndAdditive: re-requesting the same
// version opens only the lanes that are missing, which is what lets a caller
// add a reviewer — via SetDocReviewers (WL-359) — and simply run request
// again.
func TestRequestDocApprovalIsIdempotentAndAdditive(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	ctx := context.Background()
	for _, id := range []string{"bob", "carol"} {
		if err := st.CreateActor(ctx, id, "human", id, false); err != nil {
			t.Fatalf("create actor %s: %v", id, err)
		}
	}
	d := draftSpecForReview(t, h, token, "proj", "req-idem", 62)
	path := docPath(d.ID, "/request-approval")

	for _, reviewers := range [][]string{{"bob"}, {"bob"}, {"bob", "carol"}} {
		setReviewers(t, h, token, d.ID, reviewers)
		rr := doReq(t, h, "POST", path, token, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("request after set %v: status = %d, body %s", reviewers, rr.Code, rr.Body.String())
		}
	}
	if rows := awaitingFor(t, st, d.ID); len(rows) != 2 {
		t.Fatalf("awaiting rows = %d, want 2 (one per distinct reviewer)", len(rows))
	}
}

// TestSetDocReviewersUnknownActor: required_actor (via doc_reviewers) is an
// FK, so an unresolvable reviewer has to come back as an input error naming
// the name, not as a 500 naming a constraint. Unknown-reviewer validation
// moved here from request-approval (WL-359): the set is validated once, at
// assignment, not again at every request.
func TestSetDocReviewersUnknownActor(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	d := draftSpecForReview(t, h, token, "proj", "rev-unknown", 63)

	rr := doReq(t, h, "POST", docPath(d.ID, "/reviewers"), token,
		model.SetDocReviewersInput{Reviewers: []string{"nobody"}})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, "nobody") {
		t.Errorf("body %q does not name the reviewer that could not be resolved", body)
	}
	if rows := awaitingFor(t, st, d.ID); len(rows) != 0 {
		t.Errorf("awaiting rows = %d after a refused set, want none", len(rows))
	}
}

// TestRequestDocApprovalNeedsAReviewer: a document with no reviewer set
// assigned is a refusal, not a silently successful no-op that leaves the
// document waiting on nobody.
func TestRequestDocApprovalNeedsAReviewer(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	d := draftSpecForReview(t, h, token, "proj", "req-empty", 64)

	rr := doReq(t, h, "POST", docPath(d.ID, "/request-approval"), token, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
}

// TestListApprovalsServesBothEntityKinds: the JSON queue is the same rows the
// cockpit's /reviews page renders — a pull request and a document side by
// side, each carrying its own title.
func TestListApprovalsServesBothEntityKinds(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	if err := st.CreateActor(context.Background(), "bob", "human", "Bob", false); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	seedAwaitingPRApproval(t, st, "acme/site#71", "Fix the thing")
	d := draftSpecForReview(t, h, token, "proj", "queue-doc", 65)
	setReviewers(t, h, token, d.ID, []string{"bob"})
	rr := doReq(t, h, "POST", docPath(d.ID, "/request-approval"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("request status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/api/v1/approvals", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, body %s", rr.Code, rr.Body.String())
	}
	var resp model.ApprovalListResponse
	decodeInto(t, rr, &resp)

	byKind := map[string]model.AwaitingApproval{}
	for _, a := range resp.Approvals {
		byKind[a.EntityKind] = a
	}
	pr, ok := byKind["pr"]
	if !ok {
		t.Fatalf("no pr row in the queue: %+v", resp.Approvals)
	}
	if pr.Title != "Fix the thing" {
		t.Errorf("pr title = %q, want the pull request title", pr.Title)
	}
	doc, ok := byKind["doc"]
	if !ok {
		t.Fatalf("no doc row in the queue: %+v", resp.Approvals)
	}
	if doc.Title != d.Title {
		t.Errorf("doc title = %q, want %q", doc.Title, d.Title)
	}
	if doc.RequiredActorName == nil || *doc.RequiredActorName != "Bob" {
		t.Errorf("doc row required_actor_name = %v, want Bob", doc.RequiredActorName)
	}
}

// TestNoDecideRouteOnTheJSONAPI is the guard this whole change exists inside:
// 029 §7.3 makes deciding a web UI act because a session's group claims are
// fresh and a 30-day CLI token's are not. A decide route reachable with a
// bearer token would defeat that, so the absence is asserted rather than
// merely intended.
func TestNoDecideRouteOnTheJSONAPI(t *testing.T) {
	st, h, token := newTestServer(t)
	seeded := seedAwaitingPRApproval(t, st, "acme/site#72", "Fix the other thing")

	for _, path := range []string{
		"/api/v1/approvals/" + strconv.FormatInt(seeded.ID, 10) + "/decide",
		"/api/v1/approvals/" + strconv.FormatInt(seeded.ID, 10),
	} {
		rr := doReq(t, h, "POST", path, token, map[string]string{"decision": "approve"})
		if rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want no such route (029 §7.3 keeps deciding on the web session)",
				path, rr.Code)
		}
	}
}
