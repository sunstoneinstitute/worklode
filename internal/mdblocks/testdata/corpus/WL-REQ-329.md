
The cockpit stores no parallel completion percentage, editable project health,
manually advanced workflow column, or duplicated delivery or publication
status. Every displayed status keeps its evidence category, shown as a chip
beside it.

| Category | Meaning |
|---|---|
| declared | intended scope, requirement, owner, deliverable or policy |
| user-reported | an authenticated person's explicit report or decision |
| observed | a fact ingested from GitHub, CI, Payload, a data pipeline, Kubernetes/Flux or another emitter or prober |
| recommended | an AI-produced interpretation; auditable, and never itself satisfying a human gate |

A task state with no event behind it is declared. An event from GitHub, Flux,
the watcher or the server is observed, and so is a CLI lease event; any other
CLI event is user-reported (`stateEvidence`, `internal/api/cockpit.go`).

