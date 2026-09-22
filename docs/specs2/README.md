# Worklode architecture, as of 2026-09-18

This directory is a restructured, present-tense reading of the 47 specs in the
backbone (`lode doc list --kind spec`). Amendments and supersessions are folded
in. History, rationale and migration notes are cut. When two source specs
disagreed, the higher-numbered or accepted spec won, and each document records
those calls in its "Resolved conflicts" section.

The backbone stays the source of truth while this set is being reviewed. Nothing
here has been posted back with `lode doc`.

## The ten documents

| File | Covers | Source specs |
|---|---|---|
| 01-system-and-deployment.md | Glossary, the six executables and their import boundaries, prod and sandbox deployment, metrics, usage accounting, overhead cost | 2, 53, 39, 38, 22, 63, 52 |
| 02-identity-actors-and-secrets.md | OIDC and tokens, roles and route guards, agent actors, task-declared secrets, secret templates | 1, 54, 17, 42 |
| 03-tasks-and-execution.md | Projects, tasks, state machine, leases and worktrees, event log, webhook transitions, prioritization, deletion, research work | 4, 5, 44, 29 |
| 04-done-verification-workflows.md | Definition of done, verification, per-project workflows, the rule engine | 57, 58, 45, 46 |
| 05-documents.md | Document model, sections and anchors, edges, lifecycle, revise vs edit, invalidation | 25, 55, 67 |
| 06-design-queries-intents-decks.md | Plan coverage queries, intents, decision decks, meetings and notes, attachments | 26, 70, 62, 64, 21 |
| 07-knowledge-graph-and-search.md | Ontology and entity model, projection, hybrid search, IRIs, embedding trial | 6, 40, 68, 69 |
| 08-agent-harness-and-sessions.md | Hooks and statusline, sessions, Pi integration, reconciliation, inbox import | 8, 12, 41, 13, 20 |
| 09-cli-and-skills.md | The lode command surface and naming rules, repo-scoped commands, org skills, vendored design skills | 61, 19, 16, 37 |
| 10-cockpit.md | Cockpit pages, navigation shell and inbox, review surface, diffs, fact-selected panels, Progress page, drift | 32, 56, 59, 60, 65, 66, 7 |
| 11-design-authority-gate.md | The merge gate that keeps specs the design authority: the what/how citation test, guarded paths, the `Spec:` trailer, plans are read-only after minting | new |

## Section map

`section-map.tsv` maps every H2 (and split H3) of the 47 old specs to the new
document and section that holds it. Columns: `old_ref`, `old_anchor`,
`old_title`, `new_file`, `new_section`, `disposition`, `note`. Dispositions:
`moved`, `merged`, `pointer` (content owned by another new file), and three
kinds of drop: `dropped-stale` (superseded shape), `dropped-history`
(rationale, migration, amendment notes), `dropped-other` (testing, acceptance
and dependency lists; reason in `note`). 661 rows, all 47 specs covered.

Gaps the map found: `worklode_deletes_total` (WL-SPEC-44 §6) has no home;
WL-SPEC-8 §21 acceptance criteria were not carried; WL-SPEC-57 §7's backfill
paragraph and WL-SPEC-58 §2.4's defect references were dropped.

## Spec to document map

| Spec | Document |
|---|---|
| WL-SPEC-1 | 02 |
| WL-SPEC-2 | 01 |
| WL-SPEC-4 | 03 |
| WL-SPEC-5 | 03 |
| WL-SPEC-6 | 07 |
| WL-SPEC-7 | 10 |
| WL-SPEC-8 | 08 |
| WL-SPEC-12 | 08 |
| WL-SPEC-13 | 08 |
| WL-SPEC-16 | 09 |
| WL-SPEC-17 | 02 |
| WL-SPEC-19 | 09 |
| WL-SPEC-20 | 08 |
| WL-SPEC-21 | 06 |
| WL-SPEC-22 | 01 |
| WL-SPEC-25 | 05 |
| WL-SPEC-26 | 06 |
| WL-SPEC-29 | 03 |
| WL-SPEC-32 | 10 |
| WL-SPEC-37 | 09 |
| WL-SPEC-38 | 01 |
| WL-SPEC-39 | 01 |
| WL-SPEC-40 | 07 |
| WL-SPEC-41 | 08 |
| WL-SPEC-42 | 02 |
| WL-SPEC-44 | 03 |
| WL-SPEC-45 | 04 |
| WL-SPEC-46 | 04 |
| WL-SPEC-52 | 01 |
| WL-SPEC-53 | 01 |
| WL-SPEC-54 | 02 |
| WL-SPEC-55 | 05 |
| WL-SPEC-56 | 10 |
| WL-SPEC-57 | 04 |
| WL-SPEC-58 | 04 |
| WL-SPEC-59 | 10 |
| WL-SPEC-60 | 10 |
| WL-SPEC-61 | 09 |
| WL-SPEC-62 | 06 |
| WL-SPEC-63 | 01 |
| WL-SPEC-64 | 06 |
| WL-SPEC-65 | 10 |
| WL-SPEC-66 | 10 |
| WL-SPEC-67 | 05 |
| WL-SPEC-68 | 07 |
| WL-SPEC-69 | 07 |
| WL-SPEC-70 | 06 |

## Command naming decisions still open

Commands the specs describe but the binary does not have. Each was renamed to
follow the SPEC-61 naming rules. Confirm or change before posting back.

| Code | Old spelling | New spelling | Reason |
|---|---|---|---|
| N1 | `lode task done` | `lode task close` (04) or `lode task set state merged` (03) | 03 and 04 chose differently. 04's act runs the done check under the project workflow. 03's is a raw state write. One name is needed. |
| N2 | `lode project set --default-deliverable <ref>` | `lode project set default-deliverables <ref>...` | field write form |
| N3 | `lode project workflow show` | `lode project workflow` | a view is a noun |
| N4 | `lode project workflow set --file` | `lode project set workflow --file` | a view never writes |
| N5 | `lode project workflow validate --file` | `lode project set workflow --file --dry-run` | no new verb |
| N6 | `lode auth link github` | `lode actor link github` | `auth` is not an entity |
| N7 | `lode auth status` | `lode actor show` | `status` is taken by `work status` |
| N8 | `lode decompose <ref>` | `lode doc decompose <ref>` | no bare top-level verbs |
| N9 | `lode task view` | `lode task show --web` | `view` is not an allowed verb |
| N10 | `/lode-spec` | `/lode:spec` | slash form; distinct from the built `/lode:plan-spec` |
| N11 | `lode skill search` | `lode search` | search is the one cross-entity reader |
| N12 | `lode doc fetch` | kept; `fetch` added to the verb allowlist | `show` renders, `fetch` writes the local cache |
| N13 | `lode review open` | `lode review add` | new `review` entity; `add` creates |
| N14 | `lode review comment` / `reply <t>` | `lode review note <id> [--thread <tid>]` | mirrors `doc note` |
| N15 | `lode review approve` / `request-changes` | `lode review set verdict <id> approved\|changes_requested` | state write is `set` |
| N16 | `lode review guide --focused` | `lode review guide` (view) and `lode review set guide <id> [--focused]` | a view never writes |
| N17 | `lode review desk` / `await` / `ingest` | `lode review exec` / `listen` / `import` | allowlisted verbs |
| N18 | `lode doc export --anchors` | `lode doc show <ref> --anchors <map>` | no `export` verb |
| N19 | `lode pr read <repo>#<n>` | `lode task diff <id> [--pr <repo>#<n>]` | no `pr` entity |
| N20 | `lode specs --drifted` / `--unimplemented` | `lode graph drift --docs` / `lode graph gaps --unimplemented` | `specs` is not a command |

N13 to N17 need a `review` entity added to the SPEC-61 entity list in 09.

## Code the specs do not describe

Found while restructuring. Each is a place where the code is ahead of the design.

- The route guard table and default-deny `Decide` in `internal/api`. 02 §3 was written from the repo CLAUDE.md.
- Compare-and-swap on `lode doc edit` (WL-848). 05 states it in one sentence; the precondition shape is undocumented.
- The `adr` document kind is retired by SPEC-70, but `internal/cmd/doc.go` and `ns/ontology.ttl` still ship it.
- `permSearchRead` and the entity route families in SPEC-68 §3 are described as existing but have no `routeGuards` entries.
- POST /reviews/{id}/verdict was used by SPEC-59 §8 but absent from its own route table (now added to 10 §13.6).

## Resolution log

How each document resolved disagreements between its source specs. History,
kept here so the documents themselves stay present tense.

### 01-system-and-deployment
- WL-SPEC-63 §1 amends WL-SPEC-52 §3: the hook-side transcript classification (`classifyTranscriptUsage`, the widened heartbeat and session-end guards) is replaced by Edge Agent OTLP export. 52's store, API, schema and cost-report sections stand. Its classification rules are kept only as the backfill's semantics (7.4).
- WL-SPEC-22 §1 places the metrics composition root in `internal/cmd/serve.go`. WL-SPEC-53 §2 forbids `internal/cmd` importing `internal/api` or `internal/store`, so the root is `lode-server`'s main package. 53 wins.
- WL-SPEC-53 §3 keeps compatibility shims for one release and schedules their removal. Described as removed.
- WL-SPEC-52 §4 names the request body type `ProjectOverheadUsageInput` with a flat `Usage` field while §2 defines the wire body as `{agent, external_session_id, by_task}`. The §2 shape is the decision the whole-session argument depends on and is what this document records.
- WL-SPEC-2 §5 calls milestones "not yet built". WL-SPEC-39 §1 treats them as added by spec 029. The glossary entry points to `03-tasks-and-execution.md` without a build status.
- WL-SPEC-38 §4.3 calls the operator's long-lived sandbox token transitional and designs the task-scoped token (WL-136). Described as the task-scoped token, with its model deferred to `02-identity-actors-and-secrets.md`.
- WL-SPEC-39 §4.1 (cutover from hzprod) is transitional and dropped.

### 02-identity-actors-and-secrets
- Task-token provenance: WL-SPEC-1 §2.1 records the minter in the token description; WL-SPEC-54 §4 moves it to `tokens.minted_by`. 54 wins even though the amendment is marked pending.
- Catalog storage: WL-SPEC-17 §1 and WL-SPEC-42 §2 say a git-tracked ConfigMap; the folded ADR 043 says a 1Password item projected into a Kubernetes Secret. ADR 043 wins.
- Exit purge: WL-SPEC-17 §4 purges unconditionally on worktree exit; folded ADR 048 makes exit conditional on a definite "lease gone". ADR 048 wins.
- Command spelling: WL-SPEC-17 uses `/lode-done`, `/lode-block`, `/lode-resume`; WL-SPEC-42 uses `lode worktree done|block|resume`. 42 wins.
- Login command: WL-SPEC-1 §8 says `lode login`; §9.4 renames it `lode auth login` with the old spelling as a hidden alias. §9.4 wins.

### 03-tasks-and-execution
- Task kinds: WL-SPEC-4 §1.1's SQL still lists `spec`; the 025 §10 amendment renames it `design` and adds `decision`, and 005 §2a adds `rally`. This document uses the eight-kind set.
- Worktree path: 005 §4 and 025 §15.6 still say `wt/<id>-<slug>`; WL-SPEC-4 §1.2 and spec 008 retire it in favour of `<worktree_dir>/<branch>`, default `.worktrees/<id>-<slug>`. The latter wins.
- Branch prefix: 029 §1 gives `lode/COW-7-slug`; WL-SPEC-4 §2.5 retires `lode/` and renders from `LODE_BRANCH_TEMPLATE`. The template wins.
- `draft → ready` verb: WL-SPEC-4 §6.4 says `lode task ready`, 005 §2a says `lode task publish`. This document uses `publish`.
- `events.source`: WL-SPEC-4 §1.4's CHECK lists five values, but the event table names `web` and 029 §8.3 adds a source per ingest system. Described as an open set grown by migration.
- `lode task done` on a parent: WL-SPEC-4 §12 Q7 explains the refusal as "done is `in_review → merged`", but §5.1 lets `done` start from any pre-merge state. The refusal stands because the parent's `in_progress → merged` is roll-up only; the reasoning follows §5.1.
- Reopen source states: §5.1 omits `abandoned`, §8 includes it. §8's list is used.

### 04-done-verification-workflows
# 04-done-verification-workflows.md: resolved conflicts

- WL-SPEC-45 §4.1, §4.2 and §6.2 carry "Pending spec 46, not yet effective" markers on the `rules` key, rule validation and the review task naming rules. WL-SPEC-46 is the higher-numbered spec and is written here as in force.
- WL-SPEC-57 §4.1 says `done` closes from any pre-merge state. WL-SPEC-45 §5.1 says `done` accepts `ready`, `in_progress`, `in_review` and refuses `draft`. Section 3 follows 045.
- WL-SPEC-57 §0 says `done_state` stays "until Workflow claims it". WL-SPEC-45 §7 keeps `done_state` with a compatibility warning and defers folding it in. Section 9 follows 045.
- WL-SPEC-45 §6.2 minted the review task "naming the changed workflow names"; WL-SPEC-46 §5.4 extends it to rules. Section 8.10 carries the extended form.

### 05-documents
- 025 §18 says `lode show --resolved`; 055 and the CLI say `--inline`. `--inline` wins.
- 025 §14.3's regex allows only `SPEC|ADR`; 029 §4 (folded into 025) adds `PLAN` with per-kind counters. `PLAN` and counters win. File-derived numbering (025 §16.3) is withdrawn.
- 025 §18's `lode doc new`, `doc get`, `doc anchors`, `doc reviewers --set`, `lode drift`, `lode derive` are renamed by 061 to `doc add`, `doc show`, `doc lint`, `doc set reviewers`, `graph drift`, `graph derive`. 061 wins.
- 025 §10 calls the authoring kind `spec` in places and `design` elsewhere. `design` wins (the CHECK lists it).
- 025 §5.1 and §16 (git to backbone sync on-ramp) are withdrawn in the source and dropped here. 055 deletes the files. The `[doc_sync]` opt-in mirror of 025 §5 is kept as specified.
- 025 §8.6 says the grooming task is `spec`-kind. With the rename it is `design`.
- 067 defines closed tasks as the five states `lode task list` hides plus soft-deleted rows. 025 §8.2's referrer set is narrower. Both kept, as 067 intends.

### 06-design-queries-intents-decks
- 026 §12 criterion 5 ("every query computed without contacting the server") versus 026 §1 and §2.5 (the corpus is the backbone's, no offline answer): the later text wins; every query needs the server.
- 026 §4.1 (`scripts/secfrozen.py` pre-commit gate) versus 026 §4.1's own "after 025 the gate moves to the server": anchor permanence and acyclicity are accept-time checks.
- 026 spells the consolidated view `lode show --resolved`; the exports these documents were built from and the repo's own instructions spell it `--inline`. Written as `--inline`.
- 070 retires the ADR kind; 026 §2.4 and 064 §0 still treat ADRs as a live kind. 070 wins; `--kind adr` and `--adr N` are kept only as ref spellings for documents that still exist.
- 026 §5.2 (`task` frontmatter key on plans as a transitional stand-in) versus 025 §9.2 (tasks carry the plan reference): written from the task side.
- 021 §4 (blob route passes through with no web auth provider) versus 021 §4 and Q021.4 (refused unless `LODE_WEB_OPEN`): the closed default wins.
- 021 §6's earlier proposal to set CSP and `X-Content-Type-Options` as object metadata is dropped; uploads send no user metadata.
- 026 §6.1 renames 006's `wl:covers` (Artifact → Commit) to `wl:cutFrom`; recorded here since 07-knowledge-graph-and-search.md folds 006.

### 07-knowledge-graph-and-search
- WL-SPEC-6 §10 and §10.1 (`id/<type>/<localid>` under `worklode.io/ns/id/`) versus WL-SPEC-68 §3 (plural paths under the instance base): 68 wins. 6 still marks 68 §3 as pending.
- WL-SPEC-6 §10 versioned document IRI `doc/<slug>/v<n>` versus WL-SPEC-68 §4 `/docs/<KEY>-<TYPE>-<n>/<v>`: 68 wins.
- WL-SPEC-6 §1 and §11 list `wl:produces` and `wl:affects` as v1 mints; §11's own amendment moves both out of v1 projection. The amendment wins.
- WL-SPEC-40 §8 single global `embedding_config` versus WL-SPEC-69 §3.2 per-column rows: 69 wins.
- WL-SPEC-6 §7 and §11 carry "pending ADR 49 / 62" markers while also quoting those amendments as in force. The amendments are treated as in force.

### 08-agent-harness-and-sessions
- WL-SPEC-8 §16.3 says Codex has no `SessionEnd`; ADR 051 says it does and the adapter binds it. ADR 051 wins.
- WL-SPEC-8 §17.2 treats Amp as a settings array to merge; ADR 051 makes it a generated plugin binding `session.start` and `agent.start`/`agent.end`. ADR 051 wins.
- WL-SPEC-8 §17.4 delivers pi as an installer-generated global shim; WL-SPEC-41 delivers a project-local package. 041 wins.
- WL-SPEC-8 §17.5 has the status line spool token facts that the heartbeat flushes, and WL-SPEC-8 §17.6 and WL-SPEC-12 §7 derive live cost from the transcript and a Worklode OTLP receiver; WL-SPEC-63 replaces both with Edge Agent posting replacement totals. 063 wins; the status line stays a pure local read.
- WL-SPEC-12 §4 guards `heartbeat`/`session-end`/`worktree-enter` on a task-bearing directory; WL-SPEC-52 relaxes it to any git worktree and bills unattributable tokens as overhead. 052 wins.
- WL-SPEC-8 §17.7's managed block leads with `lode task claim`; ADR 051 leads with `lode worktree next`, which WL-SPEC-61 spells `lode work next`. The current spelling is used.
- WL-SPEC-13 and 20 name `lode reconcile`, `lode project doctor`, `lode project add-repo`, `lode task ready`, `lode skills install`, `lode worktree *`; WL-SPEC-61 renames them to `lode task reconcile`, `lode project health`, `lode project repo add`, `lode task publish`, `lode skill install`, `lode work *`. 061 wins.
- WL-SPEC-8 §1 lists `lode hook` and `lode statusline` as one-release compatibility shims; they have been removed, so only `lode-hook` and `lode-statusline` are named.

### 09-cli-and-skills
- 016 and 037 spell the skill group `lode skills`; 061 L1 renames it to `lode skill`. 061 wins throughout.
- 016 names `lode reconcile` as the sync fallback; 061 moves it to `lode task reconcile`, which repairs task state only. Skill resync is `lode skill sync`.
- 019 and 037 use `lode task ready/done`, `lode resume`, `lode done`, `lode worktree next/done`; 061 renames them to `task publish`, `task set state`, `work resume`, `work submit`, `work next`. 061 wins.
- 019 lists `board`, `next`, `status` as top-level commands; under 061 they are L9 shortcuts for `task board`, `work next`, `work status`.
- 019 §4.4 marks `WL-PLAN`, `WL-MILE`, `WL-DEL` as not showable until those entities exist; 061 L1 adds `milestone` and `deliverable` as entities with CLI verticals. The typed ids are recorded as recognized, without the "not showable" clause.
- 061 §2.5 lists thirteen L1 entities; its later-revised L1 lists sixteen (adding `decision`, `deliverable`, `milestone`). L1 wins.
- 037 §6.3 and §11 assume a git-first corpus with `lode doc sync` as the only write path; 055 (05-documents.md) moved documents into the backbone with `lode doc add`/`edit`. Write-back is described neutrally and pointed at 05-documents.md.
- 037 §11 defers retrieval-based skill routing to a future spec; 040 (07-knowledge-graph-and-search.md) ships `lode search` over docs, tasks and skills. Pointed there.

### 10-cockpit
- 032 §2's eight destinations in a second navigation row give way to 056 §1's five destinations in the top bar; Home, Reviews and Deliveries keep routes.
- 032 §10's fixed bottom tab bar below 880px gives way to 056 §1's wrapping top bar with a More control.
- 032 §13's "no arbitrary dashboards" is read with 065: a fixed, non-configurable set of four fact-selected panels.
- 056 §2's sidebar list gains Progress after Work per 066 §2.
- 007 §5's command spellings (`lode overview`, `lode status`, `lode drift`, `lode gaps`, `lode derive`, `lode frontier`, `lode ready`, `lode critical-path`) are replaced by 061 §2's; `lode status` and `lode ready` are deleted.
- 032 §7's "GitHub remains the primary interaction surface for PR review" gives way to 059 §1.2: GitHub stays the surface for untracked work, and a task's change is reviewed through the Worklode review object.
- 032 §12's "the cockpit carries no client state" gives way to 066, whose Progress page ships page script and keeps expansion state in the browser.
- 032 §10's "nothing in the cockpit appears on hover or focus" gives way to 066 §2.4's hover tooltips.

