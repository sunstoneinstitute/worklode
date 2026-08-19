package graphproj

import (
	"time"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Reused-vocabulary IRIs (Dublin Core Terms, PROV-O, XSD, RDF). wl: terms are
// resolved through iri.Term instead, since they live in the ontology
// namespace this package does not hardcode.
const (
	RDFType               = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
	DCTTitle              = "http://purl.org/dc/terms/title"
	DCTCreated            = "http://purl.org/dc/terms/created"
	DCTModified           = "http://purl.org/dc/terms/modified"
	DCTIsPartOf           = "http://purl.org/dc/terms/isPartOf"
	ProvWasAssociatedWith = "http://www.w3.org/ns/prov#wasAssociatedWith"
	XSDDateTime           = "http://www.w3.org/2001/XMLSchema#dateTime"
)

// TaskTriples projects a backbone task row, plus its outgoing and incoming
// edges, into the triples of spec 006 §11. It is subject-complete for the
// task's IRI: every triple returned has S == iri.Task(t.ID), and no triple
// with any other subject is produced — a graph rebuilt task-by-task is a
// faithful full projection only if each task's projection stays within its
// own subject.
//
// Edge direction convention (model.Edge{From, To, Type}): "A blocks B" is
// stored as {From: A, To: B, Type: "blocks"} — read literally, subject
// blocks object. So for this task's out-edges (From == t.ID), an out
// child_of means this task is the child (dct:isPartOf the parent in To); an
// out blocks means this task blocks To (wl:blocks). For in-edges (To ==
// t.ID), an in blocks means From blocks this task, i.e. this task depends on
// it (wl:dependsOn). An in child_of means this task is somebody else's
// parent; that triple belongs on the child's own projection (dct:isPartOf
// with this task as object), not here — subject-completeness requires
// emitting nothing for it.
//
// Both wl:blocks and wl:dependsOn are emitted even though ns/ontology.ttl
// declares them owl:inverseOf: the graph store runs no reasoner, so each
// direction must be materialised at projection time for a query to traverse
// it.
func TaskTriples(t model.Task, out, in []model.Edge) []Triple {
	subj := iri.Task(t.ID)
	triples := []Triple{
		{S: subj, P: RDFType, O: IRIRef(iri.Term("Task"))},
		{S: subj, P: DCTTitle, O: Text(t.Title)},
		{S: subj, P: iri.Term("taskState"), O: Text(t.State)},
		{S: subj, P: iri.Term("taskKind"), O: IRIRef(iri.Concept(t.Kind))},
		{S: subj, P: iri.Term("priority"), O: Text(t.Priority)},
		{S: subj, P: iri.Term("inProject"), O: IRIRef(iri.Project(t.Project))},
		{S: subj, P: DCTCreated, O: Typed(t.CreatedAt.UTC().Format(time.RFC3339), XSDDateTime)},
		{S: subj, P: DCTModified, O: Typed(t.UpdatedAt.UTC().Format(time.RFC3339), XSDDateTime)},
	}

	if t.Concern != "" {
		triples = append(triples, Triple{S: subj, P: iri.Term("concern"), O: Text(t.Concern)})
	}
	if t.CreatedBy != "" {
		triples = append(triples, Triple{S: subj, P: ProvWasAssociatedWith, O: IRIRef(iri.Agent(t.CreatedBy))})
	}

	for _, e := range out {
		switch e.Type {
		case "child_of":
			triples = append(triples, Triple{S: subj, P: DCTIsPartOf, O: IRIRef(iri.Task(e.To))})
		case "blocks":
			triples = append(triples, Triple{S: subj, P: iri.Term("blocks"), O: IRIRef(iri.Task(e.To))})
		case "follow_up_to":
			triples = append(triples, Triple{S: subj, P: iri.Term("followUpTo"), O: IRIRef(iri.Task(e.To))})
		}
	}

	for _, e := range in {
		if e.Type == "blocks" {
			triples = append(triples, Triple{S: subj, P: iri.Term("dependsOn"), O: IRIRef(iri.Task(e.From))})
		}
		// in child_of, in follow_up_to: belong to the other task's subject,
		// not this one — emit nothing (subject-completeness).
	}

	return triples
}

// ProjectTriples projects a backbone project row into the triples of spec
// 006 §11's Project node table: the type declaration and its title.
func ProjectTriples(p model.Project) []Triple {
	subj := iri.Project(p.ID)
	return []Triple{
		{S: subj, P: RDFType, O: IRIRef(iri.Term("Project"))},
		{S: subj, P: DCTTitle, O: Text(p.Name)},
	}
}
