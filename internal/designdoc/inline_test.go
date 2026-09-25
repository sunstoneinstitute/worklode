package designdoc

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// baseDoc is an accepted spec whose §2 carries a nested §2.1.
func baseDoc() *model.DocDetail {
	base := &model.DocDetail{}
	base.ID, base.Slug, base.Kind, base.Number, base.Status = 1, "004-base", "spec", 4, "accepted"
	base.Body = "# Spec 4 — Base\n\nIntro.\n\n## 1. One {#sec-1}\n\nOld text.\n\n## 2. Two {#sec-2}\n\nSection two text.\n\n### 2.1 Nested {#sec-2.1}\n\nNested text.\n"
	return base
}

func ruleFixtureFetch(rules map[string]*model.Rule) func(string) (*model.Rule, error) {
	return func(ref string) (*model.Rule, error) {
		r, ok := rules[ref]
		if !ok {
			return nil, fmt.Errorf("no rule %s", ref)
		}
		return r, nil
	}
}

// ruleAmendsFixture: WL-RULE-2 (the base's §2) is amended by WL-RULE-10
// (accepted), WL-RULE-11 (draft) and WL-RULE-12 (withdrawn). WL-RULE-10 is
// itself amended by WL-RULE-13, which amends WL-RULE-2 back: a cycle.
func ruleAmendsFixture() map[string]*model.Rule {
	amends := func(from, to string) model.RuleEdge { return model.RuleEdge{Type: "amends", From: from, To: to} }
	at := []model.RuleArrangement{{DocRef: "WL-SPEC-20", Anchor: "sec-3"}}
	return map[string]*model.Rule{
		"WL-RULE-2": {Ref: "WL-RULE-2", Status: "accepted", Heading: "Two", Body: "Section two text.\n",
			Edges: []model.RuleEdge{amends("WL-RULE-10", "WL-RULE-2"), amends("WL-RULE-11", "WL-RULE-2"), amends("WL-RULE-12", "WL-RULE-2"), amends("WL-RULE-13", "WL-RULE-2")}},
		"WL-RULE-10": {Ref: "WL-RULE-10", Status: "accepted", Heading: "Tighter two", Body: "Amending rule text.\n\n#### Deep {#sec-3.1}\n\nDeep text.\n", ArrangedIn: at,
			Edges: []model.RuleEdge{amends("WL-RULE-10", "WL-RULE-2"), amends("WL-RULE-13", "WL-RULE-10")}},
		"WL-RULE-11": {Ref: "WL-RULE-11", Status: "draft", Heading: "Proposal", Body: "Proposed rule text.\n"},
		"WL-RULE-12": {Ref: "WL-RULE-12", Status: "withdrawn", Heading: "Gone", Body: "Withdrawn rule text.\n"},
		"WL-RULE-13": {Ref: "WL-RULE-13", Status: "accepted", Heading: "Meta", Body: "Meta rule text.\n",
			Edges: []model.RuleEdge{amends("WL-RULE-13", "WL-RULE-10"), amends("WL-RULE-13", "WL-RULE-2")}},
	}
}

// TestConsolidateRuleAmends: a section's rule folds in its in-force amending
// rules with attribution, transitively; a draft amender is pending, a
// withdrawn one is left out, and a cycle stops.
func TestConsolidateRuleAmends(t *testing.T) {
	base := *baseDoc()
	base.Amendments = []model.DocAmendment{
		{Anchor: "sec-2", Rule: "WL-RULE-2", By: "WL-RULE-10"},
		{Anchor: "sec-2", Rule: "WL-RULE-2", By: "WL-RULE-11"},
		{Anchor: "sec-2", Rule: "WL-RULE-2", By: "WL-RULE-12"},
		{Anchor: "sec-2", Rule: "WL-RULE-2", By: "WL-RULE-13"},
	}
	in := NewInliner(ruleFixtureFetch(ruleAmendsFixture()))
	out, err := in.Consolidate(&base, "")
	if err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	for _, want := range []string{
		"**[amending WL-RULE-10 (WL-SPEC-20#sec-3)]:**<br>",
		"**Tighter two**",
		"Amending rule text.",
		"**Deep**", // the amending rule's own headings are flattened
		"**[amending WL-RULE-13]:**<br>",
		"Meta rule text.",
		"> Pending amendment by WL-RULE-11 (not yet effective)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, absent := range []string{"Withdrawn rule text.", "Proposed rule text."} {
		if strings.Contains(out, absent) {
			t.Errorf("%q folded in:\n%s", absent, out)
		}
	}
	if i, j := strings.Index(out, "Section two text."), strings.Index(out, "Amending rule text."); i < 0 || j < i {
		t.Errorf("amendment not beneath the section it amends:\n%s", out)
	}
	if n := strings.Count(out, "Nested text."); n != 1 {
		t.Errorf("nested section text appears %d times, want once:\n%s", n, out)
	}

	// Section mode: only §2's subtree, folds intact.
	out, err = NewInliner(ruleFixtureFetch(ruleAmendsFixture())).Consolidate(&base, "sec-2")
	if err != nil {
		t.Fatalf("consolidate sec-2: %v", err)
	}
	if strings.Contains(out, "Old text.") || strings.Contains(out, "Consolidated view") {
		t.Errorf("section mode leaked other sections or the banner:\n%s", out)
	}
	for _, want := range []string{"Section two text.", "Amending rule text.", "Nested text."} {
		if !strings.Contains(out, want) {
			t.Errorf("section mode missing %q:\n%s", want, out)
		}
	}

	// The same folds from the rule's side (lode show WL-RULE-2 --inline).
	// WL-RULE-13 is emitted once, nested under WL-RULE-10 where it is met
	// first, not again as a direct amender (body-once).
	rules := ruleAmendsFixture()
	blocks, pending, err := NewInliner(ruleFixtureFetch(rules)).RuleAmendments(rules["WL-RULE-2"])
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || len(pending) != 1 || !strings.Contains(blocks[0], "Meta rule text.") {
		t.Errorf("RuleAmendments = %q, pending %q", blocks, pending)
	}
}
