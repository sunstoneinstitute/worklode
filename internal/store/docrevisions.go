package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// ReviseDoc opens a candidate revision against an accepted spec or ADR: a copy
// of the current body to edit while the accepted version stays authoritative
// (025 §7.2). One candidate at a time.
//
// Plans are edited in place with UpdateDocBody (025 §9) and drafts are edited
// in place because there is nothing to revise against; both are
// ErrInvalidInput.
func ReviseDoc(tx *sql.Tx, now time.Time, id int64, actorID string, eventID int64) error {
	d, err := lockDoc(tx, id)
	if err != nil {
		return err
	}
	if d.kind == "plan" {
		return fmt.Errorf("doc %d is a plan: plans are edited in place (025 §9): %w", id, ErrInvalidInput)
	}
	if d.status != "accepted" {
		return fmt.Errorf("doc %d is %s: only an accepted document is revised (025 §7.2): %w",
			id, d.status, ErrInvalidInput)
	}

	if _, err := tx.Exec(
		`INSERT INTO doc_revisions (doc_id, body, created_by, created_at) VALUES ($1, $2, $3, $4)`,
		id, d.body, nullText(actorID), now.UTC().Truncate(time.Second),
	); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("doc %d already has an open revision: %w", id, ErrRevisionExists)
		}
		return fmt.Errorf("open revision of doc %d: %w", id, err)
	}
	return logDocChange(tx, id, eventID,
		map[string]string{"field": "revision", "new": "open"})
}

// UpdateRevision replaces the body of a document's open candidate revision.
// The body is parsed and linted here so a malformed candidate is refused at
// the edit rather than at the accept gate. ErrNotFound if no revision is open.
//
// The document's status is rechecked, not only its revision: a document
// superseded since the revision opened has nothing left to land, and saying so
// at the edit beats a confusing refusal at the accept gate.
func UpdateRevision(tx *sql.Tx, now time.Time, id int64, body string, eventID int64) error {
	d, err := lockDoc(tx, id)
	if err != nil {
		return err
	}
	if d.status != "accepted" {
		return fmt.Errorf("doc %d is %s: only an accepted document has a revision to edit: %w",
			id, d.status, ErrInvalidInput)
	}
	if _, err := parseDocBody(d.kind, body); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE doc_revisions SET body = $2 WHERE doc_id = $1`, id, body)
	if err != nil {
		return fmt.Errorf("update revision of doc %d: %w", id, err)
	}
	if err := requireOneAffected(res, fmt.Sprintf("update revision of doc %d", id),
		fmt.Errorf("doc %d has no open revision: %w", id, ErrNotFound)); err != nil {
		return err
	}
	return logDocChange(tx, id, eventID,
		map[string]string{"field": "revision", "new": "updated"})
}

// DiscardRevision withdraws a document's open candidate revision without
// landing it — the close-without-merging half of the pull request 025 §7.2
// says a revision structurally is. Deleting the row frees the
// one-candidate-per-document slot, so the next ReviseDoc succeeds immediately
// instead of hitting ErrRevisionExists. ErrNotFound if no revision is open.
//
// Gated on the document's assignee or the revision's created_by: anyone with
// doc.write may propose a revision, and either its author or the document's
// assignee may withdraw it. That pairing is what keeps ReviseDoc open — an
// unwanted candidate can always be cleared by someone.
//
// Unlike AcceptRevision this checks no status: a candidate left behind on a
// document that has since been superseded is exactly the litter discard
// exists to remove.
//
// Nothing is stamped, so now goes unused; the signature matches the other
// document writers, as ReplaceDocEdges' does.
func DiscardRevision(tx *sql.Tx, _ time.Time, id int64, actorID string, eventID int64) (*model.Doc, error) {
	d, err := lockDoc(tx, id)
	if err != nil {
		return nil, err
	}
	// The revision is read before the gate because the gate depends on its
	// created_by. Nothing is disclosed by that ordering: whether a candidate
	// is open is already on the detail endpoint for any doc.read holder.
	var createdBy sql.NullString
	var body string
	err = tx.QueryRow(
		`SELECT created_by, body FROM doc_revisions WHERE doc_id = $1 FOR UPDATE`, id).
		Scan(&createdBy, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("doc %d has no open revision: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load revision of doc %d: %w", id, err)
	}
	if err := checkRevisionDiscarder(id, d.assignee, createdBy.String, actorID); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(`DELETE FROM doc_revisions WHERE doc_id = $1`, id); err != nil {
		return nil, fmt.Errorf("discard revision of doc %d: %w", id, err)
	}
	// The one state_log row in this file that carries a body, because this is
	// the one verb after which the text is nowhere else: doc_revisions has no
	// history and the delete is hard, the docs row never held a candidate, and
	// the request that asked for the discard names no body to record. An
	// accepted body stays on the document; an edited one is in the update's
	// own event payload.
	if err := logDocChange(tx, id, eventID, map[string]string{
		"field": "revision", "new": "discarded", "discarded_body": body,
	}); err != nil {
		return nil, err
	}
	return getDocTx(tx, id)
}

// AcceptRevision lands a document's open candidate revision: it runs the
// 025 §6 constraint check against the accepted version and, when clean, swaps
// the body, bumps the version, rebuilds sections and edges, stamps
// last_revised_in on exactly the changed anchors, publishes every anchor the
// new version carries, applies any new document-level replaces edges, and
// consumes the candidate — one transaction, assignee-gated like AcceptDoc.
//
// The append-only rule protects the anchors the accepted version *published*
// (025 §7.2), so a never-published row that disappears is legal; renumbering
// and excess depth are violations regardless.
func AcceptRevision(tx *sql.Tx, now time.Time, id int64, actorID string, eventID int64) (*model.Doc, error) {
	d, err := lockDoc(tx, id)
	if err != nil {
		return nil, err
	}
	if err := checkDocAssignee(id, d.assignee, actorID); err != nil {
		return nil, err
	}
	if d.kind == "plan" {
		return nil, fmt.Errorf("doc %d is a plan: plans are edited in place (025 §9): %w", id, ErrInvalidInput)
	}
	if d.status != "accepted" {
		return nil, fmt.Errorf("doc %d is %s: only an accepted document has a revision to land: %w",
			id, d.status, ErrInvalidInput)
	}

	var candidateBody string
	err = tx.QueryRow(
		`SELECT body FROM doc_revisions WHERE doc_id = $1 FOR UPDATE`, id).Scan(&candidateBody)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("doc %d has no open revision: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load revision of doc %d: %w", id, err)
	}

	accepted, err := parseDocBody(d.kind, d.body)
	if err != nil {
		return nil, fmt.Errorf("parse the accepted body of doc %d: %w", id, err)
	}
	candidate, err := parseDocBody(d.kind, candidateBody)
	if err != nil {
		return nil, err
	}

	prior, err := priorSections(tx, id)
	if err != nil {
		return nil, err
	}
	diff := designdoc.CompareSections(accepted.doc, candidate.doc, designdoc.DepthLimit)
	// Removed is filtered down to the published anchors before Violations
	// renders it, so the text stays in one place and an unpublished removal
	// raises nothing.
	removed := diff.Removed[:0:0]
	for _, anchor := range diff.Removed {
		if prior[anchor].published {
			removed = append(removed, anchor)
		}
	}
	diff.Removed = removed
	if v := diff.Violations(); len(v) > 0 {
		return nil, fmt.Errorf("revision of doc %d cannot be accepted: %s: %w",
			id, strings.Join(v, "; "), ErrInvalidInput)
	}

	version := d.version + 1
	title, ok := designdoc.Title(candidate.doc)
	if !ok {
		title = d.slug
	}
	ts := now.UTC().Truncate(time.Second)
	// Snapshot the version this accept is about to overwrite (025 §4.5):
	// docs still holds its pre-update body, title and issued here, before the
	// UPDATE below runs.
	if err := snapshotDocVersion(tx, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(
		`UPDATE docs SET body = $2, title = $3, issued = coalesce($4::date, issued),
		                 version = $5, updated_at = $6
		  WHERE id = $1`,
		id, candidateBody, title, nullText(candidate.issued), version, ts,
	); err != nil {
		return nil, fmt.Errorf("land revision of doc %d: %w", id, err)
	}
	after, err := rebuildSectionsFrom(tx, id, d.kind, candidate.doc, version, prior)
	if err != nil {
		return nil, err
	}
	if err := rebuildEdges(tx, now, id, d.kind, d.project, candidate.doc.Frontmatter); err != nil {
		return nil, err
	}

	// The diff gate is what keeps a published anchor from being dropped; this
	// checks that it did. A failure here is a bug in the gate, not bad input,
	// so it carries no sentinel.
	for anchor, p := range prior {
		if _, still := after[anchor]; p.published && !still {
			return nil, fmt.Errorf(
				"internal: doc %d lost published anchor #%s in the section rebuild", id, anchor)
		}
	}

	// 025 §6 rule 5: last_revised_in moves on exactly the sections whose
	// content changed. Touching it elsewhere invalidates valid claims.
	if len(diff.Changed) > 0 {
		if _, err := tx.Exec(
			`UPDATE doc_sections SET last_revised_in = $3
			  WHERE doc_id = $1 AND anchor = ANY($2::text[])`,
			id, diff.Changed, version,
		); err != nil {
			return nil, fmt.Errorf("stamp last_revised_in on doc %d: %w", id, err)
		}
	}
	if _, err := tx.Exec(
		`UPDATE doc_sections SET published = true WHERE doc_id = $1`, id); err != nil {
		return nil, fmt.Errorf("publish sections of doc %d: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM doc_revisions WHERE doc_id = $1`, id); err != nil {
		return nil, fmt.Errorf("consume revision of doc %d: %w", id, err)
	}
	if err := supersedeReplacedDocs(tx, ts, id, eventID); err != nil {
		return nil, err
	}
	if err := logDocChange(tx, id, eventID,
		map[string]string{
			"field": "version",
			"old":   strconv.Itoa(d.version),
			"new":   strconv.Itoa(version),
		}); err != nil {
		return nil, err
	}
	return getDocTx(tx, id)
}

// GetDocRevision returns a document's open candidate revision, or ErrNotFound
// when none is open.
func (s *Store) GetDocRevision(ctx context.Context, id int64) (*model.DocRevision, error) {
	var r model.DocRevision
	var createdBy sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT doc_id, body, created_by, created_at FROM doc_revisions WHERE doc_id = $1`, id,
	).Scan(&r.Doc, &r.Body, &createdBy, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("doc %d has no open revision: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get revision of doc %d: %w", id, err)
	}
	r.CreatedBy = createdBy.String
	r.CreatedAt = r.CreatedAt.UTC()
	return &r, nil
}

// checkRevisionDiscarder gates withdrawing an open candidate on the document's
// assignee or the revision's author. Wider than checkDocAssignee on purpose:
// accepting is the maintainer's act, but closing a proposal without merging it
// is also the proposer's, which is what lets ReviseDoc stay open to any
// doc.write holder (025 §7.2's pull-request analogy).
//
// An empty actorID matches nobody, including a revision or document whose own
// column is empty.
func checkRevisionDiscarder(id int64, assignee, createdBy, actorID string) error {
	if actorID != "" && (actorID == assignee || actorID == createdBy) {
		return nil
	}
	return fmt.Errorf(
		"revision of doc %d was opened by %q and the doc is assigned to %q: %q may discard neither: %w",
		id, createdBy, assignee, actorID, ErrForbidden)
}
