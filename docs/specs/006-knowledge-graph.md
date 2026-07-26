# Spec 006 — Knowledge graph: the `ls:` vocabulary, entity model & projection

**Status:** spec · **Umbrella:** `000-umbrella-architecture.md` · **Source decisions:**
`003-platform-graph-design.md` (D4, D6, D7, D11) · **Depends on:** 004
(backbone) · **Consumed by:** 007 (drift/query), 009 (data-platform hosts the IRIs defined here).
**Amended by:** 014 (design docs as graph objects), 015 (runtime layer)

## Purpose & scope

Defines the *knowledge* half of Worklode: the `ls:` RDF vocabulary, the entity model across
the three layers (Intent / Execution·VCS / Runtime·Deploy), the canonical IRI scheme, and the
backbone→graph projection. This is the model that spec 007 queries for drift and overview.

The vocabulary ships as a **PR to `rdf-registry`** as three files (per ADR-0007 file naming):
`rdf/ls/ontology.ttl` (RDF 1.1 — classes + plain properties), `rdf/ls/ontology.1-2.ttl` (RDF 1.2 —
the triple-term annotations, e.g. section-level supersession), and `rdf/ls/concept.ttl` (SKOS
scheme). rdf-registry now **publishes RDF 1.2 alongside 1.1**, with 1.2 files suffixed `.1-2.ttl`,
so triple-term annotations ship natively. Must conform to ADR-0006 (IRI scheme), ADR-0001 (RDF-1.2
edge annotation), ADR-0007 (filenames = purpose).

**Hosting (decided).** The `ls:` ontology **stays in rdf-registry** (reusing its validated pipeline:
SHACL gate + the RDF-1.2 round-trip), but the published IRI base is **`https://worklode.io/ns/`**,
not `sunstone.institute/rdf/`. rdf-registry's pipeline emits the `worklode.io/ns/` base for the
`rdf/ls/` sources. This breaks ADR-0006's implicit "repo path = host path" mapping (`rdf/ls/` ↔
`sunstone.institute/rdf/ls/`); rdf-registry owns closing that wrinkle (a base-URL override for the
`ls` ontology) — tracked in spec 009.

**In scope:** the `ls:` terms (reuse vs mint), entity model with v1/v2 marks, Deliverable as
declared definition-of-done, the IRI grammar, backbone→graph projection, partial supersession.
**Out of scope (referenced):** backbone tables/lease (004), ranking (005), observed-layer derivers
& query implementation (007 — this spec defines the *model* it reads), data-platform ops (009 —
this spec defines the IRI scheme, they host it), plugin (008).

**Binding conventions (umbrella):** standards-first; **mint `ls:` sparingly**; **no gtio
ontologies at all** (research-scoped/experimental). The physical `gtio-sc:Component` is a
supply-chain term — a TRAP; software `ls:Component` is minted fresh.

---

## The `ls:` vocabulary

> **Superseded by 014 §1.** The prefixes are `wl:` / `wlc:` / `wlid:` and the namespaces `https://worklode.io/ns/wl/{ontology#,concept/,id/}`; the rename precedes shipping this spec.

`ls` is a **shared, cross-cutting** ontology (platform infrastructure, not a research domain),
so per ADR-0006 §1 it sits directly under `rdf/`, not under `rdf/domain/`.

```turtle
@prefix ls:      <https://worklode.io/ns/ontology#> .
@prefix lsc:     <https://worklode.io/ns/concept/> .
@prefix lsid:    <https://worklode.io/ns/id/> .
@prefix dct: <http://purl.org/dc/terms/> .
@prefix foaf:    <http://xmlns.com/foaf/0.1/> .
@prefix prov:    <http://www.w3.org/ns/prov#> .
@prefix doap:    <http://usefulinc.com/ns/doap#> .
@prefix skos:    <http://www.w3.org/2004/02/skos/core#> .
@prefix fabio:   <http://purl.org/spar/fabio/> .
@prefix rdf:     <http://www.w3.org/1999/02/22-rdf-syntax-ns#> .
@prefix rdfs:    <http://www.w3.org/2000/01/rdf-schema#> .
@prefix owl:     <http://www.w3.org/2002/07/owl#> .
@prefix xsd:     <http://www.w3.org/2001/XMLSchema#> .
```

### Reuse vs mint

> **Amended by 014.** `wl:Plan` and `wl:supersededSection` leave the mint set; `wl:Section` and `wl:lastRevisedIn` join it, and `wl:status` widens to Sections.

> **Amended by 015.** Six runtime classes (`wl:Artifact`, `wl:Build`, `wl:Deployment`, `wl:Environment`, `wl:Commit`, `wl:RuntimeEvent`), four SKOS schemes and seven properties join the mint set.

Standards-first: reuse a community term wherever one carries the intended meaning; mint only
where nothing does.

| Concept | Term | Source |
|---|---|---|
| Dependency (needs) | `dct:requires` | **reuse** |
| Decomposition (part-of) | `dct:hasPart` / `dct:isPartOf` | **reuse** |
| Supersession (replaces) | `dct:replaces` / `dct:isReplacedBy` | **reuse** |
| Owner / author | `foaf:Agent`, `prov:Agent`, `prov:wasAttributedTo` | **reuse** |
| Provenance | `prov:wasGeneratedBy`, `prov:wasDerivedFrom`, `prov:Activity` | **reuse** |
| Repo / project grouping | `doap:Project` (`doap:repository`, `doap:Version`) | **reuse** |
| Status scheme & enums | `skos:Concept`, `skos:ConceptScheme`, `skos:inScheme` | **reuse** |
| Titles / descriptions / dates | `dct:title` / `description` / `created` / `modified` | **reuse** |
| Design docs as documents | `foaf:Document` (opt. `fabio:` alignment) | **reuse** |
| **Software component** | `ls:Component` | **MINT** — `gtio-sc:Component` is supply-chain (TRAP); no gtio |
| **Design document** | `ls:DesignDoc` + `ls:ADR` / `ls:Spec` / `ls:Plan` | **MINT** — real subclasses |
| **Task** | `ls:Task` | **MINT** — projected from the backbone (D11) |
| **Deliverable** | `ls:Deliverable` | **MINT** — no standard for "declared definition-of-done" (D7); see Open Q1 |
| Design→component governance | `ls:governs` | **MINT** |
| Execution→intent realisation | `ls:implements` | **MINT** |
| Execution→component impact | `ls:affects` | **MINT** |
| DesignDoc lifecycle status | `ls:status` (+ `lsc:` SKOS scheme) | **MINT** (D4) |
| Section-level supersession | `ls:supersededSection` (annotation) | **MINT** — partial supersession; see Open Q2 |
| **Accepted deviation** (drift suppression) | `ls:AcceptedDeviation` | **MINT** — sanctioned observed-but-unasserted edge; see §Accepted deviations |
| Deviation → sanctioning decision | `ls:sanctionedBy` | **MINT** — deviation → authorising ADR (alt: reuse `dct:source`; see Open Q6) |
| Edge a deviation names, un-asserted | `rdf:subject` / `rdf:predicate` / `rdf:object` | **reuse** — RDF reification names a triple without asserting it |
| **Workstream** grouping (named-graph anchor) | `ls:Workstream` → `ls:Project` / `ls:OngoingMaintenance` | **MINT** — work-grouping a Task belongs to (≥1); anchors projection named graphs |
| Task kind | `ls:taskKind` (+ `lsc:TaskKind` SKOS) | **MINT** — feature/bug/chore/review/spike |
| Component reviewer (notify on PRs) | `ls:reviewer` | **MINT** — Component → `foaf:Agent` (GitHub user/team IRI) |
| Model-layer tag on vocabulary terms | `ls:layer` (+ `lsc:ModelLayer` SKOS) | **MINT** — intent/execution/runtime; lets you list all intent classes |
| Task ↔ GitHub issue mirror | `ls:mirrors` | **MINT** — symmetric Task↔Issue; PR→Task join piggybacks GitHub `Closes #N` |
| Task→Task dependency (transitive) | `ls:dependsOn` / `ls:blocks` | **MINT** — type-homogeneous `owl:TransitiveProperty` (ADR-0004); runtime reachability via property paths |
| Task→Workstream membership | `ls:inWorkstream` | **MINT** — split from `dct:isPartOf` to keep the Task→Task closure type-homogeneous |

Nothing else is minted in v1. Milestone (v2) will mint `ls:Milestone` then, not now.

### Classes & subclassing

```turtle
ls:Component  a owl:Class ;
> **Amended by 014 §2.** `wl:Plan` is removed and `wl:Section` added; both disjointness axioms change accordingly, and 015 §2 adds a third for the runtime classes.

    rdfs:comment "A software component — the atomic unit of the platform graph. "
                 "Repo/project (doap:Project) is a coarser grouping via dct:hasPart." .

ls:DesignDoc  a owl:Class ; rdfs:subClassOf foaf:Document .
ls:ADR   rdfs:subClassOf ls:DesignDoc .
ls:Spec  rdfs:subClassOf ls:DesignDoc .
ls:Plan  rdfs:subClassOf ls:DesignDoc .

ls:Task        a owl:Class .   # execution-owned, projected (D11)
ls:Deliverable a owl:Class .   # declared definition-of-done (D7)

ls:Workstream a owl:Class ;    # named-graph anchor; a Task ls:inWorkstream ≥1 Workstream
    rdfs:comment "A grouping of work a Task belongs to. Projection named graphs are anchored "
                 "per Workstream; a Task in several Workstreams appears in several graphs." .
ls:Project            a owl:Class ; rdfs:subClassOf ls:Workstream .   # bounded, goal-oriented — NB distinct from doap:Project (= repo)
ls:OngoingMaintenance a owl:Class ; rdfs:subClassOf ls:Workstream .   # unbounded, continuous

ls:AcceptedDeviation a owl:Class ;   # sanctioned observed-but-unasserted edge (§Accepted deviations)
    rdfs:comment "A tolerated architectural deviation that drift queries suppress. Names the "
                 "accepted edge via RDF reification (rdf:subject/predicate/object) WITHOUT "
                 "asserting it — the edge stays out of the intent layer." .

# Disjointness — a node can't be two of these at once (consistency reasoning, CI/owlrl):
[] a owl:AllDisjointClasses ;
   owl:members ( ls:Component ls:DesignDoc ls:Task ls:Deliverable ls:Workstream ) .
[] a owl:AllDisjointClasses ; owl:members ( ls:ADR ls:Spec ls:Plan ) .
[] a owl:AllDisjointClasses ; owl:members ( ls:Project ls:OngoingMaintenance ) .

# Repo grouping reuses doap; a repo holds many components:
# <repo> a doap:Project ; dct:hasPart lsid:component/... .
```

`foaf:Document` gives design docs a standard super-type; a later SPAR/`fabio:` alignment is
optional and additive (Open Q).

### Properties

```turtle
ls:governs a owl:ObjectProperty ;                 # intent → component (asserted)
    rdfs:domain ls:DesignDoc ; rdfs:range ls:Component ;
    rdfs:comment "This design doc governs the architecture of that component." .

ls:implements a owl:ObjectProperty ;              # execution → intent
    rdfs:range [ a owl:Class ; owl:unionOf ( ls:DesignDoc ls:Deliverable ls:Component ) ] ;
    rdfs:comment "A Task/PullRequest/Issue realises a DesignDoc, Deliverable, or Component. "
                 "Asserted when authored; observed when derived (spec 007). Union range is "
                 "machine-readable; SHACL (ls-shapes.ttl) enforces presence." .

ls:affects a owl:ObjectProperty ;                 # execution → component (observed)
    rdfs:range ls:Component ;
    rdfs:comment "A Task/Issue/PullRequest touches/changes that component." .

ls:status a owl:ObjectProperty, owl:FunctionalProperty ;   # D4 — exactly one status
    rdfs:domain ls:DesignDoc ; rdfs:range skos:Concept ;
    rdfs:comment "Lifecycle status in lsc:DesignDocStatus; inherited by ADR/Spec/Plan. "
                 "Functional catches >1; SHACL enforces ≥1." .

ls:sanctionedBy a owl:ObjectProperty ;            # deviation → authorising decision record
    rdfs:domain ls:AcceptedDeviation ; rdfs:range ls:DesignDoc ;
    rdfs:comment "The DesignDoc/ADR that authorises this accepted deviation." .

ls:taskKind a owl:ObjectProperty, owl:FunctionalProperty ;  # Task → lsc:TaskKind (exactly one)
    rdfs:domain ls:Task ; rdfs:range skos:Concept ;
    rdfs:comment "Kind of task (feature/bug/chore/review/spike); see lsc:TaskKind." .

# Type-homogeneous Task→Task closure (ADR-0004 rule): transitive, so `?t ls:dependsOn+ ?x`
# gives reachability at query time in Oxigraph (no reasoner) and CI/owlrl proves the closure.
ls:dependsOn a owl:ObjectProperty, owl:TransitiveProperty ;   # Task → Task
    rdfs:subPropertyOf dct:requires ;
    rdfs:domain ls:Task ; rdfs:range ls:Task ;
    rdfs:comment "This task needs that task done first (backbone `blocks`/dependency mirror). "
                 "Transitive: A→B→C ⊧ A→C." .
ls:blocks a owl:ObjectProperty, owl:TransitiveProperty ;      # Task → Task (inverse sense of dependsOn)
    owl:inverseOf ls:dependsOn ;
    rdfs:domain ls:Task ; rdfs:range ls:Task ;
    rdfs:comment "This task blocks that task (backbone-authoritative, spec 004). Transitive." .
ls:inWorkstream a owl:ObjectProperty ;            # Task → Workstream (named-graph anchor membership)
    rdfs:domain ls:Task ; rdfs:range ls:Workstream ;
    rdfs:comment "Membership of a Task in a Workstream. Split from dct:isPartOf so the "
                 "Task→Task decomposition stays type-homogeneous and cleanly transitive." .

ls:reviewer a owl:ObjectProperty ;                # Component → Agent (notify on PRs)
    rdfs:domain ls:Component ; rdfs:range foaf:Agent ;
    rdfs:comment "Agent (GitHub user/team IRI) to notify about PRs affecting this component." .

ls:mirrors a owl:ObjectProperty, owl:SymmetricProperty ;   # Task ↔ Issue
    rdfs:comment "Bidirectional mirror between a backbone Task and a GitHub Issue. The PR→Task "
                 "join piggybacks GitHub's native Closes #N (the PR closes the mirrored Issue)." .

ls:layer a owl:AnnotationProperty ;               # tags a class/property with its model layer
    rdfs:range skos:Concept ;
    rdfs:comment "Model-layer membership (lsc:ModelLayer: intent/execution/runtime) of a "
                 "vocabulary term, so all intent classes etc. can be listed. Annotation only." .
```

Note `ls:status` domain is `ls:DesignDoc`, inherited by all three subclasses. **Task
execution-state is NOT `ls:status`** — the task state machine is backbone-owned (spec 004); the
graph mirrors it as a projected literal, it does not fork the enum (Open Q3).

### Status SKOS scheme (D4)

Ordered lifecycle `draft → proposed → accepted → superseded → implemented`:

```turtle
lsc:DesignDocStatus a skos:ConceptScheme ; skos:prefLabel "DesignDoc lifecycle" .
lsc:draft       a skos:Concept ; skos:inScheme lsc:DesignDocStatus ; skos:prefLabel "draft" ;
    skos:definition "Being written; not yet proposed for review." .
lsc:proposed    a skos:Concept ; skos:inScheme lsc:DesignDocStatus ; skos:prefLabel "proposed" ;
    skos:definition "Submitted for crit review; awaiting resolution." .
lsc:accepted    a skos:Concept ; skos:inScheme lsc:DesignDocStatus ; skos:prefLabel "accepted" ;
    skos:definition "Crit-resolved and approved as intent." .
lsc:superseded  a skos:Concept ; skos:inScheme lsc:DesignDocStatus ; skos:prefLabel "superseded" ;
    skos:definition "Replaced (whole or in part) by a later doc via dct:replaces." .
lsc:implemented a skos:Concept ; skos:inScheme lsc:DesignDocStatus ; skos:prefLabel "implemented" ;
    skos:definition "Realised in code/prod by an implementing Task." .

# Lifecycle ORDER as data, not prose (skos:OrderedCollection) — "is X before Y" becomes queryable:
lsc:DesignDocStatusOrder a skos:OrderedCollection ;
    skos:memberList ( lsc:draft lsc:proposed lsc:accepted lsc:superseded lsc:implemented ) .
```

`proposed → accepted` is gated on crit-review resolution (umbrella convention). The **order** is
now data (the `skos:memberList` above), but RDF still doesn't *enforce* legal transitions — the
transition rules (which move is allowed from where) live with the authoring skill (spec 008).

### Task-kind & model-layer SKOS schemes

```turtle
lsc:TaskKind a skos:ConceptScheme ; skos:prefLabel "Task kind" .
lsc:feature a skos:Concept ; skos:inScheme lsc:TaskKind ; skos:prefLabel "feature" ;
    skos:definition "New capability or behaviour." .
lsc:bug     a skos:Concept ; skos:inScheme lsc:TaskKind ; skos:prefLabel "bug" ;
    skos:definition "Fix incorrect existing behaviour." .
lsc:chore   a skos:Concept ; skos:inScheme lsc:TaskKind ; skos:prefLabel "chore" ;
    skos:definition "Maintenance with no behaviour change (deps, tooling, cleanup)." .
lsc:review  a skos:Concept ; skos:inScheme lsc:TaskKind ; skos:prefLabel "review" ;
    skos:definition "Review/evaluate someone else's work." .
lsc:spike   a skos:Concept ; skos:inScheme lsc:TaskKind ; skos:prefLabel "spike" ;
    skos:definition "Time-boxed experiment to validate an approach; throwaway output." .

lsc:ModelLayer a skos:ConceptScheme ; skos:prefLabel "Model layer" .
lsc:intent    a skos:Concept ; skos:inScheme lsc:ModelLayer ; skos:prefLabel "intent" ;
    skos:definition "Asserted design layer — what should be true." .
lsc:execution a skos:Concept ; skos:inScheme lsc:ModelLayer ; skos:prefLabel "execution" ;
    skos:definition "Observed execution/VCS layer — tasks, issues, PRs." .
lsc:runtime   a skos:Concept ; skos:inScheme lsc:ModelLayer ; skos:prefLabel "runtime" ;
    skos:definition "Observed runtime/deploy layer — artifacts, deployments, environments." .
```

Every minted class/property carries an `ls:layer` tag (e.g. `ls:Component ls:layer lsc:intent`,
`ls:Task ls:layer lsc:execution`, `ls:Deliverable ls:layer lsc:intent`) so the model is queryable
by layer. `ls:taskKind` is backbone-projected like the rest of the Task node; `lsc:spike` is the
time-boxed validation experiment. Kind is a **fixed enum** (like `concern`, spec 005), not free text.

### Decomposition & dependency

```turtle
# Decomposition (D4): Spec ⊃ Plan ⊃ Task
> **Amended by 014 §5.** `implemented` leaves the enum — the order is `draft → proposed → accepted → superseded` — and implementation becomes a derived coverage query.

lsid:doc/spec-worklode-006 dct:hasPart lsid:doc/plan-006-projection .
lsid:doc/plan-006-projection dct:hasPart lsid:task/01H8XZ... .

# Dependency
lsid:doc/spec-worklode-007 dct:requires lsid:doc/spec-worklode-006 .
```

Task-level `child_of` / `blocks` edges are **backbone-authoritative** (spec 004); they surface in
the graph as projected `dct:isPartOf` (child_of) and **`ls:blocks` / `ls:dependsOn`** (dependency)
mirrors keyed by IRI. `ls:dependsOn`/`ls:blocks` are Task→Task and **transitive**, so overview
reachability (`?t ls:dependsOn+ ?dep`) runs in SPARQL without a reasoner and the critical-path
closure (spec 007) is expressible as property paths. Task→Workstream membership is `ls:inWorkstream`
(kept separate so the Task→Task closure stays type-homogeneous).

---

## Reasoning architecture (OWL / SHACL / SPARQL)

Reasoning runs in **three tiers**; each idiom pays off in exactly one, so the vocabulary is built
to the tier that can use it.

| Tier | Where | Does | Idioms used here |
|---|---|---|---|
| **Runtime** | `graph-server` / Oxigraph (ADR-0005) | SPARQL 1.1 — **no OWL/RDFS reasoner** | **property paths** (`?t ls:dependsOn+ ?x` reachability), `?c ls:layer lsc:intent` classification |
| **CI · OWL 2 RL** | `owlrl` in tests (ADR-0004 pattern; Jena `infer` is RDFS-only) | classification, disjointness & transitive closure — proof, not publish | `owl:AllDisjointClasses`, `owl:TransitiveProperty`, `owl:FunctionalProperty`, `owl:unionOf`, `owl:inverseOf` |
| **CI · SHACL gate** | Jena `shacl` over `rdf/shapes/ls-shapes.ttl` (ADR-0003) | closed-world **constraints** (required, cardinality) | node shapes below |
> **Amended by 014 §8.** `wlc:TaskKind` becomes exactly `feature, bug, chore, spec, review, spike`, matching the widened `tasks.kind` constraint.


**The load-bearing split.** OWL is open-world with no unique-names → it **never** flags a missing
required field or a duplicate. So **OWL classifies and checks consistency; SHACL enforces
presence/cardinality.** `owl:FunctionalProperty` on `ls:status`/`ls:taskKind` catches *>1*; the
SHACL shapes catch *0*.

**`ls-shapes.ttl` — v1 node shapes (sketch):**
- **Task** — exactly one `ls:taskKind`; exactly one projected state literal; ≥1 `ls:inWorkstream`.
- **Component** — ≥1 `ls:reviewer`.
- **Deliverable** — `dct:description` + ≥1 `dct:relation` target.
- **AcceptedDeviation** — `rdf:subject`/`predicate`/`object` + `ls:sanctionedBy` (optional `dct:valid`).
- **DesignDoc** — exactly one `ls:status` drawn from `lsc:DesignDocStatus`.

**Closure is not published** (ADR-0004): the pipeline ships asserted edges + TBox only. The
transitive/disjointness entailments are materialized by `owlrl` in CI to *prove* the axioms, and
re-derived live via SPARQL property paths — never baked into `dist/`.

---

## Entity model by layer (D6)

Three layers, joined vertically at **Deliverable**. `[v2]` = deferred. "Projected" = the node
already exists relationally in the Worklode backbone / ingest and is mirrored into the graph, not
authored there (see Projection).

### Layer 1 — Intent (asserted; authored graph-side, crit-reviewed)

| Node | Class | v1/v2 | Origin |
|---|---|---|---|
> **Obsolete (014 §2).** There is no Plan node: a Spec decomposes straight into an ordered task subtree in the backbone.

| Component | `ls:Component` | v1 | authored (per-repo manifest declares boundaries, D5) |
| DesignDoc | `ls:ADR`/`ls:Spec`/`ls:Plan` | v1 | authored |
| Deliverable | `ls:Deliverable` | v1 | authored (declared definition-of-done) |
| Milestone | `ls:Milestone` | **v2** | grouping of Deliverables |

Intent edges: `ls:governs` (DesignDoc→Component), `ls:reviewer` (Component→Agent, notify on PRs),
`dct:hasPart`, `dct:requires`, `dct:replaces`.

### Layer 2 — Execution · VCS (observed; mostly projected)

| Node | Class / term | v1/v2 | Notes |
|---|---|---|---|
| Task | `ls:Task` | v1 | **projected from backbone** (D11); carries `ls:taskKind` |
| Workstream | `ls:Project` / `ls:OngoingMaintenance` | v1 | projected from backbone; **named-graph anchor**; a Task `ls:inWorkstream` ≥1 |
| Issue | reuse `doap:` / projected node | v1 | projected from VCS ingest; `ls:mirrors` its Task |
| PullRequest | projected node | v1 | projected from VCS ingest |
| Branch, Commit, WorkflowRun, Event | — | **v2** | finer VCS granularity |

Execution edges: `ls:implements` (→ DesignDoc/Deliverable/Component), `ls:affects`
(→ Component), `ls:taskKind` (→ kind), `ls:mirrors` (Task↔Issue), `ls:inWorkstream`
(Task→Workstream), `ls:dependsOn`/`ls:blocks` (Task↔Task, transitive), `dct:isPartOf`
(child_of), `prov:wasAttributedTo` (→ author `foaf:Agent`).

### Layer 3 — Runtime · Deploy (observed; projected)

| Node | v1/v2 | Notes |
|---|---|---|
| Artifact (image `repo:tag`@digest) | v1 | declared/observed target of a Deliverable |
| Deployment | v1 | |
| Environment (`dev`, `prod`) | v1 | |
| Cluster, Namespace, Flux* | **v2** | live deploy view via Flux notifications |

Most of layers 2–3 **already exist relationally** in Worklode's ingest → these are a
projection, not a new build. v1 keeps runtime nodes minimal (declared targets); observed
confirmation of Deliverables by probing artifacts/deployments is **v2** (D7).

---

## Deliverable — declared definition-of-done (D7)

A `ls:Deliverable` is the **asserted target** that reconciles intent with prod reality: the
vertical join point where a Spec's ambition meets a running system. In v1 it is **declared
only** — its acceptance criteria are asserted, not auto-confirmed.

```turtle
lsid:deliverable/worklode-graph-live
    a ls:Deliverable ;
    dct:title "Worklode KG live in prod" ;
    dct:description "graph-server image pushed as ghcr.io/…:vX and service live in prod" ;
    # declared targets — plain references to the runtime IRIs (may not yet exist):
    dct:relation lsid:artifact/ghcr.io/sunstoneinstitute/graph-server/v1 ,
                     lsid:environment/prod .

# A Spec scopes the deliverable; a Task realises it:
lsid:doc/spec-worklode-009 dct:hasPart lsid:deliverable/worklode-graph-live .
lsid:task/01H8XZ...       ls:implements    lsid:deliverable/worklode-graph-live .
```

Acceptance criteria are `dct:description` (human-readable) plus declared `dct:relation`
links to the target Artifact/Environment IRIs. **Auto-confirmation** — probing whether the
artifact was actually pushed / the deployment is actually live, flipping the Deliverable to
satisfied — is **v2** and belongs to the observed-layer derivers (spec 007).

---

## Canonical IRI scheme (rdf-registry ADR-0006)

Branch-free, version-free term & instance IRIs (ADR-0006 §3). This is the **host/namespace
commitment** that spec 009 references (item 3) and that the data-platform must host.

> **Amended by 015 §7.** WorkflowRun is dropped (subsumed by `wl:Build`) and Commit is promoted to v1 as `wl:Commit`.

**Base:** `https://worklode.io/ns/`

| Purpose | Namespace | Prefix |
|---|---|---|
| Schema (classes/properties) | `…/ls/ontology#<Term>` (hash) | `ls:` |
| Status concepts (SKOS) | `…/ls/concept/<id>` (slash) | `lsc:` |
| Instances / KG nodes | `…/ls/id/<type>/<localid>` (slash) | `lsid:` |

**Instance grammar** — `ls/id/<type>/<localid>`, `<localid>` opaque & stable, **never**
carrying a git branch or version:

| Type | Pattern | Example |
|---|---|---|
| Component | `id/component/<slug>` (manifest slug; default = repo coords) | `…/id/component/github.com/sunstoneinstitute/worklode` |
| — multi-component repo | `id/component/<repo-coords>/<sub>` | `…/id/component/github.com/sunstoneinstitute/research-stack/pfas` |
> **Superseded by 015 §2 and §6.** Artifact/Deployment/Environment get real PROV-anchored classes there, `wl:Commit` joins v1, and §6 states which nodes actually have a v1 projection source.

| DesignDoc | `id/doc/<slug>` (design-file identity) | `…/id/doc/adr-0007-file-naming` , `…/id/doc/spec-worklode-006` |
| Task | `id/task/<taskid>` (backbone id, ULID/opaque) | `…/id/task/01H8XZ7K…` |
| Deliverable | `id/deliverable/<slug>` | `…/id/deliverable/worklode-graph-live` |
| Issue / PR | `id/{issue,pr}/<host>/<org>/<repo>/<number>` | `…/id/pr/github.com/sunstoneinstitute/worklode/42` |
| Artifact | `id/artifact/<registry>/<repo>/<tag-or-digest>` | `…/id/artifact/ghcr.io/sunstoneinstitute/graph-server/v1` |
| Deployment / Environment | `id/deployment/<…>` , `id/environment/<name>` | `…/id/environment/prod` |

The per-repo **component manifest** (D5) fixes each component's slug so the IRI is stable even
when directory layout shifts. Component IRIs are **branch-free**: the work graph lives on one
fixed graph-server branch (project is a *property*, not a branch — D1/spec 009 item 5).

Slashes inside `<localid>` are permissible (slash namespace, opaque path) and match the
rdf-registry `id/` convention.

---

## Projection: backbone → graph

**Authority stays split** (D2/D3): the **backbone owns execution facts** (task state, leases,
`blocks`/`child_of`); the **graph owns design facts** (Component, DesignDoc, `governs`,
`requires`, `replaces`, Deliverable). Task is the **bridge** (D11): backbone-authoritative, mirrored
read-only into the graph. Design nodes are authored graph-side and are **never** projected from
the backbone.

**Mechanism.** A single Go **projector** service consumes the backbone's provenance/outbox event
stream and writes projected quads to `graph-server` over GSP (spec 009 items 2, 4, 5), on the
fixed work-graph branch, authenticating via Keycloak client-credentials (`dataplatform-svc`).
Projection named graphs are **anchored per Workstream** (Open Q4): a Task's quads live in the
named graph of **each** Workstream it `ls:inWorkstream`, so a Task in several Workstreams appears
in several graphs (a triple in N graphs = N distinct quads; no conflict). Because a graph holds
many tasks, the projector maintains a task by a **per-subject replace** — `DELETE` the task IRI's
existing quads then `INSERT` the new ones, scoped to each of its Workstream graphs — rather than a
whole-graph `PUT`. **Keyed by subject IRI** → idempotent per (task, graph). A single projector +
per-branch write lock makes If-Match CAS unnecessary for v1 (spec 009 item 6). (The asserted/&lt;doc&gt;
and observed/&lt;source&gt; graph families of spec 007 are orthogonal to these Workstream graphs.)

| Entity / edge | Layer | Authority | v1? | Projected? | Trigger |
|---|---|---|---|---|---|
| Task node + `concern`/`priority`/state/`ls:taskKind` | 2 | backbone | v1 | **yes** | task lifecycle event (create/claim/transition/done/block) |
| `ls:affects` (Task→Component) | 2 | backbone | v1 | yes | task edit / ingest |
| `ls:implements` (Task→DesignDoc/Deliverable) | 2 | backbone | v1 | yes | task edit |
| `dct:isPartOf` (child_of mirror) | 2 | backbone | v1 | yes | task lifecycle |
| `ls:dependsOn` / `ls:blocks` (Task↔Task, transitive) | 2 | backbone | v1 | yes | task edit / block |
| `ls:inWorkstream` (Task→Workstream) + Workstream node | 2 | backbone | v1 | yes | project edit |
| `ls:mirrors` (Task↔Issue) | 2 | backbone+ingest | v1 | yes | task create / issue ingest |
| Issue / PullRequest + `implements`/`affects` | 2 | ingest | v1 | yes | VCS ingest (PR/issue open/merge) |
| Artifact / Deployment / Environment | 3 | ingest | v1 | yes (minimal) | deploy hook (declared target) |
| Component, DesignDoc, `governs`, `reviewer`, `requires`, `replaces` | 1 | **graph** | v1 | **no** (authored) | crit-approved design authoring |
| Deliverable + acceptance criteria | 1 | **graph** | v1 | no (authored) | design authoring |
| observed confirmation of Deliverable | 3 | derivers | **v2** | — | spec 04 |

Task execution-state is projected as a **literal** (e.g. `ls:taskState "in_progress"`) mirroring
the backbone enum; it is not modelled as `ls:status` and does not fork the backbone state machine
(Open Q3). Reads (drift, overview) run against the graph via the SPARQL path (spec 009 item 2 /
spec 007).

---

## Partial supersession (review add-on)

An ADR can be superseded **only in specific subsections**, not wholesale. Modelled as a
**scoped `dct:replaces` edge annotated with the affected section** (RDF-1.2 triple term,
ADR-0001) — **recommended over minting section sub-IRIs** (lighter; no addressable-section
namespace to maintain).

```turtle
# Full supersession — plain edge, no annotation:
lsid:doc/adr-0009 dct:replaces lsid:doc/adr-0003 .

# Partial supersession — same edge, annotated with the affected section:
lsid:doc/adr-0012 dct:replaces lsid:doc/adr-0004 .
<< lsid:doc/adr-0012 dct:replaces lsid:doc/adr-0004 >>
    ls:supersededSection "§4 Deployment" .
```

**Semantics (ADR-0001 asserted-occurrence).** The inner triple `adr-0012 replaces adr-0004`
*is* asserted as a plain triple; the annotation **scopes** it. Convention:
- `dct:replaces` **with no** `ls:supersededSection` → full supersession (the superseded doc
  moves to `lsc:superseded`).
- `dct:replaces` **with** one or more `ls:supersededSection` → partial; only those sections
  are stale. The superseded doc stays `lsc:accepted`; drift queries (spec 007) read the
  annotation to report which sections are stale.

`ls:supersededSection` is a **domain-free annotation predicate** (ADR-0001 caveat: an annotation
> **Artifact IRI superseded by 015 §5.** The pattern is kind-first — `id/artifact/<kind>/<name>/<version>` — and 015 adds Deployment, Environment, Commit and Build patterns.

> **Base amended by 014 §1 and §4.** The base gains a `wl/` segment, and design documents additionally carry immutable versioned sibling IRIs (`…/doc/<slug>/v3`) used only in pinned claims.

predicate must not declare an `rdfs:domain` of an endpoint class, or a reasoner mis-types the
triple term). Range is a literal section reference (`"§4.2"`, a heading string) — deliberately
**not** a section IRI.

> **Publishing (resolved).** rdf-registry now publishes **RDF 1.2 alongside 1.1**: 1.1 files as
> `/rdf/ls/ontology.ttl`, 1.2 files as `/rdf/ls/ontology.1-2.ttl`. The triple-term
> `ls:supersededSection` annotations live in the `.1-2.ttl` file and ship natively — no round-trip
> blocker. (`graph-server`, pyoxigraph-backed, also handles them for runtime authoring.)

---

## Accepted deviations — drift suppression (resolves spec 007 Open Q3)

Some observed-but-unasserted edges are **intentional** — a sanctioned coupling the architecture
tolerates but never elevated to intent. Without suppression, spec 007's violation query reports them
forever. An accepted deviation is modelled as an **asserted-layer fact** so it is crit-reviewed,
provenanced, and expirable like any other asserted edge — **not** a backbone allowlist.

**Why a separate node, not a triple-term annotation.** The tolerated edge must **not** be asserted:
asserting `A dct:requires B` would make it sanctioned intent, and it would then report as
*stale-intent* drift the moment code dropped it. ADR-0001's asserted-occurrence `<< A requires B >>`
*does* assert its inner triple, so it is the wrong tool here. ADR-0001's own decision rule routes a
multi-attribute claim (rationale + authorising ADR + expiry + who/when) to **reification into a
class** — the `sc:QuantifiedAssertion` precedent. So a deviation **names** its edge with the
standard RDF reification vocabulary (`rdf:subject`/`predicate`/`object`), which does not assert it,
and stays plain RDF 1.1 (no `.1-2.ttl`).

```turtle
lsid:deviation/pfas-reads-ingest-cache
    a ls:AcceptedDeviation ;
    rdf:subject   lsid:component/.../pfas ;       # the tolerated edge — UN-asserted
    rdf:predicate dct:requires ;
    rdf:object    lsid:component/.../ingest ;
    ls:sanctionedBy lsid:doc/adr-0014 ;           # the decision that authorises it
    dct:description "pfas reads ingest's on-disk cache directly; migration tracked in WT-812" ;
    prov:wasAttributedTo lsid:agent/stig ;
    dct:created "2026-07-21"^^xsd:date ;
    dct:valid   "2026-12-31"^^xsd:date .       # OPTIONAL expiry; absent = indefinite
```

- **Home graph.** Authored into the **asserted named graph of the sanctioning ADR**
  (`…/graph/asserted/<adr-id>`, spec 007), under the same crit gate (`proposed → accepted`) as any
  asserted edge. Superseding or removing that ADR removes the suppression. No new authorisation
  mechanism: whoever may author intent may author a deviation.
- **Expiry.** Optional `dct:valid` (an `xsd:date`). Past it, the deviation stops suppressing and
  the violation re-surfaces (spec 007) — a suppression cannot silently outlive its reason. No expiry
  = indefinite, but every deviation stays listable (`lode drift --acknowledged`), never invisible.
- **Scope.** Predicate-general (names any `s/p/o`); in v1 only spec 007's 4.1 `dct:requires`
  violation query consumes it.

## Dependencies

- **Spec 004 (backbone):** owns Task state, leases, `blocks`/`child_of`; emits the event/outbox
  stream the projector consumes.
- **Spec 007 (drift/query):** consumes this model — reads the two-layer diff; owns observed-layer
  derivers and Deliverable auto-confirmation (v2).
- **Spec 009 (data-platform):** hosts the IRI scheme defined here; must ship prod `graph-server`,
  a SPARQL read path (Oxigraph + outbox materializer), external write auth, and a fixed writable
  branch.
- **rdf-registry:** the `ls:` PR must satisfy ADR-0006 (IRI), ADR-0007 (filenames), ADR-0001
  (triple-term annotation), split the ontology into `ontology.ttl` (1.1) + `ontology.1-2.ttl`
  (1.2), add `rdf/shapes/ls-shapes.ttl` behind the **SHACL gate (ADR-0003)**, add an `owlrl`
  closure test in the **ADR-0004** style (Jena `infer` is RDFS-only, can't prove OWL closure), and
  add `ls` to the `/rdf/` DCAT/VoID index (ADR-0006 §5).

## Open questions

1. ~~Deliverable minting~~ — **CONFIRMED:** mint `ls:Deliverable` (no standard for "declared
   definition-of-done").
2. ~~`ls:supersededSection` vs. a standard~~ — **CONFIRMED:** mint the domain-free annotation
   predicate.
3. ~~Task-state representation~~ — **CONFIRMED:** projected as a literal mirror; does not fork the
   backbone-owned state machine (004).
4. ~~Named-graph granularity~~ — **RESOLVED.** Projection named graphs are anchored on
   **Workstreams** (`ls:Project`/`ls:OngoingMaintenance`, parent `ls:Workstream`). A Task
   `ls:inWorkstream` ≥1 Workstream and its quads live in each such graph (a triple in N graphs =
   N distinct quads; RDF has no exclusivity constraint). The projector maintains a task by a
   per-subject `DELETE`/`INSERT` within each of its Workstream graphs (see Projection). Separate
   from the asserted/&lt;doc&gt; and observed/&lt;source&gt; graph families (spec 007).
5. ~~RDF-1.2 publish blocker~~ — **RESOLVED:** rdf-registry publishes 1.2 alongside 1.1
   (`.1-2.ttl` files), so `ls:supersededSection` annotations ship natively; no interim workaround needed.
6. ~~`ls:sanctionedBy` — mint vs. reuse~~ — **CONFIRMED:** mint it.
> **Superseded by 014 §3.** Sections are addressable `wlid:section/<doc-slug>/<anchor>` nodes; partial supersession is `dct:isReplacedBy` between sections, and `wl:supersededSection` is retired.


## Acceptance criteria

1. `ls:` ontology authored as `rdf/ls/ontology.ttl` (1.1) + `rdf/ls/ontology.1-2.ttl` (1.2
   annotations) + `rdf/ls/concept.ttl` (SKOS) + `rdf/shapes/ls-shapes.ttl` (SHACL),
   ADR-0006/0007-conformant, opened as a PR to rdf-registry with a `/rdf/` index entry.
2. The mint set is exactly {`ls:Component`, `ls:DesignDoc`, `ls:ADR`, `ls:Spec`, `ls:Plan`,
   `ls:Task`, `ls:Deliverable`, `ls:Workstream`, `ls:Project`, `ls:OngoingMaintenance`,
   `ls:governs`, `ls:implements`, `ls:affects`, `ls:reviewer`, `ls:taskKind`, `ls:mirrors`,
   `ls:dependsOn`, `ls:blocks`, `ls:inWorkstream`, `ls:status`, `ls:layer`, `ls:supersededSection`,
   `ls:AcceptedDeviation`, `ls:sanctionedBy`} plus SKOS schemes `lsc:DesignDocStatus`,
   `lsc:TaskKind`, `lsc:ModelLayer`; everything else reuses
   `dcterms`/`foaf`/`prov`/`doap`/`skos`/`rdf`/`owl`. **No gtio term appears.**
3. The IRI grammar for Component / DesignDoc / Task / Deliverable / Issue / PR / Artifact /
   Deployment / Environment is documented and branch-free (ADR-0006 §3); spec 009 can host it.
4. The projector can `PUT` a Task's named graph (keyed by its `lsid:task/…` IRI) to the fixed
   work-graph branch on a backbone lifecycle event, idempotently, and a SPARQL read returns it.
5. Decomposition (`Spec ⊃ Plan ⊃ Task` via `dct:hasPart`), dependency (`dct:requires`),
   full supersession (`dct:replaces`), and **partial** supersession (annotated triple term
   with `ls:supersededSection`) each validate against the vocabulary and are distinguishable by a
   drift query (spec 007).

> **Superseded by 014 acceptance criteria 2 and 5.** Decomposition is Spec → task subtree, and partial supersession is tested at section-IRI granularity, not via a triple-term annotation.

6. A Deliverable declares its definition-of-done (`dct:description` + declared
   Artifact/Environment `dct:relation` targets) and links to the realising Task via
   `ls:implements`.
7. An `ls:AcceptedDeviation` names a `dct:requires` edge via `rdf:subject`/`predicate`/`object`
   **without** asserting it (the edge is absent from the asserted layer), carries `ls:sanctionedBy`
   → an ADR and an optional `dct:valid` expiry, and is distinguishable by a drift query
   (spec 007) as suppressing vs. expired.
8. **Workstream named graphs:** a Task belonging to two Workstreams (`ls:inWorkstream`) has its
   quads present in **both** Workstream named graphs; a per-subject re-projection updates the task
   in each without disturbing other tasks in those graphs.
9. **Task kind, reviewer, layer, mirror:** `ls:taskKind` resolves to a `lsc:TaskKind` concept
   (incl. `lsc:spike`); a Component's `ls:reviewer` resolves to a GitHub user/team Agent IRI;
   every minted class/property carries an `ls:layer` so `SELECT ?c WHERE { ?c ls:layer lsc:intent }`
   lists all intent terms; a Task and its GitHub Issue are joined by symmetric `ls:mirrors`, and a
   PR that `Closes #N` the mirrored Issue resolves to the Task.
10. **Reasoning architecture:** (a) an OWL 2 RL pass (`owlrl`) over the TBox + a seeded ABox proves
    the disjointness axioms (a node typed both `ls:Task` and `ls:Deliverable` is flagged
    inconsistent) and the `ls:dependsOn`/`ls:blocks` transitive closure (`A→B→C ⊧ A→C`); (b) the
    same reachability is returned live by a SPARQL property-path query (`?t ls:dependsOn+ ?x`)
    against Oxigraph with **no** reasoner; (c) `rdf/shapes/ls-shapes.ttl` (Jena `shacl`, ADR-0003)
    rejects a Task missing `ls:taskKind` or with two, and a Deliverable missing its `dct:relation`
    target; (d) closure is **not** present in published `dist/`.

> **Amended by 014 and 015.** The mint set loses `Plan` and `supersededSection`, gains `Section`/`lastRevisedIn`, and gains the six runtime classes plus their schemes and properties.

