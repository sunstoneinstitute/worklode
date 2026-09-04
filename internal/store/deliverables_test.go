package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// randomID returns a random hex string, for driving RecordEvent's
// (source, externalID) idempotency key in tests that don't otherwise care
// about the external id.
func randomID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// createDeliverable drives CreateDeliverable through RecordEvent, the way
// every caller does, and returns its error.
func createDeliverable(s *Store, in DeliverableInput) (*model.Deliverable, error) {
	var d *model.Deliverable
	_, _, err := s.RecordEvent(context.Background(), "web", randomID(), "deliverable.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			d, err = CreateDeliverable(tx, s.Now(), in)
			return err
		})
	if err != nil {
		return nil, err
	}
	return d, nil
}

func deliverableStore(t *testing.T) *Store {
	t.Helper()
	s := OpenTestStore(t)
	if err := s.CreateProject(context.Background(), "cow", "Cost of War", "COW"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return s
}

// TestCreateDeliverableAllocatesPerProjectOrdinals checks spec 029 §4's id
// form: a deliverable draws from its project's own DEL counter, so the first
// is COW-DEL-1 and the numbering is independent of the task counter and of
// every other project.
func TestCreateDeliverableAllocatesPerProjectOrdinals(t *testing.T) {
	t.Parallel()
	s := deliverableStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "atlas", "Atlas", "ATL"); err != nil {
		t.Fatalf("create second project: %v", err)
	}
	// A task first, to prove the two counters do not share a sequence.
	createTask(t, s, s.Now(), TaskInput{ProjectID: "cow", Title: "t", Priority: "medium", Kind: "feature"})

	for i, want := range []string{"COW-DEL-1", "COW-DEL-2", "COW-DEL-3"} {
		d, err := createDeliverable(s, DeliverableInput{ProjectID: "cow", Name: "output"})
		if err != nil {
			t.Fatalf("create deliverable %d: %v", i, err)
		}
		if d.ID != want {
			t.Errorf("deliverable %d id = %q, want %q", i, d.ID, want)
		}
	}

	other, err := createDeliverable(s, DeliverableInput{ProjectID: "atlas", Name: "output"})
	if err != nil {
		t.Fatalf("create deliverable in second project: %v", err)
	}
	if other.ID != "ATL-DEL-1" {
		t.Errorf("second project's first deliverable = %q, want ATL-DEL-1", other.ID)
	}
}

// TestCreateDeliverableStoresFields checks the three descriptive fields spec
// 029 §3.1 allows, trimmed, plus the creator and timestamps — and that a
// re-read returns the same record.
func TestCreateDeliverableStoresFields(t *testing.T) {
	t.Parallel()
	s := deliverableStore(t)
	ctx := context.Background()
	if err := s.UpsertHumanActor(ctx, "kari", "Kari Nordmann", false, "", "", nil); err != nil {
		t.Fatalf("upsert actor: %v", err)
	}

	created, err := createDeliverable(s, DeliverableInput{
		ProjectID:   "cow",
		Name:        "  Casualty datapackage  ",
		Description: "  Frictionless datapackage of verified casualty records.  ",
		URL:         "  https://example.org/data/casualties  ",
		CreatedBy:   "kari",
	})
	if err != nil {
		t.Fatalf("create deliverable: %v", err)
	}
	if created.Name != "Casualty datapackage" {
		t.Errorf("name = %q, want the trimmed value", created.Name)
	}
	if created.URL != "https://example.org/data/casualties" {
		t.Errorf("url = %q, want the trimmed value", created.URL)
	}
	if created.CreatedBy != "kari" {
		t.Errorf("created_by = %q, want kari", created.CreatedBy)
	}
	if created.CreatedAt.IsZero() || !created.UpdatedAt.Equal(created.CreatedAt) {
		t.Errorf("timestamps = %v/%v, want both set and equal", created.CreatedAt, created.UpdatedAt)
	}

	got, err := s.GetDeliverable(ctx, created.ID)
	if err != nil {
		t.Fatalf("get deliverable: %v", err)
	}
	// Compare the timestamps with Equal (the driver returns its own
	// time.Location) and the rest field by field.
	if !got.CreatedAt.Equal(created.CreatedAt) || !got.UpdatedAt.Equal(created.UpdatedAt) {
		t.Errorf("re-read timestamps = %v/%v, want %v/%v",
			got.CreatedAt, got.UpdatedAt, created.CreatedAt, created.UpdatedAt)
	}
	got.CreatedAt, got.UpdatedAt = created.CreatedAt, created.UpdatedAt
	if *got != *created {
		t.Errorf("re-read = %+v, want %+v", *got, *created)
	}
}

// TestCreateDeliverableRejectsBadInput checks that a blank name and an
// unknown project are refused, and that neither burns an ordinal — the next
// good create still gets COW-DEL-1.
func TestCreateDeliverableRejectsBadInput(t *testing.T) {
	t.Parallel()
	s := deliverableStore(t)

	if _, err := createDeliverable(s, DeliverableInput{ProjectID: "cow", Name: "   "}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("blank name error = %v, want ErrInvalidInput", err)
	}
	if _, err := createDeliverable(s, DeliverableInput{ProjectID: "nope", Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown project error = %v, want ErrNotFound", err)
	}

	d, err := createDeliverable(s, DeliverableInput{ProjectID: "cow", Name: "first"})
	if err != nil {
		t.Fatalf("create deliverable: %v", err)
	}
	if d.ID != "COW-DEL-1" {
		t.Errorf("id after two rejected creates = %q, want COW-DEL-1", d.ID)
	}
}

// TestCreateDeliverableMilestone checks that a declared milestone attach
// (spec 029 §2) is stored, a cross-project or unknown milestone is
// ErrInvalidInput, and neither rejected create burns an ordinal.
func TestCreateDeliverableMilestone(t *testing.T) {
	t.Parallel()
	s := deliverableStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "atlas", "Atlas", "ATL"); err != nil {
		t.Fatalf("create second project: %v", err)
	}
	if err := s.EnsureActor(ctx, "ada", "human", "Ada"); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	mile, err := createMilestone(s, "cow", "Internal review", 0)
	if err != nil {
		t.Fatalf("create milestone: %v", err)
	}

	if _, err := createDeliverable(s, DeliverableInput{ProjectID: "atlas", Name: "x", MilestoneID: mile.ID}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("cross-project attach at create: got %v, want ErrInvalidInput", err)
	}
	if _, err := createDeliverable(s, DeliverableInput{ProjectID: "cow", Name: "x", MilestoneID: "COW-MILE-9"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unknown milestone at create: got %v, want ErrInvalidInput", err)
	}

	d, err := createDeliverable(s, DeliverableInput{ProjectID: "cow", Name: "first", MilestoneID: mile.ID})
	if err != nil {
		t.Fatalf("create deliverable with milestone: %v", err)
	}
	if d.ID != "COW-DEL-1" {
		t.Errorf("id after two rejected creates = %q, want COW-DEL-1", d.ID)
	}
	if d.Milestone != mile.ID {
		t.Errorf("milestone = %q, want %s", d.Milestone, mile.ID)
	}
}

// TestListDeliverables checks declaration order, project scoping, and that an
// unknown or empty project yields an empty slice rather than an error.
func TestListDeliverables(t *testing.T) {
	t.Parallel()
	s := deliverableStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "atlas", "Atlas", "ATL"); err != nil {
		t.Fatalf("create second project: %v", err)
	}

	if got, err := s.ListDeliverables(ctx, "cow"); err != nil || len(got) != 0 {
		t.Fatalf("empty project list = %v, %v; want empty slice, nil", got, err)
	}

	// Distinct creation instants, so the ordering assertion is about
	// declaration order and not about a tiebreak.
	base := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	for i, name := range []string{"dataset", "analysis", "report"} {
		at := base.Add(time.Duration(i) * time.Minute)
		s.SetNowFunc(func() time.Time { return at })
		if _, err := createDeliverable(s, DeliverableInput{ProjectID: "cow", Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if _, err := createDeliverable(s, DeliverableInput{ProjectID: "atlas", Name: "elsewhere"}); err != nil {
		t.Fatalf("create in second project: %v", err)
	}

	got, err := s.ListDeliverables(ctx, "cow")
	if err != nil {
		t.Fatalf("list deliverables: %v", err)
	}
	var names []string
	for _, d := range got {
		names = append(names, d.Name)
	}
	if len(names) != 3 || names[0] != "dataset" || names[1] != "analysis" || names[2] != "report" {
		t.Errorf("names = %v, want [dataset analysis report] and nothing from the other project", names)
	}

	if _, err := s.GetDeliverable(ctx, "COW-DEL-99"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get unknown deliverable = %v, want ErrNotFound", err)
	}
}

// TestSetDeliverableMilestone mirrors TestUpdateTaskMilestone for the
// deliverable side of spec 029 §2: a same-project attach is stored and bumps
// updated_at, a cross-project or unknown milestone is refused, and detaching
// (milestone "") is always legal.
func TestSetDeliverableMilestone(t *testing.T) {
	t.Parallel()
	s := deliverableStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "atlas", "Atlas", "ATL"); err != nil {
		t.Fatalf("create second project: %v", err)
	}
	if err := s.EnsureActor(ctx, "ada", "human", "Ada"); err != nil {
		t.Fatalf("create actor: %v", err)
	}

	mile, err := createMilestone(s, "cow", "Internal review", 0)
	if err != nil {
		t.Fatalf("create milestone: %v", err)
	}

	d1, err := createDeliverable(s, DeliverableInput{ProjectID: "cow", Name: "output"})
	if err != nil {
		t.Fatalf("create deliverable in cow: %v", err)
	}
	d2, err := createDeliverable(s, DeliverableInput{ProjectID: "atlas", Name: "other"})
	if err != nil {
		t.Fatalf("create deliverable in atlas: %v", err)
	}

	set := func(now time.Time, id, milestoneID string) error {
		_, _, err := s.RecordEvent(ctx, "cli", randomID(), "deliverable.updated", nil,
			func(tx *sql.Tx, _ int64) error {
				return SetDeliverableMilestone(tx, now, id, milestoneID)
			})
		return err
	}

	attachAt := d1.UpdatedAt.Add(time.Minute)
	if err := set(attachAt, d1.ID, mile.ID); err != nil {
		t.Fatalf("attach in same project: %v", err)
	}
	got, err := s.GetDeliverable(ctx, d1.ID)
	if err != nil {
		t.Fatalf("get deliverable: %v", err)
	}
	if got.Milestone != mile.ID {
		t.Fatalf("milestone not stored: %+v", got)
	}
	if !got.UpdatedAt.Equal(attachAt) {
		t.Fatalf("updated_at after attach = %v, want %v", got.UpdatedAt, attachAt)
	}

	// 029 §5: containment never crosses a project boundary.
	if err := set(attachAt, d2.ID, mile.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cross-project attach: got %v, want ErrInvalidInput", err)
	}
	if err := set(attachAt, d1.ID, "COW-MILE-9"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown milestone: got %v, want ErrInvalidInput", err)
	}
	if err := set(attachAt, "COW-DEL-99", mile.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown deliverable: got %v, want ErrNotFound", err)
	}

	// Detach is always legal (029 §2), and also bumps updated_at.
	detachAt := attachAt.Add(time.Minute)
	if err := set(detachAt, d1.ID, ""); err != nil {
		t.Fatalf("detach: %v", err)
	}
	got, err = s.GetDeliverable(ctx, d1.ID)
	if err != nil {
		t.Fatalf("get deliverable: %v", err)
	}
	if got.Milestone != "" {
		t.Fatalf("milestone after detach: got %q, want empty", got.Milestone)
	}
	if !got.UpdatedAt.Equal(detachAt) {
		t.Fatalf("updated_at after detach = %v, want %v", got.UpdatedAt, detachAt)
	}
}
