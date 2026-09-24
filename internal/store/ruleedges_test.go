package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func ruleID(t *testing.T, s *Store, key string, number int64) int64 {
	t.Helper()
	var id int64
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		var err error
		id, err = RuleIDByRef(tx, key, number)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestLinkRules: a manual edge appears on both rules' details, a repeat
// is ErrEdgeExists, a self edge and an unknown type are ErrInvalidInput,
// unlinking an absent edge is ErrNotFound (S12).
func TestLinkRules(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	a, b := ruleID(t, s, "P1", 1), ruleID(t, s, "P1", 3)
	ctx := context.Background()
	link := func(from, to int64, typ string) error {
		return s.Tx(ctx, func(tx *sql.Tx) error { return LinkRules(tx, from, to, typ) })
	}
	if err := link(a, b, "refines"); err != nil {
		t.Fatal(err)
	}
	if err := link(a, b, "refines"); !errors.Is(err, ErrEdgeExists) {
		t.Errorf("repeat link: got %v, want ErrEdgeExists", err)
	}
	if err := link(a, a, "refines"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("self link: got %v, want ErrInvalidInput", err)
	}
	if err := link(a, b, "amends"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unknown type: got %v, want ErrInvalidInput", err)
	}
	from, err := s.GetRule(ctx, "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	to, err := s.GetRule(ctx, "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(from.Edges) != 1 || from.Edges[0].Type != "refines" || from.Edges[0].To != "P1-RULE-3" || from.Edges[0].Source != "manual" || from.Edges[0].ToHeading != "Two" {
		t.Errorf("from side: %+v", from.Edges)
	}
	if len(to.Edges) != 1 || to.Edges[0].From != "P1-RULE-1" || to.Edges[0].FromHeading != "One" {
		t.Errorf("to side: %+v", to.Edges)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return UnlinkRules(tx, a, b, "refines") }); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return UnlinkRules(tx, a, b, "refines") }); !errors.Is(err, ErrNotFound) {
		t.Errorf("unlink absent: got %v, want ErrNotFound", err)
	}
}

// TestLinkRulesLineage: LinkRules accepts wasDerivedFrom as an ordinary
// manual edge, listed on the rule detail like any other, and refuses
// supersededBy, naming lode rule supersede as its one writer (S22, R4).
func TestLinkRulesLineage(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	a, b := ruleID(t, s, "P1", 1), ruleID(t, s, "P1", 3)
	ctx := context.Background()
	link := func(from, to int64, typ string) error {
		return s.Tx(ctx, func(tx *sql.Tx) error { return LinkRules(tx, from, to, typ) })
	}
	if err := link(b, a, "wasDerivedFrom"); err != nil {
		t.Fatal(err)
	}
	from, err := s.GetRule(ctx, "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(from.Edges) != 1 || from.Edges[0].Type != "wasDerivedFrom" || from.Edges[0].To != "P1-RULE-1" || from.Edges[0].Source != "manual" {
		t.Errorf("wasDerivedFrom edge: %+v", from.Edges)
	}
	err = link(a, b, "supersededBy")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("supersededBy via link: got %v, want ErrInvalidInput", err)
	}
	if err == nil || !strings.Contains(err.Error(), "lode rule supersede") {
		t.Errorf("supersededBy via link: error %v does not name lode rule supersede", err)
	}
}

// TestUnlinkRulesRefactor: UnlinkRules refuses to remove a refactor edge,
// beside the existing refusal for a derived one (S22, R4).
func TestUnlinkRulesRefactor(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	a, b := ruleID(t, s, "P1", 1), ruleID(t, s, "P1", 3)
	ctx := context.Background()
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO rule_edges (from_rule, to_rule, type, source) VALUES ($1, $2, 'supersededBy', 'refactor')`, a, b)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return UnlinkRules(tx, a, b, "supersededBy") }); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unlink refactor: got %v, want ErrInvalidInput", err)
	}
}

// TestUnlinkRulesDerived: UnlinkRules refuses a derived edge with
// ErrInvalidInput rather than reporting ErrNotFound or deleting it.
func TestUnlinkRulesDerived(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	a, b := ruleID(t, s, "P1", 1), ruleID(t, s, "P1", 3)
	ctx := context.Background()
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO rule_edges (from_rule, to_rule, type, source) VALUES ($1, $2, 'references', 'derived')`, a, b)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return UnlinkRules(tx, a, b, "references") }); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unlink derived: got %v, want ErrInvalidInput", err)
	}
}

// TestDerivedReferences: a rule naming another rule by ref or by
// document section gets a derived references edge; the set is replaced on
// the next version; a manual references edge survives the rewrite; refs to
// itself, to a whole document, or to nothing yield no edge (S26).
func TestDerivedReferences(t *testing.T) {
	s := openDocStore(t)
	// P1-RULE-1..3 from ruleDocV1 (sec-1, sec-1.1, sec-2), doc P1-SPEC-1.
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	body := "---\nstatus: draft\n---\n# U\n\n## 1. Uno {#sec-1}\n\nSee P1-RULE-1, P1-SPEC-1#sec-2, P1-SPEC-1 (whole), P1-RULE-999, P1-RULE-4 (itself), P1-SPEC-99#sec-1 (no such doc), and P1-SPEC-1#sec-9 (no such rule at that anchor).\n"
	u := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "u", Body: body, CreatedBy: "stig"})
	ctx := context.Background()
	got, err := s.GetRule(ctx, "P1", 4)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"P1-RULE-1": true, "P1-RULE-3": true}
	if len(got.Edges) != 2 {
		t.Fatalf("edges: %+v", got.Edges)
	}
	for _, e := range got.Edges {
		if e.Type != "references" || e.Source != "derived" || e.From != "P1-RULE-4" || !want[e.To] {
			t.Errorf("unexpected edge %+v", e)
		}
	}

	// A manual references edge to P1-RULE-2, then a rewrite that drops P1-RULE-1
	// and cites P1-RULE-2 itself: the derived write now collides with the
	// manual row on (from, to, type), so ON CONFLICT DO NOTHING must fire and
	// leave the manual row's source alone rather than erroring the write.
	self, two := ruleID(t, s, "P1", 4), ruleID(t, s, "P1", 2)
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return LinkRules(tx, self, two, "references") }); err != nil {
		t.Fatal(err)
	}
	if _, err := updateDocBody(t, s, u.ID, "---\nstatus: draft\n---\n# U\n\n## 1. Uno {#sec-1}\n\nSee P1-SPEC-1#sec-2 and P1-RULE-2.\n"); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetRule(ctx, "P1", 4)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	for _, e := range got.Edges {
		sources[e.To] = e.Source
	}
	if len(sources) != 2 || sources["P1-RULE-3"] != "derived" || sources["P1-RULE-2"] != "manual" {
		t.Errorf("after rewrite: %+v", got.Edges)
	}
}

// TestDerivedReferencesFromOldRefSpelling: rule text citing the pre-S64
// spelling P1-CL-<n>, which accepted document bodies still carry, derives
// the same references edge as P1-RULE-<n> (S64).
func TestDerivedReferencesFromOldRefSpelling(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"}) // P1-RULE-1..3
	body := "---\nstatus: draft\n---\n# U\n\n## 1. Uno {#sec-1}\n\nSee P1-CL-1.\n"
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "u", Body: body, CreatedBy: "stig"})
	got, err := s.GetRule(context.Background(), "P1", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Edges) != 1 || got.Edges[0].Type != "references" || got.Edges[0].Source != "derived" || got.Edges[0].To != "P1-RULE-1" {
		t.Errorf("edges: %+v, want one derived references edge to P1-RULE-1", got.Edges)
	}
}

// TestDerivedReferencesSameDocument: a section citing another section of
// its own document derives the edge too. deriveReferences must run only
// after every doc_rules row for the write is in place, or the anchor it
// resolves through is empty (forward ref) or not yet rewritten (backward
// ref) and the edge is silently dropped (S26).
func TestDerivedReferencesSameDocument(t *testing.T) {
	s := openDocStore(t)
	// sec-1 cites sec-2 (forward) and sec-2 cites sec-1 (backward), both
	// within doc P1-SPEC-1 itself.
	body := "---\nstatus: draft\n---\n# T\n\n## 1. One {#sec-1}\n\nSee P1-SPEC-1#sec-2.\n\n## 2. Two {#sec-2}\n\nSee P1-SPEC-1#sec-1.\n"
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: body, CreatedBy: "stig"})
	ctx := context.Background()
	// Each rule sits at both ends of the pair: its own outgoing reference,
	// and the other rule's incoming one (ruleEdgesSQL matches either
	// side), so each side lists both edges.
	one, err := s.GetRule(ctx, "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	hasEdge := func(edges []model.RuleEdge, from, to string) bool {
		for _, e := range edges {
			if e.Type == "references" && e.Source == "derived" && e.From == from && e.To == to {
				return true
			}
		}
		return false
	}
	if len(one.Edges) != 2 || !hasEdge(one.Edges, "P1-RULE-1", "P1-RULE-2") {
		t.Errorf("sec-1 edges: %+v", one.Edges)
	}
	two, err := s.GetRule(ctx, "P1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(two.Edges) != 2 || !hasEdge(two.Edges, "P1-RULE-2", "P1-RULE-1") {
		t.Errorf("sec-2 edges: %+v", two.Edges)
	}
}

// TestDerivedReferencesSkipPlanArrangement: a section ref naming a plan's
// anchor derives no edge. The plan's doc_rules rows borrow the spec's
// rules, and resolving through them would point the reference at a rule
// the text never named (final review M5, increment 3).
func TestDerivedReferencesSkipPlanArrangement(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	// P1-PLAN-1 arranges P1-RULE-1 at sec-1, borrowed from the spec.
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	body := "---\nstatus: draft\n---\n# U\n\n## 1. Uno {#sec-1}\n\nSee P1-PLAN-1#sec-1.\n"
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "u", Body: body, CreatedBy: "stig"})
	got, err := s.GetRule(context.Background(), "P1", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Edges) != 0 {
		t.Errorf("edges through a plan's borrowed arrangement: %+v", got.Edges)
	}
}
