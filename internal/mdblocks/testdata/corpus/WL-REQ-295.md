
Client-side, useful with the server unreachable. Checks in order, each reporting pass, fail or skip and the fix; a later check still runs when an earlier one fails:

1. Config file found, and where the `.worklode`/`.lode` walk-up located it.
2. `server` set and reachable.
3. Token present (OS keychain or `LODE_TOKEN`) and accepted, via `GET /api/v1/whoami`.
4. `current_project` set and the project exists.
5. The `[gate]` table in `.worklode/config.toml` parses (WL-SPEC-72 §3).
6. The Worklode `pre-commit` git hook is installed in this repo.
7. Inside a worktree: it maps to a task, and that task holds a live lease.
8. Materialized secrets whose lease is gone are reaped (WL-SPEC-74 §10.6).
9. Edge Agent reachable and healthy, so no token-accounting gap.
10. Inline references in the current project are extracted and normalized: a dry run of `lode doc derive`, `lode rule derive` and `lode task derive` (WL-REQ-1860) reports no changes. On changes it warns with the node counts and the fix `lode <kind> derive`. It skips when the server is unreachable.

Exits non-zero on any failure, so it can run from a hook or CI step.
