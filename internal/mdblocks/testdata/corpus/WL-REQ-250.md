
Consumers query `graph-server` over SPARQL (WL-REQ-252). `lode graph triples` writes a project's tasks, the documents they reach and the edges between them as N-Triples, rendered from the API by `internal/graphproj`. `lode graph drift`, `lode graph gaps` and `GET /api/v1/overview` run their queries against `graph-server`. No `lode-server` route serves RDF by content negotiation.

