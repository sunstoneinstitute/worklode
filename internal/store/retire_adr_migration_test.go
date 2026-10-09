package store

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

// TestRetireADRMigration seeds live and tombstoned ADRs in two projects, then
// applies the migration that retires the kind (WL-966): live ADRs become
// specs past every spec number the project used, keep their dependents and
// stay reachable by their old ref; tombstoned ADRs are hard-deleted and the
// kind CHECK refuses 'adr'.
func TestRetireADRMigration(t *testing.T) {
	t.Parallel()
	s := OpenUnmigratedTestStore(t)
	version := retireADRMigrationVersion(t)
	if err := s.Migrate(migrationsThrough(t, version-1)); err != nil {
		t.Fatalf("migrate through %d: %v", version-1, err)
	}
	db := s.DBForTests()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	doc := func(project, kind string, number int, slug string, deleted bool) int64 {
		t.Helper()
		var id int64
		if err := db.QueryRow(
			`INSERT INTO docs (project_id, kind, number, slug, title, body, created_at, updated_at, deleted_at)
			 VALUES ($1, $2, $3, $4, $4, '# '||$4, now(), now(), CASE WHEN $5 THEN now() END) RETURNING id`,
			project, kind, number, slug, deleted).Scan(&id); err != nil {
			t.Fatalf("insert %s %s %d: %v", project, kind, number, err)
		}
		return id
	}

	exec(`INSERT INTO projects (id, name, key) VALUES ('pa', 'PA', 'PA'), ('pb', 'PB', 'PB')`)
	spec1 := doc("pa", "spec", 1, "spec-one", false)
	doc("pa", "spec", 5, "spec-five-gone", true) // a tombstone still holds number 5
	adr1 := doc("pa", "adr", 1, "adr-one", false)
	adr2 := doc("pa", "adr", 2, "adr-two", false)
	gone := doc("pa", "adr", 3, "adr-three-gone", true)
	pbADR := doc("pb", "adr", 1, "pb-adr-one", false)
	doc("pb", "adr", 2, "pb-adr-two-gone", true)
	exec(`INSERT INTO project_entity_seq (project_id, kind, next) VALUES ('pa', 'SPEC', 3), ('pa', 'ADR', 4), ('pb', 'ADR', 3)`)

	// Dependents of the live ADR that must follow it.
	exec(`INSERT INTO doc_sections (doc_id, anchor, heading, depth, position) VALUES ($1, 'sec-1', 'One', 2, 1)`, adr1)
	exec(`INSERT INTO doc_versions (doc_id, version, body, title, created_at) VALUES ($1, 1, 'v1', 'v1', now())`, adr1)
	exec(`INSERT INTO doc_notes (doc_id, anchor, body, created_at) VALUES ($1, 'sec-1', 'note', now())`, adr1)
	exec(`INSERT INTO rules (id, project_id, number) VALUES (9001, 'pa', 1)`)
	exec(`INSERT INTO rule_versions (rule_id, version, heading, body) VALUES (9001, 1, 'One', 'text')`)
	exec(`INSERT INTO doc_rules (doc_id, rule_id, rule_version, anchor, depth, position) VALUES ($1, 9001, 1, 'sec-1', 2, 1)`, adr1)
	exec(`INSERT INTO doc_edges (from_doc, type, to_doc, to_anchor) VALUES ($1, 'requires', $2, 'sec-1')`, spec1, adr1)
	// Dependents of the tombstoned ADR.
	exec(`INSERT INTO doc_edges (from_doc, type, to_doc, to_anchor) VALUES ($1, 'requires', $2, 'sec-2')`, spec1, gone)
	exec(`INSERT INTO doc_versions (doc_id, version, body, title, created_at) VALUES ($1, 1, 'v1', 'v1', now())`, gone)
	exec(`INSERT INTO tasks (id, project_id, kind, title, priority, state, about_doc, created_at, updated_at)
	      VALUES ('PA-1', 'pa', 'review', 'review it', 'low', 'ready', $1, now(), now())`, gone)

	migrateSteps(t, s, 1)

	for _, tc := range []struct {
		id        int64
		number    int
		formerADR int
		firstLine string
	}{
		{adr1, 6, 1, "Was PA-ADR-1."},
		{adr2, 7, 2, "Was PA-ADR-2."},
		{pbADR, 1, 1, "Was PB-ADR-1."},
	} {
		var kind, body string
		var number, former int
		if err := db.QueryRow(`SELECT kind, number, former_adr, body FROM docs WHERE id = $1`, tc.id).
			Scan(&kind, &number, &former, &body); err != nil {
			t.Fatalf("read doc %d: %v", tc.id, err)
		}
		first, _, _ := strings.Cut(body, "\n")
		if kind != "spec" || number != tc.number || former != tc.formerADR || first != tc.firstLine {
			t.Errorf("doc %d = %s %d former %d first line %q, want spec %d former %d %q",
				tc.id, kind, number, former, first, tc.number, tc.formerADR, tc.firstLine)
		}
	}

	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := db.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	for table, want := range map[string]int{"doc_sections": 1, "doc_versions": 1, "doc_notes": 1, "doc_rules": 1} {
		if got := count(`SELECT count(*) FROM `+table+` WHERE doc_id = $1`, adr1); got != want {
			t.Errorf("%s rows for the former ADR = %d, want %d", table, got, want)
		}
	}
	if got := count(`SELECT count(*) FROM doc_edges WHERE from_doc = $1 AND to_doc = $2`, spec1, adr1); got != 1 {
		t.Errorf("edge to the former ADR: %d rows, want 1", got)
	}

	if got := count(`SELECT count(*) FROM docs WHERE kind = 'adr' OR slug LIKE '%adr%gone'`); got != 0 {
		t.Errorf("%d ADR rows left, want 0", got)
	}
	if got := count(`SELECT count(*) FROM doc_versions WHERE doc_id = $1`, gone); got != 0 {
		t.Errorf("tombstoned ADR's versions survived: %d", got)
	}
	if got := count(`SELECT count(*) FROM doc_edges WHERE from_doc = $1 AND to_external = 'PA-ADR-3#sec-2' AND to_doc IS NULL`, spec1); got != 1 {
		t.Errorf("edge into the tombstoned ADR not turned external")
	}
	if got := count(`SELECT count(*) FROM tasks WHERE id = 'PA-1' AND about_doc IS NULL`); got != 1 {
		t.Errorf("task still points at the tombstoned ADR")
	}

	if got := count(`SELECT next FROM project_entity_seq WHERE project_id = 'pa' AND kind = 'SPEC'`); got != 8 {
		t.Errorf("pa SPEC counter = %d, want 8", got)
	}
	if got := count(`SELECT next FROM project_entity_seq WHERE project_id = 'pb' AND kind = 'SPEC'`); got != 2 {
		t.Errorf("pb SPEC counter = %d, want 2", got)
	}
	if got := count(`SELECT count(*) FROM project_entity_seq WHERE kind = 'ADR'`); got != 0 {
		t.Errorf("%d ADR counters left, want 0", got)
	}

	if _, err := db.Exec(`INSERT INTO docs (project_id, kind, number, slug, title, body, created_at, updated_at)
		VALUES ('pa', 'adr', 9, 'new-adr', 't', 'b', now(), now())`); err == nil || !strings.Contains(err.Error(), "docs_kind_check") {
		t.Errorf("insert kind adr: err = %v, want docs_kind_check violation", err)
	}

	docs, err := s.ListDocs(t.Context(), DocFilter{Project: "pa"})
	if err != nil {
		t.Fatalf("ListDocs: %v", err)
	}
	d, _, err := designdoc.ResolveRef(docs, "PA", "PA-ADR-2")
	if err != nil || d.ID != adr2 {
		t.Errorf("ResolveRef(PA-ADR-2) = %d, %v; want the successor %d", d.ID, err, adr2)
	}
}

// retireADRMigrationVersion is the number the retire-adr-kind migration has
// in the test migrations directory, NEW files numbered.
func retireADRMigrationVersion(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(MigrationsDirForTests())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		var v int
		if strings.HasSuffix(e.Name(), "_retire-adr-kind.up.sql") {
			if _, err := fmt.Sscanf(e.Name(), "%04d_", &v); err == nil {
				return v
			}
		}
	}
	t.Fatal("retire-adr-kind migration not found")
	return 0
}
