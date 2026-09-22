package store

import (
	"context"
	"database/sql"
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

// planClauses is every clause a plan's covers edges reach (S1, S2): a
// section-scoped edge reaches the clause arranged under that anchor and every
// clause arranged beneath it (a section is its whole subtree, 026 §3), a
// document-scoped edge reaches every clause the document arranges. A covered
// document that has no arrangement yet is split first.
func planClauses(tx *sql.Tx, planID int64) ([]int64, error) {
	rows, err := tx.Query(
		`SELECT to_doc, coalesce(to_anchor, '') FROM doc_edges
		  WHERE from_doc = $1 AND type = 'covers' AND to_doc IS NOT NULL
		  ORDER BY to_doc, coalesce(to_anchor, '')`, planID)
	if err != nil {
		return nil, fmt.Errorf("read covers edges of plan %d: %w", planID, err)
	}
	type edge struct {
		doc    int64
		anchor string
	}
	var edges []edge
	for rows.Next() {
		var e edge
		if err := rows.Scan(&e.doc, &e.anchor); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan covers edge of plan %d: %w", planID, err)
		}
		edges = append(edges, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	arrangements := map[int64][]clauseRow{}
	seen := map[int64]bool{}
	var out []int64
	add := func(id int64) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, e := range edges {
		entries, ok := arrangements[e.doc]
		if !ok {
			if err := ensureClauses(tx, e.doc); err != nil {
				return nil, err
			}
			entries, err = arrangedClauses(tx, e.doc)
			if err != nil {
				return nil, err
			}
			arrangements[e.doc] = entries
		}
		if e.anchor == "" {
			for _, c := range entries {
				add(c.id)
			}
			continue
		}
		for i, c := range entries {
			if c.anchor != e.anchor {
				continue
			}
			add(c.id)
			for j := i + 1; j < len(entries) && entries[j].depth > c.depth; j++ {
				add(entries[j].id)
			}
			break
		}
	}
	return out, nil
}

// ensureClauses splits a spec or ADR that predates the clause tables, so a
// plan covering it can be governed by its clauses. A document written after
// the tables exist is split on every write and is left alone here. When the
// document is already accepted, its backfilled clauses are accepted too
// (S11) — they must not stay draft just because they arrived late.
func ensureClauses(tx *sql.Tx, docID int64) error {
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM doc_clauses WHERE doc_id = $1`, docID).Scan(&n); err != nil {
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
	if err := syncClauses(tx, docID, parsed); err != nil {
		return err
	}
	if status == "accepted" {
		return acceptDocClauses(tx, docID)
	}
	return nil
}
