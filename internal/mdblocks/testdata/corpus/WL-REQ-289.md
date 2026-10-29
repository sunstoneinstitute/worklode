
- A row is created on the first heartbeat from a session, under the lease active at that moment.
- A heartbeat on a closed row re-opens it (`ended_at` back to NULL). The row spans first touch to last without recording gaps.
- `last_seen_at` is bumped on every heartbeat. A session is running while `ended_at IS NULL` and its lease is unreleased; a session on an expired lease can sit unstamped, so the lease check is required.
- `ended_at` is stamped by the session-end hook and by `closeLease` / `CloseActiveLease` for any session still open on the lease being closed. A close by that route emits no `agent_session.ended` event because the lease's own `lease.released` / `lease.expired` event records the transition, so started/ended pairs are deliberately unbalanced.

