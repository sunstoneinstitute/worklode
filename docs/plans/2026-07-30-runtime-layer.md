---
status: accepted
task: WL-27
covers: docs/specs/006-knowledge-graph.md
---
# Runtime layer (spec 015, folded into 006) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the runtime layer its vocabulary — six PROV-anchored classes,
four SKOS schemes, seven properties, SHACL shapes — plus the deterministic
row→IRI/triple projection functions that 007's `observed/deploy` deriver will
emit, satisfying the nine acceptance criteria of spec 015 (whose text now
lives folded into 006).

**Architecture (rewritten after WL-108/WL-25 landed):** The vocabulary's home
is **`ns/*.ttl` in this repo** — `ns/ontology.ttl`, `ns/concept.ttl`,
`ns/shapes.ttl`. The original rdf-registry route (`rdf/wl/*.ttl`, that repo's
pytest harness) is cancelled: rdf-registry#31 is closed, publishing
`ns/*.ttl` under `worklode.io/ns/` is unowned (`docs/follow-ups.md`), and
`ns/` already carries every runtime class, scheme, property and shape this
plan set out to mint. Tasks 1–4 therefore reduce to a **diff-and-fill**: check
the term list below against `ns/`, fill any gap by amending the spec first
and mirroring `ns/` in the same commit. The projection side lands in
`internal/graphproj` — extending the pure `Term`/`Triple`/`Document` core
that knowledge-graph part 1 landed (WL-25) — as row→triple functions over
`internal/store` types, no I/O, so re-projecting an unchanged row is provably
a byte-identical no-op. IRI minting is `internal/kg/iri` (also landed under
WL-25; plain-string constructors — the runtime patterns `iri.Artifact`,
`iri.Deployment`, `iri.Environment`, `iri.Commit` already exist). Nothing
talks to a graph server here — that is 007's deriver, out of scope.

**Tech Stack:** Turtle/SHACL/SKOS/PROV-O validated with
`riot --validate ns/*.ttl` and the Oxigraph parse gate
(`internal/graphproj/oxigraph_test.go`); Go with standard-library testing.

**Spec:** `docs/specs/006-knowledge-graph.md`

---

## One repository

Everything runs in this repo. An earlier revision split Tasks 1–4 into
rdf-registry; that route is cancelled (see Architecture above), and the task
numbering is kept so cross-references stay valid.

## Already implemented vs. what remains

**Already in place — do not rebuild:**

- Relational schema and `CHECK` constraints the SKOS schemes mirror:
  `artifacts`/`deployments`/`runtime_events` in
  `deploy/base/migrations/0001_baseline.up.sql:137-168`;
  `main_commits`/`env_deploys`/`release_frontiers` in
  `deploy/base/migrations/0005_delivery.up.sql:29-66`. Spec 006 §7: **no
  migration** — every enum mirrors an existing `CHECK`.
- Ingest: `applyRelease` (`internal/hooks/github.go:400`) creates `git_tag`
  artifacts and release frontiers; `internal/hooks/flux.go`,
  `internal/hooks/deployment.go`, `internal/hooks/push.go` feed
  deployments/env_deploys/main_commits; store code in
  `internal/store/artifacts.go`, `internal/store/delivery.go`,
  `internal/store/runtime.go`.
- Spec-document amendments (006 §7): already applied as callout blocks —
  006 mint list (`docs/specs/006-knowledge-graph.md:67`), disjointness
  (`:109`), Layer 2 WorkflowRun drop + Commit promotion (`:400`), Layer 3
  supersession (`:417`), Deliverable typing (`:434`), Artifact IRI grammar
  (`:503`); 007 deriver output (`docs/specs/007-drift-and-overview.md:131`).
  No doc edits in this plan.

**Also already in place — the vocabulary itself.** `ns/ontology.ttl`
declares all six runtime classes (`wl:Artifact`, `wl:Build`,
`wl:Deployment`, `wl:Environment`, `wl:Commit`, `wl:RuntimeEvent`), their
disjointness axiom and all seven runtime properties; `ns/concept.ttl` the
four schemes (`wlc:ArtifactKind`, `wlc:DeploymentStatus`,
`wlc:DeployTargetKind`, `wlc:RuntimeEventKind`) plus `wlc:ModelLayer`;
`ns/shapes.ttl` the node shapes (`wl:ArtifactShape`, `wl:DeploymentShape`,
`wl:EnvironmentShape` closed to dev/prod, `wl:CommitShape`,
`wl:cutFromShape`). **One term renamed since this plan was written:** the
Artifact→Commit frontier property is `wl:cutFrom`, not `wl:covers` — 026
§6.1 took `wl:covers` for the Plan→Section undertaking (`ns/ontology.ttl`
records the rationale). Every emission and test below uses `wl:cutFrom`.

**What remains (this plan):** the diff-and-fill pass over `ns/` (Tasks 1–4,
expected to find little or nothing), and the projection functions with the
§6 guards — `internal/graphproj` has the task projection (WL-25/WL-26) but
no runtime row→triple functions.

**Explicitly out of scope, per the spec:** 007's deriver/graph-server wiring;
image-publish ingest closing the `deployments.artifact_id` gap (Open Q5);
`wl:RuntimeEvent` projection (Open Q1 — declared, unprojected);
`wl:Build` instances (no source); the `env_deploys` frontier node (v2);
serving `ns/*.ttl` under `worklode.io/ns/` — unowned, tracked in
`docs/follow-ups.md` (the rdf-registry publish route died with
rdf-registry#31).

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `internal/graphproj/runtime.go` | row→triples for Artifact/Deployment/Environment/Commit/cutFrom with §6 guards; `GitHubHost`, `splitRepo` |
| `internal/graphproj/runtime_test.go` | AC3 byte-identical no-op; AC8 branch-name guard; `pypi`→`pypi_target`; §10.1 example IRIs verbatim |

**Modified files**

| Path | Change |
|---|---|
| `ns/*.ttl` | only what the Task 2–3 diff finds missing (expected: nothing); spec amendment first, mirrored in the same commit |

Already landed, consumed as-is: `internal/kg/iri` (grammar + `iri_test.go`
covering the §10.1 runtime examples), `internal/graphproj`'s
`Term`/`Triple`/`Document` with escaping/determinism tests
(`triple.go`/`triple_test.go`), and the Oxigraph `ns/` parse gate
(`oxigraph_test.go`).

**Test commands**

- Vocabulary: `riot --validate ns/*.ttl`
- Projection (pure — no Postgres):
  `go test -trimpath ./internal/graphproj/... ./internal/kg/iri/...`
- Full suite: `make test` (store/api/cmd need Postgres via
  `store.OpenTestStore`)

---

## Phase 1 — Vocabulary baseline (ns/)

### Task 1: Baseline the vocabulary checks

**Working directory:** the repo root.

- [ ] **Step 1: Confirm the tree is clean and the checks pass**

Run: `git status --short --branch`, then `riot --validate ns/*.ttl` and
`go test -trimpath ./internal/graphproj/` (the Oxigraph `ns/` parse gate;
skips without a reachable endpoint — `docker compose up -d oxigraph` to run
it for real).
Expected: clean tree, both checks PASS. Record the state so later failures
are attributable to this work.

### Task 2: Diff the runtime classes, properties and schemes against `ns/`

**Files:**
- Modify: `ns/ontology.ttl`, `ns/concept.ttl` — only if the diff finds a gap
  (expected: none)

The 13 runtime terms this plan set out to mint, all expected present and
tagged `wl:layer wlc:runtime`:

- **Classes** (`ns/ontology.ttl`, disjointness axiom included):
  `wl:Artifact` (⊑ `prov:Entity`), `wl:Build` (⊑ `prov:Activity`),
  `wl:Deployment` (⊑ `prov:Activity`), `wl:Environment` (parentless),
  `wl:Commit` (⊑ `prov:Entity`), `wl:RuntimeEvent` (⊑ `prov:Activity`).
- **Properties** (`ns/ontology.ttl`): `wl:artifactKind`, `wl:digest`,
  `wl:cutFrom` (the Artifact→Commit frontier edge — spelled `wl:covers`
  when this plan was written; renamed by the 026 §6.1 work),
  `wl:toEnvironment`, `wl:deploymentStatus`, `wl:targetKind`,
  `wl:runtimeEventKind`.
- **Schemes** (`ns/concept.ttl`), each mirroring an existing CHECK
  constraint: `wlc:ArtifactKind` {`docker_image`, `pypi`, `git_tag`,
  `binary`}, `wlc:DeploymentStatus` {`pending`, `reconciling`, `deployed`,
  `failed`}, `wlc:DeployTargetKind` {`flux_kustomization`, `pypi_target`,
  `manual`}, `wlc:RuntimeEventKind` {`crashloop`, `oom`, `flux_failure`,
  `flux_recovery`}.

- [ ] **Step 1: Diff**

Grep each term in `ns/`; confirm layer tags, domains/ranges and scheme
membership match the list above (spec 006 §2.1, §3.1, §6 as folded).

- [ ] **Step 2: Fill any gap**

For a missing or wrong term: amend spec 006 first, mirror `ns/` in the same
commit (CLAUDE.md ordering), re-run `riot --validate ns/*.ttl`. If the gap
is a *design* disagreement rather than an omission, stop and escalate — do
not improvise vocabulary.

## Phase 2 — SHACL shapes (ns/)

### Task 3: Diff the runtime node shapes against `ns/shapes.ttl`

**Files:**
- Modify: `ns/shapes.ttl` — only if the diff finds a gap (expected: none)

Expected present (spec 006 §7): `wl:ArtifactShape` (exactly one
`wl:artifactKind` from `wlc:ArtifactKind`; one `owl:versionInfo`; one
`dct:identifier`), `wl:DeploymentShape` (one `wl:toEnvironment` of class
`wl:Environment`; `wl:targetKind`/`wl:deploymentStatus` from their schemes;
`prov:used` maxCount 1), `wl:EnvironmentShape` (instance set closed to
`id/environment/dev` and `id/environment/prod`), `wl:CommitShape` (exactly
one `dct:identifier`), and `wl:cutFromShape` (subjects of `wl:cutFrom` are
Artifacts, objects Commits). No shape for `wl:Build`, `wl:RuntimeEvent` —
deliberate (006 §15 item 7).

- [ ] **Step 1: Diff, fill as in Task 2, `riot --validate ns/*.ttl`**

Note: this repo has no SHACL *execution* harness — `riot` validates syntax
and the Oxigraph gate validates parseability, but nothing here runs the
shapes against instance data (the original plan's pyshacl/owlrl rejection
fixtures died with the rdf-registry route). Enforcement happens wherever
the graph is loaded with a validator; `docs/follow-ups.md` tracks the
`ns/` gaps that are gated on other specs.

## Phase 3 — Seeded-graph semantics

### Task 4: Reduced to the Oxigraph parse gate

The original AC4/AC5/AC7/AC9 pytest suite (rdflib SPARQL reads, owlrl
disjointness closure) ran in rdf-registry's harness, which this repo does
not have and deliberately does not grow (no Python stack in a Go repo). What
survives locally:

- **Parseability + queryability of `ns/`** — the Oxigraph gate
  (`internal/graphproj/oxigraph_test.go`) already loads `ns/*.ttl` and
  queries it.
- **AC8's projection half** — Task 7's `TestReleaseCutFromTriples` and the
  branch-name guard test.
- **AC9 (no Build/RuntimeEvent instances)** — holds by construction: no
  projection function below emits either class.

- [ ] **Step 1: Verify the gate is green**

Run: `docker compose up -d oxigraph && go test -trimpath ./internal/graphproj/`
Expected: PASS (not skipped).


## Phase 4 — Projection functions (worklode)

The pure row→IRI/triple half of 007's future `observed/deploy` deriver:
deterministic functions of the relational natural keys, per 006 §10.1's
principle. No graph-server client, no named-graph management — only what
AC3 and AC8 require. Package `internal/graphproj` depends on
`internal/store` types only (no DB), so its tests need no Postgres.

### Task 5: Depend on the shared IRI grammar (AC3, first half) — landed, verify only

The IRI grammar is owned by `internal/kg/iri`
(`2026-07-30-knowledge-graph-1-graph-foundations.md` Task 1; landed —
WL-25): `internal/graphproj` imports it instead of minting its own instance
IRIs. The landed constructors are **plain-string** — pure concatenation, no
validation, no error return (that plan's design call 5; the
`(string, error)` signature an earlier revision of this plan converged on
died with the superseded platform-graph-design plan) — and the four runtime
patterns are already there: `iri.Artifact(kind, name, version)` (kind-first,
006 §10.1), `iri.Deployment(env, targetKind, targetName)`,
`iri.Environment(name)`, `iri.Commit(host, owner, repo, sha)` (four parts,
not the three-part `(host, "owner/repo", sha)` shape this plan originally
sketched).

- [ ] **Step 1: Verify the grammar and its tests cover the §10.1 examples**

`internal/kg/iri/iri_test.go`'s `TestGrammar` table already carries the
docker-image artifact, deployment, environment and commit examples verbatim.
Confirm; append any §10.1 example still missing (same table style). Kind
distinctness needs no test — the kind is the first path segment, so
distinct kinds cannot collide.

Run: `go test -trimpath ./internal/kg/iri/`
Expected: PASS. No graphproj-side code in this task; `GitHubHost` arrives
with Task 7 — it is a graphproj-level convention (how the projector
host-qualifies repo-derived local ids before minting), not part of the
shared grammar.

### Task 6: Deterministic triple rendering (AC3, second half) — landed, verify only

The rendering core landed with knowledge-graph part 1
(`internal/graphproj/triple.go`): `Triple{S, P string; O Term}` with the
`Term` constructors `IRIRef`/`Text`/`Typed` (an earlier revision of this
plan sketched a `{S, P, O string, Lit bool, DT string}` triple and a
`Render` function — superseded), N-Triples literal escaping, and
`Document(ts) []byte` rendering sorted, deduplicated N-Triples so the same
triple set is byte-identical regardless of build order.
`triple_test.go` already proves ordering, dedupe, escaping and datatypes.

- [ ] **Step 1: Verify**

Run: `go test -trimpath ./internal/graphproj/`
Expected: PASS. Nothing to build; Task 7 emits `[]graphproj.Triple` and
callers render with `graphproj.Document`.


### Task 7: Row→triples with the §6 guards (AC3, AC8)

**Files:**
- Create: `internal/graphproj/runtime.go`
- Test: `internal/graphproj/runtime_test.go`

Store types being projected: `store.Artifact` and `store.Deployment`
(`internal/store/artifacts.go`). DB enum quirk: `deployments.target_kind`
stores `pypi` (`0001_baseline.up.sql:153`) but the SKOS concept is
`wlc:pypi_target` (006 §6) — the projector owns that mapping.

IRI minting goes through `internal/kg/iri`'s plain-string constructors
(Task 5), so the row→triple functions return `[]Triple` with no error — the
same shape as the landed `TaskTriples`. The one input that can be malformed,
a repo that is not `owner/name`, is handled the way every §6 guard is: the
edge needing it is omitted, never fabricated. `wl:` terms resolve through
`iri.Term`/`iri.Concept` (the `task.go` convention); the reused external
vocabulary gets exported constants beside the ones `task.go` already
declares. `GitHubHost` and `splitRepo` live here — repos are stored as
GitHub's "owner/name" full_name, but `iri.Commit` wants the parts
separately.

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
	got := string(Document(ArtifactTriples(testArtifact(), func(string) bool { return true })))
	want := []string{
		`<https://worklode.io/ns/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/ontology#Artifact> .`,
		`<https://worklode.io/ns/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <https://worklode.io/ns/ontology#artifactKind> <https://worklode.io/ns/concept/docker_image> .`,
		`<https://worklode.io/ns/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <http://www.w3.org/2002/07/owl#versionInfo> "v1" .`,
		`<https://worklode.io/ns/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <http://purl.org/dc/terms/identifier> "ghcr.io/sunstoneinstitute/graph-server" .`,
		`<https://worklode.io/ns/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <https://worklode.io/ns/ontology#digest> "sha256:8f3c1a2b" .`,
		`<https://worklode.io/ns/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <http://www.w3.org/ns/prov#generatedAtTime> "2026-07-28T12:00:00Z"^^<http://www.w3.org/2001/XMLSchema#dateTime> .`,
		`<https://worklode.io/ns/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> <http://www.w3.org/ns/prov#wasDerivedFrom> <https://worklode.io/ns/id/commit/github.com/sunstoneinstitute/graph-server/a16c2a7> .`,
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
	first := Document(ArtifactTriples(testArtifact(), known))
	second := Document(ArtifactTriples(testArtifact(), known))
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
	got := string(Document(ArtifactTriples(a, func(string) bool { return false })))
	if strings.Contains(got, "wasDerivedFrom") {
		t.Fatalf("branch-name source_sha minted a commit edge:\n%s", got)
	}
	// The git_tag coordinate is host-qualified (006 §10.1 example).
	if !strings.Contains(got, "<https://worklode.io/ns/id/artifact/git_tag/github.com/sunstoneinstitute/worklode/v1>") {
		t.Fatalf("git_tag artifact IRI not host-qualified:\n%s", got)
	}
}

func TestArtifactWithoutRepoProjectsNoCommitEdge(t *testing.T) {
	a := testArtifact()
	a.Repo = ""
	got := string(Document(ArtifactTriples(a, func(string) bool { return true })))
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
	got := string(Document(DeploymentTriples(d, &a)))
	want := []string{
		`<https://worklode.io/ns/id/deployment/prod/flux_kustomization/graph-server> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/ontology#Deployment> .`,
		`<https://worklode.io/ns/id/deployment/prod/flux_kustomization/graph-server> <https://worklode.io/ns/ontology#toEnvironment> <https://worklode.io/ns/id/environment/prod> .`,
		`<https://worklode.io/ns/id/deployment/prod/flux_kustomization/graph-server> <https://worklode.io/ns/ontology#targetKind> <https://worklode.io/ns/concept/flux_kustomization> .`,
		`<https://worklode.io/ns/id/deployment/prod/flux_kustomization/graph-server> <https://worklode.io/ns/ontology#deploymentStatus> <https://worklode.io/ns/concept/deployed> .`,
		`<https://worklode.io/ns/id/deployment/prod/flux_kustomization/graph-server> <http://purl.org/dc/terms/identifier> "graph-server" .`,
		`<https://worklode.io/ns/id/deployment/prod/flux_kustomization/graph-server> <http://www.w3.org/ns/prov#startedAtTime> "2026-07-28T12:05:00Z"^^<http://www.w3.org/2001/XMLSchema#dateTime> .`,
		`<https://worklode.io/ns/id/deployment/prod/flux_kustomization/graph-server> <http://purl.org/dc/terms/modified> "2026-07-29T09:00:00Z"^^<http://www.w3.org/2001/XMLSchema#dateTime> .`,
		`<https://worklode.io/ns/id/deployment/prod/flux_kustomization/graph-server> <http://www.w3.org/ns/prov#used> <https://worklode.io/ns/id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1> .`,
	}
	for _, line := range want {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("missing line:\n%s\ngot:\n%s", line, got)
		}
	}
}

// deployments.artifact_id is null in practice today (006 §11.1, Open Q5):
// the prov:used edge is specified but must simply be absent, not invented.
func TestDeploymentWithoutArtifactHasNoUsedEdge(t *testing.T) {
	d := store.Deployment{
		Environment: "dev", TargetKind: "manual", TargetName: "x",
		Status:    "pending",
		FirstSeen: time.Unix(0, 0).UTC(), LastUpdate: time.Unix(0, 0).UTC(),
	}
	if got := string(Document(DeploymentTriples(d, nil))); strings.Contains(got, "prov#used") {
		t.Fatalf("deployment without artifact projected prov:used:\n%s", got)
	}
}

// The DB stores target_kind 'pypi'; the concept is wlc:pypi_target (006 §6).
func TestPyPITargetKindConcept(t *testing.T) {
	d := store.Deployment{
		Environment: "prod", TargetKind: "pypi", TargetName: "sunstone-py",
		Status:    "deployed",
		FirstSeen: time.Unix(0, 0).UTC(), LastUpdate: time.Unix(0, 0).UTC(),
	}
	got := string(Document(DeploymentTriples(d, nil)))
	if !strings.Contains(got, "<https://worklode.io/ns/concept/pypi_target>") {
		t.Fatalf("target kind pypi not mapped to wlc:pypi_target:\n%s", got)
	}
}

func TestEnvironmentAndCommitTriples(t *testing.T) {
	envs := string(Document(EnvironmentTriples()))
	for _, line := range []string{
		`<https://worklode.io/ns/id/environment/dev> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/ontology#Environment> .`,
		`<https://worklode.io/ns/id/environment/prod> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/ontology#Environment> .`,
	} {
		if !strings.Contains(envs, line+"\n") {
			t.Errorf("missing line:\n%s\ngot:\n%s", line, envs)
		}
	}

	got := string(Document(CommitTriples(GitHubHost, "sunstoneinstitute/worklode", "a16c2a7")))
	for _, line := range []string{
		`<https://worklode.io/ns/id/commit/github.com/sunstoneinstitute/worklode/a16c2a7> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/ontology#Commit> .`,
		`<https://worklode.io/ns/id/commit/github.com/sunstoneinstitute/worklode/a16c2a7> <http://purl.org/dc/terms/identifier> "a16c2a7" .`,
	} {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("missing line:\n%s\ngot:\n%s", line, got)
		}
	}
}

// AC8, first half: a release_frontiers row projects as wl:cutFrom (spelled
// wl:covers until 026 §6.1 took that name) from the git_tag artifact to the
// frontier commit.
func TestReleaseCutFromTriples(t *testing.T) {
	got := string(Document(ReleaseCutFromTriples("sunstoneinstitute/worklode", "v0.4", "a16c2a7")))
	want := `<https://worklode.io/ns/id/artifact/git_tag/github.com/sunstoneinstitute/worklode/v0.4> <https://worklode.io/ns/ontology#cutFrom> <https://worklode.io/ns/id/commit/github.com/sunstoneinstitute/worklode/a16c2a7> .` + "\n"
	if got != want {
		t.Fatalf("ReleaseCutFromTriples = %q; want %q", got, want)
	}
}

func TestMalformedRepoOmitsEdges(t *testing.T) {
	if ts := CommitTriples(GitHubHost, "not-owner-name", "a16c2a7"); ts != nil {
		t.Fatalf("CommitTriples on malformed repo = %v; want nil", ts)
	}
	if ts := ReleaseCutFromTriples("not-owner-name", "v1", "a16c2a7"); ts != nil {
		t.Fatalf("ReleaseCutFromTriples on malformed repo = %v; want nil", ts)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -trimpath ./internal/graphproj/`
Expected: FAIL — `undefined: ArtifactTriples` (and the other new functions).

- [ ] **Step 3: Write the implementation**

```go
package graphproj

import (
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// GitHubHost qualifies repo-derived local ids passed to internal/kg/iri. The
// backbone stores repos as "owner/name" (GitHub full_name); the shared IRI
// grammar wants host-qualified, owner/repo-split coordinates.
const GitHubHost = "github.com"

// External vocabulary the runtime projection reuses (006 §3.1 table),
// extending the constants task.go declares. wl: terms resolve through
// iri.Term, wlc: concepts through iri.Concept — never hardcoded.
const (
	DCTIdentifier       = "http://purl.org/dc/terms/identifier"
	OWLVersionInfo      = "http://www.w3.org/2002/07/owl#versionInfo"
	ProvGeneratedAtTime = "http://www.w3.org/ns/prov#generatedAtTime"
	ProvStartedAtTime   = "http://www.w3.org/ns/prov#startedAtTime"
	ProvUsed            = "http://www.w3.org/ns/prov#used"
	ProvWasDerivedFrom  = "http://www.w3.org/ns/prov#wasDerivedFrom"
)

// CommitKnown reports whether sha names a known main_commits row for the
// artifact's repo (store.MainIDForSHA != nil, in the caller's transaction).
type CommitKnown func(sha string) bool

// splitRepo splits a GitHub "owner/name" full_name into its two parts, as
// internal/kg/iri.Commit wants them. The backbone always stores repos in
// this form; ok is false only for malformed input.
func splitRepo(full string) (owner, name string, ok bool) {
	i := strings.IndexByte(full, '/')
	if i <= 0 || i == len(full)-1 {
		return "", "", false
	}
	return full[:i], full[i+1:], true
}

// artifactCoordinate returns the (name, IRI) coordinate for an artifact row.
// git_tag names are stored as bare "owner/name" (applyRelease,
// internal/hooks/github.go) and are host-qualified here to match the §10.1
// grammar; the other kinds carry their registry coordinate already.
func artifactCoordinate(a store.Artifact) (name, artifactIRI string) {
	name = a.Name
	if a.Kind == "git_tag" {
		name = GitHubHost + "/" + name
	}
	return name, iri.Artifact(a.Kind, name, a.Version)
}

// ArtifactTriples projects one artifacts row (006 §11.1). The commit edge is
// guarded: target_commitish is frequently a branch name, and minting a
// commit IRI from one would create a plausible, permanently wrong node —
// emit prov:wasDerivedFrom only when source_sha resolves via known. An
// artifact with no repo, or a malformed one, projects no commit edge at
// all: a repository alone does not identify a commit.
func ArtifactTriples(a store.Artifact, known CommitKnown) []Triple {
	name, s := artifactCoordinate(a)
	ts := []Triple{
		{S: s, P: RDFType, O: IRIRef(iri.Term("Artifact"))},
		{S: s, P: iri.Term("artifactKind"), O: IRIRef(iri.Concept(a.Kind))},
		{S: s, P: OWLVersionInfo, O: Text(a.Version)},
		{S: s, P: DCTIdentifier, O: Text(name)},
	}
	if a.Digest != nil {
		ts = append(ts, Triple{S: s, P: iri.Term("digest"), O: Text(*a.Digest)})
	}
	if !a.BuiltAt.IsZero() {
		ts = append(ts, Triple{S: s, P: ProvGeneratedAtTime, O: Typed(xsdTime(a.BuiltAt), XSDDateTime)})
	}
	if a.Repo != "" && a.SourceSHA != "" && known != nil && known(a.SourceSHA) {
		if owner, repo, ok := splitRepo(a.Repo); ok {
			ts = append(ts, Triple{S: s, P: ProvWasDerivedFrom,
				O: IRIRef(iri.Commit(GitHubHost, owner, repo, a.SourceSHA))})
		}
	}
	return ts
}

// DeploymentTriples projects one deployments row. artifact is the row
// deployments.artifact_id resolves to, nil when unset — null in practice
// today (006 §11.1, Open Q5), so prov:used is simply absent.
func DeploymentTriples(d store.Deployment, artifact *store.Artifact) []Triple {
	s := iri.Deployment(d.Environment, d.TargetKind, d.TargetName)
	ts := []Triple{
		{S: s, P: RDFType, O: IRIRef(iri.Term("Deployment"))},
		{S: s, P: iri.Term("toEnvironment"), O: IRIRef(iri.Environment(d.Environment))},
		{S: s, P: iri.Term("targetKind"), O: IRIRef(iri.Concept(targetKindConcept(d.TargetKind)))},
		{S: s, P: iri.Term("deploymentStatus"), O: IRIRef(iri.Concept(d.Status))},
		{S: s, P: DCTIdentifier, O: Text(d.TargetName)},
		{S: s, P: ProvStartedAtTime, O: Typed(xsdTime(d.FirstSeen), XSDDateTime)},
		{S: s, P: DCTModified, O: Typed(xsdTime(d.LastUpdate), XSDDateTime)},
	}
	if artifact != nil {
		_, artifactIRI := artifactCoordinate(*artifact)
		ts = append(ts, Triple{S: s, P: ProvUsed, O: IRIRef(artifactIRI)})
	}
	return ts
}

// EnvironmentTriples projects the fixed instance set {dev, prod} — static,
// matching the SHACL closure and store.NormalizeEnvironment.
func EnvironmentTriples() []Triple {
	var ts []Triple
	for _, name := range []string{"dev", "prod"} {
		s := iri.Environment(name)
		ts = append(ts,
			Triple{S: s, P: RDFType, O: IRIRef(iri.Term("Environment"))},
			Triple{S: s, P: DCTIdentifier, O: Text(name)},
		)
	}
	return ts
}

// CommitTriples projects one main_commits row. repo is "owner/name"
// (GitHub full_name); a malformed repo projects nothing — the §6 guard
// posture, an unmintable node is omitted rather than fabricated.
func CommitTriples(host, repo, sha string) []Triple {
	owner, name, ok := splitRepo(repo)
	if !ok {
		return nil
	}
	s := iri.Commit(host, owner, name, sha)
	return []Triple{
		{S: s, P: RDFType, O: IRIRef(iri.Term("Commit"))},
		{S: s, P: DCTIdentifier, O: Text(sha)},
	}
}

// ReleaseCutFromTriples projects one release_frontiers row joined to its
// main_commits sha: the release's git_tag artifact wl:cutFrom the frontier
// commit (006 §11.1 — release_frontiers projects as an edge, not a node;
// the property was spelled wl:covers until 026 §6.1 took that name for the
// Plan→Section undertaking). repo is "owner/name" (GitHub full_name); a
// malformed repo projects nothing.
func ReleaseCutFromTriples(repo, tag, sha string) []Triple {
	owner, name, ok := splitRepo(repo)
	if !ok {
		return nil
	}
	return []Triple{{
		S: iri.Artifact("git_tag", GitHubHost+"/"+repo, tag),
		P: iri.Term("cutFrom"),
		O: IRIRef(iri.Commit(GitHubHost, owner, name, sha)),
	}}
}

// targetKindConcept maps a deployments.target_kind DB value to its concept
// id. The DB stores 'pypi' for the target kind, but the concept is
// wlc:pypi_target — the artifact kind and target kind are different concepts
// that share a name in the relational schema (006 §6).
func targetKindConcept(dbKind string) string {
	if dbKind == "pypi" {
		return "pypi_target"
	}
	return dbKind
}

func xsdTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -trimpath ./internal/graphproj/ -v`
Expected: PASS (all tests).

- [ ] **Step 5: Run `go vet` and the package tests once more**

Run: `go vet ./internal/graphproj/ && go test -trimpath ./internal/graphproj/...`
Expected: clean, PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/graphproj
git commit -m "Project runtime rows into wl: triples with the source-sha guard"
```

---

## Phase 5 — Verification

### Task 8: Full-suite verification

- [ ] **Step 1: Vocabulary and full suite**

Run: `riot --validate ns/*.ttl && make test`
Expected: PASS. `internal/graphproj` gains only leaf code — nothing else
should have changed. Store/api/cmd tests need Postgres, as before; run the
Oxigraph-gated tests for real with `docker compose up -d oxigraph`.

- [ ] **Step 2: Acceptance-criteria walkthrough**

Confirm each criterion (spec 015's nine, folded into 006) maps to green
evidence:
1 (layer tags) and 2 (no foreign imports) → the Task 2 diff over `ns/`,
which already carries the terms with their tags — the old graph-side pytest
assertions died with the rdf-registry harness ·
3 → `TestGrammar` (`internal/kg/iri`), `TestArtifactProjectionIsIdempotent` ·
4, 5, 7 → the vocabulary and shapes exist in `ns/` (Tasks 2–3); no in-repo
SHACL/owlrl runner exercises them against instance data (Task 4) ·
6 → the four node shapes in `ns/shapes.ttl` (Task 3) ·
8 → `TestReleaseCutFromTriples`, `TestBranchNameProjectsNoCommitEdge` ·
9 → `TestMalformedRepoOmitsEdges` plus construction: no function above
emits a Build or RuntimeEvent instance anywhere.

- [ ] **Step 3: Report the deliberate leftovers**

In the completion summary, restate what this plan intentionally did not do,
so nobody mistakes it for a gap: 007 deriver wiring, image-publish ingest
(Open Q5 — the single highest-value follow-up), RuntimeEvent natural key
(Open Q1), env_deploys frontier node (v2), SHACL execution against instance
data (no harness here), and serving `ns/` under `worklode.io/ns/`
(unowned — `docs/follow-ups.md`).
