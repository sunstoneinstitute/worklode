package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

// governingNumbers reads the RULE numbers governing a task, in order.
func governingNumbers(t *testing.T, s *Store, taskID string) []int64 {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT c.number FROM task_governed_by g JOIN rules c ON c.id = g.rule_id
		  WHERE g.task_id = $1 ORDER BY c.number`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func equalInt64s(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A plan covering P1-SPEC-1 sec-1 only. The spec is ruleDocV1 (rules.go
// tests): sec-1 is rule 1, its child sec-1.1 is rule 2, sec-2 is rule 3.
const governedPlanBody = "---\nstatus: draft\ncovers: [P1-SPEC-1#sec-1]\n---\n# Plan\n\n## Tasks\n\n### Task 1 — First\n\n```yaml\nkind: feature\n```\n\nDo it.\n\n### Task 2 — Second\n\n```yaml\nkind: chore\n```\n\nDo more.\n"

// TestAcceptPlanGovernsMintedTasks: accepting a plan gives every task it mints
// a governedBy link to each rule the plan's covers edges reach (S2). A
// section-scoped edge reaches the rule at that anchor and the rules
// arranged under it, so covering sec-1 governs by rules 1 and 2 and leaves
// rule 3 out.
func TestAcceptPlanGovernsMintedTasks(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "plan", Body: governedPlanBody, CreatedBy: "stig"})

	var resolved int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM doc_edges WHERE from_doc = $1 AND type = 'covers' AND to_doc IS NOT NULL`, plan.ID).Scan(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved != 1 {
		t.Fatalf("plan's covers edge did not resolve to the spec (%d resolved rows); fix the covers ref in governedPlanBody", resolved)
	}

	_, minted, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if len(minted) != 2 {
		t.Fatalf("minted %d tasks, want 2", len(minted))
	}
	for _, task := range minted {
		if got := governingNumbers(t, s, task.ID); !equalInt64s(got, []int64{1, 2}) {
			t.Errorf("%s governed by %v, want [1 2]", task.ID, got)
		}
	}

	var source string
	var linkVersion int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT source, rule_version FROM task_governed_by WHERE task_id = $1 ORDER BY rule_id LIMIT 1`,
		minted[0].ID).Scan(&source, &linkVersion); err != nil {
		t.Fatal(err)
	}
	if source != "plan" || linkVersion != 1 {
		t.Errorf("link source = %s v%d, want plan v1", source, linkVersion)
	}
}

// TestAcceptPlanSplitsCoveredDocOnFirstUse: a spec written before the rule
// tables existed has no arrangement yet; accepting a plan that covers it
// splits it first so the minted tasks still get their links.
func TestAcceptPlanSplitsCoveredDocOnFirstUse(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	if _, err := s.db.ExecContext(context.Background(), `DELETE FROM doc_rules WHERE doc_id = $1`, spec.ID); err != nil {
		t.Fatal(err)
	}
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "plan", Body: governedPlanBody, CreatedBy: "stig"})
	_, minted, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if len(arrangementOf(t, s, spec.ID)) != 3 {
		t.Errorf("covered spec was not split on first use")
	}
	if got := governingNumbers(t, s, minted[0].ID); len(got) != 2 {
		t.Errorf("%s governed by %v, want two rules", minted[0].ID, got)
	}
}

// TestEnsureRulesBackfillsAcceptedRules: a spec that was accepted before
// the rule tables existed has no arrangement yet. Accepting a plan that
// covers it backfills the arrangement (ensureRules), and because the spec
// is already accepted, its backfilled rules must come out accepted too
// (S11), not stuck at the column default of draft.
func TestEnsureRulesBackfillsAcceptedRules(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, spec.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(context.Background(), `DELETE FROM doc_rules WHERE doc_id = $1`, spec.ID); err != nil {
		t.Fatal(err)
	}
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "plan", Body: governedPlanBody, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	arrangement := arrangementOf(t, s, spec.ID)
	if len(arrangement) != 3 {
		t.Fatalf("covered spec was not re-split on backfill: %d rows", len(arrangement))
	}
	for _, row := range arrangement {
		if row.Status != "accepted" {
			t.Errorf("rule %d status = %q, want accepted (doc %d was already accepted)", row.Number, row.Status, spec.ID)
		}
	}
}

// TestGovernAndUngovern: the architect adds and removes links by hand (S3);
// adding twice is a no-op, removing an absent link and naming an unknown
// task or rule are ErrNotFound.
func TestGovernAndUngovern(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	task := createTask(t, s, time.Now(), TaskInput{ProjectID: "p1", Title: "a task", Body: "b", Priority: "medium", Kind: "bug", CreatedBy: "stig"})

	govern := func(number int64) error {
		_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.governed", nil,
			func(tx *sql.Tx, _ int64) error {
				id, err := RuleIDByRef(tx, "P1", number)
				if err != nil {
					return err
				}
				return Govern(tx, task.ID, id, "manual", false)
			})
		return err
	}
	if err := govern(3); err != nil {
		t.Fatal(err)
	}
	if err := govern(3); err != nil {
		t.Fatalf("second govern should be a no-op: %v", err)
	}
	if got := governingNumbers(t, s, task.ID); !equalInt64s(got, []int64{3}) {
		t.Errorf("governed by %v, want [3]", got)
	}
	if err := govern(99); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown rule: err = %v, want ErrNotFound", err)
	}

	list, err := s.GovernedBy(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Rule != "P1-RULE-3" || list[0].Heading != "Two" || list[0].Source != "manual" ||
		list[0].RuleVersion != 1 || list[0].Current != 1 {
		t.Errorf("GovernedBy = %+v", list)
	}

	ungovern := func(number int64) error {
		_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.ungoverned", nil,
			func(tx *sql.Tx, _ int64) error {
				id, err := RuleIDByRef(tx, "P1", number)
				if err != nil {
					return err
				}
				return Ungovern(tx, task.ID, id)
			})
		return err
	}
	if err := ungovern(3); err != nil {
		t.Fatal(err)
	}
	if err := ungovern(3); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing an absent link: err = %v, want ErrNotFound", err)
	}
	if got := governingNumbers(t, s, task.ID); len(got) != 0 {
		t.Errorf("governed by %v after ungovern, want none", got)
	}
}

// TestGovernPin: a pinned link resolves to the version current at link time
// after the rule moves on; re-governing without the pin unpins (S10).
func TestGovernPin(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	now := s.Now()
	task := createTask(t, s, now, TaskInput{ProjectID: "p1", Title: "t", Kind: "feature", Priority: "medium", CreatedBy: "stig"})
	ctx := context.Background()
	id := ruleID(t, s, "P1", 3)
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return Govern(tx, task.ID, id, "manual", true) }); err != nil {
		t.Fatal(err)
	}
	// Land a revision so the rule is at v2.
	if err := reviseDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if err := updateRevision(t, s, d.ID, strings.Replace(ruleDocV1, "C.\n", "C changed.\n", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptRevision(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	g, err := s.GovernedBy(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 1 || g[0].Pinned != 1 || g[0].Current != 2 || g[0].URL != "/projects/p1/rule/3/1" {
		t.Errorf("pinned: %+v", g)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return Govern(tx, task.ID, id, "manual", false) }); err != nil {
		t.Fatal(err)
	}
	g, _ = s.GovernedBy(ctx, task.ID)
	if len(g) != 1 || g[0].Pinned != 0 || g[0].URL != "/projects/p1/rule/3" ||
		g[0].Source != "manual" || g[0].RuleVersion != 1 {
		t.Errorf("unpinned: %+v", g)
	}
}

// govern links task to rule number in project p1, bypassing RecordEvent
// like TestGovernPin does.
func govern(t *testing.T, s *Store, taskID string, number int64) {
	t.Helper()
	id := ruleID(t, s, "P1", number)
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		return Govern(tx, taskID, id, "manual", false)
	}); err != nil {
		t.Fatal(err)
	}
}

// TestGovernedByResolvesTo: a task governed by B, withdrawn and superseded by
// A1 and A2, and also governed by a live rule C. A1 is itself withdrawn and
// superseded by D. GovernedBy reports B's ResolvesTo as [A2, D] in
// rule-number order, the withdrawn intermediate A1 skipped, and C's
// ResolvesTo empty (S22, R8).
func TestGovernedByResolvesTo(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "u", Body: supersedeDocU, CreatedBy: "stig"})
	// ruleDocV1 mints rules 1, 2, 3; supersedeDocU mints 4, 5.
	// B = 1, A1 = 2, A2 = 3, D = 4, C = 5.
	task := createTask(t, s, time.Now(), TaskInput{ProjectID: "p1", Title: "a task", Body: "b", Priority: "medium", Kind: "bug", CreatedBy: "stig"})
	govern(t, s, task.ID, 1)
	govern(t, s, task.ID, 5)

	mustSupersede(t, s, entry("P1-RULE-1", "P1-RULE-2", "P1-RULE-3")) // B -> A1, A2
	mustSupersede(t, s, entry("P1-RULE-2", "P1-RULE-4"))              // A1 -> D

	list, err := s.GovernedBy(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("GovernedBy = %+v, want 2 entries", list)
	}
	b, c := list[0], list[1]
	if b.Rule != "P1-RULE-1" || b.Status != "withdrawn" || !equalStrings(b.ResolvesTo, []string{"P1-RULE-3", "P1-RULE-4"}) {
		t.Errorf("B = %+v, want withdrawn with ResolvesTo [P1-RULE-3 P1-RULE-4]", b)
	}
	if c.Rule != "P1-RULE-5" || len(c.ResolvesTo) != 0 {
		t.Errorf("C = %+v, want live with empty ResolvesTo", c)
	}
}

// TestGovernedByResolvesToCycleTerminates: a cycle in the supersedes edges
// (never written by SupersedeRules, which refuses one; inserted by hand
// here to exercise the CTE's UNION dedup) does not hang GovernedBy.
func TestGovernedByResolvesToCycleTerminates(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	task := createTask(t, s, time.Now(), TaskInput{ProjectID: "p1", Title: "a task", Body: "b", Priority: "medium", Kind: "bug", CreatedBy: "stig"})
	govern(t, s, task.ID, 1)

	id1, id2 := ruleID(t, s, "P1", 1), ruleID(t, s, "P1", 2)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `UPDATE rules SET status = 'withdrawn' WHERE id IN ($1, $2)`, id1, id2); err != nil {
		t.Fatal(err)
	}
	for _, e := range [][2]int64{{id1, id2}, {id2, id1}} {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO rule_edges (from_rule, to_rule, type, source) VALUES ($1, $2, 'supersedes', 'refactor')`,
			e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}

	list, err := s.GovernedBy(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Status != "withdrawn" || len(list[0].ResolvesTo) != 0 {
		t.Errorf("GovernedBy = %+v, want one withdrawn entry with no live successors", list)
	}
}

// TestGovernedByResolvesToOrdersAcrossProjects: a withdrawn rule's
// successors span two projects, P1-RULE-3 (number 3) and P2-RULE-1 (number 1).
// ResolvesTo orders by project key first, so P1-RULE-3 sorts before P2-RULE-1
// even though its rule number is larger (M7).
func TestGovernedByResolvesToOrdersAcrossProjects(t *testing.T) {
	s := openDocStore(t)
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO projects (id, name, key) VALUES ('p2','P2','P2')`); err != nil {
		t.Fatal(err)
	}
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	mustCreateDoc(t, s, DocInput{Project: "p2", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	task := createTask(t, s, time.Now(), TaskInput{ProjectID: "p1", Title: "a task", Body: "b", Priority: "medium", Kind: "bug", CreatedBy: "stig"})
	govern(t, s, task.ID, 1)

	mustSupersede(t, s, entry("P1-RULE-1", "P1-RULE-3", "P2-RULE-1"))

	list, err := s.GovernedBy(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !equalStrings(list[0].ResolvesTo, []string{"P1-RULE-3", "P2-RULE-1"}) {
		t.Errorf("GovernedBy = %+v, want ResolvesTo [P1-RULE-3 P2-RULE-1]", list)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestGovernGateLeavesExistingLinkUntouched: a gate write on a rule an
// architect already governed manually with a pin must not touch that link
// (12-spec-refactoring-design-tree.md S51 — the gate only adds links).
func TestGovernGateLeavesExistingLinkUntouched(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	now := s.Now()
	task := createTask(t, s, now, TaskInput{ProjectID: "p1", Title: "t", Kind: "feature", Priority: "medium", CreatedBy: "stig"})
	ctx := context.Background()
	id := ruleID(t, s, "P1", 3)
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return Govern(tx, task.ID, id, "manual", true) }); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return Govern(tx, task.ID, id, "gate", false) }); err != nil {
		t.Fatal(err)
	}
	g, err := s.GovernedBy(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 1 || g[0].Pinned != 1 || g[0].Source != "manual" {
		t.Errorf("gate write must leave existing link untouched: %+v", g)
	}
}

// TestRuleAtSection: a section ref resolves to the rule arranged at that
// anchor, and an unknown anchor is ErrNotFound (S51's unknown_target case).
func TestRuleAtSection(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	sh, ok := designdoc.ParseShorthand(fmt.Sprintf("P1-SPEC-%d", d.Number))
	if !ok {
		t.Fatalf("shorthand did not parse for doc number %d", d.Number)
	}
	var got int64
	err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		var err error
		got, err = RuleAtSection(tx, designdoc.SectionRef{Shorthand: sh, Anchor: "sec-1.1"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	arr := arrangementOf(t, s, d.ID)
	if arr[1].Anchor != "sec-1.1" {
		t.Fatalf("fixture drift: %+v", arr)
	}
	var want int64
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT id FROM rules WHERE number = $1`, arr[1].Number).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("RuleAtSection = %d, want rule %d at sec-1.1", got, want)
	}

	err = s.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := RuleAtSection(tx, designdoc.SectionRef{Shorthand: sh, Anchor: "sec-9"})
		return err
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown anchor: err = %v, want ErrNotFound", err)
	}

	err = s.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := RuleAtSection(tx, designdoc.SectionRef{Shorthand: designdoc.Shorthand{Key: "P1", Type: "SPEC", Number: 999}, Anchor: "sec-1"})
		return err
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown document: err = %v, want ErrNotFound", err)
	}

	err = s.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := RuleAtSection(tx, designdoc.SectionRef{Shorthand: designdoc.Shorthand{Key: "NOPE", Type: "SPEC", Number: 1}, Anchor: "sec-1"})
		return err
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown project: err = %v, want ErrNotFound", err)
	}
}

// TestHasPlanGovernance: only a link with source = 'plan' counts, which is
// what tells the gate whether a task already has plan governance (S51).
func TestHasPlanGovernance(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	now := s.Now()
	task := createTask(t, s, now, TaskInput{ProjectID: "p1", Title: "planless", Body: "b", Priority: "medium", Kind: "feature", CreatedBy: "stig"})
	var ruleID int64
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT rule_id FROM doc_rules WHERE doc_id = $1 AND position = 0`, d.ID).Scan(&ruleID); err != nil {
		t.Fatal(err)
	}
	check := func(want bool, after string) {
		t.Helper()
		var got bool
		if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
			var err error
			got, err = HasPlanGovernance(tx, task.ID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("HasPlanGovernance after %s = %v, want %v", after, got, want)
		}
	}
	check(false, "creation")
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error { return Govern(tx, task.ID, ruleID, "gate", false) }); err != nil {
		t.Fatal(err)
	}
	check(false, "a gate link")
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE task_governed_by SET source = 'plan' WHERE task_id = $1`, task.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	check(true, "a plan link")
}
