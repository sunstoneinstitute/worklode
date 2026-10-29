
Every route here is named in `routeGuards`. Claim, renew, release, reopen, `set state`, abandon, edge, decompose, delete, undelete and `POST /api/v1/merges` need the task-mutation permission; project creation and repo mapping need the admin-side permission; webhook routes are open and HMAC-signed. Roles and the grants table are defined in WL-SPEC-74 §3. There is no MCP surface: agents drive `lode --json` directly.

