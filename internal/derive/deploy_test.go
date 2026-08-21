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

const (
	knownSHA   = "deadbeef1234567890deadbeef1234567890dead"
	unknownSHA = "facefeed1234567890facefeed1234567890face"
)

var deployTestNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// seedArtifactAndDeployment seeds one docker_image artifact built from a
// known main commit, a release frontier cutting from that same commit, and
// one prod deployment referencing the artifact. It writes the tables
// directly inside one store.Store.Tx, via the tx-scoped functions
// artifacts_test.go and delivery_test.go exercise — not through
// store.RecordEvent, so no events rows exist. The derivers project the fact
// tables and never read the event log, so the shortcut is faithful to what
// they consume.
func seedArtifactAndDeployment(t *testing.T, s *store.Store) int64 {
	t.Helper()
	now := deployTestNow

	var artifactID int64
	err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		mainID, err := store.AppendMainCommit(tx, "acme/app", knownSHA, now)
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
			SourceSHA: knownSHA,
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

// seedArtifact writes one more artifact (and optionally a deployment of it)
// the same direct way, so a test can put several rows in front of the
// deriver's map iteration.
func seedArtifact(t *testing.T, s *store.Store, a store.Artifact, env string) int64 {
	t.Helper()
	var id int64
	err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		var err error
		id, err = store.CreateArtifact(tx, a)
		if err != nil {
			return err
		}
		if env == "" {
			return nil
		}
		return store.UpsertDeployment(tx, deployTestNow, store.Deployment{
			ArtifactID:  &id,
			Environment: env,
			TargetKind:  "flux_kustomization",
			TargetName:  a.Version,
			Status:      "deployed",
		})
	})
	if err != nil {
		t.Fatalf("seed artifact %s: %v", a.Version, err)
	}
	return id
}

func TestDeployTriplesProjectsRows(t *testing.T) {
	s := store.OpenTestStore(t)
	seedArtifactAndDeployment(t, s)
	// A second artifact built from a sha main_commits has never seen: the
	// commit guard (006 §11.1) must mint nothing for it.
	seedArtifact(t, s, store.Artifact{
		Kind:      "docker_image",
		Name:      "acme/app",
		Version:   "v0.9.0-rc1",
		Repo:      "acme/app",
		SourceSHA: unknownSHA,
		BuiltAt:   deployTestNow,
	}, "")

	doc, err := derive.DeployTriples(context.Background(), s)
	if err != nil {
		t.Fatalf("DeployTriples: %v", err)
	}
	got := string(doc)
	for _, want := range []string{
		"ontology#Deployment", "ontology#Artifact",
		"id/environment/prod", "id/environment/dev",
		"ontology#toEnvironment", "ontology#cutFrom",
		// The guarded commit edge and the commit node it points at. Without
		// these two, the whole file passes with a commit guard wired to
		// return false for everything — which is exactly what a swallowed
		// database error looks like.
		"prov#wasDerivedFrom", "ontology#Commit",
		"id/commit/github.com/acme/app/" + knownSHA,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, unknownSHA) {
		t.Errorf("minted a commit IRI for a sha absent from main_commits:\n%s", got)
	}
}

func TestDeployTriplesDeterministic(t *testing.T) {
	s := store.OpenTestStore(t)
	seedArtifactAndDeployment(t, s)
	// DeployTriples ranges the map AllArtifactsByID returns, and the
	// deployments it projects hang off those artifacts. One of each gives map
	// iteration nothing to randomise, so the test would still pass if
	// graphproj.Document stopped sorting; several of each is what makes this
	// a real check on the sort.
	for i, v := range []string{"v1.1.0", "v1.2.0", "v1.3.0"} {
		seedArtifact(t, s, store.Artifact{
			Kind:      "docker_image",
			Name:      "acme/app",
			Version:   v,
			Repo:      "acme/app",
			SourceSHA: knownSHA,
			BuiltAt:   deployTestNow.Add(time.Duration(i) * time.Hour),
		}, []string{"dev", "prod", "dev"}[i])
	}

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
