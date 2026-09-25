package designdoc

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// inlineFixture is a little corpus: base (accepted) is amended on §2 by
// amender §1 (accepted) and by draft §1 (draft — pending), and superseded on
// §1 by amender §2. amender §1 is itself amended by meta §1 (accepted), which
// is what transitive expansion folds.
func inlineFixture() map[int64]*model.DocDetail {
	base := &model.DocDetail{}
	base.ID, base.Slug, base.Kind, base.Number, base.Status = 1, "004-base", "spec", 4, "accepted"
	base.Body = "---\nstatus: accepted\n---\n# Spec 4 — Base\n\nIntro.\n\n## 1. One {#sec-1}\n\nOld text.\n\n## 2. Two {#sec-2}\n\nSection two text.\n\n### 2.1 Nested {#sec-2.1}\n\nNested text.\n"
	base.EdgesIn = []model.DocEdge{
		{Type: "amendedBy", FromAnchor: "sec-2", ToDoc: 2, ToAnchor: "sec-1", ToKind: "spec", ToNumber: 9, ToSlug: "009-amender"},
		{Type: "amendedBy", FromAnchor: "sec-2", ToDoc: 3, ToAnchor: "sec-1", ToKind: "spec", ToNumber: 11, ToSlug: "011-draft"},
		{Type: "isReplacedBy", FromAnchor: "sec-1", ToDoc: 2, ToAnchor: "sec-2", ToKind: "spec", ToNumber: 9, ToSlug: "009-amender"},
	}

	amender := &model.DocDetail{}
	amender.ID, amender.Slug, amender.Kind, amender.Number, amender.Status = 2, "009-amender", "spec", 9, "accepted"
	amender.Body = "---\nstatus: accepted\n---\n# Spec 9 — Amender\n\n## 1. Amendment {#sec-1}\n\nAmending text.\n\n### 1.1 Detail {#sec-1.1}\n\nAmending detail.\n\n## 2. Replacement {#sec-2}\n\nReplacing text.\n"
	amender.EdgesIn = []model.DocEdge{
		{Type: "amendedBy", FromAnchor: "sec-1", ToDoc: 4, ToAnchor: "sec-1", ToKind: "spec", ToNumber: 13, ToSlug: "013-meta"},
	}

	draft := &model.DocDetail{}
	draft.ID, draft.Slug, draft.Kind, draft.Number, draft.Status = 3, "011-draft", "spec", 11, "draft"
	draft.Body = "---\nstatus: draft\n---\n# Spec 11 — Draft\n\n## 1. Proposal {#sec-1}\n\nProposed text.\n"

	meta := &model.DocDetail{}
	meta.ID, meta.Slug, meta.Kind, meta.Number, meta.Status = 4, "013-meta", "spec", 13, "accepted"
	meta.Body = "---\nstatus: accepted\n---\n# Spec 13 — Meta\n\n## 1. Meta {#sec-1}\n\nMeta text.\n"

	return map[int64]*model.DocDetail{1: base, 2: amender, 3: draft, 4: meta}
}

func fixtureInliner(docs map[int64]*model.DocDetail) *Inliner {
	return NewInliner(func(id int64) (*model.DocDetail, error) {
		d, ok := docs[id]
		if !ok {
			return nil, fmt.Errorf("no doc %d", id)
		}
		return d, nil
	}, ruleFixtureFetch(nil))
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
	docs := inlineFixture()
	base := *docs[1]
	base.EdgesIn = nil
	base.Amendments = []model.DocAmendment{
		{Anchor: "sec-2", Rule: "WL-RULE-2", By: "WL-RULE-10"},
		{Anchor: "sec-2", Rule: "WL-RULE-2", By: "WL-RULE-11"},
		{Anchor: "sec-2", Rule: "WL-RULE-2", By: "WL-RULE-12"},
		{Anchor: "sec-2", Rule: "WL-RULE-2", By: "WL-RULE-13"},
	}
	in := NewInliner(func(id int64) (*model.DocDetail, error) { return nil, fmt.Errorf("no doc %d", id) },
		ruleFixtureFetch(ruleAmendsFixture()))
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

	// The same folds from the rule's side (lode show WL-RULE-2 --inline).
	// WL-RULE-13 is emitted once, nested under WL-RULE-10 where it is met
	// first, not again as a direct amender (body-once).
	rules := ruleAmendsFixture()
	blocks, pending, err := NewInliner(nil, ruleFixtureFetch(rules)).RuleAmendments(rules["WL-RULE-2"])
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || len(pending) != 1 || !strings.Contains(blocks[0], "Meta rule text.") {
		t.Errorf("RuleAmendments = %q, pending %q", blocks, pending)
	}
}

// TestConsolidateDoc pins the WL-84 rendering: effective claims fold in with
// attribution and flattened headings, transitively; drafts list as pending;
// nested sections are not duplicated.
func TestConsolidateDoc(t *testing.T) {
	docs := inlineFixture()
	out, err := fixtureInliner(docs).Consolidate(docs[1], "")
	if err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	for _, want := range []string{
		"Consolidated view of 004-base",
		"**[amending spec 9 §1]:**<br>",
		"Amending text.",
		"**1. Amendment**", // acting subtree's heading flattened to bold
		"**1.1 Detail**",   // nested acting heading flattened too
		"**[superseding spec 9 §2]:**<br>",
		"Replacing text.",
		"> Pending spec 11 §1 (not yet effective)",
		"**[amending spec 13 §1]:**<br>", // transitive: meta amends the amendment
		"Meta text.",
		"Old text.", // a superseded section keeps its own text (026 §3)
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "Nested text."); n != 1 {
		t.Errorf("nested section text appears %d times, want once:\n%s", n, out)
	}
	if strings.Contains(out, "Proposed text.") {
		t.Errorf("draft claim was folded in:\n%s", out)
	}

	// Section mode: only §2's subtree, folds intact.
	out, err = fixtureInliner(docs).Consolidate(docs[1], "sec-2")
	if err != nil {
		t.Fatalf("consolidate sec-2: %v", err)
	}
	if strings.Contains(out, "Old text.") || strings.Contains(out, "Consolidated view") {
		t.Errorf("section mode leaked other sections or the banner:\n%s", out)
	}
	for _, want := range []string{"Section two text.", "Amending text.", "Nested text."} {
		if !strings.Contains(out, want) {
			t.Errorf("section mode missing %q:\n%s", want, out)
		}
	}
}
