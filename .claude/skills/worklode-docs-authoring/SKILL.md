---
name: worklode-docs-authoring
description: Use when creating or editing a worklode spec, ADR or plan with lode doc, or editing ns/*.ttl — "write a new spec", "add a plan", "lode doc add", "what goes in the frontmatter", "covers vs implements", "NO-SPEC", "renumber the sections", "amend a spec", "supersede a section", "{#sec-N} anchors", "add a wl: property", "SKOS concept", "is spec NNN implemented" — and for the spec/plan/task model (design tasks, minted tasks, why groupings are queries not rows). For splitting one spec across a numbered plan series, use lode:splitting-specs-into-plans instead.
---

# Authoring specs and plans

Documents live in the Postgres backbone, not in the git tree — no
`docs/specs/`, no `docs/plans/`, no file to open, no pre-commit hook.
`lode doc` is the only way to create, read, or change one. This file is the
reference for the document model: frontmatter, references, section anchors,
amend/supersede, and how a plan declares its tasks.

## Where a document lives, and how it gets there

Draft the markdown — frontmatter included — in a scratch file, then:

```bash
lode doc anchors <file>                             # local lint: anchors, plan ## Tasks
lode doc add --kind <spec, adr or plan> --slug <slug> --file <file>   # creates it, draft
lode doc edit <ref> --file <file>            # replace a draft's body, or a plan's at any status
lode doc revise <ref>                        # open a candidate revision on an accepted doc
lode doc revise <ref> --file <file>          # update the open candidate's body
lode doc revise <ref> --accept               # land the candidate as the doc's next version
lode doc revise <ref> --discard              # withdraw it unlanded: owner or its author
lode doc submit <ref>                        # mints the review task
lode doc accept <ref>                        # owner-gated; a plan's accept mints its tasks
lode doc transfer <ref> --to <actor>         # owner-gated; move ownership to another actor
```

Read one back with `lode show <ref>` (`WL-SPEC-25`, `WL-SPEC-25#sec-9`, or
`-s <anchor>`), or `lode doc show <ref> --json` for the body plus parsed
sections and edges. `lode show <ref> --inline` folds every in-force amendment
and supersession into the section it acts on. `lode doc list`, `lode doc
versions <ref>`, and `lode doc todo <ref>` (what's left before a spec counts
as fully planned and executed) round out the reading surface; `lode doc
lint` reports the whole corpus's dangling references, unlike `doc anchors`,
which only lints one local file.

The scratch file is an editor buffer, not a copy of record — nothing reads
it once the command above succeeds. `lode doc edit` only works on a draft,
or on a plan (plans are edited in place at any status — 025 §9); an
accepted spec or ADR instead goes through `lode doc revise`: open a
candidate, edit it, `--accept` to land it or `--discard` to drop it.

## Frontmatter is mandatory

**Every document you create starts with YAML frontmatter — no exceptions.**
A spec needs `status` and, once accepted, `issued`. A plan needs `status`
and `covers` — the spec sections it undertakes to build, optionally
qualified by a `coverage:` level (`full`/`partial`/`none`) and, for
`partial`, a `fullCoverageWith` list of the plans that complete it (see
`lode:splitting-specs-into-plans` for that mechanism in full) — or
`covers: NO-SPEC` (026 §4.3, valid only here) when nothing governs it, never
omitted, since an absent `covers` reads as a forgotten one.

Keys are ontology property local names (specs 006/025/026), not a second
vocabulary — a key with no term behind it means the ontology is missing one,
not that you should invent one. Order them lifecycle → `covers` → `defers` →
dependency → amendment → supersession:

| Key | On | Meaning |
|---|---|---|
| `status` | spec, ADR | `draft`, `accepted`, or `superseded` (`proposed` is retired — a document under review stays `draft`) |
| `issued` | spec, ADR | `YYYY-MM-DD` of first publication |
| `covers` | plan | scalar or list of spec-section references this plan undertakes to build; qualifiable with `coverage:`/`fullCoverageWith:` |
| `implements` | plan | retired spelling of `covers`; still parses, reported as retired. A document carrying both is an error |
| `defers` | plan | list of `{spec, to}`: a section this plan hands off, and the document expected to cover it (026 §5.3) |
| `requires` / `isRequiredBy` | any | list of references; plain dependency, no ordering semantics |
| `blocks` / `blockedBy` | plan | orders one plan's whole execution before another's (025 §5); one row either end declares — prefer `blockedBy` on the later plan in a series, since the alternative is amending an earlier, possibly-accepted plan |
| `wasDerivedFrom` | spec | scalar reference (provenance) |
| `amends` / `amendedBy`, `replaces` / `isReplacedBy` | any | maps keyed by anchor, see below |
| `kind` | spec, ADR | `adr`, or absent for a spec — the resolver's document kind, distinct from a plan-task's `kind` (feature/bug/chore/design) below |
| `artifact` | any | catalog address(es) (`bigquery://…`, `gs://…`) this document is verified by (029 §3.1); declares additively |

A retired `task` key once named the lode task a plan's execution hung off; it
still parses (plan bodies are stored verbatim) but nothing reads it — find a
plan's minted tasks with `lode task list --plan <plan>`.

## References resolve by slug, not filename

A reference names a document, and there is no file for it to point at.
Resolution tries, in order: an **exact slug match** in the project (the bare
slug from `lode doc add --slug`, e.g. `covers: execution-backbone`); the
**`WL-SPEC-N` shorthand** (`<PROJECTKEY>-SPEC|ADR|PLAN-<n>`, 025 §14.3, e.g.
`WL-SPEC-25`, `WL-ADR-7`, `WL-PLAN-7` — the only form that crosses projects,
e.g. `CMS-SPEC-4` from inside `WL`); then a **bare corpus number** (`25`, not
`025`), only when nothing else matched and exactly one live spec or ADR
carries it. Append `#sec-N` to any of these to narrow to a section.

**A filename does not resolve.** `042-secret-templates.md` is neither a slug
nor a bare number — the trailing text after the digits makes matching it to
spec 042 a risky guess, so the number arm refuses it. It doesn't error, it
just silently fails to resolve and sits as an unresolved external ref
instead of an edge. This exact mistake has already produced dangling edges
in the corpus — check `edges_in` on `lode doc show <ref> --json`, or run
`lode doc lint`, rather than assume a `covers:`/`amends:` line did what you
meant. (A cross-project shorthand naming a project this instance can't
reach is `unresolved` too, but for a different reason: nothing in the
referring project can repair that one.)

## Section anchors

Every numbered heading in a spec or ADR carries a `{#sec-N}` anchor —
`## 2. Lease lifecycle {#sec-2}`, `### 2.1 Renewal {#sec-2.1}`. Depth is
capped at 3 levels (H2/H3/H4); a heading deeper than that is legal content
but takes no number and no anchor — it belongs to its nearest anchored
ancestor. `lode doc anchors <file>` checks numbering, anchors, and depth
before you post.

**An anchor is frozen once its document is `accepted`.** A revision that
renumbers a published anchor, or drops one without a replacing supersession,
is refused at `lode doc revise --accept` — the backbone's own append-only
rule (025 §7.2), not a linter you can skip. To insert a section between
`2.1` and `2.2` on an accepted document, use a **letter suffix**:
`### 2.1a New section {#sec-2.1a}`, which takes no counter slot and tells a
reader it was added after acceptance. Adding a genuinely *missing* anchor to
an already-correctly-numbered section is fine. A **superseded section keeps
its heading and anchor** — never delete it — with a note saying what
replaced it; a bare superseded section is a broken promise to whoever
linked it.

## Amending or superseding a section

Amending changes how a section should be read without replacing its text.
Two edits, both required, so the claim is discoverable from either document:

1. **An inline note next to the affected heading**, in the amending
   document, naming the section it acts on:

   ```markdown
   ## 2. Lease lifecycle {#sec-2}

   > **Amended by spec 012.** Closing a lease also stamps `ended_at` on every
   > open `agent_sessions` row for it.
   ```

2. **`amends` in the amending document's frontmatter, `amendedBy` in the
   amended one's**, both keyed by anchor:

   ```yaml
   amends: { ".": [WL-SPEC-4#sec-2] }        # in WL-SPEC-12
   amendedBy: { "#sec-2": [WL-SPEC-12] }     # the mirror, in WL-SPEC-4
   ```

The map key is the subject — which part of *this* document acts, `"."`
meaning the document as a whole — and each value names the *other*
document's section. Both directions are kept deliberately: "what still
constrains this section?" should answer from the document already open,
not a scan of every sibling. Use `"."` only when the amendment genuinely is
document-wide, never to fake a range of sections. `lode show <ref> --inline`
reads the result: a section's own text plus every effective amendment.

**Superseding** is the same shape with `replaces`/`isReplacedBy`, and it
too works per-section:

```yaml
# the superseding document — its §11 replaces one section of the target
replaces: { "#sec-11": [WL-SPEC-13#sec-2.3] }
```

When a **whole document** is superseded, set `status: superseded` and list
the successor(s) under `isReplacedBy` at `"."`; each successor records
`replaces` back.

## Declaring a plan's tasks

A plan body carries exactly one `## Tasks` section, holding nothing but one
`### Task <N> — <title>` subsection per task (em dash; the text after it is
the task's title). `N` runs 1, 2, 3… in document order, no gaps — every part
of a plan series restarts at 1. Each subsection is a YAML metadata fence,
then prose (what to do, which files, the test that proves it), then optional
`- [ ]` steps:

````markdown
### Task 1 — Short imperative title

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

Prose: files to touch, the test that proves it. Then optional `- [ ]` steps.
````

| Key | Required | Default | Values |
|---|---|---|---|
| `kind` | yes | — | `feature`, `bug`, `chore`, `design` — never `review`/`spike`, which plans don't mint |
| `priority` | no | `medium` | `critical`, `high`, `medium`, `low` |
| `skills` | no | none | skill-registry names; `plugin:skill` is the registry's identity, a bare name resolves while it names one skill |
| `blockedBy` | no | none | task numbers **in this file**; becomes `blocks` edges at mint |

A task's **title is its declaration's identity**: titles must be unique
within the plan, and accepting an edited plan mints only the declarations
with no task yet, leaving every existing task alone (025 §9.2). Append a
declaration to add work to an accepted plan; retitle one only to withdraw
that task and declare a new one. Ordering across files (series parts, other
plans) is the document-level `blocks`/`blockedBy` above, never a task
number — `lode doc anchors <file>` runs this whole parse first, so a
malformed task block is caught locally.

## The `ns/` ontology

`ns/` holds the `wl:` ontology extracted from specs 006/016/025/026 —
`ontology.ttl` (classes, properties, axioms), `concept.ttl` (SKOS enums),
`shapes.ttl` (SHACL) — the vocabulary the frontmatter keys come from and the
parseable form; the specs' own Turtle blocks are illustrative and don't
parse. `ns/` owns the shared schema, the specs own the rationale (025 §17):
amend the spec first, then mirror the term here (`riot --validate
ns/*.ttl`); never edit `wlc:TaskKind` apart from the migration and
`validKinds`, which a test holds together.

Term names are camelCase: `wl:` properties lowerCamelCase
(`wl:coveringPlan`), classes and concept schemes UpperCamelCase
(`wl:DesignDoc`, `wlc:TaskKind`). Snake_case is reserved for `wlc:` concepts
carrying a stored enum value, like `wlc:docker_image` (spelling the CHECK
constraint's value), so spelling says whether a term names schema or data.

## The spec / plan / task model

Spec 025, as implemented by the document store.

- A **spec** is a durable document. Writing or revising one is an ordinary
  claimable task (`kind = 'design'`, renamed from `spec` by 025 §10 and
  widened to any design document) that closes on submission for review, not
  on acceptance, a status transition rather than a task state. "Is the spec
  implemented?" is a coverage query, never a task state — don't create
  long-lived umbrella tasks per spec.
- A **plan** is an executable document; its execution is the set of tasks
  minted when the plan is accepted. 025 §9.2 mints no root row above them,
  grouping them by a reference to the plan document instead — never create a
  free-standing container task; container-ness is inferred from a task's
  `child_of` children (004 §6.1).
- **Groupings are queries, not rows** (025 §1): one plan's tasks = the tasks
  referencing it, everything in a repo set = the project. No sprint concept,
  no container above a plan's tasks — order plans with `blocks`.
- Spec → plan decomposition is always an explicit human act; skills may
  offer it, never perform it unasked.
- **The prompt is minted, the act is not** (025 §15.4). `lode doc submit`
  emits `wl:DocumentSubmitted` and moves no column — the open review task
  *is* "under review" — and accepting a spec emits `wl:DocumentAccepted`.
  The `doc-lifecycle` subscriber turns each into one task (`review` on
  submission, `design` to decompose the spec on acceptance, both carrying
  `about_doc`, suppressed while an open task of that kind already
  references the document): nothing here reviews, accepts or plans
  anything — `lode doc accept` stays the manual, owner-gated commit it was.
