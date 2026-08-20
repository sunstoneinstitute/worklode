# v1 follow-ups

Non-blocking items from the v1 review rounds. Import these as tracker tasks
once an instance is running (dogfooding); until then this file is the list.

That migration has started: **file new follow-ups as tracker tasks**
(`lode task add --follow-up-to <the task that surfaced it>`), not here. The
task-secrets items went first (WL-102, WL-103, WL-104). What remains below is
the backlog still to be imported.

Each item carries a priority tag (assessed 2026-08-14):

- `[P0]` exposure or silent wrongness — schedule now
- `[P1]` cheap correctness fixes, small diffs
- `[P2]` dogfooding friction
- `[P3]` enablers that unblock other work
- `[P4]` low-risk chores and doc/spec hygiene — batch when convenient
- `[gated]` waiting on another decision, spec, or condition — don't schedule

**A gap a plan's `covers:` already declares does not belong here.** A
`coverage: partial` claim (026 §5, `splitting-specs-into-plans`) states the
gap in a form a coverage query reads; restating it here creates a second copy
in a file nothing queries, and the two drift. This file is for what coverage
cannot express: a conflict between spec and system, a fact that has gone
stale, a defect nobody has planned against. Before adding an entry, check
whether some plan's `partial` already says it — and prefer removing an entry
outright once it is fixed over annotating it as resolved.

- `[P4]` **The doc-sync config shape contradicts itself (surfaced by the spec fold,
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
  keys. `lode doc sync` has since been retired unshipped (`f11af04`,
  2026-08-17), withdrawing 025 §16 and §5.1 with it, so nothing reads a
  `[doc_sync]` block at all; WL-147 then re-pointed `lode show --spec/--adr`
  at the backbone and deleted the scalar keys, their reader
  (`internal/cli.CorporaFrom`) and their entry in `.worklode/config.toml`.
  Neither shape survives in code. The contradiction is now confined to 025
  §5/§10's text, and the corpus cutover (025 §12) removes its subject rather
  than reconciling it.
- `[P2]` **Per-artifact delivery tracking** (004 §5.3, already deferred there): a
  repo shipping two images plus a CLI binary has one `done_state`. The
  `registry_package` handler mints every image as an artifact, but delivery is
  still decided per repo, so the second image's deploy tells the tracker
  nothing the first one didn't.
- `[P3]` **Spec 006 §11.1 still says image ingest does not exist.** It reads
  "Nothing creates `docker_image`, `pypi` or `binary` rows" and calls
  `deployments.artifact_id` null in practice, but `applyRegistryPackage`
  (`internal/hooks/github.go`) mints `docker_image` artifacts from
  `registry_package` webhooks and `FindArtifactByImage` resolves them. §15
  question 11 ("the artifact ingest gap is the real blocker") is therefore
  narrower than it reads — what remains is the App permission item below, not
  the absence of a handler. Needs an amendment to 006 §11.1 and §15 item 11.
  Noticed while executing the runtime-layer plan (WL-27), whose projection is
  correct either way: a nil artifact emits no `prov:used`.
- `[P4]` **`registry_package` ignores non-container packages**: PyPI/npm
  versions are recorded as events and dropped. The `pypi` artifact kind exists
  in the `artifacts.kind` CHECK, nothing writes it, and nothing reads it —
  wiring one end alone buys nothing.
- `[P4]` **`target_commitish` on a package version is stored verbatim**: a
  branch name there stays uncorrelated. Unlike a release it has no frontier to
  fall back on, so it would need the release path's GitHub App resolver. Worth
  doing only if real container deliveries show a branch arriving there.
- `[P2]` **A re-pushed mutable tag orphans the old digest**: `CreateArtifact`
  upserts on `(kind, name, version)`, so re-pushing `:latest` (or any moving
  tag) overwrites `digest`, and the previous digest stops resolving through
  `ArtifactByDigest`. A Flux event for the still-running old image then cannot
  correlate. Inherent to keying an image artifact on `(name, tag)`; fixing it
  means keying on the digest and treating the tag as a pointer.
- `[P1]` **The GitHub App needs `Packages: read` and a `registry_package`
  subscription** (operator action, not a code change): `registry_package` is in
  `handledEvents`, so `lode project add-repo` warns "github app is not
  subscribed to: registry_package" on every existing install until the App
  gains the permission — and a GitHub App permission change requires each
  installation to approve it. Until then no `docker_image` artifacts are minted
  and the Flux OCI correlation has nothing to resolve against.
- `[P2]` **One `tasks.state` cannot express per-repo delivery.** `taskClosed`
  (004 §1.3) asks one scalar state to satisfy every repo the task landed in,
  but `ResolveDelivery` is repo-scoped — it advances on the frontiers of the
  one repo whose webhook fired. So a task that landed in two repos with the
  same `done_state = deployed_prod` reads as delivered for both the moment
  either one deploys, and with differing `done_state`s the demand can be
  unsatisfiable in the order the facts arrive. The predicate under-blocks in
  exactly the cases the peer-ranked terminals cover (see `deliveryRanks`),
  which is where the old fixed tuple already sat, so nothing regressed — but
  "delivered" is per (task, repo) and the schema stores it per task. Modelling
  it properly is a delivery-state-per-repo table, not a predicate change.
- `[P4]` **`deliveredStateSet` stayed state-only when the blocking predicate went
  per-repo.** `internal/store/tasks.go`'s `taskClosed` now joins through the
  repo mapping (004 §1.3), and the roll-up, progress counts and blocking
  queries all read it. `AssignTask`/`UnassignTask`/`StartTask` (`assign.go`)
  still ask the fixed `deliveredStateSet`, so a `merged` task in a repo gating
  on `deployed_prod` — the shape discovery defaults any repo with a prod
  environment to — blocks its dependents while refusing to be assigned or
  started. Not a regression (those three read the same fixed set before), and
  arguably right: they ask "is there work left to own", not "does this still
  block". Revisit if the two readings visibly diverge in use.
- `[P2]` **`assignee` filter does not see leases**: `GET /api/v1/tasks?assignee=` matches
  `tasks.assignee` only, so a task an agent claimed (lease holder, no assignee) is
  invisible to it. Joining active leases would make "everything X is working on"
  one query instead of two.
- `[P2]` **`lode task list --mine`**: blocked on there being no caller identity in the
  CLI — no `whoami` route, no `Client.WhoAmI`, and `cli.Config` stores no actor
  id (`lode login` prints `res.ActorID` and discards it). Persisting the actor id
  at login, or adding the route, unblocks it; until then `--assignee <actor>` is
  the explicit form.
- `[P2]` **PR closed without merge**: release the lease and surface the task on the
  board (today it stays `in_review`; `lode task rework` is the manual path).
- `[P2]` **Bulk inbox dismiss**: `lode inbox dismiss` takes one issue at a time, which
  does not scale to `lode inbox import --state all` on a mature repo — spec 020
  keeps the import default narrow for this reason.
- `[P4]` **e2e coverage for the inbox verbs**: `lode inbox import` and `lode inbox link`
  are tested per layer but never through CLI → API → store. `link` is the cheap
  one to add (no fake GitHub needed) and covers the seam the unit tests split.
- `[P4]` **Split the inbox handlers out of `internal/api/admin.go`**: the file is 977
  lines across projects, actors/tokens, inbox, and the board. The inbox section
  alone is ~233 lines, and `internal/api/inbox_import.go` already exists as the
  sibling to move them next to.
- `[P4]` **Unmodelled billing dimensions**: `service_tier` (batch is half price) and
  server-side tool use (`web_search_requests`, billed per request) are both
  present in the transcript's `usage` block and both ignored. Claude Code runs
  the standard tier, so the first is theoretical; the second understates a
  search-heavy session by a small, real amount.
- `[P4]` **No admin surface for `model_prices`**: rates are seeded by migration `0008`
  and editable only by SQL, via `store.UpsertModelPrice`. A `lode admin price`
  verb plus an endpoint would make a mid-quarter rate change routine. Also note
  fast-mode rates for Opus 4.8 are assumed to mirror Opus 5's 2x — unverified,
  and worth checking ahead of the verb, because a wrong rate misprices every
  session recorded against it.
- `[P4]` **Cache-write TTL assumption**: a transcript entry with
  `cache_creation_input_tokens` but no `cache_creation` breakdown is attributed
  entirely to the 5-minute TTL (the vendor default), which underprices a 1-hour
  cache by 37.5%. Every current Claude Code version emits the breakdown, so
  this only bites on old transcripts.
- `[P3]` **Follow-up edges shipped without four adjacent pieces (spec 004 §1.3
  follow-up, 2026-08-14).** The `lode` plugin (`plugins/claude/lode/`, the
  `lode-worker` agent and `/lode:*` commands) does not yet know to reach for
  `--follow-up-to`, so an agent that spots a loose end mid-task still has to
  file it by hand instead of the edge doing its job. `docs/follow-ups.md`
  itself is not migrated into tasks carrying `follow_up_to` edges — a data
  question, not a code one. No board or cockpit surface shows follow-ups
  (a spec 032 question, not asked here). And `wl:followUpTo` now has its
  triple in the projection mapping (`internal/graphproj`, WL-25) but reaches
  the graph only when the part-2 projector ships (WL-26). Recorded in
  `docs/plans/2026-08-14-follow-up-edges.md`'s "Follow-ups this plan
  deliberately does not close".
- `[gated]` **Authorization is a seam, not a model**: `internal/api/authz.go` puts every
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
- `[gated]` **Deliverables landed without the rest of spec 029**: the cockpit can now
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
- `[gated]` **`project_entity_seq` carries only `DEL`**: spec 029 §4 gives milestones,
  specs, ADRs, and plans their own per-project ordinals from the same counter
  table. Its `kind` CHECK admits `'DEL'` alone, so each of those arrives with a
  one-line CHECK widening, the same way the `tasks.kind` CHECK grows.
- `[P3]` **k8s deployment manifests for the watcher**; RBAC for `lode watch`
  in-cluster. The server's own manifests landed in `deploy/base/`.
- `[P4]` **Watcher test timing**: `TestBelowRestartThresholdNotReported` uses a 5s
  `eventually` timeout and flaked once under heavy host load; bump the fence
  timeout or make it load-tolerant.
- `[P4]` **`LogChange` clock**: `state_log.at` uses the wall clock, not the store's
  injectable `nowFn`; plumb `now` through for fully deterministic timelines.
- `[gated]` **Shared HTTP helpers**: `writeJSON`/`writeErr` are duplicated between
  `internal/api` and `internal/hooks`; consolidate if a third copy appears.
- `[gated]` **Notifications** (Slack/email) remain a deliberate non-goal until the
  tracker has real usage.
- `[P4]` **`internal/api` metrics shape is the outlier** (spec 022): store, hooks, and
  embed each own a nil-safe package-private metrics struct; api hangs its
  instruments off `*server` and the HTTP middleware is not nil-safe. Extract an
  `apiMetrics` struct, or document the exception in spec 022 §1.
- `[P4]` **`worklode_lease_sweeper_runs_total` has no test**: the sweeper loop is
  built inline in `serve.go`'s `RunE` closure; extract it to a testable
  function to cover the counter.
- `[gated]` **Alert on `promhttp_metric_handler_errors_total`**: the /metrics handler
  serves partial output on collector failure (`ContinueOnError`); this counter
  is the only signal a collector is broken. Dashboards/alerts are out of scope
  for spec 022, so wire it when those land.
- `[P1]` **Provisioning doc fix** (other repo): `provisioning/context/metrics.md`
  shows bare `prometheus.io/*` annotations and a ServiceMonitor example without
  saying the annotations must sit on the *Service* (the hzdev collector's
  `k8s-service-endpoints` job) and that no Prometheus operator exists — exactly
  the trap worklode fell into before spec 022 §9.
- `[gated]` **Two shapes still duplicate a backbone enum** in `ns/shapes.ttl`
  (`sh:in`): `wl:priority` mirrors the `tasks.priority` CHECK and `wl:concern`
  the `tasks.concern` CHECK, so widening either CHECK in a migration means
  widening the matching shape by hand. `wl:taskState` was the third and is now
  pinned by `TestTaskStateShapeMatchesStateMachine` (`internal/store`). The
  transitions themselves are still not duplicated into RDF — they stay in
  `internal/store/tasks.go`.
- `[gated]` **`ns/` changes still owed at spec 029's acceptance**: `wl:Milestone`
  (subsuming 006's reserved term) and the participants/approvals vocabulary.
  Two halves are **done** — the task kinds (the `0017` CHECK, `validKinds`, and
  `wlc:TaskKind`, held together by `TestTaskKindsAgreeAcrossSources`) and
  `wl:Deliverable`, now a class in `ns/ontology.ttl`.
- `[gated]` **Adjacent repos are not registered as worklode projects**, so no cross-project
  shorthand resolves. rdf-registry, admin-cluster and provisioning carry
  `docs/adr/NNNN-*.md`; all four (with sunstone-cms) carry
  `docs/superpowers/specs/`. Registering them is what turns 026 §4.2's tier 2 from
  dormant into useful, and it needs 025's `docs` rows first.
- `[P3]` **The `lode` plugin still exists in claude-public-plugins** (`plugins/claude/lode/`,
  published as `lode@sunstone-public`). It was in-sourced here so it versions
  with the binary; removing the public copy is deliberately a separate step,
  once `lode@worklode` is confirmed installing on both Claude Code and Codex.
  Until then two marketplaces publish the same plugin. Removing it also needs
  the `sunstone-public` marketplace entry dropped and anyone still on
  `lode@sunstone-public` migrated.
- `[gated]` **`DefaultLeaseTTL = 2h` may be wrong for human/agent alternation**: the TTL
  was sized for a task an agent executes start to finish, where a missed
  commit-cadence heartbeat really does mean the agent died. A task that
  alternates between agent and human control idles for longer than that
  routinely, and now that delivery transitions no longer close leases the
  sweeper is the main thing ending them — so a too-short TTL is felt more
  often. Deliberately not changed yet: observe which stale-lease cases
  actually show up first, then decide between a longer default, a per-project
  TTL, or a distinction between agent-held and human-held leases.
- `[P4]` **`graphserver` response bodies are never drained before `Close`**: the 200
  path of `Select` stops reading at the JSON decoder's closing brace, and
  `httpError`'s excerpt is capped at 512 bytes, so `net/http` can't reuse
  those connections. Consistent with the rest of the repo (no
  `io.Copy(io.Discard, ...)` anywhere), but the e2e harness pays a fresh TLS
  handshake on each of up to 30 polls.
- `[gated]` **`graphserver.PutGraph` has no compare-and-swap**: it returns only a
  created/replaced bool and discards the `ETag`/`x-sunstone-txn` headers
  graph-server returns, and no method sends `If-Match`. graph-server has
  honoured If-Match compare-and-swap since 2026-07-25
  (`crates/graph-server/src/gsp.rs` `parse_precondition`, 412 on mismatch).
  Needed before a second work-graph writer exists; spec 006 should-have 6.
  Adding it changes `PutGraph`'s signature.
- `[P3]` **Publishing `ns/*.ttl` under `worklode.io/ns/` is unowned** (spec 006
  must-have 3, publishing half): decided 2026-08-06 that this repo serves the
  files from its own site, without rdf-registry (rdf-registry#31 closed).
  `deploy-www.yml` uploads only `www/`, so `ns/` is not served today — the
  deploy must include the ttl files and add `ns/**` to its path trigger. The
  namespace is hash-style (`…/ns/ontology#`), so dereferencing any term
  fetches the extensionless `…/ns/ontology`, which GitHub Pages cannot
  content-negotiate — pick a serving strategy deliberately. Specs 006 §14,
  006 §13.2 item 3, 025 §17 and the `ns/ontology.ttl` header still record the
  rdf-registry approach and need amending.
- `[P4]` **`internal/designdoc/corpus.go`'s sync loader is dead code.**
  `LoadSyncCorpus`, `CorpusDoc`, `SectionMeta`, `EdgeMeta` and
  `frontmatterJSON` served the retired git→backbone sync (`f11af04`) and now
  have no caller outside `corpus_test.go`; `lode doc import` walks the corpus
  itself. Deleting them also retires the RFC3339-timestamp caveat this entry
  used to record, since nothing re-encodes frontmatter as JSON any more.
  `Title` and the unexported helpers stay — the importer uses them.
- `[gated]` **`app.css` is un-minified**: the Tailwind build ships readable output so the
  contract test can assert readable strings; minify once that test asserts on
  something other than raw CSS text.
- `[gated]` **Cockpit asset filenames are not content-hashed**: `app.css` is served at
  the stable `/assets/app.css` path with a bounded cache lifetime instead of
  an immutable, hash-named file. Add content hashing when cache-busting on
  every deploy starts to matter.
- `[P4]` **`scripts/fetch-tailwind.sh` writes the download directly to
  `bin/tailwindcss`** (non-atomic) rather than temp-file + rename; it
  self-heals via the checksum/`-x` gate on the next run, but an atomic write
  would be cleaner.

## From the 2026-08-04 architecture grilling

Design items landed in spec 025. These are the mechanical leftovers.

- `[P2]` **`claim --next --concern <c>`** as a caller-side soft preference, ranking
  above `concern_rank` and below `is_critical`. `project.focus` is team-wide
  state, so today the only way for one operator to steer their own queue is to
  change everyone's ranking.
- `[P3]` **Compose gets its own graph-server**, sharing the Postgres instance on a
  separate database. Requires vendoring graph-server's migrations. Severs the
  dependency on the data-platform prod deployment (006 §13.2's only v1 blocker), so
  006/007/025 become testable in `docker compose up` and in e2e.
- `[gated]` **Review API before review UI.** Embedding crit in the worklode web UI is the
  plan; agents review too (Claude reviews Codex's work and vice versa), so
  comment/thread/resolve/approve must exist as API + `lode doc` verbs with the
  web UI as one client. Building web handlers first produces a human-only review
  system inside an agent-first product.
- `[gated]` **Review tasks take an assignee.** Choosing a reviewer stays social, but "I
  asked Kim to review it" has to be visible — routing, not authority.
- `[gated]` **Capture the initiating prompt** on a document's creation event. Specs are
  generated from a brainstorming or grilling prompt; the prompt is ~200 tokens
  and answers "why does this exist" long after the session is gone. Not the
  transcript.
- `[gated]` **One door for authoring.** A worklode skill owns the terminal write step and
  delegates the interview to `superpowers:brainstorming` / `grill-with-docs`;
  `/lode:spec` is the entry point; a pre-commit script rejects *new* files under
  `docs/specs/` and `docs/plans/` once the store is authoritative; `lode doc
  export` regenerates the tree as a read-only artifact so git history and grep
  survive. Customising or hook-patching the brainstorming skill is the wrong
  lever — it forks a skill we do not own, or makes behaviour depend on invisible
  mutation.
- `[P3]` **The copy of the `lode` plugin in `claude-public-plugins` still documents
  `wt/<id>-<slug>` (spec 008 follow-up).** The in-repo `plugins/claude/lode/` copy was
  updated when spec 008's naming cutover landed, but the duplicate published from the other
  marketplace was not — it needs the same edit, or to be dropped as part of the
  de-duplication already tracked above.
- `[P1]` **`handleWorktreeEnter`/`Create`/`Remove` resolve the layout from the wrong
  cwd (spec 008 follow-up).** `internal/hookrun/hookrun.go` resolves the
  worktree layout once from the payload cwd, then applies it to a
  `tool_input` path that may live in a different repo. `runResume` fixed the
  equivalent hazard by resolving from the target dir; these three hooks did
  not.
- `[P1]` **`applyPush`'s branch-pattern match is looser than a prefix check (spec 008
  follow-up).** `internal/hooks/push.go` short-circuits on a branch-pattern
  shape match, skipping default-branch and `last-deploy/` handling; the
  pattern is now `^KEY-N-anything$` rather than prefix-anchored. Theoretical
  today (a default branch would have to start with an uppercase project key),
  but cheap to tighten to "task exists".
- `[gated]` **`lode show --spec/--adr/<id>` shipped as the cat-mode slice of 026 §3
  only (2026-08-07).** `--resolved` / `--with-drafts` consolidation (026
  §3.1–§3.2) remains unimplemented. `lode doc list` now exists (spec 025),
  carrying the 026 §2 planning-status flags (`--needs-planning` /
  `--needs-execution`); `lode doc sync` was retired unshipped (`f11af04`).
  `lode doc sections` and `--strict-refs` remain unimplemented. WL-147 moved
  the resolution source from the file corpus to the backbone, so the command
  now needs a reachable server; the `--json` shape's `path` field became
  `doc` + `slug` with it.
- `[P4]` **025 (draft; §18 and elsewhere) still spells the command `lode doc show`;
  026 §3 implements the same command spelled `lode show` (2026-08-07).**
  `docs/plans/2026-08-03-design-doc-queries-2-consolidated-show.md`
  (draft, implements 026 §3) is titled "consolidated `lode doc show`" too.
  Reconcile the spelling when 025's reserved surface is next revisited.
- `[gated]` **Full WCAG 2.2 AA assistive-technology walkthrough** is deferred to the
  four-part project cockpit series' acceptance (spec 032), not Part 1: Part 1
  only proves the structural markers (landmarks, one `aria-current`, focus
  order) automated tests can check.
- `[gated]` **Browser-rendered 1280px/768px/360px regression automation** for the
  cockpit shell, if the CSS contract test (`TestAppCSSContent`) proves
  insufficient to catch a real layout regression at those breakpoints.
- `[gated]` **Part 1's honest-unavailable pages** (`/projects/{id}/crew`,
  `/projects/{id}/reviews`, `/projects/{id}/decisions`,
  `/projects/{id}/documents`, `/projects/{id}/activity`, `/intake`,
  `/reviews`, `/deliveries`, `/knowledge`) are placeholders naming their
  owning spec section; replace each with its real implementation as Parts 2–4
  land. Deliverables has left this list — it is a built destination now.
- `[gated]` **Mode facts stay all-false**: `modeFactsForProject`
  (`internal/api/cockpit.go`) returns an empty `modeFacts` because spec 029's
  intake and promotion stores do not exist, so mode selection cannot see them.
  Spec 032's `PinnedFocus`/`NextDecision` no longer belong here — both are
  built.

## From the 2026-08-14 spec-corpus consolidation (part 5)

Deliberately not fixed by the cutover — recorded so a reviewer does not read
them as consolidation-introduced drift. All four are `[P4]`, and cheapest as
one pass.

- `[P4]` **Folded 001 §8.3 states a stale infrastructure fact.** The one-time CLI
  login code store is justified as in-memory-safe because "the server is
  single-instance (one PVC + litestream)"; the backbone is Postgres today,
  not a single-instance litestream deployment.
- `[P4]` **Folded 008 relies on an `ExitWorktree` harness event its own event map
  never covers.** §9 defines lease release on `ExitWorktree`/`SessionEnd`,
  but §17.4's per-harness event map (derived from §9's vocabulary) has no
  `ExitWorktree`/`WorktreeExit` row — only `WorktreeEnter` is mapped per
  harness.
- `[P4]` **Folded 006 §13 ships an implementation-status report verified in
  2026-07** ("done in dev", must-have 1 "prod remains blocked on item 1", and
  must-have 3's base-URL override "not yet implemented" in rdf-registry) that
  needs refreshing against current state.
- `[P4]` **Folded 026 §4 documents a frontmatter form the corpus no longer uses.**
  Its parenthetical-annotation rule is written against `wasDerivedFrom`, and
  the fold left frontmatter carrying only `status`, `issued` and `requires`.
  Same class as the three acceptance criteria ruling 23 retired: the machinery
  stays correct for a document written after the cutover, but nothing exercises
  it today. §4.2 also still cites 025's `amends: rdf-registry:ADR-0006` as "the
  corpus's one existing cross-project reference"; the fold dropped that key, so
  the corpus now has none and the unresolvable-reference example survives only
  in prose.
- `[P4]` **`WL-SPEC-25#sec-9` is awkward to type in a shell** (025 §14.3,
  026 §4.2): `#` starts a comment, so the shorthand needs quoting on every
  `lode show` invocation. Accept `WL-SPEC-25:sec-9` — and its `WL-SPEC-25:9`
  short form — as an alternate spelling that resolves identically, keeping `#`
  canonical for prose and frontmatter. Touches `designdoc.ResolveRef` and the
  shorthand fixtures (`testdata/shorthand.yaml`, exercised from both Go and
  Python).
- `[P4]` **`secrets_materialized` breaks the dotted event-type convention**
  (spec 017): every other CLI-sourced event is `task.<verb>` —
  `task.assigned`, `task.started`, `task.reworked` — and the claim ceremony's
  event is the one underscore in the set. The spelling comes from the spec, so
  it was kept rather than improvised away, but a subscriber filtering on the
  `task.` prefix (025 §15) silently misses it. Renaming it to
  `task.secrets_materialized` is a spec amendment plus a one-line change, and
  is cheapest before part 2 of the task-secrets series ships a client that
  emits it.

## From knowledge-graph plan part 1 (2026-08-19)

- `[gated]` **The Oxigraph integration tests never run on the common CI path.**
  `_test.yml` starts its ephemeral Oxigraph under `if: contains(inputs.runs-on,
  'ubuntu-latest')`, but `pr-checks.yml` routes *trusted* PRs to the
  `gha-pgvector`/`gha-buildcache` self-hosted runners, which are not targeted
  at the `docker` label, so a Docker daemon is not guaranteed to the `test` job
  the way it is to `build-image` (`docs/self-hosted-runner.md`) — so a team PR,
  the majority path, skips the branch's only
  triple-store proof (`internal/graphproj/oxigraph_test.go`: the `ns/` parse
  gate, the project-graph replace round-trip and the `dependsOn+` path) and
  only a fork PR exercises it. This follows the plan and the runner-label
  constraint, so it is not a defect. The fix is symmetrical with Postgres: an
  always-on Oxigraph container beside hel01's Postgres
  (`docs/self-hosted-runner.md`), with `TEST_SPARQL_URL` set on the
  self-hosted branch of `_test.yml` the way `postgres-dsn` already is. Worth
  doing when the graph gets a second consumer — part 2's projector, or a CI
  SHACL gate over projected graphs.
- `[gated]` **Cross-project edges project dangling, untyped IRIs** — owned by
  **WL-117**, which must decide before per-graph SHACL validation can be
  turned on. Knowledge-graph part 2 (the projector plan) shipped without
  deciding this, so the decision moved rather than being resolved: a
  `blocks`/`child_of`/`follow_up_to` edge that crosses projects lands `A
  wl:blocks B` in P1's named graph and `B wl:dependsOn A` in P2's; neither
  graph holds both ends, so each carries an object IRI with no `rdf:type`
  beside it. Per-graph SHACL validation then fails: `wl:followUpTo`'s
  `sh:class wl:Task` (`ns/shapes.ttl`) is unsatisfied by a foreign end. Two
  candidate answers — emit a bare `rdf:type wl:Task` stub for out-of-graph
  ends, or scope validation to the union of the project graphs — and the
  choice is the projector's, not the renderer's.

## From WL-141 — three-valued plan coverage (2026-08-20)

- `[gated]` **A plan can close its own section by naming itself in
  `fullCoverageWith`.** `Store.NeedsPlanning` (`internal/store/docs.go`)
  discharges a `partial` claim once every plan named in its
  `fullCoverageWith` is accepted and itself contributes `full` or `partial`
  to the same section — and nothing excludes the naming plan from that set.
  026 §2.1's text permits this: the test is per-named-plan ("accepted and
  contributes `full` or `partial` to S"), stated with no case for the naming
  plan. But §2.1's stated rationale for the closure check is about siblings
  covering the rest of a section, and self-naming is a roundabout spelling of
  `coverage: full` rather than a real closure, so the rule probably wants
  narrowing to exclude the naming plan. That is an amendment to 026 §2.1, not
  a query fix — `scripts/secmeta.py`'s §7 check does not catch it either, so
  both would move together once the spec decides.

## From WL-148 — `lode task cost` (2026-08-20)

- `[P4]` **`leases` has no plain index on `task_id`.** Only the partial
  `leases_active` index (`WHERE released_at IS NULL`,
  `deploy/base/migrations/0001_baseline.up.sql`) exists, so `Store.TaskCost`
  (`internal/store/session_usage.go`) scans released leases too when reading
  a task's cost. Small today, a migration when lease volume grows.

## From WL-198 — non-regressing github fact upserts (2026-08-20)

- `[P3]` **`main_commits` orders by insertion id, so a replayed push lands out
  of order.** `AppendMainCommit` (`internal/store/delivery.go`) gives each
  default-branch commit the next serial id, and the frontier ids a release or
  deploy captured (`LatestMainID`) are compared against it. A backlogged push
  replayed after those facts landed gets a *higher* id than commits already
  recorded, so an existing frontier no longer covers it and a task behind that
  frontier reads as not-yet-delivered. The direction is safe — it under-reports
  rather than corrupts — so it was out of scope for the non-regressing-upsert
  fix, which guards fact columns and not this ordering. Reconcile engine 2
  (013 §2.2, WL-33) repairs facts against GitHub's current truth and heals it.

## From WL-207 — blob spool volume (2026-08-20)

- `[P2]` **`POST /api/v1/blobs` has no concurrency cap, so the spool volume's
  `sizeLimit` is enforced by pod eviction.** Each upload spools up to
  `maxBlobBytes` (100 MiB) to the `blob-spool` emptyDir, sized 1Gi in
  `deploy/base/deployment.yaml`. Nothing bounds in-flight uploads, so ~11
  concurrent max-size ones exceed the limit and the kubelet evicts the pod —
  killing every other in-flight request on a single-replica Deployment, and
  reachable by any client holding a valid token. Unbounded ephemeral storage
  is the worse trade, so the fix is a semaphore in the handler capping
  concurrency at `sizeLimit / maxBlobBytes` and returning 503 above it, not a
  larger volume. Needs the 503-vs-queue decision made before it is written.
- `[P4]` **Spec 021 §13 does not mention the spool directory's writability
  requirement.** The table still reads "defaults to `os.TempDir()`"; since
  WL-207 the server refuses to boot when blob storage is configured and that
  directory is unwritable, and the deployment mounts a volume for it. A
  one-line amendment to §13 would make the corpus of record true — README.md
  and the manifests already are.
- `[P4]` **Only the blob spool has a writable path; `os.TempDir()` is still
  read-only in-cluster.** Any future dependency that stages through it —
  multipart form parsing, an SDK that buffers uploads, exec'ing git — fails
  the same EROFS way WL-207 just fixed. Pointing `TMPDIR` at the spool mount
  would close the class rather than the instance, at the cost of letting
  unrelated temp writes consume the blob spool's budget.

## From the 2026-08-19 doc-todo-rollup plan

`lode doc todo <ref> [--deps] [--json]` (`internal/designdoc`,
`internal/cmd/doctodo.go`) landed. These are the mechanical leftovers surfaced
while dogfooding it against the real corpus.

- `[P1]` **`scripts/secmeta.py` has drifted from the amended 026 §2.1.** It is
  a second implementation of the same plan-coverage rule `designdoc.PlanIndex`
  now implements in Go, and it now lags in two ways: it does not treat
  `superseded` plans as discharging a section (the amendment this plan made to
  026 §2.1), and its `resolve_ref` implements only the bare-filename arm of
  §4, not the `./` or `../` relative-path arms the Go side handles. Two
  implementations of one spec rule disagreeing is what produced two separate
  defects during this plan's execution. `secmeta.py` reports and never
  rewrites (by design), so this is a script fix, not a data fix — but it
  should happen before the next amendment to 026 §2.1 has to be applied
  twice.
- `[P2]` **`TodoItem` should carry structured `Blockers []string`** instead of
  `internal/cmd/doctodo.go`'s `shortenPlanRefs` doing `strings.ReplaceAll` over
  prose that `internal/designdoc/todo.go` wrote into `Detail`. The rendering
  is correct today, but the seam is wrong: it works only because `todo.go`
  happens to emit repo-relative plan paths into that prose, and would fail
  silently — wrong or unshortened paths in `blocked` rows, not a crash — the
  day `Detail`'s wording changes without a matching edit to
  `shortenPlanRefs`'s pattern.
- `[P4]` **A `TaskExists` helper for the six `GetTask` callers that use it
  purely as an existence check**: `internal/api/tasks.go:90,:99`,
  `internal/api/secrets.go:72`, `internal/api/admin.go:524,:639`, and
  `internal/store/brief.go:57`. `GetTask` now returns the full `model.Task`
  (including `Closed`, which costs a second round trip in `store.GetTask`'s
  implementation), and none of these six callers read anything but the
  not-found error.
- `[P4]` **`obsidian/src/api/types.ts`'s hand-kept `Task` interface lacks
  `closed`** (`internal/model/task.go`'s `Closed bool`), and already lacked
  `secrets`. Pre-existing drift, not introduced here — WL-76 (generate
  `obsidian/src/api/types.ts` from `internal/model` instead of hand-mirroring)
  is the real fix; noting it here because this plan's `model.Task.Closed`
  read is what surfaced the gap.
- `[P4]` **Every spec in `docs/specs/` is `status: draft`** — all 24 files,
  including several long since built (e.g. 004, 005, 017, 022). This is why
  `lode doc todo` leads every answer with a `plan-draft (document)`
  acceptance item: with no spec ever marked `accepted`, the "document is
  draft: acceptance is the first act" gap fires universally rather than
  distinguishing built specs from unwritten ones. Backfilling `status` per
  spec is a deliberate human act (025 §7 — acceptance is a status transition,
  not something to automate), not a script; recording it here so the question
  is visible rather than rediscovered the next time `doc todo`'s output looks
  noisier than expected.

- `[P2]` **`resolveDoc`'s bare-filename arm builds its key by concatenation.**
  `internal/designdoc/coverage.go` joins with `dir + "/" + name` there while
  every other arm uses `path.Join`, so a corpus directory of `"."` derives
  `"./x.md"` on one arm and `"x.md"` on the others and the two keys never
  meet — every section then reads `unplanned`, with no diagnostic. Unreachable
  from `lode doc todo`, which feeds the walk the canonical `docs/specs` and
  `docs/plans` paths `designdoc.CorpusPath` builds, so this is latent rather
  than live; `path.Join` on that one line fixes it.

- `[P3]` **026 §1 still describes an unbuilt `--docs <dir>` flag.** Its "no new
  config key" claim is true again — WL-147 made `spec_corpus`/`plan_corpus`
  accepted-and-ignored — but the flag it offers in their place was never built,
  and the corpus location is no longer a client-side question at all.
