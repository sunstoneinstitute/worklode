package graphproj

import (
	"strconv"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Reused-vocabulary IRIs for the document projection (PROV-O, DCAT 3).
const (
	ProvWasGeneratedBy = "http://www.w3.org/ns/prov#wasGeneratedBy"
	DCATVersion        = "http://www.w3.org/ns/dcat#version"
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
// Deliberately not yet projected: sections, doc_edges, and 025 §4's
// versioned snapshot graphs (dcat:hasVersion and wl:lastRevisedIn). Those
// belong to the full document projection 025 sketches; carrying
// dcat:version on the canonical node is the v1 shorthand until the
// snapshots exist. Subject-complete for iri.Doc(d.Slug), like TaskTriples.
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
