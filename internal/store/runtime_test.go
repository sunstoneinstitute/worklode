package store

import (
	"database/sql"
	"testing"
	"time"
)

var runtimeTestNow = time.Date(2026, 7, 19, 15, 0, 0, 0, time.UTC)

func openRuntimeStore(t *testing.T) *Store {
	t.Helper()
	return openTaskStore(t)
}

// insertRuntimeEvent drives InsertRuntimeEvent through RecordEvent, source
// "watcher".
func insertRuntimeEvent(t *testing.T, s *Store, re RuntimeEvent) (int64, error) {
	t.Helper()
	var id int64
	_, _, err := s.RecordEvent(t.Context(), "watcher", nextExt(t), "pod.crashloop", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			id, err = InsertRuntimeEvent(tx, re)
			return err
		})
	if err != nil {
		return 0, err
	}
	return id, nil
}

func defaultRuntimeEvent() RuntimeEvent {
	return RuntimeEvent{
		Cluster:    "prod-cluster",
		Kind:       "crashloop",
		Workload:   "demo",
		Image:      "registry.example.com/sunstone/demo:v1.2.3",
		Message:    "back-off restarting failed container",
		OccurredAt: runtimeTestNow,
	}
}

func TestInsertRuntimeEventResolvesArtifact(t *testing.T) {
	s := openRuntimeStore(t)
	a := Artifact{
		Kind:      "docker_image",
		Name:      "registry.example.com/sunstone/demo",
		Version:   "v1.2.3",
		SourceSHA: "abc123",
		BuiltAt:   artifactsTestNow,
	}
	artifactID, err := createArtifact(t, s, a)
	if err != nil {
		t.Fatalf("createArtifact: %v", err)
	}

	re := defaultRuntimeEvent()
	id, err := insertRuntimeEvent(t, s, re)
	if err != nil {
		t.Fatalf("InsertRuntimeEvent: %v", err)
	}

	list, err := s.ListRuntimeEvents(t.Context(), "", 0)
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListRuntimeEvents: got %d, want 1", len(list))
	}
	got := list[0]
	if got.ID != id {
		t.Fatalf("runtime event id: got %d, want %d", got.ID, id)
	}
	if got.ArtifactID == nil || *got.ArtifactID != artifactID {
		t.Fatalf("runtime event artifact_id: got %v, want %d", got.ArtifactID, artifactID)
	}
	if got.Cluster != re.Cluster || got.Kind != re.Kind || got.Workload != re.Workload ||
		got.Image != re.Image || got.Message != re.Message {
		t.Fatalf("runtime event fields: got %+v, want fields matching %+v", got, re)
	}
	if !got.OccurredAt.Equal(re.OccurredAt) {
		t.Fatalf("runtime event occurred_at: got %v, want %v", got.OccurredAt, re.OccurredAt)
	}
}

func TestInsertRuntimeEventNoArtifactMatch(t *testing.T) {
	s := openRuntimeStore(t)
	re := defaultRuntimeEvent()
	re.Image = "registry.example.com/sunstone/unknown:v9.9.9"

	if _, err := insertRuntimeEvent(t, s, re); err != nil {
		t.Fatalf("InsertRuntimeEvent: %v", err)
	}

	list, err := s.ListRuntimeEvents(t.Context(), "", 0)
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListRuntimeEvents: got %d, want 1", len(list))
	}
	if list[0].ArtifactID != nil {
		t.Fatalf("runtime event artifact_id: got %v, want nil (no match)", list[0].ArtifactID)
	}
}

func TestInsertRuntimeEventExplicitArtifactIDNotOverridden(t *testing.T) {
	s := openRuntimeStore(t)
	a := Artifact{
		Kind:      "docker_image",
		Name:      "registry.example.com/sunstone/demo",
		Version:   "v1.2.3",
		SourceSHA: "abc123",
		BuiltAt:   artifactsTestNow,
	}
	matchingID, err := createArtifact(t, s, a)
	if err != nil {
		t.Fatalf("createArtifact: %v", err)
	}
	a2 := a
	a2.Version = "v0.0.1"
	explicitID, err := createArtifact(t, s, a2)
	if err != nil {
		t.Fatalf("createArtifact a2: %v", err)
	}

	re := defaultRuntimeEvent() // image tag v1.2.3, which would resolve to matchingID
	re.ArtifactID = &explicitID
	id, err := insertRuntimeEvent(t, s, re)
	if err != nil {
		t.Fatalf("InsertRuntimeEvent: %v", err)
	}
	_ = matchingID

	list, err := s.ListRuntimeEvents(t.Context(), "", 0)
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	var got *RuntimeEvent
	for i := range list {
		if list[i].ID == id {
			got = &list[i]
		}
	}
	if got == nil {
		t.Fatalf("runtime event %d not found in list", id)
	}
	if got.ArtifactID == nil || *got.ArtifactID != explicitID {
		t.Fatalf("runtime event artifact_id: got %v, want explicit %d (must not be overridden by image resolution)", got.ArtifactID, explicitID)
	}
}

func TestListRuntimeEventsClusterFilterAndOrder(t *testing.T) {
	s := openRuntimeStore(t)

	re1 := defaultRuntimeEvent()
	re1.Cluster = "prod-cluster"
	re1.OccurredAt = runtimeTestNow

	re2 := defaultRuntimeEvent()
	re2.Cluster = "prod-cluster"
	re2.OccurredAt = runtimeTestNow.Add(time.Minute)

	re3 := defaultRuntimeEvent()
	re3.Cluster = "dev-cluster"
	re3.OccurredAt = runtimeTestNow.Add(2 * time.Minute)

	for _, re := range []RuntimeEvent{re1, re2, re3} {
		if _, err := insertRuntimeEvent(t, s, re); err != nil {
			t.Fatalf("InsertRuntimeEvent: %v", err)
		}
	}

	all, err := s.ListRuntimeEvents(t.Context(), "", 0)
	if err != nil {
		t.Fatalf("ListRuntimeEvents all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListRuntimeEvents all: got %d, want 3", len(all))
	}
	// Newest first.
	if !all[0].OccurredAt.Equal(re3.OccurredAt) || !all[2].OccurredAt.Equal(re1.OccurredAt) {
		t.Fatalf("ListRuntimeEvents order: got occurred_at %v, %v, %v (want newest first)",
			all[0].OccurredAt, all[1].OccurredAt, all[2].OccurredAt)
	}

	prodOnly, err := s.ListRuntimeEvents(t.Context(), "prod-cluster", 0)
	if err != nil {
		t.Fatalf("ListRuntimeEvents prod: %v", err)
	}
	if len(prodOnly) != 2 {
		t.Fatalf("ListRuntimeEvents prod-cluster: got %d, want 2", len(prodOnly))
	}

	limited, err := s.ListRuntimeEvents(t.Context(), "", 1)
	if err != nil {
		t.Fatalf("ListRuntimeEvents limit 1: %v", err)
	}
	if len(limited) != 1 || !limited[0].OccurredAt.Equal(re3.OccurredAt) {
		t.Fatalf("ListRuntimeEvents limit 1: got %+v, want just the newest", limited)
	}
}

func TestListRuntimeEventsDefaultLimit(t *testing.T) {
	s := openRuntimeStore(t)
	for i := 0; i < 55; i++ {
		re := defaultRuntimeEvent()
		re.OccurredAt = runtimeTestNow.Add(time.Duration(i) * time.Second)
		if _, err := insertRuntimeEvent(t, s, re); err != nil {
			t.Fatalf("InsertRuntimeEvent %d: %v", i, err)
		}
	}
	got, err := s.ListRuntimeEvents(t.Context(), "", 0)
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	if len(got) != 50 {
		t.Fatalf("ListRuntimeEvents default limit: got %d, want 50", len(got))
	}
}
