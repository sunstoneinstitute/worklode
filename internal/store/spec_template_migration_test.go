package store

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// The fixture corpus of TestSpecTemplateMigration: spec PM-SPEC-1 has a
// preamble, a heading-only rule over two rules and an unanchored ## Sources;
// PM-SPEC-2 is anchored throughout and has none of these.
const (
	tmplSpecOne = "# One\n\nIntro paragraph.\n\n## 1. Overview {#sec-1}\n\n" +
		"### 1.1 Leases {#sec-1.1}\n\nA lease is held. See PM-REQ-1.\n\n" +
		"### 1.2 Background {#sec-1.2}\n\nWhy leases.\n\n" +
		"```\n## not a heading {#sec-9}\n```\n\n" +
		"## Sources\n\nA list.\n"
	tmplSpecTwo = "## 1. Alpha {#sec-1}\n\nAlpha text.\n\n## 2. Beta {#sec-2}\n\nBeta text.\n"
)

// TestSpecTemplateMigration applies the WL-SPEC-77 §19.7 migration to the
// fixture corpus: the heading-only rule becomes a spec heading, its covers
// edge and governedBy link move to the requirement under it, rule text
// leaves docs.body while the preamble and ## Sources stay, and the rendered
// specs still read end to end. The report rows reach `lode rule lint`.
func TestSpecTemplateMigration(t *testing.T) {
	t.Parallel()
	s := OpenUnmigratedTestStore(t)
	version := migrationVersion(t, "_spec-template-text.up.sql")
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
	doc := func(kind string, number int, status, body string) int64 {
		t.Helper()
		var id int64
		if err := db.QueryRow(
			`INSERT INTO docs (project_id, kind, number, slug, title, body, status, created_at, updated_at)
			 VALUES ('pm', $1, $2, $5, $5, $3, $4, now(), now()) RETURNING id`,
			kind, number, body, status, fmt.Sprint(kind, number)).Scan(&id); err != nil {
			t.Fatalf("insert %s %d: %v", kind, number, err)
		}
		return id
	}
	// rule arranges rule PM-<n> at anchor in doc at position pos.
	rule := func(n int, kind, status string, docID int64, pos, depth int, anchor, heading, body string) {
		t.Helper()
		id := 9000 + n
		exec(`INSERT INTO rules (id, project_id, number, kind, status) VALUES ($1, 'pm', $2, $3, $4)`, id, n, kind, status)
		exec(`INSERT INTO rule_versions (rule_id, version, heading, body) VALUES ($1, 1, $2, $3)`, id, heading, body)
		exec(`INSERT INTO doc_rules (doc_id, position, rule_id, rule_version, depth, anchor) VALUES ($1, $2, $3, 1, $4, $5)`,
			docID, pos, id, depth, anchor)
	}

	exec(`INSERT INTO projects (id, name, key) VALUES ('pm', 'PM', 'PM')`)
	one := doc("spec", 1, "accepted", tmplSpecOne)
	two := doc("spec", 2, "draft", tmplSpecTwo)
	plan := doc("plan", 3, "accepted", "# Plan\n")
	rule(1, "requirement", "accepted", one, 0, 2, "sec-1", "Overview", "\n")
	rule(2, "requirement", "accepted", one, 1, 3, "sec-1.1", "Leases", "\nA lease is held. See PM-REQ-1.\n\n")
	rule(3, "principle", "accepted", one, 2, 3, "sec-1.2", "Background", "\nWhy leases.\n\n```\n## not a heading {#sec-9}\n```\n\n")
	rule(4, "requirement", "draft", two, 0, 2, "sec-1", "Alpha", "\nAlpha text.\n\n")
	rule(5, "requirement", "draft", two, 1, 2, "sec-2", "Beta", "\nBeta text.\n")
	exec(`INSERT INTO doc_edges (from_doc, type, to_rule) VALUES ($1, 'covers', 9001)`, plan)
	exec(`INSERT INTO tasks (id, project_id, kind, title, priority, state, plan_doc, plan_task_key, created_at, updated_at)
	      VALUES ('PM-1', 'pm', 'feature', 'build it', 'low', 'ready', $1, '1', now(), now())`, plan)
	exec(`INSERT INTO task_governed_by (task_id, rule_id, rule_version, source) VALUES ('PM-1', 9001, 1, 'plan')`)
	// An open candidate revision and a version snapshot arrange the rule too.
	exec(`INSERT INTO doc_revisions (doc_id, body, created_at) VALUES ($1, $2, now())`, one, tmplSpecOne)
	exec(`INSERT INTO doc_revision_rules (doc_id, position, rule_id, rule_version, depth, anchor)
	      SELECT doc_id, position, rule_id, rule_version, depth, anchor FROM doc_rules WHERE doc_id = $1`, one)
	exec(`INSERT INTO doc_rule_versions (doc_id, version, position, rule_id, rule_version, depth, anchor)
	      SELECT doc_id, 1, position, rule_id, rule_version, depth, anchor FROM doc_rules WHERE doc_id = $1`, one)

	migrateSteps(t, s, 1)

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM rules WHERE id = 9001`).Scan(&n); err != nil || n != 0 {
		t.Errorf("heading-only rule left %d rows, err %v", n, err)
	}
	for _, table := range []string{"doc_rules", "doc_revision_rules", "doc_rule_versions"} {
		var heading string
		if err := db.QueryRow(`SELECT heading FROM `+table+` WHERE doc_id = $1 AND anchor = 'sec-1' AND rule_id IS NULL`, one).
			Scan(&heading); err != nil || heading != "Overview" {
			t.Errorf("%s sec-1 = heading %q, err %v; want the spec heading Overview", table, heading, err)
		}
	}
	covers := intColumn(t, s, `SELECT to_rule FROM doc_edges WHERE from_doc = $1 AND type = 'covers' ORDER BY to_rule`, plan)
	if !slices.Equal(covers, []int64{9002}) {
		t.Errorf("plan covers %v, want the requirement under the heading only [9002]", covers)
	}
	governed := intColumn(t, s, `SELECT rule_id FROM task_governed_by WHERE task_id = 'PM-1'`)
	if !slices.Equal(governed, []int64{9002}) {
		t.Errorf("PM-1 governed by %v, want [9002]", governed)
	}

	wantOne := "# One\n\nIntro paragraph.\n\n## 1. Overview {#sec-1}\n\n" +
		"### 1.1 Leases {#sec-1.1}\n### 1.2 Background {#sec-1.2}\n## Sources\n\nA list.\n"
	wantTwo := "## 1. Alpha {#sec-1}\n## 2. Beta {#sec-2}\n"
	for _, tc := range []struct {
		id         int64
		before     string
		want       string
		ruleAnchor []string
	}{
		{one, tmplSpecOne, wantOne, []string{"sec-1.1", "sec-1.2"}},
		{two, tmplSpecTwo, wantTwo, []string{"sec-1", "sec-2"}},
	} {
		var body string
		if err := db.QueryRow(`SELECT body FROM docs WHERE id = $1`, tc.id).Scan(&body); err != nil {
			t.Fatal(err)
		}
		if body != tc.want {
			t.Errorf("doc %d stored body:\n%q\nwant:\n%q", tc.id, body, tc.want)
		}
		rules := map[string]bool{}
		for _, a := range tc.ruleAnchor {
			rules[a] = true
		}
		if got := specTemplate(tc.before, rules); got != tc.want {
			t.Errorf("specTemplate disagrees with the migration on doc %d:\n%q", tc.id, got)
		}
	}

	got, err := s.GetDoc(t.Context(), one)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != tmplSpecOne {
		t.Errorf("rendered PM-SPEC-1:\n%q\nwant the text it had:\n%q", got.Body, tmplSpecOne)
	}
	if got, err = s.GetDoc(t.Context(), two); err != nil || got.Body != tmplSpecTwo {
		t.Errorf("rendered PM-SPEC-2:\n%q, err %v", got.Body, err)
	}

	lint, err := s.RuleLint(t.Context(), "pm")
	if err != nil {
		t.Fatal(err)
	}
	var findings []string
	for _, f := range lint.Findings {
		if f.Check == "converted-heading" || f.Check == "unresolved-ref" {
			findings = append(findings, fmt.Sprintf("%s %s %s", f.Rule, f.Check, f.Detail))
		}
	}
	want := []string{
		`PM-REQ-2 unresolved-ref PM-RULE-1 names no rule`,
		`PM-REQ-1 converted-heading now spec heading PM-SPEC-1#sec-1 "Overview"; covers from 1 plan(s) and governedBy of 1 task(s) moved to 1 rule(s)`,
	}
	if !slices.Equal(findings, want) {
		t.Errorf("lint findings:\n%s\nwant:\n%s", strings.Join(findings, "\n"), strings.Join(want, "\n"))
	}
}

// migrationVersion is the number the migration whose name ends in suffix
// has in MigrationsDirForTests.
func migrationVersion(t *testing.T, suffix string) int {
	t.Helper()
	entries, err := os.ReadDir(MigrationsDirForTests())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		var v int
		if strings.HasSuffix(e.Name(), suffix) {
			if _, err := fmt.Sscanf(e.Name(), "%04d_", &v); err == nil {
				return v
			}
		}
	}
	t.Fatalf("migration *%s not found", suffix)
	return 0
}

// intColumn reads one bigint column.
func intColumn(t *testing.T, s *Store, q string, args ...any) []int64 {
	t.Helper()
	rows, err := s.DBForTests().Query(q, args...)
	if err != nil {
		t.Fatal(err)
	}
	out, err := scanColumn[int64](rows, q)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
