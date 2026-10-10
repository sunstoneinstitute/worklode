package store

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// positionalRefRE matches a reference by position, which a rule may not make
// (WL-SPEC-77 §4c test 2).
var positionalRefRE = regexp.MustCompile(`§|(?i)\bsection\s+\d+|\babove\b|\bbelow\b`)

// lintRule is one rule as the lint reads it.
type lintRule struct {
	id, number    int64
	project, key  string
	ref, kind     string
	heading, body string
	live          bool
	words         int
}

// RuleLint measures a project's rules against the targets of WL-SPEC-77
// §4c: rules per spec, closure spread, and one finding per rule with one
// context edge, an undefined term, an open conflictsWith edge or a
// positional reference. A term is the heading of a live definition rule in
// the project; a rule whose body uses it as a whole word, case-insensitive,
// without that definition in its closure has an undefined term. An unknown
// project reads as an empty report.
func (s *Store) RuleLint(ctx context.Context, projectID string) (*model.RuleLint, error) {
	out, err := s.ruleLint(ctx, projectID)
	switch {
	case err != nil:
		s.metrics.ruleLint("error")
	case len(out.Findings) > 0:
		s.metrics.ruleLint("findings")
	default:
		s.metrics.ruleLint("clean")
	}
	return out, err
}

func (s *Store) ruleLint(ctx context.Context, projectID string) (*model.RuleLint, error) {
	out := &model.RuleLint{Project: projectID, Specs: []model.RuleLintSpec{}, Findings: []model.RuleLintFinding{}}
	rows, err := s.db.QueryContext(ctx,
		`SELECT dp.key || '-SPEC-' || d.number, count(dr.rule_id)
		   FROM docs d
		   JOIN projects dp ON dp.id = d.project_id
		   LEFT JOIN doc_rules dr ON dr.doc_id = d.id AND dr.rule_id IS NOT NULL
		  WHERE d.project_id = $1 AND d.kind = 'spec' AND d.deleted_at IS NULL AND d.status <> 'superseded'
		  GROUP BY dp.key, d.number ORDER BY d.number`, projectID)
	if err != nil {
		return nil, fmt.Errorf("count rules per spec: %w", err)
	}
	if out.Specs, err = collectRows(rows, "count rules per spec", func(r rowScanner) (model.RuleLintSpec, error) {
		var sp model.RuleLintSpec
		return sp, r.Scan(&sp.Spec, &sp.Rules)
	}); err != nil {
		return nil, err
	}

	// ponytail: reads every rule and edge in the instance, since closures
	// cross projects; scope to the reachable set if the corpus outgrows memory.
	rows, err = s.db.QueryContext(ctx,
		`SELECT r.id, r.number, r.project_id, p.key, `+ruleRefSQL("p", "r")+`, r.kind, v.heading, v.body,
		        r.status IN ('draft', 'accepted')
		   FROM rules r
		   JOIN projects p ON p.id = r.project_id
		   JOIN rule_versions v ON v.rule_id = r.id AND v.version = r.version`)
	if err != nil {
		return nil, fmt.Errorf("read rules for lint: %w", err)
	}
	all, err := collectRows(rows, "read rules for lint", func(r rowScanner) (*lintRule, error) {
		var lr lintRule
		err := r.Scan(&lr.id, &lr.number, &lr.project, &lr.key, &lr.ref, &lr.kind, &lr.heading, &lr.body, &lr.live)
		lr.words = len(strings.Fields(lr.body))
		return &lr, err
	})
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*lintRule, len(all))
	exists := make(map[designdoc.RuleRef]bool, len(all))
	for _, r := range all {
		byID[r.id] = r
		exists[designdoc.RuleRef{Key: r.key, Number: r.number}] = true
	}

	type edge struct {
		from, to int64
		typ      string
	}
	rows, err = s.db.QueryContext(ctx,
		`SELECT from_rule, to_rule, type FROM rule_edges
		  WHERE type IN ('refines', 'needs', 'conflictsWith') ORDER BY type, to_rule`)
	if err != nil {
		return nil, fmt.Errorf("read rule edges for lint: %w", err)
	}
	edges, err := collectRows(rows, "read rule edges for lint", func(r rowScanner) (edge, error) {
		var e edge
		return e, r.Scan(&e.from, &e.to, &e.typ)
	})
	if err != nil {
		return nil, err
	}
	ctxEdges := map[int64][]edge{}
	conflicts := map[int64][]edge{}
	for _, e := range edges {
		if e.typ == "conflictsWith" {
			conflicts[e.from] = append(conflicts[e.from], e)
		} else {
			ctxEdges[e.from] = append(ctxEdges[e.from], e)
		}
	}

	var subjects, defs []*lintRule
	for _, r := range all {
		if r.project == projectID && r.live {
			subjects = append(subjects, r)
			if r.kind == designdoc.RuleKindDefinition {
				defs = append(defs, r)
			}
		}
	}
	slices.SortFunc(subjects, func(a, b *lintRule) int { return int(a.number - b.number) })
	termRE := make(map[int64]*regexp.Regexp, len(defs))
	for _, d := range defs {
		termRE[d.id] = regexp.MustCompile(`(?i)(^|[^\pL\pN_])` + regexp.QuoteMeta(strings.TrimSpace(d.heading)) + `($|[^\pL\pN_])`)
	}

	var sizes, words []int
	add := func(r *lintRule, check, detail string) {
		out.Findings = append(out.Findings, model.RuleLintFinding{Rule: r.ref, Check: check, Detail: detail})
	}
	for _, r := range subjects {
		closure := map[int64]bool{r.id: true}
		total := 0
		for queue := []int64{r.id}; len(queue) > 0; queue = queue[1:] {
			total += byID[queue[0]].words
			for _, e := range ctxEdges[queue[0]] {
				if !closure[e.to] {
					closure[e.to] = true
					queue = append(queue, e.to)
				}
			}
		}
		sizes, words = append(sizes, len(closure)), append(words, total)

		if direct := ctxEdges[r.id]; len(direct) == 1 {
			add(r, "one-context-edge", direct[0].typ+" "+byID[direct[0].to].ref)
		}
		for _, d := range defs {
			if !closure[d.id] && termRE[d.id].MatchString(r.body) {
				add(r, "undefined-term", fmt.Sprintf("%q (%s) is not in the closure", d.heading, d.ref))
			}
		}
		for _, e := range conflicts[r.id] {
			add(r, "conflict", "conflictsWith "+byID[e.to].ref)
		}
		var seen []string
		for _, m := range positionalRefRE.FindAllString(r.body, -1) {
			if !slices.Contains(seen, m) {
				seen = append(seen, m)
			}
		}
		if len(seen) > 0 {
			add(r, "positional-reference", strings.Join(seen, ", "))
		}
		for _, ref := range designdoc.FindRuleRefs(r.heading + "\n" + r.body) {
			if !exists[ref] {
				add(r, "unresolved-ref", designdoc.FormatRuleRef(ref.Key, ref.Number, "")+" names no rule")
			}
		}
	}
	if err := s.convertedHeadings(ctx, projectID, out); err != nil {
		return nil, err
	}
	out.Rules = len(subjects)
	out.ClosureRules, out.ClosureWords = spread(sizes), spread(words)
	return out, nil
}

// convertedHeadings appends one converted-heading finding per rule the
// WL-SPEC-77 §19.7 migration turned into a spec heading in projectID, so an
// owner can check where its covers edges and governedBy links went.
func (s *Store) convertedHeadings(ctx context.Context, projectID string, out *model.RuleLint) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.rule_ref, p.key || '-SPEC-' || d.number || '#' || c.anchor, c.heading,
		        c.covering_plans, c.governed_tasks, c.grouped_rules
		   FROM rule_heading_conversions c
		   JOIN docs d ON d.id = c.doc_id
		   JOIN projects p ON p.id = d.project_id
		  WHERE c.project_id = $1
		  ORDER BY c.rule_ref, d.number`, projectID)
	if err != nil {
		return fmt.Errorf("read converted headings: %w", err)
	}
	found, err := collectRows(rows, "read converted headings", func(r rowScanner) (model.RuleLintFinding, error) {
		var at, heading string
		var plans, tasks, grouped int
		f := model.RuleLintFinding{Check: "converted-heading"}
		err := r.Scan(&f.Rule, &at, &heading, &plans, &tasks, &grouped)
		f.Detail = fmt.Sprintf("now spec heading %s %q; covers from %d plan(s) and governedBy of %d task(s) moved to %d rule(s)",
			at, heading, plans, tasks, grouped)
		return f, err
	})
	out.Findings = append(out.Findings, found...)
	return err
}

// spread is the nearest-rank median and 90th percentile of v; zero for none.
func spread(v []int) model.RuleLintSpread {
	if len(v) == 0 {
		return model.RuleLintSpread{}
	}
	slices.Sort(v)
	rank := func(p int) int { return v[(p*len(v)+99)/100-1] }
	return model.RuleLintSpread{Median: rank(50), P90: rank(90)}
}
