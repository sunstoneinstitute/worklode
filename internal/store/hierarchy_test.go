package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	if !isCheckViolationOn(err, "tasks_kind_check") {
		t.Fatalf("error = %v, want a tasks_kind_check violation", err)
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

// TestUpMigrationDedupsDuplicateParentEdges checks that 0006's DELETE step
// drops all but the latest child_of edge out of a task before the
// single-parent index is created, so a database carrying pre-existing
// duplicates (legal under 0005) migrates cleanly instead of aborting with a
// unique violation.
func TestUpMigrationDedupsDuplicateParentEdges(t *testing.T) {
	s := OpenUnmigratedTestStore(t)
	if err := s.Migrate(migrationsThrough(t, 5)); err != nil {
		t.Fatalf("migrate through 0005: %v", err)
	}
	if err := s.CreateProject(t.Context(), "horndb", "HornDB", "HDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.CreateActor(t.Context(), "stig", "human", "Stig", false); err != nil {
		t.Fatalf("CreateActor: %v", err)
	}

	child := createTask(t, s, taskTestNow, defaultTaskInput())
	parentA := createTask(t, s, taskTestNow, defaultTaskInput())
	parentB := createTask(t, s, taskTestNow, defaultTaskInput())

	older, newer := taskTestNow, taskTestNow.Add(time.Hour)
	if _, err := s.DBForTests().Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, 'child_of', $3)`,
		child.ID, parentA.ID, older); err != nil {
		t.Fatalf("insert first parent edge: %v", err)
	}
	if _, err := s.DBForTests().Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, 'child_of', $3)`,
		child.ID, parentB.ID, newer); err != nil {
		t.Fatalf("insert second parent edge: %v", err)
	}

	if err := s.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatalf("migrate to 0006: %v", err)
	}

	rows, err := s.DBForTests().Query(
		`SELECT to_task FROM task_edges WHERE from_task = $1 AND type = 'child_of'`, child.ID)
	if err != nil {
		t.Fatalf("query surviving edges: %v", err)
	}
	defer rows.Close()
	var survivors []string
	for rows.Next() {
		var to string
		if err := rows.Scan(&to); err != nil {
			t.Fatalf("scan: %v", err)
		}
		survivors = append(survivors, to)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(survivors) != 1 || survivors[0] != parentB.ID {
		t.Fatalf("surviving child_of edges = %v, want [%s] (the later one)", survivors, parentB.ID)
	}
}

// migrationsThrough copies the up/down pairs through version n out of
// MigrationsDirForTests into a fresh temp dir, so a test can migrate a store
// to an intermediate schema version before seeding data and applying the
// rest.
func migrationsThrough(t *testing.T, n int) string {
	t.Helper()
	src := MigrationsDirForTests()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	dir := t.TempDir()
	for _, e := range entries {
		var version int
		if _, err := fmt.Sscanf(e.Name(), "%04d_", &version); err != nil || version > n {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", e.Name(), err)
		}
	}
	return dir
}

// epicInput is the shared fixture for a container task.
func epicInput() TaskInput {
	in := defaultTaskInput()
	in.Title = "an epic"
	in.Kind = "epic"
	return in
}
