
A project's graph is subject-complete and object-open. It holds every projected triple whose subject is the project's Project node or one of its tasks, and no triple about any other subject. A cross-project edge's foreign endpoint stays a bare IRI with no `rdf:type` beside it; the projector never emits a type stub or mirrors a foreign task. The endpoint's type lives in its home project's graph. Because the store runs no reasoner, both directions of `wl:blocks` / `wl:dependsOn` are materialised.

