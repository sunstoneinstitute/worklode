# Rule kinds: review list (WL-935)

Stig confirms this list before WL-936 applies `docs/research/rule-kinds.tsv`.
Every live rule not listed here is a `requirement`. A `split` row in the TSV has no kind yet: each
needs a decision (split it, or pick one kind) before WL-936 applies the file.
Rules in the mixed-signal section are not repeated in the sections below it.

| kind | derived | text | text-mixed | total |
|---|---|---|---|---|
| requirement | 256 | 92 | 12 | 360 |
| invariant | 1 | 10 | 2 | 13 |
| informative | 5 | 21 | 1 | 27 |
| unsure | 0 | 0 | 0 | 0 |
| split | 4 | 13 | 6 | 23 |
| all | | | | 423 |

Derivation: a live rule's predecessors are the withdrawn rules its `supersedes` edges reach,
plus the rule itself when a plan covers it. A predecessor covered `full` or `partial` by any plan counts
as requirement (R); one covered only at `none` takes its N1/N2 class from
`coverage-none-classification.tsv` (WL-928). Any R and no N2 gives `requirement`. N2 and no R gives
`invariant`. Only N1 gives `informative`. R and N2 together is a split candidate. N1 beside R or N2 is
read as context and does not make a split. A predecessor with no covers edge, or covered `none` by a
plan WL-928 did not classify, leaves the rule to text classification unless an R predecessor decides it.
Coverage levels were read from the `covers` edges on 2026-09-27, before WL-929.

`text-mixed`: a predecessor was covered `full` or `partial` by one plan and `none` by others. Those rules
were classified from their text with that mixed signal as input, and the derivation's `requirement` was discarded.

## Mixed coverage signal (21)

- **WL-RULE-25** `WL-SPEC-73#sec-5` Sandboxes and worker environments (text-mixed): Section intro saying a sandbox agent is an ordinary CLI client and existing mechanisms apply, with the build in 5.5; predecessor built by WL-PLAN-106, held N1 by WL-PLAN-1.
- **WL-RULE-30** `WL-SPEC-73#sec-5.5` The sandbox session (text-mixed): Concrete sandbox session setup (CLI entry point, hooks baked at image build, task-scoped token env, MCP relay via .mcp.json) built once; built by WL-PLAN-106, WL-PLAN-1 held it N2 as a dependency only.
- **WL-RULE-53** `WL-SPEC-74#sec-4.3` Provisioning on login (text-mixed): Specific login provisioning steps (ID token checks, group gate, actor upsert, unique github_username) built once and held by tests; built by seven plans, held none by WL-PLAN-143 (unclassified).
- **WL-RULE-62** `WL-SPEC-74#sec-9` GitHub account linking (text-mixed): States the feature to build, linking each human's GitHub account at login and storing a user-to-server token; built by six plans, held none by WL-PLAN-143 (unclassified).
- **WL-RULE-64** `WL-SPEC-74#sec-9.2` Link flow (text-mixed): Concrete link flow (redirect, signed state, strict login check, upsert, finishLogin, /auth/github/link, actor show) built once; built by seven plans each, held none by WL-PLAN-143 (unclassified).
- **WL-RULE-65** `WL-SPEC-74#sec-9.3` Stored GitHub tokens (text-mixed): Concrete table schema, AES-GCM sealing and locked lazy refresh built once; built by seven plans, held none by WL-PLAN-143 (unclassified).
- **WL-RULE-67** `WL-SPEC-74#sec-10` Task-declared secrets (text-mixed): Requirement is symbolic secret declaration resolved against a catalog and materialized by an operator ceremony; invariant is that Worklode never brokers or stores values and 1Password stays source of truth; built by WL-PLAN-106, held N1 by WL-PLAN-1.
- **WL-RULE-118** `WL-SPEC-75#sec-13` Research work (text-mixed): Section intro binding all research work to the one backbone and forbidding new entities for Story and Distribution, with the builds in 13.1 onward; predecessors built by WL-PLAN-101,103, held N2 by WL-PLAN-98,105,87.
- **WL-RULE-180** `WL-SPEC-77#sec-16` The backbone is the only home (text-mixed): Mostly concrete lode doc import and lode doc lint behavior built once and held by tests; predecessor built by WL-PLAN-106, held only N1 (context) by WL-PLAN-6.
- **WL-RULE-202** `WL-SPEC-78#sec-4` Plan coverage (text-mixed): Defines the plan document model (no anchors or rules, covers edges vs implements vs produces) built once in the doc store; built by WL-PLAN-72,54,106,123, WL-PLAN-93 held N2 as a dependency on that model.
- **WL-RULE-306** `WL-SPEC-81#sec-2` The command tree (text-mixed): Requirement is building the designed-not-built commands (doc fetch, the listed plugin skills); invariant is the naming conventions every later CLI change obeys (remove vs delete, /lode:<name> follows the CLI); predecessors built partially by WL-PLAN-127,128,129, held N2 by WL-PLAN-130 and unclassified by 127,128.
- **WL-RULE-329** `WL-SPEC-82#sec-1` Facts drive the interface (text-mixed): Prohibits stored parallel status on the cockpit and requires every displayed status to keep its evidence category, which binds every cockpit feature; built by WL-PLAN-73, held N2 by WL-PLAN-85.
- **WL-RULE-335** `WL-SPEC-82#sec-4` Accessibility and responsive behavior (text-mixed): Requirement is narrow-check.sh plus the settled token, focus and layout fixes; invariant is that every page and later UI work meets WCAG 2.2 AA and those settled rules; built partially by WL-PLAN-73,81,83,85, held N2 by nine later plans.
- **WL-RULE-337** `WL-SPEC-82#sec-5.1` Lifecycle modes (text-mixed): Requirement is the three-mode selection table and its transitions; invariant is that the shell stays stable and modes are derived from facts and never persisted, which later Overview features must respect; built by WL-PLAN-73 and partly WL-PLAN-101, held N2 by WL-PLAN-140.
- **WL-RULE-349** `WL-SPEC-82#sec-13.1` The review is the API (text-mixed): Requirement is the review object in Postgres over /api/v1 with the crit Comment golden test; invariant is that every review verb must work with no crit and no browser and nothing server-side knows crit; built partially by WL-PLAN-132,133,135, held N2 by WL-PLAN-134.
- **WL-RULE-360** `WL-SPEC-82#sec-15.2` Layout (text-mixed): Concrete Progress page layout, row expansion and hover positions built once, with no-percentage a testable property of that page; predecessor built by WL-PLAN-136, held N2 by WL-PLAN-137,138,139 which extend the same page.
- **WL-RULE-362** `WL-SPEC-82#sec-15.4` Write safety (text-mixed): Requirement is the JSON write gate, frame-ancestors header and read-only stream; invariant is that every later write route under /progress/ obeys the nine rules and a relaxation must be justified; built by WL-PLAN-137,138,139, held N2 by WL-PLAN-136 and unclassified by 138,139.
- **WL-RULE-392** `WL-SPEC-1#sec-9.2` Expected GitHub identity from Keycloak (text-mixed): Concrete claim mapper, Claims field and expected_github_login sync built once; built by seven plans, held none by WL-PLAN-143 (unclassified).
- **WL-RULE-393** `WL-SPEC-1#sec-9.3` Link flow (web) (text-mixed): Concrete web link flow steps built once and held by tests; built by seven plans, held none by WL-PLAN-143 (unclassified).
- **WL-RULE-394** `WL-SPEC-1#sec-9.4` `lode auth` command group (text-mixed): Concrete command changes (auth login, hidden alias, auth status) built once; built by seven plans, held none by WL-PLAN-143 (unclassified).
- **WL-RULE-395** `WL-SPEC-1#sec-9.5` GitHub token storage and refresh (text-mixed): Concrete token table, encryption at rest and locked lazy refresh built once; built by seven plans, held none by WL-PLAN-143 (unclassified).

## Invariants (11)

- **WL-RULE-2** `WL-SPEC-72#sec-2` "What" versus "how" (text): The citation test applies to every later change: a change to the what amends the spec first or in the same change.
- **WL-RULE-3** `WL-SPEC-72#sec-2.1` Changes to the "what" (text): Defines which changes count as what-changes for the citation test that every later change is judged by.
- **WL-RULE-4** `WL-SPEC-72#sec-2.2` Changes to the "how" (text): Defines which changes never need a spec change, applied to every later change.
- **WL-RULE-9** `WL-SPEC-72#sec-6` Plans are never back-patched (text): A plan with a minted task is read-only, binding all later plan edits.
- **WL-RULE-31** `WL-SPEC-73#sec-5.6` Seams for backbone-initiated dispatch (derived): predecessors N2 (N1 context alongside): WL-RULE-840=N2; WL-RULE-841=N1
- **WL-RULE-175** `WL-SPEC-77#sec-11.3` Spec fan-out is a query (text): Fan-out needs are answered by queries owned elsewhere, and all later work is barred from making spec completion an event.
- **WL-RULE-248** `WL-SPEC-79#sec-10.3` Stored strings stay relative (text): No absolute instance IRI is ever stored, binding all later writes.
- **WL-RULE-309** `WL-SPEC-81#sec-5` cmd decides, cli renders (text): The cmd-decides, cli-renders seam binds every later command and view.
- **WL-RULE-1247** `WL-SPEC-77#sec-16.1` A document's status has one owner (text): No hook, CI job or script may ever read document status from a file, binding all later work.
- **WL-RULE-1287** `(unplaced)` Timestamps (text): Every timestamp column, payload and API field is UTC, binding all later work.
- **WL-RULE-1289** `WL-SPEC-73#sec-10` Timestamps (text): Every timestamp column, payload and API field is UTC, binding all later work.

## Unsure (0)


## Split candidates (17)

- **WL-RULE-5** `WL-SPEC-72#sec-2.3` The grey zone (text): Builds recording the classification on the task once, and binds every later author to classify grey-zone changes.
- **WL-RULE-7** `WL-SPEC-72#sec-4` Declaration (text): Builds the trailer grammar and reconciler reading once, and binds every later unplanned change to declare a Spec: trailer.
- **WL-RULE-26** `WL-SPEC-73#sec-5.1` The bootstrap is repo-committed (derived): predecessors disagree (requirement and invariant): WL-RULE-827=R; WL-RULE-828=N1; WL-RULE-829=N2
- **WL-RULE-49** `WL-SPEC-74#sec-3` Authorization (text): Builds the grants table, routeGuards and boot refusal once, and binds every later role or route to go through those tables, never a handler check.
- **WL-RULE-79** `WL-SPEC-75#sec-2.1` Row (text): Builds the tasks table once, and binds all later schema to the stated column conventions.
- **WL-RULE-104** `WL-SPEC-75#sec-9.1` Tables (text): Builds the events, state_log and event_subscribers tables once, and binds all later work never to delete or compact events.
- **WL-RULE-105** `WL-SPEC-75#sec-9.2` `RecordEvent` (text): Builds RecordEvent once, and binds every later typed-table mutation to run as its apply callback.
- **WL-RULE-107** `WL-SPEC-75#sec-9.4` Subscribers (text): Builds the subscriber read/ack/lock loop once, and binds every later subscriber handler to be idempotent.
- **WL-RULE-108** `WL-SPEC-75#sec-9.5` Event types and payloads (text): Builds the event type table and ns/ generation once, and binds every later event payload to name its subject.
- **WL-RULE-126** `WL-SPEC-75#sec-13.8` Events out, events in, crew spaces (text): Builds the gchat-crew-space subscriber once, and binds every later producing handler to use a subscriber instead of a hardcoded notifier.
- **WL-RULE-181** `WL-SPEC-77#sec-17` What an edit invalidated (derived): predecessors disagree (requirement and invariant): WL-RULE-1183=N1; WL-RULE-1184=N2; WL-RULE-1185=R; WL-RULE-1186=R; WL-RULE-1187=R; WL-RULE-1188=R; WL-RULE-1190=R; WL-RULE-1191=R; WL-RULE-1192=R; WL-RULE-1195=R; WL-RULE-1196=R
- **WL-RULE-246** `WL-SPEC-79#sec-10.1` Namespaces (text): Builds the prefixes and request-derived base once, and binds all later stored strings never to contain the wlid: expansion.
- **WL-RULE-249** `WL-SPEC-79#sec-10.4` One constructor (text): Builds internal/kg/iri and its guard test once, and binds all later code to build IRIs only through it.
- **WL-RULE-305** `WL-SPEC-81#sec-1` The naming law (derived): predecessors disagree (requirement and invariant): WL-RULE-505=R; WL-RULE-1078=R; WL-RULE-1079=N2; WL-RULE-1081=R; WL-RULE-1082=R; WL-RULE-1083=R; WL-RULE-1090=N1
- **WL-RULE-308** `WL-SPEC-81#sec-4` Enforcement (derived): predecessors disagree (requirement and invariant): WL-RULE-1088=R; WL-RULE-1089=N2
- **WL-RULE-322** `WL-SPEC-81#sec-8` Vendored design skills (text): Vendors the two skill plugins once, and binds Worklode to merge every later upstream release.
- **WL-RULE-357** `WL-SPEC-82#sec-14` Reading diffs (text): Builds the reading diff and its labelled surfaces once, and binds all later work never to gate or derive anything from it.

## Informative, by spec (26)

### DP-SPEC-24

- **DP-RULE-1** `DP-SPEC-24#sec-1` Goal (text): Goal statement summarising what the spec adds; the decisions and later sections specify it.
- **DP-RULE-2** `DP-SPEC-24#sec-2` Non-goals (text): Non-goals list.
- **DP-RULE-19** `DP-SPEC-24#sec-10` Follow-ups (text): Follow-ups list of possible later work.

### WL-SPEC-72

- **WL-RULE-1** `WL-SPEC-72#sec-1` The problem the gate solves (text): Problem statement explaining why the gate exists.
- **WL-RULE-10** `WL-SPEC-72#sec-7` What this does not do (text): States what the gate does not do.
- **WL-RULE-11** `WL-SPEC-72#sec-open-questions` Open questions (text): Open questions.

### WL-SPEC-73

- **WL-RULE-13** `WL-SPEC-73#sec-2` Glossary (text): Glossary of terms.
- **WL-RULE-14** `WL-SPEC-73#sec-3` Executables and import boundaries (text): Heading only with an empty body; the obligations sit in its subsections.
- **WL-RULE-20** `WL-SPEC-73#sec-4` Deployment (text): Heading only with an empty body; the obligations sit in its subsections.
- **WL-RULE-33** `WL-SPEC-73#sec-7` Agent usage accounting (derived): all predecessors N1: WL-RULE-1114=N1
- **WL-RULE-44** `WL-SPEC-73#sec-open-questions` Open questions (derived): all predecessors N1: WL-RULE-844=N1

### WL-SPEC-75

- **WL-RULE-129** `WL-SPEC-75#sec-open-questions` Open questions (text): Open questions.

### WL-SPEC-76

- **WL-RULE-137** `WL-SPEC-76#sec-5` External evidence routing (derived): all predecessors N1: WL-RULE-1017=N1
- **WL-RULE-160** `WL-SPEC-76#sec-12` Deferred (text): Deferred items list.
- **WL-RULE-161** `WL-SPEC-76#sec-open-questions` Open questions (text): Open questions.

### WL-SPEC-77

- **WL-RULE-183** `WL-SPEC-77#sec-open-questions` Open questions (text): Open questions.

### WL-SPEC-78

- **WL-RULE-184** `WL-SPEC-78#sec-1` Design-doc queries (text): Heading only with an empty body; the obligations sit in its subsections.
- **WL-RULE-210** `WL-SPEC-78#sec-6` Decision decks (text): Definition introducing the deck subsections that specify what is built.
- **WL-RULE-217** `WL-SPEC-78#sec-7` Meetings, minutes, and notes (text): Heading only with an empty body; the obligations sit in its subsections.

### WL-SPEC-80

- **WL-RULE-266** `WL-SPEC-80#sec-3` Branch names and worktree layout (text): Heading only with an empty body; the obligations sit in its subsections.
- **WL-RULE-282** `WL-SPEC-80#sec-7` Brief, slash commands, skills, and the worker (text): Heading only with an empty body; the obligations sit in its subsections.

### WL-SPEC-82

- **WL-RULE-336** `WL-SPEC-82#sec-5` The project Overview (text): Heading only with an empty body; the obligations sit in its subsections.
- **WL-RULE-348** `WL-SPEC-82#sec-13` The review surface (derived): all predecessors N1: WL-RULE-1036=N1
- **WL-RULE-358** `WL-SPEC-82#sec-15` The project Progress page (derived): all predecessors N1: WL-RULE-1146=N1
- **WL-RULE-371** `WL-SPEC-82#sec-17` Non-goals (text): Non-goals list.
- **WL-RULE-372** `WL-SPEC-82#sec-open-questions` Open questions (text): Open questions.
