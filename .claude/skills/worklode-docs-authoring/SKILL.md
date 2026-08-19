---
name: worklode-docs-authoring
description: Use when creating or editing any file under docs/specs/ or docs/plans/, or editing ns/*.ttl — "write a new spec", "add a plan", "what goes in the frontmatter", "covers vs implements", "NO-SPEC", "renumber the sections", "amend a spec", "supersede a section", "{#sec-N} anchors", "add a wl: property", "SKOS concept", "is spec NNN implemented" — and for the spec/plan/task model (design tasks, minted tasks, why groupings are queries not rows). For splitting one spec across a numbered plan series, use splitting-specs-into-plans instead.
---

# Authoring specs and plans

Everything under `docs/specs/` and `docs/plans/`: frontmatter, section
anchors, amendment and supersession, the `ns/` ontology the frontmatter keys
come from, and the spec/plan/task model those documents describe.

Read `docs/authoring-design-docs.md` before creating or editing anything here
— it has the filename rules, the full frontmatter schema, and how to
amend/supersede.

## Frontmatter is mandatory

**Every file you create under `docs/specs/` or `docs/plans/` starts with YAML
frontmatter — no exceptions.** A spec needs `status` and, once accepted,
`issued`. A plan needs `status` and `covers` — the spec sections it undertakes
to see built, optionally qualified by a `coverage:` level (026 §5).

It is `covers` rather than `implements` because a plan writes no code:
`implements` is a component's claim that its code meets a section, and one word
for the promise and for the evidence leaves them indistinguishable.
`implements` still parses on a plan and is reported as retired.

When no spec governs it, write `covers: NO-SPEC` (the reserved "no governing
spec" sentinel, which takes no project key — 026 §4.3) rather than omitting the
key, because an absent `covers` is indistinguishable from a forgotten one.

Frontmatter keys are ontology property names, ordered lifecycle → `covers` →
dependency → amendment → supersession. `scripts/secmeta.py` checks all of this
on commit; it reports and never rewrites, so a failure is yours to decide, not
to re-run.

## Section anchors are frozen

Spec sections carry `{#sec-N}` anchors that are **frozen once the spec is
accepted** — amend or supersede a section, never renumber it.
`scripts/secfmt.py` enforces the numbering (pre-commit hook; docs-only PRs skip
CI, so the hooks are the real gate). `./scripts/inlinespec.py` regenerates
`docs/specs/inlined/`.

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

Spec 025; files under `docs/` are the transitional mirror until it is
implemented.

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
