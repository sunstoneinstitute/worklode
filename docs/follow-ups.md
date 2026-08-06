# v1 follow-ups

Non-blocking items from the v1 review rounds. Import these as tracker tasks
once an instance is running (dogfooding); until then this file is the list.

- **Artifact correlation hardening.** Ingest `registry_package` webhooks so
  `docker_image` artifacts exist; resolve `release.target_commitish` branch
  names to commit SHAs; normalize OCI digests (`sha256:`) in flux
  `revisionSHA`. Today only `git_tag` artifacts are created, so the
  flux-revision → artifact → task chain rarely connects.
- **`assignee` filter does not see leases**: `GET /api/v1/tasks?assignee=` matches
  `tasks.assignee` only, so a task an agent claimed (lease holder, no assignee) is
  invisible to it. Joining active leases would make "everything X is working on"
  one query instead of two.
- **`lode task list --mine`**: blocked on there being no caller identity in the
  CLI — no `whoami` route, no `Client.WhoAmI`, and `cli.Config` stores no actor
  id (`lode login` prints `res.ActorID` and discards it). Persisting the actor id
  at login, or adding the route, unblocks it; until then `--assignee <actor>` is
  the explicit form.
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
- **The web UI is unauthenticated without an SSO provider**: `webAuth`
  (`internal/api/oidcweb.go:57`) passes every request through when neither
  OIDC nor GitHub login is configured, so the board, project, and task pages
  are readable by anyone who can reach the server. Spec 021 mirrors that
  bypass in `eitherAuth` for consistency — the blob route is the wrong place
  to unilaterally tighten the auth model — which means task screenshots and
  attachments inherit it too. Spec 021 raises the stakes rather than creating
  them: bodies already carried pre-release design work. Fix at the UI level,
  either by refusing to serve web surfaces with no provider configured or by
  gating the whole UI default-deny. Tracked as spec 021 Q021.4.
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
- **`internal/api` metrics shape is the outlier** (spec 022): store, hooks, and
  embed each own a nil-safe package-private metrics struct; api hangs its
  instruments off `*server` and the HTTP middleware is not nil-safe. Extract an
  `apiMetrics` struct, or document the exception in spec 022 §1.
- **`worklode_lease_sweeper_runs_total` has no test**: the sweeper loop is
  built inline in `serve.go`'s `RunE` closure; extract it to a testable
  function to cover the counter.
- **Alert on `promhttp_metric_handler_errors_total`**: the /metrics handler
  serves partial output on collector failure (`ContinueOnError`); this counter
  is the only signal a collector is broken. Dashboards/alerts are out of scope
  for spec 022, so wire it when those land.
- **Provisioning doc fix** (other repo): `provisioning/context/metrics.md`
  shows bare `prometheus.io/*` annotations and a ServiceMonitor example without
  saying the annotations must sit on the *Service* (the hzdev collector's
  `k8s-service-endpoints` job) and that no Prometheus operator exists — exactly
  the trap worklode fell into before spec 022 §9.
- **`wl:taskState` duplicates the `tasks.state` enum** in `ns/shapes.ttl`
  (`sh:in`), so widening the `CHECK` in a migration means widening that shape.
  The transitions are not duplicated — they stay in `internal/store/tasks.go`.
  Worth a check in CI if the graph ever ships.
- **`rdf-registry:ADR-0006` is unresolvable** (spec 014's frontmatter, the corpus's
  only cross-project reference). It predates the `<KEY>-<TYPE>-<n>` shorthand
  (014 §11.3) and no reference form parses the colon syntax. The target is
  `rdf-registry/docs/adr/0006-iri-namespace-scheme.md`. Rewrite it to `<KEY>-ADR-6`
  once rdf-registry is registered as a worklode project and has a key; until then
  026 §4.2 reports it.
- **Adjacent repos are not registered as worklode projects**, so no cross-project
  shorthand resolves. rdf-registry, admin-cluster and provisioning carry
  `docs/adr/NNNN-*.md`; all four (with sunstone-cms) carry
  `docs/superpowers/specs/`. Registering them is what turns 026 §4.2's tier 2 from
  dormant into useful, and it needs 025's `docs` rows first.

## From the 2026-08-04 architecture grilling

Design items landed in spec 028. These are the mechanical leftovers.

- **Multi-operator: `leases_active_worktree` is `UNIQUE (worktree)`** on a bare
  path string (0001_baseline). Two operators whose worktrees resolve to the same
  absolute path — devcontainers, shared layout conventions — collide across
  machines, and the error reads as "someone holds this lease" for a task nobody
  claimed. Change to `UNIQUE (actor_id, worktree)`. The only item in this list
  that corrupts state rather than annoying someone.
- **Bare `claim --next` spans every project.** With one operator that is a
  feature; with several, an overnight loop silently drains someone else's focused
  project. Default `--project` from the repo's `.worklode` config (019 already
  resolves `current_project`) and require `--all-projects` to span.
- **`claim --next --concern <c>`** as a caller-side soft preference, ranking
  above `concern_rank` and below `is_critical`. `project.focus` is team-wide
  state, so today the only way for one operator to steer their own queue is to
  change everyone's ranking.
- **Compose gets its own graph-server**, sharing the Postgres instance on a
  separate database. Requires vendoring graph-server's migrations. Severs the
  dependency on the data-platform prod deployment (009 §1's only v1 blocker), so
  006/007/014/015 become testable in `docker compose up` and in e2e.
- **Review API before review UI.** Embedding crit in the worklode web UI is the
  plan; agents review too (Claude reviews Codex's work and vice versa), so
  comment/thread/resolve/approve must exist as API + `lode doc` verbs with the
  web UI as one client. Building web handlers first produces a human-only review
  system inside an agent-first product.
- **Review tasks take an assignee.** Choosing a reviewer stays social, but "I
  asked Kim to review it" has to be visible — routing, not authority.
- **Capture the initiating prompt** on a document's creation event. Specs are
  generated from a brainstorming or grilling prompt; the prompt is ~200 tokens
  and answers "why does this exist" long after the session is gone. Not the
  transcript.
- **One door for authoring.** A worklode skill owns the terminal write step and
  delegates the interview to `superpowers:brainstorming` / `grill-with-docs`;
  `/lode:spec` is the entry point; a pre-commit script rejects *new* files under
  `docs/specs/` and `docs/plans/` once the store is authoritative; `lode doc
  export` regenerates the tree as a read-only artifact so git history and grep
  survive. Customising or hook-patching the brainstorming skill is the wrong
  lever — it forks a skill we do not own, or makes behaviour depend on invisible
  mutation.
- **CLAUDE.md says the web UI is unauthenticated.** `internal/api/oidcweb.go`
  gates the web pages behind Keycloak when OIDC is enabled. Fix the sentence.
