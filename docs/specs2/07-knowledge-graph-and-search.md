# Knowledge graph and search

Worklode has two ways to find and relate what it knows. The knowledge graph is an RDF model of intent, execution and runtime, named by the `wl:` vocabulary and hosted in the data-platform `graph-server`. The backbone projects its own rows into that graph read-only and never stores a design fact of its own. The corpus index is a derived retrieval aid over the backbone's documents, tasks and skills: section-anchored chunks, a dense arm and a lexical arm fused by reciprocal rank, kept fresh by a convergence loop. Every entity is named by the URL this instance serves for it, and that URL answers HTML or RDF by content negotiation. A reversible bf16 embedding trial runs on a second vector column so no vector is ever recomputed twice.

## 1. Ownership split

No fact has two owners. The backbone (Worklode's Postgres) owns execution facts: task state, leases, `blocks`, `child_of`, events, the authored design documents (see 05-documents.md), artifacts, deployments and commits. The graph owns declared architecture facts: Component, `governs`, `reviewer`, `requires`, `replaces`, Deliverable and its acceptance criteria. Task is the bridge: backbone-authoritative, mirrored into the graph. Design documents are authored in the backbone and projected into per-document `declared/` graphs.

The corpus index moves no ownership. It is derived state, rebuildable from backbone rows at any time. Dropping every chunk costs compute and no information. The queryable graph view of documents belongs to the data platform. The index is a retrieval aid over the backbone's own copy.

## 2. Vocabulary: reuse and mint

Standards-first. A community term is reused wherever it carries the intended meaning. `wl:` terms are minted sparingly. No gtio ontology is used: `gtio-sc:Component` is a supply-chain term.

| Concept | Term | Decision |
|---|---|---|
| Dependency | `dct:requires` | reuse |
| Decomposition | `dct:hasPart` / `dct:isPartOf` | reuse |
| Supersession, whole or section | `dct:replaces` / `dct:isReplacedBy` between Section nodes | reuse |
| Owner, author | `foaf:Agent`, `prov:Agent`, `prov:wasAttributedTo` | reuse |
| Provenance | `prov:wasGeneratedBy`, `prov:wasDerivedFrom`, `prov:Activity` | reuse |
| Repository grouping | `doap:Project` (`doap:repository`, `doap:Version`) | reuse |
| Enums | `skos:Concept`, `skos:ConceptScheme`, `skos:inScheme` | reuse |
| Titles, descriptions, dates | `dct:title`, `dct:description`, `dct:created`, `dct:modified` | reuse |
| Documents | `foaf:Document` | reuse |
| Software component | `wl:Component` | mint |
| Design document | `wl:DesignDoc` with subclasses `wl:Spec` and `wl:ADR`. The `adr` kind is retired for authoring (05-documents.md §2); `wl:ADR` remains for the documents that already exist. | mint |
| Document section | `wl:Section`, `wl:lastRevisedIn` | mint |
| Design clause | `wl:Clause`, subclass of `wl:Section`: the lowest heading unit of a spec or ADR, with its own identity, status and versions (12-spec-refactoring-design-tree.md S8 to S11) | mint |
| Clause to clause | `wl:refines`, `wl:constrains`, `wl:conflictsWith` (symmetric), written by an architect; `wl:references`, derived from the clause text on every version (S12, S26) | mint |
| Plan | `wl:Plan`, sibling of DesignDoc | mint |
| Task | `wl:Task` | mint, projected |
| Deliverable, Effect | `wl:Deliverable`, `wl:Effect` subclass | mint |
| Project umbrella | `wl:Project` | mint, named-graph anchor |
| Skill and pin | `wl:Skill`, `wl:recommendsSkill` | mint |
| Issue, pull request | `wl:Issue`, `wl:PullRequest`, both `prov:Entity` | mint |
| Design to component | `wl:governs` | mint |
| Deliverable to component | `wl:deliveredBy` | mint |
| Plan to section undertaking | `wl:covers`, qualified by `wl:Coverage` (`wl:coverageLevel` from `wlc:CoverageLevel`) | mint |
| Plan to section handoff | `wl:defers`, qualified by `wl:Deferral` | mint |
| Component to section evidence | `wl:implements`, annotated with `wl:pinnedVersion` | mint |
| Task to deliverable | `wl:produces` | mint |
| Work to component | `wl:affects` | mint |
| Lifecycle status | `wl:status` with `wlc:DesignDocStatus` | mint |
| Task kind | `wl:taskKind` with `wlc:TaskKind` | mint |
| Task state, priority, concern mirrors | `wl:taskState`, `wl:priority`, `wl:concern` literals | mint |
| Task dependency | `wl:dependsOn` / `wl:blocks`, transitive | mint |
| Task membership | `wl:inProject`, exactly one | mint |
| Follow-up | `wl:followUpTo` | mint |
| Human-only flag | `wl:humanOnly`, emitted only when true | mint |
| Component reviewer | `wl:reviewer` to `foaf:Agent` | mint |
| Task and issue mirror | `wl:mirrors`, symmetric | mint |
| Model-layer tag | `wl:layer` with `wlc:ModelLayer` | mint |
| Accepted deviation | `wl:AcceptedDeviation`, `wl:sanctionedBy`, RDF reification | mint |
| Release to commit | `wl:cutFrom` | mint |
| Runtime layer | `wl:Artifact`, `wl:Build`, `wl:Deployment`, `wl:Environment`, `wl:Commit`, `wl:RuntimeEvent`, seven properties, four schemes | mint |

`wl:Milestone` is deferred to v2. The runtime layer was surveyed against PROV-O, SPDX 3.0.1, DOAP, DCAT v3, schema.org, OSLC Automation, `sd:`, SLSA/in-toto, CycloneDX and TOSCA. PROV-O is the anchor; the rest offer no truthful parent. SPDX and `sd:` are cited with `rdfs:seeAlso` and never imported. The only external terms in use are `prov:`, `dcterms:`, `doap:Project`, `skos:` and `owl:versionInfo`.

The vocabulary ships to `rdf-registry` as `rdf/wl/ontology.ttl` (RDF 1.1), `rdf/wl/ontology.1-2.ttl` (RDF 1.2 triple-term annotations), `rdf/wl/concept.ttl` (SKOS) and `rdf/shapes/wl-shapes.ttl`, behind its SHACL gate and an `owlrl` closure test, indexed in the `/rdf/` DCAT/VoID index. The published base is `https://worklode.io/ns/`, which needs a base-URL override in rdf-registry. The repo-local source is `ns/` (`ontology.ttl`, `concept.ttl`, `shapes.ttl`); `scripts/nsgen.py` reads `ns/concept.ttl` only and emits `internal/ns/gen.go`.

## 3. Classes

Intent and execution classes:

| Class | Parent | Notes |
|---|---|---|
| `wl:Component` | none | atomic unit of the platform graph; a repo (`doap:Project`) holds many via `dct:hasPart` |
| `wl:DesignDoc` | `foaf:Document`, `prov:Entity` | `prov:Entity` is required because documents carry `prov:wasGeneratedBy`, `prov:wasRevisionOf`, `prov:wasAttributedTo` |
| `wl:ADR`, `wl:Spec` | `wl:DesignDoc` | disjoint |
| `wl:Section` | `foaf:Document` | addressable part of a DesignDoc; stable for the document's life, never deleted after acceptance |
| `wl:Plan` | `foaf:Document`, `prov:Entity` | reviewable and accept-gated, mutable and anchor-free; acceptance mints the execution subtree |
| `wl:Task` | `prov:Activity` | projected; authorship is `prov:wasAssociatedWith` because Activity and Entity are disjoint in PROV |
| `wl:Deliverable` | none | declared definition-of-done |
| `wl:Effect` | `wl:Deliverable` | ships no artifact; target is a Deployment IRI; witness is a Commit |
| `wl:Project` | none | the backbone `projects` table verbatim; unbounded; owns 1..n repos; anchors projection named graphs |
| `wl:AcceptedDeviation` | none | sanctioned observed-but-unasserted edge (section 9) |

Runtime classes, each tagged `wl:layer wlc:runtime`:

| Class | Parent | Meaning |
|---|---|---|
| `wl:Artifact` | `prov:Entity` | one built, versioned unit: container image, PyPI package, git tag, binary. Kind is `wl:artifactKind`, never a subclass, because the kinds carry identical edges |
| `wl:Build` | `prov:Activity` | the CI run that produced an Artifact; declared so `prov:wasGeneratedBy` is available; no v1 source |
| `wl:Deployment` | `prov:Activity` | rollout of an Artifact or a Commit to one target in one Environment; one node per (environment, target kind, target name); current state only, history is v2 |
| `wl:Environment` | none | a deployment stage; closed to `dev` and `prod` by SHACL |
| `wl:Commit` | `prov:Entity` | one commit on a repository's default branch; on the delivery state machine's critical path |
| `wl:RuntimeEvent` | `prov:Activity` | crashloop, OOM kill, Flux failure or recovery |

A git tag release is a `wl:Artifact` of kind `wlc:git_tag` with a `wl:cutFrom` edge to the commit its frontier reaches. There is no `wl:Release`.

Two `owl:AllDisjointClasses` axioms hold: one over `wl:Component wl:DesignDoc wl:Plan wl:Section wl:Task wl:Deliverable wl:Project wl:Skill`, one over the six runtime classes, plus `wl:ADR` / `wl:Spec`. Effect inherits Deliverable's disjointness.

## 4. Properties

| Property | Domain | Range | Characteristics |
|---|---|---|---|
| `wl:governs` | DesignDoc | Component | declared intent |
| `wl:deliveredBy` | Deliverable | Component | declared; at least one (SHACL); deliberately not functional |
| `wl:covers` | Plan | Section | planning intent; the qualified `wl:Coverage` node carries the level (`wlc:full`, `wlc:partial`, `wlc:none`); see 06-design-queries-intents-decks.md §4.6 |
| `wl:defers` | Plan | Section | handoff to the owner named on the qualified `wl:Deferral` node |
| `wl:implements` | Component | Section | evidence only; derived by `observed/repo-implements` from `.worklode/implements.yaml`; never declared on a plan or a task |
| `wl:pinnedVersion` | triple term `<< c wl:implements s >>` | DesignDoc version snapshot | functional; RDF 1.2 annotation; stale-claim query compares `dcat:version` numbers, never IRI strings |
| `wl:produces` | Task | Deliverable | the task that makes the deliverable exist |
| `wl:affects` | (Task, Issue, PullRequest) | Component | observed |
| `wl:status` | DesignDoc, Plan, Section (union) | `skos:Concept` in `wlc:DesignDocStatus` | functional; SHACL requires one |
| `wl:sanctionedBy` | AcceptedDeviation | DesignDoc | the authorising ADR |
| `wl:taskKind` | Task | `wlc:TaskKind` concept | functional |
| `wl:priority` | Task | `xsd:string` | functional literal mirror of the backbone enum (critical/high/medium/low) |
| `wl:concern` | Task | `xsd:string` | literal mirror; absent when none |
| `wl:taskState` | Task | literal | mirror of `tasks.state`; legal values are the column's CHECK; transitions are not modelled |
| `wl:dependsOn` | Task | Task | transitive; `rdfs:subPropertyOf dct:requires` |
| `wl:blocks` | Task | Task | transitive; `owl:inverseOf wl:dependsOn` |
| `wl:inProject` | Task | Project | functional; from `tasks.project_id` |
| `wl:reviewer` | Component | `foaf:Agent` | notify on PRs |
| `wl:mirrors` | Task or Issue | Task or Issue | symmetric; domain and range are the same union so no Issue is entailed to be a Task; SHACL pins the pairing |
| `wl:layer` | any term | `wlc:ModelLayer` concept | annotation property |
| `wl:artifactKind` | Artifact | `wlc:ArtifactKind` | functional |
| `wl:digest` | Artifact | `xsd:string` | opaque prefixed digest such as `sha256:...` |
| `wl:toEnvironment` | Deployment | Environment | functional |
| `wl:deploymentStatus` | Deployment | `wlc:DeploymentStatus` | functional |
| `wl:targetKind` | Deployment | `wlc:DeployTargetKind` | functional |
| `wl:runtimeEventKind` | RuntimeEvent | `wlc:RuntimeEventKind` | functional |
| `wl:cutFrom` | Artifact | Commit | the commit a published release was cut from |

The three section-facing predicates split by domain: a Plan `covers` a section, a Component `implements` it, a Task `produces` a Deliverable. None of the three is asserted from another domain. Task execution state is never `wl:status`. The backbone owns the state machine (see 03-tasks-and-execution.md); the graph mirrors it as a literal.

Reused runtime terms and their source columns:

| Fact | Term | Column |
|---|---|---|
| built by | `prov:wasGeneratedBy` to `wl:Build` | no v1 source |
| built from | `prov:wasDerivedFrom` to `wl:Commit` | `artifacts.source_sha` |
| build time | `prov:generatedAtTime` on the Artifact | `artifacts.built_at` |
| deployment uses | `prov:used` | `deployments.artifact_id` |
| event affects | `prov:used` | `runtime_events.artifact_id` |
| first seen | `prov:startedAtTime` | `deployments.first_seen` |
| last updated | `dct:modified` | `deployments.last_update` |
| event or publication time | `dct:date` | `runtime_events.occurred_at`, `release_frontiers.published_at` |
| version | `owl:versionInfo` | `artifacts.version` |
| name, target, sha | `dct:identifier` | the coordinate in the IRI |
| label | `dct:title` | |

Implementation is one statement, answered by a query and never a node: "Component A implemented Section B by deploying Deliverable C to Environment D."

```sparql
?component   wl:implements  ?section .
?deliverable wl:deliveredBy ?component ; dct:relation ?artifact , ?env .
?deployment  prov:used ?artifact ; wl:toEnvironment ?env .
```

This answers "is this spec implemented?" per environment. For an Effect the last two clauses bind a Commit instead of an Artifact.

## 5. SKOS schemes

All enums are controlled vocabularies mirroring a schema CHECK constraint. None is free text.

| Scheme | Members |
|---|---|
| `wlc:DesignDocStatus` | `draft`, `accepted`, `superseded`; order as data in `wlc:DesignDocStatusOrder` (`skos:OrderedCollection`) |
| `wlc:TaskKind` | `feature`, `bug`, `chore`, `design`, `review`, `spike`, `decision`, `rally` |
| `wlc:ModelLayer` | `intent`, `execution`, `runtime` |
| `wlc:ArtifactKind` | `docker_image`, `pypi`, `git_tag`, `binary` |
| `wlc:DeploymentStatus` | `pending`, `reconciling`, `deployed`, `failed` |
| `wlc:DeployTargetKind` | `flux_kustomization` (HelmRelease events file here too), `pypi_target`, `manual` |
| `wlc:RuntimeEventKind` | `crashloop`, `oom`, `flux_failure`, `flux_recovery` |

There is no `implemented` status; implementation is a per-section coverage query (see 05-documents.md). There is no `under review` status; a document under review is a draft with an open review task. Transition rules live with the authoring skill. No task kind names a container; containment is inferred from `child_of`. `wlc:rally` is the one structural kind: a hand-assembled goal with no work of its own, never claimable, no children, blocks nothing. `wlc:pypi_target` is spelled apart from `wlc:pypi` because target kind and artifact kind are different concepts. Every minted term carries a `wl:layer` tag.

## 6. Reasoning tiers

| Tier | Where | Does | Idioms |
|---|---|---|---|
| Runtime | `graph-server` / Oxigraph | SPARQL 1.1, no reasoner | property paths (`?t wl:dependsOn+ ?x`), `?c wl:layer wlc:intent` |
| CI, OWL 2 RL | `owlrl` in tests | classification, disjointness, transitive closure as proof | `owl:AllDisjointClasses`, `TransitiveProperty`, `FunctionalProperty`, `unionOf`, `inverseOf` |
| CI, SHACL gate | Jena `shacl` over the shapes graph | closed-world presence and cardinality | node shapes below |

OWL is open-world and never flags a missing field or a duplicate, so OWL classifies and SHACL enforces presence and cardinality. `owl:FunctionalProperty` catches more than one; SHACL catches zero. Closure is never published: the pipeline ships declared edges plus TBox, and entailments are re-derived live by property paths. SHACL validation of projected data runs over the union of all project named graphs plus the TBox, because a cross-project edge's foreign endpoint is typed only in its home graph.

Node shapes:

| Shape | Constraints |
|---|---|
| Task | exactly one `wl:taskKind`, one state literal, one `wl:inProject` |
| Component | at least one `wl:reviewer` |
| Deliverable | `dct:description`, at least one `dct:relation`, at least one `wl:deliveredBy` |
| Effect | Deliverable shape plus at least one `dct:relation` to a Deployment and zero to an Artifact |
| AcceptedDeviation | `rdf:subject`, `rdf:predicate`, `rdf:object`, `wl:sanctionedBy`, optional `dct:valid` |
| DesignDoc, Plan | exactly one `wl:status` from `wlc:DesignDocStatus` |
| Artifact | exactly one each of `wl:artifactKind`, `owl:versionInfo`, `dct:identifier` |
| Deployment | exactly one each of `wl:toEnvironment`, `wl:targetKind`, `wl:deploymentStatus`; at most one `prov:used` |
| Environment | `sh:in` over the `dev` and `prod` instances |
| Commit | exactly one `dct:identifier` |

The shapes graph is split in two. Product shapes (`https://worklode.io/ns/shapes.ttl`, published by rdf-registry) constrain `wl:` terms and are true of every instance. Instance shapes (`<base>/shapes.ttl`, served by this server from the static file `ns/instance-shapes.ttl`) name instance IRIs, such as the environment closure, written as relative IRIs (`<environments/prod>`) resolved against the emitted `@base`. The instance graph `owl:imports` the product graph, so a validator fetching one link gets both.

## 7. Entity model by layer

Layer 1, intent, declared and authored in the graph:

| Node | Class | Status |
|---|---|---|
| Component | `wl:Component` | v1, per-repo manifest declares boundaries |
| DesignDoc | `wl:ADR` / `wl:Spec`, addressable by `wl:Section` | v1, authored in the backbone |
| Deliverable, Effect | `wl:Deliverable`, `wl:Effect` | v1 |
| Milestone | `wl:Milestone` | v2 |

Intent edges: `wl:governs`, `wl:reviewer`, `wl:deliveredBy`, `dct:hasPart`, `dct:requires`, `dct:replaces`.

Layer 2, execution and VCS, observed:

| Node | Class | Status |
|---|---|---|
| Task | `wl:Task` | v1, projected from the backbone |
| Project | `wl:Project` | v1, projected, named-graph anchor |
| Plan | `wl:Plan` | v1, authored |
| Issue, PullRequest | `wl:Issue`, `wl:PullRequest` | v1, projected from VCS ingest |
| Branch, Event | | v2 |

Execution edges: `wl:produces`, `wl:affects`, `wl:taskKind`, `wl:mirrors`, `wl:inProject`, `wl:dependsOn` / `wl:blocks`, `dct:isPartOf` (`child_of`), `prov:wasAttributedTo` from Issue or PullRequest, `prov:wasAssociatedWith` from Task.

Layer 3, runtime and deploy, is the six runtime classes of section 3. The layers join vertically at Deliverable.

## 8. Deliverable and Effect

A Deliverable is the declared target where a spec's intent meets a running system. In v1 it is declared only. It names the Component that delivers it and typed `dct:relation` targets.

| | Declares | Confirmed when |
|---|---|---|
| `wl:Deliverable` | Artifact plus Environment | a Deployment `prov:used` that Artifact reaches `wlc:deployed` |
| `wl:Effect` | Deployment target, no Artifact | that Deployment `prov:used` a Commit on the delivering component's default branch and reaches `wlc:deployed` |

An Effect's target IRI is stable (environment, target kind, target name). A Commit IRI carries a SHA and cannot be named in advance, so the commit is discovered, never declared. A Spec scopes a deliverable through `dct:hasPart`; a Task realises it through `wl:produces`. Auto-confirmation, probing whether the artifact is live and flipping the deliverable to satisfied, is v2 and belongs to the observed-layer derivers (see 06-design-queries-intents-decks.md). An Effect has no graphed frontier in v1; `env_deploys.main_seq` stays authoritative relationally.

## 9. Accepted deviations

An observed-but-unasserted edge that the architecture tolerates is a declared-layer fact, crit-reviewed, provenanced and expirable. The tolerated edge must stay unasserted, otherwise it would become intent and report as stale-intent drift the moment code dropped it. A triple-term annotation asserts its inner triple, so the deviation names its edge with RDF reification (`rdf:subject`, `rdf:predicate`, `rdf:object`) in plain RDF 1.1.

- Home graph: the declared named graph of the sanctioning ADR, under the same crit gate. Superseding or removing the ADR removes the suppression.
- Expiry: optional `dct:valid` (`xsd:date`). Past it the violation re-surfaces. Absent means indefinite, and every deviation stays listable through `lode graph drift --acknowledged`.
- Scope: any s/p/o; in v1 only the `dct:requires` violation query consumes it.

## 10. IRIs

### 10.1 Namespaces

The IRI is the URL this instance serves, and the instance owns the base. There is no second identifier scheme.

| Prefix | Namespace | Scope | Publisher |
|---|---|---|---|
| `wl:` | `https://worklode.io/ns/ontology#` | the product | rdf-registry, once |
| `wlc:` | `https://worklode.io/ns/concept/` | the product | rdf-registry, once |
| `wlid:` | the instance's own base URL, e.g. `https://lode.sunstoneinstitute.ai/` | one deployment | the deployment |

`wlid:` is a convention, never a constant. No stored string may contain the expansion. Every Turtle or JSON-LD document using it carries a generated `@prefix` or `@context` binding. The server derives the base from the request's scheme and host; `LODE_PUBLIC_URL` is the fallback where there is no request (background loops, CLI-side renders). The base selects no data, grants no access and is never stored.

### 10.2 Instance grammar

Local part is the plural type followed by the natural key, one path segment per key column. Slashes inside a key are legal, which makes some CURIEs illegal as Turtle prefixed names; Turtle output writes those IRIs relative to `@base`. Every IRI is branch-free and version-free.

| Type | Path | Example |
|---|---|---|
| Task | `/tasks/<task-id>` | `/tasks/WL-42` |
| Document | `/docs/<KEY>-<TYPE>-<n>` | `/docs/WL-SPEC-25` |
| Section | `/docs/<KEY>-<TYPE>-<n>#<anchor>` | `/docs/WL-SPEC-25#sec-4.1` |
| Document version | `/docs/<KEY>-<TYPE>-<n>/<v>` | `/docs/WL-SPEC-25/3` |
| Project | `/projects/<project-id>` | `/projects/worklode` |
| Agent | `/agents/<actor-id>` | `/agents/github.com/stigsb` |
| Component | `/components/<slug>` | `/components/github.com/sunstoneinstitute/worklode` |
| Deliverable | `/deliverables/<id>` | `/deliverables/worklode-graph-live` |
| Skill | `/skills/<name>` | `/skills/superpowers:brainstorming` |
| Event | `/events/<id>` | `/events/8814` |
| Issue | `/issues/<host>/<owner>/<repo>/<number>` | `/issues/github.com/sunstoneinstitute/worklode/42` |
| Pull request | `/pulls/<host>/<owner>/<repo>/<number>` | `/pulls/github.com/sunstoneinstitute/worklode/42` |
| Repository | `/repos/<host>/<owner>/<name>` | `/repos/github.com/sunstoneinstitute/worklode` |
| Commit | `/commits/<host>/<owner>/<repo>/<sha>` | `/commits/github.com/sunstoneinstitute/worklode/a16c2a7` |
| Artifact | `/artifacts/<kind>/<name>/<version>` | `/artifacts/pypi/sunstone-py/0.4.1` |
| Deployment | `/deployments/<env>/<target-kind>/<target-name>` | `/deployments/prod/flux_kustomization/graph-server` |
| Environment | `/environments/<name>` | `/environments/prod` |

Named graphs take the same base under `graphs/`: `/graphs/project/<project-id>`, `/graphs/declared/<doc>`, `/graphs/observed/<source>`. Follow-up (design tree S20, S63): the paragraph below this table gives the canonical cockpit URL scheme; the Task, Document, Section and Document version rows move to it when the IRIs do. The document number is unpadded so `025` and `25` cannot be two names. `<KEY>-<TYPE>-<n>` parses unambiguously by splitting from the right, since the type set is closed and `<n>` is digits, so an IRI resolves to a row through the ordinary reference lookup. The Artifact grammar is kind-first, matching `UNIQUE (kind, name, version)`. `wl:Build` (`/builds/<host>/<owner>/<repo>/<run-id>`) is reserved with no v1 source; `wl:RuntimeEvent` has no natural key and no IRI.

**Follow-up (12-spec-refactoring-design-tree.md S20, A3, S63).** The canonical URL of an entity is `/projects/<proj>/<kind>/<n>`, with `/<ver>` for a version, where `<proj>` is the project id and `<kind>` is unique across document kinds (`spec`, `adr`, `plan`, `clause`) and task kinds (`feature`, `bug`, `chore`, `design`, `review`, `spike`, `decision`, `rally`). The cockpit serves clauses (`/projects/worklode/clause/12`), documents (`/projects/worklode/spec/25`, `/projects/worklode/spec/25/3`) and tasks (`/projects/worklode/feature/42`) there. Tasks have no version path. A task named under a kind other than its own redirects to its own kind. An uppercase `<proj>` is a project key typed by habit and redirects to the id form. The root routes redirect to the canonical URL: `/tasks/<id>`, `/docs/<KEY>-<TYPE>-<n>` (with `?v=<n>` going to the version path), `/docs/versions/<row id>/<v>`, and the bare-reference shortcut `/<ref>`. A document with no number keeps its page at `/docs/<row id>`. `/clauses/<KEY>-CL-<n>` is the resolving redirect prose links use. The IRIs in the table above, the stored strings of §10.3 and the `implements.yaml` section paths keep the `/tasks/` and `/docs/` forms until the IRIs move; they resolve to the canonical page through the redirects.

A section is a fragment of its document. `#sec-4.1` is the HTML anchor and the RDF subject in one string, and a section's triples ship inside the document's representation. A version is a path segment; the cockpit's `?v=<n>` query is a 302 to it. The reference redirect for any reference shape lives at `GET /ref/{ref...}` and identifies nothing. Ordering of versions is `dcat:version`, never the IRI (see 05-documents.md).

### 10.3 Stored strings stay relative

No absolute instance IRI is written to Postgres, an event payload, or a repository file. Stored forms are the CURIE (`wlid:tasks/WL-42`) or the path (`/tasks/WL-42`). Absolute IRIs exist only in response bodies, built at serve time. Moving a backbone between hosts therefore needs no rewrite pass.

### 10.4 One constructor

`internal/kg/iri` is the only place an instance IRI or entity path is built. Every type above has one constructor returning the path and one CURIE form. One function produces absolute IRIs from a base and a path; callers never concatenate. The cockpit's link builders sit on top of the same constructors so a page link and a triple subject cannot drift. A guard test fails the build on a `wlid:` literal or a hand-built `/tasks/`-style path outside `internal/kg/iri`.

## 11. Content negotiation

Every IRI in section 10.2 answers `GET` and picks its representation from `Accept`:

| `Accept` | Response |
|---|---|
| `text/html`, `*/*`, absent | the cockpit page, or 404 where no page exists yet |
| `text/turtle` | Turtle with a generated `@base` and `@prefix` header |
| `application/n-triples` | N-Triples, absolute IRIs |
| `application/ld+json`, `application/json` | JSON-LD with a generated `@context` |
| anything else | 406 |

An entity with no page still has a path: it answers RDF and 404s for HTML. `GET /shapes.ttl` is the one exception to negotiation: the extension names the format and the response is always Turtle with a request-derived `@base` and an `owl:imports` of the product shapes. The RDF representation is gated exactly as the HTML one, by the same `routeGuards` entry (see 02-identity-actors-and-secrets.md). Negotiation changes the format of an answer and never who may have it. Triples come from `internal/graphproj`, which projects tasks, documents and sections deterministically and guarantees a subject-complete set per subject.

## 12. Projection: backbone to graph

A single Go projector (`internal/projector`) consumes the backbone event stream and writes quads to `graph-server` over the Graph Store Protocol on the fixed `main` branch, authenticating with Keycloak client credentials (`dataplatform-svc`). graph-server exposes no SPARQL Update, so the projector recomputes every task of a dirty Project and replaces that Project's named graph wholesale with `PUT`. Rendering is deterministic, so an unchanged re-projection is byte-identical and idempotent. A Project's graph is subject-complete and object-open: every triple whose subject the project owns, and none about another subject. A cross-project edge's foreign endpoint stays a bare untyped IRI; no type stub or mirrored foreign task is emitted. The `declared/<doc>` and `observed/<source>` graph families are orthogonal to the Project graphs.

| Entity or edge | Layer | Authority | v1 | Projected | Trigger |
|---|---|---|---|---|---|
| Task node with `wl:concern`, `wl:priority`, `wl:taskState`, `wl:taskKind`, `wl:humanOnly` (when true), `dct:title`/`created`/`modified`, `prov:wasAssociatedWith` | 2 | backbone | yes | yes | task lifecycle event |
| `wl:affects` (Task to Component) | 2 | backbone | v2 | no source | |
| `wl:produces` (Task to Deliverable) | 2 | backbone | v2 | no source | |
| `dct:isPartOf` (`child_of`) | 2 | backbone | yes | yes | task lifecycle |
| `wl:dependsOn` / `wl:blocks` | 2 | backbone | yes | yes | task edit or block |
| `wl:followUpTo` | 2 | backbone | yes | yes | `--follow-up-to` |
| `wl:inProject` and Project node | 2 | backbone | yes | yes | project edit |
| `wl:mirrors` | 2 | backbone and ingest | yes | yes | task create or issue ingest |
| Issue, PullRequest, `affects` | 2 | ingest | yes | yes | VCS ingest |
| Artifact, Deployment, Environment | 3 | ingest | yes | minimal | deploy hook |
| Component, DesignDoc, `governs`, `reviewer`, `requires`, `replaces` | 1 | graph | yes | authored | design authoring |
| Deliverable, criteria, `wl:deliveredBy` | 1 | graph | yes | authored | design authoring |
| Deliverable confirmation | 3 | derivers | v2 | | |

`wl:produces` and `wl:affects` are out of v1 because the backbone stores neither relation; projecting a predicate with no source would fabricate facts. The only live `wl:affects` emission is the observed-layer deriver over PR changed paths and the manifest. `wl:implements` is derived from `.worklode/implements.yaml` and never projected. Design documents project canonical node first: type, title, status, `dcat:version`, `prov:wasGeneratedBy`.

Runtime projection feeds the `observed/deploy` graph:

| Node or edge | Source | v1 | Trigger |
|---|---|---|---|
| `wl:Artifact` with kind, version, digest | `artifacts` | yes | `release.published` |
| `prov:wasDerivedFrom` to `wl:Commit` | `artifacts.source_sha` | yes, when the sha resolves | same |
| `wl:Deployment` with status, target | `deployments` | yes | Flux webhook, PyPI publish |
| `prov:used` (Deployment to Artifact) | `deployments.artifact_id` | specified, null in practice | |
| `wl:Environment` | fixed set | yes | static |
| `wl:Commit` | `main_commits` | yes | default-branch push |
| `wl:cutFrom` | `release_frontiers` | yes | `release.published` |
| environment frontier | `env_deploys` | v2 | grain mismatch |
| `wl:RuntimeEvent` | `runtime_events` | v2 | no natural key |
| `wl:Build` | | v2 | no workflow-run ingest |

Only `git_tag` artifacts are created today (`applyRelease` in `internal/hooks/github.go`), so `prov:used` is unpopulated until image ingest exists. The projector emits `prov:wasDerivedFrom` only when `source_sha` resolves to a `main_commits` row; a branch name in `target_commitish` projects no commit edge. `env_deploys` is keyed `(repo, environment)`, which matches no node, so the per-environment frontier has no node.

## 13. What the data platform hosts

The knowledge graph lives in the data-platform `graph-server` (Postgres RDF quad store with Oxigraph and an outbox materializer behind `/sparql`). The execution backbone stays in Worklode's Postgres.

Worklode needs from `graph-server`:

| Requirement | Need |
|---|---|
| `graph-server` in every environment Worklode runs in | must |
| SPARQL read path | must |
| `worklode.io/ns/` base override in rdf-registry | must |
| External write auth via Keycloak client credentials | must |
| Fixed writable `main` branch; project is a property, never a branch | must |
| `If-Match` / ETag CAS on GSP writes | before a second writer exists |
| Per-branch write ACLs | should |

Not required from the data platform: the lease and claim primitive, branch merge and diff, and Markdown as an asset. Only RDF descriptors live in `graph-server`.

## 14. Corpus index

### 14.1 Subjects

| Kind | Source | Unit of retrieval |
|---|---|---|
| `doc` | `docs.body`, split on `doc_sections` | a section |
| `task` | `tasks.title` plus `tasks.body` | the task |
| `skill` | `skill_versions.skill_md` of the latest version, description prepended | the skill |

Events, transcripts, inbox items, blobs and task comments are not indexed. Soft-deleted skills' chunks are deleted, so the index carries no tombstones.

### 14.2 Embedding space

The embedding width is fixed at 768 dimensions for every provider. This makes vectors indexable (pgvector HNSW indexes only up to 2000 dimensions), lets a model swap re-embed without a migration, and turns the typmod into an INSERT-time guard. All supported models are Matryoshka-trained, so truncation yields a real embedding.

| Provider | Model | Width knob |
|---|---|---|
| Default, self-hosted CPU | `google/embeddinggemma-300m` via a `text-embeddings-inference` sidecar with OpenAI-compatible `/v1/embeddings` | native 768 |
| OpenAI | `text-embedding-3-small` | `dimensions: 768` |
| Google | `gemini-embedding-001` | `output_dimensionality: 768`, renormalise; native 3072 is unindexable |

The default runs on CPU with no GPU and no third-party call, because the corpus is the org's unreleased written record. The default deployment is `LODE_EMBEDDING_URL` pointed at the sidecar. A fourth provider must reach exactly 768 and speak an OpenAI-compatible endpoint or implement `embed.Provider`. Anthropic serves no embeddings API.

### 14.3 Provider interface

```go
type Role int // Document | Query

type Provider interface {
    Embed(ctx context.Context, role Role, texts []string) ([][]float32, error)
    ID() string
    Dim() int // must be 768; NewServer refuses a provider that disagrees
}
```

Queries and documents are different inputs: EmbeddingGemma expects `task: search result | query: ` and `title: none | text: ` prefixes, Gemini a `task_type`. `OpenAI` carries `QueryPrefix`, `DocumentPrefix` (`LODE_EMBEDDING_QUERY_PREFIX`, `LODE_EMBEDDING_DOCUMENT_PREFIX`) and a `Dimensions` request field; a symmetric model sets both prefixes empty. `ID()` names the embedding space and has the shape `openai:<model>@<width>@<host><path>[#<prefix digest>]`, where the digest is the first 8 hex characters of SHA-256 over query prefix, NUL, document prefix, omitted when both are empty.

### 14.4 Chunking

`LODE_EMBEDDING_CONTEXT_TOKENS` names the embedding model's context window and is required whenever `LODE_EMBEDDING_MODEL` is set; `NewServer` refuses to boot without it. The chunk budget derives from it: `ChunkRunes = context tokens * 1.75` (the rune per token ratio that kept 3600 runes inside EmbeddingGemma's 2048 window), `ChunkOverlap = ChunkRunes / 6`. Nothing in the code carries a model's window as a constant.

- Documents chunk one per section in `position` order, on the frozen `{#sec-N}` anchors in `doc_sections`. A section longer than `ChunkRunes` splits into overlapping sub-chunks that inherit the anchor. Short sections are never merged. Plans carry no anchors and chunk on `##`/`###` headings with an empty anchor, falling back to fixed windows.
- Tasks are `title + "\n\n" + body`, one chunk unless the body exceeds `ChunkRunes`. An empty body is still indexed on its title.
- Every chunk carries a stored context header built from existing columns: `WL-SPEC-25 "Documents in the backbone" — §15.2 The ordered log`, `WL-142 [feature/in_progress] Fix the thing`, `skill: test-driven-development. <description>`. It is prepended to the dense embed input, indexed at lexical weight `A` against the body's `B`, kept reproducible, and counts against the budget. It is excluded from returned excerpts.

### 14.5 Storage

One table holds all three kinds, each with its own nullable cascading FK and a CHECK that exactly one is set.

```sql
CREATE TABLE index_chunks (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    subject_kind   text NOT NULL CHECK (subject_kind IN ('doc', 'task', 'skill')),
    doc_id         bigint REFERENCES docs   (id) ON DELETE CASCADE,
    task_id        text   REFERENCES tasks  (id) ON DELETE CASCADE,
    skill_id       bigint REFERENCES skills (id) ON DELETE CASCADE,
    project        text REFERENCES projects (id) ON DELETE RESTRICT,  -- null for org-wide skills
    anchor         text NOT NULL DEFAULT '',
    chunk_index    int  NOT NULL,
    context_header text NOT NULL DEFAULT '',
    chunk_text     text NOT NULL,
    content_hash   text NOT NULL,           -- hash of the subject's source text, computed in SQL
    embedding      vector(768),             -- nullable: cleared on provider change
    embedding_bf16 vector(768),             -- the trial column, section 18
    tsv tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', context_header), 'A') ||
        setweight(to_tsvector('simple', chunk_text), 'B')
    ) STORED,
    indexed_at     timestamptz NOT NULL,
    CONSTRAINT index_chunks_one_subject CHECK (num_nonnulls(doc_id, task_id, skill_id) = 1),
    CONSTRAINT index_chunks_kind_matches_subject CHECK (
        (subject_kind = 'doc'   AND doc_id   IS NOT NULL) OR
        (subject_kind = 'task'  AND task_id  IS NOT NULL) OR
        (subject_kind = 'skill' AND skill_id IS NOT NULL))
);
CREATE UNIQUE INDEX index_chunks_doc   ON index_chunks (doc_id, anchor, chunk_index) WHERE doc_id IS NOT NULL;
CREATE UNIQUE INDEX index_chunks_task  ON index_chunks (task_id, chunk_index) WHERE task_id IS NOT NULL;
CREATE UNIQUE INDEX index_chunks_skill ON index_chunks (skill_id, chunk_index) WHERE skill_id IS NOT NULL;
CREATE INDEX index_chunks_kind_project ON index_chunks (subject_kind, project);
CREATE INDEX index_chunks_embedding      ON index_chunks USING hnsw (embedding vector_cosine_ops);
CREATE INDEX index_chunks_embedding_bf16 ON index_chunks USING hnsw (embedding_bf16 vector_cosine_ops);
CREATE INDEX index_chunks_tsv ON index_chunks USING gin (tsv);
```

The `tsv` column is generated, so it cannot drift from `chunk_text`; the two-argument `to_tsvector(regconfig, text)` is IMMUTABLE and the one-argument form must not be used. `embedding_config` records the provider id per vector column.

## 15. Hybrid search

A search runs two retrievers over `index_chunks` and fuses their rankings. Neither arm is primary and neither is a fallback.

**Dense arm.** The query is embedded once with `Role = Query` and scored by cosine similarity, max-pooled per subject (docs by `(doc_id, anchor)`, so one spec may return two sections). Max pooling lets one strong section surface its document. Pooling happens before ranking so a long document cannot accumulate RRF mass from many mediocre chunks. The similarity floor (default 0.35) is a candidate filter on this arm only; the candidate limit is 50 by default, independent of the caller's limit.

**Lexical arm.** `ts_rank_cd(tsv, websearch_to_tsquery('simple', $q))`, max-pooled per subject the same way. `websearch_to_tsquery` lets a user quote a phrase or negate a term. The configuration is `simple`, never `english`: `english` stems and drops stopwords, so `child_of` becomes `'child'` and matches the prose "the child task of a parent". Under `simple` it tokenises to `'child'`, `'of'` and matches only the identifier. The lexical arm exists to be exact; recall from stemming is the dense arm's job. A header match (`A`) outranks a body match (`B`).

**Fusion.** Reciprocal rank fusion:

```
score(s) = sum over arms of w_arm / (k + rank_arm(s))      k = 60, w = 1.0 both arms
```

Implemented as a `FULL OUTER JOIN` on the subject key with missing ranks coalesced to zero, ordered by summed score and cut to the caller's `LIMIT`. Fusion uses ranks because cosine similarity and `ts_rank_cd` live on incomparable scales and per-query min-max normalisation is unstable. On the reference fixture, query `child_of` ranks the defining section third on the dense arm and first after fusion, with no query classifier or routing heuristic.

**Filters.** Kind and project filters apply inside both arms; `project IS NULL` keeps org-wide skills visible inside a project-scoped search. pgvector applies `WHERE` after walking the HNSW index, so a selective filter can return fewer than 50 dense candidates; the fix if it appears is `hnsw.iterative_scan`. Soft-deleted docs, tasks and skills keep their chunks until convergence removes them, so the query filters on the subject's own `deleted_at`.

## 16. Freshness and invalidation

The indexer (`internal/indexer`) is a background convergence loop on `lode-server`, interval `LODE_INDEX_INTERVAL` (default 5 minutes). It hooks no write site and subscribes to no event. For each kind it selects subjects whose live `content_hash` differs from their chunk rows' hash, including subjects with no rows, re-embeds each and replaces its chunk set in one transaction. Deleted subjects' chunks are already gone by FK cascade. The hash has exactly one definition, in SQL, owned by the store per kind; no other package computes it. Convergence is self-healing and idempotent: a second pass over an unchanged corpus re-embeds nothing. A task is searchable on the next pass. The loop still runs with no provider configured and writes rows without vectors.

At startup, before the indexer embeds anything, the server compares the configured provider's `ID()` against `embedding_config.provider_id` for the active vector column. On a mismatch it nulls that column and records the new id; the other column is untouched. Invalidation nulls `embedding` without touching `content_hash`, so the staleness query also treats a chunk with no vector as stale when a provider is configured, and as finished work when none is. During a re-embed the instance degrades to lexical-only. Vectors from two models are never mixed in one column.

## 17. Surfaces, permission, metrics, degraded operation

| Surface | Contract |
|---|---|
| `GET /api/v1/search?q=&kind=&project=&limit=&mode=` | ranked `model.SearchHit` values: `Kind`, subject id, `Anchor`, `Title`, excerpt from `chunk_text`, fused score, and the per-arm ranks. `kind` is repeatable; omitted means all three. `mode` is `hybrid` (default), `dense` or `lexical` |
| `lode search <query> [--kind doc|task|skill] [--mode] [--limit] [--json]` | renders `WL-SPEC-25 §15.2  0.032  The ordered log` |
| `POST /api/v1/skills/recommend` | a thin caller of the same path with `kind=skill`; gains the lexical arm |

The route carries `permSearchRead` (`search.read`), granted to `{RoleUser, RoleAdmin}`, the same grant as `permDocRead`, `permTaskRead` and `permSkillRead`. One permission over three kinds is only honest while all three reads are granted identically; project-scoped roles must revisit it. Permissions are defined in 02-identity-actors-and-secrets.md. The cockpit search box is described in 10-cockpit.md.

| Metric | Type | Labels |
|---|---|---|
| `worklode_index_chunks` | gauge | `subject_kind` |
| `worklode_index_chunks_without_vector` | gauge | |
| `worklode_index_subjects_stale` | gauge | `subject_kind` |
| `worklode_index_reembed_total` | counter | `subject_kind`, `outcome` |
| `worklode_index_convergence_duration_seconds` | histogram | |
| `worklode_search_requests_total` | counter | `mode`, `outcome` |
| `worklode_search_duration_seconds` | histogram | `mode` |
| `worklode_search_arm_empty_total` | counter | `arm` |

`outcome` is `ok|error|empty`. Duration is labelled by `mode` because both arms run in one statement. Two alerts matter: `worklode_index_subjects_stale` must return to zero every pass, and `worklode_search_arm_empty_total{arm="lexical"}` rising while the dense arm is busy signals a broken `tsv`.

An instance with no embedding provider still has working search: convergence chunks and writes `tsv`, the lexical arm runs alone, and the response reports `provider: "none"` and `mode: "lexical"`. A failing provider degrades the same way and retries next pass. A broken `tsv` degrades to dense-only. Neither arm's failure takes search down.

## 18. bf16 embedding trial

The fp32 TEI provider is the slowest thing on any request path that calls it synchronously, and TEI has no bf16 dtype. The trial serves EmbeddingGemma in bf16 on `avx512_bf16` hardware from a second stack and keeps both vector sets on disk so trying and reverting never recompute an existing vector.

- Serving: `deploy/embeddings-bf16/` (`Dockerfile`, `app.py`, `pyproject.toml`, `uv.lock`). Weights are staged behind an `hf_token` BuildKit secret mount, never an ARG or ENV. `app.py` is a FastAPI wrapper over `sentence-transformers` with `torch_dtype=torch.bfloat16`, exposing `/v1/embeddings` in the shape `internal/embed.OpenAI` speaks. Deployment and Service `worklode-embeddings-bf16` in `deploy/base/embeddings-bf16.yaml`, with CPU-quota-aware thread counts and `/info` probes. A `workflow_dispatch` twin of `build-embeddings-image.yml` builds the image.
- Dependabot: the `docker` entry uses `directories: ["/", "/deploy", "/deploy/embeddings-bf16", "/.worklode"]` (`directory: "/"` is not recursive), plus a `uv` entry for `/deploy/embeddings-bf16`; every dependency carries a version constraint.
- Schema: `index_chunks.embedding_bf16 vector(768)` with its own HNSW index, shipped everywhere as a no-op where unused. `embedding_config` is keyed `vector_column text PRIMARY KEY CHECK (vector_column IN ('embedding', 'embedding_bf16'))`.
- Config: `LODE_EMBEDDING_VECTOR_COLUMN` is `fp32` (default) or `bf16`, validated at boot; it maps through a Go enum to the column name, so no raw environment string reaches SQL. Every store query that names the vector column (chunk insert, staleness counts, invalidation, `search.go` similarity) reads the configured column. Nothing cross-checks the column against `LODE_EMBEDDING_URL`.
- Cutover: in the hzdev overlay, point `LODE_EMBEDDING_URL` at `worklode-embeddings-bf16`, set `bf16`, restart; the next convergence pass backfills the column. Rollback flips both values back; the fp32 provider id still matches, nothing is cleared, and search is on fp32 vectors immediately.
- Decision: speed only, from `worklode_embed_request_duration_seconds` and the brief endpoint's `http_request_duration_seconds`. A human flips the values. hzprod, simultaneous comparison, retrieval-quality tooling, automatic cutover, dropping the losing column, and an N-column system are out of scope.
- Tests: store coverage that the column selector routes every query, that invalidation never clears the other column, a `NewServer` boot case for an invalid value, and one Python smoke test asserting a 768-wide vector.

## Sources

WL-SPEC-6 (knowledge graph: vocabulary, entity model, runtime layer, projection), WL-SPEC-40 (corpus indexing and hybrid search), WL-SPEC-68 (instance-scoped IRIs and content negotiation), WL-SPEC-69 (reversible bf16 embedding trial).

## Open questions

- `wl:RuntimeEvent` has no natural key, so no deterministic IRI and no projection.
- Whether `wl:Build` should stay declared with no instances until workflow-run ingest exists.
- Whether the Environment closure to `{dev, prod}` survives Cluster and Namespace beneath it.
- How the per-environment frontier (`env_deploys`, keyed `(repo, environment)`) is modelled when a query needs it.
- The artifact ingest gap: only `git_tag` rows exist, so `prov:used` stays empty until image-publish ingest lands.
- WL-SPEC-68 review notes, unresolved in the text: where the scheme comes from behind a TLS-terminating proxy and whether `Host` is validated against `LODE_PUBLIC_URL` or an alias list; the Claim IRI and the `declared/<slug>/v<n>` and `observed/<source>/<host>/<owner>/<name>` named graphs missing from the grammar table; `routeGuards` entries and `{id...}` wildcards for the fourteen new route families and `/shapes.ttl`; whether the numeric `/docs/<row id>` URL is a 302 alias or retired; the unmappable `Number == 0` documents.
- Search: arm weights (`w = 1.0` untuned), a `pg_trgm` third arm for misspellings, chunk-level access control once project-scoped roles exist, and query rewriting for the agent path.
- Whether the graph should carry a Commit-side or Deployment-side frontier for Effects.
