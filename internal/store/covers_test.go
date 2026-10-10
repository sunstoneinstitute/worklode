package store

import (
	"database/sql"
	"reflect"
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
// plan to rules, resolved when the plan is written (WL-REQ-165). A rule
// ref, in either spelling, names that rule; a section ref names the rule at
// that anchor and every rule under it; a whole-document ref names every rule
// the document contains; an entry naming no rule is kept verbatim. Nested
// entries overlap. A plan has no doc_rules rows.
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
		Body: coversPlanBody("P1-SPEC-1#sec-1", "P1-SPEC-1#sec-1.1")})
	if got := coveredRuleNumbers(t, s, nested.ID); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("nested covers = %v, want [1 2]: nested entries overlap", got)
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
// every rule that supersedes it (WL-REQ-165). The successor's section is
// discharged, the rule detail lists the plan, the successor's document lists
// the inbound covers edge, and withdrawing the successor marks the plan
// stale (WL-REQ-170).
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
	want := []model.RulePlan{{Doc: plan.ID, DocRef: "P1-PLAN-1", Status: "accepted"}}
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

// TestEditRuleShowsInTheDraftSpec: a rule a plan covers is edited as a rule,
// and the draft spec arranging it renders the edit; the plan contains no
// rules.
func TestEditRuleShowsInTheDraftSpec(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if err := editRule(t, s, "P1", 1, model.EditRuleInput{Heading: "One", Body: "\nChanged.\n\n"}, "stig"); err != nil {
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
// its rule's section (WL-REQ-165), with the edge's level and closure. A
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

// TestRetireCoverageLevelsMigration: the coverage-level retirement deletes
// the WL-928 N1 and N2 edges, every classified entry naming no rule, and the
// N3 `none` parents; keeps partial, unsure and unclassified edges as plain
// covers edges; moves a defers owner onto the edge row; and round-trips.
func TestRetireCoverageLevelsMigration(t *testing.T) {
	t.Parallel()
	s := OpenUnmigratedTestStore(t)
	if err := s.Migrate(migrationsThrough(t, 97)); err != nil {
		t.Fatalf("migrate through 0097: %v", err)
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
	exec(`INSERT INTO projects (id, name, key) VALUES ('wl', 'WL', 'WL'), ('dp', 'DP', 'DP')`)
	doc := func(project, kind string, number int) int64 {
		return id(`INSERT INTO docs (project_id, kind, number, slug, title, body, status, created_at, updated_at)
		           VALUES ($1, $2, $3, $4, 't', 'body', 'accepted', now(), now()) RETURNING id`,
			project, kind, number, project+"-"+kind+strconv.Itoa(number))
	}
	spec := doc("wl", "spec", 38)
	pos := 0
	rule := func(number, depth int) int64 {
		r := id(`INSERT INTO rules (project_id, number, status) VALUES ('wl', $1, 'accepted') RETURNING id`, number)
		exec(`INSERT INTO rule_versions (rule_id, version, heading, body) VALUES ($1, 1, 'h', 'b')`, r)
		exec(`INSERT INTO doc_rules (doc_id, position, rule_id, rule_version, depth, anchor) VALUES ($1, $2, $3, 1, $4, $5)`,
			spec, pos, r, depth, "sec-"+strconv.Itoa(number))
		pos++
		return r
	}
	n1, n2, unsure := rule(825, 2), rule(829, 2), rule(821, 2)
	parent, child, plain := rule(900, 2), rule(901, 3), rule(902, 2)
	plan1, plan106, plan7 := doc("wl", "plan", 1), doc("wl", "plan", 106), doc("wl", "plan", 7)
	dpPlan := doc("dp", "plan", 1)
	cover := func(plan, rule int64, level string) {
		exec(`INSERT INTO doc_edges (from_doc, type, to_rule, coverage) VALUES ($1, 'covers', $2, $3)`, plan, rule, level)
	}
	cover(plan1, n1, "none")
	cover(plan1, n2, "none")
	cover(plan1, plain, "none") // standalone, unclassified
	cover(plan106, unsure, "none")
	cover(plan7, parent, "none") // N3: its child is covered
	cover(plan7, child, "partial")
	for _, ext := range []string{"DP-SPEC-1#sec-0", "DP-SPEC-1#sec-14"} { // F3: N1 and unsure
		exec(`INSERT INTO doc_edges (from_doc, type, to_external, coverage) VALUES ($1, 'covers', $2, 'none')`, dpPlan, ext)
	}
	deferral := id(`INSERT INTO doc_edges (from_doc, type, to_doc, to_anchor) VALUES ($1, 'defers', $2, 'sec-1') RETURNING id`, plan7, spec)
	exec(`INSERT INTO doc_coverage_completed_with (edge_id, position, to_doc) VALUES ($1, 0, $2)`, deferral, spec)

	migrateSteps(t, s, 1)

	rows, err := db.Query(
		`SELECT d.slug || ':' || coalesce(r.number::text, e.to_external)
		   FROM doc_edges e JOIN docs d ON d.id = e.from_doc LEFT JOIN rules r ON r.id = e.to_rule
		  WHERE e.type = 'covers'`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scanColumn[string](rows, "covers after")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if want := []string{"wl-plan106:821", "wl-plan1:902", "wl-plan7:901"}; !slices.Equal(got, want) {
		t.Errorf("covers after = %v, want %v", got, want)
	}
	if owner := id(`SELECT owner_doc FROM doc_edges WHERE id = $1`, deferral); owner != spec {
		t.Errorf("defers owner = %d, want %d", owner, spec)
	}
	if n := id(`SELECT count(*) FROM information_schema.columns
	             WHERE table_name IN ('doc_edges', 'doc_edge_versions', 'doc_revision_edges')
	               AND column_name IN ('coverage', 'completed_with')`); n != 0 {
		t.Errorf("%d level columns remain, want none", n)
	}

	migrateSteps(t, s, -1)
	if n := id(`SELECT count(*) FROM doc_coverage_completed_with WHERE edge_id = $1 AND to_doc = $2`, deferral, spec); n != 1 {
		t.Errorf("after down, defers owner rows = %d, want 1", n)
	}
	if n := id(`SELECT count(*) FROM doc_edges WHERE type = 'covers' AND coverage = 'full'`); n != 3 {
		t.Errorf("after down, full covers edges = %d, want 3", n)
	}
	migrateSteps(t, s, 1)
}

// TestCoversResolveWhenRulesMinted: a plan's covers entries naming a spec
// with no rules stay in to_external until an edit mints the spec's rules;
// that write re-resolves them in its own transaction (WL-903).
func TestCoversResolveWhenRulesMinted(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", CreatedBy: "stig",
		Body: "---\nstatus: draft\n---\n# T\n\nNo sections yet.\n"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "stranded", CreatedBy: "stig",
		Body: coversPlanBody("P1-SPEC-1", "P1-SPEC-1#sec-2", "P1-SPEC-9")})
	if got := externalCovers(t, s, plan.ID); len(got) != 3 {
		t.Fatalf("before rules: to_external = %v, want all three entries", got)
	}

	if _, err := updateDocBody(t, s, spec.ID, ruleDocV1); err != nil {
		t.Fatal(err)
	}
	if got := coveredRuleNumbers(t, s, plan.ID); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Errorf("after rules: covered %v, want [1 2 3]", got)
	}
	if got := externalCovers(t, s, plan.ID); !slices.Equal(got, []string{"P1-SPEC-9"}) {
		t.Errorf("after rules: to_external = %v, want only the unknown spec", got)
	}
	var logged int
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT count(*) FROM state_log WHERE entity_kind = 'doc' AND entity_id = $1
		    AND change->>'field' = 'edges'`, strconv.FormatInt(plan.ID, 10)).Scan(&logged); err != nil {
		t.Fatal(err)
	}
	if logged != 1 {
		t.Errorf("plan has %d edges state_log rows, want 1", logged)
	}
}

// TestResolveExternalCovers: the repair for rows stranded before WL-903
// resolves each to_external covers entry that now names requirements,
// reports the rest with the reason, and changes nothing on a second run.
func TestResolveExternalCovers(t *testing.T) {
	s := openDocStore(t)
	// ruleDocV1: sec-1 is rule 1, sec-1.1 rule 2, sec-2 rule 3.
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "stranded", CreatedBy: "stig",
		Body: coversPlanBody("P1-SPEC-9")})
	for _, q := range []string{
		`UPDATE rules SET kind = 'invariant' WHERE number = 3`,
		`INSERT INTO doc_edges (from_doc, type, to_external) VALUES ($1, 'covers', 'P1-SPEC-1#sec-1')`,
		`INSERT INTO doc_edges (from_doc, type, to_external) VALUES ($1, 'covers', 'P1-SPEC-1#sec-2')`,
	} {
		args := []any{plan.ID}
		if !strings.Contains(q, "$1") {
			args = nil
		}
		if _, err := s.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}

	run := func() model.CoversResolveResponse {
		t.Helper()
		var out model.CoversResolveResponse
		if _, _, err := s.RecordDocEvent(t.Context(), "resolve", "cli", "covers-resolve-"+strconv.FormatInt(docEventSeq.Add(1), 10),
			"doc.covers_resolved", nil, func(tx *sql.Tx, eventID int64) error {
				var err error
				out, err = ResolveExternalCovers(tx, eventID)
				return err
			}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := run()
	wantResolved := []model.CoversResolution{{Plan: "P1-PLAN-1", Ref: "P1-SPEC-1#sec-1", Rules: []string{"P1-REQ-1", "P1-REQ-2"}}}
	wantUnresolved := []model.CoversResolution{
		{Plan: "P1-PLAN-1", Ref: "P1-SPEC-9", Reason: "names no rule"},
		{Plan: "P1-PLAN-1", Ref: "P1-SPEC-1#sec-2", Reason: "names no requirement"},
	}
	if !reflect.DeepEqual(first.Resolved, wantResolved) || !reflect.DeepEqual(first.Unresolved, wantUnresolved) {
		t.Errorf("first run = %+v, want resolved %+v, unresolved %+v", first, wantResolved, wantUnresolved)
	}
	if got := coveredRuleNumbers(t, s, plan.ID); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("covered %v, want [1 2]", got)
	}
	if got := externalCovers(t, s, plan.ID); !slices.Equal(got, []string{"P1-SPEC-1#sec-2", "P1-SPEC-9"}) {
		t.Errorf("to_external = %v, want the two unresolved entries kept", got)
	}

	second := run()
	if len(second.Resolved) != 0 || !reflect.DeepEqual(second.Unresolved, wantUnresolved) {
		t.Errorf("second run = %+v, want nothing resolved and the same unresolved", second)
	}
	if got := coveredRuleNumbers(t, s, plan.ID); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("second run: covered %v, want [1 2]", got)
	}
}

// TestSpecHeadingArrangesNoRule: an anchored heading followed directly by a
// deeper anchored heading is a spec heading, a doc_rules row with heading set
// and no rule, and a covers entry on its anchor resolves to the rules grouped
// under it (WL-REQ-1295).
func TestSpecHeadingArrangesNoRule(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "h", CreatedBy: "stig",
		Body: "---\nstatus: draft\n---\n# H\n\n## 1. A {#sec-1}\n\n### 1.1 B {#sec-1.1}\n\nText.\n"})

	type row struct {
		Anchor, Heading string
		Rule            sql.NullInt64
	}
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT dr.anchor, coalesce(dr.heading, ''), r.number
		   FROM doc_entries dr LEFT JOIN rules r ON r.id = dr.rule_id
		  WHERE dr.doc_id = $1 ORDER BY dr.position`, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := collectRows(rows, "arrangement", func(r rowScanner) (row, error) {
		var x row
		err := r.Scan(&x.Anchor, &x.Heading, &x.Rule)
		return x, err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []row{{"sec-1", "A", sql.NullInt64{}}, {"sec-1.1", "", sql.NullInt64{Int64: 1, Valid: true}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arrangement = %+v, want %+v", got, want)
	}
	secs, err := s.ListDocSections(t.Context(), spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) != 2 || secs[0].Kind != "heading" || secs[1].Kind != "requirement" {
		t.Errorf("sections = %+v, want kinds heading, requirement", secs)
	}
	var rules int
	if err := s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM rules`).Scan(&rules); err != nil {
		t.Fatal(err)
	}
	if rules != 1 {
		t.Errorf("%d rules minted, want 1", rules)
	}

	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "hp", CreatedBy: "stig",
		Body: coversPlanBody("P1-SPEC-1#sec-1")})
	if got := coveredRuleNumbers(t, s, plan.ID); !slices.Equal(got, []int64{1}) {
		t.Errorf("heading ref covers %v, want [1]", got)
	}
	if got := externalCovers(t, s, plan.ID); len(got) != 0 {
		t.Errorf("unresolved covers = %v, want none", got)
	}
}
