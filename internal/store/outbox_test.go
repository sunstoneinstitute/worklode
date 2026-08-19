package store

import (
	"database/sql"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// outboxStore returns a migrated store with two projects.
func outboxStore(t *testing.T) *Store {
	t.Helper()
	s := OpenTestStore(t)
	for _, p := range [][3]string{{"alpha", "Alpha", "AL"}, {"beta", "Beta", "BE"}} {
		if err := s.CreateProject(t.Context(), p[0], p[1], p[2]); err != nil {
			t.Fatalf("create project %s: %v", p[0], err)
		}
	}
	return s
}

// makeTask creates a ready task through the event log, as the API does.
func makeTask(t *testing.T, s *Store, extID, project, title string) *model.Task {
	t.Helper()
	var created *model.Task
	_, _, err := s.RecordEvent(t.Context(), "cli", extID, "task.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			task, err := CreateTask(tx, time.Now().UTC(), TaskInput{
				ProjectID: project, Title: title, Priority: "medium", Kind: "feature",
			}, eventID)
			if err != nil {
				return err
			}
			created = task
			return nil
		})
	if err != nil {
		t.Fatalf("create task %q: %v", title, err)
	}
	return created
}

func TestCreateTaskWritesStateLog(t *testing.T) {
	s := outboxStore(t)
	task := makeTask(t, s, "e1", "alpha", "outbox on create")

	entries, err := s.StateLogForEntity(t.Context(), "task", task.ID)
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("state_log entries after create = %d; want 1", len(entries))
	}
}

func TestEdgeChangesWriteStateLogForBothTasks(t *testing.T) {
	s := outboxStore(t)
	a := makeTask(t, s, "e2", "alpha", "blocker")
	b := makeTask(t, s, "e3", "alpha", "blocked")

	_, _, err := s.RecordEvent(t.Context(), "cli", "e4", "task.edge_added", nil,
		func(tx *sql.Tx, eventID int64) error {
			return AddEdge(tx, time.Now().UTC(), a.ID, b.ID, "blocks", eventID)
		})
	if err != nil {
		t.Fatalf("add edge: %v", err)
	}
	for _, id := range []string{a.ID, b.ID} {
		entries, err := s.StateLogForEntity(t.Context(), "task", id)
		if err != nil {
			t.Fatalf("state log %s: %v", id, err)
		}
		if len(entries) != 2 { // create + edge add
			t.Fatalf("%s entries = %d; want 2 (create + edge)", id, len(entries))
		}
	}

	_, _, err = s.RecordEvent(t.Context(), "cli", "e5", "task.edge_removed", nil,
		func(tx *sql.Tx, eventID int64) error {
			return RemoveEdge(tx, a.ID, b.ID, "blocks", eventID)
		})
	if err != nil {
		t.Fatalf("remove edge: %v", err)
	}
	entries, err := s.StateLogForEntity(t.Context(), "task", b.ID)
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries after removal = %d; want 3", len(entries))
	}
}
