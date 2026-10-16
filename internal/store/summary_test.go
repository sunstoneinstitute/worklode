package store

import (
	"errors"
	"testing"
)

// TestProjectSummaryFacts covers WL-REQ-1797's inputs: tasks with due and
// closed, the roster, and docs whose current version still owes a review.
func TestProjectSummaryFacts(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()
	if err := s.CreateActor(ctx, "ada", "human", "Ada", false); err != nil {
		t.Fatal(err)
	}
	seedParticipant(t, s, "horndb", "ada", "member", false)

	pastDue := defaultTaskInput()
	pastDue.Title = "late one"
	pastDue.Due = "2020-01-01"
	late := createTask(t, s, taskTestNow, pastDue)

	inReview := createTask(t, s, taskTestNow, defaultTaskInput())
	if _, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET state = 'in_review', assignee = 'ada' WHERE id = $1`, inReview.ID); err != nil {
		t.Fatal(err)
	}

	doc := docForApproval(t, s, "awaiting-doc", 1)
	assignDocReviewers(t, s, doc.ID, []string{"ada"})
	tx := mustBegin(t, s)
	ada := "ada"
	if _, err := InsertAwaitingApproval(tx, taskTestNow, "doc", DocEntityID(doc.ID), "1", "", nil, &ada, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	docForApproval(t, s, "quiet-doc", 2)

	f, err := s.ProjectSummaryFacts(ctx, "horndb")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Tasks) != 2 {
		t.Fatalf("tasks = %+v", f.Tasks)
	}
	byID := map[string]SummaryTask{}
	for _, tk := range f.Tasks {
		byID[tk.ID] = tk
	}
	if g := byID[late.ID]; g.Due != "2020-01-01" || g.Kind == "" || g.Closed || g.Title != "late one" {
		t.Errorf("late = %+v", g)
	}
	if g := byID[inReview.ID]; g.State != "in_review" || g.Assignee != "ada" || g.Closed {
		t.Errorf("in review = %+v", g)
	}
	if len(f.Roster) != 2 {
		t.Errorf("roster = %+v", f.Roster)
	}
	if len(f.Docs) != 1 || f.Docs[0].Title != doc.Title || f.Docs[0].Ref != doc.FormatRef() ||
		f.Docs[0].URL == "" {
		t.Errorf("docs = %+v (want %s)", f.Docs, doc.FormatRef())
	}

	if _, err := s.ProjectSummaryFacts(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown project err = %v", err)
	}
}
