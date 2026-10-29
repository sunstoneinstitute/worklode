package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// handoffFixture seeds project p1 with lead ada (editor), member bob
// (reporter), carol (editor), dan (science-lead), and erin holding both
// editor and science-lead, all through AddParticipant.
func handoffFixture(t *testing.T) *Store {
	t.Helper()
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "p1", "P1", "LH1"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ada", "bob", "carol", "dan", "erin", "zed"} {
		if err := s.CreateActor(ctx, id, "human", id, false); err != nil {
			t.Fatal(err)
		}
	}
	rows := []struct {
		actor, role string
		lead        bool
	}{
		{"ada", "editor", true}, {"bob", "reporter", false},
		{"carol", "editor", false}, {"dan", "science-lead", false},
		{"erin", "editor", false}, {"erin", "science-lead", false},
	}
	for _, r := range rows {
		if err := addParticipant(t, s, "p1", r.actor, r.role, r.lead, "ada"); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func handoffRecord(t *testing.T, s *Store, typ string, apply func(tx *sql.Tx, eventID int64) error) error {
	t.Helper()
	_, _, err := s.RecordEvent(context.Background(), "cli", nextExt(t), typ, nil, apply)
	return err
}

func requestHandoff(t *testing.T, s *Store, to, by string) (int64, error) {
	t.Helper()
	var hid int64
	err := handoffRecord(t, s, "crew.lead_handoff_requested", func(tx *sql.Tx, eventID int64) error {
		var err error
		hid, err = RequestLeadHandoff(tx, s.Now(), "p1", to, by, eventID)
		return err
	})
	return hid, err
}

func acceptHandoff(t *testing.T, s *Store, hid int64, actor string) (completed bool, err error) {
	t.Helper()
	err = handoffRecord(t, s, "crew.lead_handoff_accepted", func(tx *sql.Tx, eventID int64) error {
		completed, err = AcceptLeadHandoff(tx, s.Now(), hid, actor, eventID)
		return err
	})
	return
}

func authorizeHandoff(t *testing.T, s *Store, hid int64, actor string) (completed bool, err error) {
	t.Helper()
	err = handoffRecord(t, s, "crew.lead_handoff_authorized", func(tx *sql.Tx, eventID int64) error {
		completed, err = AuthorizeLeadHandoff(tx, s.Now(), hid, actor, eventID)
		return err
	})
	return
}

func cancelHandoff(t *testing.T, s *Store, hid int64, by string) error {
	t.Helper()
	return handoffRecord(t, s, "crew.lead_handoff_cancelled", func(tx *sql.Tx, eventID int64) error {
		return CancelLeadHandoff(tx, s.Now(), hid, by, eventID)
	})
}

func assertLead(t *testing.T, s *Store, want string) {
	t.Helper()
	crew, err := s.ListParticipants(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range crew {
		if p.IsLead != (p.ActorID == want) {
			t.Fatalf("lead should be %s: %+v", want, crew)
		}
	}
}

func TestLeadHandoffAcceptance(t *testing.T) {
	t.Parallel()
	s := handoffFixture(t)

	hid, err := requestHandoff(t, s, "bob", "ada")
	if err != nil {
		t.Fatal(err)
	}

	// A bystander cannot accept; each named party accepts once; the second
	// acceptance completes the ceremony and flips the lead.
	if _, err := acceptHandoff(t, s, hid, "carol"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bystander accept: got %v", err)
	}
	if done, err := acceptHandoff(t, s, hid, "ada"); err != nil || done {
		t.Fatalf("outgoing accept: done=%v err=%v", done, err)
	}
	if _, err := acceptHandoff(t, s, hid, "ada"); err == nil || !strings.Contains(err.Error(), "already accepted") {
		t.Fatalf("second outgoing accept: got %v", err)
	}
	if done, err := acceptHandoff(t, s, hid, "bob"); err != nil || !done {
		t.Fatalf("incoming accept must complete: done=%v err=%v", done, err)
	}
	assertLead(t, s, "bob")

	if h, err := s.PendingLeadHandoff(context.Background(), "p1"); err != nil || h != nil {
		t.Fatalf("no ceremony may stay pending: %+v %v", h, err)
	}
	// A completed ceremony takes no further acts.
	if _, err := acceptHandoff(t, s, hid, "bob"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("accept after completion: got %v", err)
	}
}

func TestLeadHandoffJointAuthorization(t *testing.T) {
	t.Parallel()
	s := handoffFixture(t)

	hid, err := requestHandoff(t, s, "bob", "bob")
	if err != nil {
		t.Fatal(err)
	}
	// The incoming lead cannot authorize their own handoff, and a member
	// holding neither role cannot authorize at all.
	if _, err := authorizeHandoff(t, s, hid, "bob"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("incoming authorize: got %v", err)
	}
	if _, err := authorizeHandoff(t, s, hid, "zed"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("non-crew authorize: got %v", err)
	}
	// erin holds both labels: her authorization fills the editor slot, and
	// she cannot fill the science-lead slot too.
	if done, err := authorizeHandoff(t, s, hid, "erin"); err != nil || done {
		t.Fatalf("erin authorize: done=%v err=%v", done, err)
	}
	if _, err := authorizeHandoff(t, s, hid, "erin"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("one person filling both slots: got %v", err)
	}
	// carol is an editor, but that slot is taken.
	if _, err := authorizeHandoff(t, s, hid, "carol"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("filled editor slot: got %v", err)
	}
	if done, err := authorizeHandoff(t, s, hid, "dan"); err != nil || done {
		t.Fatalf("dan authorize without incoming accept must not complete: done=%v err=%v", done, err)
	}
	h, err := s.PendingLeadHandoff(context.Background(), "p1")
	if err != nil || h == nil || h.EditorAuthorizedBy != "erin" || h.ScienceAuthorizedBy != "dan" {
		t.Fatalf("pending handoff: %+v %v", h, err)
	}
	if done, err := acceptHandoff(t, s, hid, "bob"); err != nil || !done {
		t.Fatalf("incoming accept after joint authorization must complete: done=%v err=%v", done, err)
	}
	assertLead(t, s, "bob")
}

func TestLeadHandoffRequestRules(t *testing.T) {
	t.Parallel()
	s := handoffFixture(t)

	if _, err := requestHandoff(t, s, "ada", "ada"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("handoff to the lead: got %v", err)
	}
	if _, err := requestHandoff(t, s, "zed", "ada"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("handoff to a non-member: got %v", err)
	}
	if _, err := requestHandoff(t, s, "bob", "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := requestHandoff(t, s, "carol", "ada"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("second pending ceremony: got %v", err)
	}
}

func TestLeadHandoffCancel(t *testing.T) {
	t.Parallel()
	s := handoffFixture(t)

	hid, err := requestHandoff(t, s, "bob", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if err := cancelHandoff(t, s, hid, "carol"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bystander cancel: got %v", err)
	}
	if err := cancelHandoff(t, s, hid, "bob"); err != nil {
		t.Fatal(err)
	}
	if err := cancelHandoff(t, s, hid, "bob"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("second cancel: got %v", err)
	}
	if _, err := acceptHandoff(t, s, hid, "bob"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("accept after cancel: got %v", err)
	}
	assertLead(t, s, "ada")
	// The slot is free again.
	if _, err := requestHandoff(t, s, "carol", "ada"); err != nil {
		t.Fatalf("request after cancel: %v", err)
	}
}

func TestLeadHandoffThenRemoval(t *testing.T) {
	t.Parallel()
	s := handoffFixture(t)

	err := removeParticipant(t, s, "p1", "ada", "ada")
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "hand the lead off first") {
		t.Fatalf("lead removal: got %v", err)
	}
	hid, err := requestHandoff(t, s, "bob", "ada")
	if err != nil {
		t.Fatal(err)
	}
	// The incoming lead leaving mid-ceremony makes the completing act fail.
	if err := removeParticipant(t, s, "p1", "bob", "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptHandoff(t, s, hid, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptHandoff(t, s, hid, "bob"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("completing for a departed incoming lead: got %v", err)
	}
	if err := cancelHandoff(t, s, hid, "ada"); err != nil {
		t.Fatal(err)
	}

	hid, err = requestHandoff(t, s, "carol", "ada")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"ada", "carol"} {
		if _, err := acceptHandoff(t, s, hid, a); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeParticipant(t, s, "p1", "ada", "carol"); err != nil {
		t.Fatalf("former lead must be removable: %v", err)
	}
	assertLead(t, s, "carol")
}

func closeCrew(t *testing.T, s *Store) error {
	t.Helper()
	return handoffRecord(t, s, "crew.lead_handoff_cancelled", func(tx *sql.Tx, eventID int64) error {
		return CloseCrew(tx, s.Now(), "p1", "ada", eventID)
	})
}

func TestCloseCrewCancelsPendingHandoff(t *testing.T) {
	s := handoffFixture(t)
	ctx := context.Background()
	hid, err := requestHandoff(t, s, "bob", "ada")
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.ListParticipants(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}

	if err := closeCrew(t, s); err != nil {
		t.Fatal(err)
	}
	if h, err := s.PendingLeadHandoff(ctx, "p1"); err != nil || h != nil {
		t.Fatalf("pending handoff after close = %v, %v", h, err)
	}
	var state string
	if err := s.db.QueryRowContext(ctx, `SELECT state FROM crew_lead_handoffs WHERE id = $1`, hid).Scan(&state); err != nil || state != "cancelled" {
		t.Fatalf("handoff state = %q, %v", state, err)
	}
	after, err := s.ListParticipants(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("roster changed: %d rows before, %d after", len(before), len(after))
	}
	assertLead(t, s, "ada")

	countCloses := func() (n int) {
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM state_log
		    WHERE entity_kind = 'project' AND entity_id = 'p1'
		      AND change @> '{"field":"crew","op":"close"}'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return
	}
	if n := countCloses(); n != 1 {
		t.Fatalf("close rows = %d, want 1", n)
	}
	if err := closeCrew(t, s); err != nil {
		t.Fatalf("second CloseCrew: %v", err)
	}
	if n := countCloses(); n != 1 {
		t.Fatalf("close rows after second call = %d, want 1", n)
	}
}
