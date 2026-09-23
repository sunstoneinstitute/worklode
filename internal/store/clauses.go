package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

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
	// changed collects the clauses this write inserted or revised, so their
	// references can be derived once every doc_clauses row below has been
	// written. Deriving inline in this loop would miss a ref into the same
	// document: the section it names has no arrangement row yet (forward) or
	// its own derivation already ran (backward), so it would resolve through
	// an empty or stale doc_clauses and drop the edge every time (S26).
	type changedClause struct {
		id   int64
		text string
	}
	var changed []changedClause
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
		if m == nil || m.heading != sec.Title || m.body != sec.Body {
			changed = append(changed, changedClause{id, sec.Title + "\n" + sec.Body})
		}
		if _, err := tx.Exec(
			`INSERT INTO doc_clauses (doc_id, position, clause_id, clause_version, depth, anchor)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			docID, position, id, version, sec.Level, sec.Anchor); err != nil {
			return fmt.Errorf("arrange clause %d in doc %d: %w", id, docID, err)
		}
		position++
	}
	for _, c := range changed {
		if err := deriveReferences(tx, project, c.id, c.text); err != nil {
			return err
		}
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
// accepted before the clause tables existed. Guarded by d.kind <> 'plan':
// a plan's doc_clauses rows are another document's clauses (increment 3
// R1), so accepting a plan must never flip them to accepted regardless of
// which caller reaches here or in what order.
func acceptDocClauses(tx *sql.Tx, docID int64) error {
	if _, err := tx.Exec(
		`UPDATE clauses SET status = 'accepted', updated_at = now()
		  WHERE status = 'draft'
		    AND id IN (
		      SELECT dc.clause_id FROM doc_clauses dc JOIN docs d ON d.id = dc.doc_id
		       WHERE dc.doc_id = $1 AND d.kind <> 'plan')`, docID); err != nil {
		return fmt.Errorf("accept clauses of doc %d: %w", docID, err)
	}
	return nil
}

// GetClause reads one clause by its project key and number, with the current
// version's text and every document whose arrangement holds it.
func (s *Store) GetClause(ctx context.Context, projectKey string, number int64) (*model.Clause, error) {
	c := &model.Clause{}
	var owner sql.NullString
	var tagsRaw string
	err := s.db.QueryRowContext(ctx,
		`SELECT c.id, c.project_id, p.key, c.number, c.status, c.version,
		        cv.heading, cv.body, c.owner, c.tags, c.created_at, c.updated_at
		   FROM clauses c
		   JOIN projects p ON p.id = c.project_id
		   JOIN clause_versions cv ON cv.clause_id = c.id AND cv.version = c.version
		  WHERE p.key = $1 AND c.number = $2`, projectKey, number,
	).Scan(&c.ID, &c.Project, &c.ProjectKey, &c.Number, &c.Status, &c.Version,
		&c.Heading, &c.Body, &owner, &tagsRaw, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read clause %s-CL-%d: %w", projectKey, number, err)
	}
	c.Owner = owner.String
	tags, err := scanTextArray(tagsRaw)
	if err != nil {
		return nil, fmt.Errorf("scan tags of clause %d: %w", c.ID, err)
	}
	c.Tags = nonNil(tags)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}

	c.GovernedTasks = []model.ClauseTask{}
	trows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.title, t.state, g.source, g.clause_version
		   FROM task_governed_by g JOIN tasks t ON t.id = g.task_id
		  WHERE g.clause_id = $1
		  ORDER BY g.created_at DESC, t.id`, c.ID)
	if err != nil {
		return nil, fmt.Errorf("read governed tasks of clause %d: %w", c.ID, err)
	}
	defer trows.Close()
	for trows.Next() {
		var gt model.ClauseTask
		if err := trows.Scan(&gt.ID, &gt.Title, &gt.State, &gt.Source, &gt.ClauseVersion); err != nil {
			return nil, fmt.Errorf("scan governed task of clause %d: %w", c.ID, err)
		}
		c.GovernedTasks = append(c.GovernedTasks, gt)
	}
	if err := trows.Err(); err != nil {
		return nil, err
	}
	erows, err := s.db.QueryContext(ctx, clauseEdgesSQL, c.ID)
	if err != nil {
		return nil, fmt.Errorf("read edges of clause %d: %w", c.ID, err)
	}
	defer erows.Close()
	if c.Edges, err = scanClauseEdges(erows); err != nil {
		return nil, err
	}
	return c, nil
}

// ListClauseVersions is a clause's version history, newest first.
func (s *Store) ListClauseVersions(ctx context.Context, projectKey string, number int64) ([]model.ClauseVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT cv.version, cv.heading, cv.created_at
		   FROM clause_versions cv
		   JOIN clauses c ON c.id = cv.clause_id
		   JOIN projects p ON p.id = c.project_id
		  WHERE p.key = $1 AND c.number = $2
		  ORDER BY cv.version DESC`, projectKey, number)
	if err != nil {
		return nil, fmt.Errorf("list versions of clause %s-CL-%d: %w", projectKey, number, err)
	}
	defer rows.Close()
	out := []model.ClauseVersion{}
	for rows.Next() {
		var v model.ClauseVersion
		if err := rows.Scan(&v.Version, &v.Heading, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan version of clause %s-CL-%d: %w", projectKey, number, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("clause %s-CL-%d: %w", projectKey, number, ErrNotFound)
	}
	return out, nil
}

// GetClauseVersion reads a clause as it stood at one version: the detail
// with Version, Heading and Body taken from that version's row. ArrangedIn
// and GovernedTasks are the current ones. Edges are unversioned too and
// always reflect the neighbours' current headings (S26), so an old version's
// edges are not what stood when that version was written.
func (s *Store) GetClauseVersion(ctx context.Context, projectKey string, number int64, version int) (*model.Clause, error) {
	c, err := s.GetClause(ctx, projectKey, number)
	if err != nil {
		return nil, err
	}
	err = s.db.QueryRowContext(ctx,
		`SELECT heading, body FROM clause_versions WHERE clause_id = $1 AND version = $2`,
		c.ID, version).Scan(&c.Heading, &c.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("clause %s v%d: %w", c.Ref, version, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read clause %s v%d: %w", c.Ref, version, err)
	}
	c.Version = version
	return c, nil
}

// EditClause writes a clause's new heading and body by regenerating the
// arranging document's body around it (12-spec-refactoring-design-tree.md
// S13, S14, S35). A draft document is written through UpdateDocBody, so
// syncClauses rewrites the clause's draft version in place. An accepted
// document is written through its candidate revision, opened here when none
// is open; the clause's next version appears when the revision lands. A
// plan arrangement only references a clause (increment 3 R2), so its
// doc_clauses rows are excluded here: the clause writes through the spec or
// ADR that arranges it, never through a covering plan. A clause arranged in
// no spec or ADR, or in more than one, is refused: editing is document-first
// in this stage. Returns the arranging document's id.
func EditClause(tx *sql.Tx, now time.Time, projectKey string, number int64, in model.EditClauseInput, actorID string, eventID int64) (int64, error) {
	if strings.TrimSpace(in.Heading) == "" {
		return 0, fmt.Errorf("clause heading is required: %w", ErrInvalidInput)
	}
	clauseID, err := ClauseIDByRef(tx, projectKey, number)
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(
		`SELECT dc.doc_id, dc.anchor FROM doc_clauses dc JOIN docs d ON d.id = dc.doc_id
		  WHERE dc.clause_id = $1 AND d.kind <> 'plan' AND d.deleted_at IS NULL
		  ORDER BY dc.doc_id`, clauseID)
	if err != nil {
		return 0, fmt.Errorf("read arrangements of clause %d: %w", clauseID, err)
	}
	var docIDs []int64
	var anchors []string
	for rows.Next() {
		var id int64
		var anchor string
		if err := rows.Scan(&id, &anchor); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan arrangement of clause %d: %w", clauseID, err)
		}
		docIDs, anchors = append(docIDs, id), append(anchors, anchor)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read arrangements of clause %d: %w", clauseID, err)
	}
	switch len(docIDs) {
	case 0:
		return 0, fmt.Errorf("clause %s-CL-%d is arranged in no spec or ADR; edit the document instead: %w", projectKey, number, ErrInvalidInput)
	case 1:
	default:
		return 0, fmt.Errorf("clause %s-CL-%d is arranged in %d specs or ADRs; editing a clause shared between them is not supported: %w", projectKey, number, len(docIDs), ErrInvalidInput)
	}
	docID, anchor := docIDs[0], anchors[0]

	var kind, status, body string
	if err := tx.QueryRow(`SELECT kind, status, body FROM docs WHERE id = $1 FOR UPDATE`, docID).Scan(&kind, &status, &body); err != nil {
		return 0, fmt.Errorf("load doc %d: %w", docID, err)
	}
	editable := body
	revising := kind != "plan" && status != "draft"
	if revising {
		var candidate sql.NullString
		if err := tx.QueryRow(`SELECT body FROM doc_revisions WHERE doc_id = $1`, docID).Scan(&candidate); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("load revision of doc %d: %w", docID, err)
		}
		if candidate.Valid {
			editable = candidate.String
		} else if err := ReviseDoc(tx, now, docID, actorID, eventID); err != nil {
			return 0, err
		}
	}
	parsed, err := designdoc.Parse([]byte(editable))
	if err != nil {
		return 0, fmt.Errorf("parse doc %d: %w", docID, err)
	}
	sec := parsed.SectionByAnchor(anchor)
	if sec == nil {
		return 0, fmt.Errorf("clause %s-CL-%d: its section %s is not in the editable body of doc %d: %w", projectKey, number, anchor, docID, ErrInvalidInput)
	}
	sec.Title = in.Heading
	sec.Body = sectionBody(in.Body, sec.Index == len(parsed.Sections)-1)
	next := string(parsed.Bytes())
	if revising {
		return docID, UpdateRevision(tx, now, docID, next, eventID)
	}
	_, err = UpdateDocBody(tx, now, docID, next, 0, eventID)
	return docID, err
}

// sectionBody normalises a submitted clause body to the shape designdoc.Parse
// would have produced for it, so re-parsing the regenerated document yields
// the sections and anchors it had before. Document.Bytes writes the heading
// line and the body with nothing between them, so a body that does not start
// on a fresh line after the heading, or does not end in one, swallows the
// next heading and orphans that clause. A parsed body carries a blank line
// before the next heading; the last section's runs to EOF and ends in a
// single newline, so a trailing blank line is added only when a heading
// follows.
func sectionBody(body string, last bool) string {
	body = "\n" + strings.Trim(body, "\r\n") + "\n"
	if !last {
		body += "\n"
	}
	return body
}

// textArrayMap decodes a text[] column's raw Postgres array literal (e.g.
// "{storage,search}") into a []string. pgx's stdlib database/sql driver
// hands back that literal as a plain string rather than converting it, so
// reading a native array column needs this one explicit decode step; no
// existing helper in the package does it, because every other []string
// column here (project focus, actor groups) is stored as jsonb instead.
var textArrayMap = pgtype.NewMap()

// scanTextArray decodes raw as read from a text[] column. An empty literal
// yields nil; clauses.tags is NOT NULL so "" cannot occur.
func scanTextArray(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var out []string
	if err := textArrayMap.Scan(pgtype.TextArrayOID, pgtype.TextFormatCode, []byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode text[] literal %q: %w", raw, err)
	}
	return out, nil
}

// deref returns the pointer's value, or T's zero value for nil. SetClauseMeta
// pairs it with a boolean flag per field, so the zero value it returns for a
// nil field is never actually written: nullif on the empty string, or a
// false CASE branch, discards it.
func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// SetClauseMeta sets a clause's owner and tags (S15). A nil field is left
// alone. Dates never live on a clause; they reach it through its tasks.
func SetClauseMeta(tx *sql.Tx, clauseID int64, in model.ClauseMetaInput) error {
	if in.Owner == nil && in.Tags == nil {
		return fmt.Errorf("nothing to set: %w", ErrInvalidInput)
	}
	res, err := tx.Exec(
		`UPDATE clauses
		    SET owner = CASE WHEN $2::boolean THEN nullif($3, '') ELSE owner END,
		        tags  = CASE WHEN $4::boolean THEN $5::text[] ELSE tags END,
		        updated_at = now()
		  WHERE id = $1`,
		clauseID, in.Owner != nil, deref(in.Owner), in.Tags != nil, deref(in.Tags))
	if err != nil {
		return fmt.Errorf("set meta of clause %d: %w", clauseID, err)
	}
	return requireOneAffected(res, fmt.Sprintf("clause %d", clauseID), ErrNotFound)
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

// clauseStatuses mirrors the clauses.status CHECK in migration 0082.
var clauseStatuses = map[string]bool{"draft": true, "accepted": true, "superseded": true, "withdrawn": true}

// SetClauseStatus is the one writer of a clause's status outside document
// acceptance (increment 3 R7). Withdrawing a clause marks every accepted plan
// arranging it stale (S23): the plan's frozen arrangement names text that no
// longer holds. Increment 4's split and merge lineage is the caller that
// withdraws. The clause row lock is NO KEY UPDATE so it does not wait on a
// concurrent Govern's FK KEY SHARE (see resolveSupersede).
func SetClauseStatus(tx *sql.Tx, now time.Time, clauseID int64, status string, eventID int64) error {
	if !clauseStatuses[status] {
		return fmt.Errorf("clause status %q: %w", status, ErrInvalidInput)
	}
	var old, key string
	var number int64
	err := tx.QueryRow(
		`SELECT c.status, p.key, c.number FROM clauses c JOIN projects p ON p.id = c.project_id
		  WHERE c.id = $1 FOR NO KEY UPDATE OF c`, clauseID).Scan(&old, &key, &number)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("clause %d: %w", clauseID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("read clause %d: %w", clauseID, err)
	}
	if old == status {
		return nil
	}
	if _, err := tx.Exec(`UPDATE clauses SET status = $2, updated_at = $3 WHERE id = $1`, clauseID, status, now.UTC()); err != nil {
		return fmt.Errorf("set clause %d status: %w", clauseID, err)
	}
	if status != "withdrawn" {
		return nil
	}
	rows, err := tx.Query(
		`SELECT DISTINCT d.id FROM doc_clauses dc JOIN docs d ON d.id = dc.doc_id
		  WHERE dc.clause_id = $1 AND d.kind = 'plan' AND d.status = 'accepted' AND d.deleted_at IS NULL`, clauseID)
	if err != nil {
		return fmt.Errorf("plans arranging clause %d: %w", clauseID, err)
	}
	var plans []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		plans = append(plans, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	ref := fmt.Sprintf("%s-CL-%d", key, number)
	_, err = MarkPlansStale(tx, now, plans, "clause_withdrawn", ref, nil, eventID)
	return err
}
