package graphproj

import (
	"strconv"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Reused-vocabulary IRIs for the document projection (PROV-O, DCAT 3, DCT).
const (
	ProvWasGeneratedBy    = "http://www.w3.org/ns/prov#wasGeneratedBy"
	DCATVersion           = "http://www.w3.org/ns/dcat#version"
	DCTReplaces           = "http://purl.org/dc/terms/replaces"
	DCATPreviousVersion   = "http://www.w3.org/ns/dcat#previousVersion"
	ProvWasRevisionOf     = "http://www.w3.org/ns/prov#wasRevisionOf"
	DCTIssued             = "http://purl.org/dc/terms/issued"
	XSDDate               = "http://www.w3.org/2001/XMLSchema#date"
	DCATHasVersion        = "http://www.w3.org/ns/dcat#hasVersion"
	DCATHasCurrentVersion = "http://www.w3.org/ns/dcat#hasCurrentVersion"
	DCTRequires           = "http://purl.org/dc/terms/requires"
)

// docClass maps docs.kind to its ontology class (ns/ontology.ttl): a spec is
// a wl:DesignDoc, and a plan is a document but not a DesignDoc. An unknown kind falls back to the DesignDoc super-type rather
// than emitting an unknown term.
func docClass(kind string) string {
	switch kind {
	case "spec":
		return "Spec"
	case "plan":
		return "Plan"
	default:
		return "DesignDoc"
	}
}

// DocKey is d's document key (WL-REQ-247), the one identity every
// document, version and declared-graph IRI is built on.
func DocKey(d model.Doc) string { return iri.DocKey(d.Kind, d.Project, d.Number) }

// DocTriples projects one backbone document row into its canonical node's
// triples — the v1 of WL-REQ-164's "the graph receives them by projection"
// (WL-289): type, title, status (a wlc:DesignDocStatus concept, per
// wl:status's range), the current version as a plain dcat:version literal,
// timestamps, and — the edge this projection exists to make reachable —
// prov:wasGeneratedBy naming the authoring task (WL-REQ-177).
//
// dcat:version stays a plain literal on the canonical node alongside the
// pointer edges below: DCAT permits both, WL-REQ-251 documents the literal
// as-built, and dropping it buys nothing.
//
// versions is nil for a draft (Global Constraints — a draft has no accepted
// snapshot graphs to point at), which keeps a draft's projection
// byte-identical to before this pointer machinery existed. Non-nil, each
// entry gets a dcat:hasVersion edge to its snapshot node
// (iri.DocVersion(DocKey(d), v.Version)), plus one dcat:hasCurrentVersion naming
// d.Version's snapshot (WL-REQ-166). Rules are separate subjects and have
// their own projection, RuleTriples. Subject-complete for iri.Doc(DocKey(d)),
// like TaskTriples.
func DocTriples(d model.Doc, versions []model.DocVersionSummary) []Triple {
	subj := iri.Doc(DocKey(d))
	triples := []Triple{
		{S: subj, P: RDFType, O: IRIRef(iri.Term(docClass(d.Kind)))},
		{S: subj, P: DCTTitle, O: Text(d.Title)},
		{S: subj, P: iri.Term("status"), O: IRIRef(iri.Concept(d.Status))},
		{S: subj, P: DCATVersion, O: Text(strconv.Itoa(d.Version))},
		{S: subj, P: DCTCreated, O: Typed(d.CreatedAt.UTC().Format(time.RFC3339), XSDDateTime)},
		{S: subj, P: DCTModified, O: Typed(d.UpdatedAt.UTC().Format(time.RFC3339), XSDDateTime)},
	}
	if d.GeneratedByTask != "" {
		triples = append(triples, Triple{S: subj, P: ProvWasGeneratedBy, O: IRIRef(iri.Task(d.GeneratedByTask))})
	}
	for _, v := range versions {
		triples = append(triples, Triple{S: subj, P: DCATHasVersion, O: IRIRef(iri.DocVersion(DocKey(d), v.Version))})
	}
	if len(versions) > 0 {
		triples = append(triples, Triple{S: subj, P: DCATHasCurrentVersion, O: IRIRef(iri.DocVersion(DocKey(d), d.Version))})
	}
	return triples
}

// DocVersionTriples projects one immutable document version into the
// snapshot graph of WL-REQ-166: the version's own node — iri.DocVersion(DocKey(d),
// v.Version) — plus, for each rule v.Rules arranged, a dct:isPartOf naming the
// snapshot. Rule IRIs are version-free (WL-REQ-247); the graph a caller loads
// them from carries the version. Spec headings arrange no rule and project
// nothing. projectIDs maps a rule ref's project key to its project id.
//
// prov:wasAttributedTo from WL-REQ-166's example is deliberately not emitted
// here: doc_versions stores no per-version author (WL-REQ-164's scope), and the
// document-level prov:wasGeneratedBy on the canonical node (DocTriples)
// already carries authorship.
func DocVersionTriples(d model.Doc, v model.DocVersion, projectIDs map[string]string) []Triple {
	subj := iri.DocVersion(DocKey(d), v.Version)
	triples := []Triple{
		{S: subj, P: RDFType, O: IRIRef(iri.Term(docClass(d.Kind)))},
		{S: subj, P: DCTTitle, O: Text(v.Title)},
		{S: subj, P: DCATVersion, O: Text(strconv.Itoa(v.Version))},
		{S: subj, P: DCTCreated, O: Typed(v.CreatedAt.UTC().Format(time.RFC3339), XSDDateTime)},
	}
	if v.Version > 1 {
		prev := iri.DocVersion(DocKey(d), v.Version-1)
		triples = append(triples,
			Triple{S: subj, P: DCATPreviousVersion, O: IRIRef(prev)},
			Triple{S: subj, P: ProvWasRevisionOf, O: IRIRef(prev)},
		)
	}
	if v.Issued != "" {
		triples = append(triples, Triple{S: subj, P: DCTIssued, O: Typed(v.Issued, XSDDate)})
	}
	for _, r := range v.Rules {
		if rule := RuleIRI(r.Rule, projectIDs); rule != "" {
			triples = append(triples, Triple{S: rule, P: DCTIsPartOf, O: IRIRef(subj)})
		}
	}
	return triples
}

// RuleIRI is the IRI of the rule ref names ("WL-REQ-12"), resolved through
// projectIDs (project key to project id). It is "" for a ref that does not
// parse or names an unknown project.
func RuleIRI(ref string, projectIDs map[string]string) string {
	r, ok := designdoc.ParseRuleRef(ref)
	if !ok {
		return ""
	}
	id, ok := projectIDs[r.Key]
	if !ok {
		return ""
	}
	return iri.Rule(id, r.Number)
}

// RuleTriples projects the rules d arranges as wl:Rule nodes in its declared
// graph (WL-REQ-1709): ref, kind, heading, status, the arranging document,
// and wl:lastRevisedIn naming the document version that last changed the
// rule's arranged text (lastRevisedIn, keyed by ref; absent or 0 emits
// nothing). No section node is emitted (WL-REQ-247).
//
// A rule's supersession is the successor's dct:replaces (the Rule shape,
// WL-REQ-1705), read from each rule's incoming supersedes edges; a rule's
// Edges are only consulted for that. projectIDs resolves the successor's ref.
func RuleTriples(d model.Doc, rules []model.Rule, lastRevisedIn map[string]int, projectIDs map[string]string) []Triple {
	var triples []Triple
	for _, r := range rules {
		subj := iri.Rule(r.Project, r.Number)
		triples = append(triples,
			Triple{S: subj, P: RDFType, O: IRIRef(iri.Term("Rule"))},
			Triple{S: subj, P: DCTIdentifier, O: Text(r.Ref)},
			Triple{S: subj, P: DCTTitle, O: Text(r.Heading)},
			Triple{S: subj, P: iri.Term("ruleKind"), O: IRIRef(iri.Concept(r.Kind))},
			Triple{S: subj, P: iri.Term("status"), O: IRIRef(iri.Concept(r.Status))},
			Triple{S: subj, P: DCTIsPartOf, O: IRIRef(iri.Doc(DocKey(d)))},
		)
		if v := lastRevisedIn[r.Ref]; v > 0 {
			triples = append(triples, Triple{S: subj, P: iri.Term("lastRevisedIn"), O: IRIRef(iri.DocVersion(DocKey(d), v))})
		}
		for _, e := range r.Edges {
			if e.Type != "supersedes" || e.To != r.Ref {
				continue
			}
			if succ := RuleIRI(e.From, projectIDs); succ != "" {
				triples = append(triples, Triple{S: succ, P: DCTReplaces, O: IRIRef(subj)})
			}
		}
	}
	return triples
}

// DocEdgeTriples projects d's outgoing covers and defers edges onto the rule
// IRIs they name (WL-REQ-1709). An edge naming no rule, or a rule whose
// project key projectIDs does not know, is skipped.
func DocEdgeTriples(d model.Doc, out []model.DocEdge, projectIDs map[string]string) []Triple {
	var triples []Triple
	for _, e := range out {
		if e.Type != "covers" && e.Type != "defers" {
			continue
		}
		if rule := RuleIRI(e.ToRule, projectIDs); rule != "" {
			triples = append(triples, Triple{S: iri.Doc(DocKey(d)), P: iri.Term(e.Type), O: IRIRef(rule)})
		}
	}
	return triples
}
