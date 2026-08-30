package store

import (
	"context"
	"reflect"
	"testing"
)

// TestClosedTaskIDsAbandoned pins that an abandoned task is closed regardless
// of any repo mapping (taskClosed's first disjunct).
func TestClosedTaskIDsAbandoned(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()

	task := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := transition(t, s, taskTestNow, task.ID, "ready", "abandoned"); err != nil {
		t.Fatalf("abandon: %v", err)
	}

	closed, err := s.ClosedTaskIDs(ctx, []string{task.ID})
	if err != nil {
		t.Fatalf("ClosedTaskIDs: %v", err)
	}
	if !closed[task.ID] {
		t.Fatalf("ClosedTaskIDs[%s] = %v, want true (abandoned)", task.ID, closed[task.ID])
	}
}

// TestGetTaskSetsClosed covers the GetTask path directly (the one that
// reaches the wire via internal/api/tasks.go TaskDetail): an abandoned task
// comes back with Closed true. TestCreateTaskSequentialIDsAndDefaults only
// pins Closed false on a fresh ready task, which would pass even if GetTask
// never touched the field.
func TestGetTaskSetsClosed(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()

	task := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := transition(t, s, taskTestNow, task.ID, "ready", "abandoned"); err != nil {
		t.Fatalf("abandon: %v", err)
	}

	got, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !got.Closed {
		t.Fatalf("GetTask(%s).Closed = false, want true (abandoned)", task.ID)
	}
}

// TestClosedTaskIDsReadyNotClosed pins that a freshly created ready task is
// not closed.
func TestClosedTaskIDsReadyNotClosed(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()

	task := createTask(t, s, taskTestNow, defaultTaskInput())

	closed, err := s.ClosedTaskIDs(ctx, []string{task.ID})
	if err != nil {
		t.Fatalf("ClosedTaskIDs: %v", err)
	}
	if closed[task.ID] {
		t.Fatalf("ClosedTaskIDs[%s] = true, want false (ready)", task.ID)
	}
}

// TestClosedTaskIDsPerRepoDoneState mirrors TestBlockedTaskIDsPerRepoDoneState
// against ClosedTaskIDs directly: a task is closed at or past its landed
// repo's done_state, and the same state is still open where the repo gates
// higher (004 §1.3).
func TestClosedTaskIDsPerRepoDoneState(t *testing.T) {
	t.Parallel()
	cases := []struct {
		doneState string
		state     string
		want      bool
	}{
		{"merged", "merged", true},
		{"released", "merged", false},
		{"released", "released", true},
	}
	for _, c := range cases {
		t.Run(c.doneState+"/"+c.state, func(t *testing.T) {
			s := openTaskStore(t)
			ctx := t.Context()
			mapRepo(t, s, "horndb", "acme/app", c.doneState)

			task := createTask(t, s, taskTestNow, defaultTaskInput())
			landCommit(t, s, task.ID, "acme/app", "sha-app")
			walkTo(t, s, task.ID, c.state)

			closed, err := s.ClosedTaskIDs(ctx, []string{task.ID})
			if err != nil {
				t.Fatalf("ClosedTaskIDs: %v", err)
			}
			if closed[task.ID] != c.want {
				t.Errorf("ClosedTaskIDs[%s] = %v, want %v (done_state %s, state %s)",
					task.ID, closed[task.ID], c.want, c.doneState, c.state)
			}
		})
	}
}

// TestClosedTaskIDsEmptyReturnsEmptyMapNoQuery pins that a nil id list short-
// circuits before any database round trip: a cancelled context would fail any
// real query, so a nil error here proves none was issued.
func TestClosedTaskIDsEmptyReturnsEmptyMapNoQuery(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	closed, err := s.ClosedTaskIDs(ctx, nil)
	if err != nil {
		t.Fatalf("ClosedTaskIDs(nil) with cancelled context: %v (should not query)", err)
	}
	if len(closed) != 0 {
		t.Fatalf("ClosedTaskIDs(nil) = %v, want empty map", closed)
	}
}

// TestListTasksSetsClosed pins that ListTasks populates Closed per row: an
// abandoned task and a task at its repo's done_state are closed, a ready
// task is not.
func TestListTasksSetsClosed(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()

	open := createTask(t, s, taskTestNow, defaultTaskInput())
	abandoned := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := transition(t, s, taskTestNow, abandoned.ID, "ready", "abandoned"); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	merged := createTask(t, s, taskTestNow, defaultTaskInput())
	walkTo(t, s, merged.ID, "merged") // no repo mapping: gates on DefaultDoneState "merged"

	out, err := s.ListTasks(ctx, TaskFilter{Project: "horndb"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	got := map[string]bool{}
	for _, task := range out {
		got[task.ID] = task.Closed
	}
	want := map[string]bool{open.ID: false, abandoned.ID: true, merged.ID: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListTasks Closed: got %v, want %v", got, want)
	}
}
