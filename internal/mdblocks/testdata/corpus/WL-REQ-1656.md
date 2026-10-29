
`ns/ontology.ttl` marks every edge property `wl:edgeOrigin wlc:declared` or `wlc:inferred`; a declared one names its stored type with `wl:storedAs` (`"task_edges.dependsOn"`), and an inferred one names its declared counterpart with `owl:inverseOf`. `scripts/nsgen.py` generates the declared set per table into `internal/ns/gen.go` (`EdgeTerms`), and a test holds the generated set to the Turtle (WL-REQ-178).

The graph projection (`lode graph triples`) emits each stored edge as its declared term. The graph store runs no reasoner, so the projection also emits the inverse triple for a declared term with an inferred inverse. SHACL shapes test declared terms only: the superseded-section shape reads `sh:path [ sh:inversePath dct:replaces ]`, so it holds over a graph from any producer, whether or not that producer emitted the inverse.

