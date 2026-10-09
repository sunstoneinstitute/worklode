package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

const closureDoc = "---\nstatus: draft\n---\n# T\n\n## 1. A {#sec-1}\n\none two three\n\n## 2. B {#sec-2}\n\nfour five\n\n## 3. C {#sec-3}\n\nsix\n\n## 4. D {#sec-4}\n\nseven eight\n"

// TestRuleClosure: A needs B, B refines C, A references D. The closure of A
// is A, B, C in breadth-first order with the edge each was reached by; D is
// outside it. C needs A closes a cycle, and the walk still terminates with
// each rule once (WL-SPEC-77 §4c).
func TestRuleClosure(t *testing.T) {
	s := openDocStore(t)
	s.metrics = newStoreMetrics(prometheus.NewRegistry())
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: closureDoc, CreatedBy: "stig"})
	a, b, c, d := ruleID(t, s, "P1", 1), ruleID(t, s, "P1", 2), ruleID(t, s, "P1", 3), ruleID(t, s, "P1", 4)
	ctx := context.Background()
	for _, e := range []struct {
		from, to int64
		typ      string
	}{{a, b, "needs"}, {b, c, "refines"}, {a, d, "references"}, {c, a, "needs"}} {
		if err := s.Tx(ctx, func(tx *sql.Tx) error { return LinkRules(tx, e.from, e.to, e.typ) }); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RuleClosure(ctx, "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.RuleClosureMember{
		{Ref: "P1-REQ-1", Kind: "requirement", Heading: "A", Words: 3},
		{Ref: "P1-REQ-2", Kind: "requirement", Heading: "B", Words: 2, Edge: "needs", From: "P1-REQ-1"},
		{Ref: "P1-REQ-3", Kind: "requirement", Heading: "C", Words: 1, Edge: "refines", From: "P1-REQ-2"},
	}
	if got.Rule != "P1-REQ-1" || got.Words != 6 || len(got.Members) != len(want) {
		t.Fatalf("closure: %+v", got)
	}
	for i := range want {
		if got.Members[i] != want[i] {
			t.Errorf("member %d: got %+v, want %+v", i, got.Members[i], want[i])
		}
	}
	if n := testutil.CollectAndCount(s.metrics.ruleClosureSize); n != 1 {
		t.Errorf("closure size histogram: %d series, want 1", n)
	}
	if _, err := s.RuleClosure(ctx, "P1", 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown rule: got %v, want ErrNotFound", err)
	}
}
