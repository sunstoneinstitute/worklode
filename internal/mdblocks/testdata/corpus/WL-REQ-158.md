
`internal/automations` holds the evaluator as a pure function: given parsed automations, the event and task facts, it returns a pass plan (each candidate automation in order is *matched* or *ask(prompt frame, answer schema)*). It has no store, HTTP or LLM handle. The executor is a second eventbus subscriber, `workflow-automations`, wired in `internal/api` beside `docwatch.go`, started only under a `BackgroundCtx`. It supplies facts, makes LLM calls, applies the failure mapping, and fires through the store.

Automations ride the workflow surfaces of WL-REQ-151: `GET`/`PUT .../workflows`, `lode project workflow`, `lode project set workflow` and the `workflow.write` grant.

