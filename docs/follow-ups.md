# v1 follow-ups

Non-blocking items from the v1 review rounds. Import these as tracker tasks
once an instance is running (dogfooding); until then this file is the list.

- **Artifact correlation hardening.** Ingest `registry_package` webhooks so
  `docker_image` artifacts exist; resolve `release.target_commitish` branch
  names to commit SHAs; normalize OCI digests (`sha256:`) in flux
  `revisionSHA`. Today only `git_tag` artifacts are created, so the
  flux-revision → artifact → task chain rarely connects.
- **`assignee` filter** on `GET /api/v1/tasks` (join active leases).
- **PR closed without merge**: release the lease and surface the task on the
  board (today it stays `in_review`; `lode task rework` is the manual path).
- **Bulk inbox dismiss**: `lode inbox dismiss` takes one issue at a time, which
  does not scale to `lode inbox import --state all` on a mature repo — spec 020
  keeps the import default narrow for this reason.
- **Triage lost-update window**: `PromoteIssue`, `DismissIssue`, and `LinkIssue`
  each `SELECT triage_state` and then `UPDATE` keyed only on `(repo, number)`,
  under READ COMMITTED. Two concurrent triage calls can both read `new` and both
  write, so one outcome is silently lost — and promote's loser orphans the task
  it created. Add `AND triage_state = 'new'` plus a `RowsAffected` check to all
  three. Low impact while triage is human-driven and one issue at a time.
- **e2e coverage for the inbox verbs**: `lode inbox import` and `lode inbox link`
  are tested per layer but never through CLI → API → store. `link` is the cheap
  one to add (no fake GitHub needed) and covers the seam the unit tests split.
- **Split the inbox handlers out of `internal/api/admin.go`**: the file is 757
  lines across projects, actors/tokens, inbox, and the board. The inbox section
  alone is ~233 lines, and `internal/api/inbox_import.go` already exists as the
  sibling to move them next to.
- **Cost is lost when a session never ends cleanly**: usage is reported on
  `session-end` and `worktree-exit`. A crashed agent, or a lease swept by
  `ExpireLeases`, closes the session with `ended_at` but no usage, so its spend
  never lands. Reporting on the debounced heartbeat as well would close the gap
  — the write is already replace-not-accumulate, so a mid-session report is
  safe to repeat.
- **Unmodelled billing dimensions**: `service_tier` (batch is half price) and
  server-side tool use (`web_search_requests`, billed per request) are both
  present in the transcript's `usage` block and both ignored. Claude Code runs
  the standard tier, so the first is theoretical; the second understates a
  search-heavy session by a small, real amount.
- **No admin surface for `model_prices`**: rates are seeded by migration `0008`
  and editable only by SQL, via `store.UpsertModelPrice`. A `lode admin price`
  verb plus an endpoint would make a mid-quarter rate change routine. Also note
  fast-mode rates for Opus 4.8 are assumed to mirror Opus 5's 2x — unverified.
- **Cache-write TTL assumption**: a transcript entry with
  `cache_creation_input_tokens` but no `cache_creation` breakdown is attributed
  entirely to the 5-minute TTL (the vendor default), which underprices a 1-hour
  cache by 37.5%. Every current Claude Code version emits the breakdown, so
  this only bites on old transcripts.
- **k8s deployment manifests** (flux) for the server and the watcher; RBAC
  for `lode watch` in-cluster.
- **Claude Code skill** in the claude-plugins repo teaching the
  claim → work → report → complete loop.
- **Watcher test timing**: `TestBelowRestartThresholdNotReported` uses a 5s
  `eventually` timeout and flaked once under heavy host load; bump the fence
  timeout or make it load-tolerant.
- **`LogChange` clock**: `state_log.at` uses the wall clock, not the store's
  injectable `nowFn`; plumb `now` through for fully deterministic timelines.
- **Shared HTTP helpers**: `writeJSON`/`writeErr` are duplicated between
  `internal/api` and `internal/hooks`; consolidate if a third copy appears.
- **Notifications** (Slack/email) and the HornDB/RDF projection remain
  deliberate non-goals until the tracker has real usage.
- **`wl:taskState` duplicates the `tasks.state` enum** in `ns/shapes.ttl`
  (`sh:in`), so widening the `CHECK` in a migration means widening that shape.
  The transitions are not duplicated — they stay in `internal/store/tasks.go`.
  Worth a check in CI if the graph ever ships.
