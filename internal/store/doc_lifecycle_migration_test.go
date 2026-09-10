package store

import "testing"

// TestDocLifecycleMigrationRoundTrip proves that 0078 accepts both new
// document statuses, keeps the per-project threshold positive-only, and
// safely narrows populated rows back to superseded on down.
func TestDocLifecycleMigrationRoundTrip(t *testing.T) {
	t.Parallel()
	s := OpenUnmigratedTestStore(t)
	if err := s.Migrate(migrationsThrough(t, 77)); err != nil {
		t.Fatalf("migrate through 0077: %v", err)
	}

	migrateSteps(t, s, 1)
	if _, err := s.DBForTests().Exec(
		`INSERT INTO projects (id, name, key) VALUES ('lifecycle', 'Lifecycle', 'LC')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	for _, status := range []string{"stale", "withdrawn"} {
		if _, err := s.DBForTests().Exec(
			`INSERT INTO docs (project_id, kind, number, slug, title, body, status, created_at, updated_at)
			 VALUES ('lifecycle', 'spec', $1, $2, $2, 'body', $3, now(), now())`,
			map[string]int{"stale": 1, "withdrawn": 2}[status], status, status); err != nil {
			t.Fatalf("insert %s document: %v", status, err)
		}
	}
	for _, threshold := range []int{0, -1} {
		if _, err := s.DBForTests().Exec(
			`UPDATE projects SET doc_staleness_days = $1 WHERE id = 'lifecycle'`, threshold); err == nil {
			t.Errorf("doc_staleness_days=%d succeeded, want CHECK violation", threshold)
		}
	}
	if _, err := s.DBForTests().Exec(
		`UPDATE projects SET doc_staleness_days = 30 WHERE id = 'lifecycle'`); err != nil {
		t.Fatalf("set positive staleness threshold: %v", err)
	}

	migrateSteps(t, s, -1)
	rows, err := s.DBForTests().Query(`SELECT status FROM docs ORDER BY number`)
	if err != nil {
		t.Fatalf("query statuses after down: %v", err)
	}
	defer rows.Close()
	for i := 0; i < 2; i++ {
		if !rows.Next() {
			t.Fatalf("status row %d missing after down", i)
		}
		var got string
		if err := rows.Scan(&got); err != nil {
			t.Fatalf("scan status row %d: %v", i, err)
		}
		if got != "superseded" {
			t.Errorf("status row %d = %q, want superseded", i, got)
		}
	}
	if rows.Next() {
		t.Error("unexpected extra status row after down")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read statuses after down: %v", err)
	}
	rows.Close()

	for _, column := range []string{"about_anchor", "doc_staleness_days"} {
		var exists bool
		if err := s.DBForTests().QueryRow(
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns
			 WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2)`,
			map[string]string{"about_anchor": "tasks", "doc_staleness_days": "projects"}[column], column,
		).Scan(&exists); err != nil {
			t.Fatalf("check dropped %s: %v", column, err)
		}
		if exists {
			t.Errorf("column %s still exists after down", column)
		}
	}

	migrateSteps(t, s, 1)
}
