package store

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// extSeq feeds unique external ids so every RecordEvent call in the tests is
// a distinct event (idempotency never kicks in by accident).
var extSeq atomic.Int64

func nextExt(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", t.Name(), extSeq.Add(1))
}

// openTaskStore opens a test store with the fixtures task tests need: a
// project ("horndb") and an actor ("stig").
func openTaskStore(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	ctx := t.Context()
	if err := s.CreateProject(ctx, "horndb", "HornDB", "HDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.CreateActor(ctx, "stig", "human", "Stig", false); err != nil {
		t.Fatalf("CreateActor: %v", err)
	}
	// Assignment requires Crew membership (see requireCrewMember), so the
	// fixture actor leads the fixture project.
	seedParticipant(t, s, "horndb", "stig", "member", true)
	return s
}

var taskTestNow = time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)

func defaultTaskInput() TaskInput {
	return TaskInput{
		ProjectID: "horndb",
		Title:     "a task",
		Body:      "body",
		Priority:  "medium",
		Kind:      "feature",
		CreatedBy: "stig",
	}
}

// createTask drives CreateTask through RecordEvent, the way production code
// will use it.
func createTask(t *testing.T, s *Store, now time.Time, in TaskInput) *model.Task {
	t.Helper()
	var task *model.Task
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.create", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			task, err = CreateTask(tx, now, in, eventID)
			return err
		})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

// transition drives Transition through RecordEvent and returns its error.
func transition(t *testing.T, s *Store, now time.Time, taskID, from, to string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.transition", nil,
		func(tx *sql.Tx, eventID int64) error {
			return Transition(tx, now, taskID, from, to, eventID)
		})
	return err
}

// walkTo moves a task (created in "ready") to the given state via legal
// transitions only.
func walkTo(t *testing.T, s *Store, taskID, state string) {
	t.Helper()
	paths := map[string][]string{
		"ready":         {},
		"in_progress":   {"in_progress"},
		"in_review":     {"in_progress", "in_review"},
		"merged":        {"in_progress", "in_review", "merged"},
		"deployed_dev":  {"in_progress", "in_review", "merged", "deployed_dev"},
		"deployed_prod": {"in_progress", "in_review", "merged", "deployed_dev", "deployed_prod"},
		"released":      {"in_progress", "in_review", "merged", "released"},
		"abandoned":     {"abandoned"},
	}
	steps, ok := paths[state]
	if !ok {
		t.Fatalf("walkTo: no path to state %q", state)
	}
	cur := "ready"
	for _, next := range steps {
		if err := transition(t, s, taskTestNow, taskID, cur, next); err != nil {
			t.Fatalf("walkTo %s: transition %s -> %s: %v", state, cur, next, err)
		}
		cur = next
	}
}

func addEdge(t *testing.T, s *Store, fromTask, toTask, typ string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "edge.add", nil,
		func(tx *sql.Tx, eventID int64) error {
			return AddEdge(tx, taskTestNow, fromTask, toTask, typ, eventID)
		})
	return err
}

func removeEdge(t *testing.T, s *Store, fromTask, toTask, typ string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "edge.remove", nil,
		func(tx *sql.Tx, eventID int64) error {
			return RemoveEdge(tx, fromTask, toTask, typ, eventID)
		})
	return err
}

// insertBareTask inserts a minimal 'horndb' task row with an explicit id,
// bypassing CreateTask's sequential allocator. Used by tests that need
// specific ids (e.g. to pin ordering) rather than whatever the next-task-num
// counter would assign.
func insertBareTask(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO tasks (id, project_id, title, priority, kind, state, created_at, updated_at)
		 VALUES ($1, 'horndb', $1, 'medium', 'feature', 'ready', $2, $2)`,
		id, taskTestNow); err != nil {
		t.Fatalf("insert task %s: %v", id, err)
	}
}

func isBlocked(t *testing.T, s *Store, taskID string) bool {
	t.Helper()
	var blocked bool
	err := s.Tx(t.Context(), func(tx *sql.Tx) error {
		var err error
		blocked, err = IsBlocked(tx, taskID)
		return err
	})
	if err != nil {
		t.Fatalf("IsBlocked(%s): %v", taskID, err)
	}
	return blocked
}

func TestCreateTaskSequentialIDsAndDefaults(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	t1 := createTask(t, s, taskTestNow, defaultTaskInput())
	if t1.ID != "HDB-1" {
		t.Fatalf("first task id: got %q, want HDB-1", t1.ID)
	}
	if t1.State != "ready" {
		t.Fatalf("first task state: got %q, want ready", t1.State)
	}

	in2 := defaultTaskInput()
	in2.Draft = true
	t2 := createTask(t, s, taskTestNow, in2)
	if t2.ID != "HDB-2" {
		t.Fatalf("second task id: got %q, want HDB-2", t2.ID)
	}
	if t2.State != "draft" {
		t.Fatalf("draft task state: got %q, want draft", t2.State)
	}

	// Round-trip through GetTask matches what CreateTask returned.
	got, err := s.GetTask(t.Context(), "HDB-1")
	if err != nil {
		t.Fatalf("GetTask HDB-1: %v", err)
	}
	if !reflect.DeepEqual(got, t1) {
		t.Fatalf("GetTask: got %+v, want %+v", got, t1)
	}
	if got.Project != "horndb" || got.Title != "a task" || got.Body != "body" ||
		got.Priority != "medium" || got.Kind != "feature" || got.CreatedBy != "stig" {
		t.Fatalf("GetTask fields: got %+v", got)
	}
	if !got.CreatedAt.Equal(taskTestNow) || !got.UpdatedAt.Equal(taskTestNow) {
		t.Fatalf("GetTask timestamps: got created=%v updated=%v, want %v", got.CreatedAt, got.UpdatedAt, taskTestNow)
	}
}

// mapRepo maps repo to projectID with the given done_state, creating the
// project if it is not the default "horndb" fixture.
func mapRepo(t *testing.T, s *Store, projectID, repo, doneState string) {
	t.Helper()
	ctx := t.Context()
	if projectID != "horndb" {
		if err := s.CreateProject(ctx, projectID, projectID, strings.ToUpper(projectID)); err != nil {
			t.Fatalf("CreateProject %s: %v", projectID, err)
		}
	}
	if err := s.AddRepo(ctx, projectID, repo); err != nil {
		t.Fatalf("AddRepo %s: %v", repo, err)
	}
	if err := s.SetRepoDoneState(ctx, repo, doneState); err != nil {
		t.Fatalf("SetRepoDoneState %s=%s: %v", repo, doneState, err)
	}
}

// landCommit attributes a commit in repo to taskID *and* records it on that
// repo's default branch, which is what makes the task's closed predicate join
// through the repo's mapping. Both halves are needed: taskClosed reads the
// landed set (task_commits ⋈ main_commits), the same one LandedMainID does.
func landCommit(t *testing.T, s *Store, taskID, repo, sha string) {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "commit.land", nil,
		func(tx *sql.Tx, eventID int64) error {
			if err := InsertTaskCommit(tx, TaskCommit{
				TaskID: taskID, Repo: repo, SHA: sha,
				Source: "branch_push", SeenAt: taskTestNow,
			}); err != nil {
				return err
			}
			_, err := AppendMainCommit(tx, repo, sha, taskTestNow)
			return err
		})
	if err != nil {
		t.Fatalf("land commit %s in %s for %s: %v", sha, repo, taskID, err)
	}
}

// pushBranchCommit attributes a commit to taskID without landing it: the row a
// task-branch push writes for work that may never reach the default branch.
func pushBranchCommit(t *testing.T, s *Store, taskID, repo, sha string) {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "commit.attribute", nil,
		func(tx *sql.Tx, eventID int64) error {
			return InsertTaskCommit(tx, TaskCommit{
				TaskID: taskID, Repo: repo, SHA: sha,
				Source: "branch_push", SeenAt: taskTestNow,
			})
		})
	if err != nil {
		t.Fatalf("attribute commit %s to %s: %v", sha, taskID, err)
	}
}

// TestCreateTaskAboutDoc verifies AboutDoc round-trips through CreateTask and
// GetTask, and that a task created without one reads back 0 (025 §15.4).
func TestCreateTaskAboutDoc(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	seedDocsProject(t, s)

	docID, err := insertDoc(t, s, "spec", 25, "spec-25")
	if err != nil {
		t.Fatalf("insert doc: %v", err)
	}

	in := defaultTaskInput()
	in.Kind = "review"
	in.AboutDoc = docID
	created := createTask(t, s, taskTestNow, in)
	if created.AboutDoc != docID {
		t.Fatalf("created.AboutDoc = %d, want %d", created.AboutDoc, docID)
	}

	got, err := s.GetTask(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.AboutDoc != docID {
		t.Errorf("GetTask about_doc = %d, want %d", got.AboutDoc, docID)
	}

	ordinary := createTask(t, s, taskTestNow, defaultTaskInput())
	got, err = s.GetTask(t.Context(), ordinary.ID)
	if err != nil {
		t.Fatalf("GetTask ordinary: %v", err)
	}
	if got.AboutDoc != 0 {
		t.Errorf("ordinary task about_doc = %d, want 0", got.AboutDoc)
	}
}

// TestOpenTaskForDoc exercises the §5 suppression guard: an open task of the
// matching kind referencing the doc is found; a different kind referencing
// the same doc is not; once the matching task closes, the query finds none.
func TestOpenTaskForDoc(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()
	seedDocsProject(t, s)

	docID, err := insertDoc(t, s, "spec", 26, "spec-26")
	if err != nil {
		t.Fatalf("insert doc: %v", err)
	}

	reviewIn := defaultTaskInput()
	reviewIn.Kind = "review"
	reviewIn.AboutDoc = docID
	review := createTask(t, s, taskTestNow, reviewIn)

	designIn := defaultTaskInput()
	designIn.Kind = "design"
	designIn.AboutDoc = docID
	design := createTask(t, s, taskTestNow, designIn)

	got, err := s.OpenTaskForDoc(ctx, docID, "review")
	if err != nil {
		t.Fatalf("OpenTaskForDoc review: %v", err)
	}
	if got != review.ID {
		t.Fatalf("OpenTaskForDoc review = %q, want %q (the open design task about the same doc must not satisfy the review query)", got, review.ID)
	}

	// The design query is satisfied by the design task, not the review one.
	got, err = s.OpenTaskForDoc(ctx, docID, "design")
	if err != nil {
		t.Fatalf("OpenTaskForDoc design: %v", err)
	}
	if got != design.ID {
		t.Fatalf("OpenTaskForDoc design = %q, want %q", got, design.ID)
	}

	if err := transition(t, s, taskTestNow, review.ID, "ready", "abandoned"); err != nil {
		t.Fatalf("abandon review task: %v", err)
	}
	got, err = s.OpenTaskForDoc(ctx, docID, "review")
	if err != nil {
		t.Fatalf("OpenTaskForDoc review after abandon: %v", err)
	}
	if got != "" {
		t.Fatalf("OpenTaskForDoc review after abandon = %q, want none", got)
	}
}

func TestGetTaskNotFound(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	_, err := s.GetTask(t.Context(), "HDB-999")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTask unknown: want ErrNotFound, got %v", err)
	}
}

func TestCreateTaskConcern(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	in := defaultTaskInput()
	in.Concern = "security"
	task := createTask(t, s, taskTestNow, in)
	if task.Concern != "security" {
		t.Fatalf("CreateTask concern: got %q, want security", task.Concern)
	}
	if task.NeedsDecomposition {
		t.Fatalf("CreateTask needs_decomposition: want false by default")
	}

	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Concern != "security" {
		t.Fatalf("GetTask concern: got %q, want security", got.Concern)
	}
	if got.NeedsDecomposition {
		t.Fatalf("GetTask needs_decomposition: want false by default")
	}
}

func TestCreateTaskNoConcern(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	task := createTask(t, s, taskTestNow, defaultTaskInput())
	if task.Concern != "" {
		t.Fatalf("CreateTask concern: got %q, want empty", task.Concern)
	}

	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Concern != "" {
		t.Fatalf("GetTask concern: got %q, want empty", got.Concern)
	}
}

func TestCreateTaskInvalidConcernRejected(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	in := defaultTaskInput()
	in.Concern = "not-a-concern"
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.create", nil,
		func(tx *sql.Tx, eventID int64) error {
			_, err := CreateTask(tx, taskTestNow, in, eventID)
			return err
		})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("CreateTask invalid concern: want ErrInvalidInput, got %v", err)
	}
}

// TestCreateTaskUsabilityRejectsEmptyAlt pins spec 021 Q021.1: a usability
// task whose body embeds an image with no alt text at all is refused, but
// the same body is fine on a task with a different (or no) concern, and a
// basename-derived alt on a usability task still goes through.
func TestCreateTaskUsabilityRejectsEmptyAlt(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	emptyAlt := "before\n\n![](/blob/" + strings.Repeat("a", 64) + ")\n\nafter\n"

	in := defaultTaskInput()
	in.Concern = "usability"
	in.Body = emptyAlt
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.create", nil,
		func(tx *sql.Tx, eventID int64) error {
			_, err := CreateTask(tx, taskTestNow, in, eventID)
			return err
		})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("CreateTask usability with empty alt: want ErrInvalidInput, got %v", err)
	}

	// The same body on a non-usability concern is not the lint's business.
	other := defaultTaskInput()
	other.Concern = "performance"
	other.Body = emptyAlt
	_, _, err = s.RecordEvent(t.Context(), "cli", nextExt(t), "task.create", nil,
		func(tx *sql.Tx, eventID int64) error {
			_, err := CreateTask(tx, taskTestNow, other, eventID)
			return err
		})
	if err != nil {
		t.Fatalf("CreateTask performance with empty alt: %v", err)
	}

	// A basename-derived alt on a usability task is the accepted fallback.
	fallback := defaultTaskInput()
	fallback.Concern = "usability"
	fallback.Body = "![shot.png](/blob/" + strings.Repeat("b", 64) + ")\n"
	_, _, err = s.RecordEvent(t.Context(), "cli", nextExt(t), "task.create", nil,
		func(tx *sql.Tx, eventID int64) error {
			_, err := CreateTask(tx, taskTestNow, fallback, eventID)
			return err
		})
	if err != nil {
		t.Fatalf("CreateTask usability with basename alt: %v", err)
	}
}

// updateTaskFields drives UpdateTaskFields through RecordEvent.
func updateTaskFields(t *testing.T, s *Store, now time.Time, id string, title, body, priority, concern *string, needsDecomposition *bool) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.update", nil,
		func(tx *sql.Tx, eventID int64) error {
			return UpdateTaskFields(tx, now, id, title, body, priority, concern, nil, needsDecomposition, nil, nil, nil)
		})
	return err
}

func strPtr(s string) *string { return &s }

func boolPtr(b bool) *bool { return &b }

func TestUpdateTaskFieldsConcernAndNeedsDecomposition(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	task := createTask(t, s, taskTestNow, defaultTaskInput())

	// Set concern.
	if err := updateTaskFields(t, s, taskTestNow, task.ID, nil, nil, nil, strPtr("performance"), nil); err != nil {
		t.Fatalf("set concern: %v", err)
	}
	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Concern != "performance" {
		t.Fatalf("concern after set: got %q, want performance", got.Concern)
	}

	// Clear with "".
	if err := updateTaskFields(t, s, taskTestNow, task.ID, nil, nil, nil, strPtr(""), nil); err != nil {
		t.Fatalf("clear concern with \"\": %v", err)
	}
	got, err = s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Concern != "" {
		t.Fatalf("concern after clear with \"\": got %q, want empty", got.Concern)
	}

	// Set again, then clear with "none".
	if err := updateTaskFields(t, s, taskTestNow, task.ID, nil, nil, nil, strPtr("usability"), nil); err != nil {
		t.Fatalf("set concern again: %v", err)
	}
	if err := updateTaskFields(t, s, taskTestNow, task.ID, nil, nil, nil, strPtr("none"), nil); err != nil {
		t.Fatalf("clear concern with none: %v", err)
	}
	got, err = s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Concern != "" {
		t.Fatalf("concern after clear with none: got %q, want empty", got.Concern)
	}

	// needs_decomposition true then false.
	if err := updateTaskFields(t, s, taskTestNow, task.ID, nil, nil, nil, nil, boolPtr(true)); err != nil {
		t.Fatalf("set needs_decomposition true: %v", err)
	}
	got, err = s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !got.NeedsDecomposition {
		t.Fatalf("needs_decomposition: want true")
	}
	if err := updateTaskFields(t, s, taskTestNow, task.ID, nil, nil, nil, nil, boolPtr(false)); err != nil {
		t.Fatalf("set needs_decomposition false: %v", err)
	}
	got, err = s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.NeedsDecomposition {
		t.Fatalf("needs_decomposition: want false")
	}

	// Invalid concern rejected.
	err = updateTaskFields(t, s, taskTestNow, task.ID, nil, nil, nil, strPtr("not-a-concern"), nil)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("update with invalid concern: want ErrInvalidInput, got %v", err)
	}
}

// TestUpdateTaskMilestone pins spec 029 §2's containment rule as it applies
// to UpdateTaskFields: a milestone attach must name a milestone in the
// task's own project, an unknown milestone is ErrInvalidInput just like a
// cross-project one, and detaching (milestone "") is always legal.
func TestUpdateTaskMilestone(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := t.Context()
	if err := s.CreateProject(ctx, "p1", "Project One", "P1"); err != nil {
		t.Fatalf("create project p1: %v", err)
	}
	if err := s.CreateProject(ctx, "p2", "Project Two", "P2"); err != nil {
		t.Fatalf("create project p2: %v", err)
	}
	if err := s.EnsureActor(ctx, "ada", "human", "Ada"); err != nil {
		t.Fatalf("create actor: %v", err)
	}

	mile, err := createMilestone(s, "p1", "Internal review", 0)
	if err != nil {
		t.Fatalf("create milestone: %v", err)
	}

	in1 := defaultTaskInput()
	in1.ProjectID = "p1"
	in1.CreatedBy = "ada"
	t1 := createTask(t, s, taskTestNow, in1)

	in2 := defaultTaskInput()
	in2.ProjectID = "p2"
	in2.CreatedBy = "ada"
	t2 := createTask(t, s, taskTestNow, in2)

	set := func(task, milestone string) error {
		_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.updated", nil,
			func(tx *sql.Tx, _ int64) error {
				return UpdateTaskFields(tx, taskTestNow, task, nil, nil, nil, nil, nil, nil, nil, nil, &milestone)
			})
		return err
	}

	if err := set(t1.ID, mile.ID); err != nil {
		t.Fatalf("attach in same project: %v", err)
	}
	got, err := s.GetTask(ctx, t1.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Milestone != mile.ID {
		t.Fatalf("milestone not stored: %+v", got)
	}

	// 029 §5: containment never crosses a project boundary.
	if err := set(t2.ID, mile.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cross-project attach: got %v, want ErrInvalidInput", err)
	}
	if err := set(t1.ID, "P1-MILE-9"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown milestone: got %v, want ErrInvalidInput", err)
	}

	// Detach is always legal (029 §2).
	if err := set(t1.ID, ""); err != nil {
		t.Fatalf("detach: %v", err)
	}
	got, err = s.GetTask(ctx, t1.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Milestone != "" {
		t.Fatalf("milestone after detach: got %q, want empty", got.Milestone)
	}
}

// TestUpdateTaskFieldsRejectsBlankTitle pins the invariant CreateTask already
// enforces: a task carries a title for its whole life, so an update must not
// be able to blank one out.
func TestUpdateTaskFieldsRejectsBlankTitle(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	in := defaultTaskInput()
	task := createTask(t, s, taskTestNow, in)

	for _, blank := range []string{"", "   ", "\n\t"} {
		err := updateTaskFields(t, s, taskTestNow, task.ID, strPtr(blank), nil, nil, nil, nil)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("update with title %q: want ErrInvalidInput, got %v", blank, err)
		}
	}
	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Title != in.Title {
		t.Fatalf("title after rejected updates = %q, want %q", got.Title, in.Title)
	}

	// A non-blank title still goes through.
	if err := updateTaskFields(t, s, taskTestNow, task.ID, strPtr("Renamed"), nil, nil, nil, nil); err != nil {
		t.Fatalf("update with valid title: %v", err)
	}
	if got, err = s.GetTask(t.Context(), task.ID); err != nil {
		t.Fatalf("GetTask: %v", err)
	} else if got.Title != "Renamed" {
		t.Fatalf("title = %q, want Renamed", got.Title)
	}
}

// TestUpdateTaskFieldsUsabilityRejectsEmptyAlt: an edit that only touches the
// body (concern left nil, meaning "unchanged") still has to honor an
// already-set usability concern, so the lint reads the task's current
// concern rather than only the one passed to this call.
func TestUpdateTaskFieldsUsabilityRejectsEmptyAlt(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	in := defaultTaskInput()
	in.Concern = "usability"
	task := createTask(t, s, taskTestNow, in)

	emptyAlt := "![](/blob/" + strings.Repeat("c", 64) + ")\n"
	err := updateTaskFields(t, s, taskTestNow, task.ID, nil, strPtr(emptyAlt), nil, nil, nil)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("update body with empty alt on usability task: want ErrInvalidInput, got %v", err)
	}
	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Body == emptyAlt {
		t.Fatalf("rejected body was written anyway")
	}

	// A basename-derived alt still goes through.
	fallback := "![shot.png](/blob/" + strings.Repeat("d", 64) + ")\n"
	if err := updateTaskFields(t, s, taskTestNow, task.ID, nil, strPtr(fallback), nil, nil, nil); err != nil {
		t.Fatalf("update body with basename alt: %v", err)
	}

	// Clearing the concern in the same call as the bad body lifts the lint.
	err = updateTaskFields(t, s, taskTestNow, task.ID, nil, strPtr(emptyAlt), nil, strPtr(""), nil)
	if err != nil {
		t.Fatalf("update body+clear concern with empty alt: %v", err)
	}
}

// TestCreateTaskNormalizesPins: CreateTask used to store TaskInput.Skills
// verbatim while SetTaskSkills cleaned them, so a task created with padded,
// duplicated or empty pins produced a "pinned skill not found" warning — one
// of them for the empty string — in every recommendation and brief it served.
// Both paths now share normalizePins, and an over-cap list is rejected rather
// than truncated behind the caller's back.
func TestCreateTaskNormalizesPins(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	in := defaultTaskInput()
	in.Skills = []string{"  tdd  ", "tdd", "", "   ", "tdd", "debugging"}
	task := createTask(t, s, taskTestNow, in)
	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(got.Skills) != 2 || got.Skills[0] != "tdd" || got.Skills[1] != "debugging" {
		t.Fatalf("pins stored verbatim: %+v", got.Skills)
	}
	// The returned Task must agree with what was persisted.
	if len(task.Skills) != 2 || task.Skills[0] != "tdd" {
		t.Fatalf("returned task pins: %+v", task.Skills)
	}

	over := make([]string, maxTaskPins+1)
	for i := range over {
		over[i] = fmt.Sprintf("skill-%02d", i)
	}
	in = defaultTaskInput()
	in.Skills = over
	_, _, err = s.RecordEvent(t.Context(), "cli", nextExt(t), "task.create", nil,
		func(tx *sql.Tx, eventID int64) error {
			_, err := CreateTask(tx, taskTestNow, in, eventID)
			return err
		})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("over-cap pins on create: want ErrInvalidInput, got %v", err)
	}
	if err := setTaskSkills(t, s, taskTestNow, task.ID, over); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("over-cap pins on set: want ErrInvalidInput, got %v", err)
	}
}

// setTaskSkills drives SetTaskSkills through RecordEvent, the way production
// code will use it.
func setTaskSkills(t *testing.T, s *Store, now time.Time, id string, skills []string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.skills_set", nil,
		func(tx *sql.Tx, eventID int64) error {
			return SetTaskSkills(tx, now, id, skills)
		})
	return err
}

// rawSkills reads the tasks.skills column as text, bypassing scanTask's
// nil-normalization, so a test can tell a stored JSON null from "[]".
func rawSkills(t *testing.T, s *Store, id string) string {
	t.Helper()
	var raw string
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT skills::text FROM tasks WHERE id = $1`, id).Scan(&raw); err != nil {
		t.Fatalf("read raw skills: %v", err)
	}
	return raw
}

func TestTaskSkills(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil || len(got.Skills) != 0 {
		t.Fatalf("default skills: %+v err=%v", got.Skills, err)
	}

	// CreateTask persists TaskInput.Skills too, not just SetTaskSkills.
	in := defaultTaskInput()
	in.Skills = []string{"tdd"}
	seeded := createTask(t, s, taskTestNow, in)
	got, err = s.GetTask(t.Context(), seeded.ID)
	if err != nil || len(got.Skills) != 1 || got.Skills[0] != "tdd" {
		t.Fatalf("skills from CreateTask: %+v err=%v", got.Skills, err)
	}

	setNow := taskTestNow.Add(time.Hour)
	if err := setTaskSkills(t, s, setNow, task.ID, []string{"tdd", "debugging"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err = s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(got.Skills) != 2 || got.Skills[0] != "tdd" || got.Skills[1] != "debugging" {
		t.Fatalf("skills: %+v", got.Skills)
	}
	if !got.UpdatedAt.Equal(setNow.UTC()) {
		t.Fatalf("updated_at after set = %v, want %v", got.UpdatedAt, setNow.UTC())
	}

	// Blank entries are dropped and duplicates removed, preserving order.
	if err := setTaskSkills(t, s, setNow, task.ID, []string{"tdd", "", "  ", "tdd", "debugging", "tdd"}); err != nil {
		t.Fatalf("set with blanks and dupes: %v", err)
	}
	got, err = s.GetTask(t.Context(), task.ID)
	if err != nil || len(got.Skills) != 2 || got.Skills[0] != "tdd" || got.Skills[1] != "debugging" {
		t.Fatalf("skills after blanks/dupes: %+v err=%v", got.Skills, err)
	}

	// Clearing with an empty slice replaces, rather than merges.
	if err := setTaskSkills(t, s, setNow, task.ID, []string{}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, err = s.GetTask(t.Context(), task.ID)
	if err != nil || len(got.Skills) != 0 {
		t.Fatalf("skills after clear: %+v err=%v", got.Skills, err)
	}

	// nil normalizes to a jsonb "[]", not a JSON null: SkillsByNames reads
	// this column with jsonb_array_elements_text, which errors on null. The
	// Go-side len() check above is not enough to catch a regression here —
	// read the raw column instead.
	if err := setTaskSkills(t, s, setNow, task.ID, []string{"tdd"}); err != nil {
		t.Fatalf("set again: %v", err)
	}
	clearNow := setNow.Add(time.Hour)
	if err := setTaskSkills(t, s, clearNow, task.ID, nil); err != nil {
		t.Fatalf("clear with nil: %v", err)
	}
	if raw := rawSkills(t, s, task.ID); raw != "[]" {
		t.Fatalf("stored skills after nil clear = %q, want []", raw)
	}
	got, err = s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Skills == nil {
		t.Fatal("Task.Skills is nil, violating its documented never-nil invariant")
	}
	if len(got.Skills) != 0 {
		t.Fatalf("skills after nil clear: %+v", got.Skills)
	}
	if !got.UpdatedAt.Equal(clearNow.UTC()) {
		t.Fatalf("updated_at after nil clear = %v, want %v", got.UpdatedAt, clearNow.UTC())
	}

	if err := setTaskSkills(t, s, clearNow, "WL-999", []string{"tdd"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set skills on unknown task: want ErrNotFound, got %v", err)
	}
}

// TestTaskColumnsEntriesAreCommaFree guards prefixedTaskColumns' naive
// strings.Split(taskColumns, ", "): a call expression or a bare comma in any
// entry silently mangles the alias-qualified SELECT list it builds for
// readyCandidates, surfacing only as a SQLSTATE 42601 deep inside ClaimNext.
func TestTaskColumnsEntriesAreCommaFree(t *testing.T) {
	t.Parallel()
	for _, c := range strings.Split(taskColumns, ", ") {
		if strings.ContainsAny(c, "(),") {
			t.Fatalf("taskColumns entry %q contains a call expression or comma; "+
				"prefixedTaskColumns splits naively on \", \" and would mangle it", c)
		}
	}
}

func TestTaskSecretsRoundTrip(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := t.Context()
	if err := s.CreateProject(ctx, "secproj", "Secrets", "SE"); err != nil {
		t.Fatalf("create project: %v", err)
	}

	var created *model.Task
	_, _, err := s.RecordEvent(ctx, "cli", nextExt(t), "task.create", nil,
		func(tx *sql.Tx, eventID int64) error {
			task, err := CreateTask(tx, s.Now(), TaskInput{
				ProjectID: "secproj", Title: "needs creds", Priority: "medium", Kind: "chore",
				Secrets: []string{"KUBECONFIG_HZDEV", "OPENALEX_API_KEY"},
			}, eventID)
			created = task
			return err
		})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	got, err := s.GetTask(ctx, created.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	want := []string{"KUBECONFIG_HZDEV", "OPENALEX_API_KEY"}
	if !reflect.DeepEqual(got.Secrets, want) {
		t.Fatalf("Secrets = %v; want %v", got.Secrets, want)
	}

	// Update replaces the whole list; empty clears.
	next := []string{"GITHUB_TOKEN"}
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		return UpdateTaskFields(tx, s.Now(), created.ID, nil, nil, nil, nil, &next, nil, nil, nil, nil)
	})
	if err != nil {
		t.Fatalf("update secrets: %v", err)
	}
	got, err = s.GetTask(ctx, created.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if !reflect.DeepEqual(got.Secrets, []string{"GITHUB_TOKEN"}) {
		t.Fatalf("Secrets after update = %v; want [GITHUB_TOKEN]", got.Secrets)
	}
}

func TestTaskSecretsRejectsBadName(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := t.Context()
	if err := s.CreateProject(ctx, "secproj2", "Secrets2", "SF"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	// The store re-checks independently of the API, so the loader-sensitive
	// deny-list (ADR 047) has to hold here too, not only at the HTTP edge.
	for _, name := range []string{"op://Employee/x", "LD_PRELOAD", "DYLD_LIBRARY_PATH", "PATH", "PYTHONPATH"} {
		_, _, err := s.RecordEvent(ctx, "cli", nextExt(t), "task.create", nil,
			func(tx *sql.Tx, eventID int64) error {
				_, err := CreateTask(tx, s.Now(), TaskInput{
					ProjectID: "secproj2", Title: "bad", Priority: "medium", Kind: "chore",
					Secrets: []string{name},
				}, eventID)
				return err
			})
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("create with secret name %q: %v; want ErrInvalidInput", name, err)
		}
	}
}

// checkConstraintValues reads a CHECK constraint's definition back from
// Postgres and returns the quoted values it admits, sorted, alongside the raw
// definition for the failure message. Every enum below is owned by its CHECK
// constraint; the Go copy is the thing that can drift, so the constraint is
// what the copy is compared against.
func checkConstraintValues(t *testing.T, s *Store, table, constraint string) ([]string, string) {
	t.Helper()
	var def string
	err := s.db.QueryRow(
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint
		  WHERE conrelid = $1::regclass AND conname = $2`, table, constraint).Scan(&def)
	if err != nil {
		t.Fatalf("read %s: %v", constraint, err)
	}
	matches := regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(def, -1)
	got := make([]string, 0, len(matches))
	for _, m := range matches {
		got = append(got, m[1])
	}
	slices.Sort(got)
	return got, def
}

// TestPriorityCheckConstraintMatchesModel pins model.TaskPriorities to the
// tasks.priority CHECK constraint that owns it. The Go copy is read by the
// API gate, this package's gate, the new-task form's menu, the plan parser
// and the CLI's --priority completion, so all five move together or this
// fails.
func TestPriorityCheckConstraintMatchesModel(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)

	got, def := checkConstraintValues(t, s, "tasks", "tasks_priority_check")
	want := slices.Sorted(slices.Values(model.TaskPriorities))
	if !slices.Equal(got, want) {
		t.Errorf("tasks_priority_check = %v, want %v\n"+
			"the CHECK constraint and model.TaskPriorities disagree; a migration "+
			"must move with the Go copy\nconstraint: %s", got, want, def)
	}
}

// TestStateCheckConstraintMatchesModel pins model.TaskStates to both of its
// authorities: the tasks.state CHECK constraint (migration 0005), which is
// what Postgres will actually accept, and legalTransitions, which is what the
// store will actually move a task through. A state added to one and not the
// others leaves `lode task --status` offering or omitting it wrongly, so all
// three are held together here.
func TestStateCheckConstraintMatchesModel(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)

	want := slices.Sorted(slices.Values(model.TaskStates))
	got, def := checkConstraintValues(t, s, "tasks", "tasks_state_check")
	if !slices.Equal(got, want) {
		t.Errorf("tasks_state_check = %v, want %v\n"+
			"the CHECK constraint and model.TaskStates disagree; a migration "+
			"must move with the Go copy\nconstraint: %s", got, want, def)
	}
	if machine := allStates(); !slices.Equal(machine, want) {
		t.Errorf("legalTransitions covers %v, want %v — the state machine and "+
			"model.TaskStates disagree", machine, want)
	}
}

// TestKindCheckConstraintMatchesGeneratedKinds closes the direction the API's
// TestTaskKindsAgreeAcrossSources cannot see. That test creates a task of
// every ns.TaskKind, which proves the CHECK admits at least the generated
// set; it cannot prove the CHECK admits nothing else, so a kind deleted from
// ns/concept.ttl would leave the constraint quietly wider than the Go. Here
// the constraint's own definition is read back and compared, so the two are
// pinned in both directions.
func TestKindCheckConstraintMatchesGeneratedKinds(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)

	got, def := checkConstraintValues(t, s, "tasks", "tasks_kind_check")

	if !slices.Equal(got, ns.TaskKinds) {
		t.Errorf("tasks_kind_check = %v, want %v\n"+
			"the CHECK constraint and ns/concept.ttl's wlc:TaskKind disagree; "+
			"a migration must move with the Turtle (025 §17)\nconstraint: %s",
			got, ns.TaskKinds, def)
	}
}

// TestTasksOneActiveRallyPerProject exercises the tasks_one_active_rally
// partial unique index (migration 0069): a second active rally in the same
// project is refused, but closing the first (abandoning it) frees the project
// for a new one. The draft half of that index is covered in rally_test.go.
func TestTasksOneActiveRallyPerProject(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	in := defaultTaskInput()
	in.Kind = "rally"
	first := createTask(t, s, taskTestNow, in)

	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.create", nil,
		func(tx *sql.Tx, eventID int64) error {
			_, err := CreateTask(tx, taskTestNow, in, eventID)
			return err
		})
	if err == nil {
		t.Fatal("expected a unique violation on tasks_one_active_rally, got nil error")
	}
	if !isUniqueViolationOn(err, "tasks_one_active_rally") {
		t.Fatalf("expected tasks_one_active_rally unique violation, got: %v", err)
	}

	walkTo(t, s, first.ID, "abandoned")

	second := createTask(t, s, taskTestNow, in)
	if second.ID == first.ID {
		t.Fatalf("second rally got the same id as the first: %s", second.ID)
	}
}
