# Spec consolidation map (WL, October 2026)

Working document for the `/lode:consolidate-specs` run over every spec in the
Worklode project. Owner decisions are recorded here as **Answer:** lines.
Codes (T-n, X-n, K-n, C-area-n, Q-area-n) are stable for the whole run.

Links: `W(ref)` below means https://worklode.dev.sunstoneinstitute.ai/<ref>.

## 0. Where this run starts

Most of the consolidation already happened. This map plans the tail.

| Date | What happened | Evidence |
|---|---|---|
| 2026-09-18 | `docs/specs2/` written: 11 present-tense documents folding the 47 old specs, with `section-map.tsv` (661 rows) and a resolution log in its README | `docs/specs2/README.md` |
| 2026-09-23 | The 11 documents imported as [WL-SPEC-72](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-72) (gate) and [WL-SPEC-73](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-73)..[WL-SPEC-82](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-82); `lode rule supersede --map` withdrew 657 old sections with 553 successor edges; plan `covers` edges were carried to the new sections | commit f84f1bcc, `docs/specs2/ttl/README.md` "Migrate the old specs" |
| 2026-09-28/30 | Four specs added outside the fold: [WL-SPEC-83](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-83) web app (accepted), [WL-SPEC-84](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-84) Review, [WL-SPEC-85](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-85) Progress page, [WL-SPEC-86](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-86) Automation. 84/85/86 were cut out of 82; the backbone's 82 now points at them, the git file `10-cockpit.md` still holds the monolith | agent R1 |
| 2026-10-09 | [WL-SPEC-75](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-75) accepted (v2). Everything else in 72..86 is still a draft at v1 | `lode doc list --kind spec` |

What has not happened:

- No new spec has been accepted except 75 and 83. 12 targets are drafts.
- No old spec or ADR has been withdrawn or deleted. 47 old specs and 9 ADRs still list.
- 11 old accepted specs still carry live plan `covers` edges and appear in `lode doc list --needs-planning` (1, 4, 32, 40, 42, 53, 59, 61, 63, 67 and the new 75, 83).
- Four old specs still hold live rules: WL-SPEC-1 (11 accepted rules, GitHub linking §9.x added after the fold), WL-SPEC-7 (2 draft), WL-SPEC-71 (1 draft), WL-SPEC-72's own 11.
- 59 (review), 60 (diffs), 66 (Progress) and 71 (telemetry) have plans or approvals pointing only at the old doc; 84/85/86 have no `covers` edges at all.
- No target has a "Designed, not built" section; every target has a "Sources" section; 77 and 78 carry amendment and migration prose.
- `docs/specs2/` (11 files, README, section-map, ttl/) is stale against the backbone (file-to-backbone similarity 0.48–0.94) and still committed.
- Old spec numbers are cited in code comments across `internal/` (hundreds of hits) and in `docs/follow-ups.md`.

## 1. Inventory

| Where | What | Count | Status |
|---|---|---|---|
| Backbone, kind spec, numbers < 72 | old specs | 47 (11 accepted, 35 draft, 44 superseded) | rules withdrawn 2026-09-23 except the four above |
| Backbone, kind spec, 72..86 | targets | 15 (75, 83 accepted; 13 draft) | 72..82 are the fold; 83..86 added after |
| Backbone, kind adr | ADRs 36, 43, 47, 48, 49, 50, 51, 62, 64 | 9, all draft | rules withdrawn except 36 and 64 (no anchored sections) |
| Backbone, kind plan | plans | 118 (89 accepted, 99 draft incl. superseded/spent) | `covers` edges mostly already into 73..82 |
| Git `docs/specs2/` | 11 docs + README + section-map.tsv + 12-spec-refactoring-design-tree.md + ttl/ | 1 dir | stale mirror of the backbone |
| Git `docs/*.md` | agent-surfaces, review-design, project-cockpit-design-brief, pg-events, skill-integrations, follow-ups, research/ | loose docs | out of scope except link fixes |
| Pending approvals (web UI only) | 711 (ADR 64), 717 (SPEC-71), 741–744 (PLAN-132..135), 750 (PLAN-145) | 6 | owner decides |

## 2. Target set

| Code | Target | Seed | Merges in (still to do) | Retire afterwards |
|---|---|---|---|---|
| T1 | [WL-SPEC-73](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-73) System and deployment | itself | ADR 36 (one model across packages) as a §3.2 dependency-boundary rule | 2, 22, 38, 39, 52, 53, 63; ADR 36 |
| T2 | [WL-SPEC-74](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-74) Identity, actors and secrets | itself | nothing missing from WL-SPEC-1 §9 except its test list; settle `lode auth` vs `lode login` naming (C-id-2); fix `expected_github_login` → `github_username` | 1, 17, 42, 54; ADR 43, 47, 48, 50 |
| T3 | [WL-SPEC-75](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-75) Tasks and execution (accepted) | itself | nothing | 4, 5, 29, 44 |
| T4 | [WL-SPEC-76](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-76) Done, verification and workflows | itself | nothing | 45, 46, 57, 58 |
| T5 | [WL-SPEC-77](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-77) Documents | itself | 13 rule-model decisions not yet described (B3); ADR 64 as the `lode show` resolution rule; §18–19 (unbuilt `rule add/accept/arrange`) move to Designed, not built; `source='gate'` | 25, 55, 67; ADR 64; `12-spec-refactoring-design-tree.md` |
| T6 | [WL-SPEC-78](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-78) Design queries, intents, decks, meetings, attachments | itself | nothing | 21, 26, 62, 64, 70 |
| T7 | [WL-SPEC-79](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-79) Knowledge graph and search | itself | ADR 49, 62 as current decisions; WL-SPEC-7 §1–4 if not present | 6, 40, 68, 69; ADR 49, 62 |
| T8 | [WL-SPEC-80](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-80) Agent harness and sessions | itself | nothing new: WL-SPEC-71 is already in §5.5 and §8.6–8.9 (built); ADR 51 already folded | 8, 12, 13, 20, 41, 71; ADR 51 |
| T9 | [WL-SPEC-81](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-81) CLI and skills | itself | nothing; turn the inline "Designed, not built:" table into the end section | 16, 19, 37, 61 |
| T10 | [WL-SPEC-82](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-82) Cockpit | itself | WL-SPEC-7 §5's read-only web views paragraph (the CLI part is in 81 §1–3); inbox rule for change reviews (C-rev-4) | 7, 32, 56, 65 |
| T11 | [WL-SPEC-83](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-83) Web app (accepted) | itself | nothing | — |
| T12 | [WL-SPEC-84](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-84) Review | itself | `PUT /reviews/{id}/guide` (plan 134), acceptance criteria as test facts or drop (K5) | 59, 60 |
| T13 | [WL-SPEC-85](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-85) Progress page | itself | fix `POST /progress/review` (no route); `ProjectProgress` lives in `internal/store/progress.go` | 66 |
| T14 | [WL-SPEC-86](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-86) Automation | itself | nothing; stays a draft, no edits (K7) | — |
| T15 | [WL-SPEC-72](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-72) Design authority gate | itself | verify against `internal/cmd/gate.go` (appendix B2) | — |

## 3. Disposition table

The 661-row section-level table is `docs/specs2/section-map.tsv` plus
`docs/specs2/ttl/residue.tsv`; it was executed on 2026-09-23 and is not
repeated here. This table covers only what that run did not.

| Source | Destination | Disposition |
|---|---|---|
| WL-SPEC-1 §9, 9.1–9.5a (GitHub linking, 11 live rules) | T2 §(see C-id-1) | move where missing, else supersede |
| WL-SPEC-1 §0, §1 (Purpose, Motivation), §13 Open questions | — | drop (history) |
| WL-SPEC-7 §5 Overview / CLI surface | T9 §1–3 (CLI part, present); T10 (web views paragraph, missing) | supersede WL-REQ-500; delete orphan WL-REQ-370 (no arrangement) |
| WL-SPEC-7 §8 Acceptance criteria | — | drop |
| WL-SPEC-59, 60 (all sections, already withdrawn into 82 §13–14) | T12 | re-point plans 132–135 `covers` to T12 anchors; rules already withdrawn |
| WL-SPEC-66 (all, withdrawn into 82 §15) | T13 | re-point plans 136–139 `covers` to T13 anchors |
| WL-SPEC-71 §1–7 | T8 §5.5, §8.6–8.9 | already present; supersede 71's rules, retire 71 |
| ADR 36 One model across packages | T1 §3.2 | rewrite as current decision (no normative home today; 81 §5 points at a 73 section that does not say it) |
| ADR 43, 47, 48, 50 (secrets) | T2 | already folded per README; verify, then retire |
| ADR 49, 62 (graph) | T7 | already folded; verify, retire |
| ADR 51 (Codex/Amp bindings) | T8 | already folded; verify, retire |
| ADR 64 (folded numbers resolve to successor) | T5 | state as current rule of `lode show`; retire |
| `docs/specs2/12-spec-refactoring-design-tree.md` S1–S64 | T5, T15 | move undescribed decisions (appendix B4); delete file |
| `docs/specs2/ttl/*` | — | delete (proposal graph, nothing in `ns/` derives from it, K6) |
| `docs/specs2/01..11*.md`, README, section-map.tsv | — | delete in the cleanup PR; backbone is the source |
| "Sources" sections in 72..82 | — | drop (history) |
| Amendment/migration prose in 72..82 (appendix A) | — | rewrite as current state |
| "Open questions" sections in 72..82 | per spec | decided items with a task move to "Designed, not built"; undecided stay as open questions (X4) |

## 4. Cross-cutting decisions

- **X1 Where specs live.** The backbone only. `docs/specs2/` is deleted in the phase 7 PR; `docs/` keeps an index page listing the 15 specs.
- **X2 Granularity.** Fifteen specs as listed. 84, 85, 86 stay separate from 82 (already cut that way in the backbone).
- **X3 Which text wins.** The backbone body of 72..86 wins over the git files and over every old spec. Code wins over both, except where the owner marks a code bug (task filed).
- **X4 Open questions.** Each target keeps one "Open questions" section for undecided items and gains one "Designed, not built" section at the end. A decided item moves from the former to the latter with its task.
- **X5 Stale tasks.** Decompose tasks for old specs (WL-468 for 061, WL-758 for 004, WL-770 for 032, WL-771 for 067, WL-772 for 025) are abandoned; WL-924 (decompose 75) and WL-940 (decompose 83) stay. Review tasks for old docs (WL-861 for 71) are abandoned once the content is in its target.
- **X6 Plans.** A plan's `covers` edges target rules, not sections (a rule can be arranged in more than one spec). Where the 2026-09-23 run missed them (59, 60, 66, 7 §5, 1 §9.x), the old rules are superseded with `lode rule supersede --map` so the plan → rule edges follow to the successor rules; `lode doc resolve` catches entries stored unresolved. Draft plans named in old specs (1, 3, 4, 6, 8, 9, 11, 54–56, 72, 84, 91, 116–126, 132–135, 140–143, 146–148) are the "Designed, not built" rows of their target.
- **X7 Code leftovers.** Code comments citing old spec numbers (`WL-SPEC-66 §…` in `internal/progress`, `025 §7.3` across `internal/`) are one chore task, not spec text and not part of the cleanup PR.
- **X8 Order.** Wave 1: T3, T11 (accepted; only re-link and retire). Wave 2: T12, T13 (plans point at old docs; unblock review work), T1, T2, T5, T15 (merges). Wave 3: T4, T6, T7, T8, T9, T10, T14 (edit, accept, retire).
- **X9 Acceptance.** Accepting a target is the owner's act (`lode doc accept`); agents prepare the body and report. A target with a pending web-only approval is flagged, not forced.

## 5. Owner questions

Each paragraph is one question. Recommendation first.

**K1 Accept the twelve draft targets as they stand after the fix-up, or review each?** Recommend: accept 73, 74, 76, 79, 80, 81, 82 after the agent pass (they changed little since 09-18); the owner reads 77, 78 (heaviest rewrite), 84, 85, 86, 72 before accepting.

**Answer:** agreed.

**K2 WL-SPEC-71 (telemetry) has a pending approval (717) and a review task WL-861, but its content is already in WL-SPEC-80 §5.5 and §8.6–8.9 and built.** Recommend: re-target WL-861 to review 80 §8.6–8.9, supersede 71's 8 rules into those sections, delete the draft (approval 717 lapses).

**Answer:** agreed.

**K3 ADR 36 (one model across packages) has no section anchors and no rules, and no target states the rule.** Recommend: one subsection in T1 §3.2 (dependency boundaries), then delete the ADR. Code already cites it (`internal/api/gate.go:73`); that comment joins the X7 chore.

**Answer:** agreed.

**K4 Plans 132–135 (review, drafts, approvals 741–744 pending) use WL-SPEC-59's verb names; T12 renamed them (open → add, desk → exec, comment → note, approve → set verdict, await → listen, ingest → import).** Recommend: T12's names stand (they follow WL-SPEC-61's rules). The plans are drafts: revise them to the new names and re-point their covers, or withdraw and re-plan from T12. Owner picks.

**Answer:** T12's names stand with three exceptions: keep `comment` (not `note`), keep `approve` (not `set verdict approved`), and `await` becomes `wait` (not `listen`). T12 and plans 132–135 are edited to that set; `comment`, `approve` and `wait` join the WL-SPEC-61 verb allowlist in T9.

**K5 Acceptance criteria and testing sections were dropped from every fold (59 §15, 60 §13, 66 §8 are the ones a plan still cites).** Recommend: keep them dropped; a criterion that is a behavior becomes a sentence in the relevant section, the rest is test code.

**Answer:** agreed.

**K6 Delete `docs/specs2/ttl/` (proposal graph) and `12-spec-refactoring-design-tree.md`?** Recommend: yes, after B3's 13 undescribed decisions are written into T5/T15. The ttl files use retired `wl:Clause` terms and nothing in Go or `ns/` reads them; `ns/rule.ttl` stays (it is in `ns/`, status unstable, separate question).

**Answer:** agreed.

**K7 WL-SPEC-86 Automation has no code behind it except the human-only flag.** Recommend: keep as a spec, whole body under "Designed, not built" with one task to plan it. Alternative: delete and keep the idea in an open question of T10.

**Answer:** keep 86 as a draft spec, untouched: a draft needs no "Designed, not built" section, and no planning task is filed. 86 is excluded from K1's acceptance.

**K8 Who retires.** `lode doc withdraw` on 11 accepted old specs needs a justification each; `lode doc delete` on 35 drafts and 9 ADRs. Recommend: the coordinator runs both after phase 6, with justification "Folded into WL-SPEC-nn on 2026-09-23; see docs/spec-consolidation-map.md".

**Answer:** agreed.

**K9 WL-SPEC-85 lists `POST /projects/{id}/progress/review` which has no route; WL-SPEC-84 lacks `PUT /reviews/{id}/guide` which plan 134 builds.** Recommend: both are spec text following plans, keep in "Designed, not built" of 85/84 respectively.

**Answer (owner asked whether both are needed):** no. `POST /projects/{id}/progress/review` only opens a WL-SPEC-84 document review (85 §4); the page can call 84's own review-create route, so the progress route is dropped from 85. `PUT /reviews/{id}/guide` is the only path by which the author's guide proposal reaches the backbone (84 §6 says the guide is built "from the author's proposal plus policy" but names no write); T12 adds it, as plan 134 builds it.

**K10 Open design tasks for the old corpus: WL-468, 758, 770, 771, 772 (decompose old specs), WL-839 (plan 66 overtaken), WL-874 (accept 57 + plan 121).** Recommend: abandon the five decompose tasks (their specs are retired; 75 has WL-924); keep WL-839 and WL-874 but re-target 874 to T4.

**Answer:** agreed.

## Appendix A: flat-state audit of 72..82

Full per-section list with anchors and snippets: scratchpad `audit-flat-state.md`
(to be attached to the phase 3 brief). Summary; counts are regex upper bounds,
"amend/supersede" in 73 §2 and 77 are domain terms and stay.

| Spec | history hits | old-spec citations | open questions | not-built candidates |
|---|---|---|---|---|
| 72 | 10 | 5 (`WL-SPEC-4 sec-5` as an example trailer) | 3 | 4 |
| 73 | 17 (mostly domain) | 2 | 6 | 0 |
| 74 | 1 | 1 | 4 | 0 |
| 75 | 3 | 8 | 6 | 7 |
| 76 | 8 | 1 | 3 | 0 |
| 77 | 74 | 10 | 7 | 6 |
| 78 | 30 | 10 | 10 | 20 |
| 79 | 11 | 7 | 8 | 5 |
| 80 | 3 | 1 | 6 | 5 |
| 81 | 5 | 4 | 5 | 4 |
| 82 | 2 | 7 | 2 | 3 |

All eleven have a "Sources" section (drop). None has "Designed, not built"
(81 has an inline table with that label).

## Appendix B: per-area findings

### B1 Review, Progress, Automation (T12, T13, T14)

- C-rev-1 82 no longer duplicates 84/85/86; it points at them. Git `10-cockpit.md` still has the monolith (§10, §13–16) and a Drift §16 that the backbone's 82 lacks.
- C-rev-2 84 covers 59/60 content built by plans 132–135 except: acceptance/testing sections, 59 §3.1 escalation ladder (one sentence), `PUT /reviews/{id}/guide`. Nothing of 84 is built (`review_threads`, `review_rounds`, `internal/review` absent).
- C-rev-3 84 renames verbs relative to plans 132–135 (K4).
- C-rev-4 82 §12 inbox reads only PR approvals; change reviews from 84 would not appear. Open point for T10/T12.
- C-rev-5 82 open question 1 (author-submitted round vs `review_rounds.verdict`) is unresolved in 84 §3/§5.
- C-prog-1 85 matches the built code in 9 of 10 spot checks (`internal/progress/progress.go:91-150`, `internal/api/webform.go:666-678`, `internal/api/router.go:98-121`, `internal/api/eventstream.go:43`). Mismatches: `POST .../progress/review` has no route (K9); `ProjectProgress` is in `internal/store/progress.go:47`, not `docplanning.go`; code accepts plan state `spent`, 85 does not mention it.
- C-prog-2 Code comments cite `WL-SPEC-66 §…` (`internal/progress/position.go:55`, `internal/api/progress.go:1`); chore (X7).
- C-auto-1 86 is design only. Only `HumanOnly` (`internal/model/task.go:52`) and the `lode-worker` agent exist (K7).

### B2 Residue specs (WL-SPEC-1 §9, 7, 71, 72, ADR 36, 64)

- C-id-1 All 11 live WL-SPEC-1 rules are present in 74 (§1.1, 4.2–4.3, 9–9.5) except 397 Testing and 399 Open questions (already superseded by WL-REQ-76). Supersede the nine, drop the two.
- C-id-2 74 §9.2 says `lode login` links GitHub and `lode actor show` shows link state; WL-SPEC-1 said `lode auth login/status`. Code has neither an `auth` group nor `actor show` (`internal/cmd/login.go:19`, `admin.go:15`). 74's wording stands (61 naming); the commands are "Designed, not built".
- C-id-3 GitHub user linking is mostly unbuilt: no `/auth/github/link` or `/callback` routes, `github_user_tokens` has only `actor_id, ciphertext, updated_at` (`deploy/base/migrations/0001_baseline.up.sql:29`) against 74 §9.3's columns. Section 9.3 moves to Designed, not built with WL-PLAN-52/53 (accepted) as the task source. Q-id-1: plans 52/53 are accepted but the code is absent; check their tasks before calling it unbuilt.
- C-id-4 74 §9 says `expected_github_login`; migration 0093 renamed it `github_username`, `internal/oidc/oidc.go:24` reads `githubUsername`. Stale text, fix in T2.
- C-drift-1 WL-SPEC-7 §5's CLI content is in 81 §1–3 as the built commands (`internal/cmd/graph.go:258-391`, `project.go:795`, `overview.go:14`); the top-level `lode drift/gaps/specs/ready` spellings are gone. The read-only web views paragraph is in neither 81 nor 82. WL-REQ-370 "Surface" is a draft rule with no arrangement created 2026-09-23 (a fold leftover): delete.
- C-drift-2 WL-SPEC-7 §8 is Oxigraph-era acceptance criteria; drop.
- C-tel-1 WL-SPEC-71 is fully present in 80 §5.5, §8.6–8.9 and built (`internal/api/server.go:884`, `internal/otlp/`, `internal/harness/claudecode.go:356`). Its text still cites "spec 082 §2.2", "spec 066 §5.1" (pre-fold). See K2.
- C-gate-1 WL-SPEC-72 holds in 6 of 9 spot checks (`internal/cmd/gate.go:26-109`, `internal/gate/trailer.go:14-82`, `internal/api/gate.go:114-166`). Stale: trailer examples say `WL-RULE-456` (rules print `WL-REQ-n` since #746, `WL-RULE` still parses); file names `11-design-authority-gate.md` etc. in prose. Stale as "in force": the `[gate]` guarded-path table is not configured in this repo (`.github/workflows/pr-checks.yml:238-241` says so), so CI runs the check as a no-op.
- C-gate-2 Code gaps (tasks): push-event path reads the task from the branch only, no `Worklode-Task:` body fallback (`internal/api/gate.go:96`); `lode doctor` prints "PR requirement not checked" where 72 §3 says it warns (`internal/cmd/doctor.go:190-204`); idempotency key includes the `amended` qualifier so `Spec: X` then `Spec: X amended` emit two `task.governed` events (`gate.go:137`), harmless if `Govern` is idempotent.
- C-adr-1 ADR 36: no target states the one-model rule normatively; 81 §5 points at a 73 section that does not contain it; 82 §3 states the `internal/ui` dependency only. See K3.
- C-adr-2 ADR 64 (folded numbers resolve to successor, never re-imported): not stated in any target. Belongs in T5 next to `lode show` resolution.

### B3 Rule model vs `12-spec-refactoring-design-tree.md`

Full S1–S64 table: scratchpad `audit-rule-model.md` (attach to the T5/T15 brief).

- C-rule-1 About 54 of 64 settled decisions are built; S11, S13, S14, S29 partly; S16, S42, S44 were overtaken by the plan-covers-rules model (migration 0098 retired coverage levels); S61 deferred on purpose; S9 renamed by S64.
- C-rule-2 Not described in 77 or 72: S3 (who may change governing links, events), S7 (embedding context-token ceiling), S17/S41 (one model for all project sizes), S31 (re-accepting a plan), S32 (cross-project governing links), S34 (governance changes not in timeline), S36 (canonical rule URL), S38 (how `references` edges derive), S43 (closing a plan links its ungoverned tasks), S58 (`task.governance_superseded` event), S60 (`ResolvesTo`), S62 (supersession generator), S63 (`/projects/<proj>/<kind>/<n>` URLs). Thin: S39, S40, S48, S53, S59. T5 writes the built ones in as current state.
- C-rule-3 77 §3 says `task_governed_by.source` is `plan` or `manual`; code also has `gate` (migration 0087, `internal/store/governedby.go:27`). Stale.
- C-rule-4 77 §18–19 (`lode rule add/accept/arrange/unarrange`, spec body rendered from rules with empty `docs.body`, `--editable` form, §19.7 migration) are unbuilt (`internal/cmd/rule.go` lacks them; `store/rules.go:527` still regenerates a stored body; `rules.go:559-562` refuses edits on a rule in zero or several specs where §19.4 allows them). These become T5's "Designed, not built" rows; a task exists? Q-rule-1: none found by title, file one planning task if the owner keeps the design.
- C-rule-5 `ns/rule.ttl` is still a proposal (`vs:term_status "unstable"`); `wl:Rule`, `ruleKind`, `refines/constrains/conflictsWith/references` and `dct:replaces` moved into `ns/ontology.ttl` (line 383). Out of scope here beyond K6.
- Correction to the agent's claim that the A2 import is unrun: it ran 2026-09-23 (commit f84f1bcc, 895 withdrawn rules). `supersession.ttl` and `residue.tsv` are therefore disposable with the rest of `ttl/`.

## Appendix C: tasks to file or change (running list)

| Code | Action | Source |
|---|---|---|
| A1 | chore: code comments citing old spec numbers (`WL-SPEC-66 §…`, `025 §7.3`, `ADR 036 §2`) point at the successor sections | X7, C-prog-2, K3 |
| A2 | downgraded to question H3: push has no PR body; the PR-body fallback is built | T15 |
| A3 | filed as [WL-948](https://worklode.dev.sunstoneinstitute.ai/WL-948) | C-gate-2 |
| A4 | question H2 (owner); 72 now reads as opt-in | C-gate-1 |
| A12 | filed as [WL-949](https://worklode.dev.sunstoneinstitute.ai/WL-949): gate idempotency key qualifier; pr-checks docs-only comment | T15 |
| A5 | chore: delete orphan rule WL-REQ-370 | C-drift-1 |
| A6 | design: plan 77 §18–19 rule editing commands, if kept | C-rule-4 |
| A7 | abandon WL-468, 758, 770, 771, 772 | X5 |
| A8 | supersede 59/60/66 residue so plans 132–135, 136–139 follow; revise 132–135 to the K4 verb set | K4, X6 |
| A10 | T9: add `comment`, `approve`, `wait` to the verb allowlist; T12: rename back per K4 | K4 |
| A11 | T13: drop `POST /projects/{id}/progress/review`; T12: add `PUT /reviews/{id}/guide` | K9 |
| A9 | re-target WL-861 to 80 §8.6–8.9 | K2 |
