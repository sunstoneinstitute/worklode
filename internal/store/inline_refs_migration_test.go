package store

import "testing"

// TestInlineRefsMigration: the inline_refs table and the version change
// marker exist after up, and are gone after down (WL-REQ-1852, WL-REQ-1855).
func TestInlineRefsMigration(t *testing.T) {
	t.Parallel()
	s := OpenUnmigratedTestStore(t)
	if err := s.Migrate(migrationsThrough(t, 114)); err != nil {
		t.Fatalf("migrate through 0114: %v", err)
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

// TestRuleReferencesMigration: a live and a snapshot references edge move
// into inline_refs, rule_edges refuses references afterwards, and down moves
// both back (WL-REQ-1852).
func TestRuleReferencesMigration(t *testing.T) {
	t.Parallel()
	s := OpenUnmigratedTestStore(t)
	if err := s.Migrate(migrationsThrough(t, 114)); err != nil {
		t.Fatalf("migrate through 0114: %v", err)
	}
	db := s.DBForTests()
	exec := func(q string) {
		t.Helper()
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	rows := func(q string) string {
		t.Helper()
		var out string
		if err := db.QueryRow(`SELECT coalesce(string_agg(x, ';' ORDER BY x), '') FROM (` + q + `) q(x)`).Scan(&out); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return out
	}
	exec(`INSERT INTO projects (id, name, key) VALUES ('p1', 'P1', 'P1')`)
	exec(`INSERT INTO rules (id, project_id, number, version) VALUES (1, 'p1', 1, 2), (2, 'p1', 2, 1)`)
	exec(`INSERT INTO rule_versions (rule_id, version, heading, body) VALUES (1, 1, 'a', ''), (1, 2, 'a', ''), (2, 1, 'b', '')`)
	exec(`INSERT INTO rule_edges (from_rule, to_rule, type, source) VALUES (1, 2, 'references', 'derived'), (1, 2, 'needs', 'manual')`)
	exec(`INSERT INTO rule_edge_versions (rule_id, version, to_rule, type, source) VALUES (1, 1, 2, 'references', 'manual')`)

	migrateSteps(t, s, 1)
	refs := `SELECT from_id || '>' || to_id || ' v' || from_version || ' ' || field || ' ' || source FROM inline_refs`
	if got, want := rows(refs), "1>2 v1 body manual;1>2 v2 body derived"; got != want {
		t.Errorf("inline_refs after up = %q, want %q", got, want)
	}
	if got, want := rows(`SELECT type FROM rule_edges UNION ALL SELECT type FROM rule_edge_versions`), "needs"; got != want {
		t.Errorf("rule edges after up = %q, want %q", got, want)
	}
	if _, err := db.Exec(`INSERT INTO rule_edges (from_rule, to_rule, type, source) VALUES (2, 1, 'references', 'manual')`); err == nil {
		t.Error("rule_edges accepts references after up")
	}

	migrateSteps(t, s, -1)
	if got := rows(refs); got != "" {
		t.Errorf("inline_refs after down = %q", got)
	}
	if got, want := rows(`SELECT 'live ' || type || ' ' || source FROM rule_edges
	                      UNION ALL SELECT 'v' || version || ' ' || type || ' ' || source FROM rule_edge_versions`),
		"live needs manual;live references derived;v1 references manual"; got != want {
		t.Errorf("rule edges after down = %q, want %q", got, want)
	}
}
