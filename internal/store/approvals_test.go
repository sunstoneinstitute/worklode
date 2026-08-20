package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

// mustBegin starts a tx against s and registers a rollback cleanup, matching
// the pattern the other tx-scoped function tests in this package use.
func mustBegin(t *testing.T, s *Store) *sql.Tx {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func TestInsertAwaitingApprovalIsIdempotent(t *testing.T) {
	s := OpenTestStore(t)
	tx := mustBegin(t, s)
	now := time.Now().UTC()
	for range 2 { // second insert must be a silent no-op
		if err := InsertAwaitingApproval(tx, now,
			"pr", "acme/site#7", "abc123", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	a, err := OpenApprovalForEntity(tx, "pr", "acme/site#7")
	if err != nil {
		t.Fatal(err)
	}
	if a.State != "awaiting" || a.SubjectRevision != "abc123" {
		t.Errorf("got state %q revision %q", a.State, a.SubjectRevision)
	}

	var count int
	if err := tx.QueryRow(
		`SELECT count(*) FROM approvals WHERE entity_kind = 'pr' AND entity_id = 'acme/site#7'`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("got %d rows, want 1 (second insert must not duplicate)", count)
	}
}

func TestApprovalResolveApprovedClosesOpenLookup(t *testing.T) {
	s := OpenTestStore(t)
	tx := mustBegin(t, s)
	now := time.Now().UTC()
	if err := InsertAwaitingApproval(tx, now, "pr", "acme/site#8", "sha1", nil, nil); err != nil {
		t.Fatal(err)
	}
	a, err := OpenApprovalForEntity(tx, "pr", "acme/site#8")
	if err != nil {
		t.Fatal(err)
	}
	if err := ResolveApproval(tx, a.ID, "approved", nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenApprovalForEntity(tx, "pr", "acme/site#8"); !errors.Is(err, ErrNotFound) {
		t.Errorf("got err %v, want ErrNotFound", err)
	}
}

func TestApprovalResolveChangesRequestedStaysOpen(t *testing.T) {
	s := OpenTestStore(t)
	tx := mustBegin(t, s)
	now := time.Now().UTC()
	if err := InsertAwaitingApproval(tx, now, "pr", "acme/site#9", "sha1", nil, nil); err != nil {
		t.Fatal(err)
	}
	a, err := OpenApprovalForEntity(tx, "pr", "acme/site#9")
	if err != nil {
		t.Fatal(err)
	}
	if err := ResolveApproval(tx, a.ID, "changes_requested", nil, now); err != nil {
		t.Fatal(err)
	}
	got, err := OpenApprovalForEntity(tx, "pr", "acme/site#9")
	if err != nil {
		t.Fatalf("still-open lookup: %v", err)
	}
	if got.State != "changes_requested" {
		t.Errorf("got state %q, want changes_requested", got.State)
	}
}

func TestReopenApprovalClearsResolutionAndNoOpsOnApproved(t *testing.T) {
	s := OpenTestStore(t)
	tx := mustBegin(t, s)
	now := time.Now().UTC()
	if err := s.CreateActor(t.Context(), "reviewer-1", "human", "Reviewer", false); err != nil {
		t.Fatal(err)
	}
	reviewer := "reviewer-1"

	if err := InsertAwaitingApproval(tx, now, "pr", "acme/site#10", "sha1", nil, nil); err != nil {
		t.Fatal(err)
	}
	a, err := OpenApprovalForEntity(tx, "pr", "acme/site#10")
	if err != nil {
		t.Fatal(err)
	}
	if err := ResolveApproval(tx, a.ID, "changes_requested", &reviewer, now); err != nil {
		t.Fatal(err)
	}
	resolved, err := OpenApprovalForEntity(tx, "pr", "acme/site#10")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ResolvingActor == nil || *resolved.ResolvingActor != reviewer || resolved.ResolvedAt == nil {
		t.Fatalf("got resolvingActor %v resolvedAt %v, want %q/non-nil (ResolveApproval must stamp both)",
			resolved.ResolvingActor, resolved.ResolvedAt, reviewer)
	}
	if err := ReopenApproval(tx, a.ID); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenApprovalForEntity(tx, "pr", "acme/site#10")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.State != "awaiting" || reopened.ResolvingActor != nil || reopened.ResolvedAt != nil {
		t.Errorf("got state %q resolvingActor %v resolvedAt %v, want awaiting/nil/nil",
			reopened.State, reopened.ResolvingActor, reopened.ResolvedAt)
	}

	// Approved is not reopenable: ReopenApproval must leave it untouched.
	if err := ResolveApproval(tx, a.ID, "approved", &reviewer, now); err != nil {
		t.Fatal(err)
	}
	if err := ReopenApproval(tx, a.ID); err != nil {
		t.Fatal(err)
	}
	var stillState string
	if err := tx.QueryRow(`SELECT state FROM approvals WHERE id = $1`, a.ID).Scan(&stillState); err != nil {
		t.Fatal(err)
	}
	if stillState != "approved" {
		t.Errorf("got state %q, want approved (no-op)", stillState)
	}
}

func TestGetApproval(t *testing.T) {
	s := OpenTestStore(t)
	now := time.Now().UTC()

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := InsertAwaitingApproval(tx, now, "pr", "acme/site#11", "sha1", nil, nil); err != nil {
		t.Fatal(err)
	}
	a, err := OpenApprovalForEntity(tx, "pr", "acme/site#11")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetApproval(t.Context(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EntityID != "acme/site#11" || got.State != "awaiting" {
		t.Errorf("got entityID %q state %q", got.EntityID, got.State)
	}

	if _, err := s.GetApproval(t.Context(), a.ID+1_000_000); !errors.Is(err, ErrNotFound) {
		t.Errorf("got err %v, want ErrNotFound", err)
	}
}

func TestSetRequiredActor(t *testing.T) {
	s := OpenTestStore(t)
	tx := mustBegin(t, s)
	now := time.Now().UTC()
	if err := s.CreateActor(t.Context(), "actor-a", "human", "A", false); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateActor(t.Context(), "actor-b", "human", "B", false); err != nil {
		t.Fatal(err)
	}

	if err := InsertAwaitingApproval(tx, now, "pr", "acme/site#12", "sha1", nil, nil); err != nil {
		t.Fatal(err)
	}
	a, err := OpenApprovalForEntity(tx, "pr", "acme/site#12")
	if err != nil {
		t.Fatal(err)
	}

	// required_actor starts NULL: fills.
	if err := SetRequiredActor(tx, a.ID, "actor-a"); err != nil {
		t.Fatal(err)
	}
	got, err := OpenApprovalForEntity(tx, "pr", "acme/site#12")
	if err != nil {
		t.Fatal(err)
	}
	if got.RequiredActor == nil || *got.RequiredActor != "actor-a" {
		t.Fatalf("got requiredActor %v, want actor-a", got.RequiredActor)
	}

	// Already set: must not overwrite.
	if err := SetRequiredActor(tx, a.ID, "actor-b"); err != nil {
		t.Fatal(err)
	}
	got2, err := OpenApprovalForEntity(tx, "pr", "acme/site#12")
	if err != nil {
		t.Fatal(err)
	}
	if got2.RequiredActor == nil || *got2.RequiredActor != "actor-a" {
		t.Errorf("got requiredActor %v, want unchanged actor-a", got2.RequiredActor)
	}
}

func TestActorIDForGitHubLogin(t *testing.T) {
	s := OpenTestStore(t)
	if err := s.UpsertHumanActor(t.Context(), "octo-1", "Octocat", false, "Octocat", "", nil); err != nil {
		t.Fatal(err)
	}
	tx := mustBegin(t, s)

	got, err := ActorIDForGitHubLogin(tx, "octocat")
	if err != nil {
		t.Fatal(err)
	}
	if got != "octo-1" {
		t.Errorf("got %q, want octo-1 (case-insensitive match)", got)
	}

	miss, err := ActorIDForGitHubLogin(tx, "nobody")
	if err != nil {
		t.Fatal(err)
	}
	if miss != "" {
		t.Errorf("got %q, want empty string for unknown login", miss)
	}
}

func TestPREntityID(t *testing.T) {
	got := PREntityID("sunstoneinstitute/worklode", 41)
	want := "sunstoneinstitute/worklode#41"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
