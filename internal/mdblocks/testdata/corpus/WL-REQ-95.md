
```
Claim(ctx, taskID, actorID, worktree, ttl) (*Lease, error)
```

Executed as the `apply` callback of `RecordEvent("cli", <minted id>, "lease.claimed", …)`:

1. `SELECT … FROM tasks WHERE id = $1 FOR UPDATE`. Concurrent claims of the same task serialize here; claims of different tasks never contend. Unknown task gives `ErrNotFound`.
2. Verify the actor exists (`ErrNotFound`).
3. Verify no active lease (`ErrLeased`).
4. Verify not blocked: `IsBlocked` (`ErrBlocked`).
5. Verify claimable: not a task with children, not `decision` or `rally` kind, not labelled `needs-decomposition`, not deleted.
6. `Transition(tx, now, taskID, "ready", "in_progress", eventID)` (`ErrBadTransition`).
7. `INSERT INTO leases … RETURNING id`. The `leases_active` index is the backstop: a unique violation (`23505`) maps to `ErrLeased`.

All steps, the event row and the `state_log` row commit or roll back together. `Claim` is a total, atomic function of one candidate id. Ranking, candidate construction and retry policy live in §8; on a lost race `claim --next` re-ranks and tries the next candidate.

