package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
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

// TestCreateDeliverableByLabelMintsSelector covers 029 §3.1's label form:
// the project key and title mint a stable label, which routes only through
// the label selector and is projected instead of an artifact address.
func TestCreateDeliverableByLabelMintsSelector(t *testing.T) {
	t.Parallel()
	s := deliverableStore(t)
	ctx := context.Background()

	d, err := createDeliverable(s, DeliverableInput{
		ProjectID: "cow", Name: "Datasets", Label: true,
	})
	if err != nil {
		t.Fatalf("create label deliverable: %v", err)
	}
	const wantLabel = "worklode.deliverable=COW/datasets"
	if d.Label != wantLabel || d.Artifact != "" {
		t.Fatalf("created label/artifact = %q/%q, want %q/empty", d.Label, d.Artifact, wantLabel)
	}

	var routed []DeclaredEntity
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		routed, err = OpenDeclarationsForArtifact(tx, "label", d.Label)
		return err
	}); err != nil {
		t.Fatalf("route label: %v", err)
	}
	if len(routed) != 1 || routed[0] != (DeclaredEntity{Kind: "deliverable", ID: d.ID}) {
		t.Fatalf("label routes to %+v, want deliverable %s", routed, d.ID)
	}

	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		got, err := OpenDeclarationsForArtifact(tx, "address", d.Label)
		if err != nil {
			return err
		}
		if len(got) != 0 {
			t.Errorf("address lookup for label routes to %+v, want none", got)
		}
		return nil
	}); err != nil {
		t.Fatalf("route label as address: %v", err)
	}

	got, err := s.GetDeliverable(ctx, d.ID)
	if err != nil {
		t.Fatalf("get label deliverable: %v", err)
	}
	if got.Label != wantLabel || got.Artifact != "" {
		t.Fatalf("GetDeliverable label/artifact = %q/%q, want %q/empty", got.Label, got.Artifact, wantLabel)
	}
	items, err := s.ListDeliverables(ctx, "cow")
	if err != nil {
		t.Fatalf("list label deliverable: %v", err)
	}
	if len(items) != 1 || items[0].Label != wantLabel || items[0].Artifact != "" {
		t.Fatalf("ListDeliverables = %+v, want label %q and empty artifact", items, wantLabel)
	}
}

// TestCreateDeliverableLabelRejectsConflicts keeps minting deterministic: a
// label cannot be combined with an address or declared twice in a project,
// and either refusal happens before allocating an ordinal.
func TestCreateDeliverableLabelRejectsConflicts(t *testing.T) {
	t.Parallel()
	s := deliverableStore(t)

	if _, err := createDeliverable(s, DeliverableInput{
		ProjectID: "cow", Name: "Datasets", Label: true, Artifact: testArtifact,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("label plus artifact error = %v, want ErrInvalidInput", err)
	}
	first, err := createDeliverable(s, DeliverableInput{ProjectID: "cow", Name: "Datasets", Label: true})
	if err != nil {
		t.Fatalf("create first label: %v", err)
	}
	if first.ID != "COW-DEL-1" {
		t.Fatalf("first label id = %q, want COW-DEL-1", first.ID)
	}
	if _, err := createDeliverable(s, DeliverableInput{ProjectID: "cow", Name: "Datasets", Label: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate label error = %v, want ErrInvalidInput", err)
	}
	next, err := createDeliverable(s, DeliverableInput{ProjectID: "cow", Name: "Other"})
	if err != nil {
		t.Fatalf("create after duplicate: %v", err)
	}
	if next.ID != "COW-DEL-2" {
		t.Fatalf("id after rejected labels = %q, want COW-DEL-2", next.ID)
	}
}

// TestCreateDeliverableLabelRejectsConcurrentDuplicate keeps the duplicate
// check ahead of ordinal allocation even when two declarations race. The
// first transaction leaves its declaration uncommitted while the second
// starts; after the first commits, the second must see that declaration and
// fail without consuming COW-DEL-2.
func TestCreateDeliverableLabelRejectsConcurrentDuplicate(t *testing.T) {
	s := deliverableStore(t)
	ctx := context.Background()

	tx1, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin first transaction: %v", err)
	}
	if _, err := tx1.Exec(`SELECT 1 FROM projects WHERE id = $1 FOR UPDATE`, "cow"); err != nil {
		tx1.Rollback()
		t.Fatalf("lock project: %v", err)
	}
	label := "worklode.deliverable=COW/datasets"
	if _, err := tx1.Exec(
		`INSERT INTO deliverables (id, project_id, name, description, url, created_by, created_at, updated_at)
		 VALUES ($1, $2, $3, '', '', NULL, $4, $4)`,
		"COW-DEL-9", "cow", "Datasets", s.Now()); err != nil {
		tx1.Rollback()
		t.Fatalf("seed first label deliverable: %v", err)
	}
	if err := DeclareArtifact(tx1, s.Now(), "deliverable", "COW-DEL-9", "label", label); err != nil {
		tx1.Rollback()
		t.Fatalf("declare first label: %v", err)
	}

	started := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		tx2, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			secondDone <- err
			return
		}
		close(started)
		_, err = CreateDeliverable(tx2, s.Now(), DeliverableInput{
			ProjectID: "cow", Name: "Datasets", Label: true,
		})
		if err == nil {
			err = tx2.Commit()
		} else {
			tx2.Rollback()
		}
		secondDone <- err
	}()
	<-started
	select {
	case err := <-secondDone:
		tx1.Rollback()
		t.Fatalf("second create completed before first committed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit first label: %v", err)
	}
	if err := <-secondDone; !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("concurrent duplicate error = %v, want ErrInvalidInput", err)
	}

	next, err := createDeliverable(s, DeliverableInput{ProjectID: "cow", Name: "Other"})
	if err != nil {
		t.Fatalf("create after concurrent duplicate: %v", err)
	}
	if next.ID != "COW-DEL-1" {
		t.Fatalf("id after concurrent duplicate = %q, want COW-DEL-1", next.ID)
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

// reportDeliverableState drives ReportDeliverableState through RecordEvent,
// the way both surfaces do, and returns its error.
func reportDeliverableState(s *Store, deliverableID, state, source, actorID, note string) error {
	_, _, err := s.RecordEvent(context.Background(), source, randomID(), "deliverable.reported", nil,
		func(tx *sql.Tx, eventID int64) error {
			return ReportDeliverableState(tx, eventID, s.Now(), deliverableID, state, source, actorID, note)
		})
	return err
}

// TestReportDeliverableStateWithoutDeclaration pins 029 §3.2's user report on
// a deliverable that declares no address: the evidence lands against
// artifact_uri = ” and the projection surfaces it, because the entity itself
// is the subject when the state change has no address.
func TestReportDeliverableStateWithoutDeclaration(t *testing.T) {
	t.Parallel()
	s := deliverableStore(t)
	d, err := createDeliverable(s, DeliverableInput{ProjectID: "cow", Name: "Report PDF"})
	if err != nil {
		t.Fatalf("create deliverable: %v", err)
	}

	if err := reportDeliverableState(s, d.ID, "published", "cli", "alice", "uploaded to the site"); err != nil {
		t.Fatalf("report state: %v", err)
	}

	got, err := s.GetDeliverable(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("get deliverable: %v", err)
	}
	if got.ReportedState != "published" || got.ReportedProvenance != "user_reported" {
		t.Errorf("state %q provenance %q, want published/user_reported", got.ReportedState, got.ReportedProvenance)
	}
	if got.ReportedAt == nil {
		t.Error("ReportedAt is nil, want the report's time")
	}
	var uri, source, detail string
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRow(
			`SELECT artifact_uri, source, detail::text FROM artifact_evidence
			  WHERE entity_kind = 'deliverable' AND entity_id = $1`, d.ID).Scan(&uri, &source, &detail)
	}); err != nil {
		t.Fatalf("read evidence: %v", err)
	}
	if uri != "" || source != "cli" {
		t.Errorf("artifact_uri %q source %q, want \"\"/cli", uri, source)
	}
	if !strings.Contains(detail, `"actor": "alice"`) || !strings.Contains(detail, `"note": "uploaded to the site"`) {
		t.Errorf("detail = %s, want the actor and the note", detail)
	}
}

// TestReportDeliverableStateAgainstDeclaration pins the declared-address case:
// the report files against the deliverable's first declaration, which is the
// same address the projection reads observed evidence from — so a person's
// report and an emitter's land in the same place and the newest wins.
func TestReportDeliverableStateAgainstDeclaration(t *testing.T) {
	t.Parallel()
	s := deliverableStore(t)
	d, err := createDeliverable(s, DeliverableInput{
		ProjectID: "cow", Name: "Datapackage", Artifact: "https://data.example/cow.zip",
	})
	if err != nil {
		t.Fatalf("create deliverable: %v", err)
	}

	if err := reportDeliverableState(s, d.ID, "failed", "web", "bob", ""); err != nil {
		t.Fatalf("report state: %v", err)
	}

	uri := ""
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRow(
			`SELECT artifact_uri FROM artifact_evidence
			  WHERE entity_kind = 'deliverable' AND entity_id = $1`, d.ID).Scan(&uri)
	}); err != nil {
		t.Fatalf("read evidence: %v", err)
	}
	if uri != "https://data.example/cow.zip" {
		t.Errorf("artifact_uri = %q, want the declared address", uri)
	}
	got, err := s.GetDeliverable(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("get deliverable: %v", err)
	}
	if got.ReportedState != "failed" || got.ReportedProvenance != "user_reported" {
		t.Errorf("state %q provenance %q, want failed/user_reported", got.ReportedState, got.ReportedProvenance)
	}
}

// TestReportDeliverableStateRejects pins the two refusals: an unknown
// deliverable is ErrNotFound, a state outside the artifact_evidence CHECK set
// is ErrInvalidInput — neither reaches the table as a constraint violation.
func TestReportDeliverableStateRejects(t *testing.T) {
	t.Parallel()
	s := deliverableStore(t)
	d, err := createDeliverable(s, DeliverableInput{ProjectID: "cow", Name: "Report PDF"})
	if err != nil {
		t.Fatalf("create deliverable: %v", err)
	}

	if err := reportDeliverableState(s, "COW-DEL-99", "published", "cli", "alice", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown deliverable: err = %v, want ErrNotFound", err)
	}
	if err := reportDeliverableState(s, d.ID, "bogus", "cli", "alice", ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("bad state: err = %v, want ErrInvalidInput", err)
	}
}

// TestDeclaredDeliverableKeepsObservedProjection pins the LATERAL widening
// against a regression: broadening the evidence correlation to
// COALESCE(decl.artifact_uri, ”) must not change what a deliverable with a
// declared address reports. Evidence filed against another address still does
// not surface, and evidence against ” does not leak onto it either.
func TestDeclaredDeliverableKeepsObservedProjection(t *testing.T) {
	t.Parallel()
	s := deliverableStore(t)
	d, err := createDeliverable(s, DeliverableInput{
		ProjectID: "cow", Name: "Datapackage", Artifact: "https://data.example/cow.zip",
	})
	if err != nil {
		t.Fatalf("create deliverable: %v", err)
	}
	now := s.Now()
	file := func(artifact, state string, at time.Time) {
		t.Helper()
		if _, _, err := s.RecordEvent(context.Background(), "prober", randomID(), "prober."+state, nil,
			func(tx *sql.Tx, eventID int64) error {
				_, err := InsertArtifactEvidence(tx, eventID, model.ArtifactEvidence{
					EntityKind: "deliverable", EntityID: d.ID, Artifact: artifact,
					Source: "prober", State: state, Provenance: "observed", OccurredAt: at,
				})
				return err
			}); err != nil {
			t.Fatalf("file evidence %s/%s: %v", artifact, state, err)
		}
	}
	file("https://data.example/cow.zip", "published", now)
	// Two decoys: another address entirely, and the no-declaration bucket the
	// widened correlation newly matches for deliverables that declare nothing.
	file("https://data.example/other.zip", "failed", now.Add(time.Hour))
	file("", "removed", now.Add(2*time.Hour))

	got, err := s.GetDeliverable(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("get deliverable: %v", err)
	}
	if got.ReportedState != "published" || got.ReportedProvenance != "observed" {
		t.Errorf("state %q provenance %q, want published/observed", got.ReportedState, got.ReportedProvenance)
	}
}
