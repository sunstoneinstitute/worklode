# Standalone `coverage: none` entries: classification for review

WL-928. Data for WL-929 is in `coverage-none-classification.tsv` (same directory). Classes follow WL-SPEC-77 §4 and WL-SPEC-78 §4.1:

- **N1**: rationale or context. WL-929 marks the rules `informative` and drops the edge.
- **N2**: binds the plan's work, not delivered by it. WL-929 adds `governedBy` links on the plan's minted tasks and drops the edge.
- **unsure**: not decided. Stig decides before WL-929 starts.

Counts (store read 2026-09-27): 276 entries, 195 N1, 65 N2, 16 unsure. They resolve to 173 distinct rules. 11 N3 entries are excluded and listed at the end.

## Notes for WL-929

- **F1. WL-PLAN-106's 127 standalone entries no longer have edges.** Version 7 (2026-09-26) dropped every `none` entry from the plan. The TSV lists them from version 6 with rules resolved against the current arrangement, so WL-929 can still mark them informative. Deleting their edges is a no-op.
- **F2. Every rule in this file is `withdrawn`.** The spec refactor superseded them all. 120 have one successor, 3 have several, 50 have none. The 75 successors merge predecessors of different classes (50 of them) or predecessors outside this set (48), so marking a successor informative because one predecessor is N1 would be wrong. WL-929 must decide whether `informative` goes on the withdrawn rule only.
- **F3. 48 entries have no rule.** They are `to_external` edges into specs of other projects (DP, EA, HDB), whose specs have no minted rules. They are classified for the record, but WL-929 can only drop their edges.
- **F4. Grouping.** Edges sharing an anchor prefix at `none` were grouped as one entry. Where the old header showed separate authored entries with different classes, the entry was split (WL-PLAN-1 38 §5 and §5.1, WL-PLAN-136 66 §4 and §4.1). A rule gets one class across plans, except that WL-PLAN-106 marks two rules (WL-RULE-829 and WL-RULE-840) unsure where WL-PLAN-1 marks them N2. Both results mean the rule is not informative.
- **F5. Some N2 links will land on unrelated tasks.** EA-PLAN-8's only task is a CI workflow, but its EA-SPEC-2 §2, §11 and §13 entries are N2. WL-SPEC-26 §5 (WL-PLAN-93) is the coverage contract that is itself being retired.

## N2

| Plan | Target | Rules | Reason |
|---|---|---|---|
| DP-PLAN-1 | DP-SPEC-1#sec-2 | (none, external) | Decisions D1-D11 are buildable requirements that bind the plan's tasks (it builds D9 via §10) but are delivered in later sections. |
| DP-PLAN-2 | DP-SPEC-1#sec-2 | (none, external) | Decisions D1-D11 bind the Python client (PKCE plus client credentials, GSP publish, DCAT versioning); plan obeys them. |
| DP-PLAN-3 | DP-SPEC-1#sec-2 | (none, external) | Decisions D1-D11 bind the plan's work (D2 import pipeline retarget) but are delivered in later sections. |
| EA-PLAN-6 | EA-SPEC-1#sec-11 | (none, external) | Security model states binding trust-boundary rules (1Password only, nothing on disk) that the plan's credential and hardening work must obey. |
| EA-PLAN-7 | EA-SPEC-1#sec-11 | (none, external) | Security model has concrete mitigations (mlockall, dumpable=0, UID check); plan's Secret type respects it, hardening is phase 2. |
| EA-PLAN-8 | EA-SPEC-2#sec-11 | (none, external) | Standing security constraints binding the mesh work; plan records they are delivered by other phase tasks. |
| EA-PLAN-8 | EA-SPEC-2#sec-13 | (none, external) | Phasing binds the series and requires the phase-4 mesh suspend opt-out; delivered by other plans, not this one. |
| EA-PLAN-8 | EA-SPEC-2#sec-2 | (none, external) | Structural rules (agent dials out, bulk bytes bypass coordinator) bind the mesh; plan says phases 1 and 4 honour them. |
| EA-PLAN-13 | EA-SPEC-3#sec-1 | (none, external) | Goals carry binding constraints (model-agnostic protocol, only Macs eligible for embed leases) the series must obey. |
| EA-PLAN-13 | EA-SPEC-3#sec-2 | (none, external) | Topology states architecture constraints (sidecar over UDS, no coordinator access or credential) built by phase 3. |
| HDB-PLAN-4 | HDB-SPEC-2#sec-5 | (none, external) | Prescribes libraries and access methods (arrow-rs, roaring, HDT, io_uring, dax mmap) that storage work must use; plan uses arrow and roaring. |
| HDB-PLAN-4 | HDB-SPEC-2#sec-7 | (none, external) | Carries binding Stage-1 defaults (inline xsd:int only, copy-on-write snapshots, no WAL); plan follows inline-int and defers the rest. |
| HDB-PLAN-63 | HDB-SPEC-25#sec-2 | (none, external) | Non-goals carry binding constraints: Stage-1 contract unchanged, same read surface for consumers, single-writer model. |
| HDB-PLAN-63 | HDB-SPEC-25#sec-4 | (none, external) | Phasing binds every increment to stay harness-gated (SPEC-01 subset green, suites extend); plan honors that, builds nothing here. |
| HDB-PLAN-63 | HDB-SPEC-25#sec-6 | (none, external) | Risks impose obligations on the S1 plan: bench against the NF4 write-amp budget and honor one-tick-one-batch atomicity. |
| WL-PLAN-1 | WL-SPEC-38#sec-2.2 | WL-RULE-829 | Binding rule: system packages live in the Dockerfile and bootstrap.sh fails loudly; plan tasks obey it. |
| WL-PLAN-1 | WL-SPEC-38#sec-4.1 | WL-RULE-835 | Entry-point contract (lode worktree next --json); plan says task 7 exercises it but builds nothing for it. |
| WL-PLAN-1 | WL-SPEC-38#sec-5 | WL-RULE-840 | Plan says §5 constrains task shape: build the human path only so dispatch substitutes values, never code. |
| WL-PLAN-81 | WL-SPEC-32#sec-11 | WL-RULE-788 | Plan says 032 sec-11 binds it (e2e through public surfaces only) while implemented by none of it. |
| WL-PLAN-83 | WL-SPEC-32#sec-11 | WL-RULE-788 | Plan says the standing rule binds (e2e via public surfaces only); the three-slice release content is built elsewhere. |
| WL-PLAN-85 | WL-SPEC-32#sec-1 | WL-RULE-777 | Plan says binding, not delivered: card counts must derive from facts and never invent status. |
| WL-PLAN-85 | WL-SPEC-32#sec-11 | WL-RULE-788 | Plan says standing rule: e2e drives public surfaces only; release content not planned here. |
| WL-PLAN-87 | WL-SPEC-29#sec-8.2 | WL-RULE-772 | Plan says binding constraint: no producing handler gains a hardcoded notifier; crew mutations only emit events. |
| WL-PLAN-87 | WL-SPEC-32#sec-10 | WL-RULE-787 | Plan says 032 §10 is a standing rule governing the crew page without being implemented here. |
| WL-PLAN-87 | WL-SPEC-32#sec-11 | WL-RULE-788 | Plan says 032 §11 is a standing rule governing its e2e task without being implemented here. |
| WL-PLAN-93 | WL-SPEC-26#sec-5 | WL-RULE-737, WL-RULE-738, WL-RULE-739, WL-RULE-740, WL-RULE-741 | Defines the covers contract the plan's coverage predicate reads (026 §5.1 CoverageList); binding, built elsewhere. |
| WL-PLAN-97 | WL-SPEC-32#sec-10 | WL-RULE-787 | Plan says standing rule: Milestones and Deliverables pages inherit the narrow-width and keyboard rules. |
| WL-PLAN-97 | WL-SPEC-32#sec-11 | WL-RULE-788 | Plan says the e2e task inherits §11's public-surfaces-only rule; release acceptance built elsewhere. |
| WL-PLAN-98 | WL-SPEC-29#sec-1 | WL-RULE-755 | Section defines buildable project metadata (seeded_by, labels, horizon) built by part 4; plan is bound by its seeded_by definition. |
| WL-PLAN-99 | WL-SPEC-32#sec-10 | WL-RULE-787 | Plan says 032 §10 binds every task here and none implements it: accessibility rules apply to the surfaces built. |
| WL-PLAN-99 | WL-SPEC-32#sec-11 | WL-RULE-788 | Plan says 032 §11 binds every task: e2e drives public surfaces only; release slices are not built here. |
| WL-PLAN-100 | WL-SPEC-32#sec-10 | WL-RULE-787 | Plan says 032 §10 binds every page it builds (narrow-check measured) while it builds no accessibility machinery. |
| WL-PLAN-100 | WL-SPEC-32#sec-11 | WL-RULE-788 | Plan says 032 §11 binds its e2e style; the release demonstration is buildable acceptance that no single plan owns. |
| WL-PLAN-101 | WL-SPEC-32#sec-10 | WL-RULE-787 | Plan says §10 binds it (narrow-width primary decisions) while implementing none of it; WCAG and responsive rules apply to its new pages. |
| WL-PLAN-101 | WL-SPEC-32#sec-11 | WL-RULE-788 | Plan says §11 binds it (e2e through public surfaces only); the release acceptance demo is buildable but not delivered here. |
| WL-PLAN-103 | WL-SPEC-32#sec-10 | WL-RULE-787 | Plan says binding, not delivered: the brief section obeys the narrow-width and accessibility rules. |
| WL-PLAN-103 | WL-SPEC-32#sec-11 | WL-RULE-788 | Plan says standing rule: e2e drives public surfaces only; release slices are not built here. |
| WL-PLAN-104 | WL-SPEC-32#sec-10 | WL-RULE-787 | Plan says standing rules govern the crew pages: WCAG 2.2 AA and narrow-width rules apply without being delivered here. |
| WL-PLAN-104 | WL-SPEC-32#sec-11 | WL-RULE-788 | Plan says standing rule governs its e2e task (public surfaces only); the release demonstration is built elsewhere. |
| WL-PLAN-105 | WL-SPEC-29#sec-8.2 | WL-RULE-772 | Plan says the no-hardcoded-notifier rule governs it; the subscriber must be offset-tracked, not wired into the Crew handler. |
| WL-PLAN-113 | WL-SPEC-32#sec-10 | WL-RULE-787 | Plan says binding, not delivered: the board obeys reflow and table rules but adds no accessibility scope. |
| WL-PLAN-113 | WL-SPEC-32#sec-11 | WL-RULE-788 | Plan says standing rule: e2e drives public surfaces only; slice 3's unattended run is not planned here. |
| WL-PLAN-127 | WL-SPEC-61#sec-0 | WL-RULE-1077 | Problem section ends with a binding decision: hard renames, no compatibility aliases, no deprecation period. |
| WL-PLAN-127 | WL-SPEC-61#sec-1.1 | WL-RULE-1079 | States task start and task claim keep their names; binds this plan's task-group renames. |
| WL-PLAN-127 | WL-SPEC-61#sec-6 | WL-RULE-1089 | Plan says the rename procedure is 061 §6, in order, for every task. |
| WL-PLAN-128 | WL-SPEC-61#sec-0 | WL-RULE-1077 | Problem section ends with a binding decision: hard renames, no compatibility aliases, no deprecation period. |
| WL-PLAN-128 | WL-SPEC-61#sec-1 | WL-RULE-1078, WL-RULE-1079 | Plan's tasks are written against the naming law (L3, L4, L6 cited); the law binds, enforcement is part 4's. |
| WL-PLAN-128 | WL-SPEC-61#sec-6 | WL-RULE-1089 | Plan's global constraints require the §6 rename procedure in order; procedure binds, nothing delivered. |
| WL-PLAN-129 | WL-SPEC-61#sec-0 | WL-RULE-1077 | Problem section ends with a binding decision: hard renames, no compatibility aliases, no deprecation period. |
| WL-PLAN-129 | WL-SPEC-61#sec-1 | WL-RULE-1078, WL-RULE-1079 | The naming law binds every rename this plan makes (L8 cited); enforcement is part 4's. |
| WL-PLAN-129 | WL-SPEC-61#sec-6 | WL-RULE-1089 | Plan's global constraints require following the §6 rename procedure in order; procedure binds, nothing delivered. |
| WL-PLAN-130 | WL-SPEC-61#sec-0 | WL-RULE-1077 | Problem section ends with a binding decision: hard renames, no compatibility aliases, no deprecation period. |
| WL-PLAN-130 | WL-SPEC-61#sec-1.1 | WL-RULE-1079 | States task start and task claim keep their names under L3; a constraint the rename and name-rule test must respect. |
| WL-PLAN-130 | WL-SPEC-61#sec-2.5 | WL-RULE-1085 | Resulting top-level set is built by parts 1-3; this plan's enforcement test must respect it but builds none of it. |
| WL-PLAN-130 | WL-SPEC-61#sec-6 | WL-RULE-1089 | Plan adopts the 061 §6 rename procedure as a global constraint on every task. |
| WL-PLAN-134 | WL-SPEC-59#sec-1 | WL-RULE-1037, WL-RULE-1038, WL-RULE-1039 | Review-is-the-API principle binds the guide (served only via GET, never agent-to-agent); built across the series. |
| WL-PLAN-136 | WL-SPEC-66#sec-4 | WL-RULE-1165, WL-RULE-1167, WL-RULE-1168, WL-RULE-1169, WL-RULE-1170 | Write-safety rules (CSP, POST-only, same-origin) bind the page; this read-only part obeys the CSP, part 2 builds the writes. |
| WL-PLAN-137 | WL-SPEC-66#sec-2.5 | WL-RULE-1157 | Constraint the page must obey: no percentage, counts only; the write plan must respect it. |
| WL-PLAN-138 | WL-SPEC-66#sec-2.5 | WL-RULE-1157 | Binding rule that the Progress page shows no percentage; the plan's live-update work must obey it. |
| WL-PLAN-139 | WL-SPEC-66#sec-2.5 | WL-RULE-1157 | Binding rule that the Progress page shows no percentage; the plan's page work must obey it. |
| WL-PLAN-140 | WL-SPEC-32#sec-10 | WL-RULE-787 | Plan says binding, not delivered: the due-date form must be keyboard-operable and follow the accessibility rules. |
| WL-PLAN-140 | WL-SPEC-32#sec-11 | WL-RULE-788 | Plan says the standing rule applies: e2e drives public surfaces only. |
| WL-PLAN-141 | WL-SPEC-67#sec-1 | WL-RULE-1184 | Scope carries binding invariants (adds no state, changes no rule, referrers unchanged) that the plan explicitly obeys. |
| WL-PLAN-141 | WL-SPEC-67#sec-8 | WL-RULE-1197 | Deferral with conditions for a future --sync; binds the plan not to build it, which the plan says it respects. |
| WL-PLAN-142 | WL-SPEC-67#sec-8 | WL-RULE-1197 | Deferral with conditions for a future --sync; binds the plan not to build it, which the plan says it respects. |

## unsure

| Plan | Target | Rules | Reason |
|---|---|---|---|
| DP-PLAN-1 | DP-SPEC-1#sec-14 | (none, external) | Mostly risks and open items, but R2 handling names a mock OIDC server and dev smoke test no other section specifies. |
| DP-PLAN-2 | DP-SPEC-1#sec-14 | (none, external) | Mostly risks and open items, but R2 handling names a mock OIDC server and dev smoke test no other section specifies. |
| DP-PLAN-3 | DP-SPEC-1#sec-14 | (none, external) | Mostly risks and open items, but R2 handling names a mock OIDC server and dev smoke test no other section specifies. |
| EA-PLAN-9 | EA-SPEC-1#sec-5.2 | (none, external) | Deferred but real design for internal/caller; not informative, yet it does not bind this plan's tasks. |
| WL-PLAN-106 | WL-SPEC-37#sec-10 | WL-RULE-821 | Testing section lists concrete required tests; buildable. |
| WL-PLAN-106 | WL-SPEC-37#sec-5.1 | WL-RULE-807 | States where the vendored-skill pin is recorded (lode install); reads as a requirement, not rationale. |
| WL-PLAN-106 | WL-SPEC-37#sec-9 | WL-RULE-820 | Degradation behaviours are testable requirements (fallbacks, reported errors), not rationale. |
| WL-PLAN-106 | WL-SPEC-38#sec-2.2 | WL-RULE-829 | Listed as rationale here, but WL-PLAN-1 treats it as binding (bootstrap.sh fails loudly naming what is missing). |
| WL-PLAN-106 | WL-SPEC-38#sec-5 | WL-RULE-840 | Dispatch seams are constraints the human path must keep; WL-PLAN-1 classes it N2. 5.1 is its own entry. |
| WL-PLAN-106 | WL-SPEC-4#sec-6.1 | WL-RULE-430 | Decisions table states buildable rules (parent never claimable, forbidden parent states); not rationale despite the plan listing it as none. |
| WL-PLAN-106 | WL-SPEC-45#sec-1 | WL-RULE-905, WL-RULE-906, WL-RULE-907, WL-RULE-908, WL-RULE-909 | Resolves to 45 §1.1-1.x: the state vocabulary, mandatory core and default workflow are core buildable requirements. |
| WL-PLAN-106 | WL-SPEC-45#sec-6 | WL-RULE-922, WL-RULE-923, WL-RULE-924, WL-RULE-925 | Resolves to 45 §6.x: workflow API surface, event and review task are buildable requirements. |
| WL-PLAN-106 | WL-SPEC-46#sec-7 | WL-RULE-956 | Testing section lists concrete required tests; buildable. |
| WL-PLAN-106 | WL-SPEC-46#sec-9 | WL-RULE-958 | Acceptance criteria are checkable requirements, not rationale. |
| WL-PLAN-106 | WL-SPEC-55#sec-5 | WL-RULE-993 | Order of operations for the corpus cutover is a procedure of work steps; unclear whether informative now that it ran. |
| WL-PLAN-106 | WL-SPEC-57#sec-9 | WL-RULE-1020 | Acceptance criteria are checkable requirements, not rationale. |

## N1 by plan

| Plan | Entries | Rules |
|---|---|---|
| DP-PLAN-1 | 2 | 0 |
| DP-PLAN-2 | 2 | 0 |
| DP-PLAN-3 | 2 | 0 |
| EA-PLAN-8 | 4 | 0 |
| EA-PLAN-9 | 11 | 0 |
| EA-PLAN-13 | 4 | 0 |
| HDB-PLAN-4 | 2 | 0 |
| HDB-PLAN-63 | 2 | 0 |
| WL-PLAN-1 | 7 | 7 |
| WL-PLAN-2 | 3 | 3 |
| WL-PLAN-4 | 4 | 4 |
| WL-PLAN-6 | 3 | 3 |
| WL-PLAN-7 | 2 | 2 |
| WL-PLAN-97 | 2 | 2 |
| WL-PLAN-106 | 115 | 128 |
| WL-PLAN-112 | 3 | 3 |
| WL-PLAN-113 | 1 | 1 |
| WL-PLAN-121 | 2 | 2 |
| WL-PLAN-125 | 2 | 2 |
| WL-PLAN-127 | 1 | 1 |
| WL-PLAN-128 | 1 | 1 |
| WL-PLAN-129 | 1 | 1 |
| WL-PLAN-130 | 1 | 1 |
| WL-PLAN-131 | 1 | 1 |
| WL-PLAN-132 | 3 | 3 |
| WL-PLAN-133 | 1 | 1 |
| WL-PLAN-134 | 1 | 1 |
| WL-PLAN-135 | 1 | 1 |
| WL-PLAN-136 | 3 | 3 |
| WL-PLAN-137 | 3 | 3 |
| WL-PLAN-138 | 2 | 2 |
| WL-PLAN-139 | 2 | 2 |
| WL-PLAN-141 | 1 | 1 |

## N3, excluded

A `none` parent whose children the same plan lists at another level. WL-929 drops these edges outright. The rules listed are the parent and its `none` descendants. WL-PLAN-106's version 6 also had 9 N3 entries, which version 7 removed along with its other `none` entries.

| Plan | Target | Rules |
|---|---|---|
| WL-PLAN-127 | WL-SPEC-61#sec-2 | WL-RULE-1080, WL-RULE-1084 |
| WL-PLAN-128 | WL-SPEC-61#sec-2 | WL-RULE-1080, WL-RULE-1081 |
| WL-PLAN-129 | WL-SPEC-61#sec-2 | WL-RULE-1080 |
| WL-PLAN-132 | WL-SPEC-59#sec-1 | WL-RULE-1037, WL-RULE-1039 |
| WL-PLAN-133 | WL-SPEC-59#sec-1 | WL-RULE-1037, WL-RULE-1039 |
| WL-PLAN-135 | WL-SPEC-59#sec-1 | WL-RULE-1037, WL-RULE-1039 |
| WL-PLAN-138 | WL-SPEC-66#sec-4 | WL-RULE-1165, WL-RULE-1166, WL-RULE-1167, WL-RULE-1168, WL-RULE-1169 |
| WL-PLAN-139 | WL-SPEC-66#sec-4 | WL-RULE-1165, WL-RULE-1166, WL-RULE-1168, WL-RULE-1169, WL-RULE-1170 |
| WL-PLAN-139 | WL-SPEC-66#sec-6 | WL-RULE-1176 |
| WL-PLAN-140 | WL-SPEC-32#sec-3 | WL-RULE-779 |
| WL-PLAN-143 | WL-SPEC-1#sec-9 | WL-RULE-390, WL-RULE-392, WL-RULE-393, WL-RULE-394, WL-RULE-395 |
