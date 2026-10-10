package store

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

const lintDoc = "---\nstatus: draft\n---\n# T\n\n## 1. Closure {#sec-1}\n\na set of rules\n\n## 2. A {#sec-2}\n\nThe closure of this rule, see above.\n\n## 3. B {#sec-3}\n\nUses the Closure term\n\n## 4. C {#sec-4}\n\nSection 2 says enclosure\n"

// TestRuleLint: rule 1 defines "Closure". A uses the term without the
// definition in its closure and points above; B needs the definition (one
// context edge) and conflicts with C; C cites a section by number, and
// "enclosure" is not the term (WL-REQ-1367).
func TestRuleLint(t *testing.T) {
	s := openDocStore(t)
	s.metrics = newStoreMetrics(prometheus.NewRegistry())
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: lintDoc, CreatedBy: "stig"})
	def, a, b, c := ruleID(t, s, "P1", 1), ruleID(t, s, "P1", 2), ruleID(t, s, "P1", 3), ruleID(t, s, "P1", 4)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `UPDATE rules SET kind = 'definition' WHERE id = $1`, def); err != nil {
		t.Fatal(err)
	}
	for _, e := range []struct {
		from, to int64
		typ      string
	}{{b, def, "needs"}, {a, c, "references"}, {b, c, "conflictsWith"}} {
		if err := s.Tx(ctx, func(tx *sql.Tx) error { return LinkRules(tx, e.from, e.to, e.typ) }); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RuleLint(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	want := &model.RuleLint{
		Project:      "p1",
		Rules:        4,
		Specs:        []model.RuleLintSpec{{Spec: "P1-SPEC-1", Rules: 4}},
		ClosureRules: model.RuleLintSpread{Median: 1, P90: 2},
		ClosureWords: model.RuleLintSpread{Median: 4, P90: 8},
		Findings: []model.RuleLintFinding{
			{Rule: "P1-REQ-2", Check: "undefined-term", Detail: `"Closure" (P1-RULE-1) is not in the closure`},
			{Rule: "P1-REQ-2", Check: "positional-reference", Detail: "above"},
			{Rule: "P1-REQ-3", Check: "one-context-edge", Detail: "needs P1-RULE-1"},
			{Rule: "P1-REQ-3", Check: "conflict", Detail: "conflictsWith P1-REQ-4"},
			{Rule: "P1-REQ-4", Check: "positional-reference", Detail: "Section 2"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lint:\n got %+v\nwant %+v", got, want)
	}
	if n := testutil.ToFloat64(s.metrics.ruleLints.WithLabelValues("findings")); n != 1 {
		t.Errorf("worklode_rule_lint_total{outcome=findings} = %v, want 1", n)
	}
	empty, err := s.RuleLint(ctx, "nope")
	if err != nil || empty.Rules != 0 || len(empty.Findings) != 0 {
		t.Errorf("empty project: %+v, %v", empty, err)
	}
}
