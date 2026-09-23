package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// clauseEdgeTypes are the wl: properties a clause edge may carry (S12, S26,
// S22). supersededBy and wasDerivedFrom reuse dct:isReplacedBy and
// prov:wasDerivedFrom (ns/ontology.ttl's reuse list).
var clauseEdgeTypes = map[string]bool{
	"refines": true, "constrains": true, "conflictsWith": true, "references": true,
	"supersededBy": true, "wasDerivedFrom": true,
}

// clauseEdgeTypesList names every recognized type, for error messages.
const clauseEdgeTypesList = "refines, constrains, conflictsWith, references, supersededBy, wasDerivedFrom"

// LinkClauses writes a manual edge (12-spec-refactoring-design-tree.md S12).
// A second identical edge is ErrEdgeExists; a self edge or an unknown type is
// ErrInvalidInput; an unknown clause id is ErrNotFound. supersededBy has one
// writer, lode clause supersede (S22, R4): LinkClauses refuses it.
func LinkClauses(tx *sql.Tx, fromID, toID int64, typ string) error {
	if !clauseEdgeTypes[typ] {
		return fmt.Errorf("edge type %q is not one of %s: %w", typ, clauseEdgeTypesList, ErrInvalidInput)
	}
	if typ == "supersededBy" {
		return fmt.Errorf("supersededBy edges are written by lode clause supersede, not clause link: %w", ErrInvalidInput)
	}
	if fromID == toID {
		return fmt.Errorf("a clause cannot relate to itself: %w", ErrInvalidInput)
	}
	_, err := tx.Exec(
		`INSERT INTO clause_edges (from_clause, to_clause, type, source) VALUES ($1, $2, $3, 'manual')`,
		fromID, toID, typ)
	switch {
	case isUniqueViolationOn(err, "clause_edges_pkey"):
		return fmt.Errorf("clause %d already %s clause %d: %w", fromID, typ, toID, ErrEdgeExists)
	case pgViolation(err, "23503", "clause_edges_from_clause_fkey"), pgViolation(err, "23503", "clause_edges_to_clause_fkey"):
		return fmt.Errorf("clause: %w", ErrNotFound)
	case err != nil:
		return fmt.Errorf("link clause %d %s %d: %w", fromID, typ, toID, err)
	}
	return nil
}

// UnlinkClauses removes a manual edge, or reports ErrNotFound. A derived
// edge cannot be removed by hand: it comes back on the next version anyway.
// A refactor edge cannot be removed by hand either (S22, R4): it is undone
// only by a later refactor.
func UnlinkClauses(tx *sql.Tx, fromID, toID int64, typ string) error {
	if !clauseEdgeTypes[typ] {
		return fmt.Errorf("edge type %q is not one of %s: %w", typ, clauseEdgeTypesList, ErrInvalidInput)
	}
	var source string
	err := tx.QueryRow(`SELECT source FROM clause_edges WHERE from_clause = $1 AND to_clause = $2 AND type = $3`,
		fromID, toID, typ).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("clause %d does not %s clause %d: %w", fromID, typ, toID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("read edge %d %s %d: %w", fromID, typ, toID, err)
	}
	if source == "derived" {
		return fmt.Errorf("the %s edge from clause %d to %d is derived from its text; edit the clause instead: %w", typ, fromID, toID, ErrInvalidInput)
	}
	if source == "refactor" {
		return fmt.Errorf("the %s edge from clause %d to %d was written by a refactor; only a later refactor can undo it: %w", typ, fromID, toID, ErrInvalidInput)
	}
	if _, err := tx.Exec(`DELETE FROM clause_edges WHERE from_clause = $1 AND to_clause = $2 AND type = $3`,
		fromID, toID, typ); err != nil {
		return fmt.Errorf("unlink clause %d %s %d: %w", fromID, typ, toID, err)
	}
	return nil
}

// clauseEdgesSQL lists every edge in or out of one clause with both refs and
// headings, outgoing first, then by type and the other clause's number.
const clauseEdgesSQL = `
SELECT e.type, e.source, e.created_at,
       pf.key, cf.number, vf.heading,
       pt.key, ct.number, vt.heading
  FROM clause_edges e
  JOIN clauses cf ON cf.id = e.from_clause
  JOIN projects pf ON pf.id = cf.project_id
  JOIN clause_versions vf ON vf.clause_id = cf.id AND vf.version = cf.version
  JOIN clauses ct ON ct.id = e.to_clause
  JOIN projects pt ON pt.id = ct.project_id
  JOIN clause_versions vt ON vt.clause_id = ct.id AND vt.version = ct.version
 WHERE e.from_clause = $1 OR e.to_clause = $1
 ORDER BY (e.from_clause = $1) DESC, e.type, ct.number, cf.number`

// scanClauseEdges turns the rows of clauseEdgesSQL into model edges.
func scanClauseEdges(rows *sql.Rows) ([]model.ClauseEdge, error) {
	out := []model.ClauseEdge{}
	for rows.Next() {
		var e model.ClauseEdge
		var fk, tk string
		var fn, tn int64
		if err := rows.Scan(&e.Type, &e.Source, &e.CreatedAt, &fk, &fn, &e.FromHeading, &tk, &tn, &e.ToHeading); err != nil {
			return nil, fmt.Errorf("scan clause edge: %w", err)
		}
		e.From = fmt.Sprintf("%s-CL-%d", fk, fn)
		e.To = fmt.Sprintf("%s-CL-%d", tk, tn)
		out = append(out, e)
	}
	return out, rows.Err()
}

// deriveReferences replaces a clause's derived references edges with the
// clauses its text names (S26): every WL-CL-<n> ref, and every
// WL-SPEC-<n>#sec-<a> ref resolved to the clause arranged at that anchor. A
// ref to the clause itself, to a whole document, or to nothing contributes
// no edge. Manual edges are untouched; a derived edge that would duplicate a
// manual one is skipped by the ON CONFLICT.
func deriveReferences(tx *sql.Tx, project string, clauseID int64, text string) error {
	if _, err := tx.Exec(
		`DELETE FROM clause_edges WHERE from_clause = $1 AND type = 'references' AND source = 'derived'`,
		clauseID); err != nil {
		return fmt.Errorf("clear derived references of clause %d: %w", clauseID, err)
	}
	var targets []int64
	for _, r := range designdoc.FindClauseRefs(text) {
		var id int64
		err := tx.QueryRow(
			`SELECT c.id FROM clauses c JOIN projects p ON p.id = c.project_id WHERE p.key = $1 AND c.number = $2`,
			r.Key, r.Number).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("resolve %s-CL-%d: %w", r.Key, r.Number, err)
		}
		targets = append(targets, id)
	}
	for _, r := range designdoc.FindSectionRefs(text) {
		shorthand := fmt.Sprintf("%s-%s-%d", r.Shorthand.Key, r.Shorthand.Type, r.Shorthand.Number)
		docID, ok, err := resolveDocRef(tx, project, shorthand)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		var id int64
		// A plan's doc_clauses rows borrow the spec's clauses, so a ref to a
		// plan anchor names no clause of its own.
		err = tx.QueryRow(
			`SELECT dc.clause_id FROM doc_clauses dc JOIN docs d ON d.id = dc.doc_id
			  WHERE dc.doc_id = $1 AND dc.anchor = $2 AND d.kind <> 'plan'`, docID, r.Anchor).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("resolve %s#%s: %w", shorthand, r.Anchor, err)
		}
		targets = append(targets, id)
	}
	for _, to := range targets {
		if to == clauseID {
			continue
		}
		if _, err := tx.Exec(
			`INSERT INTO clause_edges (from_clause, to_clause, type, source) VALUES ($1, $2, 'references', 'derived')
			 ON CONFLICT (from_clause, to_clause, type) DO NOTHING`, clauseID, to); err != nil {
			return fmt.Errorf("derive reference %d -> %d: %w", clauseID, to, err)
		}
	}
	return nil
}
