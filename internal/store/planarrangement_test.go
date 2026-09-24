package store

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestArrangePlanFromCovers: a plan arranges the rules its covers entries
// reach (S16): a section-scoped entry reaches the rule at that anchor and
// the rules under it; a document-scoped entry reaches every rule; the
// arrangement is rewritten on each plan body write and by accept.
func TestArrangePlanFromCovers(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	// ruleDocV1 arranges sec-1 (rule 1), sec-1.1 (rule 2), sec-2 (rule 3).
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	got := arrangementOf(t, s, plan.ID)
	if len(got) != 2 || got[0].Number != 1 || got[0].Anchor != "sec-1" || got[0].Depth != 2 ||
		got[1].Number != 2 || got[1].Anchor != "sec-1.1" || got[1].Depth != 3 {
		t.Fatalf("plan covering sec-1 should arrange rules 1 and 2 with the spec's anchors and depths: %+v", got)
	}

	whole := strings.Replace(governedPlanBody, "P1-SPEC-1#sec-1", "P1-SPEC-1", 1)
	if _, err := updateDocBody(t, s, plan.ID, whole); err != nil {
		t.Fatal(err)
	}
	got = arrangementOf(t, s, plan.ID)
	if len(got) != 3 || got[2].Number != 3 {
		t.Fatalf("plan covering the whole spec should arrange all three rules: %+v", got)
	}

	// The spec gains a rule after the plan was written; accept re-arranges.
	if _, err := updateDocBody(t, s, spec.ID, ruleDocV1+"\n## 3. Three {#sec-3}\n\nD.\n"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := acceptDoc(t, s, spec.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	_, tasks, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil || len(tasks) == 0 {
		t.Fatalf("accept plan: tasks=%d err=%v", len(tasks), err)
	}
	got = arrangementOf(t, s, plan.ID)
	if len(got) != 4 || got[3].Number != 4 {
		t.Fatalf("accepting the plan should re-arrange it against the current spec: %+v", got)
	}
	gov, err := s.GovernedBy(t.Context(), tasks[0].ID)
	if err != nil || len(gov) != 4 {
		t.Fatalf("minted task should be governed by all four rules: %d %v", len(gov), err)
	}
}

// TestEditRuleWritesThroughTheSpec: a rule arranged in a spec and in a
// plan is edited through the spec (R2); the plan arrangement only references.
func TestEditRuleWritesThroughTheSpec(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, err := editRule(t, s, "P1", 1, model.EditRuleInput{Heading: "One", Body: "\nChanged.\n\n"}, "stig"); err != nil {
		t.Fatalf("edit of a rule arranged in a spec and a plan: %v", err)
	}
	d, err := s.GetDoc(t.Context(), spec.ID)
	if err != nil || !strings.Contains(d.Body, "Changed.") {
		t.Fatalf("spec body should carry the edit: %v %q", err, d.Body)
	}
}

// TestAcceptDocRulesSkipsPlans: a plan's doc_rules hold another
// document's rules (increment 3 R1). Accepting a plan whose arrangement
// holds a covered spec's draft rules must never flip those rules to
// accepted, regardless of which caller reaches acceptDocRules or in what
// order it runs relative to arrangePlan.
func TestAcceptDocRulesSkipsPlans(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := arrangePlan(tx, plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := acceptDocRules(tx, plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	for _, row := range arrangementOf(t, s, spec.ID) {
		if row.Status != "draft" {
			t.Errorf("spec rule %d status = %q after acceptDocRules ran on the covering plan, want draft", row.Number, row.Status)
		}
	}
}
