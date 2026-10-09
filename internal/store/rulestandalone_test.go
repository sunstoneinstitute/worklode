package store

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestAddRuleStandalone: a rule added without a document is a full rule
// (WL-SPEC-77 §19.2). It reads back by ref with no arrangement, derives
// references edges from its text, a plan covers it, and the plan's minted
// tasks are governed by it.
func TestAddRuleStandalone(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})

	r, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: "Standalone", Body: "Follows P1-SPEC-1#sec-2.", Tags: []string{"x"}}, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if r.Ref != "P1-REQ-4" || r.Status != "draft" || r.Version != 1 || r.Owner != "stig" || r.Kind != "requirement" ||
		len(r.ArrangedIn) != 0 || len(r.Tags) != 1 || r.Tags[0] != "x" {
		t.Errorf("added rule = %+v", r)
	}
	got, err := s.GetRule(ctx, "P1", 4)
	if err != nil {
		t.Fatal(err)
	}
	if got.Heading != "Standalone" || got.Body != "\nFollows P1-SPEC-1#sec-2.\n" {
		t.Errorf("read back = %q %q", got.Heading, got.Body)
	}
	if len(got.Edges) != 1 || got.Edges[0].Type != "references" || got.Edges[0].To != "P1-REQ-3" {
		t.Errorf("edges = %+v, want one references edge to P1-REQ-3", got.Edges)
	}

	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "plan", CreatedBy: "stig",
		Body: "---\nstatus: draft\ncovers: [P1-REQ-4]\n---\n# Plan\n\n## Tasks\n\n### Task 1 — Build it\n\n```yaml\nkind: feature\n```\n\nDo it.\n"})
	_, minted, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if len(minted) != 1 {
		t.Fatalf("minted %d tasks, want 1", len(minted))
	}
	if g := governingNumbers(t, s, minted[0].ID); !equalInt64s(g, []int64{4}) {
		t.Errorf("minted task governed by %v, want [4]", g)
	}
	got, err = s.GetRule(ctx, "P1", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.CoveredBy) != 1 || got.CoveredBy[0].Doc != plan.ID {
		t.Errorf("covered_by = %+v", got.CoveredBy)
	}
}

func TestAddRuleRefusals(t *testing.T) {
	s := openDocStore(t)
	s.metrics = newStoreMetrics(prometheus.NewRegistry())
	ctx := context.Background()
	cases := []struct {
		in   model.AddRuleInput
		want error
	}{
		{model.AddRuleInput{Project: "p1", Body: "B."}, ErrInvalidInput},
		{model.AddRuleInput{Project: "p1", Heading: "H", Kind: "informative"}, ErrInvalidInput},
		{model.AddRuleInput{Project: "p1", Heading: "H", Kind: "bogus"}, ErrInvalidInput},
		{model.AddRuleInput{Project: "nope", Heading: "H"}, ErrNotFound},
	}
	for _, c := range cases {
		if _, err := s.AddRule(ctx, c.in, "stig"); !errors.Is(err, c.want) {
			t.Errorf("AddRule(%+v) = %v, want %v", c.in, err, c.want)
		}
	}
	r, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: "Term", Kind: "definition"}, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if r.Ref != "P1-RULE-1" || r.Kind != "definition" {
		t.Errorf("definition rule = %s %s", r.Ref, r.Kind)
	}
	if got := testutil.ToFloat64(s.metrics.ruleOps.WithLabelValues("add", "invalid")); got != 3 {
		t.Errorf("add invalid = %v, want 3", got)
	}
}

// TestAcceptRule: only the owner accepts, the newest draft version becomes
// accepted, and a rule with no draft version is refused.
func TestAcceptRule(t *testing.T) {
	s := openDocStore(t)
	s.metrics = newStoreMetrics(prometheus.NewRegistry())
	ctx := context.Background()
	if _, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: "H", Body: "B."}, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptRule(ctx, "P1", 1, "ada"); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-owner accept: %v, want ErrForbidden", err)
	}
	r, err := s.AcceptRule(ctx, "P1", 1, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "accepted" || r.Version != 1 {
		t.Errorf("accepted rule = %s v%d", r.Status, r.Version)
	}
	if _, err := s.AcceptRule(ctx, "P1", 1, "stig"); !errors.Is(err, ErrBadTransition) {
		t.Errorf("second accept: %v, want ErrBadTransition", err)
	}
	if _, err := s.AcceptRule(ctx, "P1", 99, "stig"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing rule: %v, want ErrNotFound", err)
	}
	if got := testutil.ToFloat64(s.metrics.ruleOps.WithLabelValues("accept", "ok")); got != 1 {
		t.Errorf("accept ok = %v, want 1", got)
	}
}
