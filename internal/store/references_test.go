package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// referenceStore opens a test store with the fixtures reference tests need:
// project A with milestone M, project B with deliverable D and task T, and
// an actor to attribute creates to.
func referenceStore(t *testing.T) (s *Store, mID, dID, tID string) {
	t.Helper()
	s = OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "a", "Project A", "PA"); err != nil {
		t.Fatalf("create project a: %v", err)
	}
	if err := s.CreateProject(ctx, "b", "Project B", "PB"); err != nil {
		t.Fatalf("create project b: %v", err)
	}
	if err := s.EnsureActor(ctx, "ada", "human", "Ada"); err != nil {
		t.Fatalf("create actor: %v", err)
	}

	mile, err := createMilestone(s, "a", "Internal review", 0)
	if err != nil {
		t.Fatalf("create milestone: %v", err)
	}
	del, err := createDeliverable(s, DeliverableInput{ProjectID: "b", Name: "output", CreatedBy: "ada"})
	if err != nil {
		t.Fatalf("create deliverable: %v", err)
	}
	task := createTask(t, s, s.Now(), TaskInput{ProjectID: "b", Title: "seed task", Priority: "medium", Kind: "feature"})

	return s, mile.ID, del.ID, task.ID
}

// createEntityEdge drives CreateEntityEdge through RecordEvent, the way
// production code will use it.
func createEntityEdge(s *Store, in CreateEntityEdgeInput) (*model.EntityEdge, error) {
	var e *model.EntityEdge
	_, _, err := s.RecordEvent(context.Background(), "cli", randomID(), "reference.created", nil,
		func(tx *sql.Tx, _ int64) error {
			var err error
			e, err = CreateEntityEdge(tx, s.Now(), in)
			return err
		})
	if err != nil {
		return nil, err
	}
	return e, nil
}

// mustRef creates a reference and fails the test if it errors.
func mustRef(t *testing.T, s *Store, fromKind, from, toKind, to, rel string) *model.EntityEdge {
	t.Helper()
	e, err := createEntityEdge(s, CreateEntityEdgeInput{
		FromKind: fromKind, From: from, ToKind: toKind, To: to, Rel: rel, CreatedBy: "ada",
	})
	if err != nil {
		t.Fatalf("create reference %s %s %s %s %s: %v", fromKind, from, rel, toKind, to, err)
	}
	return e
}

// refuse asserts that creating the given reference fails with wantErr.
func refuse(t *testing.T, s *Store, fromKind, from, toKind, to, rel string, wantErr error) {
	t.Helper()
	_, err := createEntityEdge(s, CreateEntityEdgeInput{
		FromKind: fromKind, From: from, ToKind: toKind, To: to, Rel: rel, CreatedBy: "ada",
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("reference %s %s %s %s %s: got %v, want %v", fromKind, from, rel, toKind, to, err, wantErr)
	}
}

// TestCreateEntityEdgeShapes checks referenceShapes' validation order (rel
// vocabulary, then end kinds, then both ends existing), the PK-violation
// mapping to ErrReferenceExists, and that CreatedAt is stamped (029 §5).
func TestCreateEntityEdgeShapes(t *testing.T) {
	t.Parallel()
	st, mID, dID, tID := referenceStore(t)

	edge := mustRef(t, st, "milestone", mID, "deliverable", dID, "depends_on")
	if edge.CreatedAt.IsZero() {
		t.Fatal("created_at not stamped")
	}
	if edge.FromKind != "milestone" || edge.From != mID || edge.ToKind != "deliverable" || edge.To != dID || edge.Rel != "depends_on" {
		t.Errorf("edge = %+v, want the requested ends and rel", edge)
	}
	if edge.CreatedBy != "ada" {
		t.Errorf("created_by = %q, want ada", edge.CreatedBy)
	}

	// Wrong shape: depends_on wants milestone -> deliverable, not milestone -> task.
	refuse(t, st, "milestone", mID, "task", tID, "depends_on", ErrInvalidInput)
	// Unknown rel.
	refuse(t, st, "milestone", mID, "deliverable", dID, "eats", ErrInvalidInput)
	// Dangling end.
	refuse(t, st, "milestone", mID, "deliverable", "PB-DEL-99", "depends_on", ErrNotFound)
	// Duplicate of the edge created above.
	refuse(t, st, "milestone", mID, "deliverable", dID, "depends_on", ErrReferenceExists)
}

// TestCreateEntityEdgeSeededBy checks the seeded_by shape (project -> task),
// which part 4 writes in production when a task is promoted from another
// project's milestone.
func TestCreateEntityEdgeSeededBy(t *testing.T) {
	t.Parallel()
	st, mID, _, tID := referenceStore(t)

	edge := mustRef(t, st, "project", "a", "task", tID, "seeded_by")
	if edge.FromKind != "project" || edge.From != "a" || edge.ToKind != "task" || edge.To != tID {
		t.Errorf("edge = %+v, want project a seeded_by task %s", edge, tID)
	}

	// Wrong shape: seeded_by wants project -> task, not milestone -> task.
	refuse(t, st, "milestone", mID, "task", tID, "seeded_by", ErrInvalidInput)
	// Dangling from-end.
	refuse(t, st, "project", "nope", "task", tID, "seeded_by", ErrNotFound)
}

// TestReferencesFor checks that both ends of a reference find it, from-side
// matches sort first, and an entity with no references gets an empty slice.
func TestReferencesFor(t *testing.T) {
	t.Parallel()
	st, mID, dID, tID := referenceStore(t)

	mustRef(t, st, "milestone", mID, "deliverable", dID, "depends_on")
	mustRef(t, st, "project", "a", "task", tID, "seeded_by")

	ctx := context.Background()
	fromMilestone, err := st.ReferencesFor(ctx, "milestone", mID)
	if err != nil {
		t.Fatalf("references for milestone: %v", err)
	}
	if len(fromMilestone) != 1 || fromMilestone[0].Rel != "depends_on" {
		t.Fatalf("references for milestone = %+v, want the one depends_on edge", fromMilestone)
	}

	toDeliverable, err := st.ReferencesFor(ctx, "deliverable", dID)
	if err != nil {
		t.Fatalf("references for deliverable: %v", err)
	}
	if len(toDeliverable) != 1 || toDeliverable[0].From != mID {
		t.Fatalf("references for deliverable = %+v, want the one edge from the milestone", toDeliverable)
	}

	empty, err := st.ReferencesFor(ctx, "task", "no-such-task")
	if err != nil || len(empty) != 0 {
		t.Fatalf("references for unknown task = %v, %v; want empty slice, nil", empty, err)
	}
}

// TestMilestoneDeliverableRefs checks the bulk reader the project Progress
// page uses: the referenced deliverable comes back with its reported state
// joined in, not just its id, and an unreferenced milestone gets an empty
// slice.
func TestMilestoneDeliverableRefs(t *testing.T) {
	t.Parallel()
	st, mID, dID, _ := referenceStore(t)
	ctx := context.Background()

	empty, err := st.MilestoneDeliverableRefs(ctx, mID)
	if err != nil || len(empty) != 0 {
		t.Fatalf("refs before any reference = %v, %v; want empty slice, nil", empty, err)
	}

	mustRef(t, st, "milestone", mID, "deliverable", dID, "depends_on")

	refs, err := st.MilestoneDeliverableRefs(ctx, mID)
	if err != nil {
		t.Fatalf("milestone deliverable refs: %v", err)
	}
	if len(refs) != 1 || refs[0].ID != dID {
		t.Fatalf("refs = %+v, want the one deliverable %s", refs, dID)
	}
	if refs[0].ReportedState != "" {
		t.Errorf("reported_state = %q, want empty until something reports", refs[0].ReportedState)
	}
}
