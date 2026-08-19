package graphproj

import (
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// GitHubHost qualifies repo-derived local ids passed to internal/kg/iri. The
// backbone stores repos as "owner/name" (GitHub full_name); the shared IRI
// grammar wants host-qualified, owner/repo-split coordinates.
const GitHubHost = "github.com"

// External vocabulary the runtime projection reuses (006 §3.1 table),
// extending the constants task.go declares. wl: terms resolve through
// iri.Term, wlc: concepts through iri.Concept — never hardcoded.
const (
	DCTIdentifier       = "http://purl.org/dc/terms/identifier"
	OWLVersionInfo      = "http://www.w3.org/2002/07/owl#versionInfo"
	ProvGeneratedAtTime = "http://www.w3.org/ns/prov#generatedAtTime"
	ProvStartedAtTime   = "http://www.w3.org/ns/prov#startedAtTime"
	ProvUsed            = "http://www.w3.org/ns/prov#used"
	ProvWasDerivedFrom  = "http://www.w3.org/ns/prov#wasDerivedFrom"
)

// CommitKnown reports whether sha names a known main_commits row for repo
// (store.MainIDForSHA != nil, in the caller's transaction). It takes the repo
// rather than closing over one because main_commits is UNIQUE (repo, sha):
// a sha alone is not a key, so a batch projecting artifacts across repos
// would otherwise pass the guard on repo A's sha while minting the IRI from
// repo B.
type CommitKnown func(repo, sha string) bool

// splitRepo splits a GitHub "owner/name" full_name at its first slash, as
// internal/kg/iri.Commit wants it. The backbone always stores repos in this
// form; ok is false when there is no slash, or nothing on either side of it.
func splitRepo(full string) (owner, name string, ok bool) {
	i := strings.IndexByte(full, '/')
	if i <= 0 || i == len(full)-1 {
		return "", "", false
	}
	return full[:i], full[i+1:], true
}

// artifactCoordinate returns the (name, IRI) coordinate for an artifact row.
// git_tag names are stored as bare "owner/name" (applyRelease,
// internal/hooks/github.go) and are host-qualified here to match the §10.1
// grammar; the other kinds carry their registry coordinate already.
func artifactCoordinate(a store.Artifact) (name, artifactIRI string) {
	name = a.Name
	if a.Kind == "git_tag" {
		name = GitHubHost + "/" + name
	}
	return name, iri.Artifact(a.Kind, name, a.Version)
}

// ArtifactTriples projects one artifacts row (006 §11.1). The commit edge is
// guarded: target_commitish is frequently a branch name, and minting a
// commit IRI from one would create a plausible, permanently wrong node —
// emit prov:wasDerivedFrom only when source_sha resolves via known. An
// artifact with no repo, or a malformed one, projects no commit edge at
// all: a repository alone does not identify a commit.
func ArtifactTriples(a store.Artifact, known CommitKnown) []Triple {
	name, s := artifactCoordinate(a)
	ts := []Triple{
		{S: s, P: RDFType, O: IRIRef(iri.Term("Artifact"))},
		{S: s, P: iri.Term("artifactKind"), O: IRIRef(iri.Concept(a.Kind))},
		{S: s, P: OWLVersionInfo, O: Text(a.Version)},
		{S: s, P: DCTIdentifier, O: Text(name)},
	}
	if a.Digest != nil {
		ts = append(ts, Triple{S: s, P: iri.Term("digest"), O: Text(*a.Digest)})
	}
	if !a.BuiltAt.IsZero() {
		ts = append(ts, Triple{S: s, P: ProvGeneratedAtTime, O: Typed(xsdTime(a.BuiltAt), XSDDateTime)})
	}
	if a.Repo != "" && a.SourceSHA != "" && known != nil && known(a.Repo, a.SourceSHA) {
		if owner, repo, ok := splitRepo(a.Repo); ok {
			ts = append(ts, Triple{S: s, P: ProvWasDerivedFrom,
				O: IRIRef(iri.Commit(GitHubHost, owner, repo, a.SourceSHA))})
		}
	}
	return ts
}

// DeploymentTriples projects one deployments row. artifact is the row
// deployments.artifact_id resolves to, nil when unset, in which case prov:used
// is simply absent rather than invented (006 §11.1, §15 question 11 — the
// column stayed null until registry_package ingest began minting docker_image
// artifacts, and stays null for any repo whose GitHub App lacks the
// subscription).
//
// wl:toEnvironment is emitted unguarded: deployments.environment is plain
// NOT NULL text with no CHECK (0001_baseline.up.sql), so the dev/prod closure
// wl:EnvironmentShape asserts rests on store.NormalizeEnvironment alone. A row
// that escaped it projects an edge to an untyped node.
func DeploymentTriples(d store.Deployment, artifact *store.Artifact) []Triple {
	s := iri.Deployment(d.Environment, d.TargetKind, d.TargetName)
	ts := []Triple{
		{S: s, P: RDFType, O: IRIRef(iri.Term("Deployment"))},
		{S: s, P: iri.Term("toEnvironment"), O: IRIRef(iri.Environment(d.Environment))},
		{S: s, P: iri.Term("targetKind"), O: IRIRef(iri.Concept(targetKindConcept(d.TargetKind)))},
		{S: s, P: iri.Term("deploymentStatus"), O: IRIRef(iri.Concept(d.Status))},
		{S: s, P: DCTIdentifier, O: Text(d.TargetName)},
		{S: s, P: ProvStartedAtTime, O: Typed(xsdTime(d.FirstSeen), XSDDateTime)},
		{S: s, P: DCTModified, O: Typed(xsdTime(d.LastUpdate), XSDDateTime)},
	}
	if artifact != nil {
		_, artifactIRI := artifactCoordinate(*artifact)
		ts = append(ts, Triple{S: s, P: ProvUsed, O: IRIRef(artifactIRI)})
	}
	return ts
}

// EnvironmentTriples projects the fixed instance set {dev, prod} — static,
// matching wl:EnvironmentShape's closure and store.NormalizeEnvironment. It is
// the only place that closure is honoured by construction; see
// DeploymentTriples for where it is merely assumed.
func EnvironmentTriples() []Triple {
	var ts []Triple
	for _, name := range []string{"dev", "prod"} {
		s := iri.Environment(name)
		ts = append(ts,
			Triple{S: s, P: RDFType, O: IRIRef(iri.Term("Environment"))},
			Triple{S: s, P: DCTIdentifier, O: Text(name)},
		)
	}
	return ts
}

// CommitTriples projects one main_commits row. repo is "owner/name"
// (GitHub full_name); a malformed repo projects nothing — the §6 guard
// posture, an unmintable node is omitted rather than fabricated.
func CommitTriples(host, repo, sha string) []Triple {
	owner, name, ok := splitRepo(repo)
	if !ok {
		return nil
	}
	s := iri.Commit(host, owner, name, sha)
	return []Triple{
		{S: s, P: RDFType, O: IRIRef(iri.Term("Commit"))},
		{S: s, P: DCTIdentifier, O: Text(sha)},
	}
}

// ReleaseCutFromTriples projects one release_frontiers row joined to its
// main_commits sha: the release's git_tag artifact wl:cutFrom the frontier
// commit (006 §11.1 — release_frontiers projects as an edge, not a node;
// the property was spelled wl:covers until 026 §6.1 took that name for the
// Plan→Section undertaking). repo is "owner/name" (GitHub full_name); a
// malformed repo projects nothing.
func ReleaseCutFromTriples(repo, tag, sha string) []Triple {
	owner, name, ok := splitRepo(repo)
	if !ok {
		return nil
	}
	return []Triple{{
		S: iri.Artifact("git_tag", GitHubHost+"/"+repo, tag),
		P: iri.Term("cutFrom"),
		O: IRIRef(iri.Commit(GitHubHost, owner, name, sha)),
	}}
}

// targetKindConcept maps a deployments.target_kind DB value to its concept
// id. The DB stores 'pypi' for the target kind, but the concept is
// wlc:pypi_target — the artifact kind and target kind are different concepts
// that share a name in the relational schema (006 §6).
func targetKindConcept(dbKind string) string {
	if dbKind == "pypi" {
		return "pypi_target"
	}
	return dbKind
}

func xsdTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
