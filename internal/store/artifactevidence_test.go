package store

import (
	"database/sql"
	"strconv"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

const testArtifact = "bigquery://sunstone-prod/cow/casualties"

// declare records one artifact declaration through its own transaction.
func declare(t *testing.T, s *Store, kind, id, artifact string) {
	t.Helper()
	if err := s.Tx(t.Context(), func(tx *sql.Tx) error {
		return DeclareArtifact(tx, s.Now(), kind, id, artifact)
	}); err != nil {
		t.Fatalf("declare %s for %s %s: %v", artifact, kind, id, err)
	}
}

func openDeclarations(t *testing.T, s *Store, artifact string) []DeclaredEntity {
	t.Helper()
	var got []DeclaredEntity
	if err := s.Tx(t.Context(), func(tx *sql.Tx) error {
		var err error
		got, err = OpenDeclarationsForArtifact(tx, artifact)
		return err
	}); err != nil {
		t.Fatalf("open declarations for %s: %v", artifact, err)
	}
	return got
}

// fileEvidence writes one evidence row through RecordEvent, the way the
// catalog ingest does, and reports whether the insert was new.
func fileEvidence(t *testing.T, s *Store, ev model.ArtifactEvidence) bool {
	t.Helper()
	var inserted bool
	_, _, err := s.RecordEvent(t.Context(), "catalog", nextExt(t), "catalog."+ev.State, nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			inserted, err = InsertArtifactEvidence(tx, eventID, ev)
			return err
		})
	if err != nil {
		t.Fatalf("file evidence for %s %s: %v", ev.EntityKind, ev.EntityID, err)
	}
	return inserted
}

func evidence(kind, id, state string, at time.Time) model.ArtifactEvidence {
	return model.ArtifactEvidence{
		EntityKind: kind, EntityID: id, Artifact: testArtifact,
		Source: "catalog", State: state, Provenance: "observed",
		OccurredAt: at,
	}
}

// TestDeclareArtifactIsIdempotent pins the ON CONFLICT: re-declaring the same
// address for the same entity leaves one routing target, so a create path
// that runs twice cannot double-route a delivery.
func TestDeclareArtifactIsIdempotent(t *testing.T) {
	s := openTaskStore(t)
	d, err := createDeliverable(s, DeliverableInput{ProjectID: "horndb", Name: "casualties"})
	if err != nil {
		t.Fatalf("create deliverable: %v", err)
	}
	declare(t, s, "deliverable", d.ID, testArtifact)
	declare(t, s, "deliverable", d.ID, testArtifact)

	got := openDeclarations(t, s, testArtifact)
	if len(got) != 1 || got[0] != (DeclaredEntity{Kind: "deliverable", ID: d.ID}) {
		t.Fatalf("open declarations = %+v, want one deliverable %s", got, d.ID)
	}
}

// TestOpenDeclarationsForArtifactPerKindOpenness covers the three declarer
// kinds and the openness predicate each one gets. A deliverable is always
// open (029 §3.2 leaves it no state to be closed by); a task uses taskClosed,
// so an abandoned one and one at its repo's done_state drop out while an
// in_progress one stays; a doc drops out only at superseded, because an
// accepted spec is still the live declaration.
func TestOpenDeclarationsForArtifactPerKindOpenness(t *testing.T) {
	s := openTaskStore(t)
	mapRepo(t, s, "horndb", "sunstoneinstitute/horndb", "merged")

	del, err := createDeliverable(s, DeliverableInput{ProjectID: "horndb", Name: "casualties"})
	if err != nil {
		t.Fatalf("create deliverable: %v", err)
	}

	openTask := createTask(t, s, taskTestNow, defaultTaskInput())
	walkTo(t, s, openTask.ID, "in_progress")

	abandoned := createTask(t, s, taskTestNow, defaultTaskInput())
	walkTo(t, s, abandoned.ID, "abandoned")

	// merged, with the work landed in a repo whose done_state is merged:
	// taskClosed calls it closed, so the catalog must not file against it.
	doneTask := createTask(t, s, taskTestNow, defaultTaskInput())
	walkTo(t, s, doneTask.ID, "merged")
	landCommit(t, s, doneTask.ID, "sunstoneinstitute/horndb", "aa11bb22cc33dd44ee55ff6677889900aabbccdd")

	draftDoc := mustCreateDoc(t, s, DocInput{Project: "horndb", Kind: "spec", Number: 1, Slug: "live-spec"})
	oldDoc := mustCreateDoc(t, s, DocInput{
		Project: "horndb", Kind: "spec", Number: 2, Slug: "old-spec", Status: "superseded",
	})

	for _, id := range []string{openTask.ID, abandoned.ID, doneTask.ID} {
		declare(t, s, "task", id, testArtifact)
	}
	declare(t, s, "deliverable", del.ID, testArtifact)
	declare(t, s, "doc", strconv.FormatInt(draftDoc.ID, 10), testArtifact)
	declare(t, s, "doc", strconv.FormatInt(oldDoc.ID, 10), testArtifact)

	want := []DeclaredEntity{
		{Kind: "deliverable", ID: del.ID},
		{Kind: "doc", ID: strconv.FormatInt(draftDoc.ID, 10)},
		{Kind: "task", ID: openTask.ID},
	}
	got := openDeclarations(t, s, testArtifact)
	if len(got) != len(want) {
		t.Fatalf("open declarations = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("open declarations = %+v, want %+v", got, want)
		}
	}
}

// TestOpenDeclarationsForArtifactMatchesExactly pins the comparison the hook
// contract promises: the address is matched byte for byte, so a differently
// cased or schemed spelling is a different artifact.
func TestOpenDeclarationsForArtifactMatchesExactly(t *testing.T) {
	s := openTaskStore(t)
	d, err := createDeliverable(s, DeliverableInput{ProjectID: "horndb", Name: "casualties"})
	if err != nil {
		t.Fatalf("create deliverable: %v", err)
	}
	declare(t, s, "deliverable", d.ID, testArtifact)

	for _, other := range []string{
		"BigQuery://sunstone-prod/cow/casualties",
		"bigquery://sunstone-prod/cow/Casualties",
		"gs://sunstone-prod/cow/casualties",
	} {
		if got := openDeclarations(t, s, other); len(got) != 0 {
			t.Errorf("%q routed to %+v, want no match", other, got)
		}
	}
}

// TestLatestArtifactEvidenceOrdering pins that the newest fact wins by the
// emitter's own clock, with id as the tiebreak for two stamped the same
// instant — and that an entity nobody reported on reads as nil, not an error.
func TestLatestArtifactEvidenceOrdering(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()
	d, err := createDeliverable(s, DeliverableInput{ProjectID: "horndb", Name: "casualties"})
	if err != nil {
		t.Fatalf("create deliverable: %v", err)
	}

	got, err := s.LatestArtifactEvidence(ctx, "deliverable", d.ID)
	if err != nil || got != nil {
		t.Fatalf("LatestArtifactEvidence before any report = %+v, %v; want nil, nil", got, err)
	}

	t0 := taskTestNow
	fileEvidence(t, s, evidence("deliverable", d.ID, "published", t0))
	fileEvidence(t, s, evidence("deliverable", d.ID, "updated", t0.Add(time.Hour)))
	// Older by occurred_at but filed last: it must not win.
	fileEvidence(t, s, evidence("deliverable", d.ID, "failed", t0.Add(-time.Hour)))

	got, err = s.LatestArtifactEvidence(ctx, "deliverable", d.ID)
	if err != nil {
		t.Fatalf("LatestArtifactEvidence: %v", err)
	}
	if got.State != "updated" || !got.OccurredAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("latest = %s at %v, want updated at %v", got.State, got.OccurredAt, t0.Add(time.Hour))
	}

	// Same instant as the winner, filed after it: id breaks the tie.
	fileEvidence(t, s, evidence("deliverable", d.ID, "deprecated", t0.Add(time.Hour)))
	got, err = s.LatestArtifactEvidence(ctx, "deliverable", d.ID)
	if err != nil {
		t.Fatalf("LatestArtifactEvidence after tie: %v", err)
	}
	if got.State != "deprecated" {
		t.Fatalf("latest after tie = %s, want deprecated", got.State)
	}
}

// TestInsertArtifactEvidenceDedupesPerEvent pins the UNIQUE key: one event
// files at most one row per entity, so replaying a delivery is a no-op.
func TestInsertArtifactEvidenceDedupesPerEvent(t *testing.T) {
	s := openTaskStore(t)
	d, err := createDeliverable(s, DeliverableInput{ProjectID: "horndb", Name: "casualties"})
	if err != nil {
		t.Fatalf("create deliverable: %v", err)
	}

	var first, second bool
	if _, _, err := s.RecordEvent(t.Context(), "catalog", nextExt(t), "catalog.published", nil,
		func(tx *sql.Tx, eventID int64) error {
			ev := evidence("deliverable", d.ID, "published", taskTestNow)
			if first, err = InsertArtifactEvidence(tx, eventID, ev); err != nil {
				return err
			}
			second, err = InsertArtifactEvidence(tx, eventID, ev)
			return err
		}); err != nil {
		t.Fatalf("record event: %v", err)
	}
	if !first || second {
		t.Fatalf("inserted = %v then %v, want true then false", first, second)
	}
}

// TestCreateDeliverableDeclaresArtifact pins the read projection: the
// declared address comes back on the deliverable, the reported state stays
// empty until an emitter reports, and then it is the newest evidence.
func TestCreateDeliverableDeclaresArtifact(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()
	d, err := createDeliverable(s, DeliverableInput{
		ProjectID: "horndb", Name: "casualties", Artifact: "  " + testArtifact + "  ",
	})
	if err != nil {
		t.Fatalf("create deliverable: %v", err)
	}
	if d.Artifact != testArtifact {
		t.Fatalf("created Artifact = %q, want %q", d.Artifact, testArtifact)
	}

	got, err := s.GetDeliverable(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDeliverable: %v", err)
	}
	if got.Artifact != testArtifact || got.ReportedState != "" || got.ReportedAt != nil {
		t.Fatalf("read back %+v, want artifact %q and no reported state", got, testArtifact)
	}

	fileEvidence(t, s, evidence("deliverable", d.ID, "published", taskTestNow))
	items, err := s.ListDeliverables(ctx, "horndb")
	if err != nil {
		t.Fatalf("ListDeliverables: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("ListDeliverables = %+v, want one", items)
	}
	if items[0].ReportedState != "published" || items[0].ReportedAt == nil ||
		!items[0].ReportedAt.Equal(taskTestNow) {
		t.Fatalf("listed %+v, want published at %v", items[0], taskTestNow)
	}
}
