---
status: accepted
issued: 2026-07-25
amends:
  ".":
    - 003-platform-graph-design.md#sec-5
    - 004-execution-backbone.md#sec-4
    - 004-execution-backbone.md#sec-5
amendedBy:
  ".":
    - 018-task-hierarchy.md
  "#sec-6":
    - 014-design-documents-as-graph-objects.md#sec-11.3
replaces:
  ".":
    - 004-execution-backbone.md#sec-1.2
---
# Spec 011 — Delivery lifecycle

## 0. Problem {#sec-0}

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

## 1. State machine {#sec-1}

```
draft → ready → in_progress → in_review → merged → deployed_dev → deployed_prod
                     │                     ↑ │  ↘ released        (terminal)
                     └─────────────────────┘ └──── (terminal, release-based repos)
                        direct-to-main jump
```

New states: `deployed_dev`, `deployed_prod`, `released`.

| State | Meaning |
|---|---|
| `merged` | Work landed on the default branch (auto-detected or manual `lode task done`) |
| `deployed_dev` | Deploy to a dev/test environment covering the landed commit: GitHub `deployment_status: success` **and** Flux `ReconciliationSucceeded` |
| `deployed_prod` | Same, for prod |
| `released` | GitHub release published covering the landed commit |

Each repo mapping carries a **`done_state`** — the terminal state that counts
as fully delivered for that repo: `merged`, `deployed_prod`, or `released`.
Discovery defaults it (prod env → `deployed_prod`; releases without a prod
env → `released`; neither → `merged`); it can be set explicitly on the repo
mapping. A task at its repo's `done_state` is fully delivered; states past it
are never expected for that repo.

`done_state` also selects which delivery branch the resolver walks. A repo
with `done_state = released` follows `merged → deployed_dev → released` and
ignores prod deploys — `deployed_prod → released` is deliberately not a legal
transition, so advancing on a prod deploy would strand such a repo's tasks one
hop short of delivered, permanently. Every other repo follows `merged →
deployed_dev → deployed_prod` and ignores releases. The asymmetry for
`done_state = merged` is deliberate: those tasks still advance past `merged`
when deploy facts exist, because `merged` is also the default for repos
discovery has not profiled yet, and real deploy signals outrank a default.

New legal transitions:

- `ready|in_progress|in_review → merged` — landing on main advances the task
  from wherever it sat; the resolver never advances a `draft`.
- `merged → deployed_dev | deployed_prod | released` — skipping `deployed_dev`
  is legal (prod-only repos, or a missed dev signal).
- `deployed_dev → deployed_prod | released`.
- Reopen: `deployed_dev|deployed_prod|released → ready`, same as `merged → ready`.
  Reopening also clears the task's `task_commits` in the same transaction, so
  the next webhook cannot resolve the task straight back to the delivery state
  it was reopened out of. Delivery is re-earned by the new work landing.
- `abandoned` stays reachable only from pre-`merged` states.

All delivery transitions are forward-only; the resolver never walks a task
backward.

> **Corrected against spec 004 §2.** This paragraph originally ended: "Landing
> on main closes any active lease (as PR-merge does today)." That sentence
> wrote down inherited GitHub-webhook behaviour and contradicts the model of
> record: the lease is worktree-scoped (004 §2) and ends only on `release`,
> `abandon`, `reopen`, or the expiry sweep. Delivery transitions — PR merge,
> landing on main, deploy to dev or prod, release published — never close the
> lease. Closing on merge served neither purpose a lease has: mutual exclusion
> is already enforced by `Claim` requiring state `ready`, so a `merged` task is
> unclaimable regardless of its lease, and liveness recovery is already the
> 2h TTL expiry sweep. Delivery state is a fact about the code; the lease is a
> fact about who occupies the worktree — and work legitimately continues in a
> worktree after its branch is deployed to dev. Whether manual `lode task done`
> also stops closing the lease is undecided and not addressed here.

Environment-name normalization: `dev`, `test`, `development`, `staging` → dev
stage; `prod`, `production` → prod stage; everything else (`copilot`,
`github-pages`, `pypi`, `*-apply`, …) is ignored. This normalization applies
to GitHub environment names only: `LODE_CLUSTER_ENV_MAP` (cluster → stage, for
Flux events) is operator config and is validated at startup to contain nothing
but `dev` and `prod`, since any other value would record `deployments` rows
that can never advance a task.

`projects.deploy_gated` is retired; this mechanism replaces it.

## 2. Fact tables {#sec-2}

Four tables, written inside the same `RecordEvent` transaction as the
webhook that produced them:

**`task_commits`** — `(task_id, repo, sha, source, seen_at)`. Attributes
commits to tasks. Sources:

- pushes to `<prefix><task-key>[-slug]` branches. The prefix is configurable
  (`LODE_BRANCH_PREFIX`, default `lode/`, replacing the hardcoded `wl/`); the
  legacy `wl/` prefix stays recognized for correlation whatever the setting.
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

The `flux_seen` latch is permanent and has no un-latch path: once a Flux
revision has correlated for a repo/env, that pair requires both signals
forever, so a repo whose Flux wiring is later removed strands its tasks at
`merged`. A revision that correlates to the wrong repo (shared history between
tracked repos) latches a pair onto a signal that will never arrive. The
handler logs `flux delivery gating latched` (repo, environment, revision) on
the transition — that line is the operator's only trace when diagnosing tasks
stuck at `merged`; the fix is a `flux_seen` reset in the database.

**`release_frontiers`** — `(repo, tag, main_seq, published_at)`. A
`release.published` event records the seq the tag covers; tasks at or below it
count as released. The frontier is the release's `target_commitish` when that
resolves to a known main commit, so a backport tag covers only what it
contains; `target_commitish` is often a branch name (UI-created tags), which
does not resolve, and the frontier then falls back to main's head as of the
webhook's arrival — right for release-on-merge. Forward-only per tag.

## 3. Handlers and resolver {#sec-3}

All handlers use the existing HMAC/idempotency plumbing in `internal/hooks`.

- **`push`** (new): by ref — `<prefix>*` → insert `task_commits`; default
  branch → append `main_commits`, set landed seqs; `last-deploy/<env>` →
  record deploy-branch-SHA → main-seq mapping from `main-sha:` trailers.
- **`deployment_status`** (new): normalize environment, resolve deployment
  SHA to a main seq, upsert `env_deploys.gh_status`. A SHA that is not yet
  known on main is dropped in v1 (no pending-facts store); the next deploy of
  that repo re-establishes a frontier that covers it.
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

**Repo delivery profile**: on `project add-repo`, fetch the repo's environment
list via the GitHub App (`internal/githubauth`) and note whether it uses
releases. This seeds the repo's `done_state`, which stays explicitly settable
with `lode project set-repo --done-state` (v1 has no lazy re-discovery from
webhook traffic). Discovery never gates transitions: if it fails, the repo
keeps `done_state = merged` and states still advance from events alone.

GitHub App requirements: repository permissions **Actions: read**
(environment discovery, `GET /repos/{owner}/{repo}/environments`),
**Deployments: read** (`deployment_status` webhook events), **Contents:
read** (`push` and `release` webhook events, and `GET
/repos/{owner}/{repo}/releases/latest` for discovery); webhook subscriptions
for `push` and `deployment_status` added alongside the existing events.
Without Actions: read the environments call 403s, discovery fails, and every
repo keeps the default `done_state = merged`, so tasks stop advancing at
`merged`. The only trace is the server's `discover repo done_state` warn log
at `add-repo` time; the repair is `lode project set-repo <repo> --done-state`.

Deferred: some repos deliver multiple artifacts (data-platform ships two
docker images plus a python library; worklode a docker image plus a CLI
binary via brew tap). v1 models one `done_state` per repo; per-artifact
delivery tracking is future work.

**Deployment config** (not code): Flux notification-controller in every
cluster gets a Provider/Alert pointing at worklode's `/hooks/flux`.

## 4. Surface changes {#sec-4}

- New states flow through existing surfaces: task JSON, `lode task list`
  filters, web UI badges.
- Task timeline shows delivery facts: landed on main at `<sha>`, dev deploy
  confirmed, prod deploy confirmed, released in `<tag>`.
- `lode task done` remains the manual escape hatch.
- `lode project add-repo` output gains the discovered delivery profile.
- The claim and claim-next API responses carry a server-derived `branch`, so
  the branch prefix is configured in one place; the CLI only falls back to
  `lode/` against a server too old to send one.

Repos shared across projects (`provisioning`, `admin-cluster`,
`rdf-registry`, …) need no special handling: delivery advances a task via the
repo its own commits landed in (`task_commits`), never by fan-out through
project→repo links. A delivery in a shared repo affects exactly the tasks
correlated to it; cross-project impact is modeled as multiple linked tasks.

Deferred: a single task whose work spans several repos (e.g. adding a new
application touches the app repo plus `admin-cluster` or `provisioning`)
still tracks delivery only through its primary repo in v1. Multi-repo task
delivery — possibly spotted by watching Flux events for the companion repos —
is future work.

## 5. Migration {#sec-5}

One schema version: create `task_commits`, `main_commits`, `env_deploys`,
`release_frontiers`; extend the `tasks.state` CHECK constraint; drop
`projects.deploy_gated`. Existing `merged` tasks stay `merged` — no backfill.

## 6. Coordination with WL-12 {#sec-6}

> **Amended by spec 014 §11.3.** The `<PROJECTKEY>-{ADR,SPEC}-<n>` alias below is adopted,
> with `<n>` taken from the document's own filename number instead of the task sequence — the
> shorthand exists so a reference is typeable without a lookup.

The branch pattern is a configurable PREFIX (default `lode/`, replacing the
hardcoded `wl/`) followed by a task key: `<prefix><task-key>[-slug]`. Task
keys MUST conform to `^[A-Z]+-\d+$`, matching the per-project keys WL-12
lands; both changes touch the same regexes. Possible later exception: ADRs
and SPECs addressable through a `<PROJECTKEY>-{ADR,SPEC}-<n>` alias (e.g.
`WL-ADR-1`, `WL-SPEC-14`), the numbers drawn from the same sequence as tasks.

## 7. Error handling {#sec-7}

- A failed correlation never fails a delivery (existing principle).
- An unresolvable SHA is dropped, not parked — v1 has no pending-facts store.
  A `deployment_status` for a SHA with no known main commit records nothing
  and self-heals on the repo's next deploy, whose frontier covers it too. A
  Flux revision that correlates to no repo records nothing and does not latch.
  A release whose `target_commitish` does not resolve falls back to main's
  head instead of dropping.
- Idempotent under redelivery: facts are natural-key upserts; transitions are
  forward-only.

## 8. Known limitations (v1) {#sec-8}

- **Push payloads carry at most 2048 commits.** A larger push drops the rest
  from `main_commits`, so a task whose landing commit falls outside that
  window is never attributed and strands at `in_review`. Those commits are
  recoverable only by reconciliation (spec 013); fetching the full range
  through the App is not worth its cost at this cap.

  Truncation is at least never silent. There is no truncation flag on the
  payload, but the `commits` array is every commit between `before` and
  `after`, so an `after` absent from it proves the delivery was partial.
  That case increments `worklode_webhook_push_truncated_total` and logs the
  repo, ref and sha range. The delivery still applies the commits it did
  carry — dropping it would lose those too.
- **Pushes over 25 MB of payload are not delivered at all.** GitHub drops the
  webhook rather than truncating it, so nothing arrives to detect and the
  push is invisible until reconciliation.
- **The `flux_seen` latch never releases** (see `env_deploys` above):
  a repo/env that once correlated a Flux revision requires both signals
  forever.
- **Discovery runs only at `add-repo`.** A repo that later gains a prod
  environment or starts cutting releases keeps its old `done_state` until
  `lode project set-repo --done-state`.

## 9. Testing {#sec-9}

- Fixture-based handler tests for `push` and `deployment_status`
  (`testdata/github` style).
- Table-driven resolver tests feeding identical fact sets in every arrival
  order, asserting identical outcomes.
- One end-to-end test: claim → branch push → merge → dev deploy (GitHub +
  Flux) → prod deploy → `deployed_prod`.
