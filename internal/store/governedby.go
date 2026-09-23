package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Govern links a task to a governing clause (12-spec-refactoring-design-tree.md
// S2, S3), recording the clause version current at link time (S10). source is
// "plan" when a plan's acceptance minted the link and "manual" when an
// architect added it. pin records the current version as the link's pinned
// version, so the link keeps reading that text as the clause moves on;
// without it the link follows the newest version. Re-governing an already
// governed clause updates the pin either way and changes nothing else, so
// re-accepting a plan is safe.
func Govern(tx *sql.Tx, taskID string, clauseID int64, source string, pin bool) error {
	_, err := tx.Exec(
		`INSERT INTO task_governed_by (task_id, clause_id, clause_version, source, pinned_version)
		 SELECT $1, c.id, c.version, $3, CASE WHEN $4 THEN c.version END FROM clauses c WHERE c.id = $2
		 ON CONFLICT (task_id, clause_id) DO UPDATE SET pinned_version = EXCLUDED.pinned_version`,
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
// made against and the clause's current version (S10).
func (s *Store) GovernedBy(ctx context.Context, taskID string) ([]model.TaskGovernance, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT p.key, c.number, g.clause_version, c.version, cv.heading, c.status, g.source,
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
	defer rows.Close()
	out := []model.TaskGovernance{}
	for rows.Next() {
		var g model.TaskGovernance
		var key, projectID string
		var number int64
		var pinned sql.NullInt64
		if err := rows.Scan(&key, &number, &g.ClauseVersion, &g.Current, &g.Heading, &g.Status, &g.Source,
			&pinned, &projectID); err != nil {
			return nil, fmt.Errorf("scan governing clause of %s: %w", taskID, err)
		}
		g.Clause = fmt.Sprintf("%s-CL-%d", key, number)
		g.Pinned = int(pinned.Int64)
		g.URL = fmt.Sprintf("/projects/%s/clause/%d", projectID, number)
		if pinned.Valid {
			g.URL += fmt.Sprintf("/%d", pinned.Int64)
		}
		out = append(out, g)
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
