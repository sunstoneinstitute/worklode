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

// setKind sets rule key-n to kind.
func setKind(t *testing.T, s *Store, key string, n int64, kind string) error {
	t.Helper()
	return s.Tx(context.Background(), func(tx *sql.Tx) error {
		return SetRuleMeta(tx, ruleID(t, s, key, n), model.RuleMetaInput{Kind: &kind})
	})
}

// openTermStore seeds projects p1, p2, p3 and g. g is the glossary project:
// its glossary spec defines Lease and Edge Agent. p1 and p2 each define
// Lease their own way, and P1's rule 2 needs P1's Lease. p3 defines nothing.
func openTermStore(t *testing.T) *Store {
	t.Helper()
	s := openDocStore(t)
	s.glossaryProject = "g"
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO projects (id, name, key) VALUES ('p2','P2','P2'), ('p3','P3','P3'), ('g','GL','GL')`); err != nil {
		t.Fatal(err)
	}
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", CreatedBy: "stig",
		Body: "---\nstatus: draft\n---\n# T\n\n## 1. Lease {#sec-1}\n\nP1 lease.\n\n## 2. Uses {#sec-2}\n\nUses a lease.\n\n## 3. Holder {#sec-3}\n\nP1 holder.\n"})
	mustCreateDoc(t, s, DocInput{Project: "p2", Kind: "spec", Slug: "t", CreatedBy: "stig",
		Body: "---\nstatus: draft\n---\n# T\n\n## 1. Lease {#sec-1}\n\nP2 lease.\n"})
	mustCreateDoc(t, s, DocInput{Project: "g", Kind: "spec", Slug: "glossary", CreatedBy: "stig",
		Body: "---\nstatus: draft\n---\n# Glossary\n\n## 1. Lease {#sec-1}\n\nInstance lease.\n\n## 2. Edge Agent {#sec-2}\n\nInstance edge agent.\n"})
	for _, d := range []struct {
		key string
		n   int64
	}{{"P1", 1}, {"P1", 3}, {"P2", 1}, {"GL", 1}, {"GL", 2}} {
		if err := setKind(t, s, d.key, d.n, "definition"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		return LinkRules(tx, ruleID(t, s, "P1", 2), ruleID(t, s, "P1", 1), "needs")
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestResolveTermProjectFirst: two projects define lease differently and
// each resolves its own; a project with no definition resolves the
// instance glossary's; an unknown slug is ErrNotFound (WL-SPEC-77 §4d).
func TestResolveTermProjectFirst(t *testing.T) {
	s := openTermStore(t)
	s.metrics = newStoreMetrics(prometheus.NewRegistry())
	ctx := context.Background()
	for _, tc := range []struct {
		project, slug, ref, body string
		instance                 bool
	}{
		{"p1", "lease", "P1-RULE-1", "P1 lease.", false},
		{"p2", "lease", "P2-RULE-1", "P2 lease.", false},
		{"p3", "lease", "GL-RULE-1", "Instance lease.", true},
		{"p1", "edge-agent", "GL-RULE-2", "Instance edge agent.", true},
	} {
		term, err := s.ResolveTerm(ctx, tc.project, tc.slug)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.project, tc.slug, err)
		}
		if term.Rule.Ref != tc.ref || term.Instance != tc.instance || strings.TrimSpace(term.Rule.Body) != tc.body {
			t.Errorf("%s %s = %s instance=%v body=%q, want %s instance=%v %q",
				tc.project, tc.slug, term.Rule.Ref, term.Instance, term.Rule.Body, tc.ref, tc.instance, tc.body)
		}
	}
	term, err := s.ResolveTerm(ctx, "p1", "lease")
	if err != nil {
		t.Fatal(err)
	}
	if len(term.NeededBy) != 1 || term.NeededBy[0].From != "P1-REQ-2" {
		t.Errorf("needed by = %+v, want P1-REQ-2", term.NeededBy)
	}
	if _, err := s.ResolveTerm(ctx, "p1", "nothing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown slug: %v, want ErrNotFound", err)
	}
	s.glossaryProject = ""
	if _, err := s.ResolveTerm(ctx, "p3", "lease"); !errors.Is(err, ErrNotFound) {
		t.Errorf("no glossary project: %v, want ErrNotFound", err)
	}
	for outcome, want := range map[string]float64{"project": 3, "instance": 2, "not_found": 2} {
		if got := testutil.ToFloat64(s.metrics.termResolves.WithLabelValues(outcome)); got != want {
			t.Errorf("worklode_term_resolutions_total{outcome=%q} = %v, want %v", outcome, got, want)
		}
	}
}

// TestListTerms lists a project's definitions with their slugs, by slug.
func TestListTerms(t *testing.T) {
	s := openTermStore(t)
	terms, err := s.ListTerms(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(terms) != 2 || terms[0].Slug != "holder" || terms[1].Slug != "lease" || terms[1].Rule.Ref != "P1-RULE-1" {
		t.Errorf("terms = %+v", terms)
	}
}

// TestTermSlugCollisionRefused: a second definition with a heading that
// slugs the same inside one project is refused, whether it becomes a
// definition or a definition's heading changes to collide.
func TestTermSlugCollisionRefused(t *testing.T) {
	s := openTermStore(t)
	if err := setKind(t, s, "P1", 2, "requirement"); err != nil {
		t.Fatal(err)
	}
	var docID int64
	if err := s.db.QueryRow(`SELECT id FROM docs WHERE project_id = 'p1' AND slug = 't'`).Scan(&docID); err != nil {
		t.Fatal(err)
	}
	_, err := updateDocBody(t, s, docID,
		"---\nstatus: draft\n---\n# T\n\n## 1. Lease {#sec-1}\n\nP1 lease.\n\n## 2. Uses {#sec-2}\n\nUses a lease.\n\n## 3. LEASE {#sec-3}\n\nP1 holder.\n")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("heading collides: %v, want ErrInvalidInput", err)
	}
	if _, err := updateDocBody(t, s, docID,
		"---\nstatus: draft\n---\n# T\n\n## 1. Lease {#sec-1}\n\nP1 lease.\n\n## 2. Lease! {#sec-2}\n\nUses a lease.\n\n## 3. Holder {#sec-3}\n\nP1 holder.\n"); err != nil {
		t.Fatal(err)
	}
	if err := setKind(t, s, "P1", 2, "definition"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("kind collides: %v, want ErrInvalidInput", err)
	}
}

// TestRuleConceptIRI: a definition takes a concept IRI from ns/concept.ttl;
// an unknown IRI and a non-definition are refused; "" clears it.
func TestRuleConceptIRI(t *testing.T) {
	s := openTermStore(t)
	ctx := context.Background()
	set := func(key string, n int64, iri string) error {
		return s.Tx(ctx, func(tx *sql.Tx) error {
			return SetRuleMeta(tx, ruleID(t, s, key, n), model.RuleMetaInput{Concept: &iri})
		})
	}
	const iri = "https://worklode.io/ns/concept/requirement"
	if err := set("P1", 1, iri); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.GetRule(ctx, "P1", 1); r.ConceptIRI != iri {
		t.Errorf("concept = %q, want %q", r.ConceptIRI, iri)
	}
	if err := set("P1", 1, "https://worklode.io/ns/concept/nope"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unknown IRI: %v, want ErrInvalidInput", err)
	}
	if err := set("P1", 2, iri); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("requirement: %v, want ErrInvalidInput", err)
	}
	if err := set("P1", 1, ""); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.GetRule(ctx, "P1", 1); r.ConceptIRI != "" {
		t.Errorf("cleared concept = %q", r.ConceptIRI)
	}
}
