
A sandbox session finds its server and identity in `LODE_SERVER` and `LODE_TOKEN` in the environment. The token is a task-scoped token minted through `POST /api/v1/tasks/{id}/tokens`, bound to the task, attributed to an agent actor, expiring and revoked with the lease. Its model is in WL-REQ-48.

