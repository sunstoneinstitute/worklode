
A plan is a document. Its task definitions are draft task rows, and accepting it publishes them, with no root row above them (WL-REQ-174). `child_of` exists to decompose an oversized task. Every decision in this table applies to a task that has children, whatever its kind.

| Decision | Rule |
|---|---|
| Container identity | Inferred: has `child_of` children |
| Parents per task | Exactly one |
| Depth | Max 2 edges (task → subtask) |
| Parent claimable | Never: excluded from the ready set and rejected in `Claim` |
| Parent states | `in_review`, `deployed_dev`, `deployed_prod`, `released` rejected; only core states |
| Closure | Automatic roll-up, forward and backward |
| Progress | `closed_children / total_children` over direct children, derived on read |
| Cross-project children | Rejected (422) |
| Blocker inheritance | None; children do not inherit a parent's blockers |
| Child ordering | Out of scope |

Surface: `GET /api/v1/tasks/{id}` carries `hierarchy: { parent: {id, title, state} | null, progress: {closed, total} }`. `POST …/edges` rejects a second parent (409), a cross-project edge (422) and a depth overflow (422). `store.Brief` carries `Parent` (id, title, state) exactly one hop up. `lode task show` prints `Parent:` and `Progress: 3/7`; `lode task board` groups children under their parent.

