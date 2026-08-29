package api_test

// docreviewers_test.go covers POST /api/v1/docs/{id}/reviewers: the durable
// reviewer set spec 025 §7.3 assigns to a document (WL-359), independent of
// any one revision. request-approval, which reads this set, is covered in
// docapproval_test.go.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestSetDocReviewers walks the owner-or-admin authority and replace
// semantics: the owner sets a first set, a third party is refused, an admin
// who does not own the document replaces it, and the replacement drops
// whoever the new set does not name — GetDoc's Reviewers is the whole
// current set, never a union of every set ever assigned.
func TestSetDocReviewers(t *testing.T) {
	st, h, token := newTestServer(t) // token's actor is alice, admin=true
	createProject(t, st, "proj")
	bobToken := docActor(t, st, "bob")
	docActor(t, st, "carol")

	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x", Body: docSpecBody, Owner: "bob",
	})

	// A third party (neither owner nor admin) is refused.
	if rr := doReq(t, h, "POST", docPath(spec.ID, "/reviewers"), docActor(t, st, "dave"),
		model.SetDocReviewersInput{Reviewers: []string{"dave"}}); rr.Code != http.StatusForbidden {
		t.Fatalf("third party status = %d, want 403, body %s", rr.Code, rr.Body.String())
	}

	// The owner assigns bob and carol.
	rr := doReq(t, h, "POST", docPath(spec.ID, "/reviewers"), bobToken,
		model.SetDocReviewersInput{Reviewers: []string{"bob", "carol"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("owner set status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got model.Doc
	decodeInto(t, rr, &got)
	if fmt.Sprint(got.Reviewers) != "[bob carol]" {
		t.Fatalf("reviewers = %v, want [bob carol]", got.Reviewers)
	}

	// alice, an admin who does not own the document, replaces the set —
	// dropping carol, not adding to her.
	rr = doReq(t, h, "POST", docPath(spec.ID, "/reviewers"), token,
		model.SetDocReviewersInput{Reviewers: []string{"bob"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("admin set status = %d, body %s", rr.Code, rr.Body.String())
	}
	decodeInto(t, rr, &got)
	if fmt.Sprint(got.Reviewers) != "[bob]" {
		t.Errorf("reviewers after replace = %v, want [bob] (carol dropped)", got.Reviewers)
	}

	// GetDoc reads the same set back.
	rr = doReq(t, h, "GET", docPath(spec.ID, ""), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get status = %d, body %s", rr.Code, rr.Body.String())
	}
	var detail model.DocDetail
	decodeInto(t, rr, &detail)
	if fmt.Sprint(detail.Reviewers) != "[bob]" {
		t.Errorf("GetDoc reviewers = %v, want [bob]", detail.Reviewers)
	}
}

// TestDocReviewersAwaitingNarrowsToOpenLanes: ReviewersAwaiting names only
// the reviewers who have not yet approved (or requested changes on) the
// current version — resolved lanes and other reviewers' still-open lanes do
// not appear for someone who already has.
func TestDocReviewersAwaitingNarrowsToOpenLanes(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	docActor(t, st, "bob")
	docActor(t, st, "carol")

	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 25, Slug: "025-x", Body: docSpecBody,
	})
	setReviewers(t, h, token, spec.ID, []string{"bob", "carol"})
	if rr := doReq(t, h, "POST", docPath(spec.ID, "/request-approval"), token, nil); rr.Code != http.StatusOK {
		t.Fatalf("request-approval status = %d", rr.Code)
	}

	rr := doReq(t, h, "GET", docPath(spec.ID, ""), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get status = %d, body %s", rr.Code, rr.Body.String())
	}
	var detail model.DocDetail
	decodeInto(t, rr, &detail)
	if fmt.Sprint(detail.Reviewers) != "[bob carol]" {
		t.Fatalf("reviewers = %v, want [bob carol]", detail.Reviewers)
	}
	if fmt.Sprint(detail.ReviewersAwaiting) != "[bob carol]" {
		t.Errorf("reviewers awaiting = %v, want [bob carol] before either decides", detail.ReviewersAwaiting)
	}
}
