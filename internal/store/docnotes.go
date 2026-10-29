package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// AddDocNote leaves one anchored, non-blocking note on a document section
// (WL-REQ-171). It blocks nothing and changes nothing about the document: the
// row records a remark and the task, session and actor that raised it.
//
// The anchor must name a section the document currently has. An unanchored
// note is not a note — nobody reading the document would ever meet it — so a
// stray anchor is refused rather than stored and lost. That refusal covers
// plans by construction, since a plan carries no sections at all (WL-REQ-172);
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
	body := strings.TrimSpace(string(in.Body))
	if body == "" {
		return model.DocNote{}, fmt.Errorf("a note on doc %d needs a body: %w", docID, ErrInvalidInput)
	}
	anchor := strings.TrimPrefix(strings.TrimSpace(in.Anchor), "#")
	if anchor == "" {
		return model.DocNote{}, fmt.Errorf(
			"a note on doc %d needs a section anchor (WL-REQ-171): %w", docID, ErrInvalidInput)
	}
	if d.kind == "plan" {
		return model.DocNote{}, fmt.Errorf(
			"doc %d is a plan and has no sections to anchor a note to (WL-REQ-172): %w", docID, ErrInvalidInput)
	}
	// A note is a bare thread anchored to its section alone, so editing the
	// section never makes it obsolete (WL-REQ-1319, WL-REQ-1667).
	th, err := OpenThread(tx, now, 0, model.OpenThreadInput{
		Anchor: model.ReviewAnchor{Doc: docID, DocAnchor: anchor}, Intent: "note", Body: body,
	}, actorID, in.Task, in.Session, eventID)
	if err != nil {
		return model.DocNote{}, err
	}
	note := model.DocNote{
		ID: th.ID, Doc: docID, Anchor: anchor, Body: model.Markdown(body),
		Task: in.Task, Session: in.Session, CreatedBy: actorID, CreatedAt: th.CreatedAt,
	}
	if err := deriveDocNoteRefs(tx, note.ID); err != nil {
		return model.DocNote{}, err
	}
	if err := logDocChange(tx, docID, eventID,
		map[string]string{"field": "note", "new": anchor}); err != nil {
		return model.DocNote{}, err
	}
	return note, nil
}

// ListDocNotes returns a document's notes, the bare threads anchored to a
// section alone, in the order they were left, which is also the order they
// render under their sections. ThreadsForSubject returns notes and review
// threads together.
func (s *Store) ListDocNotes(ctx context.Context, docID int64) ([]model.DocNote, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.doc_id, t.doc_anchor, m.body,
		        coalesce(t.raised_by_task,''), coalesce(t.raised_by_session,''), t.author_id, t.created_at
		   FROM review_threads t JOIN review_messages m ON m.thread_id = t.id AND m.seq = 1
		  WHERE t.doc_id = $1 AND t.review_id IS NULL AND t.version IS NULL AND t.doc_anchor IS NOT NULL
		  ORDER BY t.id`, docID)
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
