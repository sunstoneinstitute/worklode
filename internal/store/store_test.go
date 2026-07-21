package store

import (
	"path/filepath"
	"testing"
)

var wantTables = []string{
	"events",
	"actors",
	"tokens",
	"projects",
	"project_repos",
	"tasks",
	"task_seq",
	"task_edges",
	"leases",
	"issues",
	"pull_requests",
	"ci_runs",
	"reviews",
	"artifacts",
	"deployments",
	"runtime_events",
	"state_log",
}

func TestMigrateAppliesMigrations(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wl.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if err := s.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer rows.Close()

	got := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	for _, table := range wantTables {
		if !got[table] {
			t.Errorf("table %q missing after migration", table)
		}
	}
}

func TestMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wl.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := s1.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()
	if err := s2.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}
