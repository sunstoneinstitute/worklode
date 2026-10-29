
| Endpoint | Gate | Returns |
|---|---|---|
| `POST /api/v1/reconcile` | admin | run report; body `{repo?, task?, since?, dry_run}`; synchronous |
| `GET /api/v1/repos/doctor[?repo=]` | admin | ingestion-health report |
| `GET /api/v1/whoami` | auth only | calling actor's id, kind, admin flag |

Acceptance: a replayed `*.ignored` event produces exactly the typed-table and `state_log` result of a live delivery with the transition referencing the original event, and a second replay changes nothing; a task whose PR merged while ingestion was down reaches its delivery state in one run, attributed to `reconcile.poll`, and a second run is a no-op; both doctors name the fix for each failure; every command emits deterministic `--json`.

