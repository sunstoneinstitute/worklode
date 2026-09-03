// Package iri is the single owner of the canonical IRI grammar of spec 006
// §10 (schema/instance namespaces, id/ instance grammar) and §10.1 (runtime
// IRI grammar, kind-first to mirror each relational natural key). The
// published base carries no ontology-name segment (025 §17). Constructors
// are pure concatenation: no validation, no error return. Slashes inside a
// local id are permitted (slash namespace, opaque path).
package iri

import (
	"fmt"
	"strconv"
)

// Namespace roots (006 §10). Untyped constants so callers can build
// prefixes directly, e.g. iri.IDNS + "task/".
const (
	Base      = "https://worklode.io/ns/"
	Ontology  = Base + "ontology#" // wl:  (hash namespace)
	ConceptNS = Base + "concept/"  // wlc:
	IDNS      = Base + "id/"       // wlid:
	GraphNS   = Base + "graph/"    // named-graph families
)

// Term returns the IRI of an ontology term (class or property).
func Term(local string) string {
	return Ontology + local
}

// Concept returns the IRI of a SKOS status concept.
func Concept(local string) string {
	return ConceptNS + local
}

// Task returns the instance IRI of a backbone task.
func Task(id string) string {
	return IDNS + "task/" + id
}

// Project returns the instance IRI of a project.
func Project(projectID string) string {
	return IDNS + "project/" + projectID
}

// ProjectGraph returns the named-graph IRI for a project.
func ProjectGraph(projectID string) string {
	return GraphNS + "project/" + projectID
}

// Agent returns the instance IRI of an agent.
func Agent(actorID string) string {
	return IDNS + "agent/" + actorID
}

// Component returns the instance IRI of a component, keyed by its manifest
// slug.
func Component(slug string) string {
	return IDNS + "component/" + slug
}

// Doc returns the instance IRI of a design document.
func Doc(slug string) string {
	return IDNS + "doc/" + slug
}

// Section returns the IRI of an addressable design-document section
// (025 §3): id/section/<doc-slug>/<anchor>. The anchor is assigned at first
// publication and never changes, so the IRI is as durable as the document's.
func Section(docSlug, anchor string) string {
	return IDNS + "section/" + docSlug + "/" + anchor
}

// DocVersion returns the immutable versioned sibling IRI of a design
// document (025 §4): id/doc/<slug>/v<n>. Everything links to the canonical
// Doc IRI by default; versioned IRIs appear only in pinned claims.
func DocVersion(slug string, version int) string {
	return IDNS + "doc/" + slug + "/v" + strconv.Itoa(version)
}

// Deliverable returns the instance IRI of a deliverable.
func Deliverable(id string) string {
	return IDNS + "deliverable/" + id
}

// Issue returns the instance IRI of a repo-hosted issue.
func Issue(host, owner, repo string, number int64) string {
	return IDNS + fmt.Sprintf("issue/%s/%s/%s/%d", host, owner, repo, number)
}

// PR returns the instance IRI of a repo-hosted pull request.
func PR(host, owner, repo string, number int64) string {
	return IDNS + fmt.Sprintf("pr/%s/%s/%s/%d", host, owner, repo, number)
}

// Artifact returns the instance IRI of a built artifact (006 §10.1),
// kind-first to mirror the (kind, name, version) natural key.
func Artifact(kind, name, version string) string {
	return IDNS + "artifact/" + kind + "/" + name + "/" + version
}

// Deployment returns the instance IRI of a deployment (006 §10.1), mirroring
// the (environment, target_kind, target_name) natural key.
func Deployment(env, targetKind, targetName string) string {
	return IDNS + "deployment/" + env + "/" + targetKind + "/" + targetName
}

// Environment returns the instance IRI of a deployment environment.
func Environment(name string) string {
	return IDNS + "environment/" + name
}

// Commit returns the instance IRI of a repo-hosted commit (006 §10.1).
func Commit(host, owner, repo, sha string) string {
	return IDNS + "commit/" + host + "/" + owner + "/" + repo + "/" + sha
}

// DeclaredGraph returns the named graph holding one design doc's declared
// edges (spec 007 §Representation: one graph per design doc, so acceptance
// gating and re-authoring replace exactly one graph).
func DeclaredGraph(docSlug string) string { return GraphNS + "declared/" + docSlug }

// ObservedGraph returns the org-global named graph of a backbone-derived
// deriver source — computed server-side over all-repo state by a single
// writer: pr-affects, deploy, repo-implements (025 §11). Repo-local sources
// use RepoObservedGraph instead; spec 007 §1.1 owns the split.
func ObservedGraph(source string) string { return GraphNS + "observed/" + source }

// RepoObservedGraph returns the per-repo named graph of a repo-local deriver
// source (go-imports, repo-layout). `lode graph derive` runs from each repo's
// checkout, and the whole-graph-replace contract needs one graph per writer
// (spec 007 §1.1); the repo segment mirrors Repo's <host>/<owner>/<name>.
func RepoObservedGraph(source, host, owner, name string) string {
	return GraphNS + "observed/" + source + "/" + host + "/" + owner + "/" + name
}

// Repo returns a repository's instance IRI (the doap:Project node, D4).
// Spec 006 defines no repo pattern; this package fixes
// id/repo/<host>/<owner>/<name>.
func Repo(host, owner, name string) string {
	return IDNS + "repo/" + host + "/" + owner + "/" + name
}
