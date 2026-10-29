package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func reviewerRound(t *testing.T, s *Store, reviewID int64, in model.ReviewRoundInput, actor string) (*model.Review, error) {
	t.Helper()
	if in.Client == "" {
		in.Client = "cli"
	}
	var r *model.Review
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "review.round", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			r, err = SubmitReviewerRound(tx, taskTestNow, reviewID, in, actor, eventID)
			return err
		})
	return r, err
}

func authorRound(t *testing.T, s *Store, reviewID int64, baseRef, actor string) (*model.Review, error) {
	t.Helper()
	var r *model.Review
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "review.round", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			r, err = SubmitAuthorRound(tx, taskTestNow, reviewID, model.ReviewRoundInput{BaseRef: baseRef, Client: "cli"}, actor, eventID)
			return err
		})
	return r, err
}

// reviewEvents counts the events of type typ about review id with payload
// property key = val ("" matches any).
func reviewEvents(t *testing.T, s *Store, typ string, id int64, key, val string) int {
	t.Helper()
	q := `SELECT count(*) FROM events WHERE type = $1 AND payload->>'wl:subject' = $2`
	args := []any{typ, iri.CURIE(iri.Review(id))}
	if key != "" {
		q += ` AND payload->>$3 = $4`
		args = append(args, key, val)
	}
	var n int
	if err := s.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ruleThread is a thread input on the "the gate" bytes of the sec-2 rule.
func ruleThread(t *testing.T, ruleID int64, version int, body, intent string) model.OpenThreadInput {
	t.Helper()
	start := strings.Index(body, "the gate")
	return model.OpenThreadInput{
		Anchor: model.ReviewAnchor{Rule: ruleID, Version: version, ByteStart: start, ByteEnd: start + len("the gate"), Quote: "the gate"},
		Intent: intent, Body: intent + " on the gate",
	}
}

func TestReviewerRoundGates(t *testing.T) {
	t.Run("question rule", func(t *testing.T) {
		s, _, r, ruleID, version, body := gateFixture(t)
		q, err := openThread(t, s, r.ID, ruleThread(t, ruleID, version, body, "question"), "a")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := authorRound(t, s, r.ID, "2", "stig"); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("author round with an open question: err = %v, want ErrInvalidInput", err)
		}
		if _, _, _, err := replyThread(t, s, q.ID, "because", "stig"); err != nil {
			t.Fatalf("owner reply: %v", err)
		}
	})

	t.Run("orthogonality", func(t *testing.T) {
		s, _, r, ruleID, version, body := gateFixture(t)
		cr, err := openThread(t, s, r.ID, ruleThread(t, ruleID, version, body, "change_request"), "a")
		if err != nil {
			t.Fatal(err)
		}
		_, err = reviewerRound(t, s, r.ID, model.ReviewRoundInput{Verdict: "approved"}, "a")
		if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), fmt.Sprint(cr.ID)) {
			t.Fatalf("approve over an open change_request: err = %v, want ErrInvalidInput naming thread %d", err, cr.ID)
		}
		if _, err := resolveThread(t, s, cr.ID, "a", "resolved"); err != nil {
			t.Fatal(err)
		}
		got, err := reviewerRound(t, s, r.ID, model.ReviewRoundInput{Verdict: "approved", Threads: []model.OpenThreadInput{
			ruleThread(t, ruleID, version, body, "note"), ruleThread(t, ruleID, version, body, "question"),
		}}, "a")
		if err != nil {
			t.Fatalf("approve with a note and a question: %v", err)
		}
		if got.Round != 1 || len(got.Rounds) != 1 || got.Rounds[0].BaseRef != "1" || got.State != "open" {
			t.Errorf("review = round %d rounds %+v state %s, want one round at base 1, state open", got.Round, got.Rounds, got.State)
		}
		if n := reviewEvents(t, s, "wl:ReviewRoundSubmitted", r.ID, "wl:reviewRole", "reviewer"); n != 1 {
			t.Errorf("reviewer ReviewRoundSubmitted events = %d, want 1", n)
		}
		if n := reviewEvents(t, s, "wl:ReviewVerdictRecorded", r.ID, "wl:reviewVerdict", "wlc:approved"); n != 1 {
			t.Errorf("ReviewVerdictRecorded events = %d, want 1", n)
		}
	})

	t.Run("invalidation", func(t *testing.T) {
		s, doc, r, _, _, _ := gateFixture(t)
		for _, a := range []string{"a", "b"} {
			if _, err := reviewerRound(t, s, r.ID, model.ReviewRoundInput{Verdict: "approved"}, a); err != nil {
				t.Fatalf("%s approves: %v", a, err)
			}
		}
		got, err := s.GetReview(t.Context(), r.ID)
		if err != nil || got.State != "approved" {
			t.Fatalf("state = %v (err %v), want approved", got, err)
		}
		if _, err := authorRound(t, s, r.ID, "1", "stig"); !errors.Is(err, ErrInvalidInput) ||
			!strings.Contains(err.Error(), "nothing new to review") {
			t.Fatalf("author round at the reviewed base: err = %v, want nothing new to review", err)
		}
		if _, err := updateDocBody(t, s, doc.ID, strings.Replace(gateSpecBody, "Scope body.", "Scope body, revised.", 1)); err != nil {
			t.Fatal(err)
		}
		if _, err := authorRound(t, s, r.ID, "1", "stig"); !errors.Is(err, ErrInvalidInput) ||
			!strings.Contains(err.Error(), "version 2") {
			t.Fatalf("stale author round: err = %v, want one naming version 2", err)
		}
		if _, err := authorRound(t, s, r.ID, "2", "a"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("author round by a reviewer: err = %v, want ErrForbidden", err)
		}
		got, err = authorRound(t, s, r.ID, "2", "stig")
		if err != nil {
			t.Fatalf("author round at 2: %v", err)
		}
		if got.State != "open" || got.BaseRef != "2" || got.Round != 3 {
			t.Errorf("after author round: state %s base %s round %d, want open 2 3", got.State, got.BaseRef, got.Round)
		}
		if n := reviewEvents(t, s, "wl:ReviewRoundSubmitted", r.ID, "wl:reviewRole", "author"); n != 1 {
			t.Errorf("author ReviewRoundSubmitted events = %d, want 1", n)
		}
		if _, _, err := acceptDoc(t, s, doc.ID, "stig"); err == nil {
			t.Fatal("AcceptDoc with verdicts at an old base: want refused")
		}
		for _, a := range []string{"a", "b"} {
			if _, err := reviewerRound(t, s, r.ID, model.ReviewRoundInput{Verdict: "approved"}, a); err != nil {
				t.Fatalf("%s approves at 2: %v", a, err)
			}
		}
		if _, _, err := acceptDoc(t, s, doc.ID, "stig"); err != nil {
			t.Fatalf("AcceptDoc: %v", err)
		}
		got, err = s.GetReview(t.Context(), r.ID)
		if err != nil || got.State != "closed" || got.ClosedAt == nil {
			t.Fatalf("review after accept = %+v (err %v), want closed", got, err)
		}
		if n := reviewEvents(t, s, "wl:ReviewClosed", r.ID, "wl:outcome", "accepted"); n != 1 {
			t.Errorf("ReviewClosed accepted events = %d, want 1", n)
		}
	})

	t.Run("roster", func(t *testing.T) {
		s, _, r, _, _, _ := gateFixture(t)
		if err := s.CreateActor(t.Context(), "c", "human", "c", false); err != nil {
			t.Fatal(err)
		}
		if _, err := reviewerRound(t, s, r.ID, model.ReviewRoundInput{Verdict: "approved"}, "c"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("round by c: err = %v, want ErrForbidden", err)
		}
	})
}

// TestAuthorRoundChangesRequested: a changes_requested verdict sets the state
// and resolves the reviewer's lane; an author round on a new version reopens.
func TestAuthorRoundChangesRequested(t *testing.T) {
	s, doc, r, _, _, _ := gateFixture(t)
	got, err := reviewerRound(t, s, r.ID, model.ReviewRoundInput{Verdict: "changes_requested", OverallNote: "rework sec-1"}, "a")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "changes_requested" {
		t.Errorf("state = %s, want changes_requested", got.State)
	}
	var lane string
	if err := s.db.QueryRow(`SELECT state FROM approvals WHERE entity_kind = 'doc' AND entity_id = $1
	    AND subject_revision = '1' AND lane = 'a'`, DocEntityID(doc.ID)).Scan(&lane); err != nil || lane != "changes_requested" {
		t.Errorf("a's lane = %q (err %v), want changes_requested", lane, err)
	}
	if _, err := updateDocBody(t, s, doc.ID, strings.Replace(gateSpecBody, "Scope body.", "Scope body, reworked.", 1)); err != nil {
		t.Fatal(err)
	}
	if got, err = authorRound(t, s, r.ID, "2", "stig"); err != nil || got.State != "open" {
		t.Fatalf("author round: state %v err %v, want open", got, err)
	}
	if err := s.db.QueryRow(`SELECT state FROM approvals WHERE entity_kind = 'doc' AND entity_id = $1
	    AND subject_revision = '2' AND lane = 'a'`, DocEntityID(doc.ID)).Scan(&lane); err != nil || lane != "awaiting" {
		t.Errorf("a's lane at 2 = %q (err %v), want awaiting", lane, err)
	}
}

// TestCloseReviewRefusesARound: a closed review takes no further round.
func TestCloseReviewRefusesARound(t *testing.T) {
	s, _, r, _, _, _ := gateFixture(t)
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "review.close", nil,
		func(tx *sql.Tx, eventID int64) error { return CloseReview(tx, taskTestNow, r.ID, "abandoned", eventID) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reviewerRound(t, s, r.ID, model.ReviewRoundInput{Verdict: "approved"}, "a"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("round on a closed review: err = %v, want ErrInvalidInput", err)
	}
	if n := reviewEvents(t, s, "wl:ReviewClosed", r.ID, "wl:outcome", "abandoned"); n != 1 {
		t.Errorf("ReviewClosed events = %d, want 1", n)
	}
}
