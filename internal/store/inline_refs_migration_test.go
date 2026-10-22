package store

import "testing"

// TestInlineRefsMigration: the inline_refs table and the version change
// marker exist after up, and are gone after down (WL-REQ-1852, WL-REQ-1855).
func TestInlineRefsMigration(t *testing.T) {
	t.Parallel()
	s := OpenUnmigratedTestStore(t)
	if err := s.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	db := s.DBForTests()
	var n int
	count := func(q string) int {
		t.Helper()
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	if got := count(`SELECT count(*) FROM information_schema.tables WHERE table_name = 'inline_refs'`); got != 1 {
		t.Fatalf("inline_refs tables after up: %d", got)
	}
	if got := count(`SELECT count(*) FROM information_schema.columns
	                  WHERE column_name = 'change' AND table_name IN ('doc_versions', 'rule_versions')`); got != 2 {
		t.Fatalf("change columns after up: %d", got)
	}
	if _, err := db.Exec(`INSERT INTO doc_versions (doc_id, version, body, title, created_at, change)
	                      VALUES (0, 0, '', '', now(), 'rewrite')`); err == nil {
		t.Fatal("doc_versions.change accepts a value outside edit and normalize")
	}

	migrateSteps(t, s, -1)
	if got := count(`SELECT count(*) FROM information_schema.tables WHERE table_name = 'inline_refs'`); got != 0 {
		t.Fatalf("inline_refs tables after down: %d", got)
	}
	if got := count(`SELECT count(*) FROM information_schema.columns
	                  WHERE column_name = 'change' AND table_name IN ('doc_versions', 'rule_versions')`); got != 0 {
		t.Fatalf("change columns after down: %d", got)
	}
}
