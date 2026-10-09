package graphproj

import (
	"strconv"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Reused-vocabulary IRIs for the document projection (PROV-O, DCAT 3, DCT).
const (
	ProvWasGeneratedBy    = "http://www.w3.org/ns/prov#wasGeneratedBy"
	DCATVersion           = "http://www.w3.org/ns/dcat#version"
	DCTIsReplacedBy       = "http://purl.org/dc/terms/isReplacedBy"
	DCATPreviousVersion   = "http://www.w3.org/ns/dcat#previousVersion"
	ProvWasRevisionOf     = "http://www.w3.org/ns/prov#wasRevisionOf"
	DCTIssued             = "http://purl.org/dc/terms/issued"
	XSDDate               = "http://www.w3.org/2001/XMLSchema#date"
	DCATHasVersion        = "http://www.w3.org/ns/dcat#hasVersion"
	DCATHasCurrentVersion = "http://www.w3.org/ns/dcat#hasCurrentVersion"
	DCTRequires           = "http://purl.org/dc/terms/requires"
)

// docClass maps docs.kind to its ontology class (ns/ontology.ttl): specs and
// ADRs are the two wl:DesignDoc subclasses, and a plan is a document but not
// a DesignDoc. An unknown kind falls back to the DesignDoc super-type rather
// than emitting an unknown term.
func docClass(kind string) string {
	switch kind {
	case "spec":
		return "Spec"
	case "adr":
		return "ADR"
	case "plan":
		return "Plan"
	default:
		return "DesignDoc"
	}
}

// DocTriples projects one backbone document row into its canonical node's
// triples — the v1 of WL-SPEC-77 §3's "the graph receives them by projection"
// (WL-289): type, title, status (a wlc:DesignDocStatus concept, per
// wl:status's range), the current version as a plain dcat:version literal,
// timestamps, and — the edge this projection exists to make reachable —
// prov:wasGeneratedBy naming the authoring task (WL-SPEC-77 §13).
//
// dcat:version stays a plain literal on the canonical node alongside the
// pointer edges below: DCAT permits both, WL-SPEC-79 §12 documents the literal
// as-built, and dropping it buys nothing.
//
// versions is nil for a draft (Global Constraints — a draft has no accepted
// snapshot graphs to point at), which keeps a draft's projection
// byte-identical to before this pointer machinery existed. Non-nil, each
// entry gets a dcat:hasVersion edge to its snapshot node
// (iri.DocVersion(DocKey(d), v.Version)), plus one dcat:hasCurrentVersion naming
// d.Version's snapshot (WL-SPEC-77 §5). Sections are separate subjects and have
// their own projection, SectionTriples. Subject-complete for iri.Doc(DocKey(d)),
// like TaskTriples.
// DocKey is d's document key (WL-SPEC-79 §10.2), the one identity every
// document, section, version and declared-graph IRI is built on.
func DocKey(d model.Doc) string { return iri.DocKey(d.Kind, d.Project, d.Number) }

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
// snapshot graph of WL-SPEC-77 §5: the version's own node — iri.DocVersion(DocKey(d),
// v.Version) — plus one wl:Section node per entry in sections, the parsed
// content of that version's body. Section IRIs are version-free
// (iri.Section(DocKey(d), anchor): the anchor is the identity, and the graph a
// caller loads them from already carries the version), but each section's
// dct:isPartOf names the snapshot node, not the canonical document — that is
// what makes this projection distinct from SectionTriples, which targets the
// canonical graph.
//
// prov:wasAttributedTo from WL-SPEC-77 §5's example is deliberately not emitted
// here: doc_versions stores no per-version author (WL-SPEC-77 §3's scope), and the
// document-level prov:wasGeneratedBy on the canonical node (DocTriples)
// already carries authorship.
func DocVersionTriples(d model.Doc, v model.DocVersion, sections []model.DocSection) []Triple {
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
	for _, sec := range sections {
		secSubj := iri.Section(DocKey(d), sec.Anchor)
		triples = append(triples,
			Triple{S: secSubj, P: RDFType, O: IRIRef(iri.Term("Section"))},
			Triple{S: secSubj, P: DCTTitle, O: Text(sec.Heading)},
			Triple{S: secSubj, P: DCTIsPartOf, O: IRIRef(subj)},
		)
		if sec.LastRevisedIn > 0 {
			triples = append(triples, Triple{S: secSubj, P: iri.Term("lastRevisedIn"), O: IRIRef(iri.DocVersion(DocKey(d), sec.LastRevisedIn))})
		}
	}
	return triples
}

// SectionTriples projects one document's sections as wl:Section nodes in that
// document's own declared graph (WL-SPEC-77 §4). Each section is its own subject —
// iri.Section(key, anchor) — so this is deliberately not part of DocTriples,
// which stays subject-complete for the document node.
//
// Only published sections are projected. An unpublished section belongs to a
// draft that has not been accepted, and WL-SPEC-77 §4 freezes an anchor at first
// publication: minting an IRI from an anchor that may still change would put a
// mutable identity in a graph whose whole value is that section IRIs are
// durable.
//
// Status is derived, never stored: WL-SPEC-77 §6 keeps section-level supersession a
// query rather than a column, and the graph is where that derivation becomes
// visible. A section is superseded when an inbound replaces edge names its
// anchor, or when its document is superseded as a whole; otherwise it carries
// its document's status. The inbound edges are the `in` list from
// Store.ListDocEdges, where FromAnchor is the anchor *in this document* the
// edge lands on and ToAnchor is the anchor it left from.
//
// WL-SPEC-77 §6 rule 2's other branch — a dct:description saying why a section went
// away with no successor to point at — has no author-facing home yet, so
// nothing here emits one (WL-150).
func SectionTriples(d model.Doc, sections []model.DocSection, in []model.DocEdge) []Triple {
	// replacedBy maps an anchor in this document to the section that replaced
	// it, "" when a replacement names the anchor without resolving to a far
	// section (an unresolved external reference).
	replacedBy := map[string]string{}
	for _, e := range in {
		if e.Type != "isReplacedBy" || e.FromAnchor == "" {
			continue
		}
		successor := ""
		if e.ToKind != "" && e.ToAnchor != "" {
			successor = iri.Section(iri.DocKey(e.ToKind, e.ToProject, e.ToNumber), e.ToAnchor)
		}
		// A later successor never unsets an earlier one: a section replaced by
		// something nameable stays pointed at it.
		if _, seen := replacedBy[e.FromAnchor]; !seen || successor != "" {
			replacedBy[e.FromAnchor] = successor
		}
	}

	var triples []Triple
	for _, sec := range sections {
		if !sec.Published {
			continue
		}
		subj := iri.Section(DocKey(d), sec.Anchor)
		status := d.Status
		successor, replaced := replacedBy[sec.Anchor]
		if replaced {
			status = "superseded"
		}
		triples = append(triples,
			Triple{S: subj, P: RDFType, O: IRIRef(iri.Term("Section"))},
			Triple{S: subj, P: DCTTitle, O: Text(sec.Heading)},
			Triple{S: subj, P: DCTIsPartOf, O: IRIRef(iri.Doc(DocKey(d)))},
			Triple{S: subj, P: iri.Term("status"), O: IRIRef(iri.Concept(status))},
		)
		if successor != "" {
			triples = append(triples, Triple{S: subj, P: DCTIsReplacedBy, O: IRIRef(successor)})
		}
		// LastRevisedIn is 0 when unset; iri.DocVersion(DocKey(d), 0) would point
		// at a "v0" snapshot that never exists, so the edge is only emitted
		// once a real version has revised the section.
		if sec.LastRevisedIn > 0 {
			triples = append(triples, Triple{S: subj, P: iri.Term("lastRevisedIn"), O: IRIRef(iri.DocVersion(DocKey(d), sec.LastRevisedIn))})
		}
	}
	return triples
}
