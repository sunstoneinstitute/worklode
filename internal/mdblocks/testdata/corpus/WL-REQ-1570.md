
A requested TTL longer than 24 hours for a task-scoped token (WL-REQ-48) is refused with 422.

A task-scoped token:

- reaches a route only when that route's guard grants task scope: either the path's `{id}` is the bound task, or the route is a worker route that carries no task id (reads, filing follow-ups, document authoring). Every other route answers 403 `token is scoped to task <id>`;
- cannot mint tokens;
- has no administrative access;
- is revoked, together with every other outstanding token of the task, in the same transaction as any path that ends the lease: release, done, abandon, or the expiry sweep.

The task scope of each route is a column of the route guard table (WL-SPEC-74#sec-3a) and is enforced by one middleware, never by per-handler checks.

