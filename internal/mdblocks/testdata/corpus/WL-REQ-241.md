
| Tier | Where | Does | Idioms |
|---|---|---|---|
| Runtime | `graph-server` / Oxigraph | SPARQL 1.1, no reasoner | property paths (`?t wl:dependsOn+ ?x`), `?c wl:layer wlc:intent` |
| CI, OWL 2 RL | `owlrl` over `ns/` plus fixture data | classification, disjointness, transitive closure as proof | `owl:AllDisjointClasses`, `TransitiveProperty`, `FunctionalProperty`, `unionOf`, `inverseOf` |
| CI, SHACL gate | Jena `shacl` over `ns/shapes.ttl` plus fixture data | closed-world presence and cardinality | node shapes (WL-SPEC-79#sec-6a) |

The runtime tier is the only one running.

OWL is open-world and never flags a missing field or a duplicate, so OWL classifies and SHACL enforces presence and cardinality. `owl:FunctionalProperty` catches more than one; SHACL catches zero. Closure is never published: the pipeline ships declared edges plus TBox, and entailments are re-derived live by property paths. Any SHACL validation of projected data runs over the union of every project named graph on the work branch plus `ns/ontology.ttl` (for `rdfs:subClassOf`), never over one project graph alone: a cross-project edge's foreign endpoint is typed only in its home graph (WL-SPEC-79#sec-12a). No per-graph validation gate is introduced.

