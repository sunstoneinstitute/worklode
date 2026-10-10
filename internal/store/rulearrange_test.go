package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// arrangeRule runs ArrangeRule through RecordDocEvent, the way the API does.
func arrangeRule(t *testing.T, s *Store, docID int64, in model.ArrangeRuleInput) (string, error) {
	t.Helper()
	var anchor string
	_, _, err := s.RecordDocEvent(t.Context(), "arrange", "cli",
		fmt.Sprintf("rule-arrange-%d", docEventSeq.Add(1)), "doc.rule_arranged", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			anchor, err = ArrangeRule(tx, s.Now(), docID, in, "stig", eventID)
			return err
		})
	return anchor, err
}

// unarrangeRule runs UnarrangeRule through RecordDocEvent.
func unarrangeRule(t *testing.T, s *Store, docID int64, rule string) error {
	t.Helper()
	_, _, err := s.RecordDocEvent(t.Context(), "unarrange", "cli",
		fmt.Sprintf("rule-unarrange-%d", docEventSeq.Add(1)), "doc.rule_unarranged", nil,
		func(tx *sql.Tx, eventID int64) error {
			return UnarrangeRule(tx, s.Now(), docID, rule, "stig", eventID)
		})
	return err
}

func docBody(t *testing.T, s *Store, id int64) string {
	t.Helper()
	d, err := s.GetDoc(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d.Body
}

func ruleCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM rules`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestArrangeRuleAcrossSpecs: one rule arranged in two specs shows the same
// text in both; unarranging it from one leaves the other intact, and
// unarranging it from both leaves a standalone rule (WL-SPEC-77 §19.3). A
// draft spec is written in place; an accepted one through its candidate
// revision, landing on accept.
func TestArrangeRuleAcrossSpecs(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	a := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "a", Body: ruleDocV1, CreatedBy: "stig"})
	b := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "b", CreatedBy: "stig",
		Body: "---\nstatus: draft\n---\n# B\n\n## 1. Own {#sec-1}\n\nX.\n"})
	if _, _, err := acceptDoc(t, s, a.ID, "stig"); err != nil {
		t.Fatal(err)
	}

	// Draft spec B, appended at the end: P1-REQ-3 lands at sec-2.
	anchor, err := arrangeRule(t, s, b.ID, model.ArrangeRuleInput{Rule: "P1-REQ-3"})
	if err != nil {
		t.Fatal(err)
	}
	if anchor != "sec-2" {
		t.Errorf("default anchor = %s, want sec-2", anchor)
	}
	if body := docBody(t, s, b.ID); !strings.Contains(body, "## 2. Two {#sec-2}\n\nC.\n") {
		t.Errorf("spec B does not render the shared rule:\n%s", body)
	}
	assertArrangement(t, arrangementOf(t, s, b.ID), []arranged{
		{0, 2, 4, 1, "sec-1", "draft"},
		{1, 2, 3, 1, "sec-2", "accepted"},
	})
	if n := ruleCount(t, s); n != 4 {
		t.Errorf("arranging minted a rule: %d rules, want 4", n)
	}
	r, err := s.GetRule(ctx, "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ArrangedIn) != 2 || r.Version != 1 {
		t.Errorf("shared rule = v%d arranged in %+v", r.Version, r.ArrangedIn)
	}
	if _, err := arrangeRule(t, s, b.ID, model.ArrangeRuleInput{Rule: "P1-REQ-3"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("arranging twice: %v, want ErrInvalidInput", err)
	}

	// Under a section of a draft: the first child number.
	if anchor, err = arrangeRule(t, s, b.ID, model.ArrangeRuleInput{Rule: "P1-REQ-2", Under: "sec-1"}); err != nil || anchor != "sec-1.1" {
		t.Fatalf("under: anchor %s, err %v", anchor, err)
	}
	if body := docBody(t, s, b.ID); !strings.Contains(body, "## 1. Own {#sec-1}\n\nX.\n### 1.1 Sub {#sec-1.1}\n\nB.\n\n## 2. Two") {
		t.Errorf("under: body\n%s", body)
	}

	// Accepted spec A, after P1-REQ-1 whose next sibling is sec-2: a letter
	// suffix, after the subtree, in the candidate only.
	if anchor, err = arrangeRule(t, s, a.ID, model.ArrangeRuleInput{Rule: "P1-REQ-4", After: "P1-REQ-1"}); err != nil || anchor != "sec-1a" {
		t.Fatalf("after on accepted: anchor %s, err %v", anchor, err)
	}
	if len(arrangementOf(t, s, a.ID)) != 3 {
		t.Errorf("accepted spec's arrangement moved before the revision landed")
	}
	rev, err := s.GetDocRevision(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rev.Body, "B.\n\n## 1a. Own {#sec-1a}\n\nX.\n## 2. Two") {
		t.Errorf("candidate body:\n%s", rev.Body)
	}
	if _, err := acceptRevision(t, s, a.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	assertArrangement(t, arrangementOf(t, s, a.ID), []arranged{
		{0, 2, 1, 1, "sec-1", "accepted"},
		{1, 3, 2, 1, "sec-1.1", "accepted"},
		{2, 2, 4, 1, "sec-1a", "accepted"},
		{3, 2, 3, 1, "sec-2", "accepted"},
	})
	if n := ruleCount(t, s); n != 4 {
		t.Errorf("landing the revision minted a rule: %d rules, want 4", n)
	}

	// Unarranging from draft B leaves A intact.
	if err := unarrangeRule(t, s, b.ID, "P1-REQ-3"); err != nil {
		t.Fatal(err)
	}
	if body := docBody(t, s, b.ID); strings.Contains(body, "Two") {
		t.Errorf("spec B still renders the unarranged rule:\n%s", body)
	}
	if body := docBody(t, s, a.ID); !strings.Contains(body, "## 2. Two {#sec-2}\n\nC.\n") {
		t.Errorf("spec A lost the rule:\n%s", body)
	}
	if err := unarrangeRule(t, s, b.ID, "P1-REQ-3"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unarranging twice: %v, want ErrNotFound", err)
	}

	// Unarranging from accepted A lands with the revision, past the anchor
	// freeze, and leaves a standalone accepted rule.
	if err := unarrangeRule(t, s, a.ID, "P1-REQ-3"); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptRevision(t, s, a.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if body := docBody(t, s, a.ID); strings.Contains(body, "sec-2") {
		t.Errorf("spec A still renders the unarranged rule:\n%s", body)
	}
	r, err = s.GetRule(ctx, "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ArrangedIn) != 0 || r.Status != "accepted" || r.Body != "\nC.\n" {
		t.Errorf("standalone rule = %s %q arranged in %+v", r.Status, r.Body, r.ArrangedIn)
	}
}

// TestUnarrangeRuleRefusesArrangedChildren: a rule with anchored sections
// under it is refused, since its children would fall under another rule.
func TestUnarrangeRuleRefusesArrangedChildren(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "a", Body: ruleDocV1, CreatedBy: "stig"})
	if err := unarrangeRule(t, s, d.ID, "P1-REQ-1"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unarrange with children: %v, want ErrInvalidInput", err)
	}
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "p", CreatedBy: "stig",
		Body: "---\nstatus: draft\n---\n# Plan\n"})
	if _, err := arrangeRule(t, s, plan.ID, model.ArrangeRuleInput{Rule: "P1-REQ-1"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("arrange in a plan: %v, want ErrInvalidInput", err)
	}
}
