---
name: worklode-docs-authoring
description: Use when creating or editing a worklode spec, ADR or plan with lode doc, or editing ns/*.ttl — "write a new spec", "add a plan", "lode doc new", "what goes in the frontmatter", "covers vs implements", "NO-SPEC", "renumber the sections", "amend a spec", "supersede a section", "{#sec-N} anchors", "add a wl: property", "SKOS concept", "is spec NNN implemented" — and for the spec/plan/task model (design tasks, minted tasks, why groupings are queries not rows). For splitting one spec across a numbered plan series, use lode:splitting-specs-into-plans instead.
---

# Authoring specs and plans

Documents live in the backbone, not in the tree: frontmatter, section anchors,
amendment and supersession, the `ns/` ontology the frontmatter keys come from,
and the spec/plan/task model those documents describe.

Read `docs/authoring-design-docs.md` before creating or editing a document — it
has the slug rules, the full frontmatter schema, and how to amend/supersede.

## Where a document lives, and how it gets there

`lode doc` is the only authoring path. Draft the markdown — frontmatter
included — in a scratch file, then:

```bash
lode doc anchors <file>                             # local lint: anchors, plan ## Tasks
lode doc new --kind <spec, adr or plan> --slug <slug> --file <file>   # creates it, draft
lode doc edit <ref> --file <file>            # replace a draft's body
lode doc revise <ref> --file <file>          # candidate revision on an accepted doc
lode doc revise <ref> --discard              # withdraw it unlanded: owner or its author
lode doc submit <ref>                        # mints the review task
lode doc accept <ref>                        # owner-gated; a plan's accept mints its tasks
lode doc transfer <ref> --to <actor>         # owner-gated; move ownership to another actor
```

Read one back with `lode show <ref>` (`WL-SPEC-25`, `WL-SPEC-25#sec-9`, or
`-s <anchor>`) or `lode doc get <ref> --json` for the body plus parsed
sections and edges. The scratch file is an editor buffer, not a copy of record
— nothing reads it after `lode doc new`.

## Frontmatter is mandatory

**Every document you create starts with YAML frontmatter — no exceptions.**
A spec needs `status` and, once accepted, `issued`. A plan needs `status` and
`covers` — the spec sections it undertakes to see built, optionally qualified
by a `coverage:` level (026 §5).

It is `covers` rather than `implements` because a plan writes no code:
`implements` is a component's claim that its code meets a section, and one word
for the promise and for the evidence leaves them indistinguishable.
`implements` still parses on a plan and is reported as retired.

When no spec governs it, write `covers: NO-SPEC` (the reserved "no governing
spec" sentinel, which takes no project key — 026 §4.3) rather than omitting the
key, because an absent `covers` is indistinguishable from a forgotten one.

Frontmatter keys are ontology property names, ordered lifecycle → `covers` →
`defers` → dependency → amendment → supersession. A `covers:` reference the project
resolves becomes an edge on creation; one it does not is kept verbatim as an
external reference — a typo therefore reads as an unplanned section rather
than an error, so check `lode doc get <slug> --json` for the edges you meant.

## Section anchors are frozen

Spec sections carry `{#sec-N}` anchors that are **frozen once the spec is
accepted** — amend or supersede a section, never renumber it. `lode doc
anchors <file>` checks the numbering and anchors before you create or edit a
document. There is no inlined view: what a section says *now* is the section
plus whatever amends it, and `lode doc get <ref> --json` names that in
`edges_in` (`amendedBy`, `isReplacedBy`) — follow those before treating the
body as current.

## Amending a spec that still has a file

The corpus is split until 055 lands: newer specs exist only in the backbone,
older ones also have a file under `docs/specs/`. `inlinespec.py` builds
`docs/specs/inlined/` from `docs/specs/index.yaml` alone, so an amendment made
by a backbone-only spec is **invisible** in the view `CLAUDE.md` sends every
reader to — the target's file shows the superseded text with nothing marking it.

So when a backbone-only document amends a spec that still has a file, do the
other two of `docs/authoring-design-docs.md`'s three edits **in that file**:
the inline `> **Amended by spec NNN §M.**` note next to the heading, saying
what changed, and an `amendedBy` entry keyed by the amended section. Reference
the amending document by shorthand (`WL-SPEC-61#sec-2`) — it has no filename to
point at. `secmeta.py` reports that as `unresolved` on stderr and does not fail.

Renamed command spellings elsewhere in the corpus are a separate question, and
the answer is "leave them": `docs/agent-surfaces.md` has the rule.

## The `ns/` ontology

`ns/` holds the `wl:` ontology extracted from specs 006/016/025/026:
`ontology.ttl` (classes, properties, axioms), `concept.ttl` (SKOS enums),
`shapes.ttl` (SHACL). It is the vocabulary the frontmatter keys come from, and
the parseable form — the specs' own Turtle blocks are illustrative and do not
parse. `ns/` owns the shared schema and the specs own the rationale (025 §17);
until the codegen step exists, amend the spec first, then mirror the term here
(`riot --validate ns/*.ttl`) — and never edit `wlc:TaskKind` apart from the
migration and `validKinds`, which a test holds together.

Term names are camelCase: `wl:` properties lowerCamelCase (`wl:coveringPlan`,
`wl:runtimeEventKind`), classes and concept schemes UpperCamelCase
(`wl:DesignDoc`, `wlc:TaskKind`). Snake_case is reserved for `wlc:` concepts
that carry a stored enum value — `wlc:docker_image` spells the `docker_image`
in the CHECK constraint — so a term's spelling says whether it names schema or
data.

## The spec / plan / task model

Spec 025, as implemented by the document store.

- A **spec** is a durable document. Writing or revising one is an ordinary
  claimable task (`kind = 'design'`, renamed from `spec` by 025 §10, which also
  widens its meaning to any design document — spec, ADR, or plan) that closes
  on submission for review, not on document acceptance, which is a status
  transition on the document rather than a task state. "Is the spec
  implemented?" is a coverage query, never a task state — do not create
  long-lived umbrella tasks per spec.
- A **plan** is an executable document; its execution is the set of tasks
  minted when the plan is accepted. 025 §9.2 mints no root row above them and
  groups them by a reference to the plan document instead. No kind declares a
  container: container-ness is inferred from a task's `child_of` children
  (004 §6.1). Do not create free-standing container tasks.
- **Groupings are queries, not rows** (025 §1): one plan's tasks = the tasks
  referencing it; cross-plan "ships together" = Milestone over Deliverables
  (v2); everything in a repo set = the project. There is no sprint concept and
  no container above a plan's tasks — order plans with `blocks`.
- Spec → plan decomposition is always an explicit human act; skills may offer
  it, never perform it unasked.
- **The prompt is minted, the act is not** (025 §15.4). `lode doc submit`
  emits `wl:DocumentSubmitted` and moves no column — the open review task *is*
  "under review" — and accepting a **spec** emits `wl:DocumentAccepted`. The
  `doc-lifecycle` subscriber turns each into one task: a `review` task on
  submission, a `design` task charged with decomposing the spec on acceptance,
  both carrying `about_doc` and suppressed while an open task of that kind
  already references the document. Minting the prompt is not performing the
  act: nothing here reviews, accepts or plans anything — `lode doc accept`
  stays the manual, owner-gated commit it was.
