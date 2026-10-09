# Corpus pass: rule kinds

WL-1034, WL-PLAN-149 Task 3. Applies the five rule kinds of WL-SPEC-77 §4 to every live worklode rule, from the classification in `v2result0.jsonl` to `v2result3.jsonl` (422 rows). Every change went through `lode rule set <ref> --kind <kind>`. A rule an accepted plan covers was refused with 409 and is listed below, not forced.

Rules the classification does not name (WL-REQ-1367, WL-REQ-1368, minted after it) were not touched. Rules of other projects (the `DP` project still has 30 accepted `informative` rules) are out of scope.

## Counts per kind

Live rules (accepted and draft) in the worklode project.

| Kind | Before | After |
|---|---|---|
| `requirement` | 373 | 271 |
| `catalogue` | 0 | 74 |
| `invariant` | 3 | 7 |
| `definition` | 0 | 10 |
| `principle` | 0 | 57 |
| `informative` | 48 | 5 |

Outcome per row: 145 set, 19 refused with 409, 5 invariant candidates without enforcement checks, 1 already at its target kind (WL-RULE-1321). The other rows are `requirement` in the classification and need no change.

## Rules changed

| Rule | Heading | From | To |
|---|---|---|---|
| WL-RULE-1 | The problem the gate solves | informative | principle |
| WL-REQ-3 | Changes to the "what" | requirement | catalogue |
| WL-REQ-4 | Changes to the "how" | requirement | catalogue |
| WL-RULE-10 | What this does not do | informative | principle |
| WL-RULE-11 | Open questions | informative | principle |
| WL-RULE-13 | Glossary | informative | principle |
| WL-RULE-14 | Executables and import boundaries | informative | principle |
| WL-REQ-15 | The six executables | requirement | catalogue |
| WL-REQ-18 | Distribution | requirement | catalogue |
| WL-RULE-20 | Deployment | informative | principle |
| WL-REQ-21 | Where each instance runs | requirement | catalogue |
| WL-RULE-25 | Sandboxes and worker environments | requirement | principle |
| WL-RULE-44 | Open questions | informative | principle |
| WL-RULE-45 | Actors | requirement | definition |
| WL-RULE-46 | One actor per person | requirement | invariant |
| WL-REQ-51 | Server configuration | requirement | catalogue |
| WL-REQ-52 | Realm configuration | requirement | catalogue |
| WL-REQ-56 | CLI login | requirement | catalogue |
| WL-RULE-60 | Security properties | requirement | principle |
| WL-RULE-62 | GitHub account linking | requirement | principle |
| WL-REQ-63 | The GitHub App | requirement | catalogue |
| WL-REQ-66 | Errors | requirement | catalogue |
| WL-REQ-75 | Degradation | requirement | catalogue |
| WL-RULE-78 | Tasks | informative | principle |
| WL-REQ-80 | Kinds | requirement | catalogue |
| WL-REQ-81 | Priority and concern | requirement | catalogue |
| WL-RULE-82 | Task state machine and delivery | informative | principle |
| WL-REQ-83 | States | requirement | catalogue |
| WL-REQ-84 | Transitions | requirement | catalogue |
| WL-RULE-86 | Closed | requirement | definition |
| WL-REQ-87 | Edges | requirement | catalogue |
| WL-REQ-90 | State machine of a task with children | requirement | catalogue |
| WL-RULE-96 | Prioritization and pickup | informative | principle |
| WL-REQ-100 | Ranking | requirement | catalogue |
| WL-REQ-101 | `claim --next` and `--strict-focus` | requirement | catalogue |
| WL-RULE-103 | Event log and provenance | informative | principle |
| WL-REQ-108 | Event types and payloads | requirement | catalogue |
| WL-REQ-109 | The `doc-lifecycle` subscriber | requirement | catalogue |
| WL-REQ-111 | Metrics | requirement | catalogue |
| WL-RULE-112 | Webhook-driven delivery | requirement | principle |
| WL-REQ-113 | Fact tables | requirement | catalogue |
| WL-REQ-114 | Handlers | requirement | catalogue |
| WL-RULE-121 | Deliverables | requirement | definition |
| WL-REQ-122 | Identifiers and cross-project references | requirement | catalogue |
| WL-RULE-129 | Open questions | informative | principle |
| WL-RULE-130 | Done declarations | requirement | definition |
| WL-REQ-135 | The done check | requirement | catalogue |
| WL-RULE-138 | Provenance of a delivery state | informative | principle |
| WL-RULE-139 | Verification tasks for bugs | informative | principle |
| WL-RULE-140 | What the verification task states | informative | principle |
| WL-RULE-141 | Workflows | requirement | definition |
| WL-RULE-142 | Vocabulary versus machine | requirement | invariant |
| WL-REQ-143 | The mandatory core | requirement | catalogue |
| WL-REQ-144 | Optional states and declared entries | requirement | catalogue |
| WL-REQ-150 | State readers | requirement | catalogue |
| WL-RULE-153 | The automation engine | requirement | definition |
| WL-REQ-159 | Metrics | requirement | catalogue |
| WL-RULE-160 | Deferred | informative | principle |
| WL-RULE-161 | Open questions | informative | principle |
| WL-RULE-162 | Principle: rows are things someone made, groupings are queries | requirement | principle |
| WL-REQ-169 | Edges | requirement | catalogue |
| WL-REQ-175 | Spec fan-out is a query | requirement | catalogue |
| WL-RULE-181 | What an edit invalidated | requirement | principle |
| WL-REQ-182 | Surfaces and permissions | requirement | catalogue |
| WL-RULE-183 | Open questions | informative | principle |
| WL-RULE-184 | Design-doc queries | requirement | principle |
| WL-REQ-185 | Where the answers come from | requirement | catalogue |
| WL-RULE-196 | Consolidation | requirement | definition |
| WL-REQ-199 | Shorthand tiers | requirement | catalogue |
| WL-RULE-201 | Anchor permanence | requirement | invariant |
| WL-RULE-202 | Plan coverage | requirement | definition |
| WL-RULE-206 | Decomposition | informative | principle |
| WL-REQ-207 | Frontmatter checks | requirement | catalogue |
| WL-REQ-208 | Ontology terms | requirement | catalogue |
| WL-RULE-210 | Decision decks | requirement | principle |
| WL-RULE-217 | Meetings, minutes, and notes | requirement | principle |
| WL-RULE-218 | Entities | requirement | definition |
| WL-REQ-228 | Surfaces | requirement | catalogue |
| WL-REQ-234 | Configuration | requirement | catalogue |
| WL-REQ-237 | Vocabulary: reuse and mint | requirement | catalogue |
| WL-REQ-238 | Classes | requirement | catalogue |
| WL-REQ-239 | Properties | requirement | catalogue |
| WL-REQ-240 | SKOS schemes | requirement | catalogue |
| WL-REQ-241 | Reasoning tiers | requirement | catalogue |
| WL-REQ-242 | Entity model by layer | requirement | catalogue |
| WL-REQ-246 | Namespaces | requirement | catalogue |
| WL-REQ-247 | Instance grammar | requirement | catalogue |
| WL-REQ-254 | Subjects | requirement | catalogue |
| WL-REQ-261 | Surfaces, permission, metrics, degraded operation | requirement | catalogue |
| WL-RULE-266 | Branch names and worktree layout | informative | principle |
| WL-REQ-267 | Server-rendered branch template | requirement | catalogue |
| WL-REQ-269 | Base directory | requirement | catalogue |
| WL-REQ-272 | Hooks | requirement | catalogue |
| WL-REQ-275 | `lode install` | requirement | catalogue |
| WL-REQ-276 | Skill delivery | requirement | catalogue |
| WL-REQ-278 | Hook delivery per harness | requirement | catalogue |
| WL-REQ-280 | Degradation | requirement | catalogue |
| WL-RULE-282 | Brief, slash commands, skills, and the worker | informative | principle |
| WL-REQ-284 | Slash commands | requirement | catalogue |
| WL-RULE-287 | Agent sessions | informative | principle |
| WL-REQ-291 | Hook wiring | requirement | catalogue |
| WL-REQ-294 | Reconciliation and setup diagnosis | requirement | catalogue |
| WL-REQ-295 | `lode doctor` | requirement | catalogue |
| WL-REQ-298 | API | requirement | catalogue |
| WL-REQ-305 | The naming law | requirement | catalogue |
| WL-REQ-306 | The command tree | requirement | catalogue |
| WL-REQ-307 | Completions and ordering | requirement | catalogue |
| WL-REQ-315 | `lode show` dispatch | requirement | catalogue |
| WL-REQ-316 | Command surface for scoping | requirement | catalogue |
| WL-RULE-317 | Org-wide agent skills | requirement | principle |
| WL-RULE-322 | Vendored design skills | requirement | principle |
| WL-REQ-324 | The skill set | requirement | catalogue |
| WL-RULE-328 | Open questions | informative | principle |
| WL-REQ-341 | Panels selected by fact | requirement | catalogue |
| WL-RULE-371 | Non-goals | informative | principle |
| WL-RULE-372 | Open questions | informative | principle |
| WL-RULE-1290 | Verification | requirement | principle |
| WL-RULE-1291 | Verification | requirement | principle |
| WL-RULE-1302 | Two surfaces, one binary | informative | principle |
| WL-RULE-1312 | Non-goals | informative | principle |
| WL-RULE-1315 | The review is the API | requirement | principle |
| WL-REQ-1318 | Threads and intents | requirement | catalogue |
| WL-RULE-1320 | Guides | requirement | definition |
| WL-REQ-1322 | Schema | requirement | catalogue |
| WL-REQ-1323 | API and `lode` verbs | requirement | catalogue |
| WL-REQ-1327 | Metrics and events | requirement | catalogue |
| WL-RULE-1333 | Out of scope | informative | principle |
| WL-REQ-1340 | Facts the page needs | requirement | catalogue |
| WL-RULE-1342 | Non-goals | informative | principle |
| WL-RULE-1347 | Designed, not built | informative | principle |
| WL-RULE-1348 | Designed, not built | informative | principle |
| WL-RULE-1349 | One model across packages | requirement | invariant |
| WL-RULE-1350 | Designed, not built | informative | principle |
| WL-RULE-1351 | Designed, not built | informative | principle |
| WL-RULE-1352 | Designed, not built | informative | principle |
| WL-RULE-1353 | Designed, not built | informative | principle |
| WL-RULE-1354 | Designed, not built | informative | principle |
| WL-RULE-1358 | Designed, not built | informative | principle |
| WL-RULE-1359 | Designed, not built | informative | principle |
| WL-RULE-1360 | Designed, not built | informative | principle |
| WL-RULE-1361 | Designed, not built | informative | principle |
| WL-RULE-1363 | Designed, not built | informative | principle |
| WL-RULE-1364 | Open questions | informative | principle |
| WL-REQ-1365 | The `lode` plugin's skills and agent | requirement | catalogue |
| WL-RULE-1366 | Designed, not built | informative | principle |

## Refused: covered by an accepted plan

Each stays at its current kind until the covering plans are withdrawn or superseded. The five `informative` rules here are why accepted `informative` rules remain.

| Rule | Heading | Current | Target | Accepted covering plans |
|---|---|---|---|---|
| WL-RULE-12 | How to read this set | informative | principle | WL-PLAN-68 |
| WL-REQ-43 | Verification | requirement | principle | WL-PLAN-7, WL-PLAN-131 |
| WL-REQ-67 | Task-declared secrets | requirement | principle | WL-PLAN-43, WL-PLAN-44, WL-PLAN-45, WL-PLAN-87, WL-PLAN-104 |
| WL-RULE-76 | Open questions | informative | principle | WL-PLAN-43, WL-PLAN-44, WL-PLAN-45 |
| WL-REQ-118 | Research work | requirement | principle | WL-PLAN-81, WL-PLAN-87, WL-PLAN-97, WL-PLAN-98, WL-PLAN-99, WL-PLAN-100, WL-PLAN-101, WL-PLAN-103, WL-PLAN-104 |
| WL-REQ-197 | Reference resolution and integrity | requirement | principle | WL-PLAN-66 |
| WL-REQ-225 | Images and attachments on tasks | requirement | definition | WL-PLAN-46, WL-PLAN-47, WL-PLAN-48 |
| WL-RULE-235 | Open questions | informative | principle | WL-PLAN-46, WL-PLAN-47, WL-PLAN-48 |
| WL-REQ-236 | Ownership split | requirement | principle | WL-PLAN-42 |
| WL-REQ-243 | Deliverable and Effect | requirement | definition | WL-PLAN-42 |
| WL-REQ-245 | IRIs | requirement | principle | WL-PLAN-35, WL-PLAN-42 |
| WL-REQ-253 | Corpus index | requirement | principle | WL-PLAN-2 |
| WL-RULE-263 | Open questions | informative | principle | WL-PLAN-42 |
| WL-REQ-264 | Design lens | requirement | principle | WL-PLAN-68 |
| WL-RULE-304 | Open questions | informative | principle | WL-PLAN-39, WL-PLAN-40, WL-PLAN-41, WL-PLAN-68 |
| WL-REQ-330 | Navigation shell | requirement | principle | WL-PLAN-73, WL-PLAN-83 |
| WL-REQ-339 | Focus and the next decision | requirement | definition | WL-PLAN-73 |
| WL-REQ-342 | Intake and promotion | requirement | principle | WL-PLAN-101 |
| WL-REQ-1294 | Rules are the record | requirement | principle | WL-PLAN-146 |

## Invariant candidates without enforcement checks

§4 requires an invariant's body to state the checks that enforce it. These bodies state the property but no check, so they were not set to `invariant`.

| Rule | Heading | Kind | Note |
|---|---|---|---|
| WL-REQ-106 | Total order and the commit horizon | requirement | stays `requirement` |
| WL-REQ-1247 | A document's status has one owner | requirement | stays `requirement` |
| WL-REQ-1289 | Timestamps | requirement | stays `requirement` |
| WL-RULE-1306 | Canonical URLs | invariant | already `invariant`, left unchanged for review |
| WL-RULE-1344 | Invariants | invariant | already `invariant`, left unchanged for review |

## Template text, move when WL-PLAN-146 Task 5 lands

Rows with verdict `template` (60). They are set to `principle` for now, except the ones marked refused, which keep their current kind. Each belongs in its spec's template text (WL-SPEC-77 §19.1).

| Rule | Heading | Kind now |
|---|---|---|
| WL-RULE-1 | The problem the gate solves | principle |
| WL-RULE-10 | What this does not do | principle |
| WL-RULE-11 | Open questions | principle |
| WL-RULE-12 | How to read this set | informative (refused) |
| WL-RULE-13 | Glossary | principle |
| WL-RULE-14 | Executables and import boundaries | principle |
| WL-RULE-20 | Deployment | principle |
| WL-REQ-43 | Verification | requirement (refused) |
| WL-RULE-44 | Open questions | principle |
| WL-RULE-60 | Security properties | principle |
| WL-RULE-76 | Open questions | informative (refused) |
| WL-RULE-78 | Tasks | principle |
| WL-RULE-82 | Task state machine and delivery | principle |
| WL-RULE-96 | Prioritization and pickup | principle |
| WL-RULE-103 | Event log and provenance | principle |
| WL-RULE-129 | Open questions | principle |
| WL-RULE-138 | Provenance of a delivery state | principle |
| WL-RULE-139 | Verification tasks for bugs | principle |
| WL-RULE-140 | What the verification task states | principle |
| WL-RULE-160 | Deferred | principle |
| WL-RULE-161 | Open questions | principle |
| WL-RULE-181 | What an edit invalidated | principle |
| WL-RULE-183 | Open questions | principle |
| WL-RULE-184 | Design-doc queries | principle |
| WL-REQ-197 | Reference resolution and integrity | requirement (refused) |
| WL-RULE-206 | Decomposition | principle |
| WL-RULE-217 | Meetings, minutes, and notes | principle |
| WL-RULE-235 | Open questions | informative (refused) |
| WL-REQ-245 | IRIs | requirement (refused) |
| WL-REQ-253 | Corpus index | requirement (refused) |
| WL-RULE-263 | Open questions | informative (refused) |
| WL-RULE-266 | Branch names and worktree layout | principle |
| WL-RULE-282 | Brief, slash commands, skills, and the worker | principle |
| WL-RULE-287 | Agent sessions | principle |
| WL-RULE-304 | Open questions | informative (refused) |
| WL-RULE-328 | Open questions | principle |
| WL-REQ-330 | Navigation shell | requirement (refused) |
| WL-REQ-342 | Intake and promotion | requirement (refused) |
| WL-RULE-371 | Non-goals | principle |
| WL-RULE-372 | Open questions | principle |
| WL-RULE-1290 | Verification | principle |
| WL-RULE-1291 | Verification | principle |
| WL-REQ-1294 | Rules are the record | requirement (refused) |
| WL-RULE-1312 | Non-goals | principle |
| WL-RULE-1333 | Out of scope | principle |
| WL-RULE-1342 | Non-goals | principle |
| WL-RULE-1347 | Designed, not built | principle |
| WL-RULE-1348 | Designed, not built | principle |
| WL-RULE-1350 | Designed, not built | principle |
| WL-RULE-1351 | Designed, not built | principle |
| WL-RULE-1352 | Designed, not built | principle |
| WL-RULE-1353 | Designed, not built | principle |
| WL-RULE-1354 | Designed, not built | principle |
| WL-RULE-1358 | Designed, not built | principle |
| WL-RULE-1359 | Designed, not built | principle |
| WL-RULE-1360 | Designed, not built | principle |
| WL-RULE-1361 | Designed, not built | principle |
| WL-RULE-1363 | Designed, not built | principle |
| WL-RULE-1364 | Open questions | principle |
| WL-RULE-1366 | Designed, not built | principle |
