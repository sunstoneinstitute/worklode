package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// createMilestone drives CreateMilestone through RecordEvent, the way every
// caller does, and returns its error.
func createMilestone(s *Store, projectID, title string, position int) (*model.Milestone, error) {
	var m *model.Milestone
	_, _, err := s.RecordEvent(context.Background(), "cli", randomID(), "milestone.created", nil,
		func(tx *sql.Tx, _ int64) error {
			var err error
			m, err = CreateMilestone(tx, s.Now(), projectID, title, position, "ada")
			return err
		})
	if err != nil {
		return nil, err
	}
	return m, nil
}

// TestCreateMilestone covers spec 029 §4's id form (a milestone draws from
// its project's own MILE counter), position 0 appending after the project's
// last milestone, and the two rejections happening before an ordinal is
// burned.
func TestCreateMilestone(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "p1", "Project One", "P1"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := s.EnsureActor(ctx, "ada", "human", "Ada"); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	// A deliverable first, to prove the two counters do not share a sequence.
	if _, err := createDeliverable(s, DeliverableInput{ProjectID: "p1", Name: "output"}); err != nil {
		t.Fatalf("create deliverable: %v", err)
	}

	m1, err := createMilestone(s, "p1", "Internal review", 0)
	if err != nil {
		t.Fatalf("create first milestone: %v", err)
	}
	m2, err := createMilestone(s, "p1", "  Publication  ", 0)
	if err != nil {
		t.Fatalf("create second milestone: %v", err)
	}
	if m1.ID != "P1-MILE-1" || m2.ID != "P1-MILE-2" {
		t.Errorf("ordinals: %s, %s; want P1-MILE-1, P1-MILE-2", m1.ID, m2.ID)
	}
	if m1.Position != 1 || m2.Position != 2 {
		t.Errorf("append positions: %d, %d; want 1, 2", m1.Position, m2.Position)
	}
	if m2.Title != "Publication" {
		t.Errorf("title = %q, want it trimmed", m2.Title)
	}
	if m1.Project != "p1" || m1.CreatedBy != "ada" || m1.CreatedAt.IsZero() {
		t.Errorf("milestone = %+v, want project, creator and clock set", m1)
	}

	// An explicit position is stored as given, not renumbered.
	m3, err := createMilestone(s, "p1", "Kickoff", 1)
	if err != nil {
		t.Fatalf("create with explicit position: %v", err)
	}
	if m3.Position != 1 {
		t.Errorf("explicit position = %d, want 1", m3.Position)
	}

	if _, err := createMilestone(s, "p1", "   ", 0); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("blank title: got %v, want ErrInvalidInput", err)
	}
	if _, err := createMilestone(s, "nope", "Anything", 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown project: got %v, want ErrNotFound", err)
	}
	// Neither rejection burned an ordinal.
	m4, err := createMilestone(s, "p1", "Wrap-up", 0)
	if err != nil {
		t.Fatalf("create after rejections: %v", err)
	}
	if m4.ID != "P1-MILE-4" {
		t.Errorf("id after two rejections = %q, want P1-MILE-4", m4.ID)
	}
	if m4.Position != 3 {
		t.Errorf("append position after explicit 1 = %d, want 3", m4.Position)
	}
}

// TestCreateMilestoneRejectsLongTitle keeps a stray paste out of the row and
// out of a list cell: 200 runes is the bound, counted in runes so a title in
// a non-Latin script is not cut short.
func TestCreateMilestoneRejectsLongTitle(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	if err := s.CreateProject(context.Background(), "p1", "Project One", "P1"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := s.EnsureActor(context.Background(), "ada", "human", "Ada"); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	long := make([]rune, 201)
	for i := range long {
		long[i] = 'é'
	}
	if _, err := createMilestone(s, "p1", string(long), 0); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("201-rune title: got %v, want ErrInvalidInput", err)
	}
	if _, err := createMilestone(s, "p1", string(long[:200]), 0); err != nil {
		t.Errorf("200-rune title: got %v, want it accepted", err)
	}
}
