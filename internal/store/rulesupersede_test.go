package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// A second spec, P1-SPEC-2, created after ruleDocV1: its sections become
// rules 4 (sec-1) and 5 (sec-2).
const supersedeDocU = "---\nstatus: draft\n---\n# U\n\n## 1. Uno {#sec-1}\n\nD.\n\n## 2. Dos {#sec-2}\n\nE.\n"

func entry(old string, news ...string) model.SupersedeEntry {
	return model.SupersedeEntry{Old: old, New: news}
}

func supersede(t *testing.T, s *Store, dry bool, entries ...model.SupersedeEntry) (model.SupersedeResult, error) {
	t.Helper()
	return s.SupersedeRules(t.Context(), "p1", "stig", model.SupersedeInput{Entries: entries, DryRun: dry})
}

func mustSupersede(t *testing.T, s *Store, entries ...model.SupersedeEntry) model.SupersedeResult {
	t.Helper()
	res, err := supersede(t, s, false, entries...)
	if err != nil {
		t.Fatalf("SupersedeRules: %v", err)
	}
	return res
}

func ruleStatus(t *testing.T, s *Store, number int64) string {
	t.Helper()
	var st string
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT c.status FROM rules c JOIN projects p ON p.id = c.project_id WHERE p.key = 'P1' AND c.number = $1`,
		number).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

// refactorEdges lists every supersededBy edge as "from->to" RULE numbers.
func refactorEdges(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT f.number, tc.number, e.source FROM rule_edges e
		   JOIN rules f ON f.id = e.from_rule JOIN rules tc ON tc.id = e.to_rule
		  WHERE e.type = 'supersededBy' ORDER BY f.number, tc.number`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var from, to int64
		var source string
		if err := rows.Scan(&from, &to, &source); err != nil {
			t.Fatal(err)
		}
		if source != "refactor" {
			t.Errorf("edge %d->%d source = %s, want refactor", from, to, source)
		}
		out = append(out, strconv.FormatInt(from, 10)+"->"+strconv.FormatInt(to, 10))
	}
	return out
}

func eventCount(t *testing.T, s *Store, typ string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM events WHERE type = $1`, typ).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestSupersedeMerge: WL-RULE-B -> WL-RULE-A withdraws B, writes one refactor
// supersededBy edge B->A and leaves A live (S22, R2).
func TestSupersedeMerge(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	res := mustSupersede(t, s, entry("P1-RULE-3", "P1-RULE-1"))
	want := model.SupersedeResult{Entries: []model.SupersedeResolved{{Old: "P1-RULE-3", New: []string{"P1-RULE-1"}}}, Withdrawn: 1, Edges: 1}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("result = %+v, want %+v", res, want)
	}
	if got := ruleStatus(t, s, 3); got != "withdrawn" {
		t.Errorf("old rule status = %s, want withdrawn", got)
	}
	if got := ruleStatus(t, s, 1); got != "draft" {
		t.Errorf("successor status = %s, want draft", got)
	}
	if got := refactorEdges(t, s); !reflect.DeepEqual(got, []string{"3->1"}) {
		t.Errorf("edges = %v", got)
	}
	if n := eventCount(t, s, "rule.superseded"); n != 1 {
		t.Errorf("rule.superseded events = %d, want 1", n)
	}
	var entries, resolved string
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT payload->>'entries', payload->>'resolved' FROM events WHERE type = 'rule.superseded'`).Scan(&entries, &resolved); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(entries, `"P1-RULE-3"`) || !strings.Contains(resolved, `"P1-RULE-1"`) {
		t.Errorf("payload entries = %s, resolved = %s", entries, resolved)
	}
}

// TestSupersedeManyToMany: one old rule to two successors writes two edges,
// two old rules to one successor writes two more.
func TestSupersedeManyToMany(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "u", Body: supersedeDocU, CreatedBy: "stig"})
	res := mustSupersede(t, s,
		entry("P1-RULE-1", "P1-RULE-4", "P1-RULE-5"),
		entry("P1-RULE-2", "P1-RULE-4"),
		entry("P1-RULE-3", "P1-RULE-4"))
	if res.Withdrawn != 3 || res.Edges != 4 {
		t.Errorf("result = %+v, want 3 withdrawn and 4 edges", res)
	}
	if got := refactorEdges(t, s); !reflect.DeepEqual(got, []string{"1->4", "1->5", "2->4", "3->4"}) {
		t.Errorf("edges = %v", got)
	}
}

// TestSupersedeWithdrawOnly: a line with no successors withdraws the rule
// and writes no edge (R2).
func TestSupersedeWithdrawOnly(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	res := mustSupersede(t, s, entry("P1-RULE-2"))
	if res.Withdrawn != 1 || res.Edges != 0 || len(res.Entries) != 1 || len(res.Entries[0].New) != 0 {
		t.Errorf("result = %+v", res)
	}
	if got := ruleStatus(t, s, 2); got != "withdrawn" {
		t.Errorf("status = %s, want withdrawn", got)
	}
	if got := refactorEdges(t, s); len(got) != 0 {
		t.Errorf("edges = %v, want none", got)
	}
}

// TestSupersedeSectionRefs: section refs resolve on both sides, and an old
// document with no rule rows is split first (ensureRules, R7) so its
// rule can be minted and then withdrawn.
func TestSupersedeSectionRefs(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	u := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "u", Body: supersedeDocU, CreatedBy: "stig"})
	if _, err := s.db.ExecContext(t.Context(), `DELETE FROM doc_rules WHERE doc_id = $1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `DELETE FROM rules WHERE number IN (4, 5)`); err != nil {
		t.Fatal(err)
	}
	res := mustSupersede(t, s, entry("P1-SPEC-2#sec-1", "P1-SPEC-1#sec-1.1"))
	arr := arrangementOf(t, s, u.ID)
	if len(arr) != 2 {
		t.Fatalf("old document not split: %+v", arr)
	}
	if arr[0].Anchor != "sec-1" || arr[0].Status != "withdrawn" || arr[1].Status == "withdrawn" {
		t.Errorf("arrangement = %+v, want sec-1 withdrawn and sec-2 live", arr)
	}
	want := []model.SupersedeResolved{{Old: "P1-RULE-" + strconv.FormatInt(arr[0].Number, 10), New: []string{"P1-RULE-2"}}}
	if !reflect.DeepEqual(res.Entries, want) {
		t.Errorf("entries = %+v, want %+v", res.Entries, want)
	}
}

// TestSupersedeMarksPlansStale: an accepted plan arranging the old rule
// goes stale (S23, through SetRuleStatus).
func TestSupersedeMarksPlansStale(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	res := mustSupersede(t, s, entry("P1-RULE-1", "P1-RULE-3"))
	if res.StalePlans != 1 {
		t.Errorf("stale plans = %d, want 1", res.StalePlans)
	}
	if d, err := s.GetDoc(t.Context(), plan.ID); err != nil || d.Status != "stale" {
		t.Errorf("plan status = %v %v, want stale", d.Status, err)
	}
}

// TestSupersedeTellsGovernedTasks: a task governed by the old rule keeps
// its link and gets one task.governance_superseded event naming the old
// rule and its successors (R5).
func TestSupersedeTellsGovernedTasks(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	_, tasks, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil || len(tasks) != 2 {
		t.Fatalf("accept: %d tasks, %v", len(tasks), err)
	}
	res := mustSupersede(t, s, entry("P1-RULE-1", "P1-RULE-3"))
	if res.Tasks != 2 {
		t.Errorf("tasks = %d, want 2", res.Tasks)
	}
	for _, task := range tasks {
		if got := governingNumbers(t, s, task.ID); !equalInt64s(got, []int64{1, 2}) {
			t.Errorf("%s governed by %v, want [1 2] unchanged", task.ID, got)
		}
		rows, err := s.db.QueryContext(t.Context(),
			`SELECT payload FROM events WHERE type = 'task.governance_superseded' AND payload->>'task' = $1`, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		var payloads []map[string]any
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var p map[string]any
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			payloads = append(payloads, p)
		}
		rows.Close()
		if len(payloads) != 1 {
			t.Fatalf("%s: %d events, want 1", task.ID, len(payloads))
		}
		p := payloads[0]
		if p["rule"] != "P1-RULE-1" || !reflect.DeepEqual(p["successors"], []any{"P1-RULE-3"}) {
			t.Errorf("%s payload = %v", task.ID, p)
		}
	}
}

// TestSupersedeDryRun: a dry run reports the resolved map and the counts a
// real run would produce, and writes nothing (R6).
func TestSupersedeDryRun(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	res, err := supersede(t, s, true, entry("P1-SPEC-1#sec-1", "P1-RULE-3"))
	if err != nil {
		t.Fatal(err)
	}
	want := model.SupersedeResult{
		Entries:   []model.SupersedeResolved{{Old: "P1-RULE-1", New: []string{"P1-RULE-3"}}},
		Withdrawn: 1, Edges: 1, Tasks: 2, StalePlans: 1, DryRun: true,
	}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("result = %+v, want %+v", res, want)
	}
	if got := ruleStatus(t, s, 1); got == "withdrawn" {
		t.Error("dry run withdrew the rule")
	}
	if got := refactorEdges(t, s); len(got) != 0 {
		t.Errorf("dry run wrote edges %v", got)
	}
	for _, typ := range []string{"rule.superseded", "task.governance_superseded", "doc.stale"} {
		if n := eventCount(t, s, typ); n != 0 {
			t.Errorf("dry run wrote %d %s events", n, typ)
		}
	}
	if d, _ := s.GetDoc(t.Context(), plan.ID); d.Status != "accepted" {
		t.Errorf("dry run moved the plan to %s", d.Status)
	}
}

// TestSupersedeRerunIsNoOp: applying the same map twice changes nothing the
// second time; only the second rule.superseded event is new (R6).
func TestSupersedeRerunIsNoOp(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	m := []model.SupersedeEntry{entry("P1-RULE-1", "P1-RULE-3"), entry("P1-RULE-2")}
	mustSupersede(t, s, m...)
	res := mustSupersede(t, s, m...)
	if res.Withdrawn != 0 || res.Edges != 0 || res.Tasks != 0 || res.StalePlans != 0 {
		t.Errorf("re-run result = %+v, want zero counts", res)
	}
	if got := refactorEdges(t, s); !reflect.DeepEqual(got, []string{"1->3"}) {
		t.Errorf("edges = %v", got)
	}
	// Two tasks, each governed by both old rules.
	if n := eventCount(t, s, "task.governance_superseded"); n != 4 {
		t.Errorf("task.governance_superseded events = %d, want 4", n)
	}
	if n := eventCount(t, s, "rule.superseded"); n != 2 {
		t.Errorf("rule.superseded events = %d, want 2", n)
	}
}

// TestSupersedeRefusals: every refusal names the offending ref and writes
// nothing (R6, R7).
func TestSupersedeRefusals(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	mustSupersede(t, s, entry("P1-RULE-3")) // RULE-3 is withdrawn from here on.
	cases := []struct {
		name    string
		entries []model.SupersedeEntry
		want    error
		names   string
	}{
		{"withdrawn successor", []model.SupersedeEntry{entry("P1-RULE-1", "P1-RULE-3")}, ErrInvalidInput, "P1-RULE-3"},
		{"successor also old", []model.SupersedeEntry{entry("P1-RULE-1", "P1-RULE-2"), entry("P1-RULE-2")}, ErrInvalidInput, "P1-RULE-2"},
		{"self successor", []model.SupersedeEntry{entry("P1-RULE-1", "P1-SPEC-1#sec-1")}, ErrInvalidInput, "P1-SPEC-1#sec-1"},
		{"old twice", []model.SupersedeEntry{entry("P1-RULE-1"), entry("P1-SPEC-1#sec-1", "P1-RULE-2")}, ErrInvalidInput, "P1-SPEC-1#sec-1"},
		{"unparseable", []model.SupersedeEntry{entry("nonsense", "P1-RULE-1")}, ErrInvalidInput, "nonsense"},
		{"bare document", []model.SupersedeEntry{entry("P1-SPEC-1", "P1-RULE-1")}, ErrInvalidInput, "P1-SPEC-1"},
		{"empty map", nil, ErrInvalidInput, "empty"},
		{"unknown rule", []model.SupersedeEntry{entry("P1-RULE-99", "P1-RULE-1")}, ErrNotFound, "P1-RULE-99"},
		{"unknown anchor", []model.SupersedeEntry{entry("P1-RULE-1", "P1-SPEC-1#sec-9")}, ErrNotFound, "P1-SPEC-1#sec-9"},
		{"unknown document", []model.SupersedeEntry{entry("P1-RULE-1", "P1-SPEC-7#sec-1")}, ErrNotFound, "P1-SPEC-7#sec-1"},
		{"plan anchor", []model.SupersedeEntry{entry("P1-PLAN-1#sec-1", "P1-RULE-2")}, ErrNotFound, "P1-PLAN-1#sec-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := supersede(t, s, false, tc.entries...)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("error %q does not name %s", err, tc.names)
			}
		})
	}
	if ruleStatus(t, s, 1) == "withdrawn" || ruleStatus(t, s, 2) == "withdrawn" {
		t.Error("a refused map withdrew a rule")
	}
	if n := eventCount(t, s, "rule.superseded"); n != 1 {
		t.Errorf("rule.superseded events = %d, want only the fixture's 1", n)
	}
}

// TestSupersedeMetrics: worklode_rule_supersede_total counts each outcome.
func TestSupersedeMetrics(t *testing.T) {
	s := openDocStore(t)
	s.metrics = newStoreMetrics(prometheus.NewRegistry())
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	mustSupersede(t, s, entry("P1-RULE-3"))
	_, _ = supersede(t, s, true, entry("P1-RULE-2"))
	_, _ = supersede(t, s, false, entry("bogus"))
	_, _ = supersede(t, s, false, entry("P1-RULE-99"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _ = s.SupersedeRules(ctx, "p1", "stig", model.SupersedeInput{Entries: []model.SupersedeEntry{entry("P1-RULE-2")}})
	for _, o := range []string{"applied", "dry_run", "invalid", "not_found", "error"} {
		if got := testutil.ToFloat64(s.metrics.ruleSupersedes.WithLabelValues(o)); got != 1 {
			t.Errorf("outcome %s = %v, want 1", o, got)
		}
	}
}

// TestSupersedeDoesNotWaitOnGovern: an open transaction that has governed a
// task by the old rule holds KEY SHARE on it through the FK. The refactor's
// rule locks are NO KEY UPDATE, so it completes instead of waiting, which
// is the lock that closed the settlePlan/WithdrawDoc deadlock cycle.
func TestSupersedeDoesNotWaitOnGovern(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	task := createTask(t, s, s.Now(), TaskInput{ProjectID: "p1", Title: "Governed", Kind: "feature", Priority: "medium", CreatedBy: "stig"})
	old := ruleID(t, s, "P1", 1)
	tx, err := s.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := Govern(tx, task.ID, old, "manual", false); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.SupersedeRules(t.Context(), "p1", "stig", model.SupersedeInput{Entries: []model.SupersedeEntry{entry("P1-RULE-1", "P1-RULE-3")}})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		// Release the KEY SHARE so the blocked refactor finishes before the
		// test's database is dropped.
		tx.Rollback()
		<-done
		t.Fatal("supersede waited on a concurrent Govern's KEY SHARE lock")
	}
}
