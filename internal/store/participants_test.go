package store

import (
	"database/sql"
	"errors"
	"slices"
	"strings"
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

// TestOpenWorkOwnedBy pins the removal guard's fact query (spec 029 §6.1):
// only a task that is both assigned to the actor and still open (state not
// in deliveredStateSet) counts as owned work blocking removal.
func TestOpenWorkOwnedBy(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "p1", "P1", "OWB1"); err != nil {
		t.Fatalf("CreateProject p1: %v", err)
	}
	if err := s.CreateActor(ctx, "ada", "human", "Ada Lovelace", false); err != nil {
		t.Fatalf("CreateActor ada: %v", err)
	}

	openAssigned := createTask(t, s, taskTestNow, TaskInput{
		ProjectID: "p1", Title: "open, assigned to ada", Body: "b",
		Priority: "medium", Kind: "feature", CreatedBy: "ada",
	})
	if err := assignTask(t, s, taskTestNow, openAssigned.ID, "ada"); err != nil {
		t.Fatalf("assign openAssigned: %v", err)
	}

	deliveredAssigned := createTask(t, s, taskTestNow, TaskInput{
		ProjectID: "p1", Title: "merged, assigned to ada", Body: "b",
		Priority: "medium", Kind: "feature", CreatedBy: "ada",
	})
	if err := assignTask(t, s, taskTestNow, deliveredAssigned.ID, "ada"); err != nil {
		t.Fatalf("assign deliveredAssigned: %v", err)
	}
	walkTo(t, s, deliveredAssigned.ID, "merged")

	createTask(t, s, taskTestNow, TaskInput{
		ProjectID: "p1", Title: "open, unassigned", Body: "b",
		Priority: "medium", Kind: "feature", CreatedBy: "ada",
	})

	got, err := s.OpenWorkOwnedBy(ctx, "p1", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "task" {
		t.Fatalf("want exactly the open assigned task, got %+v", got)
	}
	if got[0].ID != openAssigned.ID || got[0].Title != "open, assigned to ada" || got[0].State != "ready" {
		t.Fatalf("wrong task returned: %+v", got[0])
	}
}

// addParticipant drives AddParticipant the way production does: inside a
// RecordEvent transaction under the "crew.member_added" event type (spec 029
// §8.4), so the test exercises the same commit boundary the API handler
// does.
func addParticipant(t *testing.T, s *Store, projectID, actor, role string, lead bool, by string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "crew.member_added", nil,
		func(tx *sql.Tx, eventID int64) error {
			return AddParticipant(tx, s.Now(), projectID, actor, role, lead, by, eventID)
		})
	return err
}

func TestAddParticipant(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "p1", "P1", "AP1"); err != nil {
		t.Fatalf("CreateProject p1: %v", err)
	}
	for _, id := range []string{"ada", "bob"} {
		if err := s.CreateActor(ctx, id, "human", strings.ToUpper(id[:1])+id[1:], false); err != nil {
			t.Fatalf("CreateActor %s: %v", id, err)
		}
	}

	add := func(actor, role string, lead bool) error {
		return addParticipant(t, s, "p1", actor, role, lead, "ada")
	}

	if err := add("ada", "editor", true); err != nil {
		t.Fatal(err)
	}
	if err := add("bob", "reporter", false); err != nil {
		t.Fatal(err)
	}
	// One actor, several role labels (029 §6.1).
	if err := add("bob", "data-scientist", false); err != nil {
		t.Fatal(err)
	}
	// The same role twice is invalid input.
	if err := add("bob", "reporter", false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate role: got %v", err)
	}
	// A second lead is refused (lead handoff is deferred).
	if err := add("bob", "co-lead", true); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("second lead: got %v", err)
	}

	// The roster reads back what was written: ada leads with one role, bob
	// holds two, sorted.
	crew, err := s.ListParticipants(ctx, "p1")
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	if len(crew) != 2 {
		t.Fatalf("crew = %+v, want 2 members", crew)
	}
	if crew[0].ActorID != "ada" || !crew[0].IsLead || !slices.Equal(crew[0].Roles, []string{"editor"}) {
		t.Fatalf("crew[0] = %+v, want ada, lead, [editor]", crew[0])
	}
	if crew[1].ActorID != "bob" || crew[1].IsLead ||
		!slices.Equal(crew[1].Roles, []string{"data-scientist", "reporter"}) {
		t.Fatalf("crew[1] = %+v, want bob, not lead, [data-scientist reporter]", crew[1])
	}

	// added_by is stored, and an empty one stores NULL rather than an
	// invented actor.
	if err := addParticipant(t, s, "p1", "bob", "observer", false, ""); err != nil {
		t.Fatalf("add with no acting actor: %v", err)
	}
	var by sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT added_by FROM project_participants WHERE project_id = 'p1' AND actor_id = 'bob' AND role = 'observer'`,
	).Scan(&by); err != nil {
		t.Fatalf("read added_by: %v", err)
	}
	if by.Valid {
		t.Fatalf("added_by = %q, want NULL", by.String)
	}

	// Validation and existence checks.
	if err := add("ada", "   ", false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("blank role: got %v", err)
	}
	if err := add("ada", strings.Repeat("x", maxParticipantRole+1), false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("over-long role: got %v", err)
	}
	if err := add("nosuch", "member", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown actor: got %v", err)
	}
	if err := addParticipant(t, s, "nosuch", "ada", "member", false, "ada"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project: got %v", err)
	}
	// A trimmed role is stored trimmed, so " editor " collides with the
	// existing "editor" row rather than creating a second one.
	if err := add("ada", "  editor  ", false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("untrimmed duplicate role: got %v", err)
	}
}
