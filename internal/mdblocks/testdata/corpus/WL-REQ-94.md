
```sql
CREATE TABLE leases (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id     text NOT NULL REFERENCES tasks(id)  ON DELETE RESTRICT,
    actor_id    text NOT NULL REFERENCES actors(id) ON DELETE RESTRICT,
    worktree    text NOT NULL,
    acquired_at timestamptz NOT NULL,
    renewed_at  timestamptz,
    expires_at  timestamptz NOT NULL,
    released_at timestamptz
);
CREATE UNIQUE INDEX leases_active          ON leases (task_id)  WHERE released_at IS NULL;
CREATE UNIQUE INDEX leases_active_worktree ON leases (worktree) WHERE released_at IS NULL;
```

A lease is keyed by git-worktree identity. `worktree` is an opaque, stable string the agent harness supplies (recommended `<host>:<abs-worktree-root>`); the backbone never parses it. A task has at most one active lease and a worktree holds at most one task. Sessions come and go against the same worktree while the single lease persists. `DefaultLeaseTTL = 2h`.

| Step | Behaviour |
|---|---|
| Acquire | Through `Claim` (§7). `acquired_at = renewed_at = now`, `expires_at = now + ttl`. |
| Renew | `renew(taskID, actor)` sets `renewed_at` and `expires_at` on the active lease held by that actor. A non-holder gets `ErrNotFound`, indistinguishable from "no lease". An expired but unswept lease is still renewable. Renewal is a commit-cadence heartbeat: the pre-commit hook calls it before each commit batch. |
| Release | `release(taskID, actor)` sets `released_at`. A task still `in_progress` returns to `ready`; a task that moved on keeps its state. Non-holder gets `ErrNotFound`. Triggered by worktree removal: the lease dies with the worktree. |
| Expiry sweep | `ExpireLeases(now)` closes every active lease past `expires_at`, reverts each still-`in_progress` task to `ready`, and fires one `system` `lease.expired` event per lease, idempotent on `lease-expired-<leaseID>`. Each expiry re-checks in its own transaction. The server runs it on a ticker. |

Closing a lease by any path also stamps `ended_at` on every open `agent_sessions` row for it, in the same transaction, with no event of its own (WL-SPEC-80 §8.2).

Surface: `POST /api/v1/tasks/{id}/claim` (`worktree`, `ttl`), `…/renew`, `…/release`; holder identity is `(actor, worktree)`. `lode task claim|renew|release --worktree <id>`; the hooks supply the value. The claim and claim-next responses carry a server-derived `branch`. `lode task reopen <id>` is the reopen path.

