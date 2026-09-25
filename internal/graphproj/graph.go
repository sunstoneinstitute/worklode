package graphproj

import (
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// ProjectGraphTriples projects one model.ProjectGraph: every task through
// TaskTriples, every document through DocTriples (without version pointers —
// the graph read carries none), and one triple per document edge and per
// task-to-document link. A generated_by link is not emitted here because
// DocTriples already states it as prov:wasGeneratedBy. An edge whose far end
// is not among g.Docs names nothing this graph knows and is skipped.
func ProjectGraphTriples(g model.ProjectGraph) []Triple {
	out := map[string][]model.Edge{}
	in := map[string][]model.Edge{}
	for _, e := range g.TaskEdges {
		out[e.From] = append(out[e.From], e)
		in[e.To] = append(in[e.To], e)
	}
	slug := map[int64]string{}
	for _, d := range g.Docs {
		slug[d.ID] = d.Slug
	}

	var triples []Triple
	for _, t := range g.Tasks {
		triples = append(triples, TaskTriples(t, out[t.ID], in[t.ID])...)
	}
	for _, d := range g.Docs {
		triples = append(triples, DocTriples(d, nil)...)
	}
	for _, e := range g.DocEdges {
		from, okF := slug[e.From]
		to, okT := slug[e.To]
		if !okF || !okT {
			continue
		}
		s, o := iri.Doc(from), iri.Doc(to)
		var p string
		switch e.Type {
		case "covers", "implements", "defers":
			p = iri.Term(e.Type)
		case "blocks":
			p = iri.Term("blocksPlan")
		case "requires":
			p = DCTRequires
		case "wasDerivedFrom":
			p = ProvWasDerivedFrom
		default:
			continue
		}
		triples = append(triples, Triple{S: s, P: p, O: IRIRef(o)})
	}
	for _, l := range g.Links {
		to, ok := slug[l.Doc]
		if !ok {
			continue
		}
		switch l.Type {
		case "planned_in":
			triples = append(triples, Triple{S: iri.Task(l.Task), P: iri.Term("plannedIn"), O: IRIRef(iri.Doc(to))})
		case "about":
			triples = append(triples, Triple{S: iri.Task(l.Task), P: iri.Term("about"), O: IRIRef(iri.Doc(to))})
		}
	}
	return triples
}
