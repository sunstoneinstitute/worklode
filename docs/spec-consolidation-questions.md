# Spec consolidation: owner questions (phase 4)

One paragraph per question. Each ends with a recommendation. A question with no comment takes the recommendation. Codes are stable; the map (`docs/spec-consolidation-map.md`) has the findings behind each.

State after wave 2 and 3: all thirteen draft targets (WL-SPEC-72, 73, 74, 76, 77, 78, 79, 80, 81, 82, 84, 85 and the two plan sets 132–139) are rewritten to flat current state with a "Designed, not built" section, verified against code. Twelve leftovers are filed (WL-948 to WL-953, WL-957 to WL-963). Nothing is accepted or retired yet.

## Design points

D1 (T15 Q3) 72 §5 condition 4 restricts `none tests|build|copy` to matching paths, undefined for `copy`. Recommend: drop the restriction for `copy`; define `tests` as `_test.go`/`testdata/` and `build` as `Makefile`, `go.mod`/`go.sum`, `Dockerfile*`, `.github/**`.

D2 (T15 Q4) "Plans are never back-patched" is guidance the code does not enforce; plans stay mutable and are re-planned by edit and re-accept ([WL-SPEC-77](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-77) §11). Recommend: keep it as guidance (as 72 now reads), do not make minted plans read-only.

D3 74 §9.5 (agent sessions acting on GitHub, ex [WL-REQ-1293](https://worklode.dev.sunstoneinstitute.ai/WL-REQ-1293)) has no plan and no code. Recommend: file one plan after [WL-PLAN-52](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-52) lands (needs `store.UserToken`); the not-built row says "no plan yet" until then.

D4 "A task-scoped token reads only within its task's project" had no code (`taskScopeAny` routes pass, `internal/api/authz.go:571`); removed from 74 §2. Recommend: drop the requirement. If wanted, it is a `requireTaskScope` check plus a task.

D5 Should `withdrawn` and `stale` plans count toward progress? Today both are derived as if accepted (`internal/progress/progress.go:123`, `internal/store/progress.go:156`). Recommend: exclude `withdrawn`, treat `stale` as accepted; write the rule into 85 §2 and file a bug.

D6 Should sections whose only rules are informative count as owed on the Progress page? Today every section is owed (`progress.go:229`), so each spec's "Designed, not built" section shows as `unplanned`. Recommend: no; skip informative-only sections, file a bug, and the fix-up pass sets every `sec-not-built` rule to informative.

D7 `--focused` placement: K4 put it on `set guide`; plan 134 makes `focused` the reviewer's declaration on its round, and an author narrowing the diff of their own work breaks the honesty rule. 84 now has `--focused` on `guide` (view), `approve` and `set verdict`. Recommend: keep that.

D8 "A document goes `in_review` on the first reviewer GET" had no plan; plan 132 declines it (no status value exists). Removed from 84. Recommend: drop it; the minted review task already shows review work is assigned.

D9 `lode review submit` (the author's round) is not in the K4 list but plans 132/133 build it and 84 §7 needs the act. Recommend: accept the verb (T9 told to allowlist it).

D10 No plan builds 84 §8 (who opens a change review: the worker skill floor list) or §11 (reading diffs, ex [WL-SPEC-60](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-60)). Recommend: a skill task after plan 133 for §8; for §11 either a plan or move it to Open questions.

D11 (N1) The close verb. 75 §3.2 (accepted) and 81 use `lode task set state merged`; `close` is not an allowed L3 verb; [WL-PLAN-121](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-121) still says `lode task done`. Recommend: the done check runs on `lode task set state merged`, no new verb (76 is written that way); revise plan 121 to match.

D12 76 §6 (provenance column) and §7 (verification tasks for bugs) have no plan and no code; the motivating bug [WL-371](https://worklode.dev.sunstoneinstitute.ai/WL-371) is deployed. Recommend: move both to §12 Deferred rather than keep them as unplanned requirements.

D13 Naming: 76 §10 says "automation" (`internal/automations`, `wl:AutomationFired`) but §10.5/§11 and [WL-PLAN-11](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-11) still say "rule" (`LODE_RULE_LLM_*`, `worklode_rule_*`, `internal/workflowrules`). "Automation" also collides with [WL-SPEC-86](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-86)'s title. Recommend: rename to automation in 76 now; revise plan 11 when next touched.

D14 Design-doc skill pins (`skills:` frontmatter → `wl:recommendsSkill`, brief "governing-design pins", `wl:Skill` projection) from old 016 have no code (`internal/store/brief.go:38` reserved nil) and no plan; removed from 81. Recommend: drop; task pins plus the plan-task `skills:` key cover the need.

D15 (replaces Q-rule-1) [WL-PLAN-146](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-146) "Rules are the record" (draft) covers 77 §19–19.7, which T5 stubbed into "Designed, not built" rows; the plan's "Read first" points at the now-stubbed §19. The original text is backed up at scratchpad `wl-spec-77-sec19-backup.md`. Recommend: keep the rows and restore the §19 design text under the stubs (a decided design with a plan may stay in the body as long as it is marked as not built), then plan 146 reads as before. Alternative: withdraw 146 and delete the rows.

D16 `lode doc coverage` is unbuilt with no plan; left out of 77. Recommend: drop it (`doc progress` and `graph gaps` answer it).

D17 Intents (§5), decision decks (§6), meetings/minutes/notes (§7): nothing built, no plan, no task. Recommend: keep the design text in 78 (it is the only survivor of 62/64/70) and file one design task per area.

D18 `--strict-refs` and `implements.yaml`/`lode doc coverage`: designed, unbuilt, unplanned. Recommend: drop `--strict-refs`; `implements` stays a 77 §13 matter.

D19 [WL-SPEC-68](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-68) (instance-scoped IRIs, content negotiation, instance shapes) is unbuilt with no plan; review task [WL-801](https://worklode.dev.sunstoneinstitute.ai/WL-801) is in_review. Recommend: drop the design, abandon [WL-801](https://worklode.dev.sunstoneinstitute.ai/WL-801), retire 68. Canonical URLs ([WL-SPEC-83](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-83)) already give entities reachable addresses.

D20 [WL-SPEC-69](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-69) (bf16 embedding trial): nothing built, no plan; [WL-PLAN-144](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-144) already addressed query latency. Recommend: drop and retire 69. 79 §18 is now a stub.

D21 Publishing the `wl:` vocabulary to rdf-registry is not done (no `rdf/wl/` there). Recommend: file a task in rdf-registry or drop the goal; left out of 79.

D22 `wl:Issue`, `wl:PullRequest`, `wl:mirrors` are declared but not projected and no plan builds them. Recommend: keep the terms, no row.

D23 Which document IRI wins ([WL-961](https://worklode.dev.sunstoneinstitute.ai/WL-961) filed): recommend the project-qualified `doc/<kind>-<project>-<nnn>`.

D24 `ns/rule.ttl`: recommend 77 owns the rule vocabulary's meaning; move `Arrangement`, `arranges`, `position` into `ontology.ttl`, delete the fold-tooling terms and the file with K6.

D25 Cursor and opencode adapters: no plan builds them, [WL-PLAN-63](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-63) decided against them for v1; left out of 80. Recommend: drop for good.

D26 `worktree-create` and `worktree-remove` hook events exist but no adapter binds them, so an abandoned worktree's lease ends only at sweeper expiry. Recommend: accept as the design (80 now reads that way).

D27 Brief fields `governing_design`, `affected_components`, `definition_of_done` are always null (`internal/model/brief.go`). Recommend: drop them from the wire type (one chore), unless a plan will fill them.

D28 Jump box (82 §2.4): not built, no plan; text kept with a "no plan yet" row. Recommend: file one plan task or delete §2.4.

D29 Fact-selected panels (82 §6, ex [WL-SPEC-65](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-65)): not built, no plan; 82 is now the only copy. Recommend: keep the design in 82 and file a plan task; 65 can then be deleted.

D30 Definition-of-done card and lane-specific review views (analysis, methodology, report): no plan. Recommend: fold the review views into [WL-PLAN-100](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-100)'s scope; drop the done card in favour of the Deliverables page.

D31 Change reviews in the inbox: plans 132/133 never mention the inbox; 82 §12 now says 84 change reviews do not appear. Recommend: add an inbox bucket over `task_reviewers`/`reviews` to [WL-PLAN-133](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-133).

D32 Per-section coverage badges on the document view (ex 7 §5): not built, no task. Recommend: drop; the Progress page covers it.

## Housekeeping (plans, tasks, approvals)

H1 (T15 Q1) No task exists for [WL-SPEC-72](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-72)'s "Designed, not built" rows (doctor warning = [WL-948](https://worklode.dev.sunstoneinstitute.ai/WL-948) filed; CI conditions 1–3 server-resolved, PR comment, `spec_gate` event). Recommend: file one planning task "gate: server-resolved CI check" for the rest.

H2 (A4) `.worklode/config.toml` has no `[gate]` table, so the spec gate is a no-op in Worklode's own repo. Recommend: add the table with the path list 72 §4 recommends, as one chore.

H3 (A2, downgraded) Push events have no `Worklode-Task:` fallback; 72 and 75 §9.6a promise only the PR-body fallback, which is built (`internal/api/gate.go:90-92`). Recommend: drop A2; optionally file an enhancement to read the newest commit's trailer on push.

H4 [WL-PLAN-9](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-9) (overhead cost) is a draft whose content is fully built (migration 0048, `ReportProjectSessionUsage`, `ProjectCost`, cockpit line). Recommend: accept it so its state becomes spent, or withdraw it; a stale draft keeps dead `covers` on 73 §7.4–9.

H5 [WL-PLAN-1](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-1) (sandbox) and [WL-PLAN-4](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-4) (admin prod) are drafts from 2026-08-21 with no tasks. Recommend: keep them as the builders named in 73's "Designed, not built"; accept when scheduled.

H6 [WL-PLAN-53](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-53) is titled "the `lode auth` command group" but 74 (per [WL-SPEC-61](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-61)) keeps `lode login` and `lode actor show`. Recommend: revise plan 53 to 74's names before tasks are minted (it has none).

H7 No plan or task rewires the Review button ([WL-951](https://worklode.dev.sunstoneinstitute.ai/WL-951) filed). Recommend: attach [WL-951](https://worklode.dev.sunstoneinstitute.ai/WL-951) to [WL-PLAN-132](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-132).

H8 Plan 135 says `lode doc export --anchors`; 84 says `lode doc show --anchors`. Recommend: rename in plan 135.

H9 Approvals 741–744 (plans 132–135) are pinned to subject revision 1; the re-pointing moved the plans to v23/v19/v14/v10. They need re-requesting in the web UI at the current versions (owner).

H10 [WL-874](https://worklode.dev.sunstoneinstitute.ai/WL-874) can run against 76 now, but [WL-PLAN-121](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-121) needs revising first: `lode task done` → manual close, and its covers anchors still point at the old 57 layout. [WL-PLAN-8](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-8) cites "migration 0046" (stale). Recommend: fold the plan revision into [WL-874](https://worklode.dev.sunstoneinstitute.ai/WL-874)'s scope.

H11 Superseding [WL-REQ-500](https://worklode.dev.sunstoneinstitute.ai/WL-REQ-500) flipped accepted plans [WL-PLAN-32](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-32), 33, 34 to `stale`. Recommend: accept; they are built history (or mark spent if the state exists for them).

H12 [WL-PLAN-122](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-122) still spells `lode specs` / `lode drift --docs`. Recommend: revise to `graph drift --docs` and `graph gaps --unimplemented` when it is next touched.

H13 The `adr` kind is still creatable (migration 0027 CHECK, `lode doc add --kind adr`) while the spec calls it retired. Recommend: after K8 deletes the nine ADRs, one chore removes `adr` from `docKinds`, the CHECK, `wl:ADR` and `--bare-superseded` (same as T6 Q3).

H14 [WL-PLAN-6](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-6) "Documents leave the tree" says "spent, do not accept" and is all built or overtaken. Recommend: delete the draft.

H15 Plans 54, 55, 56, 72 (design-doc queries, drafts) target the old file corpus (secfmt/secfrozen scripts, section-level `replaces`, retired coverage levels). Recommend: withdraw 56 and 72; rewrite 54 and 55 against rule edges or withdraw them; the not-built rows citing them then need new plans or move to Open questions.

H16 [WL-PLAN-123](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-123) names `lode decompose`; the command surface says `lode doc decompose`. Recommend: fix when the plan is accepted.

H17 [WL-PLAN-37](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-37) is superseded but still has `covers` edges into 79 §1–13. Coordinator cleanup in phase 7 (unlink or leave; superseded plans are hidden).

H18 [WL-PLAN-65](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-65) (status line and cost spool) is accepted but the cost spool no longer exists. Recommend: leave as history.

H19 [WL-PLAN-125](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-125) (Pi) is a draft with no tasks; the only 80 not-built row. Recommend: keep.

## Cross-target fix-ups (no decision needed unless you object)

These are what the fix-up pass (phase 5) will do. WL-SPEC-75 and 83 are accepted, so their edits go through `lode doc revise`.

X-75-1 [WL-SPEC-75](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-75) §9.6a (accepted) still uses `WL-RULE-<n>` and `[WL-SPEC-4](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-4) sec-5` examples; needs `lode doc revise`. (from T15)

X-75-2 [WL-SPEC-75](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-75) §13.8 is cited by 74 for crew-chat invite by email; T3 confirms it is built. (from T2)

X-80-1 [WL-SPEC-80](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-80) §8.6 should state that Claude Code's OTel log events go straight to the server's `/otlp/v1/logs` (`internal/harness/claudecode.go:338-364`); 73 §7.3 points there. (from T1; give to T8)

X-82-1 `beginJSONPost` is cited by 74 as [WL-SPEC-82](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-82); if 85 describes it, repoint. (from T2; give to T10/T13)

X-75-3 [WL-SPEC-75](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-75) §3.2 and §13.3 still name `04-done-verification-workflows.md`; should say [WL-SPEC-76](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-76). (from T4; revise with X-75-1/2)

X-81-1 [WL-SPEC-81](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-81) not-built table should list the unbuilt `lode project workflow`, `lode project set workflow --file [--dry-run]`, `lode project set default-deliverables`, `lode task edit --deliverable*/--workflow`. (from T4; give to T9 if still running, else fix-up)

X-84-1 [WL-PLAN-135](https://worklode.dev.sunstoneinstitute.ai/WL-PLAN-135) builds `lode doc export`, which 84 no longer mentions (84 says `lode doc show --anchors`); same as H8. (fix-up)

X-79-1 [WL-SPEC-79](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-79) should own the query semantics behind `graph drift --docs` and `graph gaps --unimplemented`. (give to T7 if running, else fix-up)

X-75-4 [WL-SPEC-75](https://worklode.dev.sunstoneinstitute.ai/WL-SPEC-75) §4 shows `task_edges` CHECK with `dependsOn`; code has `blocks` (migration 0049); [WL-908](https://worklode.dev.sunstoneinstitute.ai/WL-908) is the task. (T5)

X-75-5 77 §15 (commit horizon, subscribers, doc-lifecycle) duplicates 75 §9.3–9.6; one should own it. Recommend 75 owns, 77 points. (T5)

X-78-1 78 §1 cites `lode doc coverage` "defined in 05-documents.md"; T6 says removed, verify. 78 §4.6 ontology rows (`wl:cutFrom`, `wl:produces`, `wl:implements`, `wl:status`) are 79 vocabulary. (T6/T7)

X-79-2 79 §10.2 carries "Follow-up (12-spec-refactoring-design-tree.md S20, A3, S63)" prose. (T5; give to T7)

X-82-2 Blob CSP/same-origin rules in 78 overlap 82. (T6)

X-81-2 81 should hold the full plugin skill list (`add`, `plan-spec`, `start-agent-loop`, …); 80 §7 points there. (fix-up)

## Record (no action)

X7 additions: `CLAUDE.md:164` (`deps_test.go` wording, "amendment to 036"), `.worklode/Dockerfile:7-11` ("spec 038 §3.1"), `internal/model/modelrule_test.go` and `deps_test.go:15` ("ADR 036"), `internal/hookrun/hookrun.go:479` (017 §3 purge comment describes unbuilt behaviour), `internal/oidc/oidc.go:21`, `internal/api/authz.go:51,132`, `internal/secrets/env.go:13,33,61,96`, `lode doc show` draft banner "(025 §7)".

Tooling: `lode doc link --covers` works on accepted plans (bumps the version, no re-acceptance), so covers re-pointing needs no supersession. Plans 136–139 now also cover 85 sec-1..8; their 66 covers remain and go away when 66 is retired.

Fix-up: duplicate rules [WL-RULE-1314](https://worklode.dev.sunstoneinstitute.ai/WL-RULE-1314) and [WL-RULE-1325](https://worklode.dev.sunstoneinstitute.ai/WL-RULE-1325) ("Who opens a change review") in 84; `lode doc note` storage moving to `review_threads` is a 77 note (T5 absent; fix-up).

Tooling: `lode doc link/unlink --covers X#sec-N` acts on the whole subtree; a link to an invariant/informative section stores no edge; every link/unlink bumps the plan version.

X7 additions: `internal/store/brief.go:20,40`, `internal/designdoc/coverage.go:416`, `internal/store/tasks.go:156,1276`, `internal/store/hierarchy_resolve.go:17`, `internal/watcher/metrics.go:24,30`.

Filed: [WL-953](https://worklode.dev.sunstoneinstitute.ai/WL-953) (internal/cmd/CLAUDE.md drift).

X7 additions: `internal/cmd/namerule_test.go:10,41,66,114`, `internal/cmd/project.go:792`, `internal/cli/scope.go:34`, `internal/skillstore/skillstore.go:13,46,61`, `internal/cmd/graph.go:362` (help banner), `internal/cmd/secrets.go:26`, `internal/cmd/show.go:216`.

Filed: [WL-958](https://worklode.dev.sunstoneinstitute.ai/WL-958) (draft edit keeps version, no snapshot).

Filed: [WL-959](https://worklode.dev.sunstoneinstitute.ai/WL-959) (spent plans in todo/coverage), [WL-960](https://worklode.dev.sunstoneinstitute.ai/WL-960) (wl:covers range).

X7 additions: `internal/store/docplanning.go:18` (comment says draft, tasks mint ready), `internal/designdoc/*.go`, `internal/api/blobs.go`, `internal/cmd/doctodo.go:30`.

X7 additions: `internal/kg/iri/iri.go:1-4,15`, `internal/projector/projector.go:2,44`, `internal/corpusindex/chunk.go:2,16`, `internal/indexer/metrics.go:35`, `internal/api/server.go:156,162,1225`, `ns/ontology.ttl`, `ns/shapes.ttl` (many).

X7 additions: about 124 Go comment citations of 008/012/013/071/ADR 051 (`internal/harness/harness.go:18,97`, `internal/hookrun/otelheaders.go:12`, `internal/api/render.go:268,292`, `internal/api/metrics.go:279`, `internal/store/activity.go:114`, …); `internal/store/reconcile.go:1` cites a dead path; `internal/hookrun/hookrun.go:225` says `lode hook --list` (shim removed).

Filed: [WL-962](https://worklode.dev.sunstoneinstitute.ai/WL-962) (SB avatar), [WL-963](https://worklode.dev.sunstoneinstitute.ai/WL-963) (user-visible old-spec citations).

X7 additions: `internal/api/web.go:394,445,459,537-556`, `internal/api/server.go:727-730,782`, `internal/ui/layout.templ:5-29,175-184,305-320`, `internal/api/inbox.go:22,82,96,103`, `internal/store/approvals.go:1056,1083,1110`, `internal/api/webform.go:188,625,636`, `internal/api/cockpit.go:46-50`, `internal/ui/drift.templ:3,8`, `internal/ui/crew.templ:2-3`.
