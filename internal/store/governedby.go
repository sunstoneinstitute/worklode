package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Govern links a task to a governing clause (12-spec-refactoring-design-tree.md
// S2, S3), recording the clause version current at link time (S10). source is
// "plan" when a plan's acceptance minted the link and "manual" when an
// architect added it. pin records the current version as the link's pinned
// version, so the link keeps reading that text as the clause moves on;
// without it the link follows the newest version. Re-governing an already
// governed clause updates the pin either way and changes nothing else, so
// re-accepting a plan is safe — except source "gate", which only adds a link
// and leaves an existing one (pin, source, version) untouched (S51).
func Govern(tx *sql.Tx, taskID string, clauseID int64, source string, pin bool) error {
	_, err := tx.Exec(
		`INSERT INTO task_governed_by (task_id, clause_id, clause_version, source, pinned_version)
		 SELECT $1, c.id, c.version, $3, CASE WHEN $4 THEN c.version END FROM clauses c WHERE c.id = $2
		 ON CONFLICT (task_id, clause_id) DO UPDATE SET pinned_version = EXCLUDED.pinned_version
		 WHERE EXCLUDED.source <> 'gate'`,
		taskID, clauseID, source, pin)
	if pgViolation(err, "23503", "task_governed_by_task_id_fkey") {
		return fmt.Errorf("task %s: %w", taskID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("govern %s by clause %d: %w", taskID, clauseID, err)
	}
	return nil
}

// Ungovern removes one governing link, or reports ErrNotFound when the task
// is not governed by that clause.
func Ungovern(tx *sql.Tx, taskID string, clauseID int64) error {
	res, err := tx.Exec(`DELETE FROM task_governed_by WHERE task_id = $1 AND clause_id = $2`, taskID, clauseID)
	if err != nil {
		return fmt.Errorf("ungovern %s from clause %d: %w", taskID, clauseID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s is not governed by clause %d: %w", taskID, clauseID, ErrNotFound)
	}
	return nil
}

// GovernedBy lists a task's governing clauses with the version each link was
// made against and the clause's current version (S10). A withdrawn governing
// clause also carries ResolvesTo, the live clauses it resolves to through
// supersededBy edges (R8).
func (s *Store) GovernedBy(ctx context.Context, taskID string) ([]model.TaskGovernance, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT p.key, c.number, c.id, g.clause_version, c.version, cv.heading, c.status, g.source,
		        g.pinned_version, c.project_id
		   FROM task_governed_by g
		   JOIN clauses c ON c.id = g.clause_id
		   JOIN projects p ON p.id = c.project_id
		   JOIN clause_versions cv ON cv.clause_id = c.id AND cv.version = c.version
		  WHERE g.task_id = $1
		  ORDER BY c.number`, taskID)
	if err != nil {
		return nil, fmt.Errorf("read governing clauses of %s: %w", taskID, err)
	}
	type withID struct {
		g  model.TaskGovernance
		id int64
	}
	var raw []withID
	for rows.Next() {
		var g model.TaskGovernance
		var key, projectID string
		var number, id int64
		var pinned sql.NullInt64
		if err := rows.Scan(&key, &number, &id, &g.ClauseVersion, &g.Current, &g.Heading, &g.Status, &g.Source,
			&pinned, &projectID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan governing clause of %s: %w", taskID, err)
		}
		g.Clause = fmt.Sprintf("%s-CL-%d", key, number)
		g.Pinned = int(pinned.Int64)
		g.URL = fmt.Sprintf("/projects/%s/clause/%d", projectID, number)
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
			resolved, err := resolveGoverningClause(ctx, s.db, r.id)
			if err != nil {
				return nil, err
			}
			g.ResolvesTo = resolved
		}
		out = append(out, g)
	}
	return out, nil
}

// resolveGoverningClause is the live clauses reached from a withdrawn clause
// by following supersededBy edges transitively (S22, R8): a recursive CTE
// with UNION (not UNION ALL) over clause_edges, which dedupes visited
// clauses so a cycle in the edges terminates instead of recursing forever.
// Withdrawn clauses reached along the way are skipped; only live ends are
// returned, ordered by project key then clause number, since a successor
// can live in a different project from the withdrawn clause and clause
// numbers are only unique within a project.
func resolveGoverningClause(ctx context.Context, db *meteredDB, clauseID int64) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`WITH RECURSIVE chain(id) AS (
		    SELECT to_clause FROM clause_edges WHERE from_clause = $1 AND type = 'supersededBy'
		    UNION
		    SELECT ce.to_clause FROM clause_edges ce JOIN chain ch ON ce.from_clause = ch.id
		     WHERE ce.type = 'supersededBy'
		 )
		 SELECT p.key, c.number FROM chain ch
		   JOIN clauses c ON c.id = ch.id
		   JOIN projects p ON p.id = c.project_id
		  WHERE c.status <> 'withdrawn'
		  ORDER BY p.key, c.number`, clauseID)
	if err != nil {
		return nil, fmt.Errorf("resolve successors of clause %d: %w", clauseID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var key string
		var number int64
		if err := rows.Scan(&key, &number); err != nil {
			return nil, fmt.Errorf("scan successor of clause %d: %w", clauseID, err)
		}
		out = append(out, fmt.Sprintf("%s-CL-%d", key, number))
	}
	return out, rows.Err()
}

// planClauses is the plan's arranged clause ids in arrangement order. The
// arrangement is written by arrangePlan on every plan body write and again
// at accept (increment 3 R1).
func planClauses(tx *sql.Tx, planID int64) ([]int64, error) {
	rows, err := arrangedClauses(tx, planID)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.id)
	}
	return out, nil
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

// ClauseAtSection resolves a section ref (WL-SPEC-4 plus sec-5) to the
// clause arranged at that anchor, splitting the document first when it
// predates the clause tables. ErrNotFound when the project, the document or
// the anchor is unknown.
func ClauseAtSection(tx *sql.Tx, ref designdoc.SectionRef) (int64, error) {
	var projectID string
	err := tx.QueryRow(`SELECT id FROM projects WHERE key = $1`, ref.Shorthand.Key).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("project %s: %w", ref.Shorthand.Key, ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("project %s: %w", ref.Shorthand.Key, err)
	}
	base := fmt.Sprintf("%s-%s-%d", ref.Shorthand.Key, ref.Shorthand.Type, ref.Shorthand.Number)
	docID, ok, err := resolveDocRef(tx, projectID, base)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("document %s: %w", base, ErrNotFound)
	}
	if err := ensureClauses(tx, docID); err != nil {
		return 0, err
	}
	entries, err := arrangedClauses(tx, docID)
	if err != nil {
		return 0, err
	}
	for _, c := range entries {
		if c.anchor == ref.Anchor {
			return c.id, nil
		}
	}
	return 0, fmt.Errorf("%s has no clause at %s: %w", base, ref.Anchor, ErrNotFound)
}
