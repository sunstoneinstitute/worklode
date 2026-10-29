
`GET /projects/{id}/progress` paints each spec as a strip of its sections,
colored by a state derived from the plans that cover the section and the tasks
those plans minted, grouped by the act that moves them next, with the act as a
button. It stores no progress figure; every state is recomputed per request, so
it cannot disagree with `lode doc todo` or the run board. The route answers 404
for a project with no spec. `GET /api/v1/projects/{id}/progress` and `lode doc
progress [--project] [--json]` return the same `model.ProjectProgress`, so an
agent reads what a person sees.

