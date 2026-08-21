package implements

import (
	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
)

// Triples renders the claim set as <component> wl:implements <section>
// edges — the payload of the 025 §11.5 observed/repo-implements named graph.
//
// Only the edge is emitted. 025 §11.5 also wants the pinned version in the
// graph for the stale-claim query, but names no predicate or annotation
// encoding for a per-edge value; that mint follows the amend-006-then-
// mirror-ns/ route (docs/plans/2026-07-30-design-documents-as-graph-objects.md,
// open question 2, tracked as WL-275). Claims carry the pin in Go until then.
//
// The edge set is deduplicated on (component, section), so two claims that
// differ only in Pinned collapse to one triple. Resolve cannot hand that
// pair over — it errors on conflicting pins first — so this only matters for
// a hand-built slice; graphproj.Document would drop the duplicate line
// anyway. It is here so Triples is right on its own terms.
func Triples(claims []Claim) []graphproj.Triple {
	ts := make([]graphproj.Triple, 0, len(claims))
	seen := make(map[[2]string]bool, len(claims))
	for _, c := range claims {
		key := [2]string{c.Component, c.Section}
		if seen[key] {
			continue
		}
		seen[key] = true
		ts = append(ts, graphproj.Triple{
			S: c.Component,
			P: iri.Term("implements"),
			O: graphproj.IRIRef(c.Section),
		})
	}
	return ts
}
