package store

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// coversPlanBody is a mintable plan covering refs.
func coversPlanBody(refs ...string) string {
	return "---\nstatus: draft\ncovers: [" + strings.Join(refs, ", ") + "]\n---\n# Plan\n\n## Tasks\n\n" +
		"### Task 1 — First\n\n```yaml\nkind: feature\n```\n\nDo it.\n"
}

// coveredRuleNumbers is the RULE numbers a plan's covers edges point at.
func coveredRuleNumbers(t *testing.T, s *Store, planID int64) []int64 {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT r.number FROM doc_edges e JOIN rules r ON r.id = e.to_rule
		  WHERE e.from_doc = $1 AND e.type = 'covers' ORDER BY r.number`, planID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := scanColumn[int64](rows, "covered rules")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// externalCovers is the covers refs a plan kept verbatim.
func externalCovers(t *testing.T, s *Store, planID int64) []string {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT to_external FROM doc_edges
		  WHERE from_doc = $1 AND type = 'covers' AND to_external IS NOT NULL ORDER BY to_external`, planID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := scanColumn[string](rows, "external covers")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestCoversResolveToRules: each covers entry is stored as edges from the
// plan to rules, resolved when the plan is written (WL-SPEC-77 §4). A rule
// ref, in either spelling, names that rule; a section ref names the rule at
// that anchor and every rule under it; a whole-document ref names every rule
// the document contains; an entry naming no rule is kept verbatim. Where a
// nested section has its own entry, that entry sets the nested rule's level.
// A plan has no doc_rules rows.
func TestCoversResolveToRules(t *testing.T) {
	s := openDocStore(t)
	// ruleDocV1: sec-1 is rule 1, sec-1.1 rule 2, sec-2 rule 3.
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})

	mixed := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "mixed", CreatedBy: "stig",
		Body: coversPlanBody("P1-RULE-3", "P1-SPEC-1#sec-1", "P1-RULE-99", "P1-SPEC-1#sec-9")})
	if got := coveredRuleNumbers(t, s, mixed.ID); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Errorf("rule ref and section ref cover %v, want [1 2 3]", got)
	}
	leaf := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "leaf", CreatedBy: "stig",
		Body: coversPlanBody("P1-SPEC-1#sec-1.1")})
	if got := coveredRuleNumbers(t, s, leaf.ID); !slices.Equal(got, []int64{2}) {
		t.Errorf("leaf section ref covers %v, want [2]", got)
	}

	nested := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "nested", CreatedBy: "stig",
		Body: coversPlanBody("{spec: P1-SPEC-1#sec-1, coverage: none}", "P1-SPEC-1#sec-1.1")})
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT r.number || '=' || e.coverage FROM doc_edges e JOIN rules r ON r.id = e.to_rule
		  WHERE e.from_doc = $1 AND e.type = 'covers' ORDER BY r.number`, nested.ID)
	if err != nil {
		t.Fatal(err)
	}
	levels, err := scanColumn[string](rows, "nested covers")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(levels, []string{"1=none", "2=full"}) {
		t.Errorf("nested covers = %v, want [1=none 2=full]: the nested entry sets its own rule's level", levels)
	}
	if got := externalCovers(t, s, mixed.ID); !slices.Equal(got, []string{"P1-RULE-99", "P1-SPEC-1#sec-9"}) {
		t.Errorf("unresolved covers = %v, want the unknown rule and the missing anchor verbatim", got)
	}

	legacy := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "legacy", CreatedBy: "stig",
		Body: coversPlanBody("P1-CL-2")})
	if got := coveredRuleNumbers(t, s, legacy.ID); !slices.Equal(got, []int64{2}) {
		t.Errorf("WL-CL spelling covers %v, want [2]", got)
	}

	whole := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "whole", CreatedBy: "stig",
		Body: coversPlanBody("P1-SPEC-1")})
	if got := coveredRuleNumbers(t, s, whole.ID); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Errorf("whole-document ref covers %v, want [1 2 3]", got)
	}
	if _, _, err := acceptDoc(t, s, whole.ID, "stig"); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT count(*) FROM doc_rules dr JOIN docs d ON d.id = dr.doc_id WHERE d.kind = 'plan'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("plans hold %d doc_rules rows, want none", n)
	}
}

// TestCoverageFollowsSupersession: a plan covering a rule counts as covering
// every rule that supersedes it (WL-SPEC-77 §4). The successor's section is
// discharged, the rule detail lists the plan, the successor's document lists
// the inbound covers edge, and withdrawing the successor marks the plan
// stale (WL-SPEC-77 §9).
func TestCoverageFollowsSupersession(t *testing.T) {
	s := openDocStore(t)
	specA := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "a", Body: ruleDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, specA.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", CreatedBy: "stig",
		Body: coversPlanBody("P1-SPEC-1#sec-1")})
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	// Spec B's sec-1 is rule 4.
	specB := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "b", CreatedBy: "stig",
		Body: "---\nstatus: draft\n---\n# B\n\n## 1. New one {#sec-1}\n\nA2.\n"})
	if _, _, err := acceptDoc(t, s, specB.ID, "stig"); err != nil {
		t.Fatal(err)
	}

	if res := mustSupersede(t, s, entry("P1-RULE-1", "P1-RULE-4")); res.StalePlans != 1 {
		t.Errorf("stale plans = %d, want 1", res.StalePlans)
	}
	// Re-planning (an edit, then accept) clears the stale mark; the covers
	// edge still points at rule 1, which spec a still arranges at sec-1.
	if _, err := updateDocBody(t, s, plan.ID, coversPlanBody("P1-SPEC-1#sec-1")+"\nRe-planned.\n"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}

	r, err := s.GetRule(t.Context(), "P1", 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.RulePlan{{Doc: plan.ID, DocRef: "P1-PLAN-1", Status: "accepted", Coverage: "full"}}
	if !slices.Equal(r.CoveredBy, want) {
		t.Errorf("P1-RULE-4 covered by %+v, want %+v", r.CoveredBy, want)
	}

	slugs, _ := needsPlanningSlugs(t, s, "p1")
	if slices.Contains(slugs, "b") {
		t.Errorf("needs planning = %v, want spec b's only section discharged through supersession", slugs)
	}

	_, in, err := s.ListDocEdges(t.Context(), specB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(in) != 1 || in[0].Type != "isCoveredBy" || in[0].FromAnchor != "sec-1" || in[0].ToDoc != plan.ID {
		t.Errorf("spec b edges in = %+v, want isCoveredBy at sec-1 from the plan", in)
	}

	if res := mustSupersede(t, s, entry("P1-RULE-4", "P1-RULE-3")); res.StalePlans != 1 {
		t.Errorf("withdrawing the successor: stale plans = %d, want 1", res.StalePlans)
	}
	if d, err := s.GetDoc(t.Context(), plan.ID); err != nil || d.Status != "stale" {
		t.Errorf("plan status = %v %v, want stale", d.Status, err)
	}
}

// TestEditRuleWritesThroughTheSpec: a rule a plan covers is edited through
// the spec arranging it; the plan contains no rules.
func TestEditRuleWritesThroughTheSpec(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, err := editRule(t, s, "P1", 1, model.EditRuleInput{Heading: "One", Body: "\nChanged.\n\n"}, "stig"); err != nil {
		t.Fatalf("edit of a rule a plan covers: %v", err)
	}
	d, err := s.GetDoc(t.Context(), spec.ID)
	if err != nil || !strings.Contains(d.Body, "Changed.") {
		t.Fatalf("spec body should carry the edit: %v %q", err, d.Body)
	}
}

// TestPlanCoversMigration: 0091 resolves stored covers edges to rules the way
// a plan write does, keeps each level and fullCoverageWith closure, keeps an
// edge naming no rule verbatim, deletes plan doc_rules rows, and round-trips.
func TestPlanCoversMigration(t *testing.T) {
	t.Parallel()
	s := OpenUnmigratedTestStore(t)
	if err := s.Migrate(migrationsThrough(t, 90)); err != nil {
		t.Fatalf("migrate through 0090: %v", err)
	}
	db := s.DBForTests()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	id := func(q string, args ...any) int64 {
		t.Helper()
		var v int64
		if err := db.QueryRow(q, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return v
	}
	exec(`INSERT INTO projects (id, name, key) VALUES ('p1', 'P1', 'P1')`)
	doc := func(kind string, number int, slug string) int64 {
		return id(`INSERT INTO docs (project_id, kind, number, slug, title, body, status, created_at, updated_at)
		           VALUES ('p1', $1, $2, $3, $3, 'body', 'accepted', now(), now()) RETURNING id`, kind, number, slug)
	}
	rule := func(number int, docID int64, pos int, anchor string) int64 {
		r := id(`INSERT INTO rules (project_id, number, status) VALUES ('p1', $1, 'accepted') RETURNING id`, number)
		exec(`INSERT INTO rule_versions (rule_id, version, heading, body) VALUES ($1, 1, 'h', 'b')`, r)
		exec(`INSERT INTO doc_rules (doc_id, position, rule_id, rule_version, depth, anchor) VALUES ($1, $2, $3, 1, 1, $4)`,
			docID, pos, r, anchor)
		return r
	}
	spec := doc("spec", 1, "spec-1")
	successor := doc("spec", 2, "spec-2")
	r1 := rule(1, spec, 0, "sec-1")
	r2 := rule(2, spec, 1, "sec-2")
	r3 := rule(3, successor, 0, "sec-1")
	exec(`INSERT INTO rule_edges (from_rule, to_rule, type, source) VALUES ($1, $2, 'supersedes', 'refactor')`, r3, r1)
	planA := doc("plan", 1, "plan-a")
	planB := doc("plan", 2, "plan-b")
	// Plan A: a partial section edge with a closure, a whole-document edge,
	// and the plan arrangement the old code wrote.
	sectionEdge := id(`INSERT INTO doc_edges (from_doc, type, to_doc, to_anchor, coverage, declared_by)
	                   VALUES ($1, 'covers', $2, 'sec-1', 'partial', $1) RETURNING id`, planA, spec)
	exec(`INSERT INTO doc_coverage_completed_with (edge_id, position, to_external) VALUES ($1, 0, 'x.md')`, sectionEdge)
	exec(`INSERT INTO doc_edges (from_doc, type, to_doc, coverage, declared_by) VALUES ($1, 'covers', $2, 'full', $1)`, planA, spec)
	exec(`INSERT INTO doc_edges (from_doc, type, to_doc, declared_by) VALUES ($1, 'requires', $2, $1)`, planA, spec)
	exec(`INSERT INTO doc_rules (doc_id, position, rule_id, rule_version, depth, anchor) VALUES ($1, 0, $2, 1, 1, 'sec-1')`, planA, r1)
	// Plan B: an anchor naming no rule.
	exec(`INSERT INTO doc_edges (from_doc, type, to_doc, to_anchor, coverage, declared_by)
	      VALUES ($1, 'covers', $2, 'sec-9', 'full', $1)`, planB, spec)

	migrateSteps(t, s, 1)

	type edge struct {
		rule     int64
		coverage string
		closure  string
	}
	rows, err := db.Query(
		`SELECT e.to_rule, e.coverage, coalesce(string_agg(w.to_external, ','), '')
		   FROM doc_edges e LEFT JOIN doc_coverage_completed_with w ON w.edge_id = e.id
		  WHERE e.from_doc = $1 AND e.type = 'covers'
		  GROUP BY e.id ORDER BY e.to_rule`, planA)
	if err != nil {
		t.Fatal(err)
	}
	got, err := collectRows(rows, "plan a covers", func(r rowScanner) (edge, error) {
		var e edge
		err := r.Scan(&e.rule, &e.coverage, &e.closure)
		return e, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []edge{{r1, "partial", "x.md"}, {r2, "full", ""}}; !slices.Equal(got, want) {
		t.Errorf("plan a covers = %+v, want %+v", got, want)
	}
	var ext string
	if err := db.QueryRow(`SELECT to_external FROM doc_edges WHERE from_doc = $1 AND type = 'covers'`, planB).Scan(&ext); err != nil || ext != "P1-SPEC-1#sec-9" {
		t.Errorf("plan b covers = %q %v, want P1-SPEC-1#sec-9", ext, err)
	}
	if n := id(`SELECT count(*) FROM doc_rules WHERE doc_id = $1`, planA); n != 0 {
		t.Errorf("plan a keeps %d doc_rules rows, want none", n)
	}
	if n := id(`SELECT count(*) FROM doc_edges WHERE from_doc = $1 AND type = 'requires' AND to_doc = $2`, planA, spec); n != 1 {
		t.Errorf("requires edges = %d, want 1 untouched", n)
	}
	if n := id(`SELECT count(*) FROM covered_sections WHERE plan_id = $1 AND doc_id = $2 AND anchor = 'sec-1'`, planA, successor); n != 1 {
		t.Errorf("successor section covered %d times, want 1 through supersedes", n)
	}

	migrateSteps(t, s, -1)
	if n := id(`SELECT count(*) FROM doc_edges WHERE from_doc = $1 AND type = 'covers' AND to_doc = $2 AND to_anchor IN ('sec-1', 'sec-2')`, planA, spec); n != 2 {
		t.Errorf("after down, plan a section covers = %d, want 2", n)
	}
	migrateSteps(t, s, 1)
}

// TestCoversSubtreeMigration: 0092 extends each covers edge to the rules under
// its rule's section (WL-SPEC-77 §4), with the edge's level and closure. A
// rule the plan already covers keeps its own edge, and a rule under two
// covered ancestors takes the nearer one's level.
func TestCoversSubtreeMigration(t *testing.T) {
	t.Parallel()
	s := OpenUnmigratedTestStore(t)
	if err := s.Migrate(migrationsThrough(t, 91)); err != nil {
		t.Fatalf("migrate through 0091: %v", err)
	}
	db := s.DBForTests()
	id := func(q string, args ...any) int64 {
		t.Helper()
		var v int64
		if err := db.QueryRow(q, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return v
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO projects (id, name, key) VALUES ('p1', 'P1', 'P1')`)
	doc := func(kind string, number int) int64 {
		return id(`INSERT INTO docs (project_id, kind, number, slug, title, body, status, created_at, updated_at)
		           VALUES ('p1', $1, $2, $3, 't', 'body', 'accepted', now(), now()) RETURNING id`, kind, number, kind+strconv.Itoa(number))
	}
	spec := doc("spec", 1)
	// sec-1 (depth 2) > sec-1.1 (3) > sec-1.1.1 (4), sec-1.2 (3); sec-2 (2).
	var rules []int64
	for i, a := range []struct {
		anchor string
		depth  int
	}{{"sec-1", 2}, {"sec-1.1", 3}, {"sec-1.1.1", 4}, {"sec-1.2", 3}, {"sec-2", 2}} {
		r := id(`INSERT INTO rules (project_id, number, status) VALUES ('p1', $1, 'accepted') RETURNING id`, i+1)
		exec(`INSERT INTO rule_versions (rule_id, version, heading, body) VALUES ($1, 1, 'h', 'b')`, r)
		exec(`INSERT INTO doc_rules (doc_id, position, rule_id, rule_version, depth, anchor) VALUES ($1, $2, $3, 1, $4, $5)`,
			spec, i, r, a.depth, a.anchor)
		rules = append(rules, r)
	}
	cover := func(plan, rule int64, level string) int64 {
		return id(`INSERT INTO doc_edges (from_doc, type, to_rule, coverage, declared_by)
		           VALUES ($1, 'covers', $2, $3, $1) RETURNING id`, plan, rule, level)
	}
	plan := doc("plan", 1)
	e1 := cover(plan, rules[0], "partial")
	exec(`INSERT INTO doc_coverage_completed_with (edge_id, position, to_external) VALUES ($1, 0, 'x.md')`, e1)
	cover(plan, rules[1], "none")
	cover(plan, rules[3], "full")

	migrateSteps(t, s, 1)

	rows, err := db.Query(
		`SELECT r.number || '=' || e.coverage || coalesce(':' || string_agg(w.to_external, ','), '')
		   FROM doc_edges e JOIN rules r ON r.id = e.to_rule
		   LEFT JOIN doc_coverage_completed_with w ON w.edge_id = e.id
		  WHERE e.from_doc = $1 AND e.type = 'covers'
		  GROUP BY e.id, r.number ORDER BY r.number`, plan)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scanColumn[string](rows, "plan covers")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"1=partial:x.md", "2=none", "3=none", "4=full"}; !slices.Equal(got, want) {
		t.Errorf("plan covers = %v, want %v", got, want)
	}
}
