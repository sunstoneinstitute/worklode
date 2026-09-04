package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// addDocNote runs AddDocNote through RecordDocEvent, the way the API does.
func addDocNote(t *testing.T, s *Store, docID int64, in model.AddDocNoteInput, actor string) (model.DocNote, error) {
	t.Helper()
	var out model.DocNote
	_, _, err := s.RecordDocEvent(t.Context(), "note", "cli",
		fmt.Sprintf("doc-note-%d", docEventSeq.Add(1)), "doc.note_added", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			out, err = AddDocNote(tx, s.Now(), docID, in, actor, eventID)
			return err
		})
	return out, err
}

// seedDocNoteTask inserts a task in the doc fixtures' project, so a note can
// name the task that raised it (doc_notes.task_id is a foreign key).
func seedDocNoteTask(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO tasks (id, project_id, title, priority, kind, state, created_at, updated_at)
		 VALUES ($1, 'p1', 'T', 'medium', 'feature', 'ready', now(), now())`, id); err != nil {
		t.Fatal(err)
	}
}

// TestDocNoteAnchored covers 025 §8.5's whole surface: a note lands against a
// section the document actually has, carrying the task, session and actor that
// raised it; an anchor the document does not have, an empty body, and a plan
// (which has no sections at all) are each refused.
func TestDocNoteAnchored(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	seedDocNoteTask(t, s, "P1-1")
	spec := mustAcceptedSpec(t, s, "025-noted")

	note, err := addDocNote(t, s, spec.ID, model.AddDocNoteInput{
		Anchor: "sec-2", Body: "this contradicts §1", Task: "P1-1", Session: "sess-1",
	}, "stig")
	if err != nil {
		t.Fatalf("AddDocNote: %v", err)
	}
	if note.ID == 0 || note.Doc != spec.ID {
		t.Errorf("note = %+v, want a non-zero id on doc %d", note, spec.ID)
	}

	notes, err := s.ListDocNotes(t.Context(), spec.ID)
	if err != nil {
		t.Fatalf("ListDocNotes: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %+v, want exactly one", notes)
	}
	got := notes[0]
	if got.Anchor != "sec-2" || got.Body != "this contradicts §1" ||
		got.Task != "P1-1" || got.Session != "sess-1" || got.CreatedBy != "stig" {
		t.Errorf("note = %+v, want sec-2/P1-1/sess-1/stig", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("note.CreatedAt is zero")
	}

	if _, err := addDocNote(t, s, spec.ID, model.AddDocNoteInput{
		Anchor: "sec-99", Body: "nowhere",
	}, "stig"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("note on sec-99 err = %v, want ErrInvalidInput", err)
	}
	if _, err := addDocNote(t, s, spec.ID, model.AddDocNoteInput{
		Anchor: "sec-2", Body: "   ",
	}, "stig"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("note with an empty body err = %v, want ErrInvalidInput", err)
	}

	// A plan carries no sections (025 §9), so no anchor on it can be real.
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "noted-plan", Body: planMintBody, CreatedBy: "stig",
	})
	if _, err := addDocNote(t, s, plan.ID, model.AddDocNoteInput{
		Anchor: "sec-1", Body: "on a plan",
	}, "stig"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("note on a plan err = %v, want ErrInvalidInput", err)
	}

	// The server-side --has-notes filter selects exactly the noted document.
	noted, err := s.ListDocs(t.Context(), DocFilter{HasNotes: true})
	if err != nil {
		t.Fatalf("ListDocs(HasNotes): %v", err)
	}
	if len(noted) != 1 || noted[0].ID != spec.ID {
		t.Fatalf("ListDocs(HasNotes) = %+v, want only doc %d", noted, spec.ID)
	}
}
