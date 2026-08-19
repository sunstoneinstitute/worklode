package graphproj

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

func testArtifact() store.Artifact {
	digest := "sha256:8f3c1a2b"
	return store.Artifact{
		Kind:      "docker_image",
		Name:      "ghcr.io/sunstoneinstitute/graph-server",
		Version:   "v1",
		Digest:    &digest,
		Repo:      "sunstoneinstitute/graph-server",
		SourceSHA: "a16c2a7",
		BuiltAt:   time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
	}
}

func TestArtifactTriples(t *testing.T) {
	got := string(Document(ArtifactTriples(testArtifact(), func(string, string) bool { return true })))
	want := []string{
		`<https://worklode.io/ns/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/ontology#Artifact> .`,
		`<https://worklode.io/ns/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <https://worklode.io/ns/ontology#artifactKind> <https://worklode.io/ns/concept/docker_image> .`,
		`<https://worklode.io/ns/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <http://www.w3.org/2002/07/owl#versionInfo> "v1" .`,
		`<https://worklode.io/ns/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <http://purl.org/dc/terms/identifier> "ghcr.io/sunstoneinstitute/graph-server" .`,
		`<https://worklode.io/ns/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <https://worklode.io/ns/ontology#digest> "sha256:8f3c1a2b" .`,
		`<https://worklode.io/ns/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <http://www.w3.org/ns/prov#generatedAtTime> "2026-07-28T12:00:00Z"^^<http://www.w3.org/2001/XMLSchema#dateTime> .`,
		`<https://worklode.io/ns/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <http://www.w3.org/ns/prov#wasDerivedFrom> <https://worklode.io/ns/id/commit/github.com/sunstoneinstitute/graph-server/a16c2a7> .`,
	}
	for _, line := range want {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("missing line:\n%s\ngot:\n%s", line, got)
		}
	}
	if n := strings.Count(got, "\n"); n != len(want) {
		t.Errorf("rendered %d lines; want %d:\n%s", n, len(want), got)
	}
}

// AC3: re-projecting an unchanged row is a byte-identical no-op.
func TestArtifactProjectionIsIdempotent(t *testing.T) {
	known := func(string, string) bool { return true }
	first := Document(ArtifactTriples(testArtifact(), known))
	second := Document(ArtifactTriples(testArtifact(), known))
	if !bytes.Equal(first, second) {
		t.Fatal("re-projecting an unchanged artifact row changed bytes")
	}
}

// AC8: a release whose target_commitish is a branch name (unresolvable sha)
// projects no prov:wasDerivedFrom edge rather than a fabricated commit node.
func TestBranchNameProjectsNoCommitEdge(t *testing.T) {
	a := testArtifact()
	a.Kind = "git_tag"
	a.Name = "sunstoneinstitute/worklode"
	a.SourceSHA = "main" // UI-created release: branch name, not a sha
	got := string(Document(ArtifactTriples(a, func(string, string) bool { return false })))
	if strings.Contains(got, "wasDerivedFrom") {
		t.Fatalf("branch-name source_sha minted a commit edge:\n%s", got)
	}
	// The git_tag coordinate is host-qualified (006 §10.1 example).
	if !strings.Contains(got, "<https://worklode.io/ns/id/artifact/git_tag/github.com/sunstoneinstitute/worklode/v1>") {
		t.Fatalf("git_tag artifact IRI not host-qualified:\n%s", got)
	}
}

func TestArtifactWithoutRepoProjectsNoCommitEdge(t *testing.T) {
	a := testArtifact()
	a.Repo = ""
	got := string(Document(ArtifactTriples(a, func(string, string) bool { return true })))
	if strings.Contains(got, "wasDerivedFrom") {
		t.Fatal("artifact without a repo projected a commit edge")
	}
}

func TestDeploymentTriples(t *testing.T) {
	artifactID := int64(1)
	d := store.Deployment{
		ArtifactID:  &artifactID,
		Environment: "prod",
		TargetKind:  "flux_kustomization",
		TargetName:  "graph-server",
		Status:      "deployed",
		FirstSeen:   time.Date(2026, 7, 28, 12, 5, 0, 0, time.UTC),
		LastUpdate:  time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC),
	}
	a := testArtifact()
	got := string(Document(DeploymentTriples(d, &a)))
	want := []string{
		`<https://worklode.io/ns/id/deployment/prod/flux_kustomization/graph-server> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/ontology#Deployment> .`,
		`<https://worklode.io/ns/id/deployment/prod/flux_kustomization/graph-server> <https://worklode.io/ns/ontology#toEnvironment> <https://worklode.io/ns/id/environment/prod> .`,
		`<https://worklode.io/ns/id/deployment/prod/flux_kustomization/graph-server> <https://worklode.io/ns/ontology#targetKind> <https://worklode.io/ns/concept/flux_kustomization> .`,
		`<https://worklode.io/ns/id/deployment/prod/flux_kustomization/graph-server> <https://worklode.io/ns/ontology#deploymentStatus> <https://worklode.io/ns/concept/deployed> .`,
		`<https://worklode.io/ns/id/deployment/prod/flux_kustomization/graph-server> <http://purl.org/dc/terms/identifier> "graph-server" .`,
		`<https://worklode.io/ns/id/deployment/prod/flux_kustomization/graph-server> <http://www.w3.org/ns/prov#startedAtTime> "2026-07-28T12:05:00Z"^^<http://www.w3.org/2001/XMLSchema#dateTime> .`,
		`<https://worklode.io/ns/id/deployment/prod/flux_kustomization/graph-server> <http://purl.org/dc/terms/modified> "2026-07-29T09:00:00Z"^^<http://www.w3.org/2001/XMLSchema#dateTime> .`,
		`<https://worklode.io/ns/id/deployment/prod/flux_kustomization/graph-server> <http://www.w3.org/ns/prov#used> <https://worklode.io/ns/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> .`,
	}
	for _, line := range want {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("missing line:\n%s\ngot:\n%s", line, got)
		}
	}
}

// deployments.artifact_id is null whenever nothing resolved it (006 §11.1,
// §15 question 11): the prov:used edge is specified but must simply be
// absent, not invented.
func TestDeploymentWithoutArtifactHasNoUsedEdge(t *testing.T) {
	d := store.Deployment{
		Environment: "dev", TargetKind: "manual", TargetName: "x",
		Status:    "pending",
		FirstSeen: time.Unix(0, 0).UTC(), LastUpdate: time.Unix(0, 0).UTC(),
	}
	if got := string(Document(DeploymentTriples(d, nil))); strings.Contains(got, "prov#used") {
		t.Fatalf("deployment without artifact projected prov:used:\n%s", got)
	}
}

// The DB stores target_kind 'pypi'; the concept is wlc:pypi_target (006 §6).
func TestPyPITargetKindConcept(t *testing.T) {
	d := store.Deployment{
		Environment: "prod", TargetKind: "pypi", TargetName: "sunstone-py",
		Status:    "deployed",
		FirstSeen: time.Unix(0, 0).UTC(), LastUpdate: time.Unix(0, 0).UTC(),
	}
	got := string(Document(DeploymentTriples(d, nil)))
	if !strings.Contains(got, "<https://worklode.io/ns/concept/pypi_target>") {
		t.Fatalf("target kind pypi not mapped to wlc:pypi_target:\n%s", got)
	}
}

func TestEnvironmentAndCommitTriples(t *testing.T) {
	envs := string(Document(EnvironmentTriples()))
	for _, line := range []string{
		`<https://worklode.io/ns/id/environment/dev> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/ontology#Environment> .`,
		`<https://worklode.io/ns/id/environment/prod> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/ontology#Environment> .`,
	} {
		if !strings.Contains(envs, line+"\n") {
			t.Errorf("missing line:\n%s\ngot:\n%s", line, envs)
		}
	}

	got := string(Document(CommitTriples(GitHubHost, "sunstoneinstitute/worklode", "a16c2a7")))
	for _, line := range []string{
		`<https://worklode.io/ns/id/commit/github.com/sunstoneinstitute/worklode/a16c2a7> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/ontology#Commit> .`,
		`<https://worklode.io/ns/id/commit/github.com/sunstoneinstitute/worklode/a16c2a7> <http://purl.org/dc/terms/identifier> "a16c2a7" .`,
	} {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("missing line:\n%s\ngot:\n%s", line, got)
		}
	}
}

// AC8, first half: a release_frontiers row projects as wl:cutFrom (spelled
// wl:covers until 026 §6.1 took that name) from the git_tag artifact to the
// frontier commit.
func TestReleaseCutFromTriples(t *testing.T) {
	got := string(Document(ReleaseCutFromTriples("sunstoneinstitute/worklode", "v0.4", "a16c2a7")))
	want := `<https://worklode.io/ns/id/artifact/git_tag/github.com/sunstoneinstitute/worklode/v0.4> <https://worklode.io/ns/ontology#cutFrom> <https://worklode.io/ns/id/commit/github.com/sunstoneinstitute/worklode/a16c2a7> .` + "\n"
	if got != want {
		t.Fatalf("ReleaseCutFromTriples = %q; want %q", got, want)
	}
}

// AC8, second half: the wl:cutFrom edge must land on nodes the other two
// functions actually project, or it dangles and wl:cutFromShape's sh:class
// constraints fail. The three functions host-qualify independently, so this
// pins their agreement rather than trusting three hand-written IRI strings to
// keep lining up.
func TestCutFromEdgeLandsOnProjectedNodes(t *testing.T) {
	const repo, tag, sha = "sunstoneinstitute/worklode", "v0.4", "a16c2a7"

	// The subject is the git_tag artifact row applyRelease writes for this
	// release: Name is the bare full_name, Version the tag.
	release := store.Artifact{Kind: "git_tag", Name: repo, Version: tag, Repo: repo, SourceSHA: sha}

	edge := ReleaseCutFromTriples(repo, tag, sha)
	if len(edge) != 1 {
		t.Fatalf("ReleaseCutFromTriples returned %d triples; want 1", len(edge))
	}

	artifact := ArtifactTriples(release, func(string, string) bool { return true })
	if edge[0].S != artifact[0].S {
		t.Errorf("cutFrom subject %q is not the artifact ArtifactTriples projects (%q)", edge[0].S, artifact[0].S)
	}

	commit := CommitTriples(GitHubHost, repo, sha)
	if len(commit) == 0 {
		t.Fatal("CommitTriples projected nothing for a well-formed repo")
	}
	if edge[0].O != IRIRef(commit[0].S) {
		t.Errorf("cutFrom object %v is not the commit CommitTriples projects (%q)", edge[0].O, commit[0].S)
	}
}

func TestMalformedRepoOmitsEdges(t *testing.T) {
	if ts := CommitTriples(GitHubHost, "not-owner-name", "a16c2a7"); ts != nil {
		t.Fatalf("CommitTriples on malformed repo = %v; want nil", ts)
	}
	if ts := ReleaseCutFromTriples("not-owner-name", "v1", "a16c2a7"); ts != nil {
		t.Fatalf("ReleaseCutFromTriples on malformed repo = %v; want nil", ts)
	}
}
