package graphproj

import (
	"strconv"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Reused-vocabulary IRIs for the document projection (PROV-O, DCAT 3, DCT).
const (
	ProvWasGeneratedBy = "http://www.w3.org/ns/prov#wasGeneratedBy"
	DCATVersion        = "http://www.w3.org/ns/dcat#version"
	DCTIsReplacedBy    = "http://purl.org/dc/terms/isReplacedBy"
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
// triples — the v1 of 025 §5's "the graph receives them by projection"
// (WL-289): type, title, status (a wlc:DesignDocStatus concept, per
// wl:status's range), the current version as a plain dcat:version literal,
// timestamps, and — the edge this projection exists to make reachable —
// prov:wasGeneratedBy naming the authoring task (025 §12).
//
// Deliberately not yet projected here: doc_edges and 025 §4's versioned
// snapshot graphs (dcat:hasVersion and wl:lastRevisedIn). Those belong to the
// full document projection 025 sketches; carrying dcat:version on the
// canonical node is the v1 shorthand until the snapshots exist. Sections are
// separate subjects and have their own projection, SectionTriples.
// Subject-complete for iri.Doc(d.Slug), like TaskTriples.
func DocTriples(d model.Doc) []Triple {
	subj := iri.Doc(d.Slug)
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
	return triples
}

// SectionTriples projects one document's sections as wl:Section nodes in that
// document's own declared graph (025 §3.3). Each section is its own subject —
// iri.Section(slug, anchor) — so this is deliberately not part of DocTriples,
// which stays subject-complete for the document node.
//
// Only published sections are projected. An unpublished section belongs to a
// draft that has not been accepted, and 025 §3 freezes an anchor at first
// publication: minting an IRI from an anchor that may still change would put a
// mutable identity in a graph whose whole value is that section IRIs are
// durable.
//
// Status is derived, never stored: 025 §6.2 keeps section-level supersession a
// query rather than a column, and the graph is where that derivation becomes
// visible. A section is superseded when an inbound replaces edge names its
// anchor, or when its document is superseded as a whole; otherwise it carries
// its document's status. The inbound edges are the `in` list from
// Store.ListDocEdges, where FromAnchor is the anchor *in this document* the
// edge lands on and ToAnchor is the anchor it left from.
//
// 025 §6 rule 2's other branch — a dct:description saying why a section went
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
		if e.ToSlug != "" && e.ToAnchor != "" {
			successor = iri.Section(e.ToSlug, e.ToAnchor)
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
		subj := iri.Section(d.Slug, sec.Anchor)
		status := d.Status
		successor, replaced := replacedBy[sec.Anchor]
		if replaced {
			status = "superseded"
		}
		triples = append(triples,
			Triple{S: subj, P: RDFType, O: IRIRef(iri.Term("Section"))},
			Triple{S: subj, P: DCTTitle, O: Text(sec.Heading)},
			Triple{S: subj, P: DCTIsPartOf, O: IRIRef(iri.Doc(d.Slug))},
			Triple{S: subj, P: iri.Term("status"), O: IRIRef(iri.Concept(status))},
		)
		if successor != "" {
			triples = append(triples, Triple{S: subj, P: DCTIsReplacedBy, O: IRIRef(successor)})
		}
	}
	return triples
}
