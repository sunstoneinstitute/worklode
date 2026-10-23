package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// refTargets reads the targets of one field's rows, all versions, as
// "kind:id@version".
func refTargets(t *testing.T, s *Store, kind model.NodeKind, id, field string) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT to_kind || ':' || to_id || '@' || coalesce(from_version::text, '-') FROM inline_refs
		  WHERE from_kind = $1 AND from_id = $2 AND field = $3 ORDER BY 1`, kind, id, field)
	if err != nil {
		t.Fatal(err)
	}
	out, err := scanColumn[string](rows, "inline refs")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestMarkdownWritesDeriveInlineRefs edits each WL-RULE-1850 field once and
// checks the write stored its inline reference in the same transaction
// (WL-REQ-1852). Rule heading and body are TestRuleWritesDeriveInlineRefs'.
func TestMarkdownWritesDeriveInlineRefs(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	seedDocNoteTask(t, s, "P1-7")
	const target = "task:P1-7"
	tx := func(f func(tx *sql.Tx) error) {
		t.Helper()
		if err := s.Tx(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	docEvent := func(f func(tx *sql.Tx, eventID int64) error) {
		t.Helper()
		if _, _, err := s.RecordDocEvent(ctx, "edit", "cli",
			fmt.Sprintf("md-%d", docEventSeq.Add(1)), "doc.edit", nil, f); err != nil {
			t.Fatal(err)
		}
	}
	check := func(kind model.NodeKind, id, field string, want ...string) {
		t.Helper()
		if got := refTargets(t, s, kind, id, field); !slices.Equal(got, want) {
			t.Errorf("%s %s %s: got %q, want %q", kind, id, field, got, want)
		}
	}

	// Task title and body: created, then edited.
	var task *model.Task
	docEvent(func(tx *sql.Tx, eventID int64) error {
		var err error
		task, err = CreateTask(tx, s.Now(), TaskInput{ProjectID: "p1", Title: "Do P1-7",
			Body: "After P1-7.", Priority: "medium", Kind: "feature"}, eventID)
		return err
	})
	check("task", task.ID, "title", target+"@-")
	check("task", task.ID, "body", target+"@-")
	title, body := model.Markdown("Do it"), model.Markdown("Then P1-7")
	tx(func(tx *sql.Tx) error {
		return UpdateTaskFields(tx, s.Now(), task.ID, &title, &body, nil, nil, nil, nil, nil, nil, nil, nil)
	})
	check("task", task.ID, "title")
	check("task", task.ID, "body", target+"@-")

	// Doc title (unversioned) and body (at its version): created, then edited.
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "p",
		Body: "---\nstatus: draft\n---\n# Plan for P1-7\n\nSee P1-7.\n", CreatedBy: "stig"})
	doc := strconv.FormatInt(plan.ID, 10)
	check("doc", doc, "title", target+"@-")
	check("doc", doc, "body", target+"@1")
	if _, err := updateDocBody(t, s, plan.ID, "---\nstatus: draft\n---\n# Plan\n\nStill P1-7.\n"); err != nil {
		t.Fatal(err)
	}
	check("doc", doc, "body", target+"@1", target+"@2")
	newTitle := model.Markdown("Plan")
	docEvent(func(tx *sql.Tx, eventID int64) error {
		_, err := SetDocColumns(tx, s.Now(), plan.ID, model.DocColumnsInput{Title: &newTitle}, eventID)
		return err
	})
	check("doc", doc, "title")

	// Doc note body, held by its doc as note/<id>.
	spec := mustAcceptedSpec(t, s, "025-noted")
	note, err := addDocNote(t, s, spec.ID, model.AddDocNoteInput{Anchor: "sec-2", Body: "Blocks P1-7"}, "stig")
	if err != nil {
		t.Fatal(err)
	}
	check("doc", strconv.FormatInt(spec.ID, 10), "note/"+strconv.FormatInt(note.ID, 10), target+"@-")

	// Milestone title and deliverable description.
	var mile *model.Milestone
	var deliv *model.Deliverable
	tx(func(tx *sql.Tx) error {
		if mile, err = CreateMilestone(tx, s.Now(), "p1", "Ship P1-7", 0, "ada"); err != nil {
			return err
		}
		deliv, err = CreateDeliverable(tx, s.Now(), DeliverableInput{ProjectID: "p1", Name: "D",
			Description: "Made by P1-7", CreatedBy: "ada"})
		return err
	})
	check("milestone", mile.ID, "title", target+"@-")
	check("deliverable", deliv.ID, "description", target+"@-")

	// Idea title and body: created unshared (no project), then edited.
	idea, err := s.CreateIdea(ctx, "stig", model.CreateIdeaInput{Title: "P1-7 again", Body: "x", Type: "general"})
	if err != nil {
		t.Fatal(err)
	}
	check(model.NodeKindIdea, idea.ID, "title", target+"@-")
	ideaBody := model.Markdown("Per P1-7")
	if _, err := s.PatchIdea(ctx, "stig", idea.ID, model.PatchIdeaInput{Body: &ideaBody}); err != nil {
		t.Fatal(err)
	}
	check(model.NodeKindIdea, idea.ID, "body", target+"@-")

	// Decision question and context: posed, then edited.
	dctx := model.Markdown("Context: P1-7")
	dec, err := s.AddDecision(ctx, task.ID, "stig", model.DecisionInput{Key: "k", Question: "Ship P1-7?",
		Context: &dctx, ResponseType: "freetext"})
	if err != nil {
		t.Fatal(err)
	}
	decID := strconv.FormatInt(dec.ID, 10)
	check(model.NodeKindDecision, decID, "question", target+"@-")
	check(model.NodeKindDecision, decID, "context", target+"@-")
	if _, err := s.EditDecision(ctx, task.ID, "k", "stig", model.DecisionInput{Question: "Ship?"}); err != nil {
		t.Fatal(err)
	}
	check(model.NodeKindDecision, decID, "question")

	// Approval note, on an open impact review.
	var approval int64
	tx(func(tx *sql.Tx) error {
		if err := tx.QueryRow(
			`INSERT INTO approvals (entity_kind, entity_id, subject_revision, lane, state, review_kind, created_at)
			 VALUES ('task', $1, 'r1', '', 'awaiting', 'impact', $2) RETURNING id`,
			task.ID, time.Now()).Scan(&approval); err != nil {
			return err
		}
		return SetImpactNote(tx, approval, "Breaks P1-7")
	})
	check(model.NodeKindApproval, strconv.FormatInt(approval, 10), "note", target+"@-")
}

// TestDeleteDropsInlineRefs: deleting a milestone, a deliverable or a private
// idea deletes the inline_refs rows on both of its ends (WL-REQ-1852).
func TestDeleteDropsInlineRefs(t *testing.T) {
	s := openDocStore(t)
	ctx := context.Background()
	seedDocNoteTask(t, s, "P1-7")
	var mile *model.Milestone
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if mile, err = CreateMilestone(tx, s.Now(), "p1", "Ship P1-7", 0, "ada"); err != nil {
			return err
		}
		_, err = CreateDeliverable(tx, s.Now(), DeliverableInput{ProjectID: "p1", Name: "D",
			Description: "For " + mile.ID, CreatedBy: "ada"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	idea, err := s.CreateIdea(ctx, "stig", model.CreateIdeaInput{Title: "P1-7", Type: "general"})
	if err != nil {
		t.Fatal(err)
	}
	count := func() (n int) {
		t.Helper()
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM inline_refs`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != 3 {
		t.Fatalf("rows before delete = %d, want 3", n)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := DeleteMilestone(tx, mile.ID, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteIdea(ctx, "stig", idea.ID, ""); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Errorf("rows after deletes = %d, want 0", n)
	}
}
