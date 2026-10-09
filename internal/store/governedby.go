package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Govern links a task to a governing rule, recording the rule version current
// at link time. source is
// "plan" when a plan's acceptance minted the link and "manual" when an
// architect added it. pin records the current version as the link's pinned
// version, so the link keeps reading that text as the rule moves on;
// without it the link follows the newest version. Re-governing an already
// governed rule updates the pin either way and changes nothing else, so
// re-accepting a plan is safe — except source "gate", which only adds a link
// and leaves an existing one (pin, source, version) untouched (S51).
func Govern(tx *sql.Tx, taskID string, ruleID int64, source string, pin bool) error {
	_, err := tx.Exec(
		`INSERT INTO task_governed_by (task_id, rule_id, rule_version, source, pinned_version)
		 SELECT $1, c.id, c.version, $3, CASE WHEN $4 THEN c.version END FROM rules c WHERE c.id = $2
		 ON CONFLICT (task_id, rule_id) DO UPDATE SET pinned_version = EXCLUDED.pinned_version
		 WHERE EXCLUDED.source <> 'gate'`,
		taskID, ruleID, source, pin)
	if pgViolation(err, "23503", "task_governed_by_task_id_fkey") {
		return fmt.Errorf("task %s: %w", taskID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("govern %s by rule %d: %w", taskID, ruleID, err)
	}
	return nil
}

// Ungovern removes one governing link, or reports ErrNotFound when the task
// is not governed by that rule.
func Ungovern(tx *sql.Tx, taskID string, ruleID int64) error {
	res, err := tx.Exec(`DELETE FROM task_governed_by WHERE task_id = $1 AND rule_id = $2`, taskID, ruleID)
	if err != nil {
		return fmt.Errorf("ungovern %s from rule %d: %w", taskID, ruleID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s is not governed by rule %d: %w", taskID, ruleID, ErrNotFound)
	}
	return nil
}

// GovernedBy lists a task's governing rules with the version each link was
// made against and the rule's current version (S10): its task_governed_by
// links, then its project's accepted invariants it has no link to, with
// source "invariant" (WL-SPEC-77 §4). The invariants are derived here on
// every read and never stored. A withdrawn governing rule also carries
// ResolvesTo, the live rules it resolves to through supersedes edges back
// from it (R8).
func (s *Store) GovernedBy(ctx context.Context, taskID string) ([]model.TaskGovernance, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT p.key, c.number, c.kind, c.id, g.rule_version, c.version, cv.heading, c.status, g.source,
		        g.pinned_version, c.project_id
		   FROM task_governed_by g
		   JOIN rules c ON c.id = g.rule_id
		   JOIN projects p ON p.id = c.project_id
		   JOIN rule_versions cv ON cv.rule_id = c.id AND cv.version = c.version
		  WHERE g.task_id = $1
		  UNION ALL
		 SELECT p.key, c.number, c.kind, c.id, c.version, c.version, cv.heading, c.status, 'invariant',
		        NULL, c.project_id
		   FROM tasks t
		   JOIN rules c ON c.project_id = t.project_id AND c.kind = 'invariant' AND c.status = 'accepted'
		   JOIN projects p ON p.id = c.project_id
		   JOIN rule_versions cv ON cv.rule_id = c.id AND cv.version = c.version
		  WHERE t.id = $1
		    AND NOT EXISTS (SELECT 1 FROM task_governed_by g WHERE g.task_id = t.id AND g.rule_id = c.id)
		  ORDER BY 2`, taskID)
	if err != nil {
		return nil, fmt.Errorf("read governing rules of %s: %w", taskID, err)
	}
	type withID struct {
		g  model.TaskGovernance
		id int64
	}
	var raw []withID
	for rows.Next() {
		var g model.TaskGovernance
		var key, kind, projectID string
		var number, id int64
		var pinned sql.NullInt64
		if err := rows.Scan(&key, &number, &kind, &id, &g.RuleVersion, &g.Current, &g.Heading, &g.Status, &g.Source,
			&pinned, &projectID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan governing rule of %s: %w", taskID, err)
		}
		g.Rule = designdoc.FormatRuleRef(key, number, kind)
		g.Pinned = int(pinned.Int64)
		g.URL = fmt.Sprintf("/projects/%s/rule/%d", projectID, number)
		if pinned.Valid {
			g.URL += fmt.Sprintf("/%d", pinned.Int64)
		}
		raw = append(raw, withID{g, id})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	out := make([]model.TaskGovernance, 0, len(raw))
	for _, r := range raw {
		g := r.g
		if g.Status == "withdrawn" {
			resolved, err := resolveGoverningRule(ctx, s.db, r.id)
			if err != nil {
				return nil, err
			}
			g.ResolvesTo = resolved
		}
		out = append(out, g)
	}
	return out, nil
}

// resolveGoverningRule is the live rules reached from a withdrawn rule
// by following supersedes edges (new -> old) backwards from it,
// transitively (S22, R8): a recursive CTE
// with UNION (not UNION ALL) over rule_edges, which dedupes visited
// rules so a cycle in the edges terminates instead of recursing forever.
// Withdrawn rules reached along the way are skipped; only live ends are
// returned, ordered by project key then rule number, since a successor
// can live in a different project from the withdrawn rule and rule
// numbers are only unique within a project.
func resolveGoverningRule(ctx context.Context, db *meteredDB, ruleID int64) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`WITH RECURSIVE chain(id) AS (
		    SELECT from_rule FROM rule_edges WHERE to_rule = $1 AND type = 'supersedes'
		    UNION
		    SELECT ce.from_rule FROM rule_edges ce JOIN chain ch ON ce.to_rule = ch.id
		     WHERE ce.type = 'supersedes'
		 )
		 SELECT `+ruleRefSQL("p", "c")+` FROM chain ch
		   JOIN rules c ON c.id = ch.id
		   JOIN projects p ON p.id = c.project_id
		  WHERE c.status <> 'withdrawn'
		  ORDER BY p.key, c.number`, ruleID)
	if err != nil {
		return nil, fmt.Errorf("resolve successors of rule %d: %w", ruleID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, fmt.Errorf("scan successor of rule %d: %w", ruleID, err)
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// HasPlanGovernance reports whether a plan governs the task: any link with
// source = 'plan'. The gate writes only when this is false (S51).
func HasPlanGovernance(tx *sql.Tx, taskID string) (bool, error) {
	var one int
	err := tx.QueryRow(
		`SELECT 1 FROM task_governed_by WHERE task_id = $1 AND source = 'plan' LIMIT 1`, taskID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("plan governance of task %s: %w", taskID, err)
	}
	return true, nil
}

// RulesAtSection resolves a section ref (WL-SPEC-4 plus sec-5) to the rule
// arranged at that anchor or, when the anchor is a spec heading, to the rules
// grouped under it (WL-SPEC-77 §19.1), splitting the document first when it
// predates the rule tables. ErrNotFound when the project, the document or the
// anchor is unknown, or a heading groups no rule.
func RulesAtSection(tx *sql.Tx, ref designdoc.SectionRef) ([]int64, error) {
	var projectID string
	err := tx.QueryRow(`SELECT id FROM projects WHERE key = $1`, ref.Shorthand.Key).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("project %s: %w", ref.Shorthand.Key, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("project %s: %w", ref.Shorthand.Key, err)
	}
	base := fmt.Sprintf("%s-%s-%d", ref.Shorthand.Key, ref.Shorthand.Type, ref.Shorthand.Number)
	docID, ok, err := resolveDocRef(tx, projectID, base)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("document %s: %w", base, ErrNotFound)
	}
	if err := ensureRules(tx, docID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(
		`SELECT s.rule_id
		   FROM doc_rules a
		   JOIN doc_rules s ON s.doc_id = a.doc_id AND s.rule_id IS NOT NULL
		  WHERE a.doc_id = $1 AND a.anchor = $2
		    AND CASE WHEN a.rule_id IS NOT NULL THEN s.position = a.position
		             ELSE s.position > a.position AND NOT EXISTS (
		                  SELECT 1 FROM doc_rules n
		                   WHERE n.doc_id = a.doc_id AND n.depth <= a.depth
		                     AND n.position > a.position AND n.position <= s.position) END
		  ORDER BY s.position`, docID, ref.Anchor)
	if err != nil {
		return nil, fmt.Errorf("rules at %s#%s: %w", base, ref.Anchor, err)
	}
	ids, err := scanColumn[int64](rows, fmt.Sprintf("rules at %s#%s", base, ref.Anchor))
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%s has no rule at %s: %w", base, ref.Anchor, ErrNotFound)
	}
	return ids, nil
}
