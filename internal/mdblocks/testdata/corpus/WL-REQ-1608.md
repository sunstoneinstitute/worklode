
An `approvals` row (WL-REQ-124) can be decided from every authenticated surface: the cockpit, `POST /api/v1/approvals/{id}/decide`, `lode approval decide` and the MCP server. The decider is the request's actor, and the store's checks apply on every surface: the row must be `awaiting` or `changes_requested`, nobody approves their own work, and the actor must be the row's named approver or hold its required role. A task-scoped token cannot decide.

A role-gated row needs fresh group claims. The actor row records when its groups were last synced from Keycloak. A session subject is fresh. A token subject is fresh only when that sync is at most 24 hours old; otherwise the decide is refused with 403 and the answer says to log in again (`lode login`, or reconnecting the MCP connector), which syncs the groups. A row that names an actor needs no group check.

CI's manually triggered prod publish workflow queries Worklode and fails without the approval. The CMS publish button fails without the story's approval, opt-in per post, degrading to advisory when Worklode is unreachable. GitHub `pull_request_review` ingest writes into the same table: an `awaiting` row when a task-correlated PR opens, resolved when the review lands. GitHub stays the review surface.

Rationale: a 30-day token carries the group claims of the login that minted it, so a person removed from a group could still approve as that group for weeks. Checking freshness at the decide keeps that bound without tying approval to the browser.
