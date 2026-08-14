---
status: draft
requires:
- docs/specs/004-execution-backbone.md
- docs/specs/007-drift-and-overview.md
- docs/specs/025-documents-in-the-backbone.md
---
# Spec 006 — Knowledge graph: vocabulary, entity model, runtime layer & projection

## 0. Purpose & scope {#sec-0}

Defines the *knowledge* half of Worklode: the `wl:` RDF vocabulary, the entity model across the
three layers (Intent / Execution·VCS / Runtime·Deploy), the canonical IRI scheme, the
backbone→graph projection, and what the data-platform must host for any of it to run. This is the
model that spec 007 queries for drift and overview.

The vocabulary ships as a **PR to `rdf-registry`** as three files (per ADR-0007 file naming):
`rdf/wl/ontology.ttl` (RDF 1.1 — classes + plain properties), `rdf/wl/ontology.1-2.ttl` (RDF 1.2 —
the triple-term annotations), and `rdf/wl/concept.ttl` (SKOS scheme). rdf-registry **publishes RDF
1.2 alongside 1.1**, with 1.2 files suffixed `.1-2.ttl`, so triple-term annotations ship natively.
Must conform to ADR-0006 (IRI scheme), ADR-0001 (RDF-1.2 edge annotation), ADR-0007 (filenames =
purpose).

**Hosting (decided).** The `wl:` ontology **stays in rdf-registry** (reusing its validated pipeline:
SHACL gate + the RDF-1.2 round-trip), but the published IRI base is **`https://worklode.io/ns/`**,
not `sunstone.institute/rdf/`. rdf-registry's pipeline emits the `worklode.io/ns/` base for the
`rdf/wl/` sources. This breaks ADR-0006's implicit "repo path = host path" mapping (`rdf/wl/` ↔
`sunstone.institute/rdf/wl/`); rdf-registry owns closing that wrinkle (a base-URL override for the
`wl` ontology) — tracked in §13.2 item 3.

**In scope:** the `wl:` terms (reuse vs mint), the entity model with v1/v2 marks, the runtime
classes and their SKOS schemes, Deliverable as declared definition-of-done, the natural-key IRI
grammar, the backbone→graph projection — which relational table feeds each node and which nodes
have no source yet — and the data-platform requirements the whole thing rests on.
**Out of scope (referenced):** backbone tables/lease (004), ranking (005), observed-layer derivers
& query implementation (007 — this spec defines the *model* it reads), the delivery resolver and
its frontier arithmetic (004 §5 — this spec models the *facts* it reads, not
the state machine), and the plugin (008).

**Why the runtime layer is modelled here.** The Layer 3 nodes — Artifact, Deployment,
Environment, Commit — are the observed half a Deliverable's declared `dct:relation` targets point
at, so they need real classes: untyped instance IRIs leave a `wl:Deliverable` declaring its
definition-of-done against nothing, and give 007's `observed/deploy` deriver no vocabulary to emit.
The delivery lifecycle (004 §5) also makes the layer load-bearing for task
state — `deployed_dev`, `deployed_prod`, `released`, over `main_commits`, `env_deploys` and
`release_frontiers` — which puts Commit on the critical path of the state machine. §1.1, §2.1,
§3.1, §6, §10.1 and §11.1 supply it: the reuse survey behind each runtime mint, the six
classes anchored on PROV-O, the four runtime SKOS schemes, the natural-key IRI grammar, and the
projection table.

**Binding conventions:** standards-first; **mint `wl:` sparingly**; **no gtio ontologies at all**
(research-scoped/experimental). The physical `gtio-sc:Component` is a supply-chain term — a TRAP;
software `wl:Component` is minted fresh.

---

## 1. Reuse vs mint {#sec-1}

Standards-first: reuse a community term wherever one carries the intended meaning; mint only
where nothing does.

| Concept | Term | Source |
|---|---|---|
| Dependency (needs) | `dct:requires` | **reuse** |
| Decomposition (part-of) | `dct:hasPart` / `dct:isPartOf` | **reuse** |
| Supersession (replaces), whole-document and section-level | `dct:replaces` / `dct:isReplacedBy` | **reuse** — between addressable Section nodes for the partial case; no `wl:supersededSection` annotation predicate is minted (025 §3) |
| Owner / author | `foaf:Agent`, `prov:Agent`, `prov:wasAttributedTo` | **reuse** |
| Provenance | `prov:wasGeneratedBy`, `prov:wasDerivedFrom`, `prov:Activity` | **reuse** |
| Repo / project grouping | `doap:Project` (`doap:repository`, `doap:Version`) | **reuse** |
| Status scheme & enums | `skos:Concept`, `skos:ConceptScheme`, `skos:inScheme` | **reuse** |
| Titles / descriptions / dates | `dct:title` / `description` / `created` / `modified` | **reuse** |
| Design docs as documents | `foaf:Document` (opt. `fabio:` alignment) | **reuse** |
| **Software component** | `wl:Component` | **MINT** — `gtio-sc:Component` is supply-chain (TRAP); no gtio |
| **Design document** | `wl:DesignDoc` + `wl:ADR` / `wl:Spec` | **MINT** — real subclasses |
| **Document section** | `wl:Section` (+ `wl:lastRevisedIn`) | **MINT** — the addressable, individually linkable part of a DesignDoc that durable links and partial implementation both need; `wl:status` widens to it (025 §2, §3) |
| **Plan** | `wl:Plan` | **MINT** — an executable document, sibling of `wl:DesignDoc` rather than a subclass: reviewable and accept-gated, but mutable and anchor-free (025 §9) |
| **Task** | `wl:Task` | **MINT** — projected from the backbone |
| **Deliverable** | `wl:Deliverable` | **MINT** — no standard for "declared definition-of-done"; see open question 1 |
| **Effect** (artifact-free deliverable) | `wl:Effect` ⊂ `wl:Deliverable` | **MINT** — IaC/GitOps work alters system state and ships nothing; edges differ from Deliverable, so a subclass not a kind attribute |
| Design→component governance | `wl:governs` | **MINT** |
| Deliverable→component delivery | `wl:deliveredBy` | **MINT** — no standard term; closes the implementation statement (§3) |
| Component→section evidence | `wl:implements` | **MINT** — the manifest claim "that code meets this section" (025 §11); the remaining work edge is `wl:produces`, Task→Deliverable (026 §6.2) |
| Execution→component impact | `wl:affects` | **MINT** |
| DesignDoc lifecycle status | `wl:status` (+ `wlc:` SKOS scheme) | **MINT** |
| **Accepted deviation** (drift suppression) | `wl:AcceptedDeviation` | **MINT** — sanctioned observed-but-unasserted edge; see §12 |
| Deviation → sanctioning decision | `wl:sanctionedBy` | **MINT** — deviation → authorising ADR (alt: reuse `dct:source`; see open question 6) |
| Edge a deviation names, un-asserted | `rdf:subject` / `rdf:predicate` / `rdf:object` | **reuse** — RDF reification names a triple without asserting it |
| **Project** umbrella (named-graph anchor) | `wl:Project` | **MINT** — the unbounded umbrella over a set of repos (the backbone's `projects` table), not a bounded, goal-oriented grouping; anchors projection named graphs (025 §13) |
| Task kind | `wl:taskKind` (+ `wlc:TaskKind` SKOS) | **MINT** — feature/bug/chore/design/review/spike |
| Task execution-state mirror | `wl:taskState` (literal, no SKOS scheme) | **MINT** — projected literal mirroring the backbone enum, so the graph does not fork the state machine (open question 3, §11). Legal values are `tasks.state`'s `CHECK`; transitions stay in `internal/store/tasks.go` and are not modelled |
| Component reviewer (notify on PRs) | `wl:reviewer` | **MINT** — Component → `foaf:Agent` (GitHub user/team IRI) |
| Model-layer tag on vocabulary terms | `wl:layer` (+ `wlc:ModelLayer` SKOS) | **MINT** — intent/execution/runtime; lets you list all intent classes |
| Task ↔ GitHub issue mirror | `wl:mirrors` | **MINT** — symmetric; domain and range are the same `Task ∪ Issue` union, because a symmetric property with differing domain and range entails every Issue is a Task. SHACL pins the intended pairing. PR→Task join piggybacks GitHub `Closes #N` |
| **Issue** / **PullRequest** | `wl:Issue`, `wl:PullRequest` | **MINT** — §8.2 pencilled in "reuse `doap:`", but DOAP offers only `doap:bug-database` (a URL of a tracker, not a class for one issue) and OSLC CM is rejected on the grounds §1.1 gives. Both `prov:Entity`, so `prov:wasAttributedTo` is domain-correct |
| Task→Task dependency (transitive) | `wl:dependsOn` / `wl:blocks` | **MINT** — type-homogeneous `owl:TransitiveProperty` (ADR-0004); runtime reachability via property paths |
| Task→Project membership | `wl:inProject` | **MINT** — Task→Project, exactly one, derived from `tasks.project_id`; split from `dct:isPartOf` to keep the Task→Task closure type-homogeneous (025 §13) |
| **Skill** and its design-doc pin | `wl:Skill`, `wl:recommendsSkill` | **MINT** — 016 §1 declares both: the Skill node in the execution layer, the DesignDoc→Skill pin in the intent layer |
| **Runtime layer** | `wl:Artifact`, `wl:Build`, `wl:Deployment`, `wl:Environment`, `wl:Commit`, `wl:RuntimeEvent`, four SKOS schemes and seven properties | **MINT** — §1.1 surveys what was rejected; §2.1, §3.1 and §6 declare them |

Nothing else is minted in v1. Milestone (v2) will mint `wl:Milestone` then, not now.

### 1.1 Reuse survey: the runtime layer {#sec-1.1}

Standards-first, **mint sparingly**. Every candidate below was checked against its published
specification, not from memory. The result is unusual — the runtime layer is a genuine gap in the
standards landscape, and forcing a reuse here would be worse than a clean mint.

| Vocabulary | Offers | Verdict |
|---|---|---|
| **PROV-O** | `prov:Entity`, `prov:Activity`, `prov:used`, `prov:wasGeneratedBy`, `prov:wasDerivedFrom`, timing properties | **REUSE as the anchor.** Exactly the right shape for "an activity produced an artifact from a source". Carries no version, digest or registry-coordinate term — those are ours. |
| **SPDX 3.0.1** | A real, fetchable OWL model with `Build/Build` and `Software/Package` | **Reference only.** The Build profile is one class bolted onto Core's `Element`/`Relationship` scaffolding: adopting it imports ~46 classes to get one class of payload, with no supported way to cherry-pick a profile. The RDF is generated from a Markdown-authored spec, tooling consumes the JSON-LD, and there is **no PROV-O alignment** to inherit. Cite it with `rdfs:seeAlso`; do not import it. |
| **DOAP** | `doap:Version`, `doap:release`, `doap:revision` | **Rejected for artifacts.** `doap:Version` is "version information of a project release" — a changelog entry hanging off `doap:Project`, with no digest, no registry coordinate and no artifact-kind discrimination. Effectively dormant since 2022. The use of `doap:Project` for **repository** grouping (§1) is unaffected and stays. |
| **DCAT v3** | `dcat:Distribution`, `dcat:downloadURL`, `spdx:checksum` | **Rejected.** DCAT scopes `Distribution` to "an accessible form of *a dataset*" and explicitly directs implementers to subclass `dcat:Resource` for other artifacts, naming them out of scope. Reusing it would contradict the spec's own guidance. |
| **schema.org** | `SoftwareApplication`, `softwareVersion`, `downloadUrl` | **Rejected.** Describes a software *product*; `softwareVersion` is a bare string. No build, deployment or environment concept exists. |
| **OSLC Automation** | `AutomationPlan` / `Request` / `Result`, CI-shaped state enums | **Rejected.** Conceptually apt but frozen at Project Specification Draft 01 (2021) with adoption confined to Eclipse Lyo and IBM ELM. Not a standard mainstream CI recognises. |
| **`sd:` Software Description Ontology** | `sd:SoftwareImage` ("for example, a Docker container") | **Rejected as a dependency.** The closest-named term found anywhere, but last released 2021 and scoped to scientific-software cataloguing. Worth an `rdfs:seeAlso`, not a parent class. |
| **SLSA / in-toto, CycloneDX** | Build provenance and SBOM formats | **Not candidates.** JSON/protobuf only; neither publishes an RDF or OWL form. |
| **TOSCA** | Deployment topology | **Not candidates.** OASIS TOSCA has no RDF binding; the only RDF forms are single-paper academic artifacts with no resolvable namespace and no third-party use. |
| **Deployment environment (dev/prod)** | — | **Nothing exists.** No surveyed vocabulary has the concept; SPDX's `Build.environment` is a free-form dictionary, not a stage. `prov:Location` is spatial in intent and would mislead. Honest mint. |

**Conclusion.** Mint six runtime classes, every one subclassed from a PROV-O anchor where a truthful
anchor exists, so that PROV-aware tooling traverses the runtime layer without any Worklode-specific
knowledge. `wl:Environment` alone has no parent — a wrong parent is worse than none.

---

## 2. Classes & subclassing {#sec-2}

```turtle
wl:Component  a owl:Class ;
    rdfs:comment "A software component — the atomic unit of the platform graph. "
                 "Repo/project (doap:Project) is a coarser grouping via dct:hasPart." .

wl:DesignDoc  a owl:Class ; rdfs:subClassOf foaf:Document , prov:Entity .
wl:ADR   rdfs:subClassOf wl:DesignDoc .
wl:Spec  rdfs:subClassOf wl:DesignDoc .

wl:Section a owl:Class ; rdfs:subClassOf foaf:Document ;
    rdfs:comment "An addressable, individually linkable part of a DesignDoc. Stable for the "
                 "life of the document; never deleted once the document is accepted." .

# A Plan is a document but not a DesignDoc: reviewable and accept-gated, yet mutable and
# anchor-free, so the section lock never binds it. Its acceptance mints the execution subtree.
wl:Plan a owl:Class ; rdfs:subClassOf foaf:Document , prov:Entity .

wl:Task        a owl:Class .   # execution-owned, projected
wl:Deliverable a owl:Class .   # declared definition-of-done

# An Effect is a Deliverable that ships no artifact: IaC (provisioning) and GitOps
# (admin-cluster) work alters the state of an existing system instead. A subclass rather
# than a wl:deliverableKind attribute because the edges differ, not just the coordinates
# (§2.1's own test): an Effect names no Artifact, and its witness is a Commit, not an Artifact.
# Effect inherits Deliverable's disjointness — the AllDisjointClasses axiom below is unchanged.
wl:Effect a owl:Class ; rdfs:subClassOf wl:Deliverable ;
    rdfs:comment "A deliverable whose definition-of-done is a state of an existing system, not "
                 "the existence and placement of an artifact — 'admin-cluster has Keycloak SSO "
                 "configured'. Declares its target as a Deployment IRI (stable: environment + "
                 "target kind + target name) and MUST NOT declare an Artifact. Witnessed by that "
                 "Deployment reaching wlc:deployed over a Commit on the delivering component's "
                 "default branch — the commit is discovered, never declared: its IRI carries a "
                 "SHA (§10.1) and so cannot be named in advance." .

wl:Project a owl:Class ;       # named-graph anchor; every Task wl:inProject exactly one Project
    rdfs:comment "The umbrella for all work in a set of repositories — the backbone's projects "
                 "table, verbatim. Unbounded. NB: doap:Project is a single repository; a "
                 "wl:Project owns 1..n of them (project_repos). Projection named graphs are "
                 "anchored per Project." .

wl:AcceptedDeviation a owl:Class ;   # sanctioned observed-but-unasserted edge (§12)
    rdfs:comment "A tolerated architectural deviation that drift queries suppress. Names the "
                 "accepted edge via RDF reification (rdf:subject/predicate/object) WITHOUT "
                 "asserting it — the edge stays out of the intent layer." .

# Disjointness — a node can't be two of these at once (consistency reasoning, CI/owlrl):
[] a owl:AllDisjointClasses ;
   owl:members ( wl:Component wl:DesignDoc wl:Plan wl:Section wl:Task wl:Deliverable wl:Project
                 wl:Skill ) .
[] a owl:AllDisjointClasses ; owl:members ( wl:ADR wl:Spec ) .

# Deliberately absent, so nobody re-mints them: wl:Workstream and its wl:OngoingMaintenance
# subclass, the wl:inWorkstream membership edge, and the (Project OngoingMaintenance)
# disjointness axiom. One unbounded umbrella — wl:Project — covers what they modelled (025 §13).

# Repo grouping reuses doap; a repo holds many components:
# <repo> a doap:Project ; dct:hasPart wlid:component/... .
```

`foaf:Document` gives design docs a standard super-type; a later SPAR/`fabio:` alignment is
optional and additive (open question). `prov:Entity` on `wl:DesignDoc` is not decoration: documents
carry `prov:wasGeneratedBy`, `prov:wasRevisionOf` and `prov:wasAttributedTo`, each of which has
`prov:Entity` as its domain, so without the parent every provenanced document is an OWL violation
the moment the `owlrl` pass runs (025 §2).

### 2.1 Runtime classes {#sec-2.1}

```turtle
wl:Artifact a owl:Class ; rdfs:subClassOf prov:Entity ;
    rdfs:seeAlso <https://spdx.org/rdf/3.0.1/terms/Software/Package> ;
    rdfs:comment "One built, versioned unit deployed elsewhere: a container image, a PyPI "
                 "package, a git tag or a binary release. Kind is an attribute "
                 "(wl:artifactKind), not a subclass — the kinds differ in coordinates, not in "
                 "the edges they carry." .

wl:Build a owl:Class ; rdfs:subClassOf prov:Activity ;
    rdfs:seeAlso <https://spdx.org/rdf/3.0.1/terms/Build/Build> ;
    rdfs:comment "The activity that produced an Artifact — a CI workflow run. No v1 projection "
                 "source exists (see §11.1); declared now so wl:Artifact can carry "
                 "prov:wasGeneratedBy without a later breaking change." .

wl:Deployment a owl:Class ; rdfs:subClassOf prov:Activity ;
    rdfs:comment "The rollout of an Artifact OR a Commit to one target in one Environment — a Flux "
                 "Kustomization, a PyPI publish, or a manually tracked target. prov:used binds an "
                 "Artifact for built units and a Commit for IaC/GitOps repos, which ship nothing "
                 "and reconcile a git revision instead (the wl:Effect case, §9). "
                 "Mirrors current state, one node per (environment, target kind, target name); "
                 "rollout history is v2." .

wl:Environment a owl:Class ;
    rdfs:comment "A deployment stage. Deliberately parentless: no surveyed vocabulary models a "
                 "deployment stage, and prov:Location is spatial in intent. The instance set is "
                 "closed to dev and prod by SHACL, matching the normalisation the delivery "
                 "handlers already enforce." .

wl:Commit a owl:Class ; rdfs:subClassOf prov:Entity ;
    rdfs:comment "One commit on a repository's default branch. Promoted from v2 because "
                 "delivery resolution is defined in terms of commit coverage." .

wl:RuntimeEvent a owl:Class ; rdfs:subClassOf prov:Activity ;
    rdfs:comment "An observed runtime incident affecting a deployed Artifact — a crashloop, an "
                 "OOM kill, a Flux reconciliation failure or recovery." .

# Every runtime term is tagged, per the §5 convention:
wl:Artifact wl:layer wlc:runtime .    wl:Build        wl:layer wlc:runtime .
wl:Deployment wl:layer wlc:runtime .  wl:Environment  wl:layer wlc:runtime .
wl:Commit wl:layer wlc:runtime .      wl:RuntimeEvent wl:layer wlc:runtime .

[] a owl:AllDisjointClasses ;
   owl:members ( wl:Artifact wl:Build wl:Deployment wl:Environment wl:Commit wl:RuntimeEvent ) .
```

**Why `wl:Artifact` has no subclasses.** §2 mints real subclasses for `wl:DesignDoc` because
ADR and Spec play different structural roles, and §5 uses a SKOS scheme for `wl:taskKind` because
kind is a mere attribute there. Artifact kind is the latter case: a `docker_image` and a `pypi`
package carry identical edges and differ only in how their coordinates are spelled. One class plus
`wl:artifactKind` — consistent with `wl:taskKind`, and with the fixed-enum convention.

**Why a git tag is not a `wl:Release`.** The release handler already records a published release as
an `artifacts` row of kind `git_tag`; a separate class would duplicate it. A release is
`wl:Artifact` with `wl:artifactKind wlc:git_tag` plus a `wl:cutFrom` edge to the commit its frontier
reaches. `release_frontiers` projects as that edge, not as a node.

---

## 3. Properties {#sec-3}

```turtle
wl:governs a owl:ObjectProperty ;                 # intent → component (declared)
    rdfs:domain wl:DesignDoc ; rdfs:range wl:Component ;
    rdfs:comment "This design doc governs the architecture of that component." .

wl:deliveredBy a owl:ObjectProperty ;             # deliverable → component (declared)
    rdfs:domain wl:Deliverable ; rdfs:range wl:Component ;
    rdfs:comment "The component that delivers this deliverable. Deliberately NOT functional: a "
                 "deliverable may be delivered by several components, each advancing at its own "
                 "pace. Closes the Component→Section → Deliverable → Environment join that makes "
                 "'component A implemented section B by deploying deliverable C to environment D' "
                 "answerable per environment. Declared rather than derived because the derivable "
                 "route (Artifact→Build→Commit→paths→Component) needs wl:Build, which has no v1 "
                 "projection source (§11.1). SHACL enforces >=1." .

wl:implements a owl:ObjectProperty ;              # component → intent (observed)
    rdfs:domain wl:Component ; rdfs:range wl:Section ;
    rdfs:comment "That component's code meets that section: wl:implements is only "
                 "Component→Section. Derived by observed/repo-implements from "
                 ".worklode/implements.yaml (025 §11), never declared on a task." .

wl:produces a owl:ObjectProperty ;                # execution → intent
    rdfs:domain wl:Task ; rdfs:range wl:Deliverable ;
    rdfs:comment "This task is what makes that deliverable exist (026 §6.2). Component left that "
                 "range, and Issue and PullRequest left the domain. Each reaches a deliverable "
                 "through the task it is bound to; wl:affects carries work→Component." .

wl:affects a owl:ObjectProperty ;                 # execution → component (observed)
    rdfs:range wl:Component ;
    rdfs:comment "A Task/Issue/PullRequest touches/changes that component." .

wl:status a owl:ObjectProperty, owl:FunctionalProperty ;   # exactly one status
    rdfs:domain [ a owl:Class ; owl:unionOf ( wl:DesignDoc wl:Plan wl:Section ) ] ;
    rdfs:range skos:Concept ;
    rdfs:comment "Lifecycle status in wlc:DesignDocStatus; inherited by ADR and Spec. "
                 "Functional catches >1; SHACL enforces >=1, on a Plan as on a DesignDoc." .

wl:sanctionedBy a owl:ObjectProperty ;            # deviation → authorising decision record
    rdfs:domain wl:AcceptedDeviation ; rdfs:range wl:DesignDoc ;
    rdfs:comment "The DesignDoc/ADR that authorises this accepted deviation." .

wl:taskKind a owl:ObjectProperty, owl:FunctionalProperty ;  # Task → wlc:TaskKind (exactly one)
    rdfs:domain wl:Task ; rdfs:range skos:Concept ;
    rdfs:comment "Kind of task (feature/bug/chore/design/review/spike); see wlc:TaskKind." .

# Type-homogeneous Task→Task closure (ADR-0004 rule): transitive, so `?t wl:dependsOn+ ?x`
# gives reachability at query time in Oxigraph (no reasoner) and CI/owlrl proves the closure.
wl:dependsOn a owl:ObjectProperty, owl:TransitiveProperty ;   # Task → Task
    rdfs:subPropertyOf dct:requires ;
    rdfs:domain wl:Task ; rdfs:range wl:Task ;
    rdfs:comment "This task needs that task done first (backbone `blocks`/dependency mirror). "
                 "Transitive: A→B→C ⊧ A→C." .
wl:blocks a owl:ObjectProperty, owl:TransitiveProperty ;      # Task → Task (inverse sense of dependsOn)
    owl:inverseOf wl:dependsOn ;
    rdfs:domain wl:Task ; rdfs:range wl:Task ;
    rdfs:comment "This task blocks that task (backbone-authoritative, spec 004). Transitive." .
wl:inProject a owl:ObjectProperty, owl:FunctionalProperty ;   # Task → Project (named-graph anchor)
    rdfs:domain wl:Task ; rdfs:range wl:Project ;
    rdfs:comment "Derived from tasks.project_id: every Task is in exactly one Project. Split "
                 "from dct:isPartOf so the Task→Task decomposition stays type-homogeneous and "
                 "cleanly transitive." .

wl:reviewer a owl:ObjectProperty ;                # Component → Agent (notify on PRs)
    rdfs:domain wl:Component ; rdfs:range foaf:Agent ;
    rdfs:comment "Agent (GitHub user/team IRI) to notify about PRs affecting this component." .

wl:mirrors a owl:ObjectProperty, owl:SymmetricProperty ;   # Task ↔ Issue
    rdfs:comment "Bidirectional mirror between a backbone Task and a GitHub Issue. The PR→Task "
                 "join piggybacks GitHub's native Closes #N (the PR closes the mirrored Issue)." .

wl:layer a owl:AnnotationProperty ;               # tags a class/property with its model layer
    rdfs:range skos:Concept ;
    rdfs:comment "Model-layer membership (wlc:ModelLayer: intent/execution/runtime) of a "
                 "vocabulary term, so all intent classes etc. can be listed. Annotation only." .
```

`wl:status`'s domain is the union of `wl:DesignDoc`, `wl:Plan` and `wl:Section`, inherited by the
ADR and Spec subclasses (026 §6). **Task execution-state is NOT `wl:status`** — the task state
machine is backbone-owned (spec 004); the graph mirrors it as a projected literal, it does not fork
the enum (open question 3).

**Implementation is one statement.** Not one edge and not three ranges: the sentence the
vocabulary has to answer is **"Component A implemented Section B by deploying Deliverable C to
Environment D."** It is a **query, not a node** — each clause already exists above, and the
sentence is true exactly where the declared and observed halves join:

```sparql
?component   wl:implements  ?section .           # 025 §11 manifest — observed
?deliverable wl:deliveredBy ?component ;         # declared, above
             dct:relation ?artifact , ?env .     # declared, §9
?deployment  prov:used ?artifact ;               # observed, §11.1
             wl:toEnvironment ?env .
```

For a `wl:Effect` (artifact-free IaC/GitOps delivery, §9) the last two clauses bind a **Commit**
rather than an Artifact, and the deliverable declares the Deployment target directly. The statement
is unchanged; only the witness differs.

This is the ambition-reconciliation thesis at its narrowest useful grain, and it answers "is this
spec implemented?" **per environment** — satisfied in dev, not yet in prod — rather than as one
boolean. It also gives Deliverable auto-confirmation (v2, §9) its concrete shape.

**Component ↔ Deliverable is what closes it**, minted as `wl:deliveredBy` and declared above.
Without it the two meet only through the Spec that `dct:hasPart`s both, which is far too coarse —
one Spec scopes many deliverables and many components. It is *declared*, not derived, because the
derivable route (Artifact → Build → Commit → paths → Component) is closed in v1: `wl:Build` has
**no v1 projection source**. Declaring suits a node this spec already treats as declared-only.

### 3.1 Runtime properties {#sec-3.1}

Seven mints. Everything a standard covers truthfully is reused.

```turtle
wl:artifactKind a owl:ObjectProperty, owl:FunctionalProperty ;
    rdfs:domain wl:Artifact ; rdfs:range skos:Concept ;
    rdfs:comment "Kind of artifact; see wlc:ArtifactKind. Functional catches >1, SHACL catches 0." .

wl:digest a owl:DatatypeProperty ;
    rdfs:domain wl:Artifact ; rdfs:range xsd:string ;
    rdfs:comment "Content digest as an opaque prefixed string, e.g. \"sha256:abc…\". Minted "
                 "rather than reusing spdx:checksum: that is a class requiring an algorithm/value "
                 "node, and the ingested value is a single opaque string. A spdx:Checksum "
                 "alignment is additive if SBOM interop is ever wanted." .

wl:toEnvironment a owl:ObjectProperty, owl:FunctionalProperty ;
    rdfs:domain wl:Deployment ; rdfs:range wl:Environment ;
    rdfs:comment "The environment this deployment targets." .

wl:deploymentStatus a owl:ObjectProperty, owl:FunctionalProperty ;
    rdfs:domain wl:Deployment ; rdfs:range skos:Concept ;
    rdfs:comment "Current rollout status; see wlc:DeploymentStatus." .

wl:targetKind a owl:ObjectProperty, owl:FunctionalProperty ;
    rdfs:domain wl:Deployment ; rdfs:range skos:Concept ;
    rdfs:comment "Kind of deployment target; see wlc:DeployTargetKind." .

wl:runtimeEventKind a owl:ObjectProperty, owl:FunctionalProperty ;
    rdfs:domain wl:RuntimeEvent ; rdfs:range skos:Concept ;
    rdfs:comment "Kind of runtime incident; see wlc:RuntimeEventKind." .

wl:cutFrom a owl:ObjectProperty ;
    rdfs:domain wl:Artifact ; rdfs:range wl:Commit ;
    rdfs:comment "This artifact carries every commit up to and including that commit — the "
                 "delivery frontier of a published release. Domain is Artifact, not Deployment: "
                 "see the grain note in §11.1 for why the per-environment frontier has no node." .
```

**Reused, with the column each projects from:**

| Fact | Term | Notes |
|---|---|---|
| artifact built by | `prov:wasGeneratedBy` → `wl:Build` | no v1 source; see §11.1 |
| artifact built from source | `prov:wasDerivedFrom` → `wl:Commit` | `artifacts.source_sha` |
| build time | `prov:generatedAtTime` | `artifacts.built_at`; domain `prov:Entity`, so it hangs on the Artifact — correct, since v1 has no Build node |
| deployment uses artifact | `prov:used` | `deployments.artifact_id` |
| runtime event affects artifact | `prov:used` | `runtime_events.artifact_id` |
| deployment first seen | `prov:startedAtTime` | `deployments.first_seen` |
| deployment last updated | `dct:modified` | `deployments.last_update` |
| event / publication time | `dct:date` | `runtime_events.occurred_at`, `release_frontiers.published_at` |
| version string | `owl:versionInfo` | `artifacts.version` |
| name, target name, commit sha | `dct:identifier` | the coordinate, also encoded in the IRI |
| repository grouping | `doap:Project` + `dct:hasPart` | as §1 already establishes |
| human label | `dct:title` | |

**A modelling tension, stated plainly.** `deployments` is a mutable current-state row (unique per
environment/target), not an immutable event, yet `wl:Deployment` subclasses `prov:Activity`. This is
deliberate: a rollout *is* an activity, `prov:startedAtTime` genuinely means "first seen", and the
alternative — a state node with no PROV anchor — buys nothing. The consequence is that the graph
shows current state only; per-rollout history needs a second node type and is v2.

---

## 4. Status SKOS scheme {#sec-4}

Ordered, purely editorial lifecycle `draft → accepted → superseded`:

```turtle
wlc:DesignDocStatus a skos:ConceptScheme ; skos:prefLabel "DesignDoc lifecycle" .
wlc:draft       a skos:Concept ; skos:inScheme wlc:DesignDocStatus ; skos:prefLabel "draft" ;
    skos:definition "Being written, or open for crit review. Not yet accepted as intent." .
wlc:accepted    a skos:Concept ; skos:inScheme wlc:DesignDocStatus ; skos:prefLabel "accepted" ;
    skos:definition "Crit-resolved and approved as intent." .
wlc:superseded  a skos:Concept ; skos:inScheme wlc:DesignDocStatus ; skos:prefLabel "superseded" ;
    skos:definition "Replaced (whole or in part) by a later doc via dct:replaces." .

# Lifecycle ORDER as data, not prose (skos:OrderedCollection) — "is X before Y" becomes queryable:
wlc:DesignDocStatusOrder a skos:OrderedCollection ;
    skos:memberList ( wlc:draft wlc:accepted wlc:superseded ) .
```

There is no `implemented` status: it would assert that *superseded* precedes *implemented*, and
implementation is per-section and derived, so a document-level summary of it drifts. **"Is this
spec implemented?" is a coverage query (025 §7, §6), never a stored status.**

There is no "under review" status either: a document under review is a **draft with an open review
task**, so `draft → accepted` is what the crit gate governs and the open task, not the enum, is
what proves a review is in flight (025 §7). The **order** is data (the `skos:memberList` above),
but RDF still doesn't *enforce* legal transitions — the transition rules (which move is allowed
from where) live with the authoring skill (spec 008).

## 5. Task-kind & model-layer SKOS schemes {#sec-5}

```turtle
wlc:TaskKind a skos:ConceptScheme ; skos:prefLabel "Task kind" .
wlc:feature a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "feature" ;
    skos:definition "New capability or behaviour." .
wlc:bug     a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "bug" ;
    skos:definition "Fix incorrect existing behaviour." .
wlc:chore   a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "chore" ;
    skos:definition "Maintenance with no behaviour change (deps, tooling, cleanup)." .
wlc:design  a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "design" ;
    skos:definition "Author or revise a Worklode document — spec, ADR or plan; the document "
                    "produced is reachable via prov:wasGeneratedBy." .
wlc:review  a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "review" ;
    skos:definition "Review/evaluate someone else's work." .
wlc:spike   a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "spike" ;
    skos:definition "Time-boxed experiment to validate an approach; throwaway output." .

wlc:ModelLayer a skos:ConceptScheme ; skos:prefLabel "Model layer" .
wlc:intent    a skos:Concept ; skos:inScheme wlc:ModelLayer ; skos:prefLabel "intent" ;
    skos:definition "Declared design layer — what should be true." .
wlc:execution a skos:Concept ; skos:inScheme wlc:ModelLayer ; skos:prefLabel "execution" ;
    skos:definition "Observed execution/VCS layer — tasks, issues, PRs." .
wlc:runtime   a skos:Concept ; skos:inScheme wlc:ModelLayer ; skos:prefLabel "runtime" ;
    skos:definition "Observed runtime/deploy layer — artifacts, deployments, environments." .
```

`wlc:TaskKind` is exactly `feature, bug, chore, design, review, spike`, matching the `tasks.kind`
constraint. Every kind names a nature of work; no structural member replaces the `epic` container,
because a plan is a document whose tasks are grouped by their reference to it rather than under a
container row (025 §9.2, §6).

Every minted class/property carries a `wl:layer` tag (e.g. `wl:Component wl:layer wlc:intent`,
`wl:Task wl:layer wlc:execution`, `wl:Deliverable wl:layer wlc:intent`) so the model is queryable
by layer. `wl:taskKind` is backbone-projected like the rest of the Task node; `wlc:spike` is the
time-boxed validation experiment. Kind is a **fixed enum** (like `concern`, spec 005), not free text.

## 6. Runtime SKOS schemes {#sec-6}

Four fixed enums, each mirroring a `CHECK` constraint that already exists in the schema. These are
controlled vocabularies, never free text.

```turtle
wlc:ArtifactKind a skos:ConceptScheme ; skos:prefLabel "Artifact kind" .
wlc:docker_image a skos:Concept ; skos:inScheme wlc:ArtifactKind ; skos:prefLabel "docker image" ;
    skos:definition "An OCI container image in a registry, identified by name, tag and digest." .
wlc:pypi     a skos:Concept ; skos:inScheme wlc:ArtifactKind ; skos:prefLabel "PyPI package" ;
    skos:definition "A Python distribution published to a package index." .
wlc:git_tag  a skos:Concept ; skos:inScheme wlc:ArtifactKind ; skos:prefLabel "git tag" ;
    skos:definition "A published release tag on a repository." .
wlc:binary   a skos:Concept ; skos:inScheme wlc:ArtifactKind ; skos:prefLabel "binary release" ;
    skos:definition "A compiled executable published as a release asset." .

wlc:DeploymentStatus a skos:ConceptScheme ; skos:prefLabel "Deployment status" .
wlc:pending      a skos:Concept ; skos:inScheme wlc:DeploymentStatus ; skos:prefLabel "pending" ;
    skos:definition "Target known; no reconciliation observed yet." .
wlc:reconciling  a skos:Concept ; skos:inScheme wlc:DeploymentStatus ; skos:prefLabel "reconciling" ;
    skos:definition "Rollout in progress." .
wlc:deployed     a skos:Concept ; skos:inScheme wlc:DeploymentStatus ; skos:prefLabel "deployed" ;
    skos:definition "Reconciliation succeeded; what the deployment used — an artifact, or a "
                    "commit for IaC/GitOps targets — is live on this target." .
wlc:failed       a skos:Concept ; skos:inScheme wlc:DeploymentStatus ; skos:prefLabel "failed" ;
    skos:definition "Reconciliation failed; the target is not serving the intended artifact." .

wlc:DeployTargetKind a skos:ConceptScheme ; skos:prefLabel "Deployment target kind" .
wlc:flux_kustomization a skos:Concept ; skos:inScheme wlc:DeployTargetKind ;
    skos:prefLabel "Flux Kustomization" ;
    skos:definition "A Flux-reconciled target. v1 files HelmRelease events here too; the "
                    "distinction survives in the event type, not the target kind." .
wlc:pypi_target a skos:Concept ; skos:inScheme wlc:DeployTargetKind ; skos:prefLabel "PyPI publish" ;
    skos:definition "Publication to a package index as the delivery target." .
wlc:manual      a skos:Concept ; skos:inScheme wlc:DeployTargetKind ; skos:prefLabel "manual" ;
    skos:definition "A target tracked by hand, with no automated reconciliation signal." .

wlc:RuntimeEventKind a skos:ConceptScheme ; skos:prefLabel "Runtime event kind" .
wlc:crashloop     a skos:Concept ; skos:inScheme wlc:RuntimeEventKind ; skos:prefLabel "crashloop" .
wlc:oom           a skos:Concept ; skos:inScheme wlc:RuntimeEventKind ; skos:prefLabel "OOM kill" .
wlc:flux_failure  a skos:Concept ; skos:inScheme wlc:RuntimeEventKind ; skos:prefLabel "Flux failure" .
wlc:flux_recovery a skos:Concept ; skos:inScheme wlc:RuntimeEventKind ; skos:prefLabel "Flux recovery" .
```

`wlc:pypi_target` is spelled apart from `wlc:pypi` deliberately: the artifact kind and the target
kind are different concepts that happen to share a name in the relational schema.

---

## 7. Reasoning architecture (OWL / SHACL / SPARQL) {#sec-7}

Reasoning runs in **three tiers**; each idiom pays off in exactly one, so the vocabulary is built
to the tier that can use it.

| Tier | Where | Does | Idioms used here |
|---|---|---|---|
| **Runtime** | `graph-server` / Oxigraph (ADR-0005) | SPARQL 1.1 — **no OWL/RDFS reasoner** | **property paths** (`?t wl:dependsOn+ ?x` reachability), `?c wl:layer wlc:intent` classification |
| **CI · OWL 2 RL** | `owlrl` in tests (ADR-0004 pattern; Jena `infer` is RDFS-only) | classification, disjointness & transitive closure — proof, not publish | `owl:AllDisjointClasses`, `owl:TransitiveProperty`, `owl:FunctionalProperty`, `owl:unionOf`, `owl:inverseOf` |
| **CI · SHACL gate** | Jena `shacl` over `rdf/shapes/wl-shapes.ttl` (ADR-0003) | closed-world **constraints** (required, cardinality) | node shapes below |

**The load-bearing split.** OWL is open-world with no unique-names → it **never** flags a missing
required field or a duplicate. So **OWL classifies and checks consistency; SHACL enforces
presence/cardinality.** `owl:FunctionalProperty` on `wl:status`/`wl:taskKind` catches *>1*; the
SHACL shapes catch *0*.

**`wl-shapes.ttl` — v1 node shapes (sketch):**
- **Task** — exactly one `wl:taskKind`; exactly one projected state literal; exactly one
  `wl:inProject`.
- **Component** — ≥1 `wl:reviewer`.
- **Deliverable** — `dct:description` + ≥1 `dct:relation` target + ≥1 `wl:deliveredBy`.
- **Effect** — inherits the Deliverable shape, plus: ≥1 `dct:relation` naming a `wl:Deployment`,
  and **zero** naming a `wl:Artifact` (an Effect that ships something is a Deliverable).
- **AcceptedDeviation** — `rdf:subject`/`predicate`/`object` + `wl:sanctionedBy` (optional `dct:valid`).
- **DesignDoc** — exactly one `wl:status` drawn from `wlc:DesignDocStatus`; `wl:PlanShape` requires
  the same of every Plan (026 §6).
- **Artifact** — exactly one `wl:artifactKind` from `wlc:ArtifactKind`; exactly one
  `owl:versionInfo`; exactly one `dct:identifier`.
- **Deployment** — exactly one each of `wl:toEnvironment`, `wl:targetKind`, `wl:deploymentStatus`;
  at most one `prov:used`.
- **Environment** — `sh:in (wlid:environment/dev wlid:environment/prod)`, closing the set to what
  the delivery handlers normalise to.
- **Commit** — exactly one `dct:identifier` (the sha).

The runtime half of this vocabulary needs **no migration**: it adds no column and changes no
constraint, and every runtime enum mirrors a `CHECK` the schema already carries.

**Closure is not published** (ADR-0004): the pipeline ships declared edges + TBox only. The
transitive/disjointness entailments are materialized by `owlrl` in CI to *prove* the axioms, and
re-derived live via SPARQL property paths — never baked into `dist/`.

---

## 8. Entity model by layer {#sec-8}

Three layers, joined vertically at **Deliverable**. `[v2]` = deferred. "Projected" = the node
already exists relationally in the Worklode backbone / ingest and is mirrored into the graph, not
authored there (see §11). Layer 3 — Runtime · Deploy — is the six runtime classes of §2.1, and
§11.1 states which of them have a v1 projection source.

### 8.1 Layer 1 — Intent (declared) {#sec-8.1}

| Node | Class | v1/v2 | Origin |
|---|---|---|---|
| Component | `wl:Component` | v1 | authored (per-repo manifest declares boundaries) |
| DesignDoc | `wl:ADR` / `wl:Spec`, addressable by `wl:Section` | v1 | authored |
| Deliverable | `wl:Deliverable` | v1 | authored (declared definition-of-done) |
| Effect | `wl:Effect` ⊂ `wl:Deliverable` | v1 | authored; artifact-free definition-of-done for IaC/GitOps state change |
| Milestone | `wl:Milestone` | **v2** | grouping of Deliverables |

Intent edges: `wl:governs` (DesignDoc→Component), `wl:reviewer` (Component→Agent, notify on PRs),
`wl:deliveredBy` (Deliverable→Component), `dct:hasPart`, `dct:requires`, `dct:replaces`.

### 8.2 Layer 2 — Execution · VCS (observed) {#sec-8.2}

| Node | Class / term | v1/v2 | Notes |
|---|---|---|---|
| Task | `wl:Task` | v1 | **projected from backbone**; carries `wl:taskKind` |
| Project | `wl:Project` | v1 | projected from backbone; **named-graph anchor**; every Task `wl:inProject` exactly one |
| Plan | `wl:Plan` | v1 | **authored, not projected** — an executable document, sibling of `wl:DesignDoc` (§2) |
| Issue | `wl:Issue` ⊂ `prov:Entity` | v1 | projected from VCS ingest; `wl:mirrors` its Task |
| PullRequest | `wl:PullRequest` ⊂ `prov:Entity` | v1 | projected from VCS ingest |
| Branch, Event | — | **v2** | finer VCS granularity |

Commit is not deferred: it is a v1 runtime-layer class (`wl:Commit`, §2.1), because delivery
resolution is defined over commit coverage. `WorkflowRun` is not a node of its own either —
`wl:Build` subsumes it.

Execution edges: `wl:produces` (Task→Deliverable), `wl:affects` (→ Component), `wl:taskKind`
(→ kind), `wl:mirrors` (Task↔Issue), `wl:inProject` (Task→Project),
`wl:dependsOn`/`wl:blocks` (Task↔Task, transitive), `dct:isPartOf` (child_of), and authorship:
`prov:wasAttributedTo` from an Issue or PullRequest, but **`prov:wasAssociatedWith` from a Task** —
025 §12 makes Task a `prov:Activity`, PROV declares Activity and Entity disjoint, and
`prov:wasAttributedTo` has an Entity domain, so using it on a Task makes every authored Task
inconsistent.

## 9. Deliverable — declared definition-of-done {#sec-9}

A `wl:Deliverable` is the **declared target** that reconciles intent with prod reality: the
vertical join point where a Spec's ambition meets a running system. In v1 it is **declared
only** — its acceptance criteria are declared, not auto-confirmed. It declares the **Component**
that delivers it (`wl:deliveredBy`, ≥1, SHACL-enforced), closing the Component→Section →
Deliverable → Environment join of §3, and its `dct:relation` targets are typed runtime nodes
(`wl:Artifact`, `wl:Environment`), not bare IRIs.

```turtle
wlid:deliverable/worklode-graph-live
    a wl:Deliverable ;
    dct:title "Worklode KG live in prod" ;
    dct:description "graph-server image pushed as ghcr.io/…:vX and service live in prod" ;
    # who delivers it — closes the join to the Component→Section claim (§3):
    wl:deliveredBy wlid:component/graph-server ;
    # declared targets — typed references to the runtime nodes (may not yet exist):
    dct:relation wlid:artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1 ,
                     wlid:environment/prod .

# A Spec scopes the deliverable; a Task realises it:
wlid:doc/spec-worklode-006 dct:hasPart wlid:deliverable/worklode-graph-live .
wlid:task/01H8XZ...       wl:produces      wlid:deliverable/worklode-graph-live .

# --- Effect: an artifact-free deliverable (IaC / GitOps) ---
# "admin-cluster has Keycloak SSO configured" ships nothing; it changes the state of a
# running system. The declared target is the DEPLOYMENT, whose IRI is stable and
# predictable (environment + target kind + target name, §10.1) — unlike a Commit IRI,
# which carries a SHA and so cannot be named before the work exists.
wlid:effect/admin-cluster-keycloak-sso
    a wl:Effect ;
    dct:title "Keycloak SSO live on admin-cluster" ;
    dct:description "oauth2-proxy fronting the admin-cluster dashboards, OIDC client in the sunstone realm" ;
    wl:deliveredBy wlid:component/admin-cluster ;
    # declared target — the Flux target, NOT an artifact:
    dct:relation wlid:deployment/prod/flux_kustomization/admin-cluster .
```

**Two witness forms.** A Deliverable and an Effect declare different targets and are confirmed by
different observations — the same `wl:Deployment` node, reached two ways:

| | declares | confirmed when |
|---|---|---|
| `wl:Deliverable` | Artifact + Environment | a Deployment `prov:used` that **Artifact** reaches `wlc:deployed` |
| `wl:Effect` | Deployment target (no Artifact) | that Deployment `prov:used` a **Commit** on the delivering component's default branch and reaches `wlc:deployed` |

For an Effect the component *is* the coordinate: `wl:deliveredBy` → Component → repo → default
branch fully specifies "what", so there is nothing left to name. This is why neither a placeholder
commit IRI nor a reified "where the SHA will be found" triple is needed — the declaration never
refers to a commit at all.

**v1 caveat.** `wl:cutFrom` (Artifact→Commit, §3.1) carries the delivery frontier, and its domain
is deliberately kept off Deployment (§11.1's grain note: the per-environment frontier has no node).
An Effect therefore has no *graphed* frontier in v1 — the relational `env_deploys.main_seq`
(spec 004) remains authoritative for "has main advanced far enough in this environment". Whether the
graph should carry a Commit-side or Deployment-side frontier is open question 10, not decided here.

Acceptance criteria are `dct:description` (human-readable) plus declared `dct:relation`
links to the target Artifact/Environment IRIs. **Auto-confirmation** — probing whether the
artifact was actually pushed / the deployment is actually live, flipping the Deliverable to
satisfied — is **v2** and belongs to the observed-layer derivers (spec 007).

---

## 10. Canonical IRI scheme {#sec-10}

Branch-free, version-free term & instance IRIs (ADR-0006 §3). This is the **host/namespace
commitment** that §13.2 item 3 commits the data-platform to hosting.

**Base:** `https://worklode.io/ns/` — the published base carries **no ontology-name segment**,
whatever the source path in rdf-registry is (025 §17).

| Purpose | Namespace | Prefix |
|---|---|---|
| Schema (classes/properties) | `…/ontology#<Term>` (hash) | `wl:` |
| Status concepts (SKOS) | `…/concept/<id>` (slash) | `wlc:` |
| Instances / KG nodes | `…/id/<type>/<localid>` (slash) | `wlid:` |

**Instance grammar** — `id/<type>/<localid>`, `<localid>` opaque & stable, **never**
carrying a git branch or version:

| Type | Pattern | Example |
|---|---|---|
| Component | `id/component/<slug>` (manifest slug; default = repo coords) | `…/id/component/github.com/sunstoneinstitute/worklode` |
| — multi-component repo | `id/component/<repo-coords>/<sub>` | `…/id/component/github.com/sunstoneinstitute/research-stack/pfas` |
| DesignDoc | `id/doc/<slug>` (design-file identity) | `…/id/doc/adr-0007-file-naming` , `…/id/doc/spec-worklode-006` |
| Task | `id/task/<taskid>` (backbone id, ULID/opaque) | `…/id/task/01H8XZ7K…` |
| Deliverable | `id/deliverable/<slug>` | `…/id/deliverable/worklode-graph-live` |
| Issue / PR | `id/{issue,pr}/<host>/<org>/<repo>/<number>` | `…/id/pr/github.com/sunstoneinstitute/worklode/42` |
| Artifact, Build, Commit, Deployment / Environment | natural-key grammar — `id/deployment/<…>` , `id/environment/<name>` and the rest in §10.1 | `…/id/environment/prod` |

A design document additionally carries an immutable **versioned sibling** IRI (`…/doc/<slug>/v3`),
used only in pinned claims; the canonical IRI above stays version-free (025 §17, §4).

The per-repo **component manifest** fixes each component's slug so the IRI is stable even
when directory layout shifts. Component IRIs are **branch-free**: the work graph lives on one
fixed graph-server branch (project is a *property*, not a branch — §13.2 item 5).

Slashes inside `<localid>` are permissible (slash namespace, opaque path) and match the
rdf-registry `id/` convention.

---

### 10.1 Runtime IRI grammar {#sec-10.1}

**Principle: an instance IRI mirrors the relational natural key.** Projection is then a pure
function of the row, which is what makes 007's deriver contract (deterministic, idempotent,
full-replace) satisfiable without a side table mapping rows to IRIs.

A container-shaped Artifact pattern — `id/artifact/<registry>/<repo>/<tag-or-digest>`, e.g.
`…/id/artifact/ghcr.io/sunstoneinstitute/graph-server/v1` — cannot spell a PyPI package or a binary
release, though the `artifacts` table has allowed all four kinds since the baseline migration. The
grammar is therefore kind-first, matching `UNIQUE (kind, name, version)` exactly. Nothing is
published yet, so the shape is free to fix now and breaking once the rdf-registry PR lands — the
same argument 025 §17 makes for the prefix rename.

| Type | Pattern | Natural key | Example |
|---|---|---|---|
| Artifact | `id/artifact/<kind>/<name>/<version>` | `(kind, name, version)` | `…/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1` |
| — PyPI | | | `…/id/artifact/pypi/sunstone-py/0.4.1` |
| — git tag | | | `…/id/artifact/git_tag/github.com/sunstoneinstitute/worklode/v0.4` |
| Deployment | `id/deployment/<environment>/<target-kind>/<target-name>` | `(environment, target_kind, target_name)` | `…/id/deployment/prod/flux_kustomization/graph-server` |
| Environment | `id/environment/<name>` | `name` | `…/id/environment/prod` |
| Commit | `id/commit/<host>/<org>/<repo>/<sha>` | `(repo, sha)` | `…/id/commit/github.com/sunstoneinstitute/worklode/a16c2a7` |
| Build | `id/build/<host>/<org>/<repo>/<run-id>` | — | reserved; no v1 source |
| RuntimeEvent | — | **none** | see open question 7 |

Slashes inside a local id remain permissible (slash namespace, opaque path), as §10 establishes.

---

## 11. Projection: backbone → graph {#sec-11}

**Authority stays split**: the **backbone owns execution facts** (task state, leases,
`blocks`/`child_of`); the **graph owns design facts** (Component, DesignDoc, `governs`,
`requires`, `replaces`, Deliverable). Task is the **bridge**: backbone-authoritative, mirrored
read-only into the graph. Design nodes are authored graph-side and are **never** projected from
the backbone.

**Mechanism.** A single Go **projector** service consumes the backbone's provenance/outbox event
stream and writes projected quads to `graph-server` over GSP (§13.2 items 2, 4, 5), on the
fixed work-graph branch, authenticating via Keycloak client-credentials (`dataplatform-svc`).
Projection named graphs are **anchored per Project** (open question 4): a Task's quads live in the
named graph of the one Project it `wl:inProject`. Because a graph holds many tasks, the projector
maintains a task by a **per-subject replace** — `DELETE` the task IRI's existing quads then
`INSERT` the new ones, scoped to that Project graph — rather than a whole-graph `PUT`. **Keyed by
subject IRI** → idempotent per (task, graph). A single projector + per-branch write lock makes
If-Match CAS unnecessary for v1 (§13.3 item 6). (The declared/&lt;doc&gt; and observed/&lt;source&gt;
graph families of spec 007 are orthogonal to these Project graphs.)

| Entity / edge | Layer | Authority | v1? | Projected? | Trigger |
|---|---|---|---|---|---|
| Task node + `concern`/`priority`/state/`wl:taskKind` | 2 | backbone | v1 | **yes** | task lifecycle event (create/claim/transition/done/block) |
| `wl:affects` (Task→Component) | 2 | backbone | v1 | yes | task edit / ingest |
| `wl:produces` (Task→Deliverable) | 2 | backbone | v1 | yes | task edit |
| `dct:isPartOf` (child_of mirror) | 2 | backbone | v1 | yes | task lifecycle |
| `wl:dependsOn` / `wl:blocks` (Task↔Task, transitive) | 2 | backbone | v1 | yes | task edit / block |
| `wl:inProject` (Task→Project) + Project node | 2 | backbone | v1 | yes | project edit |
| `wl:mirrors` (Task↔Issue) | 2 | backbone+ingest | v1 | yes | task create / issue ingest |
| Issue / PullRequest + `affects` | 2 | ingest | v1 | yes | VCS ingest (PR/issue open/merge) |
| Artifact / Deployment / Environment | 3 | ingest | v1 | yes (minimal) | deploy hook (declared target) |
| Component, DesignDoc, `governs`, `reviewer`, `requires`, `replaces` | 1 | **graph** | v1 | **no** (authored) | crit-approved design authoring |
| Deliverable + acceptance criteria + `wl:deliveredBy` | 1 | **graph** | v1 | no (authored) | design authoring |
| observed confirmation of Deliverable | 3 | derivers | **v2** | — | spec 007 |

`wl:implements` has no row: Component→Section claims are not projected from the backbone at all,
they are derived by `observed/repo-implements` from `.worklode/implements.yaml` (025 §11, 026 §6.2).

Task execution-state is projected as a **literal** (e.g. `wl:taskState "in_progress"`) mirroring
the backbone enum; it is not modelled as `wl:status` and does not fork the backbone state machine
(open question 3). Reads (drift, overview) run against the graph via the SPARQL path (§13.2 item 2 /
spec 007).

---

### 11.1 Runtime projection {#sec-11.1}

Feeds 007's `observed/deploy` named graph. Authority is unchanged: the backbone and ingest own every
runtime fact, the graph mirrors them read-only.

| Node / edge | Source | v1? | Trigger |
|---|---|---|---|
| `wl:Artifact` + kind, version, digest | `artifacts` | v1 | `release.published` (see below) |
| `prov:wasDerivedFrom` → `wl:Commit` | `artifacts.source_sha` | v1 | same, when the sha resolves |
| `wl:Deployment` + status, target | `deployments` | v1 | Flux webhook, PyPI publish |
| `prov:used` (Deployment → Artifact) | `deployments.artifact_id` | v1 | blocked in practice; see below |
| `wl:Environment` | fixed instance set | v1 | static |
| `wl:Commit` | `main_commits` | v1 | default-branch push |
| `wl:cutFrom` (release → commit) | `release_frontiers` | v1 | `release.published` |
| environment frontier → commit | `env_deploys` | **v2** | grain mismatch; see below |
| `wl:RuntimeEvent` | `runtime_events` | **v2** | blocked on open question 7 |
| `wl:Build` | — | **v2** | no source; needs workflow-run ingest |
| Deliverable auto-confirmation | derived | **v2** | §9, spec 007 |

**What the ingest actually produces today.** `store.CreateArtifact` has exactly one call site,
`applyRelease` in `internal/hooks/github.go`, which writes `kind = 'git_tag'`. Nothing creates
`docker_image`, `pypi` or `binary` rows, though the baseline `CHECK` constraint permits all four.
Two consequences the vocabulary cannot paper over:

- **In v1 the graph will contain `git_tag` artifacts and nothing else.** The kind-first IRI grammar
  (§10.1) is still specified for all four, because the constraint allows them and an image-publish
  hook is the obvious next ingest — but the other three kinds are grammar, not data.
- **`prov:used` from a Deployment to its Artifact will be absent.** `deployments.artifact_id` is
  resolved by `FindArtifactByImage`, which matches only `kind = 'docker_image'` — a kind with no
  producer. The column is therefore null in practice, so the edge is specified and unpopulated
  until image ingest exists. This is an ingest gap, not a modelling one, and is the single highest-value
  thing to fix if the runtime layer is meant to carry weight (open question 11).

Two classes are likewise declared but not projected, and the spec says so rather than implying
coverage. `wl:Build` exists so `prov:wasGeneratedBy` is available the day workflow-run ingest lands;
until then `prov:generatedAtTime` on the Artifact carries `built_at` and no Build node is minted.
`wl:RuntimeEvent` is declared but unprojectable until it has a natural key.

**The environment frontier has no node to hang on.** `release_frontiers` is keyed `(repo, tag)`,
which is exactly a `git_tag` artifact — so `wl:cutFrom` projects cleanly from it. `env_deploys` is
keyed `(repo, environment)`, a grain that matches nothing: `wl:Deployment` is keyed
`(environment, target_kind, target_name)` and `wl:Environment` is global. Representing it needs
either a new per-repo-per-environment node or a qualified relation, and neither is worth minting
before a query wants it. Deferred to v2; the delivery resolver reads `env_deploys` relationally in
the meantime and loses nothing.

**Guarding the `wl:Commit` edge.** `applyRelease` populates `source_sha` from the release's
`target_commitish`, which is frequently a *branch name* rather than a sha — the delivery lifecycle
(004 §5.2) already handles this by falling back to main's head. The projector
must therefore emit `prov:wasDerivedFrom` only when `source_sha` resolves to a known `main_commits`
row, and drop it otherwise. Minting `wlid:commit/…/main` from a branch name would produce a
plausible, permanently wrong node. An artifact whose `repo` is set but whose `source_sha` is null or
unresolvable projects no commit edge at all: a repository alone does not identify a commit.

---

## 12. Accepted deviations — drift suppression {#sec-12}

Some observed-but-unasserted edges are **intentional** — a sanctioned coupling the architecture
tolerates but never elevated to intent. Without suppression, spec 007's violation query reports them
forever. An accepted deviation is modelled as a **declared-layer fact** so it is crit-reviewed,
provenanced, and expirable like any other declared edge — **not** a backbone allowlist.

**Why a separate node, not a triple-term annotation.** The tolerated edge must **not** be asserted:
asserting `A dct:requires B` would make it sanctioned intent, and it would then report as
*stale-intent* drift the moment code dropped it. ADR-0001's asserted-occurrence `<< A requires B >>`
*does* assert its inner triple, so it is the wrong tool here. ADR-0001's own decision rule routes a
multi-attribute claim (rationale + authorising ADR + expiry + who/when) to **reification into a
class** — the `sc:QuantifiedAssertion` precedent. So a deviation **names** its edge with the
standard RDF reification vocabulary (`rdf:subject`/`predicate`/`object`), which does not assert it,
and stays plain RDF 1.1 (no `.1-2.ttl`).

```turtle
wlid:deviation/pfas-reads-ingest-cache
    a wl:AcceptedDeviation ;
    rdf:subject   wlid:component/.../pfas ;       # the tolerated edge — UN-asserted
    rdf:predicate dct:requires ;
    rdf:object    wlid:component/.../ingest ;
    wl:sanctionedBy wlid:doc/adr-0014 ;           # the decision that authorises it
    dct:description "pfas reads ingest's on-disk cache directly; migration tracked in WT-812" ;
    prov:wasAttributedTo wlid:agent/stig ;
    dct:created "2026-07-21"^^xsd:date ;
    dct:valid   "2026-12-31"^^xsd:date .       # OPTIONAL expiry; absent = indefinite
```

- **Home graph.** Authored into the **declared named graph of the sanctioning ADR**
  (`…/graph/declared/<adr-id>`, spec 007), under the same crit gate as any declared edge — a draft
  with an open review task until it is accepted (§4). Superseding or removing that ADR removes the suppression. No new authorisation
  mechanism: whoever may author intent may author a deviation.
- **Expiry.** Optional `dct:valid` (an `xsd:date`). Past it, the deviation stops suppressing and
  the violation re-surfaces (spec 007) — a suppression cannot silently outlive its reason. No expiry
  = indefinite, but every deviation stays listable (`lode drift --acknowledged`), never invisible.
- **Scope.** Predicate-general (names any `s/p/o`); in v1 only spec 007's 4.1 `dct:requires`
  violation query consumes it.

## 13. What the data-platform must host {#sec-13}

Worklode's **knowledge graph** (the declared architecture graph + the projected work graph)
lives in the data-platform `graph-server` (Postgres RDF quad store). The **execution backbone**
(tasks, leases, events) stays in Worklode's own Postgres — so the data-platform only has to host
the *knowledge* half. This section is the minimum the data-platform must ship for that.

### 13.1 Context {#sec-13.1}

Verified against the data-platform `graph-server`, 2026-07. Built and deployed in **dev**:
named-graph writes with a genuine single-writer serialization point
(`SELECT … FOR UPDATE` per-branch + one ACID Postgres txn), O(1) copy-on-write branch create,
child-wins overlay reads, Keycloak-authenticated HTTP (GSP), the outbox table, and **Oxigraph plus
the outbox→Oxigraph materializer** behind a real `/sparql` endpoint. The full projector path —
client-credentials token → `PUT` named graph to `main` → GSP read-back → drift query over SPARQL —
is proven end-to-end in dev by data-platform's runbook
`docs/runbooks/2026-07-22-worklode-projector-acceptance.md`.

Still open: **prod** (no graph-server manifests under `deploy/overlays/prod/`; the prod-deploy plan
is deferred pending the Hetzner prod cluster) and the rdf-registry base-URL override.

### 13.2 Must-have (v1 blockers) {#sec-13.2}

1. **Prod deployment of `graph-server`** — **open**. Dev-only today; no graph-server manifests in
   `deploy/overlays/prod/`, and data-platform's prod-deploy plan is deferred until the Hetzner prod
   cluster exists. The KG cannot be authoritative for the platform on a dev service. Items 2, 4 and
   5 are proven in dev and ride on this one to reach prod.
2. **A working query/read path** — **done in dev**. Oxigraph and the outbox→Oxigraph materializer
   are deployed (`deploy/overlays/dev/`), giving a real SPARQL endpoint; the drift query in the
   acceptance runbook returns over it. The GSP-`GET`-per-graph-and-query-in-Worklode fallback is
   dropped. Remaining work is the prod copy of these manifests (item 1).
3. **A stable, documented IRI scheme** for Worklode entities — **scheme agreed, override open**;
   aligned with rdf-registry ADR-0006
   (branch-free term IRIs; `/id/…` for instances). Worklode mints IRIs for `Component`,
   `DesignDoc`, `Task`; the host/namespace grammar must be fixed and agreed. (The canonical scheme
   is authored in §10; this item is the data-platform-side commitment to host it.)
   **Base = `https://worklode.io/ns/`** (decided): the `wl:` ontology stays in rdf-registry but its
   pipeline **publishes under the `worklode.io/ns/` base**, not `sunstone.institute/rdf/`. The
   sources live at `rdf/wl/` while the published base carries no `wl/` segment (025 §17), so this
   needs a **base-URL override** for the `wl` ontology in rdf-registry (ADR-0006's implicit "repo
   path = host path" mapping doesn't hold for a foreign domain) — a required rdf-registry change,
   not yet implemented.
4. **External-service write auth confirmed** — **done in dev**. Worklode's projector is a Go service
   authenticating via Keycloak client-credentials (`dataplatform-svc`) and `PUT`-ing named graphs.
   The acceptance runbook proves the client-credentials path end-to-end for an external caller; no
   graph-server-side config was needed, because the `dataplatform-dev:readwrite` client role travels
   under its owning client regardless of `azp`.
5. **A writable, fixed branch** for the work graph — **confirmed**. Project = property, not branch
   (sibling branches are invisible to each other, which would hide cross-project edges). The runbook
   commits to the fixed `main` branch and reads it back.

### 13.3 Should-have {#sec-13.3}

6. **`If-Match` / ETag CAS on GSP writes** (their spec's v1.1). With a *single* Worklode projector
   plus the per-branch lock, lost-update risk is already contained, so this is non-blocking — but
   wanted before any second writer touches the work graph.
7. **Per-branch / per-namespace write ACLs** (their first future-enforcement candidate). Lets
   Worklode's writes be access-scoped from other data-platform writers. Fine to defer for v1.

### 13.4 Explicitly not required from the data-platform {#sec-13.4}

- The **lease/claim/job primitive** (`graph.job` + `SKIP LOCKED`) — stays on the Worklode backbone.
- **Branch merge/diff** — design-review branches can defer merge to CI-side conflict detection.
- **Markdown-as-asset** — design content stays as files in the designs repo; only RDF *descriptors*
  (IRI, status, `governs`/`requires` edges) live in `graph-server`.

## 14. Dependencies {#sec-14}

- **Spec 004 (backbone):** owns Task state, leases, `blocks`/`child_of`; emits the event/outbox
  stream the projector consumes. Its delivery lifecycle (004 §5) owns the
  state machine and frontier arithmetic; this spec models the facts that machine reads.
- **Spec 007 (drift/query):** consumes this model — reads the two-layer diff; owns the deriver
  contract, the `observed/deploy` named graph the runtime nodes land in, and Deliverable
  auto-confirmation (v2).
- **The data-platform:** hosts the IRI scheme defined here; must ship prod `graph-server`,
  a SPARQL read path (Oxigraph + outbox materializer), external write auth, and a fixed writable
  branch (§13).
- **`internal/hooks/`** (`flux.go`, `github.go`, `deployment.go`) and `internal/store/artifacts.go`
  — the ingest whose rows project into the runtime nodes.
- **rdf-registry:** the `wl:` PR must satisfy ADR-0006 (IRI), ADR-0007 (filenames), ADR-0001
  (triple-term annotation), split the ontology into `ontology.ttl` (1.1) + `ontology.1-2.ttl`
  (1.2), add `rdf/shapes/wl-shapes.ttl` behind the **SHACL gate (ADR-0003)**, add an `owlrl`
  closure test in the **ADR-0004** style (Jena `infer` is RDFS-only, can't prove OWL closure), and
  add `wl` to the `/rdf/` DCAT/VoID index (ADR-0006 §5). The gate and the closure test cover the
  runtime terms as well as the rest.

## 15. Open questions {#sec-15}

1. ~~Deliverable minting~~ — **CONFIRMED:** mint `wl:Deliverable` (no standard for "declared
   definition-of-done").
2. ~~Section-level supersession: mint or reuse~~ — **RESOLVED:** reuse `dct:isReplacedBy` between
   addressable `wl:Section` nodes; no annotation predicate is minted (025 §3).
3. ~~Task-state representation~~ — **CONFIRMED:** projected as a literal mirror; does not fork the
   backbone-owned state machine (004).
4. ~~Named-graph granularity~~ — **RESOLVED.** Projection named graphs are anchored on
   **Projects** (`wl:Project`, the backbone's `projects` table). Every Task is `wl:inProject`
   exactly one Project and its quads live in that graph; the projector maintains a task by a
   per-subject `DELETE`/`INSERT` within it (§11). Separate from the declared/&lt;doc&gt; and
   observed/&lt;source&gt; graph families (spec 007).
5. ~~RDF-1.2 publish blocker~~ — **RESOLVED:** rdf-registry publishes 1.2 alongside 1.1
   (`.1-2.ttl` files), so triple-term annotations ship natively; no interim workaround needed.
6. ~~`wl:sanctionedBy` — mint vs. reuse~~ — **CONFIRMED:** mint it.
7. **`wl:RuntimeEvent` has no natural key.** `runtime_events` has only a surrogate id, so no
   deterministic IRI can be derived from a row and 007's idempotent full-replace contract cannot be
   met. Options: add a natural key to the table (`(cluster, kind, workload, occurred_at)` is
   plausibly unique), hash the tuple into the local id, or leave the class declared and unprojected.
   Projection stays v2 until this is settled.
8. **Should `wl:Build` be minted before its ingest exists?** It is declared here so
   `prov:wasGeneratedBy` never becomes a breaking addition, at the cost of a class with no
   instances. The alternative is deferring both to the workflow-run ingest and accepting the churn.
9. **Environment as a closed instance set.** SHACL `sh:in` over `{dev, prod}` matches today's
   normalisation, but v2 wants Cluster and Namespace beneath an Environment, and a per-cluster
   preview environment would force the shape open. Confirm the closure is worth the constraint.
10. **How should the per-environment frontier be modelled when v2 needs it?** `env_deploys`' grain
    `(repo, environment)` matches no node in this vocabulary (§11.1). A per-repo-per-environment
    node and a qualified relation are both defensible; the choice should be driven by the first
    query that actually needs "what is live in dev for repo X", not settled speculatively now.
11. **The artifact ingest gap is the real blocker.** Only `git_tag` artifacts are ever created, and
    `deployments.artifact_id` resolves through a `docker_image` lookup that can never match (§11.1).
    No vocabulary decision fixes this. Whether image-publish ingest lands before or after the `wl:`
    PR determines whether the runtime layer ships with a populated `prov:used` edge or an empty one
    — worth deciding deliberately rather than discovering at projection time.

## 16. Acceptance criteria {#sec-16}

1. Worklode's projector can, against prod `graph-server`: authenticate, `PUT` a Worklode named graph
   to the fixed branch under the agreed IRI scheme, and read it back via a SPARQL query that answers
   a drift question (e.g. "components with no governing DesignDoc"). This passes against **dev**
   today (data-platform runbook `docs/runbooks/2026-07-22-worklode-projector-acceptance.md`); prod
   remains blocked on §13.2 item 1.
2. `rdf/wl/ontology.ttl` and `rdf/wl/concept.ttl` declare all six runtime classes, seven properties
   and four SKOS schemes; every class and property carries `wl:layer wlc:runtime`, so
   `SELECT ?c WHERE { ?c wl:layer wlc:runtime }` returns exactly the runtime terms.
3. No term is imported from SPDX, DOAP, DCAT, schema.org, OSLC or `sd:`; the only external terms
   used are `prov:`, `dcterms:`, `doap:Project`, `skos:` and `owl:versionInfo`. `rdfs:seeAlso`
   citations to SPDX carry no import.
4. Each of the four artifact kinds mints a distinct, deterministic IRI under the §10.1 grammar, and
   re-projecting an unchanged `artifacts` row is a byte-identical no-op.
5. On a **seeded** graph, a `wl:Deployment` resolves to its `wl:Artifact` via `prov:used` and to
   `wlid:environment/prod` via `wl:toEnvironment`, and a SPARQL read returns the artifact currently
   live in prod for a given target. Seeded, not projected: `deployments.artifact_id` is null in
   practice today (§11.1), so this criterion tests the vocabulary, and closing the ingest gap is
   tracked separately as open question 11.
6. A `wl:Deliverable` whose `dct:relation` names an artifact IRI and `wlid:environment/prod`
   resolves both to typed nodes (§9's Deliverable example round-trips against this vocabulary).
7. The SHACL gate rejects: an Artifact with two `wl:artifactKind` values or none; a Deployment
   missing `wl:toEnvironment`; an Environment outside `{dev, prod}`.
8. An `owlrl` pass over the TBox plus a seeded ABox flags a node typed both `wl:Artifact` and
   `wl:Deployment` as inconsistent, and infers `prov:Entity` for every Artifact and
   `prov:Activity` for every Deployment — confirming PROV-aware tooling traverses the layer.
9. A release tag artifact `wl:cutFrom` the commit its `release_frontiers` row names, and that commit
   resolves to a `wl:Commit` projected from `main_commits`. A release whose `target_commitish` is a
   branch name projects **no** `prov:wasDerivedFrom` edge rather than a fabricated commit node.
10. `wl:Build` and `wl:RuntimeEvent` are declared with no instances in v1, and no acceptance
    criterion anywhere claims they are projected.
