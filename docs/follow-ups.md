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
`coverage: partial` claim (026 §5, `lode:splitting-specs-into-plans`) states the
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
  `handledEvents`, so `lode project repo add` warns "github app is not
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
- `[P2]` **`lode task list --mine`**: blocked on `cli.Config` not persisting the
  caller's actor id — `lode login` prints `res.ActorID` and discards it. The
  `whoami` route and `Client.WhoAmI` now exist (spec 013); persisting the actor
  id at login unblocks this. Until then `--assignee <actor>` is the explicit
  form.
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
  nullable `milestone_id` is a column the milestone table will add. §3.2's
  **poll prober** does not exist: a push emitter can now report state (the
  signed data-catalog ingest files evidence against the declared address), but
  an address nothing pushes about is never checked, so "is the project
  published" is answered only for the deliverables an emitter covers.
  Identity **by label** (§3.1's `worklode.deliverable=COW/datasets`)
  is not modelled — only by address. And no CLI verb exists: `lode deliverable
  list/add` would mirror `POST|GET /api/v1/projects/{id}/deliverables`.
- `[gated]` **`project_entity_seq` carries only `DEL`**: spec 029 §4 gives milestones,
  specs, ADRs, and plans their own per-project ordinals from the same counter
  table. Its `kind` CHECK admits `'DEL'` alone, so each of those arrives with a
  one-line CHECK widening, the same way the `tasks.kind` CHECK grows.
- `[P3]` **k8s deployment manifests for the watcher**; RBAC for the `worklode-watch` image
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
  Adding it changes `PutGraph`'s signature. WL-266 (spec 007 §1.1) scoped this
  as hardening, not a prerequisite: the multi-repo `lode graph derive` case is solved
  by per-repo graph partitioning, and same-graph races are last-write-wins over
  fully recomputed documents — at worst one run stale, self-healing on the next
  run.
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
  `/lode:spec` is the entry point. The file corpus it wanted gated is gone
  (055), so what is left of this item is the single authoring door and, if
  anyone still wants grep over the corpus, a `lode doc export`. Customising or
  hook-patching the brainstorming skill is the wrong
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
  `/projects/{id}/documents`, `/projects/{id}/activity`, `/ideas`, `/intake`,
  `/reviews`, `/deliveries`) are placeholders naming their owning spec
  section; replace each with its real implementation as Parts 2–4 land.
  Deliverables has left this list — it is a built destination now, and so has
  Knowledge: it lands on the document corpus at `/docs` (WL-127), with the
  graph-backed expert views still to join it there.
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
  a query fix.

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

- ~~`[P1]` **`scripts/secmeta.py` has drifted from the amended 026 §2.1.**~~
  Resolved by deletion: the script went with the file corpus (055), leaving
  `designdoc.PlanIndex` as the only implementation of the rule.
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
- `[P4]` **`plugins/obsidian/src/api/types.ts`'s hand-kept `Task` interface lacks
  `closed`** (`internal/model/task.go`'s `Closed bool`), and already lacked
  `secrets`. Pre-existing drift, not introduced here — WL-76 (generate
  `plugins/obsidian/src/api/types.ts` from `internal/model` instead of hand-mirroring)
  is the real fix; noting it here because this plan's `model.Task.Closed`
  read is what surfaced the gap.
- `[P4]` **Nearly every spec is `status: draft`**, including several long
  since built (e.g. 004, 005, 017, 022). This is why
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

## From WL-38 — task blob references and CLI (2026-08-20)

- `[P2]` **`lode task attach`/`detach` bypass `recordEvent`.** Every other
  mutating task handler in `internal/api` writes through it, so an attach or
  detach leaves no row in `events` and no entry in the task timeline — the
  reference graph changes with no provenance. Its own change rather than a
  patch here, because it moves `AttachBlob`/`DetachBlob` off `*Store` and onto
  `*sql.Tx` so the write and its event commit together.
- `[P2]` **`blobref.Extract` sees only `ast.Image`, so a raw-HTML
  `<img src="/blob/…">` is not counted as an embedded reference.** Not
  reachable today — nothing renders raw HTML in a task body — but `Extract`
  is documented as the authority for the `embedded` flag, and plan 3 brings
  both HTML rendering and the GC that deletes unreferenced blobs. An
  uncounted reference there is a live image whose bytes get collected, so
  settle it before plan 3 starts: either count raw-HTML `img` sources or
  state in 021 that only markdown image syntax references a blob.
- `[P4]` **Spec 021 §7's warning for a local file behind a plain link is not
  implemented.** §7 says a link to a local file "is left alone and reported
  as a warning — use `lode task attach` for those". `uploadBodyImages`
  (`internal/cmd/task.go`) does leave it alone, correctly, but says nothing,
  so an author who wrote `[shot](./shot.png)` gets a task whose link resolves
  nowhere and no hint why. `blobref` already walks `ast.Link`, so this is a
  second exported helper and one `Fprintf`.

## From WL-52 — project crew participants e2e (2026-08-20)

- `[gated]` **`crew.write` is granted to every authenticated user, not scoped
  to a project's own Crew.** `internal/api/authz.go`'s grants table reads
  `permCrewWrite: {RoleUser, RoleAdmin}`, so spec 029 §6.1's "any Crew member
  may add or remove an ordinary Crew member" is enforced far wider than the
  spec intends — any authenticated actor, Crew member or not, can add or
  remove one on any project. This is a conflict between spec and system, not
  a planned partial: there is no project-scoped role concept yet (`authz.go`'s
  own package doc says as much), and fixing it needs authz decisions to become
  project-scoped, which is a larger change than this plan's task set. Gated on
  that decision landing. The deputy designation (spec 029 §6.1) sharpens the
  stakes too: deputy is meant to carry full lead authority, so that authority
  needs to be properly scoped before it gates anything real, though today
  nothing in `authz.go` grants anything off `is_deputy` so the label is purely
  descriptive.
## From WL-47 — multi-harness skill delivery (2026-08-21)

- `[P4]` **Five in-tree comments cite a "spec 024" that does not exist.**
  `internal/cmd/install.go` ("spec 024 acceptance 4"),
  `internal/harness/jsonhooks.go` and `internal/harness/codex_test.go`
  ("spec 024 acceptance 6"), `internal/hookrun/hookrun_test.go` ("024
  acceptance 3") and `internal/store/agent_sessions_test.go` ("spec 024 adds
  it as a harness"). There is no spec 024; it was an earlier
  number for what became 008 §17, so each should name the 008 criterion it
  means. They landed with the adapter core (WL-46) and were left alone here
  rather than rewritten under an unrelated task. Note the open-question ids
  `Q024.1`-`Q024.5` in 008 §20 are NOT affected — the `024` in a question id
  is historical but the ids themselves resolve.
- `[P4]` **008 §20's Q024.2 is now answered in code but still open in the
  spec.** It asked whether moving `.store` out of `~/.worklode/skills/`
  should "migrate silently, or does `lode doctor` report and `lode skills
  install` re-fetch?". `skillstore.migrateLegacyStore` settles it: silent, by
  rename, best-effort, with a failed rename degrading to the re-fetch the
  question names as the safe fallback. Worth folding the resolution into the
  spec so the next reader does not re-litigate it.
- `[P4]` **`lode uninstall --no-vcs --no-agent --skills` is accepted and does
  nothing.** `resolveHookTargets` serves both `install` and `uninstall`, and
  WL-47 taught its "nothing to do" guard about `--skills` so that
  `lode install --no-vcs --no-agent --skills` works. Uninstall never reads
  `targets.skills` — v1 deliberately does not remove skill links (they are
  inert, `--skills` was an explicit opt-in, and removing `~/.claude/skills`
  entries risks user content) — so that one combination now passes the guard
  and is a content-free run. Harmless, but arguably still "nothing to do".
  Tightening it means teaching the shared guard which of its two callers
  honours the flag, which is more structure than the corner case is worth.
- `[P3]` **`lode skill install --link` reports skips more thinly than
  `lode install --skills` does.** `publishLinked` (`internal/cmd/skills.go`)
  prints only id, action and path, and never reads `PublishResult.Skips`, where
  `reportInstall` (`internal/cmd/install.go`) names a reason. So `--link all`
  against a foreign symlink at `~/.agents/skills` prints
  `amp: skipped ~/.agents/skills` with no explanation, and when `PublishDirLink`
  delegates to `PublishPerSkill` the individual skipped skill names are dropped
  entirely. Spec 008 §18 row 4 wants every refusal named; the install path does
  that, this one does not. WL-47 already aligned the two loops on error
  handling — skip *reporting* is the half that stayed divergent.
- `[P4]` **Two different ownership tests guard the same kind of path.**
  `internal/skillstore/publish.go` requires a symlink to resolve inside
  `dirs.Store` (`withinStore`) before replacing it; `linkWorktreeSkill`
  (`internal/hookrun/hookrun.go`) replaces any symlink at
  `<worktree>/.agents/skills/<name>` after only an `os.Lstat` type check. The
  looser test was a deliberate WL-47 decision (a symlink at that path is ours by
  construction in a way a plain file is not, and worktree links are disposable),
  but the exposure is not quite zero: a repo that *tracks* `.agents/skills/<name>`
  as a symlink — project-scope agent skills are a real convention — and whose
  brief names a same-named skill gets that tracked symlink replaced. It stays
  visible in `git status`, since `info/exclude` does not hide tracked files, so
  it is recoverable. Worth either matching `withinStore`'s rigour or saying so
  in the comment.

## From WL-124 — anchorless subheadings in the accept diff (2026-08-21)

- `[P3]` **An anchorless heading with no anchored ancestor is diffed by
  nobody.** WL-124 made `designdoc.effectiveContent` roll an anchorless heading
  into its nearest anchored ancestor, but that walk starts from a section:
  a top-level `## Appendix` sitting beside `## 1. First {#sec-1}` has
  `Parent == nil`, so an edit under it still marks nothing `Changed` and stamps
  no `last_revised_in` — the same silent staleness, one level up. Nothing on
  the write path refuses the shape: `LintAnchors` skips anchorless headings and
  `DepthViolations` only inspects anchored ones. 025 §6.1 scopes its "content
  within the nearest anchored ancestor" rule to headings *below* the
  addressability limit and says nothing about a shallow one, so the fix is a
  spec decision first: either require every H2 in a spec or ADR to be anchored
  (a lint, enforced at accept), or define what owns an unowned heading.
  `internal/designdoc/diff_test.go` pins today's behaviour under
  "anchorless heading with no anchored ancestor belongs to nobody".

## From WL-145 — incremental plan re-acceptance (2026-08-22)

- `[P3]` **Migration 0043 backfills `plan_task_key` from `tasks.title`, the
  source the column exists to stop trusting.** A task minted before 0043 and
  renamed since (`lode task edit --title`) carries a key its declaration no
  longer spells, so the first re-accept of that plan mints the declaration a
  second time. The blast radius is one duplicate draft task per renamed task,
  visible in the accept's own output and closable; it is bounded to plans that
  existed before 0043, since every later mint records the key at mint. A
  one-shot re-key would have to parse each accepted plan's body and match
  declarations to rows by ordinal, which is guesswork of its own — worth doing
  only if a real plan turns out to be affected.
- `[P3]` **An accepted plan's unminted declaration is invisible to every
  SQL-side query.** `NeedsExecution` and `planUnfinished`
  (`internal/store/tasks.go`) read rows, and a declaration added to an accepted
  plan but not yet re-accepted is a fact about the body. So `lode doc list
  --needs-execution` omits such a plan once its minted tasks close, and a
  downstream plan's tasks become claimable while the upstream plan still
  declares unstarted work. 025 §18's "unminted" arm always meant this; making
  it detectable needs the accept-time parse to record a declaration count, or a
  reconciler that re-reads bodies.

## From WL-205 — reconcile poll wiring (2026-08-22)

- `[P3]` **`README.md` hardcodes `lode` invocations but is not an agent
  surface.** `surfaceFiles` in `internal/cmd/agentsurfaces_test.go` scans
  `CLAUDE.md`, `internal/cmd/CLAUDE.md`, `docs/agent-surfaces.md`,
  `.claude/skills/**` and `plugins/**`, and `docs/agent-surfaces.md`'s
  register omits the README too. So the README's `lode` examples — quickstart,
  project scoping, backlog import, and now the reconciliation section — rot
  silently on the next rename. Adding `README.md` to `surfaceFiles` is a
  one-line change, but the README's placeholder-heavy examples will need
  exemptions or rewording first, which is why it did not ride along with the
  section that prompted it.
- `[P3]` **The `reconcile.Options` mapping in `internal/api/reconcile.go` is
  untested.** `internal/reconcile`'s tests call `Poll` directly with their own
  `Options`, and the API test only covers the App-less skip branch, so a
  transposed or dropped field (notably `RunID`, which `Poll` requires and which
  doubles as the system event's `external_id`) would ship green. Covering it
  means an `api.NewServer` built with a fake App key against a fake GitHub;
  the poll-engine plan's Task 13 explicitly declined to rebuild the server
  fixture for that, so it wants a shared test-server option, not a one-off.

## From WL-238 — the cockpit's Deleted destination (2026-08-22)

- `[P3]` **Spec 032 §2's project-local navigation still names eight
  destinations; the cockpit now renders nine.** WL-238 adds Deleted — spec
  044's tombstone review — as the last item in `localNav`
  (`internal/ui/layout.templ`), deliberately outside 032 §2's fixed order
  because it belongs to 044 rather than to the cockpit spec. 032 §2 (or 044
  §5, which lists only the API and CLI surfaces) is owed an amendment naming
  it, so the destination list stops being a spec that the page contradicts.
  Spec 056 §1 does not settle this: it amends 032 §2's *global* list only and
  says project-local navigation is untouched.
- `[P3]` **The Deleted page is per project and has no instance-wide view.** A
  document or task is always project-scoped, so nothing is unreachable, but
  reviewing every delete across an instance still means visiting each project.
  A global destination was out of WL-238's scope for the same reason the nav
  item above is a deviation: it would be an addition to a spec's destination
  list — and after 056 §1 that list is five, deliberately shorter.

## From WL-284 — a doc note's edges (2026-08-23)

- `[P3]` **The Obsidian mirror renders a document's outgoing `edges` but not
  its `edges_in`, because nothing on the list route dates them.** The sync
  decides whether to re-render a doc note from its list row alone (`docEtag`
  over `version`/`updated_at`, WL-196), and an inbound edge is created by
  *another* document's write, which touches neither field. A rendered
  `edges_in` would therefore be correct once and then sit stale for as long as
  nothing else about the document moved — worse than absent, since a reader
  cannot tell the two apart. Fixing it means a cheap inbound-edge revision on
  `GET /api/v1/docs` (a max over the inbound rows' event ids, say) folded into
  `docIdentity`; building that was out of scope for a cosmetic parity pass.
  The same gap has one narrow effect on the outgoing side already rendered:
  creating a document re-points references stored unresolved because it did
  not exist yet (`store.CreateDoc`), so a `to_external` entry can become a
  link without the near document's row moving. It corrects on that document's
  next write.

## From the steering-instructions final review (2026-08-25)

Surfaced while applying the fix wave from the final cross-cutting review of
`docs/plans/2026-08-25-steering-instructions.md` (`lode channel serve`, the
stdio MCP relay that lets `lode-server` push steering instructions into a
live supervisor session).

- `[P2]` **No read/cancel surface exists for a pending instruction.** `lode
  task instruct` can enqueue one, but there is no `lode task instructions`
  list, no cockpit surface, and no way to see whether an instruction is still
  pending, was delivered, or to cancel one before it is delivered. Also:
  `EnqueueInstruction` (`internal/store/instructions.go`) only rejects a
  soft-deleted task, so an instruction can be queued against a task already
  in a terminal state (e.g. `delivered`, `abandoned`) whose lease will never
  be reclaimed — it then sits pending forever with no visibility that it
  will never be delivered.
- `[P2]` **`lode install` has no automation for the `.mcp.json` entry or the
  `--channels server:lode --dangerously-load-development-channels` launch
  flags this feature needs.** Wiring a supervisor session for steering
  instructions is entirely manual today — documented in
  `docs/plans/2026-08-25-steering-instructions.md`, but not tracked as a
  follow-up until now.

Recorded by WL-347's housekeeping pass over the doc-version-history plan:

- `[P4]` **Version-to-version document diffing is deliberately out of scope.**
  `lode doc versions` and `/docs/versions/{id}/{n}` show whole snapshots; a
  diff between two versions was declined by the doc-version-history plan and
  025 §4.5 (versions are immutable snapshots read whole). Check here before
  filing a diffing feature request as new.

Recorded by WL-643's pass over spec 040 §5 and §7:

- `[P3]` **A chunker change does not make the index stale.** `content_hash`
  (040 §5, §7) covers a subject's source columns only, so `ChunkRunes`,
  `ChunkOverlap` or a `context_header` format change leaves every existing
  chunk row looking fresh while the text it holds no longer matches what the
  chunker would produce today. §8 handles the sibling case for the provider,
  by nulling `embedding`; nothing handles it for the chunker. Re-indexing is
  a manual truncate until it does. Folding a chunker version into the hashed
  expression would fix it, at the cost of one full re-embed per bump.

Recorded by WL-633's pass over spec 040 §9:

- `[P3]` **The lexical arm contributes nothing on the task-brief path.**
  Recommendation now retrieves through `store.Search` (040 §9), so a short
  `lode skill recommend --text "pytest"` matches the skill naming pytest.
  The brief path (`internal/api/brief.go`) passes the whole title plus body,
  and `websearch_to_tsquery` ANDs every unquoted term, so a brief of any
  length yields an empty `tsquery` match and the dense arm answers alone.
  §9's "a task brief naming a tool by name now matches the skill that names
  it back" therefore holds for short queries only. Extracting query terms
  from a brief (or OR-ing them) is a retrieval-quality change, not a wiring
  one, so it is not folded into this task.
- `[P3]` **Recommendation scores now render as near-identical small numbers.**
  A match's `score` is the fused reciprocal rank (040 §6.1, roughly 0.016 for
  rank 1), not the cosine similarity it was under 016. `lode skill recommend`
  (`internal/cmd/skills.go`) and the session-start brief
  (`internal/hookrun/hookrun.go`) both print it as `%.2f`, so every match now
  shows as `0.02`. The list order still carries the ranking; the printed
  number no longer distinguishes anything. Widening the format or dropping
  the column from those two views is a display decision, left out of the
  wiring change.

Recorded by WL-634's pass over spec 040 §9:

- `[P2]` **`lode search` is a top-level command 061 §1 does not name.** 040
  §9 spells the CLI verb `lode search <query>`, and it answers over three
  entities at once, so no L1 `lode <entity> <verb>` spelling is true of it
  and L7's one polymorphic reader (`lode show`, one reference in, one
  subject out) is a different operation. That leaves the top level, whose
  L2 set 061 §1 closes and whose amendment it reserves to itself. The
  command ships under 040 §9; 061 §1 needs the amendment that puts `search`
  in the map, next to a rule for cross-entity readers.
- `[P3]` **A document hit's address costs one extra request.**
  `model.SearchHit` carries the document's row id, not the `WL-SPEC-25`
  reference §9's line renders, so `lode search` fetches the project's
  document list to map ids to references whenever the results hold a
  document. A `ref` on the hit itself — built where the store already
  builds a skill's qualified name — would drop the second request.
