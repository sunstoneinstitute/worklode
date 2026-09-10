package implements

import (
	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
)

// Triples renders the claim set as <component> wl:implements <section>
// edges, each annotated with its pinned document version — the payload of
// the 025 §11.5 observed/repo-implements named graph, and what the
// stale-claim query compares against wl:lastRevisedIn.
//
// Each claim yields three lines: the asserted edge, a reifier node linked to
// the edge's triple term by rdf:reifies, and wl:pinnedVersion on that
// reifier. That is the RDF 1.2 annotation encoding. The RDF-star spelling
// that puts << s p o >> in subject position is not valid RDF 1.2 N-Triples
// and Oxigraph rejects it; iri.Claim explains why the reifier is an IRI and
// not a blank node.
//
// The claim set is deduplicated on (component, section), so two claims that
// differ only in Pinned collapse to the first one's three lines. Resolve
// cannot hand that pair over — it errors on conflicting pins first — so this
// only matters for a hand-built slice, but it keeps wl:pinnedVersion single-
// valued, which its owl:FunctionalProperty declaration requires.
func Triples(claims []Claim) []graphproj.Triple {
	ts := make([]graphproj.Triple, 0, 3*len(claims))
	seen := make(map[[2]string]bool, len(claims))
	for _, c := range claims {
		key := [2]string{c.Component, c.Section}
		if seen[key] {
			continue
		}
		seen[key] = true
		edge := graphproj.Triple{
			S: c.Component,
			P: iri.Term("implements"),
			O: graphproj.IRIRef(c.Section),
		}
		reifier := iri.Claim(c.Component, c.Section)
		ts = append(ts, edge,
			graphproj.Triple{S: reifier, P: graphproj.RDFReifies, O: graphproj.TripleTerm(edge)},
			graphproj.Triple{S: reifier, P: iri.Term("pinnedVersion"), O: graphproj.IRIRef(c.Pinned)},
		)
	}
	return ts
}
