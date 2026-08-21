package derive_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// seedArtifactAndDeployment seeds one docker_image artifact built from a
// known main commit, a release frontier cutting from that same commit, and
// one prod deployment referencing the artifact — through the exported store
// API (store.Store.Tx plus the tx-scoped functions artifacts_test.go and
// delivery_test.go exercise), the way a real webhook apply would.
func seedArtifactAndDeployment(t *testing.T, s *store.Store) int64 {
	t.Helper()
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	sha := "deadbeef1234567890deadbeef1234567890dead"

	var artifactID int64
	err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		mainID, err := store.AppendMainCommit(tx, "acme/app", sha, now)
		if err != nil {
			return err
		}
		if err := store.SetReleaseFrontier(tx, "acme/app", "v1.0.0", mainID, now); err != nil {
			return err
		}
		artifactID, err = store.CreateArtifact(tx, store.Artifact{
			Kind:      "docker_image",
			Name:      "acme/app",
			Version:   "v1.0.0",
			Repo:      "acme/app",
			SourceSHA: sha,
			BuiltAt:   now,
		})
		if err != nil {
			return err
		}
		return store.UpsertDeployment(tx, now, store.Deployment{
			ArtifactID:  &artifactID,
			Environment: "prod",
			TargetKind:  "flux_kustomization",
			TargetName:  "app",
			Status:      "deployed",
		})
	})
	if err != nil {
		t.Fatalf("seed artifact and deployment: %v", err)
	}
	return artifactID
}

func TestDeployTriplesProjectsRows(t *testing.T) {
	s := store.OpenTestStore(t)
	seedArtifactAndDeployment(t, s)

	doc, err := derive.DeployTriples(context.Background(), s)
	if err != nil {
		t.Fatalf("DeployTriples: %v", err)
	}
	got := string(doc)
	for _, want := range []string{
		"ontology#Deployment", "ontology#Artifact",
		"id/environment/prod", "id/environment/dev",
		"ontology#toEnvironment", "ontology#cutFrom",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestDeployTriplesDeterministic(t *testing.T) {
	s := store.OpenTestStore(t)
	seedArtifactAndDeployment(t, s)

	first, err := derive.DeployTriples(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		next, err := derive.DeployTriples(context.Background(), s)
		if err != nil {
			t.Fatal(err)
		}
		if string(next) != string(first) {
			t.Fatalf("re-deriving unchanged rows is not byte-identical on call %d", i+2)
		}
	}
}
