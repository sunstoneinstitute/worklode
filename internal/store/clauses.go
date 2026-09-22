package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// clauseRow is one clause as a document's current arrangement holds it.
type clauseRow struct {
	id      int64
	version int
	heading string
	body    string
	anchor  string
	depth   int
}

// syncClauses makes a document's clauses agree with its parsed source
// (12-spec-refactoring-design-tree.md S8 to S11, S20). Every anchored section
// is a design clause; changed text rewrites the clause's draft version or, once
// that version is accepted, becomes its next version; anything else is a new
// clause numbered from the project's CL counter. The arrangement
// (doc_clauses) is rewritten in section order. Plans never reach here:
// rebuildSectionsFrom returns before calling it.
//
// Matching a section to a prior clause runs in three priority passes over
// the whole document, not per section in document order: anchors are
// positional (`--update-section-anchors` rewrites every `{#sec-N}` from tree
// position, and authors renumber drafts by hand), so resolving matches
// section-by-section can let a later section steal an earlier one's identity
// through the heading fallback once its own anchor match is already taken.
// Pass 1 claims by anchor and heading both matching; pass 2 claims the
// remainder by heading; pass 3 claims what's left by anchor alone. Each
// prior clause is claimed at most once. When two sections share a heading,
// candidates are claimed in document order, one per section: the first
// unclaimed clause with that heading wins. Only after every section has its
// match (or none) does the second walk, in document order, bump/keep clauses
// and write doc_clauses positions.
func syncClauses(tx *sql.Tx, docID int64, doc *designdoc.Document) error {
	var project string
	if err := tx.QueryRow(`SELECT project_id FROM docs WHERE id = $1`, docID).Scan(&project); err != nil {
		return fmt.Errorf("project of doc %d: %w", docID, err)
	}
	prior, err := arrangedClauses(tx, docID)
	if err != nil {
		return err
	}
	byAnchor := map[string]*clauseRow{}
	byHeading := map[string][]*clauseRow{}
	for i := range prior {
		byAnchor[prior[i].anchor] = &prior[i]
		byHeading[prior[i].heading] = append(byHeading[prior[i].heading], &prior[i])
	}
	claimed := map[int64]bool{}
	match := make([]*clauseRow, len(doc.Sections))

	// Pass 1: anchor and heading both match.
	for i, sec := range doc.Sections {
		if sec.Anchor == "" {
			continue
		}
		if c := byAnchor[sec.Anchor]; c != nil && !claimed[c.id] && c.heading == sec.Title {
			match[i], claimed[c.id] = c, true
		}
	}
	// Pass 2: heading matches a still-unclaimed prior clause.
	for i, sec := range doc.Sections {
		if sec.Anchor == "" || match[i] != nil {
			continue
		}
		for _, c := range byHeading[sec.Title] {
			if !claimed[c.id] {
				match[i], claimed[c.id] = c, true
				break
			}
		}
	}
	// Pass 3: anchor matches a still-unclaimed prior clause.
	for i, sec := range doc.Sections {
		if sec.Anchor == "" || match[i] != nil {
			continue
		}
		if c := byAnchor[sec.Anchor]; c != nil && !claimed[c.id] {
			match[i], claimed[c.id] = c, true
		}
	}

	if _, err := tx.Exec(`DELETE FROM doc_clauses WHERE doc_id = $1`, docID); err != nil {
		return fmt.Errorf("clear arrangement of doc %d: %w", docID, err)
	}
	position := 0
	for i, sec := range doc.Sections {
		if sec.Anchor == "" {
			continue
		}
		m := match[i]
		var id int64
		var version int
		switch {
		case m == nil:
			id, version, err = insertClause(tx, project, sec.Title, sec.Body)
		case m.heading != sec.Title || m.body != sec.Body:
			id, version, err = reviseClause(tx, m.id, sec.Title, sec.Body)
		default:
			id, version = m.id, m.version
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO doc_clauses (doc_id, position, clause_id, clause_version, depth, anchor)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			docID, position, id, version, sec.Level, sec.Anchor); err != nil {
			return fmt.Errorf("arrange clause %d in doc %d: %w", id, docID, err)
		}
		position++
	}
	return nil
}

// arrangedClauses reads a document's current arrangement with each clause's
// arranged version text, in position order.
func arrangedClauses(tx *sql.Tx, docID int64) ([]clauseRow, error) {
	rows, err := tx.Query(
		`SELECT dc.clause_id, dc.clause_version, cv.heading, cv.body, dc.anchor, dc.depth
		   FROM doc_clauses dc
		   JOIN clause_versions cv ON cv.clause_id = dc.clause_id AND cv.version = dc.clause_version
		  WHERE dc.doc_id = $1
		  ORDER BY dc.position`, docID)
	if err != nil {
		return nil, fmt.Errorf("read arrangement of doc %d: %w", docID, err)
	}
	defer rows.Close()
	var out []clauseRow
	for rows.Next() {
		var c clauseRow
		if err := rows.Scan(&c.id, &c.version, &c.heading, &c.body, &c.anchor, &c.depth); err != nil {
			return nil, fmt.Errorf("scan arrangement of doc %d: %w", docID, err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// clauseSeqKind is the clause's row key in project_entity_seq — the counter
// behind its WL-CL-<n> number.
const clauseSeqKind = "CL"

// insertClause mints a clause at version 1 with the project's next CL number.
// The upsert is the same counter CreateDoc uses for document numbers, held
// under the row lock for the rest of the transaction.
func insertClause(tx *sql.Tx, project, heading, body string) (int64, int, error) {
	var number int64
	if err := tx.QueryRow(
		`INSERT INTO project_entity_seq (project_id, kind, next) VALUES ($1, $2, 2)
		 ON CONFLICT (project_id, kind) DO UPDATE SET next = project_entity_seq.next + 1
		 RETURNING next - 1`, project, clauseSeqKind).Scan(&number); err != nil {
		return 0, 0, fmt.Errorf("allocate clause number in %s: %w", project, err)
	}
	var id int64
	if err := tx.QueryRow(
		`INSERT INTO clauses (project_id, number) VALUES ($1, $2) RETURNING id`,
		project, number).Scan(&id); err != nil {
		return 0, 0, fmt.Errorf("insert clause %s CL %d: %w", project, number, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO clause_versions (clause_id, version, heading, body) VALUES ($1, 1, $2, $3)`,
		id, heading, body); err != nil {
		return 0, 0, fmt.Errorf("insert clause %d v1: %w", id, err)
	}
	return id, 1, nil
}

// reviseClause records changed text on a clause (S10, S35). While the
// clause's newest version is a draft, the text is rewritten in place: an
// autosaving editor may write hundreds of times before anything is accepted,
// and those intermediate states have no reader. Once the newest version is
// accepted it is locked, and the change becomes the next version, itself a
// draft until the arranging document is accepted.
func reviseClause(tx *sql.Tx, id int64, heading, body string) (int64, int, error) {
	var status string
	var version int
	if err := tx.QueryRow(`SELECT status, version FROM clauses WHERE id = $1`, id).Scan(&status, &version); err != nil {
		return 0, 0, fmt.Errorf("read clause %d: %w", id, err)
	}
	if status == "draft" {
		if _, err := tx.Exec(
			`UPDATE clause_versions SET heading = $3, body = $4 WHERE clause_id = $1 AND version = $2`,
			id, version, heading, body); err != nil {
			return 0, 0, fmt.Errorf("rewrite clause %d v%d: %w", id, version, err)
		}
		if _, err := tx.Exec(`UPDATE clauses SET updated_at = now() WHERE id = $1`, id); err != nil {
			return 0, 0, fmt.Errorf("touch clause %d: %w", id, err)
		}
		return id, version, nil
	}
	if err := tx.QueryRow(
		`UPDATE clauses SET version = version + 1, status = 'draft', updated_at = now() WHERE id = $1 RETURNING version`,
		id).Scan(&version); err != nil {
		return 0, 0, fmt.Errorf("bump clause %d: %w", id, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO clause_versions (clause_id, version, heading, body) VALUES ($1, $2, $3, $4)`,
		id, version, heading, body); err != nil {
		return 0, 0, fmt.Errorf("insert clause %d v%d: %w", id, version, err)
	}
	return id, version, nil
}

// publishDocSections marks a document's sections published and its arranged
// clauses accepted. Accepting a document accepts every draft clause it
// arranges and writes nothing else (S11).
func publishDocSections(tx *sql.Tx, docID int64) error {
	if _, err := tx.Exec(`UPDATE doc_sections SET published = true WHERE doc_id = $1`, docID); err != nil {
		return fmt.Errorf("publish sections of doc %d: %w", docID, err)
	}
	return acceptDocClauses(tx, docID)
}

// acceptDocClauses accepts every draft clause a document's current
// arrangement holds (S11). Called both when a document is accepted and when
// ensureClauses backfills the arrangement of a document that was already
// accepted before the clause tables existed.
func acceptDocClauses(tx *sql.Tx, docID int64) error {
	if _, err := tx.Exec(
		`UPDATE clauses SET status = 'accepted', updated_at = now()
		  WHERE status = 'draft'
		    AND id IN (SELECT clause_id FROM doc_clauses WHERE doc_id = $1)`, docID); err != nil {
		return fmt.Errorf("accept clauses of doc %d: %w", docID, err)
	}
	return nil
}

// GetClause reads one clause by its project key and number, with the current
// version's text and every document whose arrangement holds it.
func (s *Store) GetClause(ctx context.Context, projectKey string, number int64) (*model.Clause, error) {
	c := &model.Clause{}
	err := s.db.QueryRowContext(ctx,
		`SELECT c.id, c.project_id, p.key, c.number, c.status, c.version,
		        cv.heading, cv.body, c.created_at, c.updated_at
		   FROM clauses c
		   JOIN projects p ON p.id = c.project_id
		   JOIN clause_versions cv ON cv.clause_id = c.id AND cv.version = c.version
		  WHERE p.key = $1 AND c.number = $2`, projectKey, number,
	).Scan(&c.ID, &c.Project, &c.ProjectKey, &c.Number, &c.Status, &c.Version,
		&c.Heading, &c.Body, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read clause %s-CL-%d: %w", projectKey, number, err)
	}
	c.Ref = fmt.Sprintf("%s-CL-%d", c.ProjectKey, c.Number)

	rows, err := s.db.QueryContext(ctx,
		`SELECT dc.doc_id, p.key, d.kind, d.number, dc.anchor, dc.position, dc.depth, dc.clause_version
		   FROM doc_clauses dc
		   JOIN docs d ON d.id = dc.doc_id
		   JOIN projects p ON p.id = d.project_id
		  WHERE dc.clause_id = $1
		  ORDER BY dc.doc_id, dc.position`, c.ID)
	if err != nil {
		return nil, fmt.Errorf("read arrangements of clause %d: %w", c.ID, err)
	}
	defer rows.Close()
	c.ArrangedIn = []model.ClauseArrangement{}
	for rows.Next() {
		var a model.ClauseArrangement
		var key, kind string
		var docNumber sql.NullInt64
		if err := rows.Scan(&a.Doc, &key, &kind, &docNumber, &a.Anchor, &a.Position, &a.Depth, &a.ClauseVersion); err != nil {
			return nil, fmt.Errorf("scan arrangement of clause %d: %w", c.ID, err)
		}
		a.DocRef = fmt.Sprintf("%s-%s-%d", key, strings.ToUpper(kind), docNumber.Int64)
		c.ArrangedIn = append(c.ArrangedIn, a)
	}
	return c, rows.Err()
}

// ClauseIDByRef resolves a clause's project key and number to its row id
// inside a transaction, or ErrNotFound.
func ClauseIDByRef(tx *sql.Tx, projectKey string, number int64) (int64, error) {
	var id int64
	err := tx.QueryRow(
		`SELECT c.id FROM clauses c JOIN projects p ON p.id = c.project_id
		  WHERE p.key = $1 AND c.number = $2`, projectKey, number).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("clause %s-CL-%d: %w", projectKey, number, ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("resolve clause %s-CL-%d: %w", projectKey, number, err)
	}
	return id, nil
}
