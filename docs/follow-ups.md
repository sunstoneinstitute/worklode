# v1 follow-ups

Non-blocking items from the v1 review rounds. Import these as tracker tasks
once an instance is running (dogfooding); until then this file is the list.

- **`closedStates` must become per-repo (2026-08-13 ruling, behaviour change).**
  `internal/store/tasks.go:667` is a constant SQL tuple — `('merged',
  'deployed_dev', 'deployed_prod', 'released', 'abandoned')` — mirrored by
  `closedStateSet` and pinned by `TestClosedStateSetMirrorsSQL`. The spec now
  says a task stops blocking at or past **its repo mapping's `done_state`**
  (folded 004 §1.2), so a `merged` task in a repo gating on `released` should
  still block its dependents and today does not. The constant is interpolated
  into four queries — `tasks.go` (`IsBlocked`), `hierarchy.go`,
  `project_work.go`, `brief.go` — each of which needs a join through the repo
  mapping. Keep the one state-fixed case: a task with children cannot advance
  past `merged`, so it is closed there in every repo. Spec and code disagree
  until this lands.
- **The doc-sync config shape contradicts itself (surfaced by the spec fold,
  2026-08-14).** 025 §5/§10 make the git file mirror opt-in through a
  `[doc_sync]` **block** in `.worklode/config.toml`; 025 §16.1 states the config
  reader is a flat `key = "value"` parser with no TOML-table support and
  declares the scalar `spec_corpus` / `plan_corpus` keys instead. Both are
  accepted-or-draft spec text, no document reconciles them, and folding 034
  into 025 put them in one document — which now requires a block it also says
  the parser cannot read. The consolidation deliberately preserved the
  mismatch rather than picking a winner (part-4 ruling 15: choosing is a
  design act, not a transcription), so it needs a spec amendment: either the
  reader grows table support, or the gate becomes the presence of the corpus
  keys. Whoever implements `lode doc sync` hits this first.
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
- **The web UI is unauthenticated without an SSO provider**: `webGuard`
  (`internal/api/authz.go`) serves the anonymous `authOpen` subject when
  neither OIDC nor GitHub login is configured, so the board, project, and task pages
  are readable by anyone who can reach the server — and, since the cockpit's
  creation forms (`internal/api/webform.go`) sit behind the same `webAuth`,
  writable too: such a deployment accepts a task or a deliverable from anyone
  who can reach it, recorded with a NULL `created_by` because there is no
  session to attribute it to. The forms refuse cross-origin submissions, so
  this is reachability, not CSRF. Gating the UI default-deny fixes the read
  and the write exposure together. Spec 021 mirrors that
  bypass in `eitherAuth` for consistency — the blob route is the wrong place
  to unilaterally tighten the auth model — which means task screenshots and
  attachments inherit it too. Spec 021 raises the stakes rather than creating
  them: bodies already carried pre-release design work. Fix at the UI level,
  either by refusing to serve web surfaces with no provider configured or by
  gating the whole UI default-deny. Tracked as spec 021 Q021.4.
- **Authorization is a seam, not a model**: `internal/api/authz.go` puts every
  route behind one default-deny policy table, but what that table can say is
  still the two-level truth the server always had — every authenticated actor,
  plus instance admins. Three things a real model needs are absent. Roles are
  **global**: `Decide` takes the resource it would scope on (`Request.Resource`)
  and ignores it, because project membership is spec 029 §6's Crew and does not
  exist; the day it does, project roles become rows and `Decide` gains a lookup
  rather than a new signature. There is no **ownership** rule — any authenticated
  actor may edit any task, which is deliberate for a small org and wrong for a
  large one. And roles come only from Keycloak's `admin` group at login, so a
  role a person holds in one project cannot be expressed at all. The failure
  direction is safe (unknown permission → deny, open deployment → never admin),
  which is what makes it a scaffold rather than a hazard.
- **Deliverables landed without the rest of spec 029**: the cockpit can now
  declare a deliverable (§3.1's name/description/URL) and read the list back,
  but four pieces of §2/§3 are deliberately absent. Milestones do not exist,
  so a deliverable hangs off its project rather than a milestone — the
  nullable `milestone_id` is a column the milestone table will add. Nothing
  reports deliverable **state** (§3.2's push emitters and poll prober), so the
  page says the state is unreported instead of showing one; that is the
  substantial next slice, and until it lands "is the project published" has no
  answer. Identity **by label** (§3.1's `worklode.deliverable=COW/datasets`)
  is not modelled — only by address. And no CLI verb exists: `lode deliverable
  list/add` would mirror `POST|GET /api/v1/projects/{id}/deliverables`.
- **`project_entity_seq` carries only `DEL`**: spec 029 §4 gives milestones,
  specs, ADRs, and plans their own per-project ordinals from the same counter
  table. Its `kind` CHECK admits `'DEL'` alone, so each of those arrives with a
  one-line CHECK widening, the same way the `tasks.kind` CHECK grows.
- **k8s deployment manifests for the watcher**; RBAC for `lode watch`
  in-cluster. The server's own manifests landed in `deploy/base/`.
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
- **`ns/` changes owed at spec 029's acceptance**: `wl:Milestone` and
  `wl:Deliverable` (subsuming 006's reserved terms), `epic` removed from
  `wlc:TaskKind` (migration + `validKinds` + the test that holds them
  together), and the participants/approvals vocabulary. Spec 029 carries no
  `amends:` frontmatter and the specs2 fold absorbs its "What this spec
  changes elsewhere" table (the spec-target rows live in the part-2 fold
  plan), so this entry is the only record of the `ns/` row.
- **`rdf-registry:ADR-0006` is unresolvable** (spec 025's frontmatter, the corpus's
  only cross-project reference). It predates the `<KEY>-<TYPE>-<n>` shorthand
  (025 §14.3) and no reference form parses the colon syntax. The target is
  `rdf-registry/docs/adr/0006-iri-namespace-scheme.md`. Rewrite it to `<KEY>-ADR-6`
  once rdf-registry is registered as a worklode project and has a key; until then
  026 §4.2 reports it.
- **Adjacent repos are not registered as worklode projects**, so no cross-project
  shorthand resolves. rdf-registry, admin-cluster and provisioning carry
  `docs/adr/NNNN-*.md`; all four (with sunstone-cms) carry
  `docs/superpowers/specs/`. Registering them is what turns 026 §4.2's tier 2 from
  dormant into useful, and it needs 025's `docs` rows first.
- **The `lode` plugin still exists in claude-public-plugins** (`plugins/lode/`,
  published as `lode@sunstone-public`). It was in-sourced here so it versions
  with the binary; removing the public copy is deliberately a separate step,
  once `lode@worklode` is confirmed installing on both Claude Code and Codex.
  Until then two marketplaces publish the same plugin. Removing it also needs
  the `sunstone-public` marketplace entry dropped and anyone still on
  `lode@sunstone-public` migrated.
- **`DefaultLeaseTTL = 2h` may be wrong for human/agent alternation**: the TTL
  was sized for a task an agent executes start to finish, where a missed
  commit-cadence heartbeat really does mean the agent died. A task that
  alternates between agent and human control idles for longer than that
  routinely, and now that delivery transitions no longer close leases the
  sweeper is the main thing ending them — so a too-short TTL is felt more
  often. Deliberately not changed yet: observe which stale-lease cases
  actually show up first, then decide between a longer default, a per-project
  TTL, or a distinction between agent-held and human-held leases.
- **`graphserver` response bodies are never drained before `Close`**: the 200
  path of `Select` stops reading at the JSON decoder's closing brace, and
  `httpError`'s excerpt is capped at 512 bytes, so `net/http` can't reuse
  those connections. Consistent with the rest of the repo (no
  `io.Copy(io.Discard, ...)` anywhere), but the e2e harness pays a fresh TLS
  handshake on each of up to 30 polls.
- **`graphserver.PutGraph` has no compare-and-swap**: it returns only a
  created/replaced bool and discards the `ETag`/`x-sunstone-txn` headers
  graph-server returns, and no method sends `If-Match`. graph-server has
  honoured If-Match compare-and-swap since 2026-07-25
  (`crates/graph-server/src/gsp.rs` `parse_precondition`, 412 on mismatch).
  Needed before a second work-graph writer exists; spec 006 should-have 6.
  Adding it changes `PutGraph`'s signature.
- **Publishing `ns/*.ttl` under `worklode.io/ns/` is unowned** (spec 006
  must-have 3, publishing half): decided 2026-08-06 that this repo serves the
  files from its own site, without rdf-registry (rdf-registry#31 closed).
  `deploy-www.yml` uploads only `www/`, so `ns/` is not served today — the
  deploy must include the ttl files and add `ns/**` to its path trigger. The
  namespace is hash-style (`…/ns/ontology#`), so dereferencing any term
  fetches the extensionless `…/ns/ontology`, which GitHub Pages cannot
  content-negotiate — pick a serving strategy deliberately. Specs 006 §14,
  006 §13.2 item 3, 025 §17 and the `ns/ontology.ttl` header still record the
  rdf-registry approach and need amending.
- **Design-doc sync normalizes frontmatter YAML timestamps to RFC3339**
  (spec 025): `internal/designdoc/corpus.go`'s `frontmatterJSON` re-encodes
  `issued: 2026-01-01` as `"2026-01-01T00:00:00Z"` in the stored JSON —
  deterministic and idempotency-safe, but not byte-faithful to the source
  file. Preserving the original lexical form is deliberately deferred.
- **`app.css` is un-minified**: the Tailwind build ships readable output so the
  contract test can assert readable strings; minify once that test asserts on
  something other than raw CSS text.
- **Cockpit asset filenames are not content-hashed**: `app.css` is served at
  the stable `/assets/app.css` path with a bounded cache lifetime instead of
  an immutable, hash-named file. Add content hashing when cache-busting on
  every deploy starts to matter.
- **`check-generated.sh` misses a brand-new untracked generated file**: it
  uses `git diff --exit-code`, which is silent on a `foo_templ.go` whose
  `foo.templ` was added but whose generated Go was never committed. Harden
  with `git status --porcelain` on the generated paths, or `git add -N`
  before diffing.
- **`scripts/fetch-tailwind.sh` writes the download directly to
  `bin/tailwindcss`** (non-atomic) rather than temp-file + rename; it
  self-heals via the checksum/`-x` gate on the next run, but an atomic write
  would be cleaner.

## From the 2026-08-04 architecture grilling

Design items landed in spec 025. These are the mechanical leftovers.

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
  dependency on the data-platform prod deployment (006 §13.2's only v1 blocker), so
  006/007/025 become testable in `docker compose up` and in e2e.
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
- **The copy of the `lode` plugin in `claude-public-plugins` still documents
  `wt/<id>-<slug>` (spec 008 follow-up).** The in-repo `plugins/lode/` copy was
  updated when spec 008's naming cutover landed, but the duplicate published from the other
  marketplace was not — it needs the same edit, or to be dropped as part of the
  de-duplication already tracked above.
- **`handleWorktreeEnter`/`Create`/`Remove` resolve the layout from the wrong
  cwd (spec 008 follow-up).** `internal/hookrun/hookrun.go` resolves the
  worktree layout once from the payload cwd, then applies it to a
  `tool_input` path that may live in a different repo. `runResume` fixed the
  equivalent hazard by resolving from the target dir; these three hooks did
  not.
- **`applyPush`'s branch-pattern match is looser than a prefix check (spec 008
  follow-up).** `internal/hooks/push.go` short-circuits on a branch-pattern
  shape match, skipping default-branch and `last-deploy/` handling; the
  pattern is now `^KEY-N-anything$` rather than prefix-anchored. Theoretical
  today (a default branch would have to start with an uppercase project key),
  but cheap to tighten to "task exists".
- **`lode show --spec/--adr/<id>` shipped as the cat-mode slice of 026 §3
  only (2026-08-07).** `--resolved` / `--with-drafts` consolidation (026
  §3.1–§3.2) remains unimplemented. `lode doc sync` and `lode doc list` now
  exist (spec 025); `lode doc sections`, `--strict-refs`, and the 026 §2
  planning-status flags (`--needs-planning` / `--needs-execution`) remain
  unimplemented.
- **025 (draft; §18 and elsewhere) still spells the command `lode doc show`;
  026 §3 implements the same command spelled `lode show` (2026-08-07).**
  `docs/plans/2026-08-03-design-doc-queries-2-consolidated-show.md`
  (draft, implements 026 §3) is titled "consolidated `lode doc show`" too.
  Reconcile the spelling when 025's reserved surface is next revisited.
- **Full WCAG 2.2 AA assistive-technology walkthrough** is deferred to the
  four-part project cockpit series' acceptance (spec 032), not Part 1: Part 1
  only proves the structural markers (landmarks, one `aria-current`, focus
  order) automated tests can check.
- **Browser-rendered 1280px/768px/360px regression automation** for the
  cockpit shell, if the CSS contract test (`TestAppCSSContent`) proves
  insufficient to catch a real layout regression at those breakpoints.
- **Part 1's honest-unavailable pages** (`/projects/{id}/crew`,
  `/projects/{id}/reviews`, `/projects/{id}/decisions`,
  `/projects/{id}/documents`, `/projects/{id}/activity`, `/intake`,
  `/reviews`, `/deliveries`, `/knowledge`) are placeholders naming their
  owning spec section; replace each with its real implementation as Parts 2–4
  land. Deliverables has left this list — it is a built destination now.
- **Mode facts stay all-false**: `modeFactsForProject`
  (`internal/api/cockpit.go`) returns an empty `modeFacts` because spec 029's
  intake and promotion stores do not exist, so mode selection cannot see them.
  Spec 032's `PinnedFocus`/`NextDecision` no longer belong here — both are
  built.
- **`worklode_doc_upserts_total` is incremented inside `ApplyDocSync`'s
  transaction (spec 025 follow-up).** `internal/store/docs.go` calls
  `s.metrics.docUpsert(outcome)` per doc before the transaction commits; a
  mid-batch DB failure that rolls the tx back leaves the counter already
  advanced for docs earlier in the batch, even though no rows were written.
  Extreme edge (requires a failure partway through a multi-doc sync). Move the
  increment to after commit when this is next touched.

## From the 2026-08-14 spec-corpus consolidation (part 5)

Deliberately not fixed by the cutover — recorded so a reviewer does not read
them as consolidation-introduced drift.

- **`ns/` mirrors pre-025 vocabulary** (part-4 residual). `ns/concept.ttl`
  still ships `wlc:epic`, and `ns/ontology.ttl`'s `owl:AllDisjointClasses`
  still names `wl:Workstream` and includes `wl:Issue`/`wl:PullRequest`, where
  folded 006 §2's own axiom carries `wl:Project` and neither. CLAUDE.md's rule
  is amend the spec first, then mirror the term; 025 amends it, and the
  mirror follows once 025 ships.
- **Folded 001 §8.3 states a stale infrastructure fact.** The one-time CLI
  login code store is justified as in-memory-safe because "the server is
  single-instance (one PVC + litestream)"; the backbone is Postgres today,
  not a single-instance litestream deployment.
- **Folded 008 relies on an `ExitWorktree` harness event its own event map
  never covers.** §9 defines lease release on `ExitWorktree`/`SessionEnd`,
  but §17.4's per-harness event map (derived from §9's vocabulary) has no
  `ExitWorktree`/`WorktreeExit` row — only `WorktreeEnter` is mapped per
  harness.
- **Folded 006 §13 ships an implementation-status report verified in
  2026-07** ("done in dev", must-have 1 "prod remains blocked on item 1", and
  must-have 3's base-URL override "not yet implemented" in rdf-registry) that
  needs refreshing against current state.
- **Folded 020 uses `epic` as live vocabulary; folded 029 §2 removes it from
  `TaskKind`.** 020 has six sites — §0, §2.1, §3.3's `kind = "epic"` 422
  rejection with `validKinds` and `epicForbiddenStates`, and two rows of §4's
  table. 020's transcription was faithful to its pre-fold source and 029's own
  change table never named 020, so the gap is 029's. Fixing it is a spec-plus-
  code change: the enum is held together by the `tasks.kind` CHECK constraint,
  `validKinds`, and `wlc:TaskKind`, which a test keeps in agreement.
- **Folded 026 §4 documents a frontmatter form the corpus no longer uses.**
  Its parenthetical-annotation rule is written against `wasDerivedFrom`, and
  the fold left frontmatter carrying only `status`, `issued` and `requires`.
  Same class as the three acceptance criteria ruling 23 retired: the machinery
  stays correct for a document written after the cutover, but nothing exercises
  it today.
