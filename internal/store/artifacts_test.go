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
	if err := s.db.QueryRow(`SELECT digest, source_sha FROM artifacts WHERE id = $1`, id1).
		Scan(&gotDigest, &gotSourceSHA); err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if gotDigest != digest || gotSourceSHA != "def456" {
		t.Fatalf("artifact after redelivery: digest=%q source_sha=%q, want %q and def456", gotDigest, gotSourceSHA, digest)
	}
}

// TestCreateArtifactNonRegressing covers the guard on artifacts.built_at: a
// stale delivery must not overwrite a newer build, and must still report the
// existing row's id — the guarded DO UPDATE returns no row, so that id comes
// from the fallback SELECT (WL-198).
func TestCreateArtifactNonRegressing(t *testing.T) {
	s := openArtifactsStore(t)

	newDigest := "sha256:newer"
	a := defaultArtifact()
	a.BuiltAt = artifactsTestNow.Add(time.Hour)
	a.Digest = &newDigest
	a.SourceSHA = "newer-sha"
	id, err := createArtifact(t, s, a)
	if err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	staleDigest := "sha256:older"
	stale := defaultArtifact()
	stale.BuiltAt = artifactsTestNow
	stale.Digest = &staleDigest
	stale.SourceSHA = "older-sha"
	staleID, err := createArtifact(t, s, stale)
	if err != nil {
		t.Fatalf("stale CreateArtifact: %v", err)
	}
	if staleID != id {
		t.Fatalf("stale CreateArtifact id: got %d, want the existing %d", staleID, id)
	}

	var gotDigest, gotSourceSHA string
	if err := s.db.QueryRow(`SELECT digest, source_sha FROM artifacts WHERE id = $1`, id).
		Scan(&gotDigest, &gotSourceSHA); err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if gotDigest != newDigest || gotSourceSHA != "newer-sha" {
		t.Fatalf("artifact after stale upsert: digest=%q source_sha=%q, want %q and newer-sha", gotDigest, gotSourceSHA, newDigest)
	}

	// A newer build applies.
	newestDigest := "sha256:newest"
	newest := defaultArtifact()
	newest.BuiltAt = artifactsTestNow.Add(2 * time.Hour)
	newest.Digest = &newestDigest
	newest.SourceSHA = "newest-sha"
	newestID, err := createArtifact(t, s, newest)
	if err != nil {
		t.Fatalf("newer CreateArtifact: %v", err)
	}
	if newestID != id {
		t.Fatalf("newer CreateArtifact id: got %d, want %d", newestID, id)
	}
	if err := s.db.QueryRow(`SELECT digest, source_sha FROM artifacts WHERE id = $1`, id).
		Scan(&gotDigest, &gotSourceSHA); err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if gotDigest != newestDigest || gotSourceSHA != "newest-sha" {
		t.Fatalf("artifact after newer upsert: digest=%q source_sha=%q, want %q and newest-sha", gotDigest, gotSourceSHA, newestDigest)
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

// artifactIDBySourceSHA drives ArtifactIDBySourceSHA inside its own
// RecordEvent transaction, since it is tx-scoped.
func artifactIDBySourceSHA(t *testing.T, s *Store, sha string) *int64 {
	t.Helper()
	var got *int64
	_, _, err := s.RecordEvent(t.Context(), "flux", nextExt(t), "lookup", nil,
		func(tx *sql.Tx, _ int64) error {
			var err error
			got, err = ArtifactIDBySourceSHA(tx, sha)
			return err
		})
	if err != nil {
		t.Fatalf("ArtifactIDBySourceSHA: %v", err)
	}
	return got
}

func TestArtifactIDBySourceSHANoneFound(t *testing.T) {
	s := openArtifactsStore(t)
	got := artifactIDBySourceSHA(t, s, "nonexistent")
	if got != nil {
		t.Fatalf("ArtifactIDBySourceSHA nonexistent: got %v, want nil", got)
	}
}

func TestArtifactIDBySourceSHANewestWins(t *testing.T) {
	s := openArtifactsStore(t)
	a1 := defaultArtifact()
	id1, err := createArtifact(t, s, a1)
	if err != nil {
		t.Fatalf("createArtifact a1: %v", err)
	}
	a2 := defaultArtifact()
	a2.Version = "v2.0.0"
	id2, err := createArtifact(t, s, a2)
	if err != nil {
		t.Fatalf("createArtifact a2: %v", err)
	}
	if id2 <= id1 {
		t.Fatalf("test setup: expected id2 > id1, got id1=%d id2=%d", id1, id2)
	}

	got := artifactIDBySourceSHA(t, s, a1.SourceSHA)
	if got == nil || *got != id2 {
		t.Fatalf("ArtifactIDBySourceSHA: got %v, want newest id %d", got, id2)
	}
}

// artifactByDigest drives ArtifactByDigest inside its own RecordEvent
// transaction, since it is tx-scoped.
func artifactByDigest(t *testing.T, s *Store, digest string) *Artifact {
	t.Helper()
	var got *Artifact
	_, _, err := s.RecordEvent(t.Context(), "flux", nextExt(t), "lookup", nil,
		func(tx *sql.Tx, _ int64) error {
			var err error
			got, err = ArtifactByDigest(tx, digest)
			return err
		})
	if err != nil {
		t.Fatalf("ArtifactByDigest: %v", err)
	}
	return got
}

func TestArtifactByDigest(t *testing.T) {
	s := openArtifactsStore(t)
	digest := "sha256:feed00"
	a := defaultArtifact()
	a.Digest = &digest
	if _, err := createArtifact(t, s, a); err != nil {
		t.Fatalf("createArtifact: %v", err)
	}

	got := artifactByDigest(t, s, digest)
	if got == nil || got.SourceSHA != a.SourceSHA {
		t.Fatalf("ArtifactByDigest = %+v, want the seeded artifact", got)
	}
}

func TestArtifactByDigestNoneFound(t *testing.T) {
	s := openArtifactsStore(t)
	got := artifactByDigest(t, s, "sha256:absent")
	if got != nil {
		t.Fatalf("ArtifactByDigest = %+v, want nil", got)
	}
}

// deploymentStatus drives DeploymentStatus inside its own RecordEvent
// transaction, since it is tx-scoped.
func deploymentStatus(t *testing.T, s *Store, environment, targetKind, targetName string) string {
	t.Helper()
	var got string
	_, _, err := s.RecordEvent(t.Context(), "flux", nextExt(t), "lookup", nil,
		func(tx *sql.Tx, _ int64) error {
			var err error
			got, err = DeploymentStatus(tx, environment, targetKind, targetName)
			return err
		})
	if err != nil {
		t.Fatalf("DeploymentStatus: %v", err)
	}
	return got
}

func TestDeploymentStatusNoneFound(t *testing.T) {
	s := openArtifactsStore(t)
	got := deploymentStatus(t, s, "prod", "flux_kustomization", "demo/demo")
	if got != "" {
		t.Fatalf("DeploymentStatus nonexistent: got %q, want empty", got)
	}
}

func TestDeploymentStatusFound(t *testing.T) {
	s := openArtifactsStore(t)
	d := defaultDeployment()
	d.Status = "failed"
	if err := upsertDeployment(t, s, artifactsTestNow, d); err != nil {
		t.Fatalf("upsertDeployment: %v", err)
	}
	got := deploymentStatus(t, s, d.Environment, d.TargetKind, d.TargetName)
	if got != "failed" {
		t.Fatalf("DeploymentStatus: got %q, want failed", got)
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

// TestArtifactsBySourceSHAs covers the bulk reader: one query answers every
// sha, and each group matches what ArtifactsBySourceSHA returns.
func TestArtifactsBySourceSHAs(t *testing.T) {
	s := openArtifactsStore(t)

	a1 := defaultArtifact()
	a2 := defaultArtifact()
	a2.Kind = "git_tag"
	a2.Name = "release"
	a3 := defaultArtifact()
	a3.Version = "v2.0.0"
	a3.SourceSHA = "other-sha"
	for _, a := range []Artifact{a1, a2, a3} {
		if _, err := createArtifact(t, s, a); err != nil {
			t.Fatalf("createArtifact: %v", err)
		}
	}

	shas := []string{"abc123", "other-sha", "absent-sha"}
	got, err := s.ArtifactsBySourceSHAs(t.Context(), shas)
	if err != nil {
		t.Fatalf("ArtifactsBySourceSHAs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ArtifactsBySourceSHAs: got %d keys, want 2 (absent-sha must be absent)", len(got))
	}
	for _, sha := range shas {
		want, err := s.ArtifactsBySourceSHA(t.Context(), sha)
		if err != nil {
			t.Fatalf("ArtifactsBySourceSHA %s: %v", sha, err)
		}
		if !reflect.DeepEqual(got[sha], want) {
			t.Fatalf("ArtifactsBySourceSHAs[%s] = %v, want %v", sha, got[sha], want)
		}
	}

	empty, err := s.ArtifactsBySourceSHAs(t.Context(), nil)
	if err != nil {
		t.Fatalf("ArtifactsBySourceSHAs(nil): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("ArtifactsBySourceSHAs(nil): got %v, want empty non-nil map", empty)
	}
}

// TestDeploymentsForArtifacts covers the bulk reader: one query answers every
// artifact id, and each group matches what DeploymentsForArtifact returns.
func TestDeploymentsForArtifacts(t *testing.T) {
	s := openArtifactsStore(t)

	first := defaultArtifact()
	firstID, err := createArtifact(t, s, first)
	if err != nil {
		t.Fatalf("createArtifact first: %v", err)
	}
	second := defaultArtifact()
	second.Name = "sunstoneinstitute/other"
	second.SourceSHA = "other-sha"
	secondID, err := createArtifact(t, s, second)
	if err != nil {
		t.Fatalf("createArtifact second: %v", err)
	}
	bare := defaultArtifact()
	bare.Name = "sunstoneinstitute/bare"
	bare.SourceSHA = "bare-sha"
	bareID, err := createArtifact(t, s, bare)
	if err != nil {
		t.Fatalf("createArtifact bare: %v", err)
	}

	// Deployments are keyed by (environment, target_kind, target_name), so
	// each row needs its own target to stay distinct.
	for i, spec := range []struct {
		artifactID  int64
		environment string
		target      string
		offset      time.Duration
	}{
		{firstID, "prod", "demo/one", 2 * time.Minute},
		{firstID, "dev", "demo/two", time.Minute},
		{secondID, "prod", "demo/three", time.Minute},
	} {
		artifactID := spec.artifactID
		d := defaultDeployment()
		d.ArtifactID = &artifactID
		d.Environment = spec.environment
		d.TargetName = spec.target
		if err := upsertDeployment(t, s, artifactsTestNow.Add(spec.offset), d); err != nil {
			t.Fatalf("upsertDeployment %d: %v", i, err)
		}
	}

	ids := []int64{firstID, secondID, bareID}
	got, err := s.DeploymentsForArtifacts(t.Context(), ids)
	if err != nil {
		t.Fatalf("DeploymentsForArtifacts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("DeploymentsForArtifacts: got %d keys, want 2 (bare artifact must be absent)", len(got))
	}
	for _, id := range ids {
		want, err := s.DeploymentsForArtifact(t.Context(), id)
		if err != nil {
			t.Fatalf("DeploymentsForArtifact %d: %v", id, err)
		}
		if !reflect.DeepEqual(got[id], want) {
			t.Fatalf("DeploymentsForArtifacts[%d] = %v, want %v", id, got[id], want)
		}
	}

	empty, err := s.DeploymentsForArtifacts(t.Context(), nil)
	if err != nil {
		t.Fatalf("DeploymentsForArtifacts(nil): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("DeploymentsForArtifacts(nil): got %v, want empty non-nil map", empty)
	}
}
