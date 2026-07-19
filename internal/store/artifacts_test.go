package store

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

var artifactsTestNow = time.Date(2026, 7, 19, 14, 0, 0, 0, time.UTC)

func openArtifactsStore(t *testing.T) *Store {
	t.Helper()
	return openTaskStore(t)
}

// createArtifact drives CreateArtifact through RecordEvent, source "github".
func createArtifact(t *testing.T, s *Store, a Artifact) (int64, error) {
	t.Helper()
	var id int64
	_, _, err := s.RecordEvent(t.Context(), "github", nextExt(t), "release.published", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			id, err = CreateArtifact(tx, a)
			return err
		})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// upsertDeployment drives UpsertDeployment through RecordEvent, source "flux".
func upsertDeployment(t *testing.T, s *Store, now time.Time, d Deployment) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "flux", nextExt(t), "kustomization.applied", nil,
		func(tx *sql.Tx, eventID int64) error {
			return UpsertDeployment(tx, now, d)
		})
	return err
}

func defaultArtifact() Artifact {
	return Artifact{
		Kind:      "docker_image",
		Name:      "sunstoneinstitute/demo",
		Version:   "v1.2.3",
		Repo:      "sunstoneinstitute/demo",
		SourceSHA: "abc123",
		BuiltAt:   artifactsTestNow,
	}
}

func TestCreateArtifactUpsertReturnsSameID(t *testing.T) {
	s := openArtifactsStore(t)
	a := defaultArtifact()

	id1, err := createArtifact(t, s, a)
	if err != nil {
		t.Fatalf("first CreateArtifact: %v", err)
	}

	// Redelivery with a different digest/source_sha updates in place and
	// returns the same id.
	a2 := a
	digest := "sha256:deadbeef"
	a2.Digest = &digest
	a2.SourceSHA = "def456"
	id2, err := createArtifact(t, s, a2)
	if err != nil {
		t.Fatalf("second CreateArtifact: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("CreateArtifact redelivery id: got %d, want %d", id2, id1)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM artifacts`).Scan(&count); err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	if count != 1 {
		t.Fatalf("artifacts count: got %d, want 1", count)
	}

	var gotDigest, gotSourceSHA string
	if err := s.db.QueryRow(`SELECT digest, source_sha FROM artifacts WHERE id = ?`, id1).
		Scan(&gotDigest, &gotSourceSHA); err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if gotDigest != digest || gotSourceSHA != "def456" {
		t.Fatalf("artifact after redelivery: digest=%q source_sha=%q, want %q and def456", gotDigest, gotSourceSHA, digest)
	}
}

func TestArtifactsBySourceSHA(t *testing.T) {
	s := openArtifactsStore(t)
	a1 := defaultArtifact()
	a2 := defaultArtifact()
	a2.Kind = "git_tag"
	a2.Name = "release"
	a2.Version = "v1.2.3"
	a3 := defaultArtifact()
	a3.Version = "v2.0.0"
	a3.SourceSHA = "other-sha"

	for _, a := range []Artifact{a1, a2, a3} {
		if _, err := createArtifact(t, s, a); err != nil {
			t.Fatalf("createArtifact: %v", err)
		}
	}

	got, err := s.ArtifactsBySourceSHA(t.Context(), "abc123")
	if err != nil {
		t.Fatalf("ArtifactsBySourceSHA: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ArtifactsBySourceSHA: got %d artifacts, want 2", len(got))
	}

	none, err := s.ArtifactsBySourceSHA(t.Context(), "nonexistent")
	if err != nil {
		t.Fatalf("ArtifactsBySourceSHA nonexistent: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("ArtifactsBySourceSHA nonexistent: got %d, want 0", len(none))
	}
}

func TestFindArtifactByImage(t *testing.T) {
	s := openArtifactsStore(t)
	a := defaultArtifact()
	a.Name = "registry.example.com/sunstone/demo"
	a.Version = "v1.2.3"
	if _, err := createArtifact(t, s, a); err != nil {
		t.Fatalf("createArtifact: %v", err)
	}

	got, err := s.FindArtifactByImage(t.Context(), "registry.example.com/sunstone/demo:v1.2.3")
	if err != nil {
		t.Fatalf("FindArtifactByImage: %v", err)
	}
	if got.Name != a.Name || got.Version != a.Version || got.Kind != "docker_image" {
		t.Fatalf("FindArtifactByImage: got %+v", got)
	}

	if _, err := s.FindArtifactByImage(t.Context(), "registry.example.com/sunstone/demo:v9.9.9"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FindArtifactByImage wrong tag: want ErrNotFound, got %v", err)
	}
	if _, err := s.FindArtifactByImage(t.Context(), "registry.example.com/other/image:v1.2.3"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FindArtifactByImage wrong name: want ErrNotFound, got %v", err)
	}
	if _, err := s.FindArtifactByImage(t.Context(), "no-tag-here"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FindArtifactByImage no tag: want ErrNotFound, got %v", err)
	}
}

func defaultDeployment() Deployment {
	return Deployment{
		Environment: "prod",
		TargetKind:  "flux_kustomization",
		TargetName:  "demo/demo",
		Status:      "deployed",
	}
}

func TestUpsertDeploymentInsertAndUpdate(t *testing.T) {
	s := openArtifactsStore(t)
	d := defaultDeployment()

	if err := upsertDeployment(t, s, artifactsTestNow, d); err != nil {
		t.Fatalf("first UpsertDeployment: %v", err)
	}

	list, err := s.ListDeployments(t.Context(), "")
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListDeployments: got %d, want 1", len(list))
	}
	got := list[0]
	if !got.FirstSeen.Equal(artifactsTestNow) || !got.LastUpdate.Equal(artifactsTestNow) {
		t.Fatalf("first insert timestamps: got first_seen=%v last_update=%v, want both %v",
			got.FirstSeen, got.LastUpdate, artifactsTestNow)
	}
	if got.ArtifactID != nil {
		t.Fatalf("artifact_id: got %v, want nil", got.ArtifactID)
	}

	// Update: status changes, artifact gets linked, last_update advances,
	// but first_seen is preserved.
	artifactID, err := createArtifact(t, s, defaultArtifact())
	if err != nil {
		t.Fatalf("createArtifact: %v", err)
	}
	later := artifactsTestNow.Add(10 * time.Minute)
	d2 := d
	d2.Status = "reconciling"
	d2.ArtifactID = &artifactID
	if err := upsertDeployment(t, s, later, d2); err != nil {
		t.Fatalf("second UpsertDeployment: %v", err)
	}

	list, err = s.ListDeployments(t.Context(), "")
	if err != nil {
		t.Fatalf("ListDeployments after update: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListDeployments after update: got %d, want 1", len(list))
	}
	got = list[0]
	if got.Status != "reconciling" {
		t.Fatalf("status after update: got %q, want reconciling", got.Status)
	}
	if got.ArtifactID == nil || *got.ArtifactID != artifactID {
		t.Fatalf("artifact_id after update: got %v, want %d", got.ArtifactID, artifactID)
	}
	if !got.FirstSeen.Equal(artifactsTestNow) {
		t.Fatalf("first_seen after update: got %v, want preserved %v", got.FirstSeen, artifactsTestNow)
	}
	if !got.LastUpdate.Equal(later) {
		t.Fatalf("last_update after update: got %v, want %v", got.LastUpdate, later)
	}
}

func TestUpsertDeploymentNilArtifactIDPreservesLink(t *testing.T) {
	s := openArtifactsStore(t)

	artifactID, err := createArtifact(t, s, defaultArtifact())
	if err != nil {
		t.Fatalf("createArtifact: %v", err)
	}
	d := defaultDeployment()
	d.ArtifactID = &artifactID
	if err := upsertDeployment(t, s, artifactsTestNow, d); err != nil {
		t.Fatalf("first UpsertDeployment: %v", err)
	}

	// Status-only redelivery (image not resolved → nil ArtifactID) must not
	// sever the previously resolved artifact link.
	later := artifactsTestNow.Add(5 * time.Minute)
	d2 := defaultDeployment()
	d2.Status = "failed"
	d2.ArtifactID = nil
	if err := upsertDeployment(t, s, later, d2); err != nil {
		t.Fatalf("second UpsertDeployment: %v", err)
	}

	list, err := s.ListDeployments(t.Context(), "")
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListDeployments: got %d, want 1", len(list))
	}
	got := list[0]
	if got.Status != "failed" {
		t.Fatalf("status after nil-artifact update: got %q, want failed", got.Status)
	}
	if got.ArtifactID == nil || *got.ArtifactID != artifactID {
		t.Fatalf("artifact_id after nil-artifact update: got %v, want preserved %d", got.ArtifactID, artifactID)
	}
	if !got.LastUpdate.Equal(later) {
		t.Fatalf("last_update after nil-artifact update: got %v, want %v", got.LastUpdate, later)
	}
}

func TestListDeploymentsFilter(t *testing.T) {
	s := openArtifactsStore(t)
	dProd := defaultDeployment()
	dDev := defaultDeployment()
	dDev.Environment = "dev"

	if err := upsertDeployment(t, s, artifactsTestNow, dProd); err != nil {
		t.Fatalf("upsert prod: %v", err)
	}
	if err := upsertDeployment(t, s, artifactsTestNow, dDev); err != nil {
		t.Fatalf("upsert dev: %v", err)
	}

	all, err := s.ListDeployments(t.Context(), "")
	if err != nil {
		t.Fatalf("ListDeployments all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListDeployments all: got %d, want 2", len(all))
	}

	prodOnly, err := s.ListDeployments(t.Context(), "prod")
	if err != nil {
		t.Fatalf("ListDeployments prod: %v", err)
	}
	if len(prodOnly) != 1 || prodOnly[0].Environment != "prod" {
		t.Fatalf("ListDeployments prod: got %+v", prodOnly)
	}
}

func TestArtifactsBySourceSHAKindOrder(t *testing.T) {
	s := openArtifactsStore(t)
	a := defaultArtifact()
	id, err := createArtifact(t, s, a)
	if err != nil {
		t.Fatalf("createArtifact: %v", err)
	}
	got, err := s.ArtifactsBySourceSHA(t.Context(), a.SourceSHA)
	if err != nil {
		t.Fatalf("ArtifactsBySourceSHA: %v", err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("ArtifactsBySourceSHA: got %+v, want id %d", got, id)
	}
	if !reflect.DeepEqual(got[0].Kind, a.Kind) {
		t.Fatalf("ArtifactsBySourceSHA kind: got %q, want %q", got[0].Kind, a.Kind)
	}
}
