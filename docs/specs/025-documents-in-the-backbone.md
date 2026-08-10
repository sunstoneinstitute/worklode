---
status: accepted
issued: 2026-08-02
requires:
  - 004-execution-backbone.md
  - 006-knowledge-graph.md
  - 014-design-documents-as-graph-objects.md
  - 018-task-hierarchy.md
amends:
  "#sec-2":
    - 014-design-documents-as-graph-objects.md#sec-0
    - 014-design-documents-as-graph-objects.md#sec-4
    - 014-design-documents-as-graph-objects.md#sec-10
  "#sec-3":
    - 014-design-documents-as-graph-objects.md#sec-5
  "#sec-4":
    - 014-design-documents-as-graph-objects.md#sec-2
  "#sec-4.1":
    - 016-org-wide-skills.md#sec-1
  "#sec-6":
    - 006-knowledge-graph.md#sec-1.5
    - 018-task-hierarchy.md
  "#sec-8":
    - 006-knowledge-graph.md#sec-1.1
    - 006-knowledge-graph.md#sec-1.2
    - 006-knowledge-graph.md#sec-1.3
amendedBy:
  "#sec-2":
    - 034-design-doc-sync.md#sec-7
  "#sec-3":
    - 027-event-watchers.md#sec-5
  "#sec-10":
    - 027-event-watchers.md#sec-6
replaces:
  "#sec-6":
    - 014-design-documents-as-graph-objects.md#sec-8
---
# Spec 025 — Documents in the backbone

## 0. Why {#sec-0}

Spec 018 minted `kind = 'epic'` to stand in for a plan inside the task graph: a row that is
never claimable, holds no commit, and whose state is computed from its children. That is a
grouping wearing the `tasks` table's schema, and it exists because plans had no representation
of their own — their content lived in git files, their execution arrived as a flat list of
unrelated tasks, and something had to hold the set together.

This spec removes the epic by removing its reason to exist. Documents — specs, ADRs, plans —
become first-class objects in the backbone, authored and reviewed there, with git files as a
transitional mirror only. A plan's acceptance mints its tasks directly, each referencing the
document, so the container is the plan itself rather than a proxy row. The remaining jobs the
epic might have done are either already owned (cross-plan "ships together" is `wl:Milestone`,
reserved since 006) or were never legitimate rows at all (§1).

Alongside the epic, this resolves three collisions found on the way:

- `wl:Project` (006: "bounded, goal-oriented") contradicts the backbone's `projects`, which are
  unbounded umbrellas. The backbone wins (§8).
- The `--kind spec` task was drifting toward "umbrella open while the spec is unimplemented",
  which stores a coverage query as a row. Spec-kind tasks are authoring work only (§6).
- Plan documents carry `- [ ]` checkboxes — execution state, ticked in git — while tasks also
  track execution state. Two owners of one fact; the plan's task set ends it (§5).

## 1. Principle: rows are things someone made; groupings are queries {#sec-1}

A row exists because an act created it: a task row because work was defined, a document row
because an artifact was authored, a `child_of` edge because one decompose transaction wired it.
Everything cross-cutting is derived — a plan's task set, a spec's coverage, a milestone's
membership, "all plans derived from spec N with unfinished work". This is 014 §6's "coverage is
computed, never stored", applied to execution grouping.

The rule decides concrete cases:

| Candidate row | Verdict |
|---|---|
| Epic grouping one plan's tasks | Query over the tasks' document reference — row deleted |
| Plan root task | Query — same grouping, same verdict; the plan document is the only identity the set needs (§5) |
| Spec-level container over its plans | Query over `implements` + the plans' task-set states — never minted |
| Spec-umbrella task, open while unimplemented | Coverage query — never minted |
| Sprint / iteration container | Never minted; time-boxing is ranking and deadlines |
| Milestone | Row (declared intent: these deliverables ship together), membership derived |

## 2. Documents move into the backbone {#sec-2}

> **Amended by 034 §7.** The one-time corpus import that precedes deleting the git trees becomes
> an ongoing git→backbone sync (034): the corpus is populated into the store continuously while
> git stays the authoring surface, and the files are deleted only once backbone authoring (§3, §5)
> lands. The end state — backbone-authored documents, git files gone — is unchanged.

014 established documents as durable, section-addressable, status-gated objects that are not
git files, and placed them in the knowledge graph. The logical model stands; the authoritative
store moves. Documents are authored in the backbone — Postgres rows, wrapped in the same
event-logged transaction machinery as tasks — and the graph receives them by projection,
exactly as it receives tasks (006 §6). One authoring path, one review surface, one place
`lode` talks to.

- `docs`: identity, kind (`spec | adr | plan`), title, body, editorial status (§3), frontmatter
  as columns (`issued`, `requires`, `wasDerivedFrom`, …), version counter.
- `doc_sections`: anchor, heading, depth, `last_revised_in` — specs and ADRs only (§4).
  Anchors follow 014 §3 unchanged: assigned at first acceptance, frozen, letter-suffix inserts.
- `doc_edges`: `implements`, `amends`/`amendedBy`, `replaces`/`isReplacedBy`, section-scoped
  where the ends are sections. Both directions stored, checked for agreement (014 §11). Plans
  additionally take `blocks`/`blockedBy` between whole documents — the ordering edge that would
  otherwise need a container row to attach to (§7).

014 §4's canonical + versioned IRIs, `dcat:hasVersion` shape and `wl:lastRevisedIn` survive as
the **projection** of this store; its named-graph publication transaction becomes an ordinary
backbone transaction followed by projector catch-up. The §7 constraints of 014 (append-only
anchors, no renumbering, superseded-with-explanation) are enforced at accept time by the
server instead of a SPARQL-side gate.

**Transitional:** `docs/specs/` and `docs/plans/` stay in git, checked by `secfmt.py`, until
this spec is implemented and the corpus is imported. From that point the backbone is the store
of record.

**Files after import — opt-in sync, not mandatory deletion.** A repository may keep
`docs/specs/` and `docs/plans/` as a git-tracked mirror of the backbone rather than deleting
them. This is opt-in per repository through a `[doc_sync]` block in `.worklode/config.toml`
naming the spec and plan directories (§10). With it, `lode doc pull`/`push` reconcile the files
against the backbone; without it, the files are deleted at import and `pull`/`push` are no-ops.
Because the backbone has no branches for documents, sync is directional: **`push` (file→api)
runs only from the default branch**, while **`pull` (api→file) runs from any branch** — a
feature branch reads the canonical document, it never writes one. When both the file and the
backbone doc have changed since the last sync, the command refuses and reports rather than
overwriting. Either way `.worklode/implements.yaml` (014 §6) is unchanged — the claim "this
commit satisfies §X" is a property of the commit and stays in git.

The authority sentence in 000 §1 updates accordingly: the backbone owns execution facts **and
design-document artifacts**; the graph owns the derived, queryable view of both plus the
architecture model around them. Still no fact with two owners — the graph's copy is a
projection, never an authoring surface.

## 3. Editorial lifecycle {#sec-3}

> **Amendment pending (spec 027 §5, draft).** Minting a task that asks for review or planning
> is not the act it asks for, so the watchers do not breach "acceptance and decomposition are
> deliberate human acts". Stated below; it becomes effective when 027 is accepted.

```
draft ──(manual accept, assignee only)──▶ accepted ──▶ superseded
```

`proposed` leaves the scheme (it was already reduced to "editorial" by 014 §5, which removed
`implemented`). A document under review is a **draft with an open review task** — review is
work, tracked as a `kind = 'review'` task against the doc, with crit anchoring comments to
sections. Storing "under review" as a status would duplicate what the open task already
proves. 014 §5's revision flow keeps its shape — a candidate version against a stable
identity, accepted version authoritative throughout — with the candidate carrying `draft`
rather than `proposed`.

Acceptance is always a deliberate human act (`lode doc accept`), never automatic and never a
side effect. The same applies one step later: decomposing an accepted spec into plans is
explicitly chosen — skills may *offer* the step, never take it.

Minting a task that *asks for* review or planning is not the act it asks for, so 027's
watchers do not breach this: `lode doc submit` emits an event that mints the review task, and
acceptance mints a task to decide how the spec becomes plans. Whether it becomes one plan or
four, and what they say, remains entirely the assignee's — and neither watcher ever accepts a
document.

## 4. Plans are documents {#sec-4}

014 §2 demoted plans out of the document model because a plan must stay freely mutable — the
section lock that protects an accepted spec would be harmful on a document that is rewritten
mid-execution. That argument holds and is preserved. What it does not require is that plans
have no document form at all, and the review requirement decides the question: plans get the
same draft → review → accept gate as specs (today provided by PR review, which dies with the
files), and review machinery — comment anchoring, revision tracking, the accept gate — must
not be built twice, once for `docs.body` and once for `tasks.body`.

So plans return to the document store as a **sibling of DesignDoc, not a subclass**:

```turtle
wl:Plan a owl:Class ;
    rdfs:subClassOf foaf:Document , prov:Entity ;   # NOT wl:DesignDoc
    wl:layer wlc:execution ;
    rdfs:comment "An executable document: a bundle of task definitions with instructions
        attached. Reviewable and accept-gated like a DesignDoc, but spent once executed,
        freely mutable, and carrying no frozen section anchors — nothing may pin a claim
        to a plan (014 §2's argument, preserved). Acceptance mints its tasks (025 §5)." .
```

Plan documents take no `doc_sections` rows and no anchors. Nothing addresses into a plan;
`implements` on a plan names spec sections, exactly as plan frontmatter does today.

### 4.1 Task declarations — the `## Tasks` section {#sec-4.1}

A plan body declares the tasks its acceptance mints (§5) in exactly one `## Tasks` section,
containing nothing but one `### Task <N> — <title>` subsection per task; prose sitting
directly under the heading, outside any task, fails the accept rather than being dropped — it
is text an author meant for some task's body, and losing it silently is the same failure as
silently dropping an unknown key. `N` enumerates from 1
in document order, without gaps, **within each plan file**: a plan series shares no sequence —
every part restarts at 1 — and `N` never crosses a file. Each subsection opens with a fenced
YAML metadata block, then prose (what to do, which files, the test that proves it), then an
optional `- [ ]` step list. Canonical shape:

````markdown
## Tasks

### Task 1 — Short imperative title

```yaml
kind: feature            # feature | bug | chore | design
priority: medium         # critical | high | medium | low
skills:                  # skills the executing agent loads before starting
  - superpowers:test-driven-development
blockedBy: [ ]           # task numbers within this plan
```

Prose: files to touch, the test that proves it.

- [ ] step
- [ ] step
````

The metadata keys, like frontmatter keys, name the backbone column or ontology term that
receives them; an unknown key is an accept error, never silently dropped:

| Key | Required | Default | Value → destination |
|---|---|---|---|
| `kind` | yes | — | one of `feature`, `bug`, `chore`, `design` → `tasks.kind`, projected as `wl:taskKind` |
| `priority` | no | `medium` | one of `critical`, `high`, `medium`, `low` → `tasks.priority` (ranking input, spec 005; backbone-only, not projected) |
| `skills` | no | none | list of skill identifiers → the task pin of 016 §3, projected as `wl:requiresSkill` (below) |
| `blockedBy` | no | none | list of task numbers in this file → `blocks` edges between the minted tasks |

`kind` takes the subset of `wlc:TaskKind` (§6) a plan may mint: `review` tasks are created by
the review lifecycle (§3), and a spike's outcome is an input to planning, so both are
authored outside plans.

The task's **title** is the heading text after `Task <N> — ` (em dash, required, non-empty)
and becomes `tasks.title`. Everything between the metadata block and the next task heading —
or the section's end — becomes `tasks.body` verbatim. The step list is part of that body:
executor guidance, never execution state — nothing reads the boxes, and the task's state
remains the only execution state (§5).

**`blockedBy` becomes edges at mint.** For each number `m` it lists, the accept transaction
(§5) wires a `blocks` edge from this plan's task `m` to the declaring task — `task_edges`
rows, backbone-authoritative (004), projected as `wl:blocks` / `wl:dependsOn`. A number
naming no task in this file, a self-reference, or a cycle fails the accept, naming the tasks
at fault. Ordering across the files of a plan series, like all cross-plan ordering, is the
document-level `blocks` edge (§2, §7) — never a task number.

A **skill identifier** is the org skill-registry name (016 §1), written in the
`plugin:skill` form the skill repos already use (`superpowers:test-driven-development`) when
the skill ships in a plugin; resolution falls back to the segment after the colon where the
registry name is unqualified. The fallback is a **second** attempt, never a first: an exact
match on the written identifier wins outright and the after-colon form is not consulted, so a
registry holding both `superpowers:tdd` and a bare `tdd` resolves the qualified pin to the
qualified row. Accept does not require the name to resolve — a pin naming an
unknown skill is a brief warning, never a failure (016 §3). The key is `skills`, matching the
doc-frontmatter pin key and the backbone Task field, both from 016 §3; the projected term is
minted here, amending 016 §1's mint set:

```turtle
wl:requiresSkill a owl:ObjectProperty ;
    wl:layer wlc:execution ;
    rdfs:domain wl:Task ; rdfs:range wl:Skill ;
    rdfs:comment "The executing agent loads this skill before starting: the task pin of
        016 §3, projected from the backbone Task's skills field and minted onto the task by
        plan acceptance (025 §4.1). Brief resolution unions it with the governing design's
        wl:recommendsSkill; the two stay distinct because their owners differ — the pin on a
        task is execution-layer fact, the pin in a design document a declared intent." .
```

Until this spec is implemented, plans under `docs/plans/` carry the same section and humans
mint the tasks from it by hand; the format is identical so acceptance can take the minting
over unchanged.

## 5. Acceptance mints the plan's tasks {#sec-5}

`lode doc accept` on a plan runs one transaction: create the tasks its `## Tasks` section
declares (§4.1), `draft`, each carrying a reference to the document, and wire the declared
`blockedBy` numbers as `blocks` edges between them. **Nothing is minted above them.** The invariant is
`doc.status = accepted ⟺ its tasks exist`, by construction, with nothing to keep in sync by
hand.

The invariant binds `lode doc accept`, which is the only act that mints. Two cases sit outside
it, both deliberately:

- **Historical import.** Backfilling a plan whose execution already happened — or never will —
  records the document at the status it actually reached and mints nothing. The importer is
  not `doc accept`; a plan can therefore arrive `accepted` with an empty task set, and that is
  a faithful record rather than a broken invariant. Read the invariant as scoped to plans
  accepted through the verb.
- **Re-acceptance after an edit.** An accepted plan stays freely mutable (§4), so re-accepting
  one mints the task declarations that have no row yet and leaves every existing row alone —
  never mutating a body, never deleting a task whose declaration disappeared. A minted task is
  execution fact and outlives the declaration it came from; withdrawing work is a task
  transition, not a document edit.

A plan's task set is the query `tasks WHERE plan_doc = <doc>` — §1's rule applied to the case
that most tempted a row. A root task would own no fact of its own: its state was computed from
its children (018 §3.3), its body restated the document's, it was never claimable and never
held a commit. With the plan itself a real object in the same store, everything a root carried
is either **on the document** (identity, title, body, editorial status, ordering) or **derived
from the task set** (progress, completion). Minting it would store a grouping as a row, which
is what §0 removes the epic for.

Two acts, three things, each owning one fact:

| Entity | Owns | Created by |
|---|---|---|
| Authoring task (`kind = 'design'`) | The work of writing the plan | whoever picks up the planning |
| Plan document | Content, editorial status, and the identity of the set | the authoring work |
| Tasks + their `plan_doc` reference | Execution state | the accept transaction |

`plan_doc` is nullable: tasks that no plan authored — an inbox promotion, a one-off chore —
carry none, and their absence from every plan's task set is the correct answer rather than a
special case.

**What this leaves of 018.** The `child_of` machinery survives, narrowed to its other job:
decomposing an oversized task into subtasks (018 §8), which stays the only way a task acquires
children. Its guards — never claimable, excluded from the ready set, delivery states forbidden,
closure by roll-up in both directions, progress derived on read, single parent, cross-project
children rejected, brief showing one hop up — all still apply, but now to *a task that has
children* rather than to a declared kind. 018 §1 chose declared over inferred because an epic
had to exist before its children did (`task add --kind epic` created an empty one); with that
path gone and `decompose` creating parent-hood and children in the same transaction, there is
no window in which the two disagree, and the predicate "has children" is exactly as sharp as a
column. The depth cap of 2 (018 §3.2) is unchanged and no longer half-spent on a container:
task → subtask is the whole of it.

The plan-doc `- [ ]` checkbox convention retires with the files: the tasks' state is the only
execution state.

## 6. Task kinds {#sec-6}

```sql
-- target CHECK; ships with the concept.ttl edit and regenerated code, atomically
CHECK (kind IN ('feature','bug','chore','design','review','spike'))
```

- **`epic` is removed.** No jobs remain: one-plan grouping is the document reference (§5),
  cross-plan grouping is `wl:Milestone` (§8), everything else is a query (§1).
- **`spec` is renamed `design`** and widened to every document kind: *author or revise a
  Worklode document (spec, ADR, or plan); the document produced is reachable via
  `prov:wasGeneratedBy`* (014 §9's mechanism, unchanged). A design task is claimable, real
  work, and closes when its document is accepted — it is never an umbrella held open against
  coverage. "Is the spec implemented?" is `lode doc coverage`, not a task state.
- **No structural kind replaces `epic`.** With no container row minted for a plan (§5), the
  only remaining container is a decomposed parent, and its container-ness follows from having
  children. So 014 §8's ruling — task kinds describe the nature of work, and no kind is added
  for plans — stands rather than being overturned, and the scheme is left with six kinds that
  all mean the same sort of thing.

Migration: `UPDATE tasks SET kind = 'design' WHERE kind = 'spec'`; forbid `epic` (no rows
exist); swap the CHECK; regenerate `validKinds` and `wlc:TaskKind` from `ns/` (§9) in the same
commit so `TestTaskKindsAgreeAcrossSources` never sees the sources disagree.

## 7. Spec fan-out is a query {#sec-7}

A spec producing several plans gets no container above their tasks. Each need is owned:

| Need | Owner |
|---|---|
| "How far along is spec N?" | `lode doc coverage` — accepted sections × plans × task-set states |
| "Which specs need planning?" | accepted sections not named by any accepted plan's `implements` |
| "Which plans need execution?" | accepted plan docs whose task set is unminted or unfinished |
| Plan B after plan A | `blocks` edge between the two plan documents (§2) |
| "These land together" | Milestone over their deliverables (§8) |
| Task Y waits on part of spec N | Y blocks on the plan document(s) covering that section, or on individual tasks when the wait is narrower |

The document-level `blocks` edge is what makes dropping the root affordable: ordering is a
statement about plans, and a plan is now a real object, so the edge attaches to it directly
instead of to a proxy task. The ready-set query expands it — a task is blocked while any task
in a blocking plan's set is open — which is the same predicate 005 §3 already runs, evaluated
over a set rather than a row.

"All of spec N is done" is not a completion event — coverage grows under amendment — so no row
may claim to be it. And with no container level to spend, the depth cap of 2 covers task →
decomposed subtask alone; a plan- or spec-level parent would spend it again for nothing.

## 8. Project, Workstream, Milestone {#sec-8}

The backbone's `projects` are unbounded umbrellas over sets of repos (`project_repos` already
models the set), and that is what `wl:Project` now means; 006's "bounded, goal-oriented"
definition loses. With the bounded variant gone, `wl:Workstream` has a single subclass and
collapses; `wl:OngoingMaintenance` is redundant once the umbrella is inherently ongoing.

Target `ns/` state (applied at implementation, §9):

```turtle
wl:Project a owl:Class ;
    wl:layer wlc:execution ;
    rdfs:comment "The umbrella for all work in a set of repositories — the backbone's
        projects table, verbatim. Unbounded. NB: doap:Project is a single repository;
        a wl:Project owns 1..n of them (project_repos)." .

wl:inProject a owl:ObjectProperty, owl:FunctionalProperty ;
    wl:layer wlc:execution ;
    rdfs:domain wl:Task ; rdfs:range wl:Project ;
    rdfs:comment "Derived from tasks.project_id: every Task is in exactly one Project.
        Replaces wl:inWorkstream. Projection named graphs are anchored per Project." .

# deleted: wl:Workstream, wl:OngoingMaintenance, wl:inWorkstream,
#          the (Project OngoingMaintenance) disjointness axiom.
# shapes: the Task shape's inWorkstream minCount-1 becomes inProject exactly-1.
# disjointness: Workstream's slot in the top-level axiom is taken by wl:Project.
```

`wl:Milestone` stays reserved for v2 exactly as 006 §1.1 and 003 leave it, and its shape is
confirmed by this spec's principle: a milestone **groups Deliverables** — the declared intent
that a set of outcomes ships together, typically ending with a release. Task membership is
derived through the existing `wl:implements` (whose range already includes Deliverable), so
milestone progress is a coverage query and no task→milestone edge is ever stored. Bounded
short-horizon grouping needs no further concept: one intent's tasks are its plan's task set; a
cross-plan set that must land together is a (small) milestone. A calendar-boxed sprint
container is deliberately unrepresentable.

## 9. `ns/` is the schema source {#sec-9}

Shared schema — classes, properties, SKOS enums, SHACL shapes — is owned by `ns/*.ttl`; specs
own the rationale and cite the terms. Where today the Turtle mirrors the specs by hand, a
**Python codegen step** (rdflib; Go's RDF ecosystem is not worth fighting) reads `ns/` and
emits the Go constants, SQL `CHECK` fragments and validation tables that currently drift
apart. Generated artifacts are checked in; CI regenerates and fails on diff. A schema change
is therefore one commit touching the Turtle, the generated code, and the migration together —
`ns/` edits stop being a documentation chore and become the change itself.

## 10. Surfaces {#sec-10}

> **Amendment pending (spec 027 §6, draft).** `lode doc submit` is 027's verb; it is listed
> here because the review task it mints is what replaces a `proposed` status (§3). The entry
> becomes effective when 027 is accepted.

014 §10's reserved surface, now backed by the backbone store, plus the verbs this spec adds:

```
lode doc new --kind spec|adr|plan     author a draft (skill-guided)
lode doc submit <id>                  hand a draft to review; mints the review task (027 §5)
lode doc show <id> [--resolved]       --resolved inlines amendments and supersessions
lode doc accept <id>                  the manual commit; on a plan, mints its tasks (§5)
lode doc list --needs-planning        accepted specs with unplanned accepted sections
lode doc list --needs-execution       accepted plans whose task set is unminted or unfinished
lode doc coverage <id>                per-section implemented / unimplemented / stale
lode doc revise | anchors             as 014 §10
lode doc pull [<id>|--all]            api→file mirror; any branch; no-op unless [doc_sync] set
lode doc push [<id>|--all]            file→api; default branch only; no-op unless [doc_sync] set
```

`pull` and `push` back the opt-in file mirror of §2, gated on a `[doc_sync]` block in
`.worklode/config.toml`; with no such block they do nothing. `push` refuses off the default
branch (the backbone has no doc branches) and both refuse on a since-last-sync conflict rather
than overwrite. `push` reconciles a file through the same `revise`/`accept` path as any edit,
so the event log and 014 §7's anchor-freeze and supersession rules still apply — it is not a
raw body overwrite.

The lode plugin ships skills for the guided flows — authoring a spec, running its review,
accepting, offering (never assuming) decomposition into plans, and plan review — so the
ceremony lives in skills while every state change is one of the deterministic verbs above.

## 11. Amendments to existing specs {#sec-11}

| Spec | Change |
|---|---|
| 000 | §1 authority split: backbone owns doc artifacts; graph is projection (§2) |
| 006 | §1.1/§1.2/§1.3: Workstream/OngoingMaintenance/inWorkstream out, Project redefined, inProject in (§8); §1.5 TaskKind: `epic` out, `spec`→`design` (§6) |
| 014 | §0/§4/§10 store moves to backbone, graph becomes projection (§2); §5 `proposed` dropped (§3); §2 plans return as non-DesignDoc documents (§4); §8 superseded, though its "no kind for plans" ruling survives the replacement (§6) |
| 016 | §1's mint set gains `wl:requiresSkill` (Task → Skill): the task pin, backbone-only until now, becomes projectable and plan-declarable (§4.1) |
| 018 | §1's declared container narrows to "has children", the only path being `decompose`; `task add --kind epic` and `--kind` on `task parent` go; roll-up, ready-set exclusion, single parent, depth cap intact (§5, §6) |
| Migration | Kind swap (§6); `docs`/`doc_sections`/`doc_edges` (§2); nullable `plan_doc` on tasks |
| CLAUDE.md / authoring docs | Ownership sentence, spec→plan→task model, `task:` key retirement |

## 12. Out of scope {#sec-12}

- **Corpus import** — moving the existing 25 specs and 40 plans into the store, anchor
  assignment for legacy prose, and deleting the files (or keeping them as a §2 sync mirror).
  Follows 014's "adoption" boundary; it
  is the implementation plan's final phase, not design.
- **Milestone implementation** — v2, as reserved; only its shape is pinned here (§8).
- **Graph projection of docs** — the projector work belongs to 006 §6's existing contract.
- **Review tooling internals** — crit integration details; this spec fixes only that review
  targets documents.

## 13. Acceptance criteria {#sec-13}

1. `epic` is absent from the CHECK, `validKinds`, and `wlc:TaskKind` and no structural kind
   replaces it; `design` is present in all three; the sources are generated from `ns/` and CI
   fails on drift.
2. Accepting a plan document mints its tasks and their `plan_doc` references in one
   transaction and creates no row above them; the set is reachable only by query, and
   `doc.status = accepted ⟺ the tasks exist` holds in both directions.
3. `lode task decompose` is the only path by which a task acquires children; the container
   guards apply exactly while a task has them, with no kind to declare.
4. A spec with two accepted plans has no row above either plan's tasks, and a plan-to-plan
   `blocks` edge orders them; `lode doc coverage`, `--needs-planning` and `--needs-execution`
   answer from queries alone.
5. A document under review is `draft` with an open review task; `proposed` appears nowhere;
   `lode doc accept` is manual and assignee-gated.
6. Plan documents carry no section anchors and accept mid-execution edits; spec documents
   enforce 014 §7's anchor constraints at accept time.
7. `wl:Workstream` and `wl:OngoingMaintenance` are absent from `ns/`; every projected Task
   carries exactly one `wl:inProject`; no `wl:` term for sprints exists.
8. No stored task→milestone edge exists when Milestone ships; its task set derives via
   Deliverables.
