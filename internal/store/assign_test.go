package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// assigneeChanges returns the task's "assignee" state_log payloads, oldest
// first, so a test can assert the recorded old -> new provenance.
func assigneeChanges(t *testing.T, s *Store, id string) []map[string]string {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT change FROM state_log WHERE entity_kind = 'task' AND entity_id = $1 ORDER BY id`, id)
	if err != nil {
		t.Fatalf("read state_log for %s: %v", id, err)
	}
	defer rows.Close()
	var out []map[string]string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan state_log: %v", err)
		}
		var change map[string]string
		if err := json.Unmarshal([]byte(raw), &change); err != nil {
			t.Fatalf("unmarshal change %q: %v", raw, err)
		}
		if change["field"] == "assignee" {
			out = append(out, change)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate state_log: %v", err)
	}
	return out
}

// assignTask drives AssignTask through RecordEvent, the way production code
// will use it.
func assignTask(t *testing.T, s *Store, now time.Time, id, assignee string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.assigned", nil,
		func(tx *sql.Tx, eventID int64) error {
			return AssignTask(tx, now, id, assignee, eventID)
		})
	return err
}

// unassignTask drives UnassignTask through RecordEvent.
func unassignTask(t *testing.T, s *Store, now time.Time, id string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.unassigned", nil,
		func(tx *sql.Tx, eventID int64) error {
			return UnassignTask(tx, now, id, eventID)
		})
	return err
}

// startTask drives StartTask through RecordEvent and returns the assignee it
// settled on.
func startTask(t *testing.T, s *Store, now time.Time, id, actorID string) (string, error) {
	t.Helper()
	var assignee string
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.started", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			assignee, err = StartTask(tx, now, id, actorID, eventID)
			return err
		})
	return assignee, err
}

// stopTask drives StopTask through RecordEvent.
func stopTask(t *testing.T, s *Store, now time.Time, id, actorID string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.stopped", nil,
		func(tx *sql.Tx, eventID int64) error {
			return StopTask(tx, now, id, actorID, eventID)
		})
	return err
}

func TestAssignTaskHappyPath(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	if err := s.CreateActor(t.Context(), "bob", "human", "Bob", false); err != nil {
		t.Fatalf("CreateActor bob: %v", err)
	}
	seedParticipant(t, s, "horndb", "bob", "member", false)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := assignTask(t, s, taskTestNow, task.ID, "bob"); err != nil {
		t.Fatalf("AssignTask: %v", err)
	}
	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Assignee != "bob" {
		t.Fatalf("Assignee after assign: got %q, want bob", got.Assignee)
	}
}

func TestAssignTaskMissingActor(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	err := assignTask(t, s, taskTestNow, task.ID, "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("assign to missing actor: want ErrNotFound, got %v", err)
	}
	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Assignee != "" {
		t.Fatalf("Assignee after failed assign: got %q, want empty", got.Assignee)
	}
}

func TestAssignTaskTerminalStateRejected(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())
	walkTo(t, s, task.ID, "merged")

	if err := assignTask(t, s, taskTestNow, task.ID, "stig"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("assign on merged task: want ErrInvalidInput, got %v", err)
	}
}

func TestAssignTaskWithChildrenRejected(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	parent, _ := parentWithChildren(t, s, 1)

	if err := assignTask(t, s, taskTestNow, parent.ID, "stig"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("assign on a task with children: want ErrInvalidInput, got %v", err)
	}
}

func TestAssignTaskUnknownTask(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	if err := assignTask(t, s, taskTestNow, "HDB-999", "stig"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("assign unknown task: want ErrNotFound, got %v", err)
	}
}

func TestUnassignTaskClears(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := assignTask(t, s, taskTestNow, task.ID, "stig"); err != nil {
		t.Fatalf("AssignTask: %v", err)
	}

	if err := unassignTask(t, s, taskTestNow, task.ID); err != nil {
		t.Fatalf("UnassignTask: %v", err)
	}
	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Assignee != "" {
		t.Fatalf("Assignee after unassign: got %q, want empty", got.Assignee)
	}
}

// TestAssignmentChangesRecordPreviousAssignee pins the state_log provenance
// for the three write paths: without "old", a reassignment loses who held the
// task before, and the web timeline degrades to "assignee set to ...".
func TestAssignmentChangesRecordPreviousAssignee(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	if err := s.CreateActor(t.Context(), "bob", "human", "Bob", false); err != nil {
		t.Fatalf("CreateActor bob: %v", err)
	}
	seedParticipant(t, s, "horndb", "bob", "member", false)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	// unassigned -> stig: old is the empty string, not a missing key.
	if err := assignTask(t, s, taskTestNow, task.ID, "stig"); err != nil {
		t.Fatalf("AssignTask stig: %v", err)
	}
	// stig -> bob: the previous assignee is carried.
	if err := assignTask(t, s, taskTestNow, task.ID, "bob"); err != nil {
		t.Fatalf("AssignTask bob: %v", err)
	}
	// bob -> unassigned.
	if err := unassignTask(t, s, taskTestNow, task.ID); err != nil {
		t.Fatalf("UnassignTask: %v", err)
	}

	got := assigneeChanges(t, s, task.ID)
	want := []map[string]string{
		{"field": "assignee", "old": "", "new": "stig"},
		{"field": "assignee", "old": "stig", "new": "bob"},
		{"field": "assignee", "old": "bob", "new": ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("assignee state_log:\n got %v\nwant %v", got, want)
	}
}

// TestStartTaskAutoAssignRecordsEmptyOld covers StartTask's auto-assign path,
// the third LogChange call site.
func TestStartTaskAutoAssignRecordsEmptyOld(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	if _, err := startTask(t, s, taskTestNow, task.ID, "stig"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	got := assigneeChanges(t, s, task.ID)
	want := []map[string]string{{"field": "assignee", "old": "", "new": "stig"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("assignee state_log:\n got %v\nwant %v", got, want)
	}
}

// TestUnassignTaskTerminalStateRejected also pins the verb in the message:
// it read "cannot assign" while the caller was unassigning.
func TestUnassignTaskTerminalStateRejected(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())
	walkTo(t, s, task.ID, "merged")

	err := unassignTask(t, s, taskTestNow, task.ID)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unassign on merged task: want ErrInvalidInput, got %v", err)
	}
	if !strings.Contains(err.Error(), "cannot unassign") {
		t.Fatalf("unassign error %v does not say \"cannot unassign\"", err)
	}
}

func TestUnassignTaskUnknownTask(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	if err := unassignTask(t, s, taskTestNow, "HDB-999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unassign unknown task: want ErrNotFound, got %v", err)
	}
}

func TestStartTaskAutoAssigns(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	assignee, err := startTask(t, s, taskTestNow, task.ID, "stig")
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if assignee != "stig" {
		t.Fatalf("StartTask returned assignee %q, want stig", assignee)
	}
	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Assignee != "stig" {
		t.Fatalf("Assignee after start: got %q, want stig", got.Assignee)
	}
	if got.State != "in_progress" {
		t.Fatalf("state after start: got %q, want in_progress", got.State)
	}
}

func TestStartTaskAlreadyAssignedToCaller(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := assignTask(t, s, taskTestNow, task.ID, "stig"); err != nil {
		t.Fatalf("AssignTask: %v", err)
	}

	assignee, err := startTask(t, s, taskTestNow, task.ID, "stig")
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if assignee != "stig" {
		t.Fatalf("StartTask returned assignee %q, want stig", assignee)
	}
	mustState(t, s, task.ID, "in_progress")
}

func TestStartTaskAssignedToSomeoneElse(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	if err := s.CreateActor(t.Context(), "bob", "human", "Bob", false); err != nil {
		t.Fatalf("CreateActor bob: %v", err)
	}
	seedParticipant(t, s, "horndb", "bob", "member", false)
	task := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := assignTask(t, s, taskTestNow, task.ID, "bob"); err != nil {
		t.Fatalf("AssignTask: %v", err)
	}

	if _, err := startTask(t, s, taskTestNow, task.ID, "stig"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("start someone else's task: want ErrInvalidInput, got %v", err)
	}
	mustState(t, s, task.ID, "ready")
}

func TestStartTaskBlockedRejected(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}

	if _, err := startTask(t, s, taskTestNow, blocked.ID, "stig"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("start blocked task: want ErrInvalidInput, got %v", err)
	}
	mustState(t, s, blocked.ID, "ready")
}

func TestStartTaskUnknownTask(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	if _, err := startTask(t, s, taskTestNow, "HDB-999", "stig"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("start unknown task: want ErrNotFound, got %v", err)
	}
}

func TestStartTaskUnknownActor(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	if _, err := startTask(t, s, taskTestNow, task.ID, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("start by unknown actor: want ErrNotFound, got %v", err)
	}
	mustState(t, s, task.ID, "ready")
}

// TestStartTaskTerminalStateRejected pins StartTask's terminal-state guard
// to ErrInvalidInput, matching AssignTask/UnassignTask: without the explicit
// deliveredStateSet check, a merged task would instead fail Transition's
// from-state check with ErrBadTransition.
func TestStartTaskTerminalStateRejected(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())
	walkTo(t, s, task.ID, "merged")

	if _, err := startTask(t, s, taskTestNow, task.ID, "stig"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("start on merged task: want ErrInvalidInput, got %v", err)
	}
}

func TestStopTaskHappyPath(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())
	if _, err := startTask(t, s, taskTestNow, task.ID, "stig"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	if err := stopTask(t, s, taskTestNow, task.ID, "stig"); err != nil {
		t.Fatalf("StopTask: %v", err)
	}
	mustState(t, s, task.ID, "ready")
}

func TestStopTaskNonAssigneeRejected(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	if err := s.CreateActor(t.Context(), "bob", "human", "Bob", false); err != nil {
		t.Fatalf("CreateActor bob: %v", err)
	}
	task := createTask(t, s, taskTestNow, defaultTaskInput())
	if _, err := startTask(t, s, taskTestNow, task.ID, "stig"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	if err := stopTask(t, s, taskTestNow, task.ID, "bob"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("stop by non-assignee: want ErrInvalidInput, got %v", err)
	}
	mustState(t, s, task.ID, "in_progress")
}

// TestStopTaskWhileLeasedRejected proves the active-lease guard itself
// fires, not just the state/assignee guard ahead of it: the task is assigned
// to stig *before* being claimed, so by the time StopTask runs, state is
// in_progress and assignee == actorID — the only way to reach ErrInvalidInput
// is through the active-lease check. Asserting on the error message (not
// just the sentinel) is what makes that provable; errors.Is alone can't tell
// this apart from the earlier guard.
func TestStopTaskWhileLeasedRejected(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	task := createTask(t, s, leaseTestNow, defaultTaskInput())
	if err := assignTask(t, s, leaseTestNow, task.ID, "stig"); err != nil {
		t.Fatalf("AssignTask: %v", err)
	}
	if _, err := s.Claim(t.Context(), task.ID, "stig", "host:/wt", 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	err := stopTask(t, s, leaseTestNow, task.ID, "stig")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("stop while leased: want ErrInvalidInput, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "active lease") {
		t.Fatalf("stop while leased: error %v does not mention the active lease", err)
	}
	mustState(t, s, task.ID, "in_progress")
}

func TestStopTaskUnknownTask(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	if err := stopTask(t, s, taskTestNow, "HDB-999", "stig"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stop unknown task: want ErrNotFound, got %v", err)
	}
}

func TestListTasksFilterByAssignee(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	if err := s.CreateActor(t.Context(), "bob", "human", "Bob", false); err != nil {
		t.Fatalf("CreateActor bob: %v", err)
	}
	seedParticipant(t, s, "horndb", "bob", "member", false)
	mine := createTask(t, s, taskTestNow, defaultTaskInput())
	bobs := createTask(t, s, taskTestNow, defaultTaskInput())
	// unassigned has no assignee at all — it must be excluded from the
	// "stig" filter for that reason, not merely because it belongs to
	// someone else, which is what bobs alone would prove.
	unassigned := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := assignTask(t, s, taskTestNow, mine.ID, "stig"); err != nil {
		t.Fatalf("AssignTask mine: %v", err)
	}
	if err := assignTask(t, s, taskTestNow, bobs.ID, "bob"); err != nil {
		t.Fatalf("AssignTask bobs: %v", err)
	}

	got, err := s.ListTasks(t.Context(), TaskFilter{Assignee: "stig"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(got) != 1 || got[0].ID != mine.ID {
		t.Fatalf("ListTasks{Assignee: stig}: got %v, want [%s]", got, mine.ID)
	}
	for _, task := range got {
		if task.ID == unassigned.ID || task.ID == bobs.ID {
			t.Fatalf("ListTasks{Assignee: stig} leaked %s, which is not stig's", task.ID)
		}
	}
}

// TestAssignTaskNonCrewMember covers the Crew gate on assignment (spec 029
// §6.1): a task belongs to someone on its project's Crew, so both AssignTask
// and StartTask — which assigns as it starts — refuse an outsider, and
// neither leaves a trace on the task.
func TestAssignTaskNonCrewMember(t *testing.T) {
	s := openTaskStore(t)
	if err := s.CreateActor(t.Context(), "outsider", "human", "Outsider", false); err != nil {
		t.Fatalf("CreateActor outsider: %v", err)
	}
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	err := assignTask(t, s, taskTestNow, task.ID, "outsider")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("assign to a non-crew actor: want ErrInvalidInput, got %v", err)
	}
	if !strings.Contains(err.Error(), "crew") {
		t.Fatalf("message must name the crew: %v", err)
	}
	if _, err := startTask(t, s, taskTestNow, task.ID, "outsider"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("start by a non-crew actor: want ErrInvalidInput, got %v", err)
	}

	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Assignee != "" || got.State != "ready" {
		t.Fatalf("after refused assign/start: assignee=%q state=%q, want empty/ready", got.Assignee, got.State)
	}
}
