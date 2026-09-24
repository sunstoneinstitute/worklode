package store

import (
	"database/sql"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

// planEntry is one rule a plan's covers edges reach, with the anchor and
// depth it has in the spec that arranges it. Order is coveredRules'
// deterministic edge order (by to_doc, to_anchor), not the order the
// covers: list was written in.
type planEntry struct {
	id      int64
	version int
	depth   int
	anchor  string
}

// coveredRules walks a plan's resolved covers edges the way 026 §5.1
// states it: a section-scoped edge reaches the rule at that anchor and the
// rules arranged under it; a document-scoped edge reaches every rule the
// document arranges; an unresolved edge reaches nothing. A covered spec that
// predates the rule tables is split first (ensureRules).
func coveredRules(tx *sql.Tx, planID int64) ([]planEntry, error) {
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

	arrangements := map[int64][]ruleRow{}
	seen := map[int64]bool{}
	var out []planEntry
	add := func(c ruleRow) {
		if !seen[c.id] {
			seen[c.id] = true
			out = append(out, planEntry{id: c.id, version: c.version, depth: c.depth, anchor: c.anchor})
		}
	}
	for _, e := range edges {
		entries, ok := arrangements[e.doc]
		if !ok {
			if err := ensureRules(tx, e.doc); err != nil {
				return nil, err
			}
			entries, err = arrangedRules(tx, e.doc)
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

// arrangePlan rewrites a plan's doc_rules rows from its covers edges (S16,
// increment 3 R1). A plan arranges the rules it covers and mints none of its
// own; its ## Tasks prose is not a rule. Runs on every plan body write from
// rebuildEdges and again from acceptPlanDoc, so accept sees the spec as it is.
func arrangePlan(tx *sql.Tx, planID int64) error {
	entries, err := coveredRules(tx, planID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM doc_rules WHERE doc_id = $1`, planID); err != nil {
		return fmt.Errorf("clear arrangement of plan %d: %w", planID, err)
	}
	for i, e := range entries {
		if _, err := tx.Exec(
			`INSERT INTO doc_rules (doc_id, position, rule_id, rule_version, depth, anchor)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			planID, i, e.id, e.version, e.depth, e.anchor); err != nil {
			return fmt.Errorf("arrange rule %d in plan %d: %w", e.id, planID, err)
		}
	}
	return nil
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
