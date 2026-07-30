package store

import (
	"database/sql"
	"testing"
)

// TestMigrationAllowsEpicKind checks the 0006 CHECK change: 'epic' is a legal
// task kind and an unknown kind is still rejected.
func TestMigrationAllowsEpicKind(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	if epic.Kind != "epic" {
		t.Fatalf("kind = %q, want epic", epic.Kind)
	}

	bad := defaultTaskInput()
	bad.Kind = "saga"
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.create", nil,
		func(tx *sql.Tx, _ int64) error {
			_, err := CreateTask(tx, taskTestNow, bad)
			return err
		})
	if err == nil {
		t.Fatal("CreateTask with kind=saga succeeded, want a CHECK violation")
	}
}

// TestSingleParentIndex checks that the partial unique index rejects a second
// child_of edge out of one task, whichever parent it points at.
func TestSingleParentIndex(t *testing.T) {
	s := openTaskStore(t)
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	epicA := createTask(t, s, taskTestNow, epicInput())
	epicB := createTask(t, s, taskTestNow, epicInput())

	if _, err := s.DBForTests().Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, 'child_of', $3)`,
		child.ID, epicA.ID, taskTestNow); err != nil {
		t.Fatalf("first parent edge: %v", err)
	}
	_, err := s.DBForTests().Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, 'child_of', $3)`,
		child.ID, epicB.ID, taskTestNow)
	if err == nil {
		t.Fatal("second parent edge succeeded, want a unique violation")
	}
	if !isUniqueViolationOn(err, "task_edges_single_parent") {
		t.Fatalf("error = %v, want a task_edges_single_parent unique violation", err)
	}
}

// epicInput is the shared fixture for a container task.
func epicInput() TaskInput {
	in := defaultTaskInput()
	in.Title = "an epic"
	in.Kind = "epic"
	return in
}
