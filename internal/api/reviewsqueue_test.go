package api_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// reviewsDoc seeds one spec whose title is title, authored by createdBy, and
// files an awaiting approval on revision 1 with the given role and actor
// (nil for neither). It returns the approval id.
func reviewsDoc(t *testing.T, st *store.Store, project string, number int,
	title, createdBy string, role, actor *string) int64 {
	t.Helper()
	body := strings.Replace(docSpecBody, "# Documents in the backbone", "# "+title, 1)
	d := seedDoc(t, st, store.DocInput{
		Project: project, Kind: "spec", Number: number,
		Slug: fmt.Sprintf("%03d-%s", number, strings.ToLower(strings.ReplaceAll(title, " ", "-"))),
		Body: body, CreatedBy: createdBy,
	})
	entity := store.DocEntityID(d.ID)
	seedEvent(t, st, "reviews-queue-"+entity, func(tx *sql.Tx, _ int64) error {
		_, err := store.InsertAwaitingApproval(tx, st.Now(), "doc", entity, "1", "",
			role, actor, nil)
		return err
	})
	for _, row := range awaitingFor(t, st, d.ID) {
		return row.ID
	}
	t.Fatalf("approval on %s not in the queue", title)
	return 0
}

// TestReviewsQueueOrdersMineThenUnassigned is WL-1003. Approval 823 (an
// unlaned doc approval naming no actor and no role) was in the queue the
// whole time: the page applies no actor filter, so the owner's row rendered,
// but oldest first among every other project's rows. The queue now puts what
// the signed-in actor can act on first, then rows assigned to nobody, then
// rows assigned to somebody else.
//
// "Mine" is the Home badge's match (required_actor is the actor, or
// required_role is one of their groups) plus a row the actor authored under a
// flow that allows self-review of its kind (WL-944): there the author is who
// decides it.
func TestReviewsQueueOrdersMineThenUnassigned(t *testing.T) {
	t.Parallel()
	st, h, iss := newOIDCServer(t, api.Config{})
	seedActor(t, st, "dana", "human", "Dana", false)
	seedActor(t, st, "erin", "human", "Erin", false)
	createProject(t, st, "other")
	createProject(t, st, "solo")
	seedEvent(t, st, "reviews-queue-solo-flow", func(tx *sql.Tx, _ int64) error {
		return store.SetProjectApprovalFlow(tx, "solo", model.ApprovalFlowSnapshot{
			Flow: model.ApprovalFlow{Name: "solo", Rev: "1", SelfReview: []string{"doc"}},
		})
	})

	erin, role := "erin", "crew-backbone"
	dana := "dana"
	// Inserted oldest first, so the old oldest-first order is exactly this.
	reviewsDoc(t, st, "other", 1, "Erin owes this", "erin", nil, &erin)
	reviewsDoc(t, st, "other", 2, "Nobody owes this", "erin", nil, nil)
	reviewsDoc(t, st, "other", 3, "Erin wrote this", "erin", nil, nil)
	reviewsDoc(t, st, "other", 4, "Crew owes this", "erin", &role, nil)
	reviewsDoc(t, st, "other", 5, "Dana owes this", "erin", nil, &dana)
	selfID := reviewsDoc(t, st, "solo", 6, "Dana wrote this", "dana", nil, nil)

	session := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "dana", "name": "Dana",
		"groups": []string{"user", "crew-backbone"},
	})
	rr := withSession(t, h, "GET", "/reviews", session, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /reviews = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()

	// Nothing filters the page: every awaiting row renders for this viewer.
	for _, title := range []string{"Erin owes this", "Nobody owes this",
		"Erin wrote this", "Crew owes this", "Dana owes this", "Dana wrote this"} {
		if !strings.Contains(body, title) {
			t.Errorf("/reviews is missing the row %q", title)
		}
	}

	// Mine in queue order, then unassigned in queue order, then the rest.
	order := []string{
		"Waiting on you", "Crew owes this", "Dana owes this", "Dana wrote this",
		"Assigned to nobody", "Nobody owes this", "Erin wrote this",
		"Assigned to someone else", "Erin owes this",
	}
	last := -1
	for _, want := range order {
		i := strings.Index(body, want)
		if i < 0 {
			t.Fatalf("/reviews is missing %q:\n%s", want, body)
		}
		if i < last {
			t.Errorf("%q renders before the entry listed ahead of it", want)
		}
		last = i
	}

	// The author's self-reviewable row carries the decide action, and the
	// decide succeeds under the solo flow.
	if !strings.Contains(body, fmt.Sprintf("/approvals/%d/decide", selfID)) {
		t.Errorf("self-reviewable row %d has no decide form", selfID)
	}
	if rr := decideForm(t, h, session, selfID, "approve", nil); rr.Code != http.StatusSeeOther {
		t.Errorf("author decides own doc under solo = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
}

// TestReviewsQueueSignedOutListsByAssignment: with no signed-in actor nothing
// is "mine", so the page opens on the rows assigned to nobody.
func TestReviewsQueueSignedOutListsByAssignment(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	seedActor(t, st, "erin", "human", "Erin", false)
	createProject(t, st, "other")
	erin := "erin"
	reviewsDoc(t, st, "other", 1, "Erin owes this", "alice", nil, &erin)
	reviewsDoc(t, st, "other", 2, "Nobody owes this", "alice", nil, nil)

	body := getOK(t, h, "/reviews")
	if strings.Contains(body, "Waiting on you") {
		t.Error("an anonymous page has nothing waiting on the viewer")
	}
	nobody, other := strings.Index(body, "Nobody owes this"), strings.Index(body, "Erin owes this")
	if nobody < 0 || other < 0 || nobody > other {
		t.Errorf("want the unassigned row before the assigned one (at %d, %d)", nobody, other)
	}
}
