package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
)

// TestAuthoringTaskClosesOnSubmission pins WL-REQ-1629 for a design task
// whose only output is documents: with its draft unsubmitted it cannot reach
// in_review, and submitting the draft closes it without a PR.
func TestAuthoringTaskClosesOnSubmission(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	ctx := t.Context()
	seedDocsTask(t, s, "P1-1")
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-x",
		Body: specBody, CreatedBy: "stig", GeneratedByTask: "P1-1",
	})

	toReview := func(taskID string) error {
		_, _, err := s.RecordEvent(ctx, "cli", "review-"+taskID, "task.updated", []byte(`{}`),
			func(tx *sql.Tx, eventID int64) error {
				return Transition(tx, s.Now(), taskID, "in_progress", "in_review", eventID)
			})
		return err
	}
	if err := toReview("P1-1"); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("in_review with an unsubmitted draft: err = %v, want ErrBadTransition", err)
	}

	submit := func(externalID string, apply func(tx *sql.Tx, eventID int64) error) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"wl:subject": DocIRI(*doc)})
		if _, _, err := s.RecordEvent(ctx, "cli", externalID, typeDocumentSubmitted, payload, apply); err != nil {
			t.Fatalf("record submission: %v", err)
		}
	}
	submit("submit-1", func(tx *sql.Tx, eventID int64) error {
		return CloseAuthoringTask(tx, s.Now(), doc.ID, eventID)
	})
	got, err := s.GetTask(ctx, "P1-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "merged" || !got.Closed {
		t.Fatalf("after submit: state = %s closed = %v, want merged and closed", got.State, got.Closed)
	}

	// A design task still open with a submitted document (submitted before
	// the close existed) may still be submitted for review.
	seedDocsTask(t, s, "P1-2")
	if _, err := s.db.ExecContext(ctx, `UPDATE docs SET generated_by_task = 'P1-2' WHERE id = $1`, doc.ID); err != nil {
		t.Fatal(err)
	}
	if err := toReview("P1-2"); err != nil {
		t.Fatalf("in_review with a submitted document: %v", err)
	}
}
