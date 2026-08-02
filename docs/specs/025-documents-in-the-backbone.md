---
status: draft
issued: 2026-08-02
requires:
  - 004-execution-backbone.md
  - 006-knowledge-graph.md
  - 014-design-documents-as-graph-objects.md
  - 018-task-hierarchy.md
amends:
  "#sec-2":
    - 000-umbrella-architecture.md#sec-1
    - 014-design-documents-as-graph-objects.md#sec-0
    - 014-design-documents-as-graph-objects.md#sec-4
    - 014-design-documents-as-graph-objects.md#sec-10
  "#sec-3":
    - 014-design-documents-as-graph-objects.md#sec-5
  "#sec-4":
    - 014-design-documents-as-graph-objects.md#sec-2
  "#sec-6":
    - 006-knowledge-graph.md#sec-1.5
    - 018-task-hierarchy.md
  "#sec-8":
    - 006-knowledge-graph.md#sec-1.1
    - 006-knowledge-graph.md#sec-1.2
    - 006-knowledge-graph.md#sec-1.3
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
transitional mirror only. A plan's acceptance mints its execution subtree directly, so the
container is the plan itself rather than a proxy borrowed from Jira. The remaining jobs the
epic might have done are either already owned (cross-plan "ships together" is `wl:Milestone`,
reserved since 006) or were never legitimate rows at all (§1).

Alongside the epic, this resolves three collisions found on the way:

- `wl:Project` (006: "bounded, goal-oriented") contradicts the backbone's `projects`, which are
  unbounded umbrellas. The backbone wins (§8).
- The `--kind spec` task was drifting toward "umbrella open while the spec is unimplemented",
  which stores a coverage query as a row. Spec-kind tasks are authoring work only (§6).
- Plan documents carry `- [ ]` checkboxes — execution state, ticked in git — while tasks also
  track execution state. Two owners of one fact; the plan subtree ends it (§5).

## 1. Principle: rows are things someone made; groupings are queries {#sec-1}

A row exists because an act created it: a task row because work was defined, a document row
because an artifact was authored, a `child_of` edge because one accept or decompose transaction
wired it. Everything cross-cutting is derived — a plan's task set, a spec's coverage, a
milestone's membership, "all plans derived from spec N with unfinished work". This is 014 §6's
"coverage is computed, never stored", applied to execution grouping.

The rule decides concrete cases:

| Candidate row | Verdict |
|---|---|
| Epic grouping one plan's tasks | Query (the subtree the plan's acceptance created) — row deleted |
| Spec-level container over its plans' roots | Query over `implements` + root states — never minted |
| Spec-umbrella task, open while unimplemented | Coverage query — never minted |
| Sprint / iteration container | Never minted; time-boxing is ranking and deadlines |
| Milestone | Row (declared intent: these deliverables ship together), membership derived |
| Plan root task | Row — created by the acceptance act, owns execution state |

## 2. Documents move into the backbone {#sec-2}

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
  where the ends are sections. Both directions stored, checked for agreement (014 §11).

014 §4's canonical + versioned IRIs, `dcat:hasVersion` shape and `wl:lastRevisedIn` survive as
the **projection** of this store; its named-graph publication transaction becomes an ordinary
backbone transaction followed by projector catch-up. The §7 constraints of 014 (append-only
anchors, no renumbering, superseded-with-explanation) are enforced at accept time by the
server instead of a SPARQL-side gate.

**Transitional:** `docs/specs/` and `docs/plans/` stay in git, checked by `secfmt.py`, until
this spec is implemented and the corpus is imported. From that point the files are deleted;
repositories reference spec sections only through `.worklode/implements.yaml` (014 §6, which
is unchanged by this spec — the claim "this commit satisfies §X" is a property of the commit
and stays in git).

The authority sentence in 000 §1 updates accordingly: the backbone owns execution facts **and
design-document artifacts**; the graph owns the derived, queryable view of both plus the
architecture model around them. Still no fact with two owners — the graph's copy is a
projection, never an authoring surface.

## 3. Editorial lifecycle {#sec-3}

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
        to a plan (014 §2's argument, preserved). Acceptance mints its execution subtree
        (025 §5)." .
```

Plan documents take no `doc_sections` rows and no anchors. Nothing addresses into a plan;
`implements` on a plan names spec sections, exactly as plan frontmatter does today.

## 5. Acceptance mints the execution subtree {#sec-5}

`lode doc accept` on a plan runs one transaction: mint a root task with `kind = 'plan'` bound
1:1 to the document, create the plan's tasks as its children (`draft`), and wire the
`child_of` edges. The root is born past its own draft phase — editorial drafting happened
doc-side, so `doc.status = accepted ⟺ root exists`, by construction, with nothing to keep in
sync by hand.

Three entities, each owning one fact, all created by acts:

| Entity | Owns | Created by |
|---|---|---|
| Authoring task (`kind = 'design'`) | The work of writing the plan | whoever picks up the planning |
| Plan document | Content + editorial status | the authoring work |
| Root task (`kind = 'plan'`) + children | Execution state + roll-up | the accept transaction |

Everything 018 built for the epic re-targets the plan root unchanged: never claimable and
excluded from the ready set, delivery states forbidden, closure by roll-up (018 §3.3, both
directions), progress derived on read, single parent, depth cap 2, cross-project children
rejected, brief showing one hop up. Where 018 says *epic*, read *plan root*.

`lode task decompose` (018 §8) survives as the second mint path: converting an oversized task
in place creates a plan root with a **null document reference** — an inline plan. It is
tracker-native, skips the review gate, and that is honest: no reviewable artifact was
authored. If a decomposition deserves review, it deserves a plan document.

The plan-doc `- [ ]` checkbox convention retires with the files: children's task state is the
only execution state.

## 6. Task kinds {#sec-6}

```sql
-- target CHECK; ships with the concept.ttl edit and regenerated code, atomically
CHECK (kind IN ('feature','bug','chore','design','review','spike','plan'))
```

- **`epic` is removed.** No jobs remain: one-plan grouping is the subtree (§5), cross-plan
  grouping is `wl:Milestone` (§8), everything else is a query (§1).
- **`spec` is renamed `design`** and widened to every document kind: *author or revise a
  Worklode document (spec, ADR, or plan); the document produced is reachable via
  `prov:wasGeneratedBy`* (014 §9's mechanism, unchanged). A design task is claimable, real
  work, and closes when its document is accepted — it is never an umbrella held open against
  coverage. "Is the spec implemented?" is `lode doc coverage`, not a task state.
- **`plan` is added**, the one structural kind: the container minted by accept or decompose.
  014 §8 ruled "no kind is added for plans" while plans were pure subtrees and the only
  candidate container was the epic; with the plan root as a real object carrying real guards,
  the kind is how the guards attach (declared, not inferred — 018 §1's argument stands).

Migration: `UPDATE tasks SET kind = 'design' WHERE kind = 'spec'`; forbid `epic` (no rows
exist); swap the CHECK; regenerate `validKinds` and `wlc:TaskKind` from `ns/` (§9) in the same
commit so `TestTaskKindsAgreeAcrossSources` never sees the sources disagree.

## 7. Spec fan-out is a query {#sec-7}

A spec producing several plans gets no container above the plan roots. Each need is owned:

| Need | Owner |
|---|---|
| "How far along is spec N?" | `lode doc coverage` — accepted sections × plans × root states |
| "Which specs need planning?" | accepted sections not named by any accepted plan's `implements` |
| "Which plans need execution?" | accepted plan docs whose root is absent or unmerged |
| Plan B after plan A | `blocks` edge between the roots |
| "These land together" | Milestone over their deliverables (§8) |
| Task Y waits on part of spec N | Y blocks on the specific plan root(s) |

"All of spec N is done" is not a completion event — coverage grows under amendment — so no row
may claim to be it. This also keeps the depth cap at 2: plan root → task → decomposed subtask;
a spec-level parent would force 3.

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
short-horizon grouping needs no further concept: one intent's tasks are its plan subtree; a
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

014 §10's reserved surface, now backed by the backbone store, plus the verbs this spec adds:

```
lode doc new --kind spec|adr|plan     author a draft (skill-guided)
lode doc show <id> [--resolved]       --resolved inlines amendments and supersessions
lode doc accept <id>                  the manual commit; on a plan, mints the subtree (§5)
lode doc list --needs-planning        accepted specs with unplanned accepted sections
lode doc list --needs-execution      accepted plans with no live root
lode doc coverage <id>                per-section implemented / unimplemented / stale
lode doc revise | anchors             as 014 §10
```

The lode plugin ships skills for the guided flows — authoring a spec, running its review,
accepting, offering (never assuming) decomposition into plans, and plan review — so the
ceremony lives in skills while every state change is one of the deterministic verbs above.

## 11. Amendments to existing specs {#sec-11}

| Spec | Change |
|---|---|
| 000 | §1 authority split: backbone owns doc artifacts; graph is projection (§2) |
| 006 | §1.1/§1.2/§1.3: Workstream/OngoingMaintenance/inWorkstream out, Project redefined, inProject in (§8); §1.5 TaskKind: `epic` out, `spec`→`design`, `plan` in (§6) |
| 014 | §0/§4/§10 store moves to backbone, graph becomes projection (§2); §5 `proposed` dropped (§3); §2 plans return as non-DesignDoc documents (§4); §8 superseded (§6) |
| 018 | Doc-wide: `epic` reads `plan root`; container minted by accept/decompose, never `task add --kind epic`; machinery otherwise intact (§5, §6) |
| Migration | Kind swap (§6); `docs`/`doc_sections`/`doc_edges` (§2); root→doc reference on tasks |
| CLAUDE.md / authoring docs | Ownership sentence, spec→plan→task model, `task:` key retirement |

## 12. Out of scope {#sec-12}

- **Corpus import** — moving the existing 25 specs and 40 plans into the store, anchor
  assignment for legacy prose, and deleting the files. Follows 014's "adoption" boundary; it
  is the implementation plan's final phase, not design.
- **Milestone implementation** — v2, as reserved; only its shape is pinned here (§8).
- **Graph projection of docs** — the projector work belongs to 006 §6's existing contract.
- **Review tooling internals** — crit integration details; this spec fixes only that review
  targets documents.

## 13. Acceptance criteria {#sec-13}

1. `kind = 'epic'` is absent from the CHECK, `validKinds`, and `wlc:TaskKind`; `design` and
   `plan` are present in all three; the sources are generated from `ns/` and CI fails on
   drift.
2. Accepting a plan document mints root + children + edges in one transaction; the root is
   never claimable, rolls up per 018 §3.3, and `doc accept` is the only path that creates a
   doc-bound root.
3. `lode task decompose` mints a docless plan root; `lode task add --kind plan` is rejected.
4. A spec with two accepted plans has no row above the two roots; `lode doc coverage`,
   `--needs-planning` and `--needs-execution` answer from queries alone.
5. A document under review is `draft` with an open review task; `proposed` appears nowhere;
   `lode doc accept` is manual and assignee-gated.
6. Plan documents carry no section anchors and accept mid-execution edits; spec documents
   enforce 014 §7's anchor constraints at accept time.
7. `wl:Workstream` and `wl:OngoingMaintenance` are absent from `ns/`; every projected Task
   carries exactly one `wl:inProject`; no `wl:` term for sprints exists.
8. No stored task→milestone edge exists when Milestone ships; its task set derives via
   Deliverables.
