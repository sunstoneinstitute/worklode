
`GET /drift` is a read-only page over every project: the ready frontier in
backbone rank order with fan-out, depth and a critical marker, and the
estimate-free critical path, both from the backbone; then architectural
violations (observed, not declared or acknowledged), stale intent (declared,
not observed) and coverage gaps from the knowledge graph (WL-SPEC-79). With no
graph configured the page keeps its backbone half and says the graph half is
unavailable. It renders no form and no act: resolving drift means changing a
declaration or the code. The route is `permWebRead`; the link to it is a card
on the global `/docs` index that only admins see. The CLI reads the same
facts through `lode graph` (WL-SPEC-81).

