---
name: splitting-specs-into-plans
description: Use when a worklode spec is too large for one implementation plan and must be split into a numbered plan series — "split spec 0NN into plans", "write plan 1 of N", "plan the cockpit", "decompose this spec", "how many parts should this be" — or when checking whether a spec's sections are fully planned. Defines the section-coverage frontmatter and the task decomposition order.
---

# Splitting a spec into a plan series

Two decisions, in this order. Get the first wrong and the second is wasted
work.

1. **The split** — which spec sections each part covers, and how completely.
   This is the `covers:` frontmatter. Write it for every part before
   drafting any part's body.
2. **The decomposition** — how one part's sections become tasks. This is the
   layer order in §3.

Derived from the four-way planning comparison on spec 032 part 1
(2026-08-09): the plans differed more in what they thought part 1 *was* than
in quality, because nothing recorded the split.

## 1. Section coverage frontmatter

A plan's `covers:` is a list of objects, one per spec section the plan touches.
The key is `covers`, not `implements`: a plan writes no code, so it claims
nothing. `wl:implements` is a component's claim that its code meets a section
(025 §11); a plan undertakes, and its minted tasks discharge that (026 §5).

```yaml
---
status: draft
covers:
  - spec: WL-SPEC-32#sec-2
    coverage: full
  - spec: WL-SPEC-32#sec-3
    coverage: partial
    fullCoverageWith:
      - 2026-08-10-project-cockpit-2-intake-and-launch
  - spec: WL-SPEC-32#sec-11
    coverage: none
---
```

| Key | Required | Meaning |
|---|---|---|
| `spec` | yes | Document reference with a `#sec-N` fragment — `WL-SPEC-<N>` shorthand, or the document's slug. |
| `coverage` | yes | `full`, `partial`, or `none` |
| `fullCoverageWith` | no | Plans that, together with this one, make the section `full`. Plan slugs — plans have no number shorthand. |

**`full`** — after this plan executes, the section is satisfied. Nothing else
is owed.

**`partial`** — this plan covers part of the section. Name the rest in
`fullCoverageWith:` when you know which parts finish it. Without that key the
aggregate stays `partial`, which is a declared gap rather than an oversight —
that is the point of writing it down.

**`none`** — the plan is bound by the section but covers nothing in it.
Use it for standing rules: 032 §11's "end-to-end tests drive the HTTP UI and
API surfaces and do not write directly to the store" governs every part while
being implemented by none of them. Without `none`, a reader cannot tell a
governing constraint from a forgotten section.

### Aggregate coverage is a query

For a spec section `S`, over accepted-or-superseded plans naming it (a
superseded plan is spent, and discharges what it covered — 026 §2.1):

- any plan claims `full` → **fully planned**;
- a plan claims `partial` with `fullCoverageWith: [P…]`, every `P` exists and
  is accepted or superseded and contributes `full` or `partial` to `S` →
  **fully planned**;
- otherwise, any `partial` claim → **partially planned** (report the gap);
- only `none` claims, no deferral → **bound only**;
- a plan `defers` `S` to a named owner and nothing claims `partial` →
  **deferred**, owner named (026 §5.3);
- no plan names `S` → **unplanned**.

The backbone runs that query — `lode doc list --needs-planning --json` returns
each accepted spec's uncovered anchors already classified `unplanned`,
`partial`, `bound-only` or `deferred` (with the deferral's `owner`; 026 §2.1),
so "which sections has nobody planned" needs no reading of plans at all.

### Validator contract

A bare `covers` reference means `full`; `NO-SPEC` stays bare. The object form
requires `spec` with a section fragment and one of the three levels.
`fullCoverageWith` is valid only beside `partial`, must be non-empty, and names
accepted plans contributing `full` or `partial` to the same section. `none`
contributes nothing. `implements` remains readable only as a retired spelling
and is reported; new output always writes `covers`.

**`fullCoverageWith` is on its way out, but is still what runs.** Draft spec
026 §5.4 replaces the forward pointer with a decomposition stamp: a `partial`
claim is legal only inside a complete sibling set, `lode decompose <ref>`
validates and stamps it, and a covered section in a decomposed spec counts as
fully planned whatever its level says. None of that is built — no
`wl:decomposedAt`, no `lode decompose` for documents — and the validator, the
`doc_edges` completion side-table, and `lode doc list --needs-planning` all
still read `fullCoverageWith`. Keep writing it. When §5.4 ships, the key
disappears and the completeness rule replaces this subsection.

`lode doc anchors <file>` lints a draft locally before `lode doc new` — anchors,
and a plan's `## Tasks` definitions. Creating the document is what turns
`covers:` into edges: a reference no document in the project resolves to is
kept as an external reference instead, which is a silently unplanned section,
so check `lode doc get <slug> --json` for `edges` you expected.

Note the layer this sits in: **planning** coverage is declared intent on a plan.
025 §11's `<component> wl:implements <section>` is **implementation** coverage,
observed from `.worklode/implements.yaml`. Different question, different owner.

## 2. Choosing the split

1. **List the anchors.** `lode doc get WL-SPEC-<N> --json | jq -r '.sections[].anchor'`
   (or read the rendered spec with `lode show WL-SPEC-<N>`). Account
   for every anchor across the series: one or more parts claim `full` or
   `partial`, or the section is deliberately unplanned. A standing constraint
   may repeat as `coverage: none` in every part it governs.
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
   come later. The blocks are the contract between parts; `fullCoverageWith:`
   only works if the later parts' slugs are fixed.
6. **Claim honestly.** A part that claims `#sec-11` and cannot demonstrate any
   of §11's acceptance bullets should claim `coverage: none` or drop the
   section. Claiming a section you cannot prove is the failure this format
   exists to catch.

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
carrying durable rationale means the spec was incomplete
(`docs/authoring-design-docs.md`).

## 5. Worklode plan conventions

Body format, task YAML keys (`kind`/`priority`/`skills`/`blockedBy`), slug and
reference syntax: `docs/authoring-design-docs.md` §"Declaring a plan's tasks",
and the `worklode-docs-authoring` skill for how a document is created. A series
part restarts task numbering at 1; ordering across parts is a document-level
edge, never a task number. Declare it with `blockedBy:` on the later part
(WL-143): it writes the same single `blocks` row with the ends swapped, and it
is the only spelling that works while writing a series forward, since `blocks:`
on the earlier part would mean amending a plan that may already be accepted.
Both ends must already resolve, so create the earlier part first either way.

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
