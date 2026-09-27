package store

import (
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// coversRules resolves one covers entry to the requirements it names, when
// the plan is written (WL-SPEC-77 §4, WL-SPEC-78 §4.1): a rule ref (any
// infix) to that rule, a <doc>#sec-N entry to the requirements at that
// anchor and under it, and a whole-document entry to every requirement the
// document contains, in arrangement order. A section or document entry skips
// invariants and informative rules; a rule ref naming one is refused. named
// is false when the entry names no rule of any kind: a plan, an unknown
// rule, a missing anchor or nothing, and the caller keeps the reference in
// to_external.
func coversRules(tx *sql.Tx, project, ref string) (rules []int64, named bool, err error) {
	base, fragment := designdoc.SplitFragment(ref)
	if r, ok := designdoc.ParseRuleRef(base); ok && fragment == "" {
		var id int64
		var kind string
		err := tx.QueryRow(
			`SELECT c.id, c.kind FROM rules c JOIN projects p ON p.id = c.project_id
			  WHERE p.key = $1 AND c.number = $2`, r.Key, r.Number).Scan(&id, &kind)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("resolve covers %s: %w", ref, err)
		}
		if kind != designdoc.RuleKindRequirement {
			return nil, true, fmt.Errorf("covers %s: %s is an %s rule, and a plan covers only requirements (WL-SPEC-78 §4.1): %w",
				ref, designdoc.FormatRuleRef(r.Key, r.Number, kind), kind, ErrInvalidInput)
		}
		return []int64{id}, true, nil
	}
	docID, resolved, err := resolveDocRef(tx, project, base)
	if err != nil || !resolved {
		return nil, false, err
	}
	if err := ensureRules(tx, docID); err != nil {
		return nil, false, err
	}
	// A section's subtree is its own rule and every later rule in position
	// order until the next heading at its depth or shallower.
	rows, err := tx.Query(
		`SELECT s.rule_id, r.kind
		   FROM docs d
		   LEFT JOIN doc_rules a ON a.doc_id = d.id AND a.anchor = $2
		   JOIN doc_rules s ON s.doc_id = d.id
		   JOIN rules r ON r.id = s.rule_id
		  WHERE d.id = $1 AND d.kind <> 'plan'
		    AND ($2 = '' OR (a.rule_id IS NOT NULL AND s.position >= a.position
		         AND NOT EXISTS (SELECT 1 FROM doc_rules n
		                          WHERE n.doc_id = d.id AND n.depth <= a.depth
		                            AND n.position > a.position AND n.position <= s.position)))
		  ORDER BY s.position`, docID, fragment)
	if err != nil {
		return nil, false, fmt.Errorf("rules of %s: %w", ref, err)
	}
	type scoped struct {
		id   int64
		kind string
	}
	all, err := collectRows(rows, "rules of "+ref, func(r rowScanner) (scoped, error) {
		var x scoped
		err := r.Scan(&x.id, &x.kind)
		return x, err
	})
	if err != nil {
		return nil, false, err
	}
	for _, x := range all {
		if x.kind == designdoc.RuleKindRequirement {
			rules = append(rules, x.id)
		}
	}
	return rules, len(all) > 0, nil
}

// planRules is the rules a plan's covers edges point at, in edge order: the
// rules that govern the tasks it mints and, at close, its ungoverned tasks
// (WL-SPEC-75 §4). It reads the edges as stored and does not follow
// supersession; a governed task resolves a withdrawn rule to its successors
// when it is read.
func planRules(tx *sql.Tx, planID int64) ([]int64, error) {
	rows, err := tx.Query(
		`SELECT to_rule FROM doc_edges
		  WHERE from_doc = $1 AND type = 'covers' AND to_rule IS NOT NULL
		  ORDER BY id`, planID)
	if err != nil {
		return nil, fmt.Errorf("covered rules of plan %d: %w", planID, err)
	}
	return scanColumn[int64](rows, fmt.Sprintf("covered rules of plan %d", planID))
}

// acceptedPlansCovering is the accepted, live plans covering any of the rules,
// through the covered_rules view, so a plan covering a predecessor covers its
// successors too. These are the plans a withdrawal marks stale (WL-SPEC-77
// §9).
func acceptedPlansCovering(tx *sql.Tx, ruleIDs []int64) ([]int64, error) {
	rows, err := tx.Query(
		`SELECT DISTINCT d.id FROM covered_rules c JOIN docs d ON d.id = c.plan_id
		  WHERE c.rule_id = ANY($1) AND d.status = 'accepted' AND d.deleted_at IS NULL
		  ORDER BY d.id`, ruleIDs)
	if err != nil {
		return nil, fmt.Errorf("plans covering rules %v: %w", ruleIDs, err)
	}
	return scanColumn[int64](rows, "plans covering rules")
}

// ensureRules splits a spec or ADR that predates the rule tables, so a
// plan covering it can be governed by its rules. A document written after
// the tables exist is split on every write and is left alone here. When the
// document is already accepted, its backfilled rules are accepted too
// (S11) — they must not stay draft just because they arrived late.
func ensureRules(tx *sql.Tx, docID int64) error {
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM doc_rules WHERE doc_id = $1`, docID).Scan(&n); err != nil {
		return fmt.Errorf("count arrangement of doc %d: %w", docID, err)
	}
	if n > 0 {
		return nil
	}
	var kind, status, body string
	if err := tx.QueryRow(`SELECT kind, status, body FROM docs WHERE id = $1`, docID).Scan(&kind, &status, &body); err != nil {
		return fmt.Errorf("read doc %d: %w", docID, err)
	}
	if kind == "plan" {
		return nil
	}
	parsed, err := designdoc.Parse([]byte(body))
	if err != nil {
		return fmt.Errorf("parse doc %d: %w", docID, err)
	}
	if _, err := syncRules(tx, docID, parsed); err != nil {
		return err
	}
	if status == "accepted" {
		return acceptDocRules(tx, docID)
	}
	return nil
}

// ResolveExternalCovers re-resolves every live plan's to_external covers
// entries: the rows stranded before their target had rules (WL-903).
func ResolveExternalCovers(tx *sql.Tx, eventID int64) (model.CoversResolveResponse, error) {
	return resolveExternalCovers(tx, 0, eventID)
}

// resolveExternalCovers re-resolves live plans' to_external covers edges
// through coversRules, the resolver every edge write uses. target limits it
// to entries whose document part resolves to that document; 0 takes every
// entry. An entry that now names requirements is replaced by its plan -> rule
// edges. One naming none stays as it is and is reported with the reason.
// Each plan whose edges moved gets one state_log row.
func resolveExternalCovers(tx *sql.Tx, target, eventID int64) (model.CoversResolveResponse, error) {
	out := model.CoversResolveResponse{Resolved: []model.CoversResolution{}, Unresolved: []model.CoversResolution{}}
	type stranded struct {
		id, plan              int64
		project, planRef, ref string
	}
	rows, err := tx.Query(
		`SELECT e.id, d.id, d.project_id, p.key || '-PLAN-' || coalesce(d.number::text, d.slug), e.to_external
		   FROM doc_edges e JOIN docs d ON d.id = e.from_doc JOIN projects p ON p.id = d.project_id
		  WHERE e.type = 'covers' AND e.to_external IS NOT NULL
		    AND d.kind = 'plan' AND d.deleted_at IS NULL
		    AND ($1 = 0 OR EXISTS (SELECT 1 FROM docs t WHERE t.id = $1
		         AND (strpos(e.to_external, t.slug) > 0
		              OR e.to_external ~ ('(^|[^0-9])0*' || t.number || '([^0-9]|$)'))))
		  ORDER BY e.id`, target)
	if err != nil {
		return out, fmt.Errorf("read unresolved covers: %w", err)
	}
	all, err := collectRows(rows, "read unresolved covers", func(r rowScanner) (stranded, error) {
		var c stranded
		err := r.Scan(&c.id, &c.plan, &c.project, &c.planRef, &c.ref)
		return c, err
	})
	if err != nil {
		return out, err
	}
	touched := map[int64]bool{}
	for _, c := range all {
		// The query's slug-or-number match only narrows; resolveDocRef decides.
		if target != 0 {
			base, _ := designdoc.SplitFragment(c.ref)
			id, ok, err := resolveDocRef(tx, c.project, base)
			if err != nil {
				return out, err
			}
			if !ok || id != target {
				continue
			}
		}
		entry := model.CoversResolution{Plan: c.planRef, Ref: c.ref}
		rules, named, err := coversRules(tx, c.project, c.ref)
		switch {
		case errors.Is(err, ErrInvalidInput):
			entry.Reason = "names a rule that is not a requirement"
		case err != nil:
			return out, err
		case !named:
			entry.Reason = "names no rule"
		case len(rules) == 0:
			entry.Reason = "names no requirement"
		}
		if entry.Reason != "" {
			out.Unresolved = append(out.Unresolved, entry)
			continue
		}
		if err := replaceExternalCover(tx, c.id, c.plan, rules); err != nil {
			return out, err
		}
		for _, r := range rules {
			entry.Rules = append(entry.Rules, ruleRefOf(tx, r))
		}
		out.Resolved = append(out.Resolved, entry)
		touched[c.plan] = true
	}
	for _, id := range slices.Sorted(maps.Keys(touched)) {
		if err := logDocChange(tx, id, eventID, map[string]string{"field": "edges"}); err != nil {
			return out, err
		}
	}
	return out, nil
}

// replaceExternalCover replaces the to_external covers edge edgeID of plan
// fromDoc with one edge per rule. An edge the plan already holds is kept.
func replaceExternalCover(tx *sql.Tx, edgeID, fromDoc int64, rules []int64) error {
	for _, r := range rules {
		if _, err := tx.Exec(
			`INSERT INTO doc_edges (from_doc, from_anchor, type, to_rule)
			 SELECT from_doc, from_anchor, type, $2 FROM doc_edges WHERE id = $1
			 ON CONFLICT (from_doc, coalesce(from_anchor,''), type, coalesce(to_doc, 0),
			              coalesce(to_rule, 0), coalesce(to_anchor,''), coalesce(to_external,''))
			 DO NOTHING`, edgeID, r); err != nil {
			return fmt.Errorf("re-point covers edge %d of doc %d to rule %d: %w", edgeID, fromDoc, r, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM doc_edges WHERE id = $1`, edgeID); err != nil {
		return fmt.Errorf("drop re-pointed covers edge %d of doc %d: %w", edgeID, fromDoc, err)
	}
	return nil
}
