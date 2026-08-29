package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
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

// TestListParticipantsAllProjects pins ListParticipants(ctx, "") — the
// bulk form Home's card grid needs to avoid an N+1 read (spec 029, plan D
// task 5). It must return every project's roster in one query, each row
// carrying its own ProjectID, grouped project id ascending with lead first
// within a project. bob holds a role on both p1 and p2 with a different
// label on each: folding on actor id alone (instead of (project id, actor
// id)) would merge his two rows into one and lose one of the roles, which
// is exactly the bug this test exists to catch.
func TestListParticipantsAllProjects(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "p1", "P1", "LPA1"); err != nil {
		t.Fatalf("CreateProject p1: %v", err)
	}
	if err := s.CreateProject(ctx, "p2", "P2", "LPA2"); err != nil {
		t.Fatalf("CreateProject p2: %v", err)
	}
	for _, id := range []string{"ada", "bob"} {
		if err := s.CreateActor(ctx, id, "human", strings.ToUpper(id[:1])+id[1:], false); err != nil {
			t.Fatalf("CreateActor %s: %v", id, err)
		}
	}

	if err := addParticipant(t, s, "p1", "ada", "editor", true, "ada"); err != nil {
		t.Fatal(err)
	}
	if err := addParticipant(t, s, "p1", "bob", "member", false, "ada"); err != nil {
		t.Fatal(err)
	}
	// bob again on p2, with a different role than on p1.
	if err := addParticipant(t, s, "p2", "bob", "engineer", false, "ada"); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListParticipants(ctx, "")
	if err != nil {
		t.Fatalf("ListParticipants(\"\"): %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 rows across both projects, got %d: %+v", len(got), got)
	}

	// Grouped p1 before p2; within p1, the lead (ada) comes first.
	if got[0].ProjectID != "p1" || got[0].ActorID != "ada" || !got[0].IsLead ||
		!slices.Equal(got[0].Roles, []string{"editor"}) {
		t.Fatalf("row 0 wrong: %+v", got[0])
	}
	if got[1].ProjectID != "p1" || got[1].ActorID != "bob" || got[1].IsLead ||
		!slices.Equal(got[1].Roles, []string{"member"}) {
		t.Fatalf("row 1 wrong: %+v", got[1])
	}
	if got[2].ProjectID != "p2" || got[2].ActorID != "bob" || got[2].IsLead ||
		!slices.Equal(got[2].Roles, []string{"engineer"}) {
		t.Fatalf("row 2 wrong: %+v", got[2])
	}

	// Every row must carry its own ProjectID (not left blank).
	for i, row := range got {
		if row.ProjectID == "" {
			t.Fatalf("row %d has empty ProjectID: %+v", i, row)
		}
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
	seedParticipant(t, s, "p1", "ada", "member", false)

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
			return AddParticipant(tx, s.Now(), projectID, actor, role, lead, false, by, eventID)
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
	if err := add("bob", "editor", true); !errors.Is(err, ErrInvalidInput) {
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
	if err := addParticipant(t, s, "p1", "bob", "domain-expert", false, ""); err != nil {
		t.Fatalf("add with no acting actor: %v", err)
	}
	var by sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT added_by FROM project_participants WHERE project_id = 'p1' AND actor_id = 'bob' AND role = 'domain-expert'`,
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

// TestAddParticipantDeputy covers the deputy designation (spec 029 §6.1): it
// is mutually exclusive with lead, at most one per project, and read back as
// a virtual "acting-lead" entry folded into Roles rather than a stored role.
func TestAddParticipantDeputy(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "p1", "P1", "AD1"); err != nil {
		t.Fatalf("CreateProject p1: %v", err)
	}
	for _, id := range []string{"ada", "bob", "cleo"} {
		if err := s.CreateActor(ctx, id, "human", strings.ToUpper(id[:1])+id[1:], false); err != nil {
			t.Fatalf("CreateActor %s: %v", id, err)
		}
	}

	addDeputy := func(actor, role string, lead, deputy bool) error {
		_, _, err := s.RecordEvent(ctx, "cli", nextExt(t), "crew.member_added", nil,
			func(tx *sql.Tx, eventID int64) error {
				return AddParticipant(tx, s.Now(), "p1", actor, role, lead, deputy, "ada", eventID)
			})
		return err
	}

	if err := addDeputy("ada", "editor", true, false); err != nil {
		t.Fatalf("add ada as lead: %v", err)
	}
	// Lead and deputy on the same add is refused.
	if err := addDeputy("bob", "reporter", true, true); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("lead and deputy together: got %v", err)
	}
	if err := addDeputy("bob", "reporter", false, true); err != nil {
		t.Fatalf("add bob as deputy: %v", err)
	}
	// A second deputy is refused.
	if err := addDeputy("cleo", "member", false, true); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("second deputy: got %v", err)
	}
	// acting-lead cannot be set as a role directly — it stays outside the
	// fixed vocabulary.
	if err := addDeputy("cleo", "acting-lead", false, false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("role=acting-lead: got %v", err)
	}
	// The lead/deputy exclusion is per actor, not per row: ada already leads
	// (from an earlier row) and bob is already deputy, so a second role row
	// claiming the other flag is refused too.
	if err := addDeputy("ada", "member", false, true); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("existing lead adding a deputy row: got %v", err)
	}
	if err := addDeputy("bob", "domain-expert", true, false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("existing deputy adding a lead row: got %v", err)
	}

	crew, err := s.ListParticipants(ctx, "p1")
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	if len(crew) != 2 {
		t.Fatalf("crew = %+v, want 2 members", crew)
	}
	bobFound := false
	for _, m := range crew {
		if m.ActorID != "bob" {
			continue
		}
		bobFound = true
		if !m.IsDeputy {
			t.Fatalf("bob.IsDeputy = %v, want true", m.IsDeputy)
		}
		if !slices.Equal(m.Roles, []string{"acting-lead", "reporter"}) {
			t.Fatalf("bob.Roles = %+v, want [acting-lead reporter]", m.Roles)
		}
	}
	if !bobFound {
		t.Fatalf("crew = %+v, want bob present", crew)
	}
}

// removeParticipant drives RemoveParticipant the way production does:
// inside a RecordEvent transaction under the "crew.member_removed" event
// type (spec 029 §8.4).
func removeParticipant(t *testing.T, s *Store, projectID, actor, by string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "crew.member_removed", nil,
		func(tx *sql.Tx, eventID int64) error {
			return RemoveParticipant(tx, s.Now(), projectID, actor, by, eventID)
		})
	return err
}

// TestRemoveParticipantGuard covers spec 029 §6.1's removal guard in the
// order the rules fire: an unknown member, the lead, open work owned by the
// member, and the removal itself clearing every role row at once.
func TestRemoveParticipantGuard(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "p1", "P1", "RP1"); err != nil {
		t.Fatalf("CreateProject p1: %v", err)
	}
	for _, id := range []string{"ada", "bob"} {
		if err := s.CreateActor(ctx, id, "human", strings.ToUpper(id[:1])+id[1:], false); err != nil {
			t.Fatalf("CreateActor %s: %v", id, err)
		}
	}
	if err := addParticipant(t, s, "p1", "ada", "editor", true, "ada"); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"reporter", "data-scientist"} {
		if err := addParticipant(t, s, "p1", "bob", role, false, "ada"); err != nil {
			t.Fatal(err)
		}
	}
	task := createTask(t, s, taskTestNow, TaskInput{
		ProjectID: "p1", Title: "open, assigned to bob", Body: "b",
		Priority: "medium", Kind: "feature", CreatedBy: "ada",
	})
	if err := assignTask(t, s, taskTestNow, task.ID, "bob"); err != nil {
		t.Fatalf("assign task to bob: %v", err)
	}

	remove := func(actor string) error { return removeParticipant(t, s, "p1", actor, "ada") }

	// An actor who is not on the crew at all.
	if err := remove("nosuch"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removing a non-member: got %v, want ErrNotFound", err)
	}

	// Open work blocks the removal, and the message names the item so the
	// caller can render the responsibility list (032 §6).
	err := remove("bob")
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "task") {
		t.Fatalf("open work must block removal with the item listed: %v", err)
	}
	if !strings.Contains(err.Error(), task.ID+" (task, ready)") {
		t.Fatalf("message must list the item as <id> (<kind>, <state>): %v", err)
	}

	// Unassign the task; removal now succeeds and clears every role row.
	if err := unassignTask(t, s, taskTestNow, task.ID); err != nil {
		t.Fatalf("unassign task: %v", err)
	}
	if err := remove("bob"); err != nil {
		t.Fatal(err)
	}
	crew, err := s.ListParticipants(ctx, "p1")
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	if len(crew) != 1 || crew[0].ActorID != "ada" {
		t.Fatalf("crew = %+v, want only ada", crew)
	}

	// Removing the same member twice is ErrNotFound, not a silent no-op.
	if err := remove("bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second removal: got %v, want ErrNotFound", err)
	}

	// The lead is never removable while handoff is deferred.
	if err := remove("ada"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("lead removal: got %v", err)
	}

	// The removal is in the change log, naming every role row it cleared.
	var change []byte
	if err := s.db.QueryRowContext(ctx,
		`SELECT change FROM state_log
		  WHERE entity_kind = 'project' AND entity_id = 'p1'
		    AND change->>'op' = 'remove'
		  ORDER BY id DESC LIMIT 1`).Scan(&change); err != nil {
		t.Fatalf("read state_log: %v", err)
	}
	var logged struct {
		Actor string   `json:"actor"`
		Roles []string `json:"roles"`
		By    string   `json:"by"`
	}
	if err := json.Unmarshal(change, &logged); err != nil {
		t.Fatalf("decode change %s: %v", change, err)
	}
	if logged.Actor != "bob" || logged.By != "ada" ||
		!slices.Equal(logged.Roles, []string{"data-scientist", "reporter"}) {
		t.Fatalf("change = %+v, want bob/[data-scientist reporter]/ada", logged)
	}
}

// TestParticipantRolesMatchMigration holds the Go vocabulary and migration
// 0046's CHECK constraint together (WL-297): a role added to one and not the
// other would let the store admit what Postgres refuses, or vice versa.
func TestParticipantRolesMatchMigration(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(MigrationsDirForTests(), "0046_participant_role_check.up.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	m := regexp.MustCompile(`ADD CONSTRAINT project_participants_role_check\s*\n\s*CHECK \(role IN \(([^)]+)\)\)`).
		FindSubmatch(data)
	if m == nil {
		t.Fatalf("migration 0046 carries no recognizable role CHECK")
	}
	inMigration := map[string]bool{}
	for _, q := range regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(string(m[1]), -1) {
		inMigration[q[1]] = true
	}
	if len(inMigration) != len(validParticipantRoles) {
		t.Fatalf("migration CHECK has %d roles, store has %d", len(inMigration), len(validParticipantRoles))
	}
	for role := range validParticipantRoles {
		if !inMigration[role] {
			t.Errorf("role %q is in validParticipantRoles but not migration 0046's CHECK", role)
		}
	}
	// The ordered list is the same set.
	if got := ParticipantRoles(); len(got) != len(validParticipantRoles) {
		t.Fatalf("ParticipantRoles() has %d entries, want %d", len(got), len(validParticipantRoles))
	}
	for _, role := range ParticipantRoles() {
		if !validParticipantRoles[role] {
			t.Errorf("ParticipantRoles() lists %q, which validParticipantRoles rejects", role)
		}
	}
}

// addDeputyTo adds one deputy-designated member.
func addDeputyTo(t *testing.T, s *Store, projectID, actor, role string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "crew.member_added", nil,
		func(tx *sql.Tx, eventID int64) error {
			return AddParticipant(tx, s.Now(), projectID, actor, role, false, true, actor, eventID)
		})
	return err
}

// WL-338: the deputy sorts right after the lead, ahead of ordinary members
// whose added_at would otherwise place them earlier.
func TestListParticipantsSortsDeputyAfterLead(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "p1", "P1", "LP1"); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"ada", "bob", "dana"} {
		if err := s.CreateActor(ctx, a, "human", a, false); err != nil {
			t.Fatal(err)
		}
	}

	// bob (ordinary) joins first, dana (deputy) last: added_at alone would
	// put bob second.
	seedParticipant(t, s, "p1", "ada", "editor", true)
	seedParticipant(t, s, "p1", "bob", "member", false)
	if err := addDeputyTo(t, s, "p1", "dana", "member"); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListParticipants(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, p := range got {
		order = append(order, p.ActorID)
	}
	want := []string{"ada", "dana", "bob"}
	if !slices.Equal(order, want) {
		t.Fatalf("roster order = %v, want %v (lead, deputy, then members)", order, want)
	}
}
