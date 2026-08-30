package store

import (
	"fmt"
	"testing"
)

var wantTables = []string{
	"events",
	"actors",
	"tokens",
	"projects",
	"project_repos",
	"tasks",
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
	"github_user_tokens",
	"agent_sessions",
	"skills",
	"skill_versions",
	"skill_embeddings",
	"embedding_config",
	"docs",
	"doc_sections",
	"doc_edges",
	"doc_coverage_completed_with",
	"doc_revisions",
	"approvals",
}

func TestMigrateAppliesMigrations(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	s := OpenTestStore(t) // first Migrate happened here
	if err := s.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestMigrateRoundTrip(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t) // up happened here
	if err := s.MigrateDown(MigrationsDirForTests()); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatal(err)
	}
}

// TestMigrateReleasesDedicatedConnection guards against golang-migrate's pgx
// driver leaking the dedicated advisory-lock connection it opens via
// db.Conn(). A leaked connection makes Postgres refuse
// CREATE DATABASE ... TEMPLATE with SQLSTATE 55006 ("source database is
// being accessed by other users") even after Migrate has returned and the
// Store been closed — exactly the failure the template-database builder
// (testhelpers.go) used to work around by reaching into newMigrate/m.Close()
// directly instead of going through Store.Migrate().
func TestMigrateReleasesDedicatedConnection(t *testing.T) {
	t.Parallel()
	admin := adminConnForTest(t)
	dbName := randomDBName(t, "wl_test_leak_")
	if _, err := admin.Exec("CREATE DATABASE " + sqlIdent(dbName)); err != nil {
		t.Fatalf("create database %s: %v", dbName, err)
	}
	t.Cleanup(func() { dropDatabase(t, admin, dbName) })

	s := openTestDB(t, dbName)
	if err := s.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatalf("migrate %s: %v", dbName, err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store for %s: %v", dbName, err)
	}

	cloneName := dbName + "_clone"
	stmt := fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", sqlIdent(cloneName), sqlIdent(dbName))
	if _, err := admin.Exec(stmt); err != nil {
		t.Fatalf("CREATE DATABASE ... TEMPLATE %s: %v (Migrate's dedicated connection was not released)", dbName, err)
	}
	t.Cleanup(func() { dropDatabase(t, admin, cloneName) })
}
