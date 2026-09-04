package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// createMilestone drives CreateMilestone through RecordEvent, the way every
// caller does, and returns its error.
func createMilestone(s *Store, projectID, title string, position int) (*model.Milestone, error) {
	var m *model.Milestone
	_, _, err := s.RecordEvent(context.Background(), "cli", randomID(), "milestone.created", nil,
		func(tx *sql.Tx, _ int64) error {
			var err error
			m, err = CreateMilestone(tx, s.Now(), projectID, title, position, "ada")
			return err
		})
	if err != nil {
		return nil, err
	}
	return m, nil
}

// TestCreateMilestone covers spec 029 §4's id form (a milestone draws from
// its project's own MILE counter), position 0 appending after the project's
// last milestone, and the two rejections happening before an ordinal is
// burned.
func TestCreateMilestone(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "p1", "Project One", "P1"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := s.EnsureActor(ctx, "ada", "human", "Ada"); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	// A deliverable first, to prove the two counters do not share a sequence.
	if _, err := createDeliverable(s, DeliverableInput{ProjectID: "p1", Name: "output"}); err != nil {
		t.Fatalf("create deliverable: %v", err)
	}

	m1, err := createMilestone(s, "p1", "Internal review", 0)
	if err != nil {
		t.Fatalf("create first milestone: %v", err)
	}
	m2, err := createMilestone(s, "p1", "  Publication  ", 0)
	if err != nil {
		t.Fatalf("create second milestone: %v", err)
	}
	if m1.ID != "P1-MILE-1" || m2.ID != "P1-MILE-2" {
		t.Errorf("ordinals: %s, %s; want P1-MILE-1, P1-MILE-2", m1.ID, m2.ID)
	}
	if m1.Position != 1 || m2.Position != 2 {
		t.Errorf("append positions: %d, %d; want 1, 2", m1.Position, m2.Position)
	}
	if m2.Title != "Publication" {
		t.Errorf("title = %q, want it trimmed", m2.Title)
	}
	if m1.Project != "p1" || m1.CreatedBy != "ada" || m1.CreatedAt.IsZero() {
		t.Errorf("milestone = %+v, want project, creator and clock set", m1)
	}

	// An explicit position is stored as given, not renumbered.
	m3, err := createMilestone(s, "p1", "Kickoff", 1)
	if err != nil {
		t.Fatalf("create with explicit position: %v", err)
	}
	if m3.Position != 1 {
		t.Errorf("explicit position = %d, want 1", m3.Position)
	}

	if _, err := createMilestone(s, "p1", "   ", 0); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("blank title: got %v, want ErrInvalidInput", err)
	}
	if _, err := createMilestone(s, "nope", "Anything", 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown project: got %v, want ErrNotFound", err)
	}
	// Neither rejection burned an ordinal.
	m4, err := createMilestone(s, "p1", "Wrap-up", 0)
	if err != nil {
		t.Fatalf("create after rejections: %v", err)
	}
	if m4.ID != "P1-MILE-4" {
		t.Errorf("id after two rejections = %q, want P1-MILE-4", m4.ID)
	}
	if m4.Position != 3 {
		t.Errorf("append position after explicit 1 = %d, want 3", m4.Position)
	}
}

// TestCreateMilestoneRejectsLongTitle keeps a stray paste out of the row and
// out of a list cell: 200 runes is the bound, counted in runes so a title in
// a non-Latin script is not cut short.
func TestCreateMilestoneRejectsLongTitle(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	if err := s.CreateProject(context.Background(), "p1", "Project One", "P1"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := s.EnsureActor(context.Background(), "ada", "human", "Ada"); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	long := make([]rune, 201)
	for i := range long {
		long[i] = 'é'
	}
	if _, err := createMilestone(s, "p1", string(long), 0); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("201-rune title: got %v, want ErrInvalidInput", err)
	}
	if _, err := createMilestone(s, "p1", string(long[:200]), 0); err != nil {
		t.Errorf("200-rune title: got %v, want it accepted", err)
	}
}

// setMilestone attaches a child row to a milestone directly. The writers that
// do this arrive in later tasks of the milestones plan; a store test is the
// one place a direct write is legitimate.
func setMilestone(t *testing.T, s *Store, table, id, milestoneID string) {
	t.Helper()
	if _, err := s.db.ExecContext(t.Context(),
		`UPDATE `+table+` SET milestone_id = $1 WHERE id = $2`, milestoneID, id); err != nil {
		t.Fatalf("attach %s %s to %s: %v", table, id, milestoneID, err)
	}
}

// setTaskState forces a task's state without walking the transitions, so a
// progress test can pin a bucket in one line.
func setTaskState(t *testing.T, s *Store, id, state string) {
	t.Helper()
	if _, err := s.db.ExecContext(t.Context(),
		`UPDATE tasks SET state = $1 WHERE id = $2`, state, id); err != nil {
		t.Fatalf("set task %s state: %v", id, err)
	}
}

// progressFixture builds the shared shape both reader tests read: project p1
// with two milestones, MILE-1 holding two tasks (one merged, one ready) and
// two deliverables (one reported published, one unreported), MILE-2 holding
// nothing, and one project task attached to no milestone at all.
func progressFixture(t *testing.T) (*Store, *model.Milestone, *model.Milestone) {
	t.Helper()
	s := OpenTestStore(t)
	ctx := t.Context()
	if err := s.CreateProject(ctx, "p1", "Project One", "P1"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := s.EnsureActor(ctx, "ada", "human", "Ada"); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	m1, err := createMilestone(s, "p1", "Internal review", 0)
	if err != nil {
		t.Fatalf("create first milestone: %v", err)
	}
	m2, err := createMilestone(s, "p1", "Publication", 0)
	if err != nil {
		t.Fatalf("create second milestone: %v", err)
	}

	in := TaskInput{ProjectID: "p1", Title: "work", Priority: "medium", Kind: "feature"}
	closed := createTask(t, s, s.Now(), in)
	setMilestone(t, s, "tasks", closed.ID, m1.ID)
	setTaskState(t, s, closed.ID, "merged")
	open := createTask(t, s, s.Now(), in)
	setMilestone(t, s, "tasks", open.ID, m1.ID)
	setTaskState(t, s, open.ID, "ready")
	createTask(t, s, s.Now(), in) // attached to no milestone

	live, err := createDeliverable(s, DeliverableInput{ProjectID: "p1", Name: "datapackage", Artifact: testArtifact})
	if err != nil {
		t.Fatalf("create reported deliverable: %v", err)
	}
	setMilestone(t, s, "deliverables", live.ID, m1.ID)
	fileEvidence(t, s, evidence("deliverable", live.ID, "published", s.Now()))

	quiet, err := createDeliverable(s, DeliverableInput{ProjectID: "p1", Name: "report"})
	if err != nil {
		t.Fatalf("create unreported deliverable: %v", err)
	}
	setMilestone(t, s, "deliverables", quiet.ID, m1.ID)
	return s, m1, m2
}

// TestListMilestonesProgress checks 029 §2's derived progress: a milestone's
// counts come from the children attached to it, an empty milestone derives
// zeroes, and work attached to no milestone counts nowhere.
func TestListMilestonesProgress(t *testing.T) {
	t.Parallel()
	s, _, _ := progressFixture(t)

	got, err := s.ListMilestones(t.Context(), "p1")
	if err != nil {
		t.Fatalf("list milestones: %v", err)
	}
	if len(got) != 2 || got[0].ID != "P1-MILE-1" || got[1].ID != "P1-MILE-2" {
		t.Fatalf("order wrong: %+v", got)
	}
	want := model.MilestoneProgress{TasksTotal: 2, TasksClosed: 1, DeliverablesTotal: 2, DeliverablesLive: 1}
	if got[0].Progress != want {
		t.Errorf("MILE-1 progress = %+v, want %+v", got[0].Progress, want)
	}
	if got[1].Progress != (model.MilestoneProgress{}) {
		t.Errorf("empty milestone must derive zero progress: %+v", got[1].Progress)
	}
	if got[0].Title != "Internal review" || got[0].Position != 1 {
		t.Errorf("row fields = %+v, want the stored title and position", got[0])
	}
}

// TestListMilestonesEmpty pins the ListDeliverables contract: a project with
// no milestones, and a project id that names nothing, are both an empty slice
// rather than an error.
func TestListMilestonesEmpty(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	if err := s.CreateProject(t.Context(), "p1", "Project One", "P1"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	for _, id := range []string{"p1", "nope"} {
		got, err := s.ListMilestones(t.Context(), id)
		if err != nil {
			t.Fatalf("list milestones for %s: %v", id, err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("list milestones for %s = %+v, want an empty slice", id, got)
		}
	}
}

// TestGetMilestone checks the detail reads the same children the progress was
// derived from, excludes work attached to no milestone, and reports an
// unknown id as ErrNotFound.
func TestGetMilestone(t *testing.T) {
	t.Parallel()
	s, m1, m2 := progressFixture(t)

	got, err := s.GetMilestone(t.Context(), m1.ID)
	if err != nil {
		t.Fatalf("get milestone: %v", err)
	}
	if got.ID != m1.ID || got.Title != m1.Title || got.Position != m1.Position {
		t.Errorf("milestone = %+v, want the stored row", got.Milestone)
	}
	want := model.MilestoneProgress{TasksTotal: 2, TasksClosed: 1, DeliverablesTotal: 2, DeliverablesLive: 1}
	if got.Progress != want {
		t.Errorf("progress = %+v, want %+v", got.Progress, want)
	}
	if len(got.Tasks) != 2 {
		t.Fatalf("tasks = %+v, want the two attached ones", got.Tasks)
	}
	if len(got.Deliverables) != 2 {
		t.Fatalf("deliverables = %+v, want the two attached ones", got.Deliverables)
	}
	// The projection the deliverable list already uses, not a second one.
	var reported int
	for _, d := range got.Deliverables {
		if d.ReportedState == "published" {
			reported++
		}
	}
	if reported != 1 {
		t.Errorf("reported states = %+v, want exactly one published", got.Deliverables)
	}

	empty, err := s.GetMilestone(t.Context(), m2.ID)
	if err != nil {
		t.Fatalf("get empty milestone: %v", err)
	}
	if len(empty.Tasks) != 0 || len(empty.Deliverables) != 0 || empty.Progress != (model.MilestoneProgress{}) {
		t.Errorf("empty milestone = %+v, want no children and zero progress", empty)
	}

	if _, err := s.GetMilestone(t.Context(), "P1-MILE-99"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown milestone: got %v, want ErrNotFound", err)
	}
}
