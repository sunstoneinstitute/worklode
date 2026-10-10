package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// glossarySlug is the slug of a project's glossary spec (WL-SPEC-77 §4d).
const glossarySlug = "glossary"

// WithGlossaryProject names the project whose glossary spec is the
// instance glossary (WL-SPEC-77 §4d). Without it the instance has none.
// serverapp passes it from --glossary-project / LODE_GLOSSARY_PROJECT.
func WithGlossaryProject(projectID string) Option {
	return func(s *Store) { s.glossaryProject = projectID }
}

// liveDefinition is a definition that holds: draft or accepted. A
// withdrawn or superseded definition neither resolves nor collides.
func liveDefinition(r model.Rule) bool {
	return r.Kind == designdoc.RuleKindDefinition && (r.Status == "draft" || r.Status == "accepted")
}

// terms reads the live definitions among rules as terms, by slug.
func terms(rules []model.Rule) []model.Term {
	out := []model.Term{}
	for _, r := range rules {
		if liveDefinition(r) {
			out = append(out, model.Term{Slug: designdoc.TermSlug(r.Heading), Rule: r, NeededBy: []model.RuleEdge{}})
		}
	}
	slices.SortFunc(out, func(a, b model.Term) int { return strings.Compare(a.Slug, b.Slug) })
	return out
}

// ListTerms lists a project's own definitions as terms, by slug.
func (s *Store) ListTerms(ctx context.Context, projectID string) ([]model.Term, error) {
	rules, err := s.ListRules(ctx, RuleFilter{Project: projectID})
	if err != nil {
		return nil, err
	}
	return terms(rules), nil
}

// instanceTerms lists the definitions the instance glossary arranges: the
// glossary spec of the glossary project. None when no glossary project is
// configured or it has no glossary spec.
func (s *Store) instanceTerms(ctx context.Context) ([]model.Term, error) {
	if s.glossaryProject == "" {
		return nil, nil
	}
	var docID int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM docs WHERE project_id = $1 AND slug = $2 AND kind = 'spec' AND deleted_at IS NULL`,
		s.glossaryProject, glossarySlug).Scan(&docID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read glossary of %s: %w", s.glossaryProject, err)
	}
	rules, err := s.ListRules(ctx, RuleFilter{Doc: docID})
	if err != nil {
		return nil, err
	}
	out := terms(rules)
	for i := range out {
		out[i].Instance = true
	}
	return out, nil
}

// ResolveTerm resolves a term slug inside a project (WL-SPEC-77 §4d): the
// project's own definition first, then the instance glossary's. The term
// carries the definition's full detail and every needs edge into it.
// ErrNotFound when neither defines it.
func (s *Store) ResolveTerm(ctx context.Context, projectID, slug string) (*model.Term, error) {
	term, err := s.findTerm(ctx, projectID, slug)
	if err != nil {
		s.metrics.termResolve("error")
		return nil, err
	}
	if term == nil {
		s.metrics.termResolve("not_found")
		return nil, fmt.Errorf("term %q in project %s: %w", slug, projectID, ErrNotFound)
	}
	full, err := s.GetRule(ctx, term.Rule.ProjectKey, term.Rule.Number)
	if err != nil {
		s.metrics.termResolve("error")
		return nil, err
	}
	term.Rule = *full
	for _, e := range full.Edges {
		if e.Type == "needs" && e.To == full.Ref {
			term.NeededBy = append(term.NeededBy, e)
		}
	}
	if term.Instance {
		s.metrics.termResolve("instance")
	} else {
		s.metrics.termResolve("project")
	}
	return term, nil
}

// findTerm is ResolveTerm's lookup: nil, nil when nothing defines slug.
func (s *Store) findTerm(ctx context.Context, projectID, slug string) (*model.Term, error) {
	own, err := s.ListTerms(ctx, projectID)
	if err != nil {
		return nil, err
	}
	instance, err := s.instanceTerms(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range append(own, instance...) {
		if t.Slug == slug {
			return &t, nil
		}
	}
	return nil, nil
}

// checkTermSlug refuses a live definition whose term slug is empty or is
// already another live definition's in the same project (WL-SPEC-77 §4d).
// Any other rule passes. Called after a write that makes a rule a
// definition or changes a heading.
func checkTermSlug(tx *sql.Tx, ruleID int64) error {
	rows, err := tx.Query(
		`SELECT r.id, v.heading
		   FROM rules r JOIN rule_versions v ON v.rule_id = r.id AND v.version = r.version
		  WHERE r.project_id = (SELECT project_id FROM rules WHERE id = $1)
		    AND r.kind = $2 AND r.status IN ('draft', 'accepted')`, ruleID, designdoc.RuleKindDefinition)
	if err != nil {
		return fmt.Errorf("read definitions beside rule %d: %w", ruleID, err)
	}
	type def struct {
		id   int64
		slug string
	}
	defs, err := collectRows(rows, "definitions", func(r rowScanner) (def, error) {
		var d def
		var heading string
		err := r.Scan(&d.id, &heading)
		d.slug = designdoc.TermSlug(heading)
		return d, err
	})
	if err != nil {
		return err
	}
	i := slices.IndexFunc(defs, func(d def) bool { return d.id == ruleID })
	if i < 0 {
		return nil
	}
	slug := defs[i].slug
	if slug == "" {
		return fmt.Errorf("%s: a definition's heading needs a letter or digit to make its term slug: %w", ruleRefOf(tx, ruleID), ErrInvalidInput)
	}
	for _, d := range defs {
		if d.id != ruleID && d.slug == slug {
			return fmt.Errorf("%s: term %q is already defined by %s in this project: %w",
				ruleRefOf(tx, ruleID), slug, ruleRefOf(tx, d.id), ErrInvalidInput)
		}
	}
	return nil
}
