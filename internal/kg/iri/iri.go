// Package iri is the single owner of the IRI grammar of WL-REQ-246:
// namespaces, the instance grammar and named graphs. Callers never
// concatenate IRIs themselves (§10.4). Constructors are pure concatenation:
// no validation, no error return. Slashes inside a local id are permitted
// (slash namespace, opaque path).
package iri

import (
	"fmt"
	"strconv"
	"strings"
)

// Namespace roots (WL-REQ-246). Untyped constants so callers can build
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

// DocKey returns a design document's key (WL-REQ-247):
// <kind>-<project>-<nnn>, the number zero-padded to three digits.
// Project-qualified because document numbers are unique only per project.
// Every document, section, version and declared-graph IRI is built on it.
func DocKey(kind, project string, number int) string {
	return fmt.Sprintf("%s-%s-%03d", kind, project, number)
}

// Doc returns the instance IRI of a design document, keyed by DocKey.
func Doc(key string) string {
	return IDNS + "doc/" + key
}

// Event returns the IRI of one event-log row (WL-REQ-248).
func Event(id int64) string {
	return IDNS + "event/" + strconv.FormatInt(id, 10)
}

// CURIE abbreviates an instance IRI to its wlid: form, the form event
// payloads and task bodies store (WL-REQ-248).
func CURIE(instance string) string {
	return "wlid:" + strings.TrimPrefix(instance, IDNS)
}

// Section returns the IRI of an addressable design-document section
// (WL-REQ-165): id/section/<doc-key>/<anchor>. The anchor is assigned at first
// publication and never changes, so the IRI is as durable as the document's.
func Section(docKey, anchor string) string {
	return IDNS + "section/" + docKey + "/" + anchor
}

// DocVersion returns the immutable versioned sibling IRI of a design
// document (WL-REQ-166): id/doc/<doc-key>/v<n>. Everything links to the canonical
// Doc IRI by default; versioned IRIs appear only in pinned claims.
func DocVersion(docKey string, version int) string {
	return IDNS + "doc/" + docKey + "/v" + strconv.Itoa(version)
}

// Claim returns the IRI of the reifier node for one wl:implements claim
// (WL-REQ-177). RDF 1.2 annotates an asserted edge by linking a reifier to the
// edge's triple term with rdf:reifies, and the reifier here must be an IRI:
// graphproj.Document replaces a whole named graph and has to render
// byte-identical output for the same claim set, which a blank node's
// arbitrary label would break. Keying it on the edge it reifies —
// id/claim/<component local id>/<section local id> — makes the same claim
// mint the same IRI on every run and keeps the node readable in the store.
// Both arguments are id/ IRIs whose local ids are already path-safe.
func Claim(componentIRI, sectionIRI string) string {
	return IDNS + "claim/" +
		strings.TrimPrefix(componentIRI, IDNS) + "/" +
		strings.TrimPrefix(sectionIRI, IDNS)
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

// Artifact returns the instance IRI of a built artifact (WL-REQ-246),
// kind-first to mirror the (kind, name, version) natural key.
func Artifact(kind, name, version string) string {
	return IDNS + "artifact/" + kind + "/" + name + "/" + version
}

// Deployment returns the instance IRI of a deployment (WL-REQ-246), mirroring
// the (environment, target_kind, target_name) natural key.
func Deployment(env, targetKind, targetName string) string {
	return IDNS + "deployment/" + env + "/" + targetKind + "/" + targetName
}

// Environment returns the instance IRI of a deployment environment.
func Environment(name string) string {
	return IDNS + "environment/" + name
}

// Commit returns the instance IRI of a repo-hosted commit (WL-REQ-246).
func Commit(host, owner, repo, sha string) string {
	return IDNS + "commit/" + host + "/" + owner + "/" + repo + "/" + sha
}

// DeclaredGraph returns the named graph holding one design doc's declared
// edges (WL-SPEC-82 §Representation: one graph per design doc, so acceptance
// gating and re-authoring replace exactly one graph).
func DeclaredGraph(docKey string) string { return GraphNS + "declared/" + docKey }

// DeclaredVersionGraph returns the named graph holding one immutable
// document version (WL-REQ-166): graph/declared/<doc-key>/v<n>. Sibling of
// DeclaredGraph, which stays the document's mutable canonical graph.
func DeclaredVersionGraph(docKey string, version int) string {
	return DeclaredGraph(docKey) + "/v" + strconv.Itoa(version)
}

// ObservedGraph returns the org-global named graph of a backbone-derived
// deriver source — computed server-side over all-repo state by a single
// writer: pr-affects, deploy, repo-implements (WL-REQ-177). Repo-local sources
// use RepoObservedGraph instead; WL-SPEC-82 §16.1 owns the split.
func ObservedGraph(source string) string { return GraphNS + "observed/" + source }

// RepoObservedGraph returns the per-repo named graph of a repo-local deriver
// source (go-imports, repo-layout). `lode graph derive` runs from each repo's
// checkout, and the whole-graph-replace contract needs one graph per writer
// (WL-SPEC-82 §16.1); the repo segment mirrors Repo's <host>/<owner>/<name>.
func RepoObservedGraph(source, host, owner, name string) string {
	return GraphNS + "observed/" + source + "/" + host + "/" + owner + "/" + name
}

// Repo returns a repository's instance IRI (the doap:Project node, D4).
// Spec 006 defines no repo pattern; this package fixes
// id/repo/<host>/<owner>/<name>.
func Repo(host, owner, name string) string {
	return IDNS + "repo/" + host + "/" + owner + "/" + name
}
