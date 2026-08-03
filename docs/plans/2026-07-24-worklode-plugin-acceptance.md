---
status: superseded
implements: docs/specs/008-worklode-plugin.md
---
# Worklode plugin — acceptance walkthrough (spec 008)

Scripted verification of the seven spec-008 acceptance criteria. Items marked
**auto** are already proven by the Go test suite (`go test ./... -count=1` and
`go test -tags e2e ./e2e/`); items marked **human** need a live server plus the
`lode` plugin installed in Claude Code and are left for the user to run.

Prereqs for the human steps:
- Local backbone up: `export LODE_BOOTSTRAP_TOKEN=wl_$(openssl rand -hex 20); docker compose up -d`.
- `lode` CLI on PATH (`go install ./cmd/lode`) and pointed at the server
  (`~/.config/worklode/config.toml` with `server`/`token`).
- The `lode` plugin installed from the Sunstone marketplace
  (`sunstoneinstitute/claude-plugins`, `plugins/lode/`); `lode` on PATH so the
  hook shim is live.

## 1 — `/lode:next` binds a worktree + lease + brief; plain checkout untouched (human)

1. In a clean repo checkout, start a Claude session and run `/lode:next`.
   Expect: a `wt/<WL-n>-<slug>` worktree created, branch `wl/<WL-n>-<slug>`, the
   lease bound to `<hostname>:<abs-worktree-path>` (check `lode status --json`),
   and the brief injected into context.
2. In a second plain checkout of the same repo, start a session but do NOT run
   `/lode:next`. Expect: no Worklode output, no claim, no worktree — session
   completely untouched (SessionStart hook is a NOP outside a `wt/` dir).

Backed by: A3 (`lode next`), B1 (SessionStart NOP guard), B2 (`next` skill).
E2E: `TestNextEndToEnd` proves the worktree+lease+rebind path.

## 2 — remove releases, commit renews, neither fires outside (human + auto)

1. `lode status` in the worktree → lease held. `git worktree remove <dir>` →
   `lode status`/backbone shows the lease released (WorktreeRemove hook).
2. In a fresh `/lode:next` worktree, note `renewed_at` via `lode status`, make a
   commit, re-check → `renewed_at` advanced (pre-commit hook heartbeat).
3. Run a commit and a worktree removal in a plain checkout → neither touches any
   lease (guard NOP).

Backed by: A4 (hook events), B1 (hooks.json wiring). **auto** for the guard-NOP
and pre-commit-renew paths: `hookrun` tests assert no API calls outside a `wt/`
worktree and a renew call for pre-commit inside one.

## 3 — sweeper-expired lease re-acquired by resume/auto-resume, no new claim (human + auto)

1. Claim with a short TTL (`lode next` then let the sweeper expire it, or claim
   with a small `ttl`). Task returns to `ready`, worktree still on disk.
2. `/lode:resume` in that worktree → re-acquires the same task with this
   worktree's identity, no collision, brief re-injected.
3. Auto-resume: open a new session in the worktree (SessionStart) → same
   re-acquire, no duplicate claim.

Backed by: A3 (`lode resume`), A4 (`session-start`/`worktree-create` auto-resume).
E2E: `TestPickupLoop` exercises the claim→resume path.

## 4 — install-git-hooks chains existing hook + framework, idempotent (auto)

Fully covered by A5 Go tests (`internal/cmd/installhooks_test.go`): fresh
install; idempotent re-run; existing third-party `pre-commit` preserved as
`.pre-lode` and chained; `.pre-commit-config.yaml` present → chains the
framework binary; installed hook + guard NOP → `git commit` succeeds in a plain
repo with no lode server running.

## 5 — `lode task brief <id> --json` is one bounded payload (auto)

Covered by A2 store/handler tests: the Brief carries task + concern/priority +
branch + open blockers + active lease, with `governing_design`,
`affected_components`, and `definition_of_done` present in the shape (reserved
as null/empty until spec 006). No file contents, no unbounded lists.

## 6 — compiled hooks daisy-chain via `--next` (auto)

Covered by A4 `hookrun` tests: a guard-NOP event with `--next touch <tmpfile>`
still runs the downstream command; `install-git-hooks` composes the chain.

## 7 — skills carry judgment only, no renewal logic (auto/inspection)

`working-under-worklode` (B3) contains the done/block/release judgment loop and
explicitly excludes heartbeats/renewal/TTLs. Partial per plan scope: the two
graph-fed skills (`architectural-review`, `authoring-design-as-graph`) are
deferred until spec 006 exists — noted as out of scope in the plan header.

## Deferred / not covered here

- `/lode:spec`, `authoring-design-as-graph`, `architectural-review` (need the
  spec-006 knowledge graph).
- Task ↔ GitHub-Issue mirror (Q008.4).
