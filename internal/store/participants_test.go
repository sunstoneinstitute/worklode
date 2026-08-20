package store

import (
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

// participantSeq feeds a monotonically increasing AddedAt to seedParticipant
// calls, so ordering assertions (lead first, then AddedAt, then actor id)
// have a deterministic AddedAt to sort by regardless of call order across
// parallel tests.
var participantSeq atomic.Int64

var participantsTestEpoch = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

// seedParticipant inserts one project_participants row directly. The
// exported writers (AddParticipant etc.) arrive in a later task; store
// tests are the one place a direct write is legitimate.
func seedParticipant(t *testing.T, s *Store, projectID, actorID, role string, isLead bool) {
	t.Helper()
	addedAt := participantsTestEpoch.Add(time.Duration(participantSeq.Add(1)) * time.Second)
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO project_participants (project_id, actor_id, role, is_lead, added_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		projectID, actorID, role, isLead, addedAt); err != nil {
		t.Fatalf("seed participant %s/%s/%s: %v", projectID, actorID, role, err)
	}
}

func TestProjectsForActor(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "p1", "P1", "PFA1"); err != nil {
		t.Fatalf("CreateProject p1: %v", err)
	}
	if err := s.CreateProject(ctx, "p2", "P2", "PFA2"); err != nil {
		t.Fatalf("CreateProject p2: %v", err)
	}
	if err := s.CreateActor(ctx, "ada", "human", "Ada Lovelace", false); err != nil {
		t.Fatalf("CreateActor ada: %v", err)
	}
	if err := s.CreateActor(ctx, "bob", "human", "Bob Builder", false); err != nil {
		t.Fatalf("CreateActor bob: %v", err)
	}

	// Seed: projects p1, p2; actors ada, bob. ada leads p1 as "editor", is
	// also "reporter" on p1, and is a plain "member" of p2. bob is "member"
	// of p1.
	seedParticipant(t, s, "p1", "ada", "editor", true)
	seedParticipant(t, s, "p1", "ada", "reporter", false)
	seedParticipant(t, s, "p2", "ada", "member", false)
	seedParticipant(t, s, "p1", "bob", "member", false)

	got, err := s.ProjectsForActor(ctx, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 projects, got %d", len(got))
	}
	if got[0].Project.ID != "p1" || !got[0].IsLead ||
		!slices.Equal(got[0].Roles, []string{"editor", "reporter"}) {
		t.Fatalf("p1 row wrong: %+v", got[0])
	}
	if got[1].Project.ID != "p2" || got[1].IsLead {
		t.Fatalf("p2 row wrong: %+v", got[1])
	}
}

// TestProjectsForActorEmpty pins the "empty slice, not an error" contract
// for an actor on no projects.
func TestProjectsForActorEmpty(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateActor(ctx, "loner", "human", "Loner", false); err != nil {
		t.Fatalf("CreateActor: %v", err)
	}

	got, err := s.ProjectsForActor(ctx, "loner")
	if err != nil {
		t.Fatalf("ProjectsForActor: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty slice, got %+v", got)
	}
}

func TestListParticipants(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "p1", "P1", "LP1"); err != nil {
		t.Fatalf("CreateProject p1: %v", err)
	}
	if err := s.CreateActor(ctx, "ada", "human", "Ada Lovelace", false); err != nil {
		t.Fatalf("CreateActor ada: %v", err)
	}
	if err := s.CreateActor(ctx, "bob", "human", "Bob Builder", false); err != nil {
		t.Fatalf("CreateActor bob: %v", err)
	}
	if err := s.CreateActor(ctx, "cleo", "human", "Cleo Copilot", false); err != nil {
		t.Fatalf("CreateActor cleo: %v", err)
	}

	// bob and cleo seed before ada, so insertion order alone would put ada
	// last; ada must still sort first because she is lead. bob then sorts
	// before cleo on AddedAt.
	seedParticipant(t, s, "p1", "bob", "member", false)
	seedParticipant(t, s, "p1", "cleo", "member", false)
	seedParticipant(t, s, "p1", "ada", "editor", true)
	seedParticipant(t, s, "p1", "ada", "reporter", false)

	got, err := s.ListParticipants(ctx, "p1")
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 participants, got %d: %+v", len(got), got)
	}
	if got[0].ActorID != "ada" || !got[0].IsLead || got[0].DisplayName != "Ada Lovelace" ||
		!slices.Equal(got[0].Roles, []string{"editor", "reporter"}) {
		t.Fatalf("ada row wrong: %+v", got[0])
	}
	if got[1].ActorID != "bob" || got[1].IsLead ||
		!slices.Equal(got[1].Roles, []string{"member"}) {
		t.Fatalf("bob row wrong: %+v", got[1])
	}
	if got[2].ActorID != "cleo" || got[2].IsLead ||
		!slices.Equal(got[2].Roles, []string{"member"}) {
		t.Fatalf("cleo row wrong: %+v", got[2])
	}

	// Empty roster: a project with no participants returns an empty slice,
	// not an error.
	if err := s.CreateProject(ctx, "p2", "P2", "LP2"); err != nil {
		t.Fatalf("CreateProject p2: %v", err)
	}
	empty, err := s.ListParticipants(ctx, "p2")
	if err != nil {
		t.Fatalf("ListParticipants p2: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("want empty roster, got %+v", empty)
	}

	// Unknown project -> ErrNotFound, so the API 404s like every other
	// project route.
	if _, err := s.ListParticipants(ctx, "does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
