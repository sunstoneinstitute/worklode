package store

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

const derivedSpecBody = "# S\n\n## 1. One {#sec-1}\n\n## 2. Two {#sec-2}\n\n## 3. Three {#sec-3}\n\n" +
	"## 3a Three-a {#sec-3a}\n\n## 4. Four {#sec-4}\n\n## Open questions {#sec-open-questions}\n"

// TestDerivedSectionNumbersMigration: existing specs are renumbered from
// their arrangement (WL-REQ-165). A letter-suffixed sec-3a becomes sec-4,
// a slugged heading keeps its anchor, and the locators naming an anchor
// move with their entry. A plan covering #sec-4 written before the change
// still points at the same rule, now at sec-5.
func TestDerivedSectionNumbersMigration(t *testing.T) {
	t.Parallel()
	s := OpenUnmigratedTestStore(t)
	version := migrationVersion(t, "_derived-section-numbers.up.sql")
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
	doc := func(kind string, number int, body string) int64 {
		t.Helper()
		var id int64
		if err := db.QueryRow(
			`INSERT INTO docs (project_id, kind, number, slug, title, body, status, created_at, updated_at)
			 VALUES ('pm', $1, $2, $4, $4, $3, 'accepted', now(), now()) RETURNING id`,
			kind, number, body, fmt.Sprint(kind, number)).Scan(&id); err != nil {
			t.Fatalf("insert %s %d: %v", kind, number, err)
		}
		return id
	}
	exec(`INSERT INTO projects (id, name, key) VALUES ('pm', 'PM', 'PM')`)
	spec := doc("spec", 1, derivedSpecBody)
	plan := doc("plan", 2, "# Plan\n")
	other := doc("spec", 3, "# Other\n")
	anchors := []string{"sec-1", "sec-2", "sec-3", "sec-3a", "sec-4", "sec-open-questions"}
	headings := []string{"One", "Two", "Three", "Three-a", "Four", "Open questions"}
	for i, a := range anchors {
		if a == "sec-open-questions" {
			exec(`INSERT INTO doc_rules (doc_id, position, heading, depth, anchor) VALUES ($1, $2, $3, 2, $4)`, spec, i, headings[i], a)
		} else {
			id := 9001 + i
			exec(`INSERT INTO rules (id, project_id, number, kind, status) VALUES ($1, 'pm', $2, 'requirement', 'accepted')`, id, i+1)
			exec(`INSERT INTO rule_versions (rule_id, version, heading, body) VALUES ($1, 1, $2, E'\nText.\n')`, id, headings[i])
			exec(`INSERT INTO doc_rules (doc_id, position, rule_id, rule_version, depth, anchor) VALUES ($1, $2, $3, 1, 2, $4)`, spec, i, id, a)
		}
		exec(`INSERT INTO doc_sections (doc_id, anchor, number, heading, depth, position, published)
		      VALUES ($1, $2, nullif(substr($2, 5), 'open-questions'), $3, 2, $4, true)`, spec, a, headings[i], i)
	}
	// The plan's covers #sec-4 resolved to the rule there when written.
	exec(`INSERT INTO doc_edges (from_doc, type, to_rule) VALUES ($1, 'covers', 9005)`, plan)
	exec(`INSERT INTO doc_edges (from_doc, type, to_doc, to_anchor) VALUES ($1, 'requires', $2, 'sec-4')`, other, spec)
	exec(`INSERT INTO doc_notes (doc_id, anchor, body, created_at) VALUES ($1, 'sec-3a', 'n', now())`, spec)

	migrateSteps(t, s, 1)

	column := func(q string, args ...any) []string {
		t.Helper()
		rows, err := db.Query(q, args...)
		if err != nil {
			t.Fatal(err)
		}
		out, err := scanColumn[string](rows, q)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	want := []string{"sec-1", "sec-2", "sec-3", "sec-4", "sec-5", "sec-open-questions"}
	if got := column(`SELECT anchor FROM doc_sections WHERE doc_id = $1 ORDER BY position`, spec); !slices.Equal(got, want) {
		t.Errorf("section anchors = %v, want %v", got, want)
	}
	if got := column(`SELECT coalesce(number, '') FROM doc_sections WHERE doc_id = $1 ORDER BY position`, spec); !slices.Equal(got, []string{"1", "2", "3", "4", "5", ""}) {
		t.Errorf("section numbers = %v", got)
	}
	if got := column(`SELECT coalesce(slug, '') FROM doc_rules WHERE doc_id = $1 ORDER BY position`, spec); !slices.Equal(got, []string{"", "", "", "", "", "sec-open-questions"}) {
		t.Errorf("slugs = %v", got)
	}
	if got := column(`SELECT rule_id::text || ' ' || anchor FROM covered_sections WHERE plan_id = $1`, plan); !slices.Equal(got, []string{"9005 sec-5"}) {
		t.Errorf("plan covers = %v, want the same rule 9005 at sec-5", got)
	}
	if got := column(`SELECT to_anchor FROM doc_edges WHERE from_doc = $1`, other); !slices.Equal(got, []string{"sec-5"}) {
		t.Errorf("requires to_anchor = %v, want sec-5", got)
	}
	if got := column(`SELECT anchor FROM doc_notes WHERE doc_id = $1`, spec); !slices.Equal(got, []string{"sec-4"}) {
		t.Errorf("note anchor = %v, want sec-4", got)
	}

	if err := s.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDoc(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"## 4. Three-a (PM-REQ-4) {#sec-4}", "## 5. Four (PM-REQ-5) {#sec-5}", "## Open questions {#sec-open-questions}"} {
		if !strings.Contains(got.Body, w) {
			t.Errorf("rendered body lacks %q:\n%s", w, got.Body)
		}
	}
	// The schema half reverses and reapplies.
	migrateSteps(t, s, -1)
	migrateSteps(t, s, 1)
}
