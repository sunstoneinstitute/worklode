---
name: writing-docs
description: Use when creating or editing a Worklode spec, rule or plan, or editing ns/*.ttl — "write a new spec", "add a plan", "lode doc add", "what goes in the frontmatter", "covers vs implements", "NO-SPEC", "renumber the sections", "arrange rules", "edit a rule", "governedBy", "amend a spec", "supersede a rule", "{#sec-N} anchors", "add a wl: property", "SKOS concept", "is spec NNN implemented" — and for the spec/plan/task model (design tasks, minted tasks, why groupings are queries not rows). For splitting one spec across a numbered plan series, use lode:splitting-specs-into-plans instead.
---

# Authoring specs and plans

Documents live in the Postgres backbone, not in the git tree — no
`docs/specs/`, no `docs/plans/`, no file to open, no pre-commit hook.
A spec is an **arrangement of rules**: each anchored section carries a rule
with its own ref, text, status and version history. The arrangement supplies
reading order, depth and anchors. A plan is governed by the rules its `covers`
entries reach and declares the tasks that undertake them; its prose creates
no rules. Accepting the plan gives its minted tasks governing rule links.

Write document bodies with `lode doc`, and edit one rule with `lode rule edit`.
This skill covers authoring syntax. For rule inspection, versions, pins,
lineage and refactor maps, read the `lode:worklode` skill's
`references/specs-and-docs.md`.

## Where a document lives, and how it gets there

Draft the markdown — frontmatter included — in a scratch file, then:

```bash
lode doc lint <file>                                # local lint: anchors, plan ## Tasks
lode doc add --kind <spec-or-plan> --slug <slug> --file <file>   # creates it, draft
lode doc edit <ref> --file <file>            # replace a draft's body, or a plan's at any status
lode doc edit <ref> --file <file> --note "why"   # amend an accepted spec in place (WL-SPEC-77 §10)
lode doc edit <ref> --file <file> --substantive  # same, judged substantive: reviewers are asked again
lode doc revise <ref>                        # open a candidate revision on an accepted doc
lode doc revise <ref> --file <file>          # update the open candidate's body
lode doc revise <ref> --accept               # land the candidate as the doc's next version
lode doc revise <ref> --discard              # withdraw it unlanded: owner or its author
lode doc submit <ref>                        # mints the review task
lode doc accept <ref>                        # owner-gated; a plan's accept mints its tasks
lode doc transfer <ref> --to <actor>         # owner-gated; move ownership to another actor
```

Read one back with `lode show <ref>` (`WL-SPEC-77`, `WL-SPEC-77#sec-11`, or
`-s <anchor>`), or `lode doc show <ref> --json` for the body plus parsed
sections and edges. `lode show <ref> --inline` folds every in-force amendment
and supersession into the section it acts on. `lode doc list`, `lode doc
versions <ref>`, and `lode doc todo <ref>` (what's left before a spec counts
as fully planned and executed) round out the reading surface; `lode doc
lint` with no argument reports the whole corpus's dangling references,
unlike `lode doc lint <file>`, which only lints one local file.

The scratch file is an editor buffer, not a copy of record — nothing reads
it once the command above succeeds. `lode doc edit` replaces a draft's body,
and a plan's at any status (plans are edited in place — WL-SPEC-77 §11). On an
accepted spec it is WL-SPEC-77 §10's in-place amendment, and the server
gates it mechanically: an edit that changes a `wl:`/`wlc:` term, a code
span or fenced block, an acceptance-criteria section, the frontmatter
`requires` list, or a section that open work already points at (§8.2) is
refused, naming the rule. That edit goes through `lode doc revise` instead:
open a candidate, edit it, `--accept` to land it or `--discard` to drop it.
An amendment that passes the gates needs `--note` saying what changed and
why, or `--substantive`, which asks the document's reviewers again and marks
the sections it touched.

**The backbone assigns the number, not you.** Never hand-create a file for a
document, and never read the next number off filenames. The corpus lives in
the backbone (WL-SPEC-77 §16), so a draft that has no file anywhere still holds its
number. `lode doc list` is the authority.

## Author rules, arrange them into specs

Write each anchored section as a rule that can be read on its own: state the
requirement and keep its rationale and exceptions beside it. Reference other
rules by ref when they constrain it. Use the arranging spec for context and
reading order; a plan carries the implementation steps.

```bash
lode rule list --doc <spec-ref> --json       # inspect rule identities before and after editing
lode show <rule-ref>                         # requirement and where it is arranged
lode rule edit <rule-ref> --file <body-file> # body under its heading, optional --heading
```

Every rule has exactly one kind, and each kind states one obligation
(WL-SPEC-77 §4). The kind sets its ref's infix:

| Kind | Ref | Obligation | A reviewer checks it | Planning |
|---|---|---|---|---|
| `requirement` | `WL-REQ-<n>` | a plan builds it once; afterwards its tests hold the behavior | directly against the change | a gap until an accepted plan covers it |
| `catalogue` | `WL-REQ-<n>` | a set of entries with one shape, each answering the same question (command tree, metric table, error table, state list); a plan builds its entries | by membership: the change adds, removes or alters an entry | a gap until an accepted plan covers it |
| `invariant` | `WL-RULE-<n>` | a property that holds in every state, with the checks that enforce it; binds every task in its project | against the state, not the change | never covered, never a gap |
| `definition` | `WL-RULE-<n>` | one term, one meaning, in one scope | whether the term is used as defined | never covered, never a gap |
| `principle` | `WL-RULE-<n>` | a design stance other rules `refine`; not checkable on its own | through its refiners only | never covered, never a gap |

New rules are requirements. Set another kind with
`lode rule set <rule-ref> --kind <kind>`.
A rule an accepted plan covers stays a requirement or catalogue. Rationale is
not a kind: it ends the body of the rule it explains. Background nothing
refines is template text of the spec, not a rule. The number alone names the
rule: `WL-REQ-12`, `WL-RULE-12` and `WL-CL-12` all resolve, and output prints
the current kind's infix. A rule that both builds something and binds later
work is two rules: split it into a requirement and an invariant.

### Edges and the context closure

Link rules with `lode rule link <a> --<edge> <b>` (`lode rule unlink` removes
it):

| Edge | Meaning | In closure |
|---|---|---|
| `--refines` | A narrows B, a principle or wider rule | yes |
| `--needs` | A cannot be applied without a fact or term B states; A does not narrow B | yes |
| `--references` | B helps a reader and A does not need it | no |

Text that names a rule ref also yields a derived `references` edge. The other
edges are `--amends`, `--constrains`, `--conflicts-with` and `--derived-from`.

A rule's **context closure** is the rule plus everything reachable over
`refines` and `needs`. Read it with `lode show <rule-ref> --closure`. A spec
arranges rules, never closures: arrange the context rules too if the spec
should show them.

### Checklist for a new rule

A rule is sized so a reader with it and its closure can decide whether a change
complies. Apply the five sizing tests (WL-SPEC-77 §4c) before submitting:

1. **It decides something.** It is one of the five kinds. Overviews,
   motivation, non-goals, plan indexes and open questions are template text.
2. **It is self-contained over its closure.** No reference by position ("§N",
   "above"); needed context is a `refines` or `needs` edge. Every term it uses
   is a `definition` in its closure or defined in the rule. A fact lives in one
   rule, its owner, and rules that need it `needs` the owner.
3. **It is not too big.** One subject and one check. Storage, API and CLI of one
   feature are three rules, each needing the model rule.
4. **It is not too small.** If it cannot be applied without restating its
   parent and is the parent's only refiner, merge it into the parent.
5. **Arrangement does not pull context.** A rendered spec shows the rules it
   arranges and nothing else.

Two rules stating one fact with different values get a `conflictsWith` edge
until the non-owner is fixed.

### Definitions and the glossary

A `definition` rule holds one term. `lode rule terms [--project <id>]` lists a
project's definitions with their term slugs, and
`lode rule set <ref> --concept <iri>` attaches the `ns/concept.ttl` IRI it
defines (definitions only). Term pages are `/projects/<proj>/term/<slug>`.
Every project has a `glossary` spec of definitions, one term each. The instance
glossary is the `glossary` spec (`WL-SPEC-87`) of the project named by the
server setting `glossary_project` (`--glossary-project`,
`LODE_GLOSSARY_PROJECT`). A term resolves to the project's definition first,
then the instance's; a project definition that narrows an instance term
`refines` it. `lode rule lint [--project <id>]` reports closure sizes,
undefined terms, conflicts and positional references.

The store assigns rule refs when new anchored sections are written. Existing
rules match by anchor and heading, then heading alone, then anchor alone.
Check the arrangement after moving or renaming sections; copying text into a
new spec does not establish shared identity. `lode rule edit` writes only the
rule: it rewrites a draft version or adds the next draft. Specs arranging the
rule keep showing its accepted version, with a pending marker, until the owner
runs `lode rule accept`, which bumps every accepted spec arranging it. A draft
rule version is mutable; accepted text is preserved in version history.

Use `spec` for new durable design, including rationale previously written as
an ADR. Existing ADR references remain valid. Preserve accepted section
anchors even though the rule has its own identity. To withdraw, split or merge
rules, use the explicit lineage/refactor procedure in the reference above;
removing prose alone does not redirect task governance.

## Frontmatter is mandatory

**Every document you create starts with YAML frontmatter — no exceptions.**
A spec needs `status` and, once accepted, `issued`. A plan needs `status`
and `covers` — requirement refs, document/section references, or
whole-document references selecting the requirements it builds, each a
plain reference: a `covers` edge always means the plan builds the whole
requirement, no levels — or
`covers: NO-SPEC` (WL-SPEC-78 §3.3, valid only here) when nothing governs
it, never omitted, since an absent `covers` reads as a forgotten one. See
`lode:splitting-specs-into-plans` for the cases that used to be expressed
with a coverage level: splitting a rule the plan only partly builds,
invariants, definitions and principles.

Keys are ontology property local names (WL-SPEC-77, WL-SPEC-78, WL-SPEC-79), not a second
vocabulary — a key with no term behind it means the ontology is missing one,
not that you should invent one. Order them lifecycle → `covers` → `defers` →
dependency → amendment → supersession:

| Key | On | Meaning |
|---|---|---|
| `status` | spec | `draft`, `accepted`, or `superseded` (`proposed` is retired — a document under review stays `draft`) |
| `issued` | spec | `YYYY-MM-DD` of first publication |
| `covers` | plan | scalar or list of requirement refs (`WL-REQ-<n>`), spec-section references, or whole-document references this plan undertakes to build in full; a section or document entry skips invariants, definitions and principles, a direct ref to one is refused; `coverage:`/`fullCoverageWith:` are refused |
| `implements` | plan | retired spelling of `covers`; still parses, reported as retired. A document carrying both is an error |
| `defers` | plan | list of `{spec, to}`: a section this plan hands off, and the document expected to cover it (WL-SPEC-78 §4.2) |
| `requires` | any | list of references; plain dependency, no ordering semantics |
| `blockedBy` | plan | list of plans whose whole execution runs before this one's (WL-SPEC-77 §8), declared on the later plan |
| `isRequiredBy`, `blocks` | none | inverse spellings, not keys: a header carrying one is refused, naming `requires` or `blockedBy` (WL-SPEC-77 §8.1) |
| `wasDerivedFrom` | spec | scalar reference (provenance) |
| `amends`, `amendedBy`, `replaces`, `isReplacedBy` | none | not keys: a header carrying one is refused. Amendment and supersession are rule edges, see below |
| `artifact` | any | catalog address(es) (`bigquery://…`, `gs://…`) this document is verified by (WL-SPEC-75 §13.3); declares additively |

A retired `task` key once named the lode task a plan's execution hung off; it
still parses (plan bodies are stored verbatim) but nothing reads it — find a
plan's minted tasks with `lode task list --plan <plan>`.

## References resolve by slug, not filename

A reference names a document, and there is no file for it to point at.
Resolution tries, in order: an **exact slug match** in the project (the bare
slug from `lode doc add --slug`, e.g. `covers: execution-backbone`); the
**`WL-SPEC-N` shorthand** (`<PROJECTKEY>-SPEC|PLAN-<n>`, WL-SPEC-78 §3.2, e.g.
`WL-SPEC-77`, `WL-PLAN-7` — the only form that crosses projects,
e.g. `CMS-SPEC-4` from inside `WL`); then a **bare corpus number** (`25`, not
`025`), only when nothing else matched and exactly one live spec
carries it. Append `#sec-N` to any of these to narrow to a section. The `adr`
kind is retired: every former ADR is a spec, and a `<KEY>-ADR-<n>` ref
resolves to that successor spec (WL-SPEC-77 §7a).

**A filename does not resolve.** `042-secret-templates.md` is neither a slug
nor a bare number — the trailing text after the digits makes matching it to
spec 042 a risky guess, so the number arm refuses it. It doesn't error, it
just silently fails to resolve and sits as an unresolved external ref
instead of an edge. This exact mistake has already produced dangling edges
in the corpus — check `edges_in` on `lode doc show <ref> --json`, or run
`lode doc lint`, rather than assume a `covers:`/`requires:` line did what you
meant. (A cross-project shorthand naming a project this instance can't
reach is `unresolved` too, but for a different reason: nothing in the
referring project can repair that one.)

## Section anchors

Every numbered heading in a spec carries a `{#sec-N}` anchor —
`## 2. Lease lifecycle {#sec-2}`, `### 2.1 Renewal {#sec-2.1}`. Depth is
capped at 3 levels (H2/H3/H4); a heading deeper than that is legal content
but takes no number and no anchor — it belongs to its nearest anchored
ancestor. `lode doc lint <file>` checks numbering, anchors, and depth
before you post, and `--update-section-anchors` on `lode doc add` or `lode
doc edit` fixes what it reports: it renumbers the headings and rewrites their anchors from the
structure, so inserting a section does not mean editing every number below
it by hand. It is refused on an accepted document, for the reason below.

**An anchor is frozen once its document is `accepted`.** A revision that
renumbers a published anchor, or drops one without a replacing supersession,
is refused at `lode doc revise --accept` — the backbone's own append-only
rule (WL-SPEC-77 §9), not a linter you can skip. To insert a section between
`2.1` and `2.2` on an accepted document, use a **letter suffix**:
`### 2.1a New section {#sec-2.1a}`, which takes no counter slot and tells a
reader it was added after acceptance. Adding a genuinely *missing* anchor to
an already-correctly-numbered section is fine. A **superseded section keeps
its heading and anchor** — never delete it — with a note saying what
replaced it; a bare superseded section is a broken promise to whoever
linked it.

## Amendment and supersession

Use a rule edit or a candidate spec revision when changing the owning spec's
requirement. Documents do not amend or replace each other: rules do
(WL-SPEC-77 §4). A header carrying `amends`, `amendedBy`, `replaces` or
`isReplacedBy` is refused.

**Amending** changes how a rule is read without replacing its text.
`lode rule link WL-REQ-A --amends WL-REQ-B` records that rule A amends rule
B, and `lode rule unlink` removes it. `lode show <ref> --inline` reads the
result: a section's own text plus every in-force amendment of its rule,
attributed to the amending rule.

**Superseding** runs through `lode rule supersede --map <file>`: it withdraws
the old rules and writes a `supersedes` edge from each successor. A document
is superseded when all its rules are withdrawn, and accepting a document
supersedes every document whose rules are all withdrawn and superseded by
rules it arranges. `lode doc list --bare-superseded` lists withdrawn rules
with no successor.

## Declaring a plan's tasks

A plan's `covers` names requirement refs, spec sections, or whole documents.
The store resolves each entry to requirements when the plan is written and
stores a `covers` edge from the plan to each one, then gives every minted
task `governedBy` links to that set. The project's accepted invariants govern
every task as well, derived when the task is read. Verify the result with
`lode rule list --doc <plan-ref>` and `lode show <task-id> --json`.

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
with no task yet, leaving every existing task alone (WL-SPEC-77 §11.2). Append a
declaration to add work to an accepted plan; retitle one only to withdraw
that task and declare a new one. Ordering across files (series parts, other
plans) is the document-level `blockedBy` above, never a task
number — `lode doc lint <file>` runs this whole parse first, so a
malformed task block is caught locally.

**A plan may legitimately declare no tasks.** A *coverage-only* plan records
coverage for work already built: it carries at least one `covers` or `defers`
entry and no tasks at all, and accepting it mints nothing. Acceptance is still
what puts the record in force, since the aggregate coverage query counts
accepted plans only (WL-SPEC-78 §1). "No tasks at all" is exact — no `## Tasks`
heading, and no heading opening with `Task`/`Tasks` followed by a number. An
empty `## Tasks` section, or a `## Task 1` heading that missed the em-dash
format above, is an authoring mistake and still fails both the lint and the
accept (WL-SPEC-77 §11.2).

## After editing an accepted spec or plan, check what it invalidated

**A minted task's body is a snapshot.** `lode doc accept` copies each
declaration's prose into the task it mints and never looks again, so editing
the document afterwards leaves every existing task carrying the old text.
Re-accepting does not repair it: WL-SPEC-77 §11.2 mints only declarations with no row
yet and never mutates a body. Nothing reports the divergence, so the edit is
not finished until you have walked it yourself:

```bash
lode task list --plan <slug> --status all --json   # every task the plan minted
lode show <task-id> --json                         # the body as minted
lode task edit <task-id> --body-file <file>        # re-body a drifted one
```

Compare each `### Task N` declaration against its task. The join is exact, not
a guess: a minted task records `plan_doc` and `plan_task_key` (the declaration
title), unique together, so a task renamed during execution still points at
the declaration it came from.

The judgment is yours, and it splits on task state:

- **Open (`ready`, `claimed`, in flight)** — re-body it. Someone is about to
  work from prose you now know is wrong.
- **Closed (`deployed_dev`, `merged`, abandoned)** — leave it. A finished
  task is execution fact, and a title naming the wrong migration number is
  history, not an error to correct.

Two failure modes worth checking for by hand, since no linter covers them:
**repo paths that stopped resolving** (a deleted doc, a renamed script) —
`lode doc lint` checks doc-to-doc references only — and **numbers assigned
downstream**, migrations above all, where `./scripts/check-migrations.sh`
renumbers on collision and what shipped is rarely the number the plan named.

For a spec or rule edit, inspect the rule's `arranged_in` and `governed_tasks`
with `lode show <rule-ref> --json`. Check task pins and `resolves_to`
successors, and any plans marked stale by a refactor. Use `lode doc todo
<ref>` for remaining planning gaps. Each affected plan has minted task bodies
to compare; a new rule version does not rewrite those snapshots.

## The `ns/` ontology

`ns/` holds the `wl:` ontology extracted from WL-SPEC-77, WL-SPEC-78 and WL-SPEC-79 —
`ontology.ttl` (classes, properties, axioms), `concept.ttl` (SKOS enums),
`shapes.ttl` (SHACL) — the vocabulary the frontmatter keys come from and the
parseable form; the specs' own Turtle blocks are illustrative and don't
parse. `ns/` owns the shared schema, the specs own the rationale (WL-SPEC-77 §14):
amend the spec first, then mirror the term here (`riot --validate
ns/*.ttl`); never edit `wlc:TaskKind` apart from the migration and
`validKinds`, which a test holds together.

Term names are camelCase: `wl:` properties lowerCamelCase
(`wl:coveringPlan`), classes and concept schemes UpperCamelCase
(`wl:DesignDoc`, `wlc:TaskKind`). Snake_case is reserved for `wlc:` concepts
carrying a stored enum value, like `wlc:docker_image` (spelling the CHECK
constraint's value), so spelling says whether a term names schema or data.

## The spec / plan / task model

WL-SPEC-77, as implemented by the document store.

- A **spec** is a durable document. Writing or revising one is an ordinary
  claimable task (`kind = 'design'`, renamed from `spec` by WL-SPEC-77 §12 and
  widened to any design document) that closes on submission for review, not
  on acceptance, a status transition rather than a task state. "Is the spec
  implemented?" is a coverage query, never a task state — don't create
  long-lived umbrella tasks per spec.
- A **plan** is an executable document; its execution is the set of tasks
  minted when the plan is accepted. WL-SPEC-77 §11.2 mints no root row above them,
  grouping them by a reference to the plan document instead — never create a
  free-standing container task; container-ness is inferred from a task's
  `child_of` children (WL-SPEC-75 §5).
- **Groupings are queries, not rows** (WL-SPEC-77 §1): one plan's tasks = the tasks
  referencing it, everything in a repo set = the project. No sprint concept,
  no container above a plan's tasks — order plans with `blockedBy`.
- Spec → plan decomposition is always an explicit human act; skills may
  offer it, never perform it unasked.
- **The prompt is minted, the act is not** (WL-SPEC-75 §9.6). `lode doc submit`
  emits `wl:DocumentSubmitted` and moves no column — the open review task
  *is* "under review" — and accepting a spec emits `wl:DocumentAccepted`.
  The `doc-lifecycle` subscriber turns each into one task (`review` on
  submission, `design` to decompose the spec on acceptance, both carrying
  `about_doc`, suppressed while an open task of that kind already
  references the document): nothing here reviews, accepts or plans
  anything — `lode doc accept` stays the manual, owner-gated commit it was.
