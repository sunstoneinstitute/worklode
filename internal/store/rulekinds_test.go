package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// setRuleKind sets rule number n of project P1 to kind.
func setRuleKind(t *testing.T, s *Store, n int64, kind string) {
	t.Helper()
	id := ruleID(t, s, "P1", n)
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		return SetRuleMeta(tx, id, model.RuleMetaInput{Kind: &kind})
	}); err != nil {
		t.Fatalf("set rule %d kind %s: %v", n, kind, err)
	}
}

// TestRuleKindSetsRefInfix: a rule is minted a requirement and prints as
// WL-REQ-<n>; set to another kind it prints as WL-RULE-<n>, and an unknown
// kind is refused (WL-SPEC-77 §4).
func TestRuleKindSetsRefInfix(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	c, err := s.GetRule(ctx, "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != "requirement" || c.Ref != "P1-REQ-1" {
		t.Errorf("minted rule = %s %s, want requirement P1-REQ-1", c.Kind, c.Ref)
	}
	setRuleKind(t, s, 1, "invariant")
	if c, _ = s.GetRule(ctx, "P1", 1); c.Kind != "invariant" || c.Ref != "P1-RULE-1" {
		t.Errorf("after set = %s %s, want invariant P1-RULE-1", c.Kind, c.Ref)
	}
	list, err := s.ListRules(ctx, RuleFilter{Project: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Ref != "P1-RULE-1" || list[1].Ref != "P1-REQ-2" {
		t.Errorf("list refs = %s %s, want P1-RULE-1 P1-REQ-2", list[0].Ref, list[1].Ref)
	}
	bad := "obligation"
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return SetRuleMeta(tx, ruleID(t, s, "P1", 1), model.RuleMetaInput{Kind: &bad})
	}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unknown kind: %v, want ErrInvalidInput", err)
	}
}

// TestCoversStoresOnlyRequirements: a section or whole-document covers entry
// resolves to the requirements in its scope and skips invariants and
// informative rules; a section holding none stores no edge; a direct ref to
// an invariant is refused, naming its kind (WL-SPEC-78 §4.1).
func TestCoversStoresOnlyRequirements(t *testing.T) {
	s := openDocStore(t)
	// ruleDocV1: sec-1 is rule 1, sec-1.1 rule 2, sec-2 rule 3.
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	setRuleKind(t, s, 2, "invariant")
	setRuleKind(t, s, 3, "informative")

	section := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "section", CreatedBy: "stig",
		Body: coversPlanBody("P1-SPEC-1#sec-1")})
	if got := coveredRuleNumbers(t, s, section.ID); !slices.Equal(got, []int64{1}) {
		t.Errorf("mixed section covers %v, want [1]", got)
	}
	whole := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "whole", CreatedBy: "stig",
		Body: coversPlanBody("P1-SPEC-1")})
	if got := coveredRuleNumbers(t, s, whole.ID); !slices.Equal(got, []int64{1}) {
		t.Errorf("whole document covers %v, want [1]", got)
	}
	info := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "info", CreatedBy: "stig",
		Body: coversPlanBody("P1-SPEC-1#sec-2")})
	if got, ext := coveredRuleNumbers(t, s, info.ID), externalCovers(t, s, info.ID); len(got) != 0 || len(ext) != 0 {
		t.Errorf("informative section covers %v, external %v; want neither", got, ext)
	}
	_, err := createDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "direct", CreatedBy: "stig",
		Body: coversPlanBody("P1-REQ-2")})
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "invariant") {
		t.Errorf("direct ref to an invariant: %v, want ErrInvalidInput naming invariant", err)
	}
}

// TestNeedsPlanningReportsOnlyRequirements: a section whose rule is not a
// requirement is never a planning gap (WL-SPEC-78 §1.3).
func TestNeedsPlanningReportsOnlyRequirements(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x") // sec-1, sec-2, sec-2.1: rules 1 to 3
	setRuleKind(t, s, 2, "invariant")
	setRuleKind(t, s, 3, "informative")
	_, gaps := needsPlanningSlugs(t, s, "p1")
	if len(gaps) != 1 || gaps[0].Sections != 1 || !slices.Equal(gapAnchors(gaps[0]), []string{"sec-1(unplanned)"}) {
		t.Errorf("gaps = %+v, want only sec-1 of one requirement", gaps)
	}
}

// TestAcceptedInvariantGovernsEveryTask: a task's governing rules are its
// links plus its project's accepted invariants, derived on read and never
// stored (WL-SPEC-77 §4). A draft invariant governs nothing.
func TestAcceptedInvariantGovernsEveryTask(t *testing.T) {
	s := openDocStore(t)
	mustAcceptedSpec(t, s, "025-x")                                                                               // accepted rules 1 to 3
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "draft", Body: ruleDocV1, CreatedBy: "stig"}) // draft rules 4 to 6
	setRuleKind(t, s, 2, "invariant")
	setRuleKind(t, s, 5, "invariant")
	task := createTask(t, s, time.Now(), TaskInput{ProjectID: "p1", Title: "a task", Body: "b", Priority: "medium", Kind: "bug", CreatedBy: "stig"})

	list, err := s.GovernedBy(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Rule != "P1-RULE-2" || list[0].Source != "invariant" {
		t.Errorf("GovernedBy = %+v, want the accepted invariant P1-RULE-2 only", list)
	}
	if got := governingNumbers(t, s, task.ID); len(got) != 0 {
		t.Errorf("stored links = %v, want none: invariant governance is derived", got)
	}
}
