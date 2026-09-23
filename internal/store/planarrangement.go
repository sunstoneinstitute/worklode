package store

import (
	"database/sql"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

// planEntry is one clause a plan's covers edges reach, with the anchor and
// depth it has in the spec that arranges it. Order is coveredClauses'
// deterministic edge order (by to_doc, to_anchor), not the order the
// covers: list was written in.
type planEntry struct {
	id      int64
	version int
	depth   int
	anchor  string
}

// coveredClauses walks a plan's resolved covers edges the way 026 §5.1
// states it: a section-scoped edge reaches the clause at that anchor and the
// clauses arranged under it; a document-scoped edge reaches every clause the
// document arranges; an unresolved edge reaches nothing. A covered spec that
// predates the clause tables is split first (ensureClauses).
func coveredClauses(tx *sql.Tx, planID int64) ([]planEntry, error) {
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
	var out []planEntry
	add := func(c clauseRow) {
		if !seen[c.id] {
			seen[c.id] = true
			out = append(out, planEntry{id: c.id, version: c.version, depth: c.depth, anchor: c.anchor})
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
				add(c)
			}
			continue
		}
		for i, c := range entries {
			if c.anchor != e.anchor {
				continue
			}
			add(c)
			for j := i + 1; j < len(entries) && entries[j].depth > c.depth; j++ {
				add(entries[j])
			}
			break
		}
	}
	return out, nil
}

// arrangePlan rewrites a plan's doc_clauses rows from its covers edges (S16,
// increment 3 R1). A plan arranges the clauses it covers and mints none of its
// own; its ## Tasks prose is not a clause. Runs on every plan body write from
// rebuildEdges and again from acceptPlanDoc, so accept sees the spec as it is.
func arrangePlan(tx *sql.Tx, planID int64) error {
	entries, err := coveredClauses(tx, planID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM doc_clauses WHERE doc_id = $1`, planID); err != nil {
		return fmt.Errorf("clear arrangement of plan %d: %w", planID, err)
	}
	for i, e := range entries {
		if _, err := tx.Exec(
			`INSERT INTO doc_clauses (doc_id, position, clause_id, clause_version, depth, anchor)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			planID, i, e.id, e.version, e.depth, e.anchor); err != nil {
			return fmt.Errorf("arrange clause %d in plan %d: %w", e.id, planID, err)
		}
	}
	return nil
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
