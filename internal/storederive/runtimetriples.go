package storederive

import (
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/store"
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
		name = graphproj.GitHubHost + "/" + name
	}
	return name, iri.Artifact(a.Kind, name, a.Version)
}

// ArtifactTriples projects one artifacts row (006 §11.1). The commit edge is
// guarded: target_commitish is frequently a branch name, and minting a
// commit IRI from one would create a plausible, permanently wrong node —
// emit prov:wasDerivedFrom only when source_sha resolves via known. An
// artifact with no repo, or a malformed one, projects no commit edge at
// all: a repository alone does not identify a commit.
func ArtifactTriples(a store.Artifact, known CommitKnown) []graphproj.Triple {
	name, s := artifactCoordinate(a)
	ts := []graphproj.Triple{
		{S: s, P: graphproj.RDFType, O: graphproj.IRIRef(iri.Term("Artifact"))},
		{S: s, P: iri.Term("artifactKind"), O: graphproj.IRIRef(iri.Concept(a.Kind))},
		{S: s, P: graphproj.OWLVersionInfo, O: graphproj.Text(a.Version)},
		{S: s, P: graphproj.DCTIdentifier, O: graphproj.Text(name)},
	}
	if a.Digest != nil {
		ts = append(ts, graphproj.Triple{S: s, P: iri.Term("digest"), O: graphproj.Text(*a.Digest)})
	}
	if !a.BuiltAt.IsZero() {
		ts = append(ts, graphproj.Triple{S: s, P: graphproj.ProvGeneratedAtTime, O: graphproj.Typed(xsdTime(a.BuiltAt), graphproj.XSDDateTime)})
	}
	if a.Repo != "" && a.SourceSHA != "" && known != nil && known(a.Repo, a.SourceSHA) {
		if owner, repo, ok := splitRepo(a.Repo); ok {
			ts = append(ts, graphproj.Triple{S: s, P: graphproj.ProvWasDerivedFrom,
				O: graphproj.IRIRef(iri.Commit(graphproj.GitHubHost, owner, repo, a.SourceSHA))})
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
func DeploymentTriples(d store.Deployment, artifact *store.Artifact) []graphproj.Triple {
	s := iri.Deployment(d.Environment, d.TargetKind, d.TargetName)
	ts := []graphproj.Triple{
		{S: s, P: graphproj.RDFType, O: graphproj.IRIRef(iri.Term("Deployment"))},
		{S: s, P: iri.Term("toEnvironment"), O: graphproj.IRIRef(iri.Environment(d.Environment))},
		{S: s, P: iri.Term("targetKind"), O: graphproj.IRIRef(iri.Concept(targetKindConcept(d.TargetKind)))},
		{S: s, P: iri.Term("deploymentStatus"), O: graphproj.IRIRef(iri.Concept(d.Status))},
		{S: s, P: graphproj.DCTIdentifier, O: graphproj.Text(d.TargetName)},
		{S: s, P: graphproj.ProvStartedAtTime, O: graphproj.Typed(xsdTime(d.FirstSeen), graphproj.XSDDateTime)},
		{S: s, P: graphproj.DCTModified, O: graphproj.Typed(xsdTime(d.LastUpdate), graphproj.XSDDateTime)},
	}
	if artifact != nil {
		_, artifactIRI := artifactCoordinate(*artifact)
		ts = append(ts, graphproj.Triple{S: s, P: graphproj.ProvUsed, O: graphproj.IRIRef(artifactIRI)})
	}
	return ts
}

// EnvironmentTriples projects the fixed instance set {dev, prod} — static,
// matching wl:EnvironmentShape's closure and store.NormalizeEnvironment. It is
// the only place that closure is honoured by construction; see
// DeploymentTriples for where it is merely assumed.
func EnvironmentTriples() []graphproj.Triple {
	var ts []graphproj.Triple
	for _, name := range []string{"dev", "prod"} {
		s := iri.Environment(name)
		ts = append(ts,
			graphproj.Triple{S: s, P: graphproj.RDFType, O: graphproj.IRIRef(iri.Term("Environment"))},
			graphproj.Triple{S: s, P: graphproj.DCTIdentifier, O: graphproj.Text(name)},
		)
	}
	return ts
}

// CommitTriples projects one main_commits row. repo is "owner/name"
// (GitHub full_name); a malformed repo projects nothing — the §6 guard
// posture, an unmintable node is omitted rather than fabricated.
func CommitTriples(host, repo, sha string) []graphproj.Triple {
	owner, name, ok := splitRepo(repo)
	if !ok {
		return nil
	}
	s := iri.Commit(host, owner, name, sha)
	return []graphproj.Triple{
		{S: s, P: graphproj.RDFType, O: graphproj.IRIRef(iri.Term("Commit"))},
		{S: s, P: graphproj.DCTIdentifier, O: graphproj.Text(sha)},
	}
}

// ReleaseCutFromTriples projects one release_frontiers row joined to its
// main_commits sha: the release's git_tag artifact wl:cutFrom the frontier
// commit (006 §11.1 — release_frontiers projects as an edge, not a node;
// the property was spelled wl:covers until 026 §6.1 took that name for the
// Plan→Section undertaking). repo is "owner/name" (GitHub full_name); a
// malformed repo projects nothing.
func ReleaseCutFromTriples(repo, tag, sha string) []graphproj.Triple {
	owner, name, ok := splitRepo(repo)
	if !ok {
		return nil
	}
	return []graphproj.Triple{{
		S: iri.Artifact("git_tag", graphproj.GitHubHost+"/"+repo, tag),
		P: iri.Term("cutFrom"),
		O: graphproj.IRIRef(iri.Commit(graphproj.GitHubHost, owner, name, sha)),
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
