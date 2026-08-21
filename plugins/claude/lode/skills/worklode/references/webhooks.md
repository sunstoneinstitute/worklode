# How webhooks drive task state

Deep reference for the `worklode` skill: which ingested event does what.
Every handler below only *records a fact*; the two functions that actually
change task state (`store.Transition`, `store.ResolveDelivery`) are called
from the fact-recording code, never the other way around, so replaying an
old webhook (`lode inbox import`) is always safe.

## GitHub App (HMAC-signed)

| Event / action | Records | Task-state effect |
|---|---|---|
| `pull_request` `opened` / `ready_for_review` | the PR row, correlated to a task by branch name | If the correlated task is `in_progress`, moves it to `in_review`. Any other task state (`ready`, `merged`, …) is left alone — a correlation must never fail delivery. |
| `pull_request` `review_requested` | — | Reopens an approval row for the newly requested reviewer (spec 029 §7.1). |
| `pull_request_review` `approved` / `changes_requested` / `commented` | the review | Resolves (closes) the open approval this review decides. |
| `pull_request` `closed`, `merged: true` | the head SHA and merge-commit SHA as `task_commits` | **No state change yet.** The task advances only once one of these SHAs actually appears on the default branch via a `push` event — see below. The lease is deliberately left untouched (a merge doesn't free the worktree). |
| `push` to a repo's default branch | new `main_commits`, then finds every task whose recorded commit sits at or below the new frontier | Calls `ResolveDelivery` for each — see below. |
| `workflow_run` | a `ci_runs` row (status/conclusion/url) | None directly; visible on `lode timeline`. |
| `release` | an `artifacts` row (`git_tag`) | None directly. |
| `registry_package` | an `artifacts` row (`docker_image`/`pypi`) | None directly. |

Reading `merged` off `pull_request.closed` and *not* transitioning there is
the detail worth remembering: **the merge event records evidence, the push
event is what fires the transition.** A repo whose default branch a task's
commit never lands on stays stuck at whatever `in_review`/`ready` state it
was in — which is correct, not a bug, if the PR was closed unmerged, or
merged to a non-default branch.

## `ResolveDelivery` (called after a push, for every affected task)

Forward-only, order-independent — safe to call repeatedly, and safe when
facts arrive out of sequence (a prod deploy webhook that beats the dev one,
say). It advances a task to the *furthest* milestone its recorded facts
support, never backslides, and never touches `draft` or `abandoned` tasks:

1. Task's repo commit sits on `main` → `merged` (if not already past it).
2. The repo's configured `done_state` (`project_repos.done_state`, falls back
   to a package default) decides how far it can go without further
   evidence: some repos stop at `merged`, some go straight to `released`.
3. A Flux `Kustomization` reconciliation for the environment covering that
   commit → `deployed_dev`, then (separately) `deployed_prod`.

## Flux (HMAC-signed notification receiver)

One event per `Kustomization` reconciliation attempt:

| Condition | Effect |
|---|---|
| `severity: error`, or reason `ReconciliationFailed`/`HealthCheckFailed` | Deployment status → `failed`; emits a `flux_failure` runtime event. |
| reason `ReconciliationSucceeded` | Deployment status → `deployed`; calls into `ResolveDelivery` for tasks below this frontier. If the *prior* status was `failed`, also emits a `flux_recovery` runtime event. |
| anything else (`Progressing`, GC, …) | Deployment status → `reconciling`. No task effect. |

The artifact a Flux event concerns is resolved either by OCI digest or by
git-commit SHA (`revision` field), whichever the event's payload carries.

## Data catalog (HMAC-signed, `POST /hooks/catalog`)

Reports what a catalog knows about an artifact address, as **deliverable
evidence** (spec 029 §3.2: deliverable state is reported by emitters, never
asserted by a human closing a task). **No task-state effect at all** — this
path never calls `Transition` or `ResolveDelivery`.

Routing is unlike every path above. GitHub correlates by repo and branch;
this one is a *declaration lookup*: the payload's `artifact` address is
matched against `artifact_declarations`, and the fact is filed against every
still-open entity that declared it. "Open" is per kind — a deliverable
always (it stores no state to be closed by), a task by `taskClosed` (the same
per-repo `done_state` predicate that decides blocking), a doc unless
`superseded`.

| Ack | Meaning |
|---|---|
| `ok` | Recorded, and evidence filed against at least one open declarer. |
| `duplicate` | Already seen, by `X-Catalog-Delivery` or body hash. No second row. |
| `unrouted` | Recorded, but nothing open declares that address. Not an error. |

Today only a deliverable can declare an address, via `artifact` on `POST
/api/v1/projects/{id}/deliverables`. **The contract is provisional** — no
data-platform emitter exists yet; see the doc block atop
`internal/hooks/catalog.go` for the payload.

## Kubernetes pod watcher (`lode watch`, not a webhook)

An informer, not an ingest endpoint: it watches pod status directly and
writes `runtime_events` of kind `crashloop` or `oom` when a container's
termination reason matches, deduped per (pod, container, kind). These are
observability facts only — no task-state effect. `lode watch` is what a
cluster runs continuously; nothing about it is triggered by GitHub or Flux.

## `lode inbox import`

Not a webhook — a backfill path that replays GitHub issues/PRs through the
*same* store functions the live webhook uses, so running it twice, or
running it after the live webhook already saw an event, is safe: the
event log's `(source, external_id)` uniqueness absorbs the duplicate before
any of the above logic runs again.
