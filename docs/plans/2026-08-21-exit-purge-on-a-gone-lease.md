---
status: draft
covers:
- docs/specs/048-exit-purge-on-a-gone-lease.md#sec-2
- docs/specs/048-exit-purge-on-a-gone-lease.md#sec-3
---
# Exit purges secrets on a definitely gone lease — implementation plan

ADR 048 resolves the WL-107 gap: `handleWorktreeExit` purges a task's
materialized secrets only after a definite backbone "lease gone" answer.
One hook, one backbone call, one conditional purge — a single task.

Everything needed already exists: `purgeSecrets` (internal/hookrun),
`(*cli.Client).GetTask` returning `model.TaskDetail` with a nullable `Lease`,
`cli.ClientError` carrying the HTTP status, and the package's
`backboneTimeout` (2s) bounding every hook backbone call.

## Tasks

### Task 1 — Purge secrets on worktree exit when the lease is definitely gone

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
```

In `internal/hookrun/hookrun.go`:

- Add a helper `leaseGone(ctx, c, taskID) (gone, known bool)` — three-valued
  on purpose, per ADR 048 §2:
  - `c.GetTask` succeeds and `detail.Lease == nil` → `(true, true)`;
  - the error is a `*cli.ClientError` with `Status == http.StatusNotFound` →
    `(true, true)`;
  - anything else (timeout, transport error, 5xx, 401/403, decode failure) →
    `(false, false)` — the backbone did not answer the question.
- In `handleWorktreeExit`, after `closeSession`: build the client via
  `opts.client()` (a config failure warns and skips the check), wrap `ctx`
  with `backboneTimeout`, call `leaseGone`, and call
  `purgeSecrets(opts, taskID)` only when `gone && known`. One attempt, no
  retry; every failure path warns to stderr and the hook still exits 0.
- Rewrite `purgeSecrets`'s doc comment: it is no longer "bound to worktree
  removal, not to a session leaving" — removal/done/block call it
  unconditionally (local-only, before any backbone call), exit calls it
  conditionally after a definite "gone" (ADR 048 §2 owns the rationale).

In `internal/hookrun/hookrun_test.go`, drive `worktree-exit` against a stub
server (the package's existing pattern) and assert on the materialized
manifest/keystore fixtures:

- task exists, no lease → purged;
- task 404s → purged;
- task exists with a live lease → kept (this replaces WL-36's unconditional
  "exit never purges" expectation);
- server returns 500 → kept, hook exits 0;
- server unreachable (closed port) → kept, hook exits 0, exit completes
  within the backbone budget;
- purge outcome never changes the hook's exit code.

- [ ] `leaseGone` helper with the three-valued contract
- [ ] conditional purge wired into `handleWorktreeExit` after `closeSession`
- [ ] `purgeSecrets` doc comment matches ADR 048
- [ ] tests above green under `make test`
