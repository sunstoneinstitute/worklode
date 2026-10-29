
`abandon` is the preferred close and records that work was considered and dropped. `delete` is a narrower close for a row that should never have existed: a mistyped `lode task add`, a duplicate import, dev re-seeding. Delete is a tombstone, orthogonal to state. It is not a state value, and the transition set is untouched.

| | `abandon` | `delete` |
|---|---|---|
| Says | considered and dropped | should not have existed |
| Is | a task state | a tombstone |
| Applies to | tasks | tasks and design documents |
| Leaves | a visible `abandoned` row | a hidden row and its events |
| Reversible by | reopen | undelete |

Tasks and documents each carry `deleted_at timestamptz`, `deleted_by text`, `delete_justification text`, all null on a live row and set together. `deleted_at IS NULL` is the whole predicate. Undelete clears all three. Both emit events (`task.deleted`/`task.undeleted`, `doc.deleted`/`doc.undeleted`) with the justification in the payload. Nothing is ever removed: events, `state_log`, edges and artifacts stay valid.

Surface: `DELETE /api/v1/tasks/{id}`, `DELETE /api/v1/docs/{id}` with optional `{"justification": "..."}`; `POST …/{id}/undelete`. All four are in `routeGuards` and need the permission the entity's other mutations need; there is no delete-specific role. `lode task delete <id> [--justification …]`, `lode doc delete <ref> [--justification …]`, `lode task undelete`, `lode doc undelete`. No `-j` shorthand. `lode task list --deleted` and `lode doc list --deleted` show tombstoned rows instead of live ones. Metric: `worklode_deletes_total{entity, op, outcome}` in `internal/api`, `outcome` in `ok | justification_required | not_found | error`.

