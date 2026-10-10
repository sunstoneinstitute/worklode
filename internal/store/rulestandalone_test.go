package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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
	if _, err := s.AcceptRule(ctx, "P1", 1, false, "ada"); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-owner accept: %v, want ErrForbidden", err)
	}
	r, err := s.AcceptRule(ctx, "P1", 1, false, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "accepted" || r.Version != 1 {
		t.Errorf("accepted rule = %s v%d", r.Status, r.Version)
	}
	if _, err := s.AcceptRule(ctx, "P1", 1, false, "stig"); !errors.Is(err, ErrBadTransition) {
		t.Errorf("second accept: %v, want ErrBadTransition", err)
	}
	if _, err := s.AcceptRule(ctx, "P1", 99, false, "stig"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing rule: %v, want ErrNotFound", err)
	}
	if got := testutil.ToFloat64(s.metrics.ruleOps.WithLabelValues("accept", "ok")); got != 1 {
		t.Errorf("accept ok = %v, want 1", got)
	}
}

// setRuleOwner makes owner the owner of rule key-number.
func setRuleOwner(t *testing.T, s *Store, key string, number int64, owner string) {
	t.Helper()
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		return SetRuleMeta(tx, ruleID(t, s, key, number), model.RuleMetaInput{Owner: &owner})
	}); err != nil {
		t.Fatal(err)
	}
}

// TestAcceptRuleBumpsArrangingSpecs: editing a rule arranged in two accepted
// specs, then accepting it, bumps both specs' versions; each now shows the
// new text and its prior version still renders the old (WL-SPEC-77 §19.4).
// A version nobody judged substantive and nothing refers to mints no review.
func TestAcceptRuleBumpsArrangingSpecs(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	a := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "a", Body: ruleDocV1, CreatedBy: "stig"})
	b := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "b", CreatedBy: "stig",
		Body: "---\nstatus: draft\n---\n# B\n\n## 1. Own {#sec-1}\n\nX.\n"})
	if _, err := arrangeRule(t, s, b.ID, model.ArrangeRuleInput{Rule: "P1-REQ-3"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{a.ID, b.ID} {
		if _, _, err := acceptDoc(t, s, id, "stig"); err != nil {
			t.Fatal(err)
		}
	}
	setRuleOwner(t, s, "P1", 3, "stig")
	before := map[int64]int{}
	for _, id := range []int64{a.ID, b.ID} {
		d, err := s.GetDoc(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		before[id] = d.Version
	}

	if err := editRule(t, s, "P1", 3, model.EditRuleInput{Heading: "Two", Body: "C changed."}, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptRule(ctx, "P1", 3, false, "stig"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{a.ID, b.ID} {
		d, err := s.GetDoc(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if d.Version != before[id]+1 || !strings.Contains(d.Body, "C changed.") {
			t.Errorf("doc %d: version %d (was %d), body:\n%s", id, d.Version, before[id], d.Body)
		}
		prior, err := s.GetDocVersion(ctx, id, before[id])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(prior.Body, "\nC.\n") || strings.Contains(prior.Body, "C changed.") {
			t.Errorf("doc %d v%d must render the old text:\n%s", id, before[id], prior.Body)
		}
	}
	var reviews int
	if err := s.db.QueryRow(`SELECT count(*) FROM tasks WHERE kind = 'review'`).Scan(&reviews); err != nil {
		t.Fatal(err)
	}
	if reviews != 0 {
		t.Errorf("a non-substantive version minted %d review tasks", reviews)
	}
}

// TestAcceptRuleSubstantiveGates: a version its author judges substantive
// mints one review task and marks the accepted plan covering the rule stale
// (WL-SPEC-77 §10, §19.4).
func TestAcceptRuleSubstantiveGates(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, spec.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	setRuleOwner(t, s, "P1", 1, "stig")
	if err := editRule(t, s, "P1", 1, model.EditRuleInput{Heading: "One", Body: "A narrowed."}, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptRule(ctx, "P1", 1, true, "stig"); err != nil {
		t.Fatal(err)
	}
	var reviews int
	if err := s.db.QueryRow(`SELECT count(*) FROM tasks WHERE kind = 'review' AND title LIKE 'Review P1-REQ-1 v2%'`).Scan(&reviews); err != nil {
		t.Fatal(err)
	}
	if reviews != 1 {
		t.Errorf("review tasks = %d, want 1", reviews)
	}
	if d, err := s.GetDoc(ctx, plan.ID); err != nil || d.Status != "stale" {
		t.Errorf("covering plan = %v %v, want stale", d.Status, err)
	}
}

// TestAcceptRevisionGatesRuleVersions: a spec revision that adds a code span
// to one rule mints one review task for that rule and marks the plan covering
// it stale; a rule left unchanged, and one reworded without tripping a
// check, mint nothing (WL-SPEC-77 §19.4).
func TestAcceptRevisionGatesRuleVersions(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, spec.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if err := reviseDoc(t, s, spec.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	v2 := strings.Replace(ruleDocV1, "\nA.\n", "\nA calls `Frob()`.\n", 1)
	v2 = strings.Replace(v2, "\nC.\n", "\nC reworded.\n", 1)
	if err := updateRevision(t, s, spec.ID, v2); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptRevision(t, s, spec.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	var titles []string
	rows, err := s.db.Query(`SELECT title FROM tasks WHERE kind = 'review' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatal(err)
		}
		titles = append(titles, title)
	}
	rows.Close()
	if len(titles) != 1 || !strings.HasPrefix(titles[0], "Review P1-REQ-1 v2") {
		t.Errorf("review tasks = %q, want one for P1-REQ-1 v2", titles)
	}
	if d, err := s.GetDoc(ctx, plan.ID); err != nil || d.Status != "stale" {
		t.Errorf("covering plan = %v %v, want stale", d.Status, err)
	}
}

// TestAcceptRuleUnderOpenRevision: a candidate revision opened before a rule
// version was accepted still holds the older text; landing it keeps the
// accepted version rather than writing the older text back as a new one.
func TestAcceptRuleUnderOpenRevision(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	a := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "a", Body: ruleDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, a.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if err := reviseDoc(t, s, a.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	setRuleOwner(t, s, "P1", 3, "stig")
	if err := editRule(t, s, "P1", 3, model.EditRuleInput{Heading: "Two", Body: "C changed."}, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptRule(ctx, "P1", 3, false, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptRevision(t, s, a.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetRule(ctx, "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != 2 || r.Status != "accepted" {
		t.Errorf("rule after landing = v%d %s, want accepted v2", r.Version, r.Status)
	}
	if body := docBody(t, s, a.ID); !strings.Contains(body, "C changed.") {
		t.Errorf("landed spec lost the accepted text:\n%s", body)
	}
}

// TestArrangeRuleShowsAcceptedVersion: a rule with a pending draft is
// arranged at its accepted version, and the spec marks the draft
// (WL-SPEC-77 §19.4).
func TestArrangeRuleShowsAcceptedVersion(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	a := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "a", Body: ruleDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, a.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if err := editRule(t, s, "P1", 3, model.EditRuleInput{Heading: "Two", Body: "C draft."}, "stig"); err != nil {
		t.Fatal(err)
	}
	b := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "b", CreatedBy: "stig",
		Body: "---\nstatus: draft\n---\n# B\n\n## 1. Own {#sec-1}\n\nX.\n"})
	if _, err := arrangeRule(t, s, b.ID, model.ArrangeRuleInput{Rule: "P1-REQ-3"}); err != nil {
		t.Fatal(err)
	}
	if body := docBody(t, s, b.ID); !strings.Contains(body, "\nC.\n") || strings.Contains(body, "C draft.") {
		t.Errorf("arranged spec must show the accepted version:\n%s", body)
	}
	secs, err := s.ListDocSections(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) != 2 || secs[1].Pending != 2 || secs[1].Rule != "P1-REQ-3" {
		t.Errorf("sections = %+v, want sec-2 pending v2 of P1-REQ-3", secs)
	}
}

// TestAcceptRuleOwnedByArrangingSpec: a rule minted from a spec section has
// no owner of its own, so the owner of the lowest-id spec arranging it
// accepts it (WL-SPEC-77 §19.2). The owner of a later arranging spec is
// refused, and an unarranged rule with no owner is refused naming the fix.
func TestAcceptRuleOwnedByArrangingSpec(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "a", Body: ruleDocV1, CreatedBy: "stig"})
	b := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "b", CreatedBy: "ada",
		Body: "---\nstatus: draft\n---\n# B\n\n## 1. Own {#sec-1}\n\nX.\n"})
	if _, err := arrangeRule(t, s, b.ID, model.ArrangeRuleInput{Rule: "P1-REQ-3"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptRule(ctx, "P1", 3, false, "ada"); !errors.Is(err, ErrForbidden) {
		t.Errorf("later spec's owner accept: %v, want ErrForbidden", err)
	}
	if _, err := s.AcceptRule(ctx, "P1", 3, false, "stig"); err != nil {
		t.Fatalf("first spec's owner accept: %v", err)
	}

	r, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: "Loose", Body: "B."}, "stig")
	if err != nil {
		t.Fatal(err)
	}
	num := r.Number
	setRuleOwner(t, s, "P1", num, "")
	_, err = s.AcceptRule(ctx, "P1", num, false, "stig")
	if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), "lode rule set owner") {
		t.Errorf("unarranged ownerless accept: %v, want ErrForbidden naming lode rule set owner", err)
	}
}
