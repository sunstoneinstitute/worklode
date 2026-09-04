package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// AddDocNote leaves one anchored, non-blocking note on a document section
// (025 §8.5). It blocks nothing and changes nothing about the document: the
// row records a remark and the task, session and actor that raised it.
//
// The anchor must name a section the document currently has. An unanchored
// note is not a note — nobody reading the document would ever meet it — so a
// stray anchor is refused rather than stored and lost. That refusal covers
// plans by construction, since a plan carries no sections at all (025 §9);
// it gets its own message because "no section sec-1" would read as a typo
// rather than as a category error.
//
// An empty body is refused for the same reason: an anchor with nothing said
// at it is noise on every reader's render of that section.
func AddDocNote(
	tx *sql.Tx, now time.Time, docID int64,
	in model.AddDocNoteInput, actorID string, eventID int64,
) (model.DocNote, error) {
	d, err := lockDoc(tx, docID)
	if err != nil {
		return model.DocNote{}, err
	}
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return model.DocNote{}, fmt.Errorf("a note on doc %d needs a body: %w", docID, ErrInvalidInput)
	}
	anchor := strings.TrimPrefix(strings.TrimSpace(in.Anchor), "#")
	if anchor == "" {
		return model.DocNote{}, fmt.Errorf(
			"a note on doc %d needs a section anchor (025 §8.5): %w", docID, ErrInvalidInput)
	}
	if d.kind == "plan" {
		return model.DocNote{}, fmt.Errorf(
			"doc %d is a plan and has no sections to anchor a note to (025 §9): %w", docID, ErrInvalidInput)
	}
	var exists bool
	err = tx.QueryRow(
		`SELECT true FROM doc_sections WHERE doc_id = $1 AND anchor = $2`, docID, anchor).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DocNote{}, fmt.Errorf(
			"doc %d has no section #%s to anchor a note to: %w", docID, anchor, ErrInvalidInput)
	}
	if err != nil {
		return model.DocNote{}, fmt.Errorf("check section #%s of doc %d: %w", anchor, docID, err)
	}

	note := model.DocNote{
		Doc: docID, Anchor: anchor, Body: body,
		Task: in.Task, Session: in.Session, CreatedBy: actorID,
		CreatedAt: now.UTC().Truncate(time.Second),
	}
	if err := tx.QueryRow(
		`INSERT INTO doc_notes (doc_id, anchor, body, task_id, session_id, created_by, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		docID, anchor, body, nullText(in.Task), nullText(in.Session), nullText(actorID), note.CreatedAt,
	).Scan(&note.ID); err != nil {
		return model.DocNote{}, fmt.Errorf("add note on doc %d #%s: %w", docID, anchor, err)
	}
	if err := logDocChange(tx, docID, eventID,
		map[string]string{"field": "note", "new": anchor}); err != nil {
		return model.DocNote{}, err
	}
	return note, nil
}

// ListDocNotes returns a document's notes in the order they were left, which
// is also the order they render under their sections.
func (s *Store) ListDocNotes(ctx context.Context, docID int64) ([]model.DocNote, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, doc_id, anchor, body,
		        coalesce(task_id,''), coalesce(session_id,''), coalesce(created_by,''), created_at
		   FROM doc_notes WHERE doc_id = $1 ORDER BY id`, docID)
	if err != nil {
		return nil, fmt.Errorf("list notes on doc %d: %w", docID, err)
	}
	return collectRows(rows, fmt.Sprintf("list notes on doc %d", docID),
		func(r rowScanner) (model.DocNote, error) {
			var n model.DocNote
			if err := r.Scan(&n.ID, &n.Doc, &n.Anchor, &n.Body,
				&n.Task, &n.Session, &n.CreatedBy, &n.CreatedAt); err != nil {
				return model.DocNote{}, err
			}
			n.CreatedAt = n.CreatedAt.UTC()
			return n, nil
		})
}
