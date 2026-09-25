package store

import (
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

// coveredRule is one rule a covers entry resolves to. Depth is how specific
// the entry is: the heading depth of its anchor for a <doc>#sec-N entry, 0 for
// a whole-document entry and ruleRefDepth for a rule ref. When two entries of
// one plan reach the same rule, the deeper one sets its level, so a nested
// section's own entry overrides the level its ancestor's entry implies.
type coveredRule struct {
	ID    int64
	Depth int
}

// ruleRefDepth ranks a rule-ref entry above any section entry.
const ruleRefDepth = math.MaxInt32

// coversRules resolves one covers entry to the rules it names, when the plan
// is written (WL-SPEC-77 §4, WL-SPEC-78 §4.1): a rule ref (WL-RULE-<n>, or its
// earlier spelling WL-CL-<n>) to that rule, a <doc>#sec-N entry to the rule at
// that anchor and every rule arranged under it, and a whole-document entry to
// every rule the document contains, in arrangement order. Only specs and ADRs
// contain rules, so an entry naming a plan, an unknown rule, a missing anchor
// or nothing resolves to none and the caller keeps the reference in
// to_external.
func coversRules(tx *sql.Tx, project, ref string) ([]coveredRule, error) {
	base, fragment := designdoc.SplitFragment(ref)
	if r, ok := designdoc.ParseRuleRef(base); ok && fragment == "" {
		id, err := RuleIDByRef(tx, r.Key, r.Number)
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return []coveredRule{{id, ruleRefDepth}}, nil
	}
	docID, resolved, err := resolveDocRef(tx, project, base)
	if err != nil || !resolved {
		return nil, err
	}
	if err := ensureRules(tx, docID); err != nil {
		return nil, err
	}
	// A section's subtree is its own rule and every later rule in position
	// order until the next heading at its depth or shallower.
	rows, err := tx.Query(
		`SELECT s.rule_id, coalesce(a.depth, 0)
		   FROM docs d
		   LEFT JOIN doc_rules a ON a.doc_id = d.id AND a.anchor = $2
		   JOIN doc_rules s ON s.doc_id = d.id
		  WHERE d.id = $1 AND d.kind <> 'plan'
		    AND ($2 = '' OR (a.rule_id IS NOT NULL AND s.position >= a.position
		         AND NOT EXISTS (SELECT 1 FROM doc_rules n
		                          WHERE n.doc_id = d.id AND n.depth <= a.depth
		                            AND n.position > a.position AND n.position <= s.position)))
		  ORDER BY s.position`, docID, fragment)
	if err != nil {
		return nil, fmt.Errorf("rules of %s: %w", ref, err)
	}
	return collectRows(rows, "rules of "+ref, func(r rowScanner) (coveredRule, error) {
		var c coveredRule
		err := r.Scan(&c.ID, &c.Depth)
		return c, err
	})
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
	if err := syncRules(tx, docID, parsed); err != nil {
		return err
	}
	if status == "accepted" {
		return acceptDocRules(tx, docID)
	}
	return nil
}
