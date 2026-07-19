# work-tracker — spec

**Status:** approved design, v1
**Date:** 2026-07-19

## Why

TASKS.md + mirrored GitHub issues is a hand-maintained two-way sync with
lock-by-convention. It has race conditions, needs care to use, and is slow.
This system replaces it: one authoritative view of all planned and in-progress
work across the org, with real locking/leasing, tracking each task from
planning through review, build, deployment, and runtime.

## Decisions (settled)

| Decision | Choice |
|---|---|
| Scope | Org-wide (all Sunstone repos) from day one |
| Language | Go (single binary, several subcommands) |
| Storage | SQLite, single writer; litestream for replication/backup |
| Migrations | golang-migrate, embedded migration files, applied on startup and via `wt migrate` |
| Data model | Append-only event log + typed current-state tables, updated in the same transaction |
| Source of truth | The tracker. GitHub issues are an *inbox*: triage promotes them into tasks. No bidirectional sync. |
| GitHub ingest | One org-installed GitHub App delivering webhooks (issues, PRs, reviews, CI, releases, deployment status) |
| Flux ingest | flux notification-controller `Provider` (generic webhook + HMAC) + `Alert` resources |
| Pod-level ingest | `wt watch`: small watcher (client-go) reporting crash loops / OOMKills resolved to image tags. Runs in-cluster later; locally against `~/.kube/config` now. |
| Agent interface | `wt` CLI + a Claude Code skill (in claude-plugins) teaching the claim → work → report → complete loop |
| Views | CLI queries + read-only server-rendered web pages |
| Local dev | docker-compose.yml runs the server; k8s deployment comes later |

Key architectural consequence: the server is the **single writer**, so it is
also the lock manager. Claims, lease renewals, and state transitions are
serialized SQLite transactions — no distributed locking.

"Where is this task in the pipeline" is **derived, not stored**: task → PR →
artifact → deployment → runtime events is an edge walk. The task state machine
itself stays small.

## Components

One Go module, one binary `wt`, subcommands:

- `wt serve` — HTTP API, webhook receivers, read-only web UI.
- `wt migrate` — apply golang-migrate migrations (also applied automatically on `serve` startup).
- `wt watch` — cluster watcher; posts runtime events to the server API. `--kubeconfig` for local use, in-cluster config when deployed.
- `wt <noun> <verb>` — CLI client commands (see CLI section). Config from `~/.config/wt/config.toml` (server URL, token) overridable by `WT_SERVER` / `WT_TOKEN`.

## Data model

Every ingested fact lands in `events` first; the same transaction updates the
typed tables. Every state change records the event that caused it. This gives
provenance and per-task timelines for free; full replay is possible later but
not built in v1.

### Tables (v1 migrations)

- `events` — id, source (`github`|`flux`|`watcher`|`cli`|`system`), external_id (delivery id; unique per source for idempotency), type, payload (JSON), received_at.
- `actors` — id (slug), kind (`human`|`agent`|`service`), display_name.
- `tokens` — token_hash, actor_id, description, created_at, expires_at, revoked_at. Bearer auth for API; webhooks use HMAC instead.
- `projects` — id (slug), name. `project_repos` — project_id, repo (`owner/name`); a project can span repos, a repo maps to exactly one project.
- `tasks` — id (`WT-<n>`, global sequence), project_id, title, body (markdown), priority (`critical`|`high`|`medium`|`low`), kind (`feature`|`bug`|`chore`|`spec`), state, created_by, created_at, updated_at.
  - State machine: `draft → ready → in_progress → in_review → done`, plus `abandoned` (terminal). "Blocked" is derived from open `blocks` edges, not a state. `wt task add` and inbox promotion create tasks in `ready`; `--draft` creates in `draft` (not claimable).
- `task_edges` — from_task, to_task, type (`child_of`|`blocks`), created_at. (`A blocks B`; `A child_of B` makes B an epic.)
- `leases` — task_id (unique among active), actor_id, session_id, acquired_at, renewed_at, expires_at, released_at. Claim = one transaction: verify no active lease, insert lease, transition task to `in_progress`. Default TTL 2h, renewable. A sweeper expires stale leases: task reverts to `ready`, expiry recorded as an event.
- `issues` (inbox) — repo, number, title, state (github state), triage_state (`new`|`promoted`|`dismissed`), task_id (when promoted), applies_to_versions (JSON, set at triage), url.
- `pull_requests` — repo, number, title, state (`open`|`merged`|`closed`), task_id, head_ref, head_sha, merge_sha, url, opened_at, merged_at. Correlated to a task by branch name `wt/<task-id>-<slug>` or a `WT-Task: <id>` line in the PR body.
- `ci_runs` — repo, head_sha, workflow, status, conclusion, url, started_at, completed_at.
- `reviews` — repo, pr number, reviewer, state (`approved`|`changes_requested`|`commented`), submitted_at.
- `artifacts` — id, kind (`docker_image`|`pypi`|`git_tag`|`binary`), name, version, digest, repo, source_sha, built_at. Correlated to PRs via source_sha.
- `deployments` — id, artifact_id, environment (`dev`|`prod`|…), target_kind (`flux_kustomization`|`pypi`|`manual`), target_name, status (`pending`|`reconciling`|`deployed`|`failed`), first_seen, last_update.
- `runtime_events` — id, cluster, kind (`crashloop`|`oom`|`flux_failure`|`flux_recovery`), workload, image, artifact_id (nullable, resolved by image), message, occurred_at.
- `state_log` — entity_kind, entity_id, change (JSON: field, old, new), event_id, at. Generic audit of typed-table changes.

## Ingestion

- **GitHub** (`POST /hooks/github`): HMAC-verified App webhooks. Handlers: issues → inbox; pull_request → `pull_requests` + task correlation; pull_request_review → `reviews`; workflow_run / check_suite → `ci_runs`; release / registry_package → `artifacts`. Task auto-transitions: PR opened → `in_review`; PR merged → stays `in_review` until `wt task done` or deployment-verified (v1: merged PR moves task to `done` unless the task's project opts into deploy-gating; opt-in flag on project).
- **Flux** (`POST /hooks/flux`): HMAC-verified notification-controller alerts. Kustomization/HelmRelease reconcile events → `deployments` status; failures also recorded as `runtime_events` (`flux_failure`).
- **Watcher** (`wt watch`): watches pods across namespaces; on CrashLoopBackOff / OOMKilled, resolves the owning workload's image, posts to `POST /api/v1/runtime-events` with bearer token.
- Idempotency: every webhook delivery id / watcher event key is unique per source in `events`; replays are no-ops.

## API (v1, JSON, `/api/v1`)

- Tasks: create, get, list (filters: project, state, priority, assignee), update, `claim`, `renew`, `release`, `done`, `abandon`; `GET /tasks/{id}/timeline` (events + state log + linked PRs/artifacts/deployments/runtime events).
- Edges: add/remove `blocks` / `child_of`.
- Inbox: list, promote (creates task, sets applies_to_versions), dismiss.
- Projects, actors, tokens: minimal CRUD (admin token).
- Runtime events: create (watcher).
- Health: `/healthz`; metrics: `/metrics` (Prometheus).

## CLI

`wt task add|list|show|claim|renew|release|done|abandon|block|unblock`,
`wt inbox list|promote|dismiss`, `wt project add|list`,
`wt timeline <task>`, `wt board [project]` (org/project overview),
`wt actor add`, `wt token create|revoke`.

`wt task claim` prints the branch name (`wt/<id>-<slug>`) so agents and humans
correlate PRs automatically.

## Web view (read-only)

Server-rendered (`html/template`), no JS build step:
- `/` — org board: in-flight work grouped by project, lease holders, blocked tasks, recent failures.
- `/tasks/{id}` — task page with full timeline.
- `/projects/{id}` — project board.

## Auth

- CLI/watcher → server: bearer tokens (`tokens` table, hash stored). Bootstrap: `wt token create` locally against the DB, or `WT_BOOTSTRAP_TOKEN` env on first run.
- GitHub webhooks: App webhook secret (HMAC SHA-256).
- Flux webhooks: notification-controller HMAC.

## Local dev / deployment

- `docker-compose.yml`: `tracker` service (multi-stage Dockerfile, CGO-free build using `modernc.org/sqlite`), volume-mounted `/data` for the DB, port 8080. Litestream as an optional compose profile.
- `wt watch --kubeconfig ~/.kube/config` runs locally against any cluster now; in-cluster Deployment + flux manifests come later.
- Webhooks during local testing: `gh webhook forward` or smee.io relay (documented in README, not part of the system).

## Migration from TASKS.md

`wt import horndb-tasks` (one-off command, can live outside v1 core): parse
TASKS.md + `gh issue list` → projects/tasks/edges with provenance events.
HornDB is the first onboarded project; the model is org-wide from the start.

## Non-goals (v1)

- Bidirectional GitHub issue sync; interactive web editing; HornDB/RDF
  projection (possible later from typed tables); event replay machinery;
  multi-writer/HA server; notifications (Slack/email).

## Testing

- Unit tests per package; store tests run against real SQLite (temp file, migrations applied).
- Ingestion handlers tested with recorded webhook fixtures (JSON files).
- Lease semantics: concurrency test — N goroutines race to claim one task, exactly one wins; expiry sweeper test with injected clock.
- End-to-end smoke: compose up, seed, drive CLI against the API.
