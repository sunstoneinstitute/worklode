package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
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
func createTask(t *testing.T, s *Store, now time.Time, in TaskInput) *Task {
	t.Helper()
	var task *Task
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.create", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			task, err = CreateTask(tx, now, in)
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
			return AddEdge(tx, taskTestNow, fromTask, toTask, typ)
		})
	return err
}

func removeEdge(t *testing.T, s *Store, fromTask, toTask, typ string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "edge.remove", nil,
		func(tx *sql.Tx, eventID int64) error {
			return RemoveEdge(tx, fromTask, toTask, typ)
		})
	return err
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
	if got.ProjectID != "horndb" || got.Title != "a task" || got.Body != "body" ||
		got.Priority != "medium" || got.Kind != "feature" || got.CreatedBy != "stig" {
		t.Fatalf("GetTask fields: got %+v", got)
	}
	if !got.CreatedAt.Equal(taskTestNow) || !got.UpdatedAt.Equal(taskTestNow) {
		t.Fatalf("GetTask timestamps: got created=%v updated=%v, want %v", got.CreatedAt, got.UpdatedAt, taskTestNow)
	}
}

func TestTransitionLegal(t *testing.T) {
	s := openTaskStore(t)

	cases := []struct{ from, to string }{
		{"draft", "ready"},
		{"ready", "in_progress"},
		{"in_progress", "in_review"},
		{"in_progress", "ready"},
		{"in_review", "in_progress"},
		{"ready", "merged"},
		{"in_progress", "merged"},
		{"in_review", "merged"},
		{"merged", "deployed_dev"},
		{"merged", "deployed_prod"},
		{"merged", "released"},
		{"deployed_dev", "deployed_prod"},
		{"deployed_dev", "released"},
		{"draft", "abandoned"},
		{"ready", "abandoned"},
		{"in_progress", "abandoned"},
		{"in_review", "abandoned"},
		{"merged", "ready"},
		{"deployed_dev", "ready"},
		{"deployed_prod", "ready"},
		{"released", "ready"},
		{"abandoned", "ready"},
	}
	for _, c := range cases {
		in := defaultTaskInput()
		in.Draft = c.from == "draft"
		task := createTask(t, s, taskTestNow, in)
		if !in.Draft {
			walkTo(t, s, task.ID, c.from)
		}
		if err := transition(t, s, taskTestNow, task.ID, c.from, c.to); err != nil {
			t.Fatalf("transition %s -> %s: %v", c.from, c.to, err)
		}
		got, err := s.GetTask(t.Context(), task.ID)
		if err != nil {
			t.Fatalf("GetTask %s: %v", task.ID, err)
		}
		if got.State != c.to {
			t.Fatalf("after %s -> %s: state is %q", c.from, c.to, got.State)
		}
	}
}

func TestTransitionIllegal(t *testing.T) {
	s := openTaskStore(t)

	cases := []struct{ from, to string }{
		{"draft", "merged"},
		{"draft", "in_progress"},
		{"merged", "abandoned"},
		{"released", "deployed_dev"},
		{"abandoned", "merged"},
		{"abandoned", "in_progress"},
	}
	for _, c := range cases {
		in := defaultTaskInput()
		in.Draft = c.from == "draft"
		task := createTask(t, s, taskTestNow, in)
		if !in.Draft {
			walkTo(t, s, task.ID, c.from)
		}
		err := transition(t, s, taskTestNow, task.ID, c.from, c.to)
		if !errors.Is(err, ErrBadTransition) {
			t.Fatalf("transition %s -> %s: want ErrBadTransition, got %v", c.from, c.to, err)
		}
	}
}

func TestTransitionWrongCurrentState(t *testing.T) {
	s := openTaskStore(t)

	task := createTask(t, s, taskTestNow, defaultTaskInput()) // state: ready
	err := transition(t, s, taskTestNow, task.ID, "in_progress", "in_review")
	if !errors.Is(err, ErrBadTransition) {
		t.Fatalf("transition with wrong from: want ErrBadTransition, got %v", err)
	}
	// The task is untouched.
	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.State != "ready" {
		t.Fatalf("state after failed transition: got %q, want ready", got.State)
	}
}

func TestTransitionUnknownTask(t *testing.T) {
	s := openTaskStore(t)

	err := transition(t, s, taskTestNow, "HDB-999", "ready", "in_progress")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("transition unknown task: want ErrNotFound, got %v", err)
	}
}

func TestTransitionWritesStateLogAndBumpsUpdatedAt(t *testing.T) {
	s := openTaskStore(t)

	created := taskTestNow
	moved := taskTestNow.Add(5 * time.Minute)

	task := createTask(t, s, created, defaultTaskInput())

	var eventID int64
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.transition", nil,
		func(tx *sql.Tx, evID int64) error {
			eventID = evID
			return Transition(tx, moved, task.ID, "ready", "in_progress", evID)
		})
	if err != nil {
		t.Fatalf("transition: %v", err)
	}

	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt: got %v, want %v", got.CreatedAt, created)
	}
	if !got.UpdatedAt.Equal(moved) {
		t.Fatalf("UpdatedAt: got %v, want %v (bumped)", got.UpdatedAt, moved)
	}

	var kind, entityID, changeJSON string
	var loggedEventID int64
	row := s.db.QueryRow(
		`SELECT entity_kind, entity_id, change, event_id FROM state_log WHERE entity_id = $1`, task.ID)
	if err := row.Scan(&kind, &entityID, &changeJSON, &loggedEventID); err != nil {
		t.Fatalf("read state_log: %v", err)
	}
	if kind != "task" || entityID != task.ID || loggedEventID != eventID {
		t.Fatalf("state_log row: kind=%q entity=%q event_id=%d, want task/%s/%d",
			kind, entityID, loggedEventID, task.ID, eventID)
	}
	var change map[string]string
	if err := json.Unmarshal([]byte(changeJSON), &change); err != nil {
		t.Fatalf("unmarshal change %q: %v", changeJSON, err)
	}
	want := map[string]string{"field": "state", "old": "ready", "new": "in_progress"}
	if !reflect.DeepEqual(change, want) {
		t.Fatalf("state_log change: got %v, want %v", change, want)
	}
}

func TestBlocksEdgeAndBlockedTaskIDs(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()

	blocker := createTask(t, s, taskTestNow, defaultTaskInput()) // HDB-1
	blocked := createTask(t, s, taskTestNow, defaultTaskInput()) // HDB-2

	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}

	// Blocked while the blocker is ready.
	ids, err := s.BlockedTaskIDs(ctx)
	if err != nil {
		t.Fatalf("BlockedTaskIDs: %v", err)
	}
	if !ids[blocked.ID] || ids[blocker.ID] {
		t.Fatalf("BlockedTaskIDs with blocker ready: got %v, want only %s", ids, blocked.ID)
	}
	if !isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked(%s): want true while blocker ready", blocked.ID)
	}

	// Still blocked while the blocker is in_progress.
	walkTo(t, s, blocker.ID, "in_progress")
	ids, err = s.BlockedTaskIDs(ctx)
	if err != nil {
		t.Fatalf("BlockedTaskIDs: %v", err)
	}
	if !ids[blocked.ID] {
		t.Fatalf("BlockedTaskIDs with blocker in_progress: %s missing from %v", blocked.ID, ids)
	}

	// Unblocked once the blocker is merged (legal walk: in_review then merged).
	if err := transition(t, s, taskTestNow, blocker.ID, "in_progress", "in_review"); err != nil {
		t.Fatalf("transition to in_review: %v", err)
	}
	if err := transition(t, s, taskTestNow, blocker.ID, "in_review", "merged"); err != nil {
		t.Fatalf("transition to merged: %v", err)
	}
	ids, err = s.BlockedTaskIDs(ctx)
	if err != nil {
		t.Fatalf("BlockedTaskIDs: %v", err)
	}
	if ids[blocked.ID] {
		t.Fatalf("BlockedTaskIDs with blocker merged: %s should be unblocked, got %v", blocked.ID, ids)
	}
	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked(%s): want false after blocker merged", blocked.ID)
	}
}

// TestBlockedTaskIDsDeliveredBlocker pins closedStates: a blocker that has
// advanced past merged must stay unblocking. Narrowing closedStates back to
// ('merged', 'abandoned') would make these dependents block again.
func TestBlockedTaskIDsDeliveredBlocker(t *testing.T) {
	for _, state := range []string{"deployed_dev", "deployed_prod", "released"} {
		t.Run(state, func(t *testing.T) {
			s := openTaskStore(t)
			ctx := t.Context()

			blocker := createTask(t, s, taskTestNow, defaultTaskInput())
			blocked := createTask(t, s, taskTestNow, defaultTaskInput())
			if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
				t.Fatalf("AddEdge blocks: %v", err)
			}
			walkTo(t, s, blocker.ID, state)

			ids, err := s.BlockedTaskIDs(ctx)
			if err != nil {
				t.Fatalf("BlockedTaskIDs: %v", err)
			}
			if ids[blocked.ID] {
				t.Fatalf("BlockedTaskIDs with blocker %s: %s should be unblocked, got %v",
					state, blocked.ID, ids)
			}
			if isBlocked(t, s, blocked.ID) {
				t.Fatalf("IsBlocked(%s): want false with blocker %s", blocked.ID, state)
			}
		})
	}
}

func TestBlockedTaskIDsAbandonedBlocker(t *testing.T) {
	s := openTaskStore(t)

	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := transition(t, s, taskTestNow, blocker.ID, "ready", "abandoned"); err != nil {
		t.Fatalf("abandon blocker: %v", err)
	}
	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: abandoned blocker must not block")
	}
}

func TestChildOfCycleRejected(t *testing.T) {
	s := openTaskStore(t)

	t1 := createTask(t, s, taskTestNow, defaultTaskInput())
	t2 := createTask(t, s, taskTestNow, defaultTaskInput())
	t3 := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, t1.ID, t2.ID, "child_of"); err != nil {
		t.Fatalf("AddEdge %s child_of %s: %v", t1.ID, t2.ID, err)
	}
	err := addEdge(t, s, t2.ID, t1.ID, "child_of")
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("direct cycle: want ErrCycle, got %v", err)
	}

	// Transitive cycle: t2 child_of t3 makes the chain t1 -> t2 -> t3;
	// t3 child_of t1 would close the loop.
	if err := addEdge(t, s, t2.ID, t3.ID, "child_of"); err != nil {
		t.Fatalf("AddEdge %s child_of %s: %v", t2.ID, t3.ID, err)
	}
	err = addEdge(t, s, t3.ID, t1.ID, "child_of")
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("transitive cycle: want ErrCycle, got %v", err)
	}
}

func TestAddEdgeDuplicateRejected(t *testing.T) {
	s := openTaskStore(t)

	t1 := createTask(t, s, taskTestNow, defaultTaskInput())
	t2 := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, t1.ID, t2.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := addEdge(t, s, t1.ID, t2.ID, "blocks"); !errors.Is(err, ErrEdgeExists) {
		t.Fatalf("duplicate edge: want ErrEdgeExists, got %v", err)
	}
}

func TestAddEdgeSelfRejected(t *testing.T) {
	s := openTaskStore(t)

	task := createTask(t, s, taskTestNow, defaultTaskInput())
	for _, typ := range []string{"child_of", "blocks"} {
		if err := addEdge(t, s, task.ID, task.ID, typ); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("self-edge %s: want ErrInvalidInput, got %v", typ, err)
		}
	}
}

func TestAddEdgeUnknownTask(t *testing.T) {
	s := openTaskStore(t)

	task := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, task.ID, "HDB-999", "blocks"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AddEdge to unknown task: want ErrNotFound, got %v", err)
	}
	if err := addEdge(t, s, "HDB-999", task.ID, "blocks"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AddEdge from unknown task: want ErrNotFound, got %v", err)
	}
}

func TestRemoveEdgeAndListEdges(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()

	t1 := createTask(t, s, taskTestNow, defaultTaskInput())
	t2 := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, t1.ID, t2.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	out, in, err := s.ListEdges(ctx, t1.ID)
	if err != nil {
		t.Fatalf("ListEdges %s: %v", t1.ID, err)
	}
	wantOut := []Edge{{FromTask: t1.ID, ToTask: t2.ID, Type: "blocks"}}
	if !reflect.DeepEqual(out, wantOut) || len(in) != 0 {
		t.Fatalf("ListEdges %s: out=%v in=%v, want out=%v in=[]", t1.ID, out, in, wantOut)
	}
	out, in, err = s.ListEdges(ctx, t2.ID)
	if err != nil {
		t.Fatalf("ListEdges %s: %v", t2.ID, err)
	}
	if len(out) != 0 || !reflect.DeepEqual(in, wantOut) {
		t.Fatalf("ListEdges %s: out=%v in=%v, want out=[] in=%v", t2.ID, out, in, wantOut)
	}

	if err := removeEdge(t, s, t1.ID, t2.ID, "blocks"); err != nil {
		t.Fatalf("RemoveEdge: %v", err)
	}
	out, in, err = s.ListEdges(ctx, t1.ID)
	if err != nil {
		t.Fatalf("ListEdges after remove: %v", err)
	}
	if len(out) != 0 || len(in) != 0 {
		t.Fatalf("ListEdges after remove: out=%v in=%v, want both empty", out, in)
	}

	if err := removeEdge(t, s, t1.ID, t2.ID, "blocks"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RemoveEdge absent: want ErrNotFound, got %v", err)
	}
}

func TestListTasksFiltersAndOrdering(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "other", "Other", "OT"); err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}

	mk := func(project, priority string) *Task {
		in := defaultTaskInput()
		in.ProjectID = project
		in.Priority = priority
		return createTask(t, s, taskTestNow, in)
	}
	tLow := mk("horndb", "low")          // HDB-1
	tCrit := mk("horndb", "critical")    // HDB-2
	tMed := mk("horndb", "medium")       // HDB-3
	tHigh := mk("horndb", "high")        // HDB-4
	tCrit2 := mk("horndb", "critical")   // HDB-5
	tOther := mk("other", "high")        // OT-1
	walkTo(t, s, tMed.ID, "in_progress") // HDB-3 -> in_progress

	idsOf := func(tasks []Task) []string {
		var ids []string
		for _, task := range tasks {
			ids = append(ids, task.ID)
		}
		return ids
	}

	// No filter: priority order (critical first), then id within a priority —
	// key lexically (HDB before OT), then the numeric suffix.
	all, err := s.ListTasks(ctx, TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks all: %v", err)
	}
	wantAll := []string{tCrit.ID, tCrit2.ID, tHigh.ID, tOther.ID, tMed.ID, tLow.ID}
	if got := idsOf(all); !reflect.DeepEqual(got, wantAll) {
		t.Fatalf("ListTasks order: got %v, want %v", got, wantAll)
	}

	// Project filter.
	horn, err := s.ListTasks(ctx, TaskFilter{Project: "horndb"})
	if err != nil {
		t.Fatalf("ListTasks project: %v", err)
	}
	wantHorn := []string{tCrit.ID, tCrit2.ID, tHigh.ID, tMed.ID, tLow.ID}
	if got := idsOf(horn); !reflect.DeepEqual(got, wantHorn) {
		t.Fatalf("ListTasks project=horndb: got %v, want %v", got, wantHorn)
	}

	// States filter.
	inProg, err := s.ListTasks(ctx, TaskFilter{States: []string{"in_progress"}})
	if err != nil {
		t.Fatalf("ListTasks states: %v", err)
	}
	if got := idsOf(inProg); !reflect.DeepEqual(got, []string{tMed.ID}) {
		t.Fatalf("ListTasks states=[in_progress]: got %v, want [%s]", got, tMed.ID)
	}

	// Priority filter.
	crit, err := s.ListTasks(ctx, TaskFilter{Priority: "critical"})
	if err != nil {
		t.Fatalf("ListTasks priority: %v", err)
	}
	if got := idsOf(crit); !reflect.DeepEqual(got, []string{tCrit.ID, tCrit2.ID}) {
		t.Fatalf("ListTasks priority=critical: got %v, want [%s %s]", got, tCrit.ID, tCrit2.ID)
	}

	// Combined filters.
	combo, err := s.ListTasks(ctx, TaskFilter{Project: "other", States: []string{"ready", "draft"}, Priority: "high"})
	if err != nil {
		t.Fatalf("ListTasks combined: %v", err)
	}
	if got := idsOf(combo); !reflect.DeepEqual(got, []string{tOther.ID}) {
		t.Fatalf("ListTasks combined: got %v, want [%s]", got, tOther.ID)
	}
}

func TestGetTaskNotFound(t *testing.T) {
	s := openTaskStore(t)

	_, err := s.GetTask(t.Context(), "HDB-999")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTask unknown: want ErrNotFound, got %v", err)
	}
}

func TestCreateTaskConcern(t *testing.T) {
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
	s := openTaskStore(t)

	in := defaultTaskInput()
	in.Concern = "not-a-concern"
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.create", nil,
		func(tx *sql.Tx, eventID int64) error {
			_, err := CreateTask(tx, taskTestNow, in)
			return err
		})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("CreateTask invalid concern: want ErrInvalidInput, got %v", err)
	}
}

// updateTaskFields drives UpdateTaskFields through RecordEvent.
func updateTaskFields(t *testing.T, s *Store, now time.Time, id string, title, body, priority, concern *string, needsDecomposition *bool) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.update", nil,
		func(tx *sql.Tx, eventID int64) error {
			return UpdateTaskFields(tx, now, id, title, body, priority, concern, needsDecomposition)
		})
	return err
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

func TestUpdateTaskFieldsConcernAndNeedsDecomposition(t *testing.T) {
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

// TestUpdateTaskFieldsRejectsBlankTitle pins the invariant CreateTask already
// enforces: a task carries a title for its whole life, so an update must not
// be able to blank one out.
func TestUpdateTaskFieldsRejectsBlankTitle(t *testing.T) {
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
