
`lode rule supersede` never moves a governing link. It records one `task.governance_superseded` event per task governed by a rule it withdraws, naming the old rule, its successors and the actor. A task read (`lode show <task>`, `GET /api/v1/tasks/{id}`) lists, for each withdrawn governing rule, the live rules reached through `supersedes` edges, transitively, as `resolves_to`. Governance changes are recorded in the event log only, never as task state transitions.

