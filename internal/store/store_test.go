package store

import (
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
	s := OpenTestStore(t)

	rows, err := s.db.Query(
		`SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'`)
	if err != nil {
		t.Fatalf("query information_schema.tables: %v", err)
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
	s := OpenTestStore(t) // first Migrate happened here
	if err := s.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestMigrateRoundTrip(t *testing.T) {
	s := OpenTestStore(t) // up happened here
	if err := s.MigrateDown(MigrationsDirForTests()); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatal(err)
	}
}
