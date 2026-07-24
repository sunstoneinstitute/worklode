# Delivery lifecycle — design

**Date**: 2026-07-25
**Status**: Approved design, pending implementation plan

## Problem

The task state machine ends at `merged`, reached only from `in_review` (PR-merge
auto-transition or manual `lode task done`). This misses two realities:

1. Work sometimes lands on `main` without a PR; nothing detects it.
2. "Merged" is not "delivered". A service (data-platform, worklode) is delivered
   when it runs in prod, via two stages (dev, then prod). A library
   (sunstone-py) is delivered when a release is published. Today nothing
   tracks either; the per-project `deploy_gated` flag only *blocks* the
   merge→merged transition and nothing ever unblocks it.

The goal is one generic, event-driven mechanism for all sunstoneinstitute
repos — no per-repo lifecycle configuration.

## State machine

```
draft → ready → in_progress → in_review → merged → deployed_dev → deployed_prod
                     │                     ↑ │  ↘ released        (terminal)
                     └─────────────────────┘ └──── (terminal, release-based repos)
                        direct-to-main jump
```

New states: `deployed_dev`, `deployed_prod`, `released`.

| State | Meaning | Terminal for |
|---|---|---|
| `merged` | Work landed on the default branch (auto-detected or manual `lode task done`) | Repos with no deployable envs and no releases |
| `deployed_dev` | Deploy to a dev/test environment covering the landed commit: GitHub `deployment_status: success` **and** Flux `ReconciliationSucceeded` | — |
| `deployed_prod` | Same, for prod | All repos with a prod env |
| `released` | GitHub release published covering the landed commit | Repos without a prod env |

New legal transitions:

- `ready|in_progress|in_review → merged` — landing on main advances the task
  from wherever it sat; the resolver never advances a `draft`.
- `merged → deployed_dev | deployed_prod | released` — skipping `deployed_dev`
  is legal (prod-only repos, or a missed dev signal).
- `deployed_dev → deployed_prod | released`.
- Reopen: `deployed_dev|deployed_prod|released → ready`, same as `merged → ready`.
- `abandoned` stays reachable only from pre-`merged` states.

All delivery transitions are forward-only; the resolver never walks a task
backward. Landing on main closes any active lease (as PR-merge does today).

Environment-name normalization: `dev`, `test`, `development`, `staging` → dev
stage; `prod`, `production` → prod stage; everything else (`copilot`,
`github-pages`, `pypi`, `*-apply`, …) is ignored.

`projects.deploy_gated` is retired; this mechanism replaces it.

## Fact tables

Four tables, written inside the same `RecordEvent` transaction as the
webhook that produced them:

**`task_commits`** — `(task_id, repo, sha, source, seen_at)`. Attributes
commits to tasks. Sources:

- pushes to `<prefix><task-key>[-slug]` branches. The prefix is configurable
  (`LODE_BRANCH_PREFIX`, default `lode/`), replacing the hardcoded `wl/` in
  `internal/store/changes.go`.
- PR correlation (existing head-ref / `WL-Task:` body mechanisms; the PR's
  SHAs join this table).
- task-key references in commit messages on default-branch pushes (fallback
  for commits made directly on main).

**`main_commits`** — `(repo, sha, seq, pushed_at)`. Every default-branch push
appends its commits in order. Main history is linear in push order, so
"commit X is included in the state at commit Y" is `seq(X) <= seq(Y)` — no
git ancestry calls, no clone. A task's **landed seq** is the seq of the main
commit that matched it (a `task_commits` SHA in the push, a merge-commit
message naming the task branch, or a task-key marker in a commit message).

**`env_deploys`** — `(repo, environment, main_seq, gh_status, flux_status,
updated_at)`, environment normalized to `dev`/`prod`. The per-environment
**deployed frontier**:

- GitHub `deployment_status: success` sets `gh_status`. A deployment SHA on
  main resolves to its seq directly; a `last-deploy/*` SHA resolves via the
  `main-sha:` trailers in the cherry-picked commits (visible in the push
  payload for that branch).
- A Flux `ReconciliationSucceeded` whose revision SHA matches a known
  deploy-branch or main SHA for the repo sets `flux_status` (environment via
  the existing cluster→env mapping). Flux failures mark the attempt failed,
  surfacing reconciliation failures.

A frontier is confirmed at seq N only when both signals are present. Every
task with landed seq ≤ N is covered — one integer comparison handles batched
deploys carrying many tasks.

Dual-signal gating requires that Flux events for the repo/env are actually
correlatable (revision SHAs matching the repo's branches). For a repo/env
where no Flux revision has ever matched — a deploy not reconciled by Flux, or
a cluster whose webhook isn't wired yet — the GitHub signal alone confirms
the frontier; the first matching Flux revision upgrades that repo/env to
dual-signal gating permanently. This prevents tasks stranding at `merged` while
still enforcing Flux confirmation everywhere it exists.

**`release_frontiers`** — `(repo, tag, main_seq, published_at)`. A
`release.published` event records the latest main seq at publish time; tasks
at or below it count as released. Sufficient because our releases tag main's
head.

## Handlers and resolver

All handlers use the existing HMAC/idempotency plumbing in `internal/hooks`.

- **`push`** (new): by ref — `<prefix>*` → insert `task_commits`; default
  branch → append `main_commits`, set landed seqs; `last-deploy/<env>` →
  record deploy-branch-SHA → main-seq mapping from `main-sha:` trailers.
- **`deployment_status`** (new): normalize environment, resolve deployment
  SHA to a main seq, upsert `env_deploys.gh_status`.
- **Flux handler** (extended): also resolve revision SHA to `(repo, main
  seq)` and update `env_deploys.flux_status`. Existing `deployments`-table
  behavior unchanged.
- **`pull_request`** (changed): merged-PR handling records facts only; the
  `in_review → merged` transition moves into the resolver. The `deploy_gated`
  branch is deleted.
- **`release`** (extended): still creates the artifact; also records the
  release frontier.

**Resolver** — `ResolveDelivery(tx, taskID)`: reads the task's landed seq,
env frontiers, and release frontier; computes the furthest supported
milestone; issues forward-only transitions (multi-step in one resolve when
signals arrived out of order). Every handler calls it for affected tasks at
the end of its apply. All lifecycle rules live here; handlers only record
facts. Arrival order of GitHub and Flux events therefore never matters.

**Repo delivery profile** (display only, never gates transitions): on
`project add-repo` and lazily on webhook traffic, fetch the repo's
environment list via the GitHub App (`internal/githubauth`) and note whether
it uses releases. Feeds UI/CLI hints like "merged — awaiting dev deploy". If
discovery fails, everything still works from events alone.

**Deployment config** (not code): Flux notification-controller in every
cluster gets a Provider/Alert pointing at worklode's `/hooks/flux`.

## Surface changes

- New states flow through existing surfaces: task JSON, `lode task list`
  filters, web UI badges.
- Task timeline shows delivery facts: landed on main at `<sha>`, dev deploy
  confirmed, prod deploy confirmed, released in `<tag>`.
- `lode task done` remains the manual escape hatch.
- `lode project add-repo` output gains the discovered delivery profile.

## Migration

One schema version: create `task_commits`, `main_commits`, `env_deploys`,
`release_frontiers`; extend the `tasks.state` CHECK constraint; drop
`projects.deploy_gated`. Existing `merged` tasks stay `merged` — no backfill.

## Coordination with WL-12

The branch-pattern change (hardcoded `wl/` → configurable prefix, default
`lode/`) touches the same regexes as WL-12 (per-project task keys). This
design assumes task keys are `<PROJECTKEY>-<n>` as WL-12 lands; the branch
pattern becomes `<prefix><task-key>[-slug]`.

## Error handling

- A failed correlation never fails a delivery (existing principle).
- Unresolvable SHAs are still recorded as facts; the resolver simply doesn't
  advance.
- Idempotent under redelivery: facts are natural-key upserts; transitions are
  forward-only.

## Testing

- Fixture-based handler tests for `push` and `deployment_status`
  (`testdata/github` style).
- Table-driven resolver tests feeding identical fact sets in every arrival
  order, asserting identical outcomes.
- One end-to-end test: claim → branch push → merge → dev deploy (GitHub +
  Flux) → prod deploy → `deployed_prod`.
