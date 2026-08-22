package cmd

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

func fixtureInliner(docs map[int64]*model.DocDetail) *docInliner {
	return newDocInliner(func(id int64) (*model.DocDetail, error) {
		d, ok := docs[id]
		if !ok {
			return nil, fmt.Errorf("no doc %d", id)
		}
		return d, nil
	})
}

// TestConsolidateDoc pins the WL-84 rendering: effective claims fold in with
// attribution and flattened headings, transitively; drafts list as pending;
// nested sections are not duplicated.
func TestConsolidateDoc(t *testing.T) {
	docs := inlineFixture()
	out, err := fixtureInliner(docs).consolidateDoc(docs[1], "")
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
	out, err = fixtureInliner(docs).consolidateDoc(docs[1], "sec-2")
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
