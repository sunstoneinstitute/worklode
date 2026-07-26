# Spec 015 — Runtime layer: artifacts, builds, deployments & environments

**Status:** draft · **Umbrella:** `000-umbrella-architecture.md` · **Depends on:** 006 (knowledge
graph — amends it), 007 (drift & overview — supplies the vocabulary its deploy deriver emits),
014 (prefix rename) · **Amends:** 006, 007.

## Purpose & scope

Spec 006 lists Artifact, Deployment and Environment as **v1 projected nodes** in its Layer 3 table
and gives them instance IRIs — but never assigns them a class. They exist in the IRI grammar and as
`dct:relation` targets of a `wl:Deliverable`, and nowhere else: the mint list in 006's acceptance
criterion 2 contains no runtime term at all. A Deliverable therefore declares its definition-of-done
by pointing at untyped IRIs, and 007's `observed/deploy` deriver has no vocabulary to emit.

Two things have also moved since 006's source decisions (2026-07-21):

- **Spec 011 — delivery lifecycle** (`011-delivery-lifecycle.md`) made the runtime
  layer load-bearing for task state — `deployed_dev`, `deployed_prod`, `released` — and added
  `main_commits`, `env_deploys` and `release_frontiers`. Commit, which 006 defers to v2, is now on
  the critical path of the state machine.
- **Spec 014** renamed `ls:` → `wl:`. This spec is written in `wl:` throughout and lands with 014 or
  after it, never before.

This spec covers, and only covers:

- The **reuse survey** — what was evaluated for artifacts/builds/deployments and why each mint is
  honest rather than lazy.
- The **classes**: `wl:Artifact`, `wl:Build`, `wl:Deployment`, `wl:Environment`, `wl:Commit`,
  `wl:RuntimeEvent`, all anchored on PROV-O.
- The **SKOS schemes** for the four runtime enums.
- The **natural-key IRI grammar**, replacing 006's docker-only Artifact pattern.
- The **projection table** — which relational table feeds each node, and which nodes have no source
  yet.

Out of scope (reference, do not duplicate): the delivery resolver and its frontier arithmetic
(Spec 011 — delivery lifecycle — this spec models the *facts* it reads, not the state machine); drift
queries and the deriver contract (007); Deliverable auto-confirmation (v2, 006 §Deliverable); the
`wl:` rename itself (014).

---

## 1. Reuse survey

Binding convention from the umbrella: standards-first, **mint sparingly**. Every candidate below was
checked against its published specification, not from memory. The result is unusual — the runtime
layer is a genuine gap in the standards landscape, and forcing a reuse here would be worse than a
clean mint.

| Vocabulary | Offers | Verdict |
|---|---|---|
| **PROV-O** | `prov:Entity`, `prov:Activity`, `prov:used`, `prov:wasGeneratedBy`, `prov:wasDerivedFrom`, timing properties | **REUSE as the anchor.** Exactly the right shape for "an activity produced an artifact from a source". Carries no version, digest or registry-coordinate term — those are ours. |
| **SPDX 3.0.1** | A real, fetchable OWL model with `Build/Build` and `Software/Package` | **Reference only.** The Build profile is one class bolted onto Core's `Element`/`Relationship` scaffolding: adopting it imports ~46 classes to get one class of payload, with no supported way to cherry-pick a profile. The RDF is generated from a Markdown-authored spec, tooling consumes the JSON-LD, and there is **no PROV-O alignment** to inherit. Cite it with `rdfs:seeAlso`; do not import it. |
| **DOAP** | `doap:Version`, `doap:release`, `doap:revision` | **Rejected for artifacts.** `doap:Version` is "version information of a project release" — a changelog entry hanging off `doap:Project`, with no digest, no registry coordinate and no artifact-kind discrimination. Effectively dormant since 2022. 03's existing use of `doap:Project` for **repository** grouping is unaffected and stays. |
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

## 2. Classes

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
                 "source exists (see §6); declared now so wl:Artifact can carry "
                 "prov:wasGeneratedBy without a later breaking change." .

wl:Deployment a owl:Class ; rdfs:subClassOf prov:Activity ;
    rdfs:comment "The rollout of an Artifact to one target in one Environment — a Flux "
                 "Kustomization, a PyPI publish, or a manually tracked target. Mirrors current "
                 "state, one node per (environment, target kind, target name); rollout history "
                 "is v2." .

wl:Environment a owl:Class ;
    rdfs:comment "A deployment stage. Deliberately parentless: no surveyed vocabulary models a "
                 "deployment stage, and prov:Location is spatial in intent. The instance set is "
                 "closed to dev and prod by SHACL, matching the normalisation the delivery "
                 "handlers already enforce." .

wl:Commit a owl:Class ; rdfs:subClassOf prov:Entity ;
    rdfs:comment "One commit on a repository's default branch. Promoted from v2 (006) because "
                 "delivery resolution is defined in terms of commit coverage." .

wl:RuntimeEvent a owl:Class ; rdfs:subClassOf prov:Activity ;
    rdfs:comment "An observed runtime incident affecting a deployed Artifact — a crashloop, an "
                 "OOM kill, a Flux reconciliation failure or recovery." .

# Every runtime term is tagged, per 006's convention:
wl:Artifact wl:layer wlc:runtime .    wl:Build        wl:layer wlc:runtime .
wl:Deployment wl:layer wlc:runtime .  wl:Environment  wl:layer wlc:runtime .
wl:Commit wl:layer wlc:runtime .      wl:RuntimeEvent wl:layer wlc:runtime .

[] a owl:AllDisjointClasses ;
   owl:members ( wl:Artifact wl:Build wl:Deployment wl:Environment wl:Commit wl:RuntimeEvent ) .
```

**Why `wl:Artifact` has no subclasses.** Spec 006 mints real subclasses for `wl:DesignDoc` because
ADR and Spec play different structural roles, and uses a SKOS scheme for `wl:taskKind` because kind
is a mere attribute there. Artifact kind is the latter case: a `docker_image` and a `pypi` package
carry identical edges and differ only in how their coordinates are spelled. One class plus
`wl:artifactKind` — consistent with `wl:taskKind`, and with the umbrella's fixed-enum convention.

**Why a git tag is not a `wl:Release`.** The release handler already records a published release as
an `artifacts` row of kind `git_tag`; a separate class would duplicate it. A release is
`wl:Artifact` with `wl:artifactKind wlc:git_tag` plus a `wl:covers` edge to the commit its frontier
reaches. `release_frontiers` projects as that edge, not as a node.

---

## 3. SKOS schemes

Four fixed enums, each mirroring a `CHECK` constraint that already exists in the schema. Per the
umbrella convention these are controlled vocabularies, never free text.

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
    skos:definition "Reconciliation succeeded; the artifact is live on this target." .
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

## 4. Properties

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

wl:covers a owl:ObjectProperty ;
    rdfs:domain wl:Artifact ; rdfs:range wl:Commit ;
    rdfs:comment "This artifact carries every commit up to and including that commit — the "
                 "delivery frontier of a published release. Domain is Artifact, not Deployment: "
                 "see the grain note in §6 for why the per-environment frontier has no node." .
```

**Reused, with the column each projects from:**

| Fact | Term | Notes |
|---|---|---|
| artifact built by | `prov:wasGeneratedBy` → `wl:Build` | no v1 source; see §6 |
| artifact built from source | `prov:wasDerivedFrom` → `wl:Commit` | `artifacts.source_sha` |
| build time | `prov:generatedAtTime` | `artifacts.built_at`; domain `prov:Entity`, so it hangs on the Artifact — correct, since v1 has no Build node |
| deployment uses artifact | `prov:used` | `deployments.artifact_id` |
| runtime event affects artifact | `prov:used` | `runtime_events.artifact_id` |
| deployment first seen | `prov:startedAtTime` | `deployments.first_seen` |
| deployment last updated | `dct:modified` | `deployments.last_update` |
| event / publication time | `dct:date` | `runtime_events.occurred_at`, `release_frontiers.published_at` |
| version string | `owl:versionInfo` | `artifacts.version` |
| name, target name, commit sha | `dct:identifier` | the coordinate, also encoded in the IRI |
| repository grouping | `doap:Project` + `dct:hasPart` | as 006 already establishes |
| human label | `dct:title` | |

**A modelling tension, stated plainly.** `deployments` is a mutable current-state row (unique per
environment/target), not an immutable event, yet `wl:Deployment` subclasses `prov:Activity`. This is
deliberate: a rollout *is* an activity, `prov:startedAtTime` genuinely means "first seen", and the
alternative — a state node with no PROV anchor — buys nothing. The consequence is that the graph
shows current state only; per-rollout history needs a second node type and is v2.

---

## 5. IRI grammar

**Principle: an instance IRI mirrors the relational natural key.** Projection is then a pure
function of the row, which is what makes 007's deriver contract (deterministic, idempotent,
full-replace) satisfiable without a side table mapping rows to IRIs.

Spec 006's Artifact pattern — `id/artifact/<registry>/<repo>/<tag-or-digest>` — is container-shaped
and cannot spell a PyPI package or a binary release, though the `artifacts` table has allowed all
four kinds since the baseline migration. It is replaced by a kind-first grammar matching
`UNIQUE (kind, name, version)` exactly. Nothing is published yet, so the change is free now and
breaking once the rdf-registry PR lands — the same argument spec 014 makes for the prefix rename.

| Type | Pattern | Natural key | Example |
|---|---|---|---|
| Artifact | `id/artifact/<kind>/<name>/<version>` | `(kind, name, version)` | `…/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1` |
| — PyPI | | | `…/id/artifact/pypi/sunstone-py/0.4.1` |
| — git tag | | | `…/id/artifact/git_tag/github.com/sunstoneinstitute/worklode/v0.4` |
| Deployment | `id/deployment/<environment>/<target-kind>/<target-name>` | `(environment, target_kind, target_name)` | `…/id/deployment/prod/flux_kustomization/graph-server` |
| Environment | `id/environment/<name>` | `name` | `…/id/environment/prod` |
| Commit | `id/commit/<host>/<org>/<repo>/<sha>` | `(repo, sha)` | `…/id/commit/github.com/sunstoneinstitute/worklode/a16c2a7` |
| Build | `id/build/<host>/<org>/<repo>/<run-id>` | — | reserved; no v1 source |
| RuntimeEvent | — | **none** | see Open question 1 |

Slashes inside a local id remain permissible (slash namespace, opaque path), as 006 establishes.

---

## 6. Projection

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
| `wl:covers` (release → commit) | `release_frontiers` | v1 | `release.published` |
| environment frontier → commit | `env_deploys` | **v2** | grain mismatch; see below |
| `wl:RuntimeEvent` | `runtime_events` | **v2** | blocked on Open question 1 |
| `wl:Build` | — | **v2** | no source; needs workflow-run ingest |
| Deliverable auto-confirmation | derived | **v2** | 006 §Deliverable, 007 |

**What the ingest actually produces today.** `store.CreateArtifact` has exactly one call site,
`applyRelease` in `internal/hooks/github.go`, which writes `kind = 'git_tag'`. Nothing creates
`docker_image`, `pypi` or `binary` rows, though the baseline `CHECK` constraint permits all four.
Two consequences the vocabulary cannot paper over:

- **In v1 the graph will contain `git_tag` artifacts and nothing else.** The kind-first IRI grammar
  (§5) is still specified for all four, because the constraint allows them and an image-publish
  hook is the obvious next ingest — but the other three kinds are grammar, not data.
- **`prov:used` from a Deployment to its Artifact will be absent.** `deployments.artifact_id` is
  resolved by `FindArtifactByImage`, which matches only `kind = 'docker_image'` — a kind with no
  producer. The column is therefore null in practice, so the edge is specified and unpopulated
  until image ingest exists. This is an ingest gap, not a modelling one, and is the single highest-value
  thing to fix if the runtime layer is meant to carry weight (Open question 5).

Two classes are likewise declared but not projected, and the spec says so rather than implying
coverage. `wl:Build` exists so `prov:wasGeneratedBy` is available the day workflow-run ingest lands;
until then `prov:generatedAtTime` on the Artifact carries `built_at` and no Build node is minted.
`wl:RuntimeEvent` is declared but unprojectable until it has a natural key.

**The environment frontier has no node to hang on.** `release_frontiers` is keyed `(repo, tag)`,
which is exactly a `git_tag` artifact — so `wl:covers` projects cleanly from it. `env_deploys` is
keyed `(repo, environment)`, a grain that matches nothing: `wl:Deployment` is keyed
`(environment, target_kind, target_name)` and `wl:Environment` is global. Representing it needs
either a new per-repo-per-environment node or a qualified relation, and neither is worth minting
before a query wants it. Deferred to v2; the delivery resolver reads `env_deploys` relationally in
the meantime and loses nothing.

**Guarding the `wl:Commit` edge.** `applyRelease` populates `source_sha` from the release's
`target_commitish`, which is frequently a *branch name* rather than a sha (UI-created tags —
Spec 011 — delivery lifecycle — already handles this by falling back to main's head). The projector must
therefore emit `prov:wasDerivedFrom` only when `source_sha` resolves to a known `main_commits` row,
and drop it otherwise. Minting `wlid:commit/…/main` from a branch name would produce a plausible,
permanently wrong node. An artifact whose `repo` is set but whose `source_sha` is null or
unresolvable projects no commit edge at all: a repository alone does not identify a commit.

---

## 7. Amendments to existing specs

| Spec | Change |
|---|---|
| 03 | Add the six runtime classes, four SKOS schemes and seven properties to the mint list in acceptance criterion 2; replace the Artifact IRI pattern with the kind-first grammar (§5) and add Deployment/Commit/Build patterns; move Commit from v2 to v1 in the Layer 3 table; **drop `WorkflowRun` from the v2 Layer 2 list** — `wl:Build` subsumes it; extend the `owl:AllDisjointClasses` axiom set; note that a Deliverable's `dct:relation` targets are now typed |
| 04 | The `observed/deploy` deriver emits the vocabulary in §2–§4 and the IRIs in §5; its output is the projection table in §6 |
| Migration | None. This spec adds no column and changes no constraint; every enum mirrors a `CHECK` that already exists |
| rdf-registry | `rdf/wl/ontology.ttl` and `rdf/wl/concept.ttl` gain these terms; `rdf/shapes/wl-shapes.ttl` gains the node shapes below |

**New SHACL node shapes** (`wl-shapes.ttl`, per ADR-0003):

- **Artifact** — exactly one `wl:artifactKind` from `wlc:ArtifactKind`; exactly one
  `owl:versionInfo`; exactly one `dct:identifier`.
- **Deployment** — exactly one each of `wl:toEnvironment`, `wl:targetKind`, `wl:deploymentStatus`;
  at most one `prov:used`.
- **Environment** — `sh:in (wlid:environment/dev wlid:environment/prod)`, closing the set to what
  the delivery handlers normalise to.
- **Commit** — exactly one `dct:identifier` (the sha).

---

## Dependencies

- **Spec 006** — the vocabulary this extends; the `wl:layer` convention, the SHACL gate and the
  `owlrl` closure test all widen to the new terms.
- **Spec 007** — the deriver contract and the `observed/deploy` named graph these nodes land in.
- **Spec 014** — the `wl:` rename. This spec is written in `wl:` and must not land before 014.
- **Spec 011 — delivery lifecycle** (`011-delivery-lifecycle.md`) — owns the state
  machine and frontier arithmetic; this spec models the facts it reads.
- **`internal/hooks/`** (`flux.go`, `github.go`, `deployment.go`) and `internal/store/artifacts.go`
  — the ingest whose rows project into these nodes.
- **rdf-registry** — ADR-0006 (IRI scheme), ADR-0003 (SHACL gate), ADR-0004 (`owlrl` closure test).

## Open questions

1. **`wl:RuntimeEvent` has no natural key.** `runtime_events` has only a surrogate id, so no
   deterministic IRI can be derived from a row and 007's idempotent full-replace contract cannot be
   met. Options: add a natural key to the table (`(cluster, kind, workload, occurred_at)` is
   plausibly unique), hash the tuple into the local id, or leave the class declared and unprojected.
   Projection stays v2 until this is settled.
2. **Should `wl:Build` be minted before its ingest exists?** It is declared here so
   `prov:wasGeneratedBy` never becomes a breaking addition, at the cost of a class with no
   instances. The alternative is deferring both to the workflow-run ingest and accepting the churn.
3. **Environment as a closed instance set.** SHACL `sh:in` over `{dev, prod}` matches today's
   normalisation, but v2 wants Cluster and Namespace beneath an Environment, and a per-cluster
   preview environment would force the shape open. Confirm the closure is worth the constraint.
4. **How should the per-environment frontier be modelled when v2 needs it?** `env_deploys`' grain
   `(repo, environment)` matches no node in this vocabulary (§6). A per-repo-per-environment node
   and a qualified relation are both defensible; the choice should be driven by the first query
   that actually needs "what is live in dev for repo X", not settled speculatively now.
5. **The artifact ingest gap is the real blocker.** Only `git_tag` artifacts are ever created, and
   `deployments.artifact_id` resolves through a `docker_image` lookup that can never match (§6). No
   vocabulary decision fixes this. Whether image-publish ingest lands before or after the `wl:` PR
   determines whether the runtime layer ships with a populated `prov:used` edge or an empty one —
   worth deciding deliberately rather than discovering at projection time.

## Acceptance criteria

1. `rdf/wl/ontology.ttl` and `rdf/wl/concept.ttl` declare all six classes, seven properties and
   four SKOS schemes; every class and property carries `wl:layer wlc:runtime`, so
   `SELECT ?c WHERE { ?c wl:layer wlc:runtime }` returns exactly the runtime terms.
2. No term is imported from SPDX, DOAP, DCAT, schema.org, OSLC or `sd:`; the only external terms
   used are `prov:`, `dcterms:`, `doap:Project`, `skos:` and `owl:versionInfo`. `rdfs:seeAlso`
   citations to SPDX carry no import.
3. Each of the four artifact kinds mints a distinct, deterministic IRI under the §5 grammar, and
   re-projecting an unchanged `artifacts` row is a byte-identical no-op.
4. On a **seeded** graph, a `wl:Deployment` resolves to its `wl:Artifact` via `prov:used` and to
   `wlid:environment/prod` via `wl:toEnvironment`, and a SPARQL read returns the artifact currently
   live in prod for a given target. Seeded, not projected: `deployments.artifact_id` is null in
   practice today (§6), so this criterion tests the vocabulary, and closing the ingest gap is
   tracked separately as Open question 5.
5. A `wl:Deliverable` whose `dct:relation` names an artifact IRI and `wlid:environment/prod`
   resolves both to typed nodes (006's Deliverable example round-trips against this vocabulary).
6. The SHACL gate rejects: an Artifact with two `wl:artifactKind` values or none; a Deployment
   missing `wl:toEnvironment`; an Environment outside `{dev, prod}`.
7. An `owlrl` pass over the TBox plus a seeded ABox flags a node typed both `wl:Artifact` and
   `wl:Deployment` as inconsistent, and infers `prov:Entity` for every Artifact and
   `prov:Activity` for every Deployment — confirming PROV-aware tooling traverses the layer.
8. A release tag artifact `wl:covers` the commit its `release_frontiers` row names, and that commit
   resolves to a `wl:Commit` projected from `main_commits`. A release whose `target_commitish` is a
   branch name projects **no** `prov:wasDerivedFrom` edge rather than a fabricated commit node.
9. `wl:Build` and `wl:RuntimeEvent` are declared with no instances in v1, and no acceptance
   criterion anywhere claims they are projected.
