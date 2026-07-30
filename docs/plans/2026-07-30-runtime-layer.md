# Runtime layer (spec 015) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the runtime layer its vocabulary — six PROV-anchored classes,
four SKOS schemes, seven properties, SHACL shapes — plus the deterministic
row→IRI/triple projection functions that 007's `observed/deploy` deriver will
emit, satisfying spec 015's nine acceptance criteria.

**Architecture:** The vocabulary lands as Turtle in the **rdf-registry** repo
(`rdf/wl/ontology.ttl`, `rdf/wl/concept.ttl`, `rdf/shapes/wl-shapes.ttl`),
validated by that repo's existing pytest harness (rdflib + pyshacl + owlrl).
The projection side lands in **worklode** as a new pure package
`internal/graphproj`: natural-key IRI constructors and row→N-Triples
functions over `internal/store` types, no I/O, so re-projecting an unchanged
row is provably a byte-identical no-op. Nothing talks to a graph server yet —
that is 007's deriver, out of scope here.

**Tech Stack:** Turtle/SHACL/SKOS/PROV-O; Python 3.11+ with `uv`, pytest,
rdflib, pyshacl, owlrl (rdf-registry harness); Go with standard-library
testing (worklode).

**Spec:** `docs/specs/015-runtime-layer.md`

---

## Two repositories

Tasks 1–4 run in **`/Users/stig/git/sunstone/rdf-registry`** (spec 015 §7
places the TTL there); Tasks 5–7 run in **`/Users/stig/git/sunstone/worklode`**.
Every command below states its working directory. Each repo gets its own
branch and its own commits. rdf-registry uses conventional-commit messages
(`feat(wl): …`); worklode uses plain imperative sentences.

## Already implemented vs. what remains

**Already in place — do not rebuild:**

- Relational schema and `CHECK` constraints the SKOS schemes mirror:
  `artifacts`/`deployments`/`runtime_events` in
  `deploy/base/migrations/0001_baseline.up.sql:137-168`;
  `main_commits`/`env_deploys`/`release_frontiers` in
  `deploy/base/migrations/0005_delivery.up.sql:29-66`. Spec 015 §7: **no
  migration** — every enum mirrors an existing `CHECK`.
- Ingest: `applyRelease` (`internal/hooks/github.go:400`) creates `git_tag`
  artifacts and release frontiers; `internal/hooks/flux.go`,
  `internal/hooks/deployment.go`, `internal/hooks/push.go` feed
  deployments/env_deploys/main_commits; store code in
  `internal/store/artifacts.go`, `internal/store/delivery.go`,
  `internal/store/runtime.go`.
- Spec-document amendments (015 §7): already applied as callout blocks —
  006 mint list (`docs/specs/006-knowledge-graph.md:67`), disjointness
  (`:109`), Layer 2 WorkflowRun drop + Commit promotion (`:400`), Layer 3
  supersession (`:417`), Deliverable typing (`:434`), Artifact IRI grammar
  (`:503`); 007 deriver output (`docs/specs/007-drift-and-overview.md:131`).
  No doc edits in this plan.

**What remains (this plan):** the TTL vocabulary, shapes and their tests
(nothing exists — rdf-registry has no `rdf/wl/` directory at all), and the
projection functions with the §6 guards (worklode has no graph code at all).

**Explicitly out of scope, per the spec:** 007's deriver/graph-server wiring;
image-publish ingest closing the `deployments.artifact_id` gap (Open Q5);
`wl:RuntimeEvent` projection (Open Q1 — declared, unprojected);
`wl:Build` instances (no source); the `env_deploys` frontier node (v2);
publishing `worklode.io/ns/` IRIs from rdf-registry's build — that is the
separate unlanded plan `docs/superpowers/plans/2026-07-22-worklode-ns-base-override.md`
on rdf-registry branch `worklode-io-spec`. The tests here validate the TTL
sources directly and do not depend on the publish pipeline; the build's
`discover_resources()` silently ignores `worklode.io` IRIs today, so adding
these files does not disturb the existing site build or invariants.

**One deliberate scaffolding decision:** spec 006 (the full `wl:` ontology)
is unimplemented. 015's acceptance criteria still need three 006-owned terms
to exist — `wl:layer` (AC1 tags), `wlc:ModelLayer`/`wlc:runtime` (AC1), and
`wl:Deliverable` (AC5 round-trip). `rdf/wl/ontology.ttl` therefore carries a
clearly-marked minimal scaffolding section for exactly those terms; 006's
implementation later extends the same files. Scaffolding terms are **not**
tagged `wl:layer wlc:runtime`, so AC1's query still returns exactly the 13
runtime terms.

## File Structure

**New files — rdf-registry (`/Users/stig/git/sunstone/rdf-registry`)**

| Path | Responsibility |
|---|---|
| `rdf/wl/ontology.ttl` | six runtime classes, seven properties, disjointness, layer tags; minimal 006 scaffolding |
| `rdf/wl/concept.ttl` | four runtime SKOS schemes; `wlc:ModelLayer` + `wlc:runtime` scaffolding |
| `rdf/shapes/wl-shapes.ttl` | Artifact / Deployment / Environment / Commit node shapes (ADR-0003) |
| `tests/test_wl_runtime.py` | AC1, AC2, AC4–AC9 graph-side tests |
| `tests/fixtures/wl/runtime-abox.ttl` | seeded ABox: 4 artifact kinds, deployment, environments, commit, deliverable |
| `tests/fixtures/wl/inconsistent-abox.ttl` | node typed both Artifact and Deployment (AC7) |
| `tests/fixtures/wl/shacl/artifact-two-kinds.ttl` | AC6 rejection fixture |
| `tests/fixtures/wl/shacl/artifact-no-kind.ttl` | AC6 rejection fixture |
| `tests/fixtures/wl/shacl/deployment-no-environment.ttl` | AC6 rejection fixture |
| `tests/fixtures/wl/shacl/environment-staging.ttl` | AC6 rejection fixture |

**Modified files — rdf-registry**

| Path | Change |
|---|---|
| `scripts/validate_shacl.py` | the gate runs `wl-shapes.ttl` alongside `gtio-shapes.ttl` |

**New files — worklode (`/Users/stig/git/sunstone/worklode`)**

| Path | Responsibility |
|---|---|
| `internal/graphproj/iri.go` | §5 natural-key IRI grammar + segment escaping; package doc |
| `internal/graphproj/iri_test.go` | the §5 example IRIs verbatim; distinctness; escaping |
| `internal/graphproj/triple.go` | `Triple` + deterministic sorted N-Triples `Render` |
| `internal/graphproj/triple_test.go` | ordering, dedupe, literal escaping, datatypes |
| `internal/graphproj/runtime.go` | row→triples for Artifact/Deployment/Environment/Commit/covers with §6 guards |
| `internal/graphproj/runtime_test.go` | AC3 byte-identical no-op; AC8 branch-name guard; `pypi`→`pypi_target` |

**Modified files — worklode:** none.

**Test commands**

- rdf-registry: `cd /Users/stig/git/sunstone/rdf-registry && uv run pytest tests/test_wl_runtime.py -q`
- rdf-registry full suite: `uv run pytest -q` (Jena-CLI tests skip if `shacl` is not on PATH — that is pre-existing behavior)
- worklode: `go test ./internal/graphproj/...` (pure package — no Postgres needed)
- worklode full suite: `go test ./...` (store/api/cmd need Postgres via `store.OpenTestStore`)

---

## Phase 1 — Vocabulary tracer bullet (rdf-registry)

### Task 1: rdf-registry branch + baseline

**Working directory:** `/Users/stig/git/sunstone/rdf-registry`

- [ ] **Step 1: Confirm the repo is clean and on main**

Run: `git -C /Users/stig/git/sunstone/rdf-registry status --short --branch`
Expected: `## main...origin/main`, no modified files. If there is local
state, stop and report — do not stash someone else's work.

- [ ] **Step 2: Create the branch**

```bash
git -C /Users/stig/git/sunstone/rdf-registry switch -c wl-runtime-layer main
```

- [ ] **Step 3: Run the baseline suite**

Run: `cd /Users/stig/git/sunstone/rdf-registry && uv run pytest -q`
Expected: PASS (some tests may SKIP if the Jena `shacl` CLI is absent).
Record the pass/skip counts so later failures are attributable to this work.

### Task 2: Runtime vocabulary TTL (AC1, AC2)

**Working directory:** `/Users/stig/git/sunstone/rdf-registry`

**Files:**
- Create: `rdf/wl/ontology.ttl`
- Create: `rdf/wl/concept.ttl`
- Test: `tests/test_wl_runtime.py`

Namespaces (spec 014 §1, already reflected in 006): `wl:` =
`https://worklode.io/ns/wl/ontology#`, `wlc:` =
`https://worklode.io/ns/wl/concept/`, instances under
`https://worklode.io/ns/wl/id/`. Note: instance IRIs contain `/` in the
local part, which Turtle prefixed names cannot express — fixtures and shapes
write instances as full `<…>` IRIs throughout.

- [ ] **Step 1: Write the failing test**

Create `tests/test_wl_runtime.py`:

```python
"""Runtime-layer vocabulary tests — worklode spec 015.

Covers acceptance criteria 1 (layer query) and 2 (no foreign imports) in
this phase; later phases append SHACL, SPARQL and owlrl tests to this file.
"""
from pathlib import Path

import pytest
from rdflib import Graph, Namespace, URIRef
from rdflib.collection import Collection
from rdflib.namespace import OWL, RDF, RDFS, SKOS

WL = Namespace("https://worklode.io/ns/wl/ontology#")
WLC = Namespace("https://worklode.io/ns/wl/concept/")
WLID = "https://worklode.io/ns/wl/id/"

WL_DIR = Path(__file__).parent.parent / "rdf" / "wl"
ONTOLOGY = WL_DIR / "ontology.ttl"
CONCEPTS = WL_DIR / "concept.ttl"

RUNTIME_CLASSES = {
    WL.Artifact, WL.Build, WL.Deployment,
    WL.Environment, WL.Commit, WL.RuntimeEvent,
}
RUNTIME_PROPERTIES = {
    WL.artifactKind, WL.digest, WL.toEnvironment, WL.deploymentStatus,
    WL.targetKind, WL.runtimeEventKind, WL.covers,
}


@pytest.fixture(scope="module")
def vocab() -> Graph:
    g = Graph()
    g.parse(ONTOLOGY)
    g.parse(CONCEPTS)
    return g


# --- AC1: every runtime term is layer-tagged; nothing else is ---

def test_layer_query_returns_exactly_the_runtime_terms(vocab):
    got = set(vocab.subjects(WL.layer, WLC.runtime))
    assert got == RUNTIME_CLASSES | RUNTIME_PROPERTIES


def test_runtime_classes_are_mutually_disjoint(vocab):
    for axiom in vocab.subjects(RDF.type, OWL.AllDisjointClasses):
        members_head = vocab.value(axiom, OWL.members)
        members = set(Collection(vocab, members_head))
        if WL.Artifact in members:
            assert members == RUNTIME_CLASSES
            return
    pytest.fail("no owl:AllDisjointClasses axiom covering the runtime classes")


def test_prov_anchors(vocab):
    prov = Namespace("http://www.w3.org/ns/prov#")
    assert (WL.Artifact, RDFS.subClassOf, prov.Entity) in vocab
    assert (WL.Commit, RDFS.subClassOf, prov.Entity) in vocab
    assert (WL.Build, RDFS.subClassOf, prov.Activity) in vocab
    assert (WL.Deployment, RDFS.subClassOf, prov.Activity) in vocab
    assert (WL.RuntimeEvent, RDFS.subClassOf, prov.Activity) in vocab
    # wl:Environment is deliberately parentless (015 §2).
    assert vocab.value(WL.Environment, RDFS.subClassOf) is None


# --- SKOS schemes mirror the CHECK constraints exactly ---

SCHEMES = {
    WLC.ArtifactKind: {WLC.docker_image, WLC.pypi, WLC.git_tag, WLC.binary},
    WLC.DeploymentStatus: {WLC.pending, WLC.reconciling, WLC.deployed, WLC.failed},
    WLC.DeployTargetKind: {WLC.flux_kustomization, WLC.pypi_target, WLC.manual},
    WLC.RuntimeEventKind: {WLC.crashloop, WLC.oom, WLC.flux_failure, WLC.flux_recovery},
}


@pytest.mark.parametrize("scheme,concepts", SCHEMES.items(), ids=str)
def test_scheme_closed_to_its_concepts(vocab, scheme, concepts):
    assert (scheme, RDF.type, SKOS.ConceptScheme) in vocab
    got = set(vocab.subjects(SKOS.inScheme, scheme))
    assert got == concepts
    for c in concepts:
        assert (c, RDF.type, SKOS.Concept) in vocab
        assert vocab.value(c, SKOS.prefLabel) is not None


# --- AC2: no foreign vocabulary imported ---

FORBIDDEN = (
    "https://spdx.org/",
    "http://usefulinc.com/ns/doap#",
    "http://www.w3.org/ns/dcat#",
    "http://schema.org/",
    "https://schema.org/",
    "http://open-services.net/",
    "https://w3id.org/okn/o/sd",
)


def test_no_foreign_vocabulary_imported(vocab):
    for s, p, o in vocab:
        assert not str(p).startswith(FORBIDDEN), f"foreign predicate {p}"
        if p == RDFS.seeAlso:
            continue  # citations carry no import (015 AC2)
        if isinstance(o, URIRef):
            assert not str(o).startswith(FORBIDDEN), f"foreign term {o} via {p}"


def test_spdx_cited_only_via_seealso(vocab):
    cites = [
        (s, o) for s, o in vocab.subject_objects(RDFS.seeAlso)
        if str(o).startswith("https://spdx.org/")
    ]
    assert {s for s, _ in cites} == {WL.Artifact, WL.Build}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /Users/stig/git/sunstone/rdf-registry && uv run pytest tests/test_wl_runtime.py -q`
Expected: ERROR in the `vocab` fixture — `rdf/wl/ontology.ttl` does not exist.

- [ ] **Step 3: Write `rdf/wl/ontology.ttl`**

The class/property Turtle is spec 015 §2 and §4 verbatim, with `rdfs:label`
added per rdf-registry template conventions and the layer tag on properties
as AC1 requires:

```turtle
@prefix wl:   <https://worklode.io/ns/wl/ontology#> .
@prefix wlc:  <https://worklode.io/ns/wl/concept/> .
@prefix dct:  <http://purl.org/dc/terms/> .
@prefix owl:  <http://www.w3.org/2002/07/owl#> .
@prefix prov: <http://www.w3.org/ns/prov#> .
@prefix rdfs: <http://www.w3.org/2000/01/rdf-schema#> .
@prefix skos: <http://www.w3.org/2004/02/skos/core#> .
@prefix xsd:  <http://www.w3.org/2001/XMLSchema#> .

<https://worklode.io/ns/wl/ontology> a owl:Ontology ;
    dct:title "Worklode ontology (wl)"@en ;
    rdfs:comment "Currently the runtime-layer slice (worklode spec 015) plus minimal shared scaffolding. Worklode spec 006 owns the full mint set and extends this file."@en .

# ---------------------------------------------------------------------------
# Shared scaffolding — owned by spec 006, declared minimally so the runtime
# layer is testable. 006's implementation replaces this section with the full
# declarations. Deliberately NOT tagged wl:layer wlc:runtime.
# ---------------------------------------------------------------------------

wl:layer a owl:AnnotationProperty ;
    rdfs:label "layer" ;
    rdfs:comment "Model-layer tag on vocabulary terms; see wlc:ModelLayer." .

wl:Deliverable a owl:Class ;
    rdfs:label "Deliverable" ;
    rdfs:comment "Declared definition-of-done (spec 006). Minimal declaration so runtime fixtures resolve a Deliverable's dct:relation targets to typed nodes." .

# ---------------------------------------------------------------------------
# Runtime classes (spec 015 §2)
# ---------------------------------------------------------------------------

wl:Artifact a owl:Class ; rdfs:subClassOf prov:Entity ;
    rdfs:label "Artifact" ;
    rdfs:seeAlso <https://spdx.org/rdf/3.0.1/terms/Software/Package> ;
    rdfs:comment "One built, versioned unit deployed elsewhere: a container image, a PyPI package, a git tag or a binary release. Kind is an attribute (wl:artifactKind), not a subclass — the kinds differ in coordinates, not in the edges they carry." ;
    wl:layer wlc:runtime .

wl:Build a owl:Class ; rdfs:subClassOf prov:Activity ;
    rdfs:label "Build" ;
    rdfs:seeAlso <https://spdx.org/rdf/3.0.1/terms/Build/Build> ;
    rdfs:comment "The activity that produced an Artifact — a CI workflow run. No v1 projection source exists (015 §6); declared now so wl:Artifact can carry prov:wasGeneratedBy without a later breaking change." ;
    wl:layer wlc:runtime .

wl:Deployment a owl:Class ; rdfs:subClassOf prov:Activity ;
    rdfs:label "Deployment" ;
    rdfs:comment "The rollout of an Artifact OR a Commit to one target in one Environment — a Flux Kustomization, a PyPI publish, or a manually tracked target. prov:used binds an Artifact for built units and a Commit for IaC/GitOps repos. Mirrors current state, one node per (environment, target kind, target name); rollout history is v2." ;
    wl:layer wlc:runtime .

wl:Environment a owl:Class ;
    rdfs:label "Environment" ;
    rdfs:comment "A deployment stage. Deliberately parentless: no surveyed vocabulary models a deployment stage, and prov:Location is spatial in intent. The instance set is closed to dev and prod by SHACL, matching the normalisation the delivery handlers enforce." ;
    wl:layer wlc:runtime .

wl:Commit a owl:Class ; rdfs:subClassOf prov:Entity ;
    rdfs:label "Commit" ;
    rdfs:comment "One commit on a repository's default branch. Promoted from v2 (006) because delivery resolution is defined in terms of commit coverage." ;
    wl:layer wlc:runtime .

wl:RuntimeEvent a owl:Class ; rdfs:subClassOf prov:Activity ;
    rdfs:label "RuntimeEvent" ;
    rdfs:comment "An observed runtime incident affecting a deployed Artifact — a crashloop, an OOM kill, a Flux reconciliation failure or recovery. Declared but unprojectable until it has a natural key (015 Open question 1)." ;
    wl:layer wlc:runtime .

[] a owl:AllDisjointClasses ;
   owl:members ( wl:Artifact wl:Build wl:Deployment wl:Environment wl:Commit wl:RuntimeEvent ) .

# ---------------------------------------------------------------------------
# Runtime properties (spec 015 §4)
# ---------------------------------------------------------------------------

wl:artifactKind a owl:ObjectProperty, owl:FunctionalProperty ;
    rdfs:label "artifact kind" ;
    rdfs:domain wl:Artifact ; rdfs:range skos:Concept ;
    rdfs:comment "Kind of artifact; see wlc:ArtifactKind. Functional catches >1, SHACL catches 0." ;
    wl:layer wlc:runtime .

wl:digest a owl:DatatypeProperty ;
    rdfs:label "digest" ;
    rdfs:domain wl:Artifact ; rdfs:range xsd:string ;
    rdfs:comment "Content digest as an opaque prefixed string, e.g. \"sha256:abc…\". Minted rather than reusing spdx:checksum: that is a class requiring an algorithm/value node, and the ingested value is a single opaque string." ;
    wl:layer wlc:runtime .

wl:toEnvironment a owl:ObjectProperty, owl:FunctionalProperty ;
    rdfs:label "to environment" ;
    rdfs:domain wl:Deployment ; rdfs:range wl:Environment ;
    rdfs:comment "The environment this deployment targets." ;
    wl:layer wlc:runtime .

wl:deploymentStatus a owl:ObjectProperty, owl:FunctionalProperty ;
    rdfs:label "deployment status" ;
    rdfs:domain wl:Deployment ; rdfs:range skos:Concept ;
    rdfs:comment "Current rollout status; see wlc:DeploymentStatus." ;
    wl:layer wlc:runtime .

wl:targetKind a owl:ObjectProperty, owl:FunctionalProperty ;
    rdfs:label "target kind" ;
    rdfs:domain wl:Deployment ; rdfs:range skos:Concept ;
    rdfs:comment "Kind of deployment target; see wlc:DeployTargetKind." ;
    wl:layer wlc:runtime .

wl:runtimeEventKind a owl:ObjectProperty, owl:FunctionalProperty ;
    rdfs:label "runtime event kind" ;
    rdfs:domain wl:RuntimeEvent ; rdfs:range skos:Concept ;
    rdfs:comment "Kind of runtime incident; see wlc:RuntimeEventKind." ;
    wl:layer wlc:runtime .

wl:covers a owl:ObjectProperty ;
    rdfs:label "covers" ;
    rdfs:domain wl:Artifact ; rdfs:range wl:Commit ;
    rdfs:comment "This artifact carries every commit up to and including that commit — the delivery frontier of a published release. Domain is Artifact, not Deployment: the per-environment frontier has no node (015 §6)." ;
    wl:layer wlc:runtime .
```

- [ ] **Step 4: Write `rdf/wl/concept.ttl`**

Spec 015 §3 verbatim, plus the `wlc:ModelLayer` scaffolding:

```turtle
@prefix wlc:  <https://worklode.io/ns/wl/concept/> .
@prefix skos: <http://www.w3.org/2004/02/skos/core#> .

# --- Scaffolding owned by spec 006: the model-layer scheme, minimally. ---
wlc:ModelLayer a skos:ConceptScheme ; skos:prefLabel "Model layer" .
wlc:runtime a skos:Concept ; skos:inScheme wlc:ModelLayer ; skos:prefLabel "runtime" .

# --- Runtime enums (spec 015 §3); each mirrors an existing CHECK constraint. ---

wlc:ArtifactKind a skos:ConceptScheme ; skos:prefLabel "Artifact kind" .
wlc:docker_image a skos:Concept ; skos:inScheme wlc:ArtifactKind ; skos:prefLabel "docker image" ;
    skos:definition "An OCI container image in a registry, identified by name, tag and digest." .
wlc:pypi a skos:Concept ; skos:inScheme wlc:ArtifactKind ; skos:prefLabel "PyPI package" ;
    skos:definition "A Python distribution published to a package index." .
wlc:git_tag a skos:Concept ; skos:inScheme wlc:ArtifactKind ; skos:prefLabel "git tag" ;
    skos:definition "A published release tag on a repository." .
wlc:binary a skos:Concept ; skos:inScheme wlc:ArtifactKind ; skos:prefLabel "binary release" ;
    skos:definition "A compiled executable published as a release asset." .

wlc:DeploymentStatus a skos:ConceptScheme ; skos:prefLabel "Deployment status" .
wlc:pending a skos:Concept ; skos:inScheme wlc:DeploymentStatus ; skos:prefLabel "pending" ;
    skos:definition "Target known; no reconciliation observed yet." .
wlc:reconciling a skos:Concept ; skos:inScheme wlc:DeploymentStatus ; skos:prefLabel "reconciling" ;
    skos:definition "Rollout in progress." .
wlc:deployed a skos:Concept ; skos:inScheme wlc:DeploymentStatus ; skos:prefLabel "deployed" ;
    skos:definition "Reconciliation succeeded; what the deployment used — an artifact, or a commit for IaC/GitOps targets — is live on this target." .
wlc:failed a skos:Concept ; skos:inScheme wlc:DeploymentStatus ; skos:prefLabel "failed" ;
    skos:definition "Reconciliation failed; the target is not serving the intended artifact." .

wlc:DeployTargetKind a skos:ConceptScheme ; skos:prefLabel "Deployment target kind" .
wlc:flux_kustomization a skos:Concept ; skos:inScheme wlc:DeployTargetKind ;
    skos:prefLabel "Flux Kustomization" ;
    skos:definition "A Flux-reconciled target. v1 files HelmRelease events here too; the distinction survives in the event type, not the target kind." .
wlc:pypi_target a skos:Concept ; skos:inScheme wlc:DeployTargetKind ; skos:prefLabel "PyPI publish" ;
    skos:definition "Publication to a package index as the delivery target. Spelled apart from wlc:pypi: the artifact kind and the target kind are different concepts that share a name in the relational schema." .
wlc:manual a skos:Concept ; skos:inScheme wlc:DeployTargetKind ; skos:prefLabel "manual" ;
    skos:definition "A target tracked by hand, with no automated reconciliation signal." .

wlc:RuntimeEventKind a skos:ConceptScheme ; skos:prefLabel "Runtime event kind" .
wlc:crashloop a skos:Concept ; skos:inScheme wlc:RuntimeEventKind ; skos:prefLabel "crashloop" .
wlc:oom a skos:Concept ; skos:inScheme wlc:RuntimeEventKind ; skos:prefLabel "OOM kill" .
wlc:flux_failure a skos:Concept ; skos:inScheme wlc:RuntimeEventKind ; skos:prefLabel "Flux failure" .
wlc:flux_recovery a skos:Concept ; skos:inScheme wlc:RuntimeEventKind ; skos:prefLabel "Flux recovery" .
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd /Users/stig/git/sunstone/rdf-registry && uv run pytest tests/test_wl_runtime.py -q`
Expected: PASS (9 tests).

- [ ] **Step 6: Run the full suite**

Run: `uv run pytest -q`
Expected: same pass/skip counts as the Task 1 baseline plus the 9 new tests.
The site build ignores `worklode.io` IRIs (`scripts/query.py`
`discover_resources` filters on the sunstone base), so no invariant test
should change; if one does, the new TTL broke a repo-wide assumption — fix
the TTL, not the invariant.

- [ ] **Step 7: Commit**

```bash
cd /Users/stig/git/sunstone/rdf-registry
git add rdf/wl tests/test_wl_runtime.py
git commit -m "feat(wl): add runtime-layer vocabulary (worklode spec 015 §2-§4)"
```

---

## Phase 2 — SHACL shapes (rdf-registry)

### Task 3: Node shapes, rejection fixtures, gate wiring (AC6)

**Working directory:** `/Users/stig/git/sunstone/rdf-registry`

**Files:**
- Create: `rdf/shapes/wl-shapes.ttl`
- Create: `tests/fixtures/wl/runtime-abox.ttl` (the conforming seeded graph, reused by Phase 3)
- Create: `tests/fixtures/wl/shacl/artifact-two-kinds.ttl`, `…/artifact-no-kind.ttl`, `…/deployment-no-environment.ttl`, `…/environment-staging.ttl`
- Modify: `scripts/validate_shacl.py:23-24` (`SHAPES` constant) and `:88-96` (gate loop)
- Test: append to `tests/test_wl_runtime.py`

These tests use **pyshacl** (already a dependency), not the Jena CLI: the wl
fixtures are plain Turtle with no RDF 1.2 triple terms, so the
rdflib/pyshacl path suffices and the tests never skip. The Jena gate
(`validate_shacl.py`, ADR-0003) additionally gains the wl shapes so future
instance data in the tree is gated too.

- [ ] **Step 1: Write the failing tests**

Append to `tests/test_wl_runtime.py`:

```python
# --- AC6: the SHACL gate rejects malformed runtime nodes ---

from pyshacl import validate as shacl_validate  # noqa: E402  (grouped with this phase)

SHAPES = Path(__file__).parent.parent / "rdf" / "shapes" / "wl-shapes.ttl"
FIXTURES_WL = Path(__file__).parent / "fixtures" / "wl"
ABOX = FIXTURES_WL / "runtime-abox.ttl"


def wl_shacl(data_path: Path) -> tuple[bool, str]:
    data = Graph()
    data.parse(ONTOLOGY)
    data.parse(CONCEPTS)
    data.parse(data_path)
    shapes = Graph()
    shapes.parse(SHAPES)
    conforms, _, text = shacl_validate(data, shacl_graph=shapes, inference="none")
    return conforms, text


def test_seeded_abox_conforms():
    conforms, text = wl_shacl(ABOX)
    assert conforms, text


@pytest.mark.parametrize("fixture", [
    "artifact-two-kinds.ttl",
    "artifact-no-kind.ttl",
    "deployment-no-environment.ttl",
    "environment-staging.ttl",
])
def test_gate_rejects(fixture):
    conforms, _ = wl_shacl(FIXTURES_WL / "shacl" / fixture)
    assert not conforms, f"{fixture} must violate the wl shapes"
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `uv run pytest tests/test_wl_runtime.py -q`
Expected: the five new tests ERROR — `wl-shapes.ttl` and the fixtures do not
exist yet.

- [ ] **Step 3: Write `rdf/shapes/wl-shapes.ttl`**

The four node shapes from spec 015 §7. Instance IRIs are written in full —
`/` is not legal in a Turtle prefixed local name.

```turtle
@prefix sh:   <http://www.w3.org/ns/shacl#> .
@prefix wl:   <https://worklode.io/ns/wl/ontology#> .
@prefix wlc:  <https://worklode.io/ns/wl/concept/> .
@prefix wlsh: <https://worklode.io/ns/wl/shapes#> .
@prefix dct:  <http://purl.org/dc/terms/> .
@prefix owl:  <http://www.w3.org/2002/07/owl#> .
@prefix prov: <http://www.w3.org/ns/prov#> .

wlsh:ArtifactShape a sh:NodeShape ; sh:targetClass wl:Artifact ;
    sh:property [ sh:path wl:artifactKind ; sh:minCount 1 ; sh:maxCount 1 ;
        sh:in (wlc:docker_image wlc:pypi wlc:git_tag wlc:binary) ] ;
    sh:property [ sh:path owl:versionInfo ; sh:minCount 1 ; sh:maxCount 1 ] ;
    sh:property [ sh:path dct:identifier ; sh:minCount 1 ; sh:maxCount 1 ] .

wlsh:DeploymentShape a sh:NodeShape ; sh:targetClass wl:Deployment ;
    sh:property [ sh:path wl:toEnvironment ; sh:minCount 1 ; sh:maxCount 1 ;
        sh:class wl:Environment ] ;
    sh:property [ sh:path wl:targetKind ; sh:minCount 1 ; sh:maxCount 1 ;
        sh:in (wlc:flux_kustomization wlc:pypi_target wlc:manual) ] ;
    sh:property [ sh:path wl:deploymentStatus ; sh:minCount 1 ; sh:maxCount 1 ;
        sh:in (wlc:pending wlc:reconciling wlc:deployed wlc:failed) ] ;
    sh:property [ sh:path prov:used ; sh:maxCount 1 ] .

# Environment: the instance set is closed to dev and prod (015 §2, Open Q3).
wlsh:EnvironmentShape a sh:NodeShape ; sh:targetClass wl:Environment ;
    sh:in (<https://worklode.io/ns/wl/id/environment/dev>
           <https://worklode.io/ns/wl/id/environment/prod>) .

wlsh:CommitShape a sh:NodeShape ; sh:targetClass wl:Commit ;
    sh:property [ sh:path dct:identifier ; sh:minCount 1 ; sh:maxCount 1 ] .
```

- [ ] **Step 4: Write the conforming seeded ABox**

Create `tests/fixtures/wl/runtime-abox.ttl`. This is the seeded graph for
AC4/AC5/AC7/AC8/AC9 as well: one artifact per kind (distinct §5 IRIs), the
closed environment pair, a commit, a deployed prod deployment using the
docker artifact, and 006's Deliverable example rewritten in `wl:`.

```turtle
@prefix wl:   <https://worklode.io/ns/wl/ontology#> .
@prefix wlc:  <https://worklode.io/ns/wl/concept/> .
@prefix dct:  <http://purl.org/dc/terms/> .
@prefix owl:  <http://www.w3.org/2002/07/owl#> .
@prefix prov: <http://www.w3.org/ns/prov#> .
@prefix xsd:  <http://www.w3.org/2001/XMLSchema#> .

<https://worklode.io/ns/wl/id/environment/dev>
    a wl:Environment ; dct:identifier "dev" .
<https://worklode.io/ns/wl/id/environment/prod>
    a wl:Environment ; dct:identifier "prod" .

<https://worklode.io/ns/wl/id/commit/github.com/sunstoneinstitute/worklode/a16c2a7>
    a wl:Commit ; dct:identifier "a16c2a7" .

<https://worklode.io/ns/wl/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1>
    a wl:Artifact ;
    wl:artifactKind wlc:docker_image ;
    owl:versionInfo "v1" ;
    dct:identifier "ghcr.io/sunstoneinstitute/graph-server" ;
    wl:digest "sha256:8f3c1a2b00000000000000000000000000000000000000000000000000000000" ;
    prov:generatedAtTime "2026-07-28T12:00:00Z"^^xsd:dateTime .

<https://worklode.io/ns/wl/id/artifact/pypi/sunstone-py/0.4.1>
    a wl:Artifact ;
    wl:artifactKind wlc:pypi ;
    owl:versionInfo "0.4.1" ;
    dct:identifier "sunstone-py" .

<https://worklode.io/ns/wl/id/artifact/git_tag/github.com/sunstoneinstitute/worklode/v0.4>
    a wl:Artifact ;
    wl:artifactKind wlc:git_tag ;
    owl:versionInfo "v0.4" ;
    dct:identifier "github.com/sunstoneinstitute/worklode" ;
    prov:wasDerivedFrom <https://worklode.io/ns/wl/id/commit/github.com/sunstoneinstitute/worklode/a16c2a7> ;
    wl:covers <https://worklode.io/ns/wl/id/commit/github.com/sunstoneinstitute/worklode/a16c2a7> .

<https://worklode.io/ns/wl/id/artifact/binary/lode/v0.4.0>
    a wl:Artifact ;
    wl:artifactKind wlc:binary ;
    owl:versionInfo "v0.4.0" ;
    dct:identifier "lode" .

<https://worklode.io/ns/wl/id/deployment/prod/flux_kustomization/graph-server>
    a wl:Deployment ;
    wl:toEnvironment <https://worklode.io/ns/wl/id/environment/prod> ;
    wl:targetKind wlc:flux_kustomization ;
    wl:deploymentStatus wlc:deployed ;
    dct:identifier "graph-server" ;
    prov:used <https://worklode.io/ns/wl/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> ;
    prov:startedAtTime "2026-07-28T12:05:00Z"^^xsd:dateTime ;
    dct:modified "2026-07-29T09:00:00Z"^^xsd:dateTime .

# AC5: 006 §Deliverable example, typed against this vocabulary.
<https://worklode.io/ns/wl/id/deliverable/worklode-graph-live>
    a wl:Deliverable ;
    dct:title "Worklode KG live in prod" ;
    dct:relation
        <https://worklode.io/ns/wl/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> ,
        <https://worklode.io/ns/wl/id/environment/prod> .
```

- [ ] **Step 5: Write the four rejection fixtures**

`tests/fixtures/wl/shacl/artifact-two-kinds.ttl`:

```turtle
@prefix wl:  <https://worklode.io/ns/wl/ontology#> .
@prefix wlc: <https://worklode.io/ns/wl/concept/> .
@prefix dct: <http://purl.org/dc/terms/> .
@prefix owl: <http://www.w3.org/2002/07/owl#> .

<https://worklode.io/ns/wl/id/artifact/docker_image/bad/two-kinds>
    a wl:Artifact ;
    wl:artifactKind wlc:docker_image , wlc:pypi ;
    owl:versionInfo "v1" ; dct:identifier "bad" .
```

`tests/fixtures/wl/shacl/artifact-no-kind.ttl`:

```turtle
@prefix wl:  <https://worklode.io/ns/wl/ontology#> .
@prefix dct: <http://purl.org/dc/terms/> .
@prefix owl: <http://www.w3.org/2002/07/owl#> .

<https://worklode.io/ns/wl/id/artifact/docker_image/bad/no-kind>
    a wl:Artifact ; owl:versionInfo "v1" ; dct:identifier "bad" .
```

`tests/fixtures/wl/shacl/deployment-no-environment.ttl`:

```turtle
@prefix wl:  <https://worklode.io/ns/wl/ontology#> .
@prefix wlc: <https://worklode.io/ns/wl/concept/> .
@prefix dct: <http://purl.org/dc/terms/> .

<https://worklode.io/ns/wl/id/deployment/prod/flux_kustomization/bad>
    a wl:Deployment ;
    wl:targetKind wlc:flux_kustomization ;
    wl:deploymentStatus wlc:deployed ;
    dct:identifier "bad" .
```

`tests/fixtures/wl/shacl/environment-staging.ttl`:

```turtle
@prefix wl:  <https://worklode.io/ns/wl/ontology#> .
@prefix dct: <http://purl.org/dc/terms/> .

<https://worklode.io/ns/wl/id/environment/staging>
    a wl:Environment ; dct:identifier "staging" .
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `uv run pytest tests/test_wl_runtime.py -q`
Expected: PASS (14 tests).

- [ ] **Step 7: Wire the wl shapes into the Jena gate**

In `scripts/validate_shacl.py`, replace the single-shapes constant
(line 24):

```python
SHAPES = REPO_ROOT / "rdf" / "shapes" / "gtio-shapes.ttl"
```

with:

```python
SHAPES_FILES = [
    REPO_ROOT / "rdf" / "shapes" / "gtio-shapes.ttl",
    REPO_ROOT / "rdf" / "shapes" / "wl-shapes.ttl",
]
```

and in `validate_shacl()` replace the existence check and the per-file loop:

```python
    for shapes in SHAPES_FILES:
        if not shapes.exists():
            error(f"SHACL shapes graph not found: {shapes}")

    rdf_dir = REPO_ROOT / "rdf"
    any_violation = False
    for data_file in _data_files(rdf_dir):
        rel = data_file.relative_to(REPO_ROOT)
        for shapes in SHAPES_FILES:
            report = shacl_report(shapes, data_file)
            conforms, violations = report_conforms(report)
            if conforms:
                info(f"✓ {rel} conforms to {shapes.name}")
            else:
                any_violation = True
                print(f"\n❌ SHACL violations in {rel} ({shapes.name}):")
                for v in violations:
                    print(f"   - {v}")
```

`tests/test_shacl.py` imports only `shacl_report`, `report_conforms` and
`validate_shacl`, so no test changes are needed.

- [ ] **Step 8: Run the full suite**

Run: `uv run pytest -q`
Expected: PASS/SKIP as before plus the 5 new tests. If the Jena `shacl` CLI
is installed locally, `test_real_rdf_tree_conforms` now also runs the wl
shapes over the tree — `rdf/wl/` contains TBox only, so it conforms.

- [ ] **Step 9: Commit**

```bash
cd /Users/stig/git/sunstone/rdf-registry
git add rdf/shapes/wl-shapes.ttl tests/fixtures/wl tests/test_wl_runtime.py scripts/validate_shacl.py
git commit -m "feat(wl): add runtime-layer SHACL shapes and gate wiring (spec 015 §7)"
```

---

## Phase 3 — Seeded-graph semantics (rdf-registry)

### Task 4: SPARQL reads, owlrl closure, coverage edge (AC4, AC5, AC7, AC8, AC9)

**Working directory:** `/Users/stig/git/sunstone/rdf-registry`

**Files:**
- Create: `tests/fixtures/wl/inconsistent-abox.ttl`
- Test: append to `tests/test_wl_runtime.py`

AC4 is explicitly a **seeded** test (the spec says so: `deployments.artifact_id`
is null in practice today, so this criterion tests the vocabulary; the ingest
gap is Open Q5, out of scope).

- [ ] **Step 1: Write the failing tests**

Append to `tests/test_wl_runtime.py`:

```python
# --- AC4/AC5/AC7/AC8/AC9: seeded-graph semantics ---

import owlrl  # noqa: E402

# owlrl surfaces OWL 2 RL inconsistencies (e.g. cax-adc, disjoint classes
# with a common individual) as err:ErrorMessage nodes in the closed graph.
ERR = Namespace("http://www.daml.org/2002/03/agents/agent-ont#")
PROV = Namespace("http://www.w3.org/ns/prov#")

DOCKER_ARTIFACT = URIRef(
    WLID + "artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1")
GIT_TAG_ARTIFACT = URIRef(
    WLID + "artifact/git_tag/github.com/sunstoneinstitute/worklode/v0.4")
COMMIT = URIRef(WLID + "commit/github.com/sunstoneinstitute/worklode/a16c2a7")
DEPLOYMENT = URIRef(WLID + "deployment/prod/flux_kustomization/graph-server")
PROD = URIRef(WLID + "environment/prod")
DELIVERABLE = URIRef(WLID + "deliverable/worklode-graph-live")


@pytest.fixture(scope="module")
def seeded(vocab) -> Graph:
    g = Graph()
    for t in vocab:
        g.add(t)
    g.parse(ABOX)
    return g


def test_ac4_artifact_live_in_prod_for_target(seeded):
    rows = list(seeded.query("""
        PREFIX wl:  <https://worklode.io/ns/wl/ontology#>
        PREFIX wlc: <https://worklode.io/ns/wl/concept/>
        PREFIX dct: <http://purl.org/dc/terms/>
        PREFIX prov: <http://www.w3.org/ns/prov#>
        SELECT ?artifact WHERE {
            ?d a wl:Deployment ;
               wl:toEnvironment <https://worklode.io/ns/wl/id/environment/prod> ;
               dct:identifier "graph-server" ;
               wl:deploymentStatus wlc:deployed ;
               prov:used ?artifact .
        }"""))
    assert [r.artifact for r in rows] == [DOCKER_ARTIFACT]
    assert (DEPLOYMENT, WL.toEnvironment, PROD) in seeded


def test_ac5_deliverable_relation_targets_are_typed(seeded):
    dct = Namespace("http://purl.org/dc/terms/")
    targets = set(seeded.objects(DELIVERABLE, dct.relation))
    assert targets == {DOCKER_ARTIFACT, PROD}
    assert (DOCKER_ARTIFACT, RDF.type, WL.Artifact) in seeded
    assert (PROD, RDF.type, WL.Environment) in seeded


def test_ac8_release_covers_projected_commit(seeded):
    assert (GIT_TAG_ARTIFACT, WL.covers, COMMIT) in seeded
    assert (COMMIT, RDF.type, WL.Commit) in seeded


def test_ac9_build_and_runtimeevent_have_no_instances(seeded):
    assert list(seeded.subjects(RDF.type, WL.Build)) == []
    assert list(seeded.subjects(RDF.type, WL.RuntimeEvent)) == []


def _closed(*paths: Path) -> Graph:
    g = Graph()
    g.parse(ONTOLOGY)
    g.parse(CONCEPTS)
    for p in paths:
        g.parse(p)
    owlrl.DeductiveClosure(owlrl.OWLRL_Semantics).expand(g)
    return g


def test_ac7_prov_types_inferred():
    g = _closed(ABOX)
    assert (DOCKER_ARTIFACT, RDF.type, PROV.Entity) in g
    assert (COMMIT, RDF.type, PROV.Entity) in g
    assert (DEPLOYMENT, RDF.type, PROV.Activity) in g
    assert list(g.subjects(RDF.type, ERR.ErrorMessage)) == []


def test_ac7_dual_typed_node_is_inconsistent():
    g = _closed(FIXTURES_WL / "inconsistent-abox.ttl")
    messages = [
        str(g.value(m, ERR.error))
        for m in g.subjects(RDF.type, ERR.ErrorMessage)
    ]
    assert any("common individual" in m for m in messages), messages
```

- [ ] **Step 2: Write the inconsistent fixture**

Create `tests/fixtures/wl/inconsistent-abox.ttl`:

```turtle
@prefix wl: <https://worklode.io/ns/wl/ontology#> .

<https://worklode.io/ns/wl/id/fixture/artifact-and-deployment>
    a wl:Artifact , wl:Deployment .
```

- [ ] **Step 3: Run the tests to verify they pass**

Run: `uv run pytest tests/test_wl_runtime.py -q`
Expected: PASS (20 tests). If `test_ac7_dual_typed_node_is_inconsistent`
fails on the message text, print `messages` — the assertion keys on owlrl's
cax-adc wording ("Disjoint classes … have a common individual …"); the fix
is matching owlrl's actual message, never weakening the disjointness axiom.

- [ ] **Step 4: Run the full suite**

Run: `uv run pytest -q`
Expected: PASS/SKIP baseline plus 20 wl tests.

- [ ] **Step 5: Commit**

```bash
cd /Users/stig/git/sunstone/rdf-registry
git add tests/fixtures/wl/inconsistent-abox.ttl tests/test_wl_runtime.py
git commit -m "test(wl): seeded-graph semantics for the runtime layer (spec 015 AC4-AC9)"
```

---

## Phase 4 — Projection functions (worklode)

The pure row→IRI/triple half of 007's future `observed/deploy` deriver:
deterministic functions of the relational natural keys, per 015 §5's
principle. No graph-server client, no named-graph management — only what
AC3 and AC8 require. Package `internal/graphproj` depends on
`internal/store` types only (no DB), so its tests need no Postgres.

### Task 5: IRI grammar (AC3, first half)

**Working directory:** `/Users/stig/git/sunstone/worklode` (module
`github.com/sunstoneinstitute/worklode`)

**Files:**
- Create: `internal/graphproj/iri.go`
- Test: `internal/graphproj/iri_test.go`

- [ ] **Step 1: Write the failing test**

```go
package graphproj

import "testing"

// The expected strings are spec 015 §5's examples verbatim.
func TestIRIGrammar(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"docker image",
			ArtifactIRI("docker_image", "ghcr.io/sunstoneinstitute/graph-server", "v1"),
			"https://worklode.io/ns/wl/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1"},
		{"pypi",
			ArtifactIRI("pypi", "sunstone-py", "0.4.1"),
			"https://worklode.io/ns/wl/id/artifact/pypi/sunstone-py/0.4.1"},
		{"git tag",
			ArtifactIRI("git_tag", "github.com/sunstoneinstitute/worklode", "v0.4"),
			"https://worklode.io/ns/wl/id/artifact/git_tag/github.com/sunstoneinstitute/worklode/v0.4"},
		{"binary",
			ArtifactIRI("binary", "lode", "v0.4.0"),
			"https://worklode.io/ns/wl/id/artifact/binary/lode/v0.4.0"},
		{"deployment",
			DeploymentIRI("prod", "flux_kustomization", "graph-server"),
			"https://worklode.io/ns/wl/id/deployment/prod/flux_kustomization/graph-server"},
		{"environment",
			EnvironmentIRI("prod"),
			"https://worklode.io/ns/wl/id/environment/prod"},
		{"commit",
			CommitIRI(GitHubHost, "sunstoneinstitute/worklode", "a16c2a7"),
			"https://worklode.io/ns/wl/id/commit/github.com/sunstoneinstitute/worklode/a16c2a7"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q; want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestArtifactIRIsDistinctAcrossKinds(t *testing.T) {
	seen := map[string]string{}
	for _, kind := range []string{"docker_image", "pypi", "git_tag", "binary"} {
		iri := ArtifactIRI(kind, "same-name", "v1")
		if prev, dup := seen[iri]; dup {
			t.Fatalf("kinds %s and %s collide on %s", prev, kind, iri)
		}
		seen[iri] = kind
	}
}

func TestEscapeSegment(t *testing.T) {
	cases := []struct{ in, want string }{
		{"graph-server", "graph-server"},
		{"ghcr.io/sunstoneinstitute/graph-server", "ghcr.io/sunstoneinstitute/graph-server"}, // slashes verbatim
		{"a b", "a%20b"},
		{"a%b", "a%25b"},
		{"a`b", "a%60b"},
		{`a"b`, "a%22b"},
	}
	for _, tc := range cases {
		if got := escapeSegment(tc.in); got != tc.want {
			t.Errorf("escapeSegment(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/graphproj/...`
Expected: FAIL — the package does not exist.

- [ ] **Step 3: Write the implementation**

```go
// Package graphproj projects execution-backbone rows into the wl: runtime
// layer (spec 015): pure functions from relational natural keys to instance
// IRIs and triples. Projection is a function of the row — deterministic and
// idempotent — which is what makes 007's observed/deploy deriver contract
// (full-replace, no row→IRI side table) satisfiable. This package does no
// I/O; the deriver that talks to the graph server is spec 007's and does
// not exist yet.
package graphproj

import (
	"fmt"
	"strings"
)

// Namespaces per spec 014 §1 / 006 §IRI.
const (
	nsOntology = "https://worklode.io/ns/wl/ontology#"
	nsConcept  = "https://worklode.io/ns/wl/concept/"
	nsID       = "https://worklode.io/ns/wl/id/"

	// GitHubHost qualifies repo-derived local ids. The backbone stores
	// repos as "owner/name" (GitHub full_name); the IRI grammar wants
	// host-qualified coordinates.
	GitHubHost = "github.com"
)

// ArtifactIRI mirrors artifacts' natural key UNIQUE (kind, name, version) —
// spec 015 §5, kind-first.
func ArtifactIRI(kind, name, version string) string {
	return nsID + "artifact/" + escapeSegment(kind) + "/" +
		escapeSegment(name) + "/" + escapeSegment(version)
}

// DeploymentIRI mirrors deployments' natural key
// UNIQUE (environment, target_kind, target_name).
func DeploymentIRI(environment, targetKind, targetName string) string {
	return nsID + "deployment/" + escapeSegment(environment) + "/" +
		escapeSegment(targetKind) + "/" + escapeSegment(targetName)
}

// EnvironmentIRI names a deployment stage; the instance set is closed to
// dev and prod by SHACL.
func EnvironmentIRI(name string) string {
	return nsID + "environment/" + escapeSegment(name)
}

// CommitIRI mirrors main_commits' natural key UNIQUE (repo, sha), with the
// host prefixed ("owner/name" → "github.com/owner/name").
func CommitIRI(host, repo, sha string) string {
	return nsID + "commit/" + escapeSegment(host) + "/" +
		escapeSegment(repo) + "/" + escapeSegment(sha)
}

// escapeSegment percent-encodes characters that cannot appear raw in an IRI
// path. Slashes stay verbatim: local ids are slash namespaces with opaque
// paths (006 §IRI, 015 §5).
func escapeSegment(s string) string {
	const forbidden = "<>\"{}|\\^`%?#"
	var b strings.Builder
	for _, r := range s {
		if r <= 0x20 || strings.ContainsRune(forbidden, r) {
			for _, c := range []byte(string(r)) {
				fmt.Fprintf(&b, "%%%02X", c)
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/graphproj/ -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/graphproj
git commit -m "Add the runtime-layer IRI grammar (spec 015 §5)"
```

### Task 6: Deterministic triple rendering (AC3, second half)

**Files:**
- Create: `internal/graphproj/triple.go`
- Test: `internal/graphproj/triple_test.go`

- [ ] **Step 1: Write the failing test**

```go
package graphproj

import (
	"bytes"
	"testing"
)

func TestRenderSortsAndDeduplicates(t *testing.T) {
	ts := []Triple{
		{S: "https://x/b", P: "https://p/1", O: "https://o/1"},
		{S: "https://x/a", P: "https://p/1", O: "https://o/1"},
		{S: "https://x/b", P: "https://p/1", O: "https://o/1"}, // duplicate
		{S: "https://x/a", P: "https://p/2", O: "v1", Lit: true},
	}
	want := "<https://x/a> <https://p/1> <https://o/1> .\n" +
		"<https://x/a> <https://p/2> \"v1\" .\n" +
		"<https://x/b> <https://p/1> <https://o/1> .\n"
	if got := string(Render(ts)); got != want {
		t.Fatalf("Render = %q; want %q", got, want)
	}
}

func TestRenderIsInputOrderIndependent(t *testing.T) {
	a := []Triple{{S: "https://x/a", P: "https://p/1", O: "https://o/1"},
		{S: "https://x/b", P: "https://p/1", O: "https://o/1"}}
	b := []Triple{a[1], a[0]}
	if !bytes.Equal(Render(a), Render(b)) {
		t.Fatal("Render depends on input order")
	}
}

func TestRenderLiteralEscapingAndDatatype(t *testing.T) {
	ts := []Triple{
		{S: "https://x/a", P: "https://p/1", O: "line1\n\"quoted\"\\", Lit: true},
		{S: "https://x/a", P: "https://p/2", O: "2026-07-28T12:00:00Z", Lit: true,
			DT: "http://www.w3.org/2001/XMLSchema#dateTime"},
	}
	want := "<https://x/a> <https://p/1> \"line1\\n\\\"quoted\\\"\\\\\" .\n" +
		"<https://x/a> <https://p/2> \"2026-07-28T12:00:00Z\"^^<http://www.w3.org/2001/XMLSchema#dateTime> .\n"
	if got := string(Render(ts)); got != want {
		t.Fatalf("Render = %q; want %q", got, want)
	}
}

func TestRenderEmpty(t *testing.T) {
	if got := Render(nil); got != nil {
		t.Fatalf("Render(nil) = %q; want nil", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/graphproj/`
Expected: FAIL — `undefined: Triple`, `undefined: Render`

- [ ] **Step 3: Write the implementation**

```go
package graphproj

import (
	"sort"
	"strings"
)

// Triple is one emitted statement. O is an IRI unless Lit is true; DT is an
// optional datatype IRI for literals.
type Triple struct {
	S, P, O string
	Lit     bool
	DT      string
}

// Render serializes triples as sorted, deduplicated N-Triples. Sorting plus
// dedupe make the output a pure function of the triple set, so re-projecting
// an unchanged row is a byte-identical no-op (015 acceptance criterion 3).
func Render(ts []Triple) []byte {
	lines := make([]string, 0, len(ts))
	seen := make(map[string]struct{}, len(ts))
	for _, t := range ts {
		l := formatTriple(t)
		if _, dup := seen[l]; dup {
			continue
		}
		seen[l] = struct{}{}
		lines = append(lines, l)
	}
	if len(lines) == 0 {
		return nil
	}
	sort.Strings(lines)
	return []byte(strings.Join(lines, "\n") + "\n")
}

func formatTriple(t Triple) string {
	obj := "<" + t.O + ">"
	if t.Lit {
		obj = `"` + escapeLiteral(t.O) + `"`
		if t.DT != "" {
			obj += "^^<" + t.DT + ">"
		}
	}
	return "<" + t.S + "> <" + t.P + "> " + obj + " ."
}

// escapeLiteral applies N-Triples string escaping.
func escapeLiteral(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`,
	)
	return r.Replace(s)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/graphproj/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/graphproj/triple.go internal/graphproj/triple_test.go
git commit -m "Render projection triples as sorted deterministic N-Triples"
```

### Task 7: Row→triples with the §6 guards (AC3, AC8)

**Files:**
- Create: `internal/graphproj/runtime.go`
- Test: `internal/graphproj/runtime_test.go`

Store types being projected: `store.Artifact` and `store.Deployment`
(`internal/store/artifacts.go:14-36`). DB enum quirk: `deployments.target_kind`
stores `pypi` (`0001_baseline.up.sql:153`) but the SKOS concept is
`wlc:pypi_target` (015 §3) — the projector owns that mapping.

- [ ] **Step 1: Write the failing test**

```go
package graphproj

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

func testArtifact() store.Artifact {
	digest := "sha256:8f3c1a2b"
	return store.Artifact{
		Kind:      "docker_image",
		Name:      "ghcr.io/sunstoneinstitute/graph-server",
		Version:   "v1",
		Digest:    &digest,
		Repo:      "sunstoneinstitute/graph-server",
		SourceSHA: "a16c2a7",
		BuiltAt:   time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
	}
}

func TestArtifactTriples(t *testing.T) {
	got := string(Render(ArtifactTriples(testArtifact(), func(string) bool { return true })))
	want := []string{
		`<https://worklode.io/ns/wl/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/wl/ontology#Artifact> .`,
		`<https://worklode.io/ns/wl/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <https://worklode.io/ns/wl/ontology#artifactKind> <https://worklode.io/ns/wl/concept/docker_image> .`,
		`<https://worklode.io/ns/wl/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <http://www.w3.org/2002/07/owl#versionInfo> "v1" .`,
		`<https://worklode.io/ns/wl/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <http://purl.org/dc/terms/identifier> "ghcr.io/sunstoneinstitute/graph-server" .`,
		`<https://worklode.io/ns/wl/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <https://worklode.io/ns/wl/ontology#digest> "sha256:8f3c1a2b" .`,
		`<https://worklode.io/ns/wl/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <http://www.w3.org/ns/prov#generatedAtTime> "2026-07-28T12:00:00Z"^^<http://www.w3.org/2001/XMLSchema#dateTime> .`,
		`<https://worklode.io/ns/wl/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <http://www.w3.org/ns/prov#wasDerivedFrom> <https://worklode.io/ns/wl/id/commit/github.com/sunstoneinstitute/graph-server/a16c2a7> .`,
	}
	for _, line := range want {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("missing line:\n%s\ngot:\n%s", line, got)
		}
	}
	if n := strings.Count(got, "\n"); n != len(want) {
		t.Errorf("rendered %d lines; want %d:\n%s", n, len(want), got)
	}
}

// AC3: re-projecting an unchanged row is a byte-identical no-op.
func TestArtifactProjectionIsIdempotent(t *testing.T) {
	known := func(string) bool { return true }
	first := Render(ArtifactTriples(testArtifact(), known))
	second := Render(ArtifactTriples(testArtifact(), known))
	if !bytes.Equal(first, second) {
		t.Fatal("re-projecting an unchanged artifact row changed bytes")
	}
}

// AC8: a release whose target_commitish is a branch name (unresolvable sha)
// projects no prov:wasDerivedFrom edge rather than a fabricated commit node.
func TestBranchNameProjectsNoCommitEdge(t *testing.T) {
	a := testArtifact()
	a.Kind = "git_tag"
	a.Name = "sunstoneinstitute/worklode"
	a.SourceSHA = "main" // UI-created release: branch name, not a sha
	got := string(Render(ArtifactTriples(a, func(string) bool { return false })))
	if strings.Contains(got, "wasDerivedFrom") {
		t.Fatalf("branch-name source_sha minted a commit edge:\n%s", got)
	}
	// The git_tag coordinate is host-qualified (015 §5 example).
	if !strings.Contains(got, "<https://worklode.io/ns/wl/id/artifact/git_tag/github.com/sunstoneinstitute/worklode/v1>") {
		t.Fatalf("git_tag artifact IRI not host-qualified:\n%s", got)
	}
}

func TestArtifactWithoutRepoProjectsNoCommitEdge(t *testing.T) {
	a := testArtifact()
	a.Repo = ""
	got := string(Render(ArtifactTriples(a, func(string) bool { return true })))
	if strings.Contains(got, "wasDerivedFrom") {
		t.Fatal("artifact without a repo projected a commit edge")
	}
}

func TestDeploymentTriples(t *testing.T) {
	artifactID := int64(1)
	d := store.Deployment{
		ArtifactID:  &artifactID,
		Environment: "prod",
		TargetKind:  "flux_kustomization",
		TargetName:  "graph-server",
		Status:      "deployed",
		FirstSeen:   time.Date(2026, 7, 28, 12, 5, 0, 0, time.UTC),
		LastUpdate:  time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC),
	}
	a := testArtifact()
	got := string(Render(DeploymentTriples(d, &a)))
	want := []string{
		`<https://worklode.io/ns/wl/id/deployment/prod/flux_kustomization/graph-server> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/wl/ontology#Deployment> .`,
		`<https://worklode.io/ns/wl/id/deployment/prod/flux_kustomization/graph-server> <https://worklode.io/ns/wl/ontology#toEnvironment> <https://worklode.io/ns/wl/id/environment/prod> .`,
		`<https://worklode.io/ns/wl/id/deployment/prod/flux_kustomization/graph-server> <https://worklode.io/ns/wl/ontology#targetKind> <https://worklode.io/ns/wl/concept/flux_kustomization> .`,
		`<https://worklode.io/ns/wl/id/deployment/prod/flux_kustomization/graph-server> <https://worklode.io/ns/wl/ontology#deploymentStatus> <https://worklode.io/ns/wl/concept/deployed> .`,
		`<https://worklode.io/ns/wl/id/deployment/prod/flux_kustomization/graph-server> <http://purl.org/dc/terms/identifier> "graph-server" .`,
		`<https://worklode.io/ns/wl/id/deployment/prod/flux_kustomization/graph-server> <http://www.w3.org/ns/prov#startedAtTime> "2026-07-28T12:05:00Z"^^<http://www.w3.org/2001/XMLSchema#dateTime> .`,
		`<https://worklode.io/ns/wl/id/deployment/prod/flux_kustomization/graph-server> <http://purl.org/dc/terms/modified> "2026-07-29T09:00:00Z"^^<http://www.w3.org/2001/XMLSchema#dateTime> .`,
		`<https://worklode.io/ns/wl/id/deployment/prod/flux_kustomization/graph-server> <http://www.w3.org/ns/prov#used> <https://worklode.io/ns/wl/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> .`,
	}
	for _, line := range want {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("missing line:\n%s\ngot:\n%s", line, got)
		}
	}
}

// deployments.artifact_id is null in practice today (015 §6, Open Q5):
// the prov:used edge is specified but must simply be absent, not invented.
func TestDeploymentWithoutArtifactHasNoUsedEdge(t *testing.T) {
	d := store.Deployment{
		Environment: "dev", TargetKind: "manual", TargetName: "x",
		Status:    "pending",
		FirstSeen: time.Unix(0, 0).UTC(), LastUpdate: time.Unix(0, 0).UTC(),
	}
	if got := string(Render(DeploymentTriples(d, nil))); strings.Contains(got, "prov#used") {
		t.Fatalf("deployment without artifact projected prov:used:\n%s", got)
	}
}

// The DB stores target_kind 'pypi'; the concept is wlc:pypi_target (015 §3).
func TestPyPITargetKindConcept(t *testing.T) {
	d := store.Deployment{
		Environment: "prod", TargetKind: "pypi", TargetName: "sunstone-py",
		Status:    "deployed",
		FirstSeen: time.Unix(0, 0).UTC(), LastUpdate: time.Unix(0, 0).UTC(),
	}
	got := string(Render(DeploymentTriples(d, nil)))
	if !strings.Contains(got, "<https://worklode.io/ns/wl/concept/pypi_target>") {
		t.Fatalf("target kind pypi not mapped to wlc:pypi_target:\n%s", got)
	}
}

func TestEnvironmentAndCommitTriples(t *testing.T) {
	envs := string(Render(EnvironmentTriples()))
	for _, line := range []string{
		`<https://worklode.io/ns/wl/id/environment/dev> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/wl/ontology#Environment> .`,
		`<https://worklode.io/ns/wl/id/environment/prod> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/wl/ontology#Environment> .`,
	} {
		if !strings.Contains(envs, line+"\n") {
			t.Errorf("missing line:\n%s\ngot:\n%s", line, envs)
		}
	}

	got := string(Render(CommitTriples(GitHubHost, "sunstoneinstitute/worklode", "a16c2a7")))
	for _, line := range []string{
		`<https://worklode.io/ns/wl/id/commit/github.com/sunstoneinstitute/worklode/a16c2a7> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/wl/ontology#Commit> .`,
		`<https://worklode.io/ns/wl/id/commit/github.com/sunstoneinstitute/worklode/a16c2a7> <http://purl.org/dc/terms/identifier> "a16c2a7" .`,
	} {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("missing line:\n%s\ngot:\n%s", line, got)
		}
	}
}

// AC8, first half: a release_frontiers row projects as wl:covers from the
// git_tag artifact to the frontier commit.
func TestReleaseCoversTriples(t *testing.T) {
	got := string(Render(ReleaseCoversTriples("sunstoneinstitute/worklode", "v0.4", "a16c2a7")))
	want := `<https://worklode.io/ns/wl/id/artifact/git_tag/github.com/sunstoneinstitute/worklode/v0.4> <https://worklode.io/ns/wl/ontology#covers> <https://worklode.io/ns/wl/id/commit/github.com/sunstoneinstitute/worklode/a16c2a7> .` + "\n"
	if got != want {
		t.Fatalf("ReleaseCoversTriples = %q; want %q", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/graphproj/`
Expected: FAIL — `undefined: ArtifactTriples` (and the other new functions).

- [ ] **Step 3: Write the implementation**

```go
package graphproj

import (
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// External vocabulary terms the projection reuses (015 §4 table).
const (
	rdfType        = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
	dctIdentifier  = "http://purl.org/dc/terms/identifier"
	dctModified    = "http://purl.org/dc/terms/modified"
	owlVersionInfo = "http://www.w3.org/2002/07/owl#versionInfo"
	provGeneratedAtTime = "http://www.w3.org/ns/prov#generatedAtTime"
	provStartedAtTime   = "http://www.w3.org/ns/prov#startedAtTime"
	provUsed            = "http://www.w3.org/ns/prov#used"
	provWasDerivedFrom  = "http://www.w3.org/ns/prov#wasDerivedFrom"
	xsdDateTime         = "http://www.w3.org/2001/XMLSchema#dateTime"
)

// CommitKnown reports whether sha names a known main_commits row for the
// artifact's repo (store.MainIDForSHA != nil, in the caller's transaction).
type CommitKnown func(sha string) bool

// artifactCoordinate returns the (name, IRI) coordinate for an artifact row.
// git_tag names are stored as bare "owner/name" (applyRelease,
// internal/hooks/github.go:411-418) and are host-qualified here to match the
// §5 grammar; the other kinds carry their registry coordinate already.
func artifactCoordinate(a store.Artifact) (name, iri string) {
	name = a.Name
	if a.Kind == "git_tag" {
		name = GitHubHost + "/" + name
	}
	return name, ArtifactIRI(a.Kind, name, a.Version)
}

// ArtifactTriples projects one artifacts row (015 §6). The commit edge is
// guarded: target_commitish is frequently a branch name, and minting a
// commit IRI from one would create a plausible, permanently wrong node —
// emit prov:wasDerivedFrom only when source_sha resolves via known. An
// artifact with no repo projects no commit edge at all: a repository alone
// does not identify a commit.
func ArtifactTriples(a store.Artifact, known CommitKnown) []Triple {
	name, s := artifactCoordinate(a)
	ts := []Triple{
		{S: s, P: rdfType, O: nsOntology + "Artifact"},
		{S: s, P: nsOntology + "artifactKind", O: nsConcept + a.Kind},
		{S: s, P: owlVersionInfo, O: a.Version, Lit: true},
		{S: s, P: dctIdentifier, O: name, Lit: true},
	}
	if a.Digest != nil {
		ts = append(ts, Triple{S: s, P: nsOntology + "digest", O: *a.Digest, Lit: true})
	}
	if !a.BuiltAt.IsZero() {
		ts = append(ts, Triple{S: s, P: provGeneratedAtTime, O: xsdTime(a.BuiltAt), Lit: true, DT: xsdDateTime})
	}
	if a.Repo != "" && a.SourceSHA != "" && known != nil && known(a.SourceSHA) {
		ts = append(ts, Triple{S: s, P: provWasDerivedFrom, O: CommitIRI(GitHubHost, a.Repo, a.SourceSHA)})
	}
	return ts
}

// DeploymentTriples projects one deployments row. artifact is the row
// deployments.artifact_id resolves to, nil when unset — null in practice
// today (015 §6, Open Q5), so prov:used is simply absent.
func DeploymentTriples(d store.Deployment, artifact *store.Artifact) []Triple {
	s := DeploymentIRI(d.Environment, d.TargetKind, d.TargetName)
	ts := []Triple{
		{S: s, P: rdfType, O: nsOntology + "Deployment"},
		{S: s, P: nsOntology + "toEnvironment", O: EnvironmentIRI(d.Environment)},
		{S: s, P: nsOntology + "targetKind", O: nsConcept + targetKindConcept(d.TargetKind)},
		{S: s, P: nsOntology + "deploymentStatus", O: nsConcept + d.Status},
		{S: s, P: dctIdentifier, O: d.TargetName, Lit: true},
		{S: s, P: provStartedAtTime, O: xsdTime(d.FirstSeen), Lit: true, DT: xsdDateTime},
		{S: s, P: dctModified, O: xsdTime(d.LastUpdate), Lit: true, DT: xsdDateTime},
	}
	if artifact != nil {
		_, iri := artifactCoordinate(*artifact)
		ts = append(ts, Triple{S: s, P: provUsed, O: iri})
	}
	return ts
}

// EnvironmentTriples projects the fixed instance set {dev, prod} — static,
// matching the SHACL closure and store.NormalizeEnvironment.
func EnvironmentTriples() []Triple {
	var ts []Triple
	for _, name := range []string{"dev", "prod"} {
		s := EnvironmentIRI(name)
		ts = append(ts,
			Triple{S: s, P: rdfType, O: nsOntology + "Environment"},
			Triple{S: s, P: dctIdentifier, O: name, Lit: true},
		)
	}
	return ts
}

// CommitTriples projects one main_commits row.
func CommitTriples(host, repo, sha string) []Triple {
	s := CommitIRI(host, repo, sha)
	return []Triple{
		{S: s, P: rdfType, O: nsOntology + "Commit"},
		{S: s, P: dctIdentifier, O: sha, Lit: true},
	}
}

// ReleaseCoversTriples projects one release_frontiers row joined to its
// main_commits sha: the release's git_tag artifact wl:covers the frontier
// commit (015 §6 — release_frontiers projects as an edge, not a node).
func ReleaseCoversTriples(repo, tag, sha string) []Triple {
	return []Triple{{
		S: ArtifactIRI("git_tag", GitHubHost+"/"+repo, tag),
		P: nsOntology + "covers",
		O: CommitIRI(GitHubHost, repo, sha),
	}}
}

// targetKindConcept maps a deployments.target_kind DB value to its concept
// id. The DB stores 'pypi' for the target kind, but the concept is
// wlc:pypi_target — the artifact kind and target kind are different concepts
// that share a name in the relational schema (015 §3).
func targetKindConcept(dbKind string) string {
	if dbKind == "pypi" {
		return "pypi_target"
	}
	return dbKind
}

func xsdTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/graphproj/ -v`
Expected: PASS (all tests).

- [ ] **Step 5: Run `go vet` and the package tests once more**

Run: `go vet ./internal/graphproj/ && go test ./internal/graphproj/...`
Expected: clean, PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/graphproj
git commit -m "Project runtime rows into wl: triples with the source-sha guard"
```

---

## Phase 5 — Verification

### Task 8: Full-suite verification, both repos

- [ ] **Step 1: rdf-registry suite**

Run: `cd /Users/stig/git/sunstone/rdf-registry && uv run pytest -q`
Expected: PASS (with the same Jena-CLI skips as the Task 1 baseline).

- [ ] **Step 2: worklode suite**

Run: `cd /Users/stig/git/sunstone/worklode && go test ./...`
Expected: PASS. `internal/graphproj` is a leaf package — nothing else should
have changed. Store/api/cmd tests need Postgres, as before.

- [ ] **Step 3: Acceptance-criteria walkthrough**

Confirm each criterion maps to green tests:
1 → `test_layer_query_returns_exactly_the_runtime_terms` ·
2 → `test_no_foreign_vocabulary_imported`, `test_spdx_cited_only_via_seealso` ·
3 → `TestIRIGrammar`, `TestArtifactIRIsDistinctAcrossKinds`, `TestArtifactProjectionIsIdempotent` ·
4 → `test_ac4_artifact_live_in_prod_for_target` ·
5 → `test_ac5_deliverable_relation_targets_are_typed` ·
6 → `test_gate_rejects[...]` ·
7 → `test_ac7_prov_types_inferred`, `test_ac7_dual_typed_node_is_inconsistent` ·
8 → `test_ac8_release_covers_projected_commit`, `TestReleaseCoversTriples`, `TestBranchNameProjectsNoCommitEdge` ·
9 → `test_ac9_build_and_runtimeevent_have_no_instances` (and no task above
creates a Build or RuntimeEvent instance anywhere).

- [ ] **Step 4: Report the deliberate leftovers**

In the completion summary, restate what this plan intentionally did not do,
so nobody mistakes it for a gap: 007 deriver wiring, image-publish ingest
(Open Q5 — the single highest-value follow-up), RuntimeEvent natural key
(Open Q1), env_deploys frontier node (v2), and worklode.io publishing
(rdf-registry branch `worklode-io-spec`).
