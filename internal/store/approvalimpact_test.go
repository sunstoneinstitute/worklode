// Spec 029 §7.1's explicit impact review, end to end: the fan-out a
// designation triggers on the entities governed by what moved, the dependent
// owner's note, and the prior approver's decision.

package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

// mustApproved seeds an entity's approved review row at revision, with an
// optional role requirement — the state a dependent must be in before an
// upstream move can raise an impact question about it.
func mustApproved(t *testing.T, tx *sql.Tx, now time.Time,
	kind, entityID, revision string, role *string) {
	t.Helper()
	mustInsertAwaiting(t, tx, now, kind, entityID, revision, role, nil)
	ap := mustOpenApproval(t, tx, kind, entityID)
	mustResolve(t, tx, ap.ID, "approved", now)
}

// mustGovern records that dependent@revision references upstream@upstreamRev,
// which is what makes it a dependent for the impact fan-out.
func mustGovern(t *testing.T, tx *sql.Tx, now time.Time,
	depKind, depID, depRev, upKind, upID, upRev string) {
	t.Helper()
	if err := InsertGovernedRefs(tx, now, nil, depKind, depID, depRev,
		[]GovernedRef{{Kind: upKind, ID: upID, Revision: upRev}}); err != nil {
		t.Fatal(err)
	}
}

// impactRows returns an entity's impact-kind approvals, newest first.
func impactRows(t *testing.T, tx *sql.Tx, kind, entityID string) []Approval {
	t.Helper()
	all, err := ListApprovalsForEntity(tx, kind, entityID)
	if err != nil {
		t.Fatal(err)
	}
	var out []Approval
	for _, a := range all {
		if a.ReviewKind == "impact" {
			out = append(out, a)
		}
	}
	return out
}

// TestDesignateRevisionFansOutImpactRows: a designation raises exactly one
// impact question per dependent that already approved something, and none
// for a dependent whose own review is still open.
func TestDesignateRevisionFansOutImpactRows(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	tx := mustBegin(t, s)
	now := time.Now().UTC()
	role := "science-leads"

	// The upstream: an open review row, so designating rebinds it.
	mustInsertAwaiting(t, tx, now, "pr", "acme/up#1", "up-a", nil, nil)
	// A decided dependent and an undecided one, both referencing the upstream.
	mustApproved(t, tx, now, "pr", "acme/dep#1", "dep-a", &role)
	mustInsertAwaiting(t, tx, now, "pr", "acme/dep#2", "dep-b", nil, nil)
	mustGovern(t, tx, now, "pr", "acme/dep#1", "dep-a", "pr", "acme/up#1", "up-a")
	mustGovern(t, tx, now, "pr", "acme/dep#2", "dep-b", "pr", "acme/up#1", "up-a")

	out, opened, err := DesignateRevision(tx, now, "pr", "acme/up#1", "up-b")
	if err != nil {
		t.Fatal(err)
	}
	if out != RevisionRebind {
		t.Fatalf("outcome = %v, want RevisionRebind", out)
	}
	if opened != 1 {
		t.Fatalf("opened = %d impact rows, want 1 (only the approved dependent)", opened)
	}
	got := impactRows(t, tx, "pr", "acme/dep#1")
	if len(got) != 1 {
		t.Fatalf("got %d impact rows on the approved dependent, want 1", len(got))
	}
	if want := ImpactRevision("dep-a", "acme/up#1", "up-b"); got[0].SubjectRevision != want {
		t.Errorf("subject_revision = %q, want %q", got[0].SubjectRevision, want)
	}
	if got[0].State != "awaiting" || got[0].RequiredActor != nil ||
		got[0].RequiredRole == nil || *got[0].RequiredRole != role {
		t.Errorf("unexpected impact row: %+v", got[0])
	}
	if n := len(impactRows(t, tx, "pr", "acme/dep#2")); n != 0 {
		t.Errorf("got %d impact rows on the undecided dependent, want 0", n)
	}
}

// TestDesignateRevisionAbsorbsIntoOpenImpactRow: one open impact question per
// dependent, whatever the upstream does next. Both paths are pinned — a
// genuinely new upstream head (which varies subject_revision, so ON CONFLICT
// alone would not catch it) and a redelivery of the head already recorded.
func TestDesignateRevisionAbsorbsIntoOpenImpactRow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, second string }{
		{"a different upstream head", "up-c"},
		{"a redelivery of the same head", "up-b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := OpenTestStore(t)
			tx := mustBegin(t, s)
			now := time.Now().UTC()

			mustInsertAwaiting(t, tx, now, "pr", "acme/up#2", "up-a", nil, nil)
			mustApproved(t, tx, now, "pr", "acme/dep#3", "dep-a", nil)
			mustGovern(t, tx, now, "pr", "acme/dep#3", "dep-a", "pr", "acme/up#2", "up-a")

			if _, opened, err := DesignateRevision(tx, now, "pr", "acme/up#2", "up-b"); err != nil {
				t.Fatal(err)
			} else if opened != 1 {
				t.Fatalf("first designation opened %d, want 1", opened)
			}
			_, opened, err := DesignateRevision(tx, now, "pr", "acme/up#2", tc.second)
			if err != nil {
				t.Fatal(err)
			}
			if opened != 0 {
				t.Errorf("second designation opened %d, want 0 (absorbed)", opened)
			}
			if n := len(impactRows(t, tx, "pr", "acme/dep#3")); n != 1 {
				t.Errorf("got %d impact rows, want 1: the open one absorbs the change", n)
			}
		})
	}
}

// impactFixture seeds an approved dependent, the upstream it references, and
// the designation that raises the impact question; it returns the open impact
// row's id. decider is provisioned as the dependent's prior approver.
func impactFixture(t *testing.T, tx *sql.Tx, upID, depID, decider string) int64 {
	t.Helper()
	now := taskTestNow
	mustInsertAwaiting(t, tx, now, "pr", upID, "up-a", nil, nil)
	mustInsertAwaiting(t, tx, now, "pr", depID, "dep-a", nil, nil)
	ap := mustOpenApproval(t, tx, "pr", depID)
	if err := ResolveApproval(tx, ap.ID, "approved", &decider, now); err != nil {
		t.Fatal(err)
	}
	mustGovern(t, tx, now, "pr", depID, "dep-a", "pr", upID, "up-a")
	if _, opened, err := DesignateRevision(tx, now, "pr", upID, "up-b"); err != nil {
		t.Fatal(err)
	} else if opened != 1 {
		t.Fatalf("fixture opened %d impact rows, want 1", opened)
	}
	rows := impactRows(t, tx, "pr", depID)
	if len(rows) != 1 {
		t.Fatalf("fixture left %d impact rows, want 1", len(rows))
	}
	return rows[0].ID
}

// TestImpactDecideConfirmLeavesDependentUntouched: confirming that the prior
// decision still holds resolves the impact row and touches nothing else.
func TestImpactDecideConfirmLeavesDependentUntouched(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t) // project "horndb", actor "stig"
	tx := mustBegin(t, s)
	id := impactFixture(t, tx, "acme/up#10", "acme/dep#10", "stig")

	got, err := DecideApproval(tx, DecideInput{
		ApprovalID: id, Decision: "approve", ActorID: "stig", Now: taskTestNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "approved" {
		t.Fatalf("impact state = %q, want approved", got.State)
	}
	rows, err := ListApprovalsForEntity(tx, "pr", "acme/dep#10")
	if err != nil {
		t.Fatal(err)
	}
	// Two rows: the original approved review and the now-confirmed impact.
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: a confirmation mints nothing", len(rows))
	}
	for _, r := range rows {
		if r.State != "approved" {
			t.Errorf("row %d state = %q, want approved (untouched)", r.ID, r.State)
		}
	}
}

// TestImpactDecideReopenMintsReviewRow: reopening puts the dependent back in
// review at the revision its approved row bound.
func TestImpactDecideReopenMintsReviewRow(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	tx := mustBegin(t, s)
	id := impactFixture(t, tx, "acme/up#11", "acme/dep#11", "stig")

	if _, err := DecideApproval(tx, DecideInput{
		ApprovalID: id, Decision: "request_changes", ActorID: "stig", Now: taskTestNow,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := ListApprovalsForEntity(tx, "pr", "acme/dep#11")
	if err != nil {
		t.Fatal(err)
	}
	var open []Approval
	for _, r := range rows {
		if r.ReviewKind == "review" && r.State == "awaiting" {
			open = append(open, r)
		}
	}
	if len(open) != 1 || open[0].SubjectRevision != "dep-a" {
		t.Fatalf("review rows = %+v, want exactly one awaiting row at dep-a", rows)
	}
}

// TestImpactDecideRefusesNonPriorApprover: only someone who approved the
// dependent may say whether their own decision still holds (029 §7.1).
func TestImpactDecideRefusesNonPriorApprover(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	if err := s.CreateActor(t.Context(), "ada", "human", "Ada", false); err != nil {
		t.Fatal(err)
	}
	tx := mustBegin(t, s)
	id := impactFixture(t, tx, "acme/up#12", "acme/dep#12", "stig")

	if _, err := DecideApproval(tx, DecideInput{
		ApprovalID: id, Decision: "approve", ActorID: "ada", Now: taskTestNow,
	}); !errors.Is(err, ErrNotPriorApprover) {
		t.Fatalf("err = %v, want ErrNotPriorApprover", err)
	}
	if got := impactRows(t, tx, "pr", "acme/dep#12"); got[0].State != "awaiting" {
		t.Errorf("state = %q after a refused decide, want awaiting", got[0].State)
	}
}

// TestSetImpactNote: the note lands on an open impact row, and is refused on
// a decided one, on an ordinary review row, and when empty.
func TestSetImpactNote(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	tx := mustBegin(t, s)
	id := impactFixture(t, tx, "acme/up#13", "acme/dep#13", "stig")

	if err := SetImpactNote(tx, id, ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("empty note: err = %v, want ErrInvalidInput", err)
	}
	reviewID := openApprovalID(t, tx, "pr", "acme/other#1", "sha1", "")
	if err := SetImpactNote(tx, reviewID, "not an impact row"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("review-kind row: err = %v, want ErrInvalidInput", err)
	}
	if err := SetImpactNote(tx, id, "the schema change does not reach us"); err != nil {
		t.Fatal(err)
	}
	got := impactRows(t, tx, "pr", "acme/dep#13")
	if got[0].Note == nil || *got[0].Note != "the schema change does not reach us" {
		t.Fatalf("note = %v, want the submitted text", got[0].Note)
	}

	if _, err := DecideApproval(tx, DecideInput{
		ApprovalID: id, Decision: "approve", ActorID: "stig", Now: taskTestNow,
	}); err != nil {
		t.Fatal(err)
	}
	if err := SetImpactNote(tx, id, "too late"); !errors.Is(err, ErrApprovalResolved) {
		t.Errorf("decided row: err = %v, want ErrApprovalResolved", err)
	}
}
