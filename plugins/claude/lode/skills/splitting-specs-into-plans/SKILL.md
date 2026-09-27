---
name: splitting-specs-into-plans
description: Use when a worklode spec is too large for one implementation plan and must be split into a numbered plan series — "split spec 0NN into plans", "write plan 1 of N", "plan the cockpit", "decompose this spec", "how many parts should this be" — or when checking whether a spec's arranged rules are fully planned.
---

# Splitting a spec into a plan series

Two decisions, in this order. Get the first wrong and the second is wasted
work.

1. **The split** — which rules each part undertakes, and how completely.
   This is the `covers:` frontmatter. Write it for every part before
   drafting any part's body.
2. **The decomposition** — how one part's requirements become tasks governed
   by those rules. This is the layer order in §3.

Derived from the four-way planning comparison on spec 032 part 1
(2026-08-09): the plans differed more in what they thought part 1 *was* than
in quality, because nothing recorded the split.

A spec arranges independently versioned rules. A plan is governed by the rules
reached through `covers`; acceptance gives its minted tasks `governedBy` links to the
governing set. Planning allocates work against the rules, while the existing
frontmatter and gap queries still address their document sections.

## 1. Section coverage frontmatter

A plan's `covers:` is a list of plain references — a requirement ref
(`WL-REQ-<n>`), a `<doc>#sec-N` reference, or a whole-document reference.
Only requirements are covered: a section or whole-document entry resolves to
the requirements in its scope and skips invariants and informative rules,
and a direct ref to an invariant or informative rule is refused. The key is
`covers`, not `implements`: a plan writes no code, so it claims nothing.
`wl:implements` is a component's claim that its code meets a section
(025 §11); a plan undertakes, and its minted tasks discharge that
(WL-SPEC-78 §4.1). A `covers` entry always means the plan builds the whole
requirement: there are no coverage levels, and `coverage:`/`fullCoverageWith:` are
refused on write.

```yaml
---
status: draft
covers:
  - WL-SPEC-32#sec-2
  - WL-SPEC-32#sec-4
---
```

Three cases that look like partial coverage are expressed another way:

| Case | Expression |
|---|---|
| the plan builds part of a requirement | split it (`lode rule link WL-REQ-B --derived-from WL-REQ-A`, see §2 below) so each plan covers whole requirements |
| the plan must obey a rule but builds nothing in it | the rule is an **invariant** (`WL-RULE-<n>`) and, once accepted, governs every task in its project; no `covers` entry |
| the rule has nothing to build (rationale, context) | the rule is **informative** (`WL-RULE-<n>`); no `covers` entry |

A rule's kind is set with `lode rule set <rule-ref> --kind <kind>`.
If a rule is still a requirement but only states a constraint, reclassify it
before planning rather than leaving it as a gap no plan will close.

Use it for a standing rule such as 032 §11's "end-to-end tests drive the HTTP
UI and API surfaces and do not write directly to the store": an invariant
governs every part while being built by none of them, and it needs no
`covers` entry to do so.

### Planning gaps are a query

For a spec section `S` whose rule is a requirement (an invariant or
informative rule is never a gap), over accepted-or-superseded plans covering its rule
(a superseded plan is spent, and discharges what it covered — WL-SPEC-78
§1.3):

- some such plan covers it, and no draft plan also covers it →
  **planned**;
- a draft plan covers it → **plan-draft**, reported as a gap until the plan
  is accepted;
- no plan covers it, and a plan `defers` it to a named owner → **deferred**,
  owner named (WL-SPEC-78 §1.3);
- no plan names it at all → **unplanned**.

The backbone runs that query — `lode doc list --needs-planning --json` returns
each accepted spec's uncovered anchors already classified `plan-draft`,
`deferred` or `unplanned` (with the deferral's `owner`; WL-SPEC-78 §1.3), so
"which sections has nobody planned" needs no reading of plans at all.

### Validator contract

A `covers` entry must be a plain reference; `NO-SPEC` stays bare.
`coverage:` and `fullCoverageWith:` are refused, on a plan and by `lode doc
import`. `implements` remains readable only as a retired spelling and is
reported; new output always writes `covers`.

`lode doc lint <file>` lints a draft locally before `lode doc add` — anchors,
and a plan's `## Tasks` definitions. Creating the document is what turns
`covers:` into edges: a reference no document in the project resolves to is
kept as an external reference instead, which is a silently unplanned section,
so check `lode doc show <slug> --json` for `edges` you expected.

Note the layer this sits in: **planning** coverage is declared intent on a plan.
025 §11's `<component> wl:implements <section>` is **implementation** coverage,
observed from `.worklode/implements.yaml`. Different question, different owner.

## 2. Choosing the split

1. **List the rules and their anchors.** Read `lode show <spec-ref> --inline`
   and `lode rule list --doc <spec-ref> --json`. Use
   `lode doc show <spec-ref> --json` for the section anchors used by `covers`.
   Run `lode show <rule-ref> --json` for each rule to inspect its existing
   plans, governed tasks and relationships before declaring new work. Account
   for every requirement across the series: some part covers it, or the
   section is deliberately unplanned. A standing invariant governs every part
   it applies to without a `covers` entry in any of them.
2. **Check what the spec's `requires:` actually delivers.** Read the schema
   (`ls deploy/base/migrations/`) and the packages (`ls internal/`), not the
   specs' `status:`. A spec can be `accepted` with nothing built. A section
   whose facts do not exist yet cannot be `full` in any part.
3. **Group by dependency, not by section number.** Sections that need the same
   unavailable facts belong in the same part. Sections implementable against
   today's schema belong in part 1.
4. **Part 1 earns its keep alone.** It must produce something demonstrable over
   real data. If part 1 cannot be demonstrated, the split is wrong.
5. **Write every part's `covers:` block now**, including parts whose bodies
   come later. The blocks are the contract between parts.
6. **Claim honestly.** A part that claims `#sec-11` and cannot demonstrate any
   of §11's acceptance bullets should drop the section, or split the rule so
   the part covering it is the one that can. Claiming a section you cannot
   prove is the failure this format exists to catch.
7. A plan is bounded by a token budget the server enforces (12 S19): about
   32,000 tokens is the soft budget, past which `lode doc add` and `lode doc
   edit` print a warning, and 64,000 is the hard ceiling, past which the
   write is refused. A project may set `plan_tokens_soft` and
   `plan_tokens_hard` with `lode project set settings`. Split when the
   warning appears.

## 3. Decomposing one part into tasks

Order tasks by **layer**, so each is testable with the cheapest possible
harness and nothing is written twice:

| | Layer | Test harness |
|---|---|---|
| 1 | Shell / chrome — layout, navigation, assets. No domain logic. | Handler tests |
| 2 | Cross-cutting request state — identity, context plumbing | Handler tests |
| 3 | Pure projection — types, categories, mode selection. No DB, no HTTP. | Table tests, no database |
| 4 | Pure derivation — ranking, the one-decision rule. Still pure. | Table tests, no database |
| 5 | Store readers — one bulk reader per fact family, UI-neutral | Store tests, real Postgres |
| 6 | **The tracer** — the first page joining 1–5 over real data | Handler + store |
| 7–9 | Fan-out — remaining destinations reusing the shell | Handler tests |
| 10 | Each mutation — store, API, CLI, and web in one task | All surfaces |
| 11 | e2e journey + docs alignment | `-tags e2e` |

The rules that make the order work:

- **Pure before I/O.** Mode selection and ranking are table-tested against
  typed inputs with no database, which also means later parts can populate
  those inputs without touching the derivation.
- **The tracer is the convergence point, not task 1.** It is where the spine
  is proven; putting it first forces every layer to be stubbed and rewritten.
- **One reader per fact family, and refactor existing callers onto it in the
  same task.** Two surfaces that compute "blocked" separately will disagree.
- **A mutation is one task across every surface.** Store write, event source,
  metric, API route, CLI verb, and web form land together or the event
  provenance ends up half-wired.
- **e2e last, through public surfaces only.** Never a direct store write.
- **Every task leaves `go test ./...` green.** Route moves update their
  assertions in the same task.

**Right-sizing:** split where a reviewer could reject one task while approving
its neighbour. Fold setup, config, scaffolding, and docs into the task whose
deliverable needs them. A task bundling a store reader, an endpoint, a page,
and a metric cannot be partially rejected — it is too big. Expect 8–12 tasks
for a substantial part; 5 usually means several tasks were merged.

## 4. Step granularity

Calibrated from the 032 comparison, where the same part drew plans from 474 to
4,204 lines:

- **Too thin** (~2 aggregate steps per task, no test code): the implementer
  designs the tests, so a fully-specified Sonnet task escalates to Opus and the
  tiering in `MODEL_SELECTION.md` stops paying.
- **Too thick** (~77% of lines inside code fences): the plan becomes the
  implementation in markdown. A 180-line stylesheet transcribed into a plan is
  an asset that ships unreviewed and unlintable.
- **Right:** real code for the first test of each new behaviour; exact commands
  with their expected output; an explicit commit step. Point at asset files;
  do not transcribe them. Roughly 25–45% of lines in code fences.

Quote exact values from the spec in Global Constraints — palette hexes, label
spellings, ordered destination lists. Do not restate spec prose; a plan
carrying durable rationale means the spec was incomplete (see the
`lode:writing-docs` skill).

## 5. Worklode plan conventions

Body format, task YAML keys (`kind`/`priority`/`skills`/`blockedBy`), and
reference syntax: the `lode:writing-docs` skill's "Declaring a plan's
tasks" section, which also covers how a document is created. A series
part restarts task numbering at 1; ordering across parts is a document-level
edge, never a task number. Declare it with `blockedBy:` on the later part;
`blocks:` is refused (WL-SPEC-77 §8.1). The named part must already resolve,
so create the earlier part first.

Constraints a plan inherits in the worklode repo itself — state them once in
Global Constraints, do not repeat per task. A plan in another project inherits
that project's equivalents, not these:

- New endpoint, background loop, outbound call, or store operation with
  meaningful outcomes adds or extends `worklode_*` metrics with tests, bounded
  labels, never a project or task id.
- New migrations are a new numbered `.up.sql`/`.down.sql` pair, listed in
  `deploy/base/kustomization.yaml`, never an edit to a shipped one.
- Store tests need Postgres with pgvector; a skipped test proved nothing.
- `e2e/` drives public surfaces only.
