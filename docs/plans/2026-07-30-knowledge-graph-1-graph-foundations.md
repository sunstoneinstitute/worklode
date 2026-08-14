---
status: accepted
task: WL-8
covers: docs/specs/006-knowledge-graph.md
---
# Knowledge graph 1/2 (spec 006): vocabulary, IRIs & the graph layer — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 1 of 2. Task numbering is global across the series: this plan
holds Tasks 1–6 and ends when the whole graph side is built and proven against
Oxigraph in tests, with no backbone or server coupling yet;
`2026-07-30-knowledge-graph-2-projector.md` holds Tasks 7–11 and ends when
backbone tasks are mirrored into the graph in production. Part 1 must be
merged before part 2 starts. This part carries the series-wide context —
scope, design calls, and the full file-structure tables; part 2 restates only
what shapes its own tasks.

**Goal:** Ship the Worklode half of spec 006: the `wl:` vocabulary sources, the
canonical IRI grammar as a Go package, and a backbone→graph projector that
mirrors every task into its Workstream named graph and proves it with a SPARQL
read-back.

**Architecture:** The `wl:` ontology/SKOS/SHACL sources are authored under
`rdf/wl/` (staged for the rdf-registry PR) and parse-gated by loading them into
Oxigraph in tests. A new `internal/kg/iri` package fixes the IRI grammar, a new
`internal/graph` package turns a backbone task row into triples and renders
per-subject-replace SPARQL updates, and a new `internal/projector` polls the
existing `state_log` outbox (checkpointed in a new `graph_projection` table)
and pushes each dirty task into the SPARQL endpoint. `lode serve` runs the
projector as a background loop when `LODE_GRAPH_URL` is set.

**Tech Stack:** Go 1.26, cobra CLI, PostgreSQL via `database/sql`,
standard-library testing, SPARQL 1.1 Protocol + Graph Store Protocol over
`net/http`, `golang.org/x/oauth2/clientcredentials` (already a dependency) for
Keycloak client-credentials auth, Oxigraph (docker) as the test endpoint.

**Spec:** `docs/specs/006-knowledge-graph.md`

---

## Scope: spec 006 is too large for one plan

Spec 006 spans a cross-repo rdf-registry PR with its own CI gates, a projector
service, graph-side design authoring, and is amended throughout by specs 014
and 015. This plan is deliberately **phase 1**: the vocabulary, the IRI
grammar, and a tracer-bullet vertical slice of the projection (backbone task →
quads in a Workstream named graph → SPARQL read-back, acceptance criteria 4, 8
and 10b). Deferred to later plans:

- **rdf-registry PR + CI gates** (acceptance criteria 1, 10a/c/d): moving
  `rdf/wl/` into rdf-registry, the SHACL gate (ADR-0003), the `owlrl` closure
  test (ADR-0004), the `/rdf/` DCAT/VoID index entry, and the
  `worklode.io/ns/` base-URL override (spec 009 item 3). The sources this plan
  authors are the PR's content.
- **Issue/PR projection and `wl:mirrors`**: needs the Task↔GitHub-Issue mirror
  lifecycle (spec 004 Q5 / 008 Q008.4), still open.
- **Runtime-node projection** (Artifact/Deployment/Environment): spec 015
  types these and replaces 006's Artifact IRI pattern; ships with 015's plan.
- **Declared-layer authoring** (Component, DesignDoc, Deliverable, Effect,
  AcceptedDeviation instances and their crit-gated named graphs): spec 014
  reworks the document model; authoring surfaces belong to 014/008 plans. The
  *vocabulary* for all of it ships here.
- **Partial supersession**: 025 §3 retires `wl:supersededSection` for section
  IRIs, so 006's triple-term mechanism (acceptance criterion 5) is not built
  at all; sections land with 014. Consequence: no `ontology.1-2.ttl` is needed
  in this phase — the annotation predicate was 006's only RDF-1.2 content.
- **Drift queries** (spec 007) and Deliverable auto-confirmation (v2).

### Already implemented vs. remaining

Nothing graph-side exists yet — `grep -ri 'rdf\|sparql\|oxigraph'` matches only
`www/index.html`. What 006 calls the projection *sources* all shipped with the
backbone:

- The event outbox: `events` + `state_log` (`internal/store/events.go`,
  `deploy/base/migrations/0001_baseline.up.sql:3-11,172-180`). Task state
  transitions already write `state_log` rows via `Transition`
  (`internal/store/tasks.go:154-179`); creates and edge changes do not yet
  (fixed in Task 7, part 2).
- The relational task model: `tasks`, `task_edges` (`child_of`/`blocks`),
  `projects` (`internal/store/tasks.go`, `internal/store/projects.go`).
- Runtime ingest exists relationally (`internal/store/artifacts.go`,
  `internal/hooks/flux.go`) for later phases to project.

### Amendments honored, and gaps this plan closes

Spec 025 §17 says the `ls:`→`wl:` rename "must happen before spec 006 ships",
so everything here uses `wl:`/`wlc:`/`wlid:` under
`https://worklode.io/ns/`. Also honored: no `wl:Plan` (025 §2), no
`wl:supersededSection` (025 §3), status enum without `implemented` (025 §7),
six-value TaskKind including `spec` (014 §8, matching today's `tasks.kind`
CHECK plus the two values 014 adds). `wl:Section`, `wl:lastRevisedIn` and the
015 runtime terms ship with those specs' plans.

Three things 006 requires but never names — decided here, flagged for the spec:

1. **Projected literal predicates.** The projection table projects
   `concern`/`priority`/state, and the SHACL sketch requires "exactly one
   projected state literal", but none of the three predicates is in the mint
   set. This plan mints `wl:taskState` (functional), `wl:priority`
   (functional) and `wl:concern` as datatype properties, documented as
   projection-only mirrors that never fork the backbone enums (Open Q3).
2. **Workstream IRIs.** The IRI grammar has no workstream pattern. This plan
   fixes `id/workstream/<project-id>` for the instance and
   `https://worklode.io/ns/graph/workstream/<project-id>` for its
   projection named graph (following spec 007's `declared/…`, `observed/…`
   graph-family style).
3. **One workstream per task in v1.** The backbone gives a task exactly one
   `project_id` and no way to move it, so the projector writes each task to
   exactly one Workstream graph. The multi-workstream model (acceptance
   criterion 8) is real in the vocabulary and proven at the graph layer
   (Task 6); the projector grows multi-graph fan-out when the backbone grows
   multi-workstream membership.

---

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `internal/kg/iri/iri.go` | the canonical IRI grammar: namespaces + constructors, pure functions |
| `internal/kg/iri/iri_test.go` | table test over every pattern |
| `rdf/doc.go` | package stub so the vocabulary sources carry a Go test |
| `rdf/vocab_test.go` | mint-set presence + retired/forbidden-term checks over the `.ttl` files |
| `rdf/wl/ontology.ttl` | `wl:` classes and properties (RDF 1.1) |
| `rdf/wl/concept.ttl` | `wlc:` SKOS schemes (DesignDocStatus, TaskKind, ModelLayer) |
| `rdf/wl/wl-shapes.ttl` | SHACL node shapes (enforced later by rdf-registry CI) |
| `internal/graph/triple.go` | `Term`/`Triple`, literal escaping, `InsertData`/`ReplaceSubject` rendering |
| `internal/graph/triple_test.go` | escaping and exact update-string rendering |
| `internal/graph/task.go` | backbone `store.Task` + edges → subject-complete triples |
| `internal/graph/task_test.go` | mapping including kind concept, state literal, edge directions |
| `internal/graph/client.go` | SPARQL 1.1 protocol client (`Update`/`Select`/`Ask`) + GSP `Load`, oauth2 TokenSource |
| `internal/graph/client_test.go` | httptest: paths, content types, JSON parsing, bearer token, errors |
| `internal/graph/graphtest/graphtest.go` | Oxigraph test endpoint (skip-unless-CI, like `OpenTestStore`) + unique graphs |
| `internal/graph/oxigraph_test.go` | integration: vocabulary parse-loads; replace round-trip; two-graph task (criterion 8) |
| `deploy/base/migrations/0008_graph_projection.up.sql` | `graph_projection` checkpoint table |
| `deploy/base/migrations/0008_graph_projection.down.sql` | drop it |
| `internal/store/projection.go` | checkpoint get/set + `DirtyTaskIDs` over `state_log` |
| `internal/store/projection_test.go` | checkpoint round-trip, dirty scan, dedupe, limit |
| `internal/store/outbox_test.go` | create/edge mutations write `state_log` rows |
| `internal/projector/projector.go` | the poll loop: dirty tasks → per-subject replace → advance checkpoint |
| `internal/projector/projector_test.go` | real store + fake SPARQL endpoint; idempotence; edge fan-out; error retry |
| `internal/projector/e2e_oxigraph_test.go` | full slice vs. Oxigraph incl. `wl:dependsOn+` property path (criterion 10b) |

Migration id `0008` is provisional: ids are assigned sequentially at execution
time by the migration-id script, with `0008` the current next-free (0001–0005
on main; 0006/0007 claimed by in-flight worktrees).

**Modified files**

| Path | Change |
|---|---|
| `internal/store/tasks.go:96-148` | `CreateTask` gains `eventID` and writes a `state_log` row |
| `internal/store/tasks.go:402-506` | `AddEdge`/`RemoveEdge` gain `eventID` and log both endpoints |
| `internal/api/tasks.go:112-129` | pass `eventID` to `CreateTask` |
| `internal/api/tasks.go:385-388, 424-427` | pass `eventID` to `AddEdge`/`RemoveEdge` |
| `internal/cmd/serve.go` | `graphProjectorFromEnv` + background projection loop |
| `docker-compose.yml` | `oxigraph` service for local integration tests |
| `.github/workflows/_test.yml` | start Oxigraph, set `TEST_SPARQL_URL` |
| `README.md` | "Knowledge graph projection" section (env vars, compose service) |

**Test commands**

- Pure packages: `go test ./internal/kg/iri/... ./rdf/...`
- Graph package (unit tests run anywhere; integration needs Oxigraph):
  `docker compose up -d oxigraph && go test ./internal/graph/...`
- Postgres-backed (skip if unreachable outside CI):
  `docker compose up -d postgres && go test ./internal/store/... ./internal/api/... ./internal/projector/...`
- Everything: `docker compose up -d postgres oxigraph && go test ./...`

---

## Task 1: The IRI grammar package

`internal/kg/iri` is the single owner of the IRI grammar; the
platform-graph-design plan's Task 1 also creates this package, with
error-returning, validated constructors (`func(...) (string, error)`) rather
than the plain-string ones below. If that plan's Task 1 lands first: it
already defines `Component`, `Task`, `Doc`, `Deliverable`, `Issue`, `PR` and
`Environment` with that signature, so reconcile rather than redefine them —
add this task's `Term`, `Concept`, `Workstream`, `WorkstreamGraph` and
`Agent` to the existing package (matching its `(string, error)` signature),
and change every call site below of the seven overlapping names to match
`internal/kg/iri`'s existing versions instead of recreating `iri.go`.

**Files:**
- Create: `internal/kg/iri/iri.go`
- Test: `internal/kg/iri/iri_test.go`

- [ ] **Step 1: Write the failing test**

```go
package iri_test

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
)

func TestGrammar(t *testing.T) {
	const base = "https://worklode.io/ns/"
	cases := []struct{ name, got, want string }{
		{"term", iri.Term("Task"), base + "ontology#Task"},
		{"concept", iri.Concept("feature"), base + "concept/feature"},
		{"task", iri.Task("WL-7"), base + "id/task/WL-7"},
		{"workstream", iri.Workstream("worklode"), base + "id/workstream/worklode"},
		{"workstream graph", iri.WorkstreamGraph("worklode"), base + "graph/workstream/worklode"},
		{"component default slug", iri.Component("github.com/sunstoneinstitute/worklode"),
			base + "id/component/github.com/sunstoneinstitute/worklode"},
		{"doc", iri.Doc("spec-worklode-006"), base + "id/doc/spec-worklode-006"},
		{"deliverable", iri.Deliverable("worklode-graph-live"), base + "id/deliverable/worklode-graph-live"},
		{"issue", iri.Issue("github.com", "sunstoneinstitute", "worklode", 42),
			base + "id/issue/github.com/sunstoneinstitute/worklode/42"},
		{"pr", iri.PR("github.com", "sunstoneinstitute", "worklode", 42),
			base + "id/pr/github.com/sunstoneinstitute/worklode/42"},
		{"environment", iri.Environment("prod"), base + "id/environment/prod"},
		{"agent", iri.Agent("stig"), base + "id/agent/stig"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q; want %q", tc.got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/kg/iri/...`
Expected: FAIL — `no required module provides package .../internal/kg/iri`

- [ ] **Step 3: Write the implementation**

```go
// Package iri mints the canonical Worklode IRIs (spec 006 §Canonical IRI
// scheme, prefixes renamed per spec 025 §17). IRIs are branch-free and
// version-free; local ids are opaque and stable, and slashes inside a local
// id are permitted (slash namespace, opaque path).
//
// The Artifact pattern is deliberately absent: spec 006 §10.1 replaces 006's
// docker-only grammar with a kind-first one and ships with 015's plan.
package iri

import "fmt"

const (
	// Base is the published namespace root (spec 009 item 3).
	Base = "https://worklode.io/ns/"
	// Ontology is the wl: schema namespace (hash namespace).
	Ontology = Base + "ontology#"
	// ConceptNS is the wlc: SKOS concept namespace.
	ConceptNS = Base + "concept/"
	// IDNS is the wlid: instance namespace.
	IDNS = Base + "id/"
	// GraphNS holds named-graph IRIs, in the family style of spec 007's
	// declared/... and observed/... graphs.
	GraphNS = Base + "graph/"
)

// Term returns the IRI of a wl: class or property.
func Term(local string) string { return Ontology + local }

// Concept returns the IRI of a wlc: SKOS concept.
func Concept(local string) string { return ConceptNS + local }

// Task returns a backbone task's instance IRI (backbone id, e.g. WL-7).
func Task(id string) string { return IDNS + "task/" + id }

// Workstream returns the instance IRI of a backbone project acting as a
// Workstream. Spec 006 defines no workstream pattern; this package fixes
// id/workstream/<project-id>.
func Workstream(projectID string) string { return IDNS + "workstream/" + projectID }

// WorkstreamGraph returns the named graph a Workstream's projected quads
// live in (spec 006 §Projection, Open Q4: graphs are anchored per
// Workstream).
func WorkstreamGraph(projectID string) string { return GraphNS + "workstream/" + projectID }

// Component returns a component's instance IRI from its manifest slug
// (default = repo coordinates, D5).
func Component(slug string) string { return IDNS + "component/" + slug }

// Doc returns a design document's instance IRI from its design-file slug.
func Doc(slug string) string { return IDNS + "doc/" + slug }

// Deliverable returns a deliverable's instance IRI.
func Deliverable(slug string) string { return IDNS + "deliverable/" + slug }

// Issue returns a VCS issue's instance IRI.
func Issue(host, owner, repo string, number int64) string {
	return fmt.Sprintf("%sissue/%s/%s/%s/%d", IDNS, host, owner, repo, number)
}

// PR returns a pull request's instance IRI.
func PR(host, owner, repo string, number int64) string {
	return fmt.Sprintf("%spr/%s/%s/%s/%d", IDNS, host, owner, repo, number)
}

// Environment returns an environment's instance IRI (dev, prod).
func Environment(name string) string { return IDNS + "environment/" + name }

// Agent returns a backbone actor's instance IRI (a foaf:/prov: Agent).
func Agent(actorID string) string { return IDNS + "agent/" + actorID }
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/kg/iri/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/kg/iri
git commit -m "Add the canonical wl IRI grammar"
```

---

## Task 2: The vocabulary sources

**Files:**
- Create: `rdf/doc.go`, `rdf/wl/ontology.ttl`, `rdf/wl/concept.ttl`, `rdf/wl/wl-shapes.ttl`
- Test: `rdf/vocab_test.go`

- [ ] **Step 1: Write the failing test**

`rdf/doc.go`:

```go
// Package rdf holds the wl: vocabulary sources under rdf/wl/, staged for
// the rdf-registry PR (spec 006 acceptance criterion 1). The SHACL gate and
// owlrl closure proof run in rdf-registry CI; the tests here guard the mint
// set and the spec-014 renames, and internal/graph's Oxigraph test is the
// parse gate.
package rdf
```

`rdf/vocab_test.go`:

```go
package rdf

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// read returns a vocabulary source under rdf/wl/.
func read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("wl/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// mintedDeclarations is spec 006 acceptance criterion 2 as amended: minus
// wl:Plan and wl:supersededSection (014), minus the 014/015 additions that
// ship with those specs' plans, plus the three projected-literal predicates
// the spec's projection table needs but never names.
var mintedDeclarations = []string{
	"wl:Component a owl:Class",
	"wl:DesignDoc a owl:Class",
	"wl:ADR a owl:Class",
	"wl:Spec a owl:Class",
	"wl:Task a owl:Class",
	"wl:Deliverable a owl:Class",
	"wl:Effect a owl:Class",
	"wl:Workstream a owl:Class",
	"wl:Project a owl:Class",
	"wl:OngoingMaintenance a owl:Class",
	"wl:AcceptedDeviation a owl:Class",
	"wl:governs a owl:ObjectProperty",
	"wl:deliveredBy a owl:ObjectProperty",
	"wl:implements a owl:ObjectProperty",
	"wl:affects a owl:ObjectProperty",
	"wl:status a owl:ObjectProperty, owl:FunctionalProperty",
	"wl:sanctionedBy a owl:ObjectProperty",
	"wl:taskKind a owl:ObjectProperty, owl:FunctionalProperty",
	"wl:dependsOn a owl:ObjectProperty, owl:TransitiveProperty",
	"wl:blocks a owl:ObjectProperty, owl:TransitiveProperty",
	"wl:inWorkstream a owl:ObjectProperty",
	"wl:reviewer a owl:ObjectProperty",
	"wl:mirrors a owl:ObjectProperty, owl:SymmetricProperty",
	"wl:layer a owl:AnnotationProperty",
	"wl:taskState a owl:DatatypeProperty, owl:FunctionalProperty",
	"wl:priority a owl:DatatypeProperty, owl:FunctionalProperty",
	"wl:concern a owl:DatatypeProperty",
}

func TestOntologyMintSet(t *testing.T) {
	s := read(t, "ontology.ttl")
	for _, decl := range mintedDeclarations {
		if !strings.Contains(s, decl) {
			t.Errorf("ontology.ttl missing %q", decl)
		}
	}
	// Every minted term carries a model-layer tag (spec 006 acceptance
	// criterion 9). wl:layer itself is untagged, hence len-1.
	if n := strings.Count(s, "wl:layer wlc:"); n < len(mintedDeclarations)-1 {
		t.Errorf("wl:layer tags = %d; want >= %d (one per minted term)",
			n, len(mintedDeclarations)-1)
	}
	for _, axiom := range []string{
		"owl:members ( wl:Component wl:DesignDoc wl:Task wl:Deliverable wl:Workstream )",
		"owl:members ( wl:ADR wl:Spec )",
		"owl:members ( wl:Project wl:OngoingMaintenance )",
	} {
		if !strings.Contains(s, axiom) {
			t.Errorf("ontology.ttl missing disjointness axiom %q", axiom)
		}
	}
}

func TestConceptSchemes(t *testing.T) {
	s := read(t, "concept.ttl")
	for _, want := range []string{
		"wlc:DesignDocStatus a skos:ConceptScheme",
		"wlc:draft", "wlc:proposed", "wlc:accepted", "wlc:superseded",
		// 025 §7: implemented left the enum, so the ordered list has 4 members.
		"skos:memberList ( wlc:draft wlc:proposed wlc:accepted wlc:superseded )",
		"wlc:TaskKind a skos:ConceptScheme",
		"wlc:feature", "wlc:bug", "wlc:chore", "wlc:spec", "wlc:review", "wlc:spike",
		"wlc:ModelLayer a skos:ConceptScheme",
		"wlc:intent", "wlc:execution", "wlc:runtime",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("concept.ttl missing %q", want)
		}
	}
}

func TestNoRetiredOrForbiddenTerms(t *testing.T) {
	oldPrefix := regexp.MustCompile(`(^|[^a-zA-Z])ls(c|id)?:`)
	for _, name := range []string{"ontology.ttl", "concept.ttl", "wl-shapes.ttl"} {
		s := read(t, name)
		if oldPrefix.MatchString(s) {
			t.Errorf("%s still uses an old prefix (renamed by spec 014)", name)
		}
		for _, bad := range []string{"gtio", "wl:Plan", "wl:supersededSection", "wlc:implemented"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s contains retired/forbidden term %q", name, bad)
			}
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./rdf/...`
Expected: FAIL — `read wl/ontology.ttl: ... no such file or directory`

- [ ] **Step 3: Write `rdf/wl/ontology.ttl`**

```turtle
@prefix wl:   <https://worklode.io/ns/ontology#> .
@prefix wlc:  <https://worklode.io/ns/concept/> .
@prefix dct:  <http://purl.org/dc/terms/> .
@prefix foaf: <http://xmlns.com/foaf/0.1/> .
@prefix skos: <http://www.w3.org/2004/02/skos/core#> .
@prefix rdfs: <http://www.w3.org/2000/01/rdf-schema#> .
@prefix owl:  <http://www.w3.org/2002/07/owl#> .
@prefix xsd:  <http://www.w3.org/2001/XMLSchema#> .

<https://worklode.io/ns/ontology> a owl:Ontology ;
    dct:title "Worklode wl: ontology" ;
    rdfs:comment "The Worklode knowledge-graph vocabulary (spec 006, prefixes per spec 014). Standards-first: dcterms/foaf/prov/doap/skos are reused; only the terms below are minted." .

# ---- Model-layer annotation (tags every minted term; itself untagged) ----
wl:layer a owl:AnnotationProperty ;
    rdfs:range skos:Concept ;
    rdfs:comment "Model-layer membership (wlc:ModelLayer: intent/execution/runtime) of a vocabulary term. Annotation only." .

# ---- Classes ----
wl:Component a owl:Class ; wl:layer wlc:intent ;
    rdfs:comment "A software component - the atomic unit of the platform graph. Repo/project (doap:Project) is a coarser grouping via dct:hasPart." .

wl:DesignDoc a owl:Class ; rdfs:subClassOf foaf:Document ; wl:layer wlc:intent ;
    rdfs:comment "A design document; authored graph-side, crit-reviewed, never projected." .
wl:ADR a owl:Class ; rdfs:subClassOf wl:DesignDoc ; wl:layer wlc:intent .
wl:Spec a owl:Class ; rdfs:subClassOf wl:DesignDoc ; wl:layer wlc:intent .

wl:Task a owl:Class ; wl:layer wlc:execution ;
    rdfs:comment "Execution-owned; projected read-only from the Worklode backbone (D11)." .

wl:Deliverable a owl:Class ; wl:layer wlc:intent ;
    rdfs:comment "Declared definition-of-done (D7): the vertical join point where intent meets a running system. Declared only in v1; auto-confirmation is v2 (spec 007)." .

wl:Effect a owl:Class ; rdfs:subClassOf wl:Deliverable ; wl:layer wlc:intent ;
    rdfs:comment "A deliverable whose definition-of-done is a state of an existing system, not the existence and placement of an artifact. Declares a Deployment target and MUST NOT declare an Artifact; witnessed by that Deployment over a Commit on the delivering component's default branch (spec 015)." .

wl:Workstream a owl:Class ; wl:layer wlc:execution ;
    rdfs:comment "A grouping of work a Task belongs to. Projection named graphs are anchored per Workstream; a Task in several Workstreams appears in several graphs." .
wl:Project a owl:Class ; rdfs:subClassOf wl:Workstream ; wl:layer wlc:execution ;
    rdfs:comment "Bounded, goal-oriented workstream. Distinct from doap:Project (= repo)." .
wl:OngoingMaintenance a owl:Class ; rdfs:subClassOf wl:Workstream ; wl:layer wlc:execution ;
    rdfs:comment "Unbounded, continuous workstream." .

wl:AcceptedDeviation a owl:Class ; wl:layer wlc:intent ;
    rdfs:comment "A tolerated architectural deviation that drift queries suppress. Names the accepted edge via RDF reification (rdf:subject/predicate/object) WITHOUT asserting it - the edge stays out of the intent layer." .

# ---- Disjointness (proved by owlrl in rdf-registry CI; closure never published, ADR-0004) ----
[] a owl:AllDisjointClasses ;
   owl:members ( wl:Component wl:DesignDoc wl:Task wl:Deliverable wl:Workstream ) .
[] a owl:AllDisjointClasses ; owl:members ( wl:ADR wl:Spec ) .
[] a owl:AllDisjointClasses ; owl:members ( wl:Project wl:OngoingMaintenance ) .

# ---- Object properties ----
wl:governs a owl:ObjectProperty ; wl:layer wlc:intent ;
    rdfs:domain wl:DesignDoc ; rdfs:range wl:Component ;
    rdfs:comment "This design doc governs the architecture of that component." .

wl:deliveredBy a owl:ObjectProperty ; wl:layer wlc:intent ;
    rdfs:domain wl:Deliverable ; rdfs:range wl:Component ;
    rdfs:comment "The component that delivers this deliverable. Deliberately NOT functional. Declared rather than derived: the derivable route needs wl:Build, which has no v1 projection source (spec 015). SHACL enforces >= 1." .

wl:implements a owl:ObjectProperty ; wl:layer wlc:execution ;
    rdfs:range [ a owl:Class ; owl:unionOf ( wl:DesignDoc wl:Deliverable wl:Component ) ] ;
    rdfs:comment "Execution realises intent. Declared when authored; observed when derived (spec 007). Spec 014 narrows the DesignDoc half to Component-to-Section manifest claims." .

wl:affects a owl:ObjectProperty ; wl:layer wlc:execution ;
    rdfs:range wl:Component ;
    rdfs:comment "A Task/Issue/PullRequest touches/changes that component." .

wl:status a owl:ObjectProperty, owl:FunctionalProperty ; wl:layer wlc:intent ;
    rdfs:domain wl:DesignDoc ; rdfs:range skos:Concept ;
    rdfs:comment "Lifecycle status in wlc:DesignDocStatus (D4). Functional catches more than one; SHACL enforces at least one." .

wl:sanctionedBy a owl:ObjectProperty ; wl:layer wlc:intent ;
    rdfs:domain wl:AcceptedDeviation ; rdfs:range wl:DesignDoc ;
    rdfs:comment "The DesignDoc/ADR that authorises this accepted deviation." .

wl:taskKind a owl:ObjectProperty, owl:FunctionalProperty ; wl:layer wlc:execution ;
    rdfs:domain wl:Task ; rdfs:range skos:Concept ;
    rdfs:comment "Kind of task; see wlc:TaskKind. Backbone-projected." .

wl:dependsOn a owl:ObjectProperty, owl:TransitiveProperty ; wl:layer wlc:execution ;
    rdfs:subPropertyOf dct:requires ;
    rdfs:domain wl:Task ; rdfs:range wl:Task ;
    rdfs:comment "This task needs that task done first (backbone dependency mirror). Transitive: reachability is a SPARQL property path, no reasoner needed." .

wl:blocks a owl:ObjectProperty, owl:TransitiveProperty ; wl:layer wlc:execution ;
    owl:inverseOf wl:dependsOn ;
    rdfs:domain wl:Task ; rdfs:range wl:Task ;
    rdfs:comment "This task blocks that task (backbone-authoritative, spec 004). Transitive." .

wl:inWorkstream a owl:ObjectProperty ; wl:layer wlc:execution ;
    rdfs:domain wl:Task ; rdfs:range wl:Workstream ;
    rdfs:comment "Membership of a Task in a Workstream. Split from dct:isPartOf so the Task-to-Task decomposition stays type-homogeneous and cleanly transitive." .

wl:reviewer a owl:ObjectProperty ; wl:layer wlc:intent ;
    rdfs:domain wl:Component ; rdfs:range foaf:Agent ;
    rdfs:comment "Agent (GitHub user/team IRI) to notify about PRs affecting this component." .

wl:mirrors a owl:ObjectProperty, owl:SymmetricProperty ; wl:layer wlc:execution ;
    rdfs:comment "Bidirectional mirror between a backbone Task and a GitHub Issue. The PR-to-Task join piggybacks GitHub's native Closes #N." .

# ---- Projected literal mirrors (backbone-owned enums; the graph never forks them, Open Q3) ----
wl:taskState a owl:DatatypeProperty, owl:FunctionalProperty ; wl:layer wlc:execution ;
    rdfs:domain wl:Task ; rdfs:range xsd:string ;
    rdfs:comment "Projected literal mirror of the backbone task state machine (spec 004). Deliberately not wl:status." .

wl:priority a owl:DatatypeProperty, owl:FunctionalProperty ; wl:layer wlc:execution ;
    rdfs:domain wl:Task ; rdfs:range xsd:string ;
    rdfs:comment "Projected mirror of the backbone priority enum (spec 005)." .

wl:concern a owl:DatatypeProperty ; wl:layer wlc:execution ;
    rdfs:domain wl:Task ; rdfs:range xsd:string ;
    rdfs:comment "Projected mirror of the backbone concern enum (spec 005); absent when the task has none." .
```

- [ ] **Step 4: Write `rdf/wl/concept.ttl`**

```turtle
@prefix wlc:  <https://worklode.io/ns/concept/> .
@prefix skos: <http://www.w3.org/2004/02/skos/core#> .

# DesignDoc lifecycle (D4). Order draft -> proposed -> accepted -> superseded;
# spec 014 par.5 removed the implemented terminal (implementation is a derived
# coverage query, never a status).
wlc:DesignDocStatus a skos:ConceptScheme ; skos:prefLabel "DesignDoc lifecycle" .
wlc:draft a skos:Concept ; skos:inScheme wlc:DesignDocStatus ; skos:prefLabel "draft" ;
    skos:definition "Being written; not yet proposed for review." .
wlc:proposed a skos:Concept ; skos:inScheme wlc:DesignDocStatus ; skos:prefLabel "proposed" ;
    skos:definition "Submitted for crit review; awaiting resolution." .
wlc:accepted a skos:Concept ; skos:inScheme wlc:DesignDocStatus ; skos:prefLabel "accepted" ;
    skos:definition "Crit-resolved and approved as intent." .
wlc:superseded a skos:Concept ; skos:inScheme wlc:DesignDocStatus ; skos:prefLabel "superseded" ;
    skos:definition "Replaced (whole or in part) by a later doc via dct:replaces." .

# Lifecycle order as data (queryable), not prose:
wlc:DesignDocStatusOrder a skos:OrderedCollection ;
    skos:memberList ( wlc:draft wlc:proposed wlc:accepted wlc:superseded ) .

# Task kind (spec 014 par.8: exactly these six, matching tasks.kind).
wlc:TaskKind a skos:ConceptScheme ; skos:prefLabel "Task kind" .
wlc:feature a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "feature" ;
    skos:definition "New capability or behaviour." .
wlc:bug a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "bug" ;
    skos:definition "Fix incorrect existing behaviour." .
wlc:chore a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "chore" ;
    skos:definition "Maintenance with no behaviour change (deps, tooling, cleanup)." .
wlc:spec a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "spec" ;
    skos:definition "Write or revise a design document." .
wlc:review a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "review" ;
    skos:definition "Review/evaluate someone else's work." .
wlc:spike a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "spike" ;
    skos:definition "Time-boxed experiment to validate an approach; throwaway output." .

# Model layer.
wlc:ModelLayer a skos:ConceptScheme ; skos:prefLabel "Model layer" .
wlc:intent a skos:Concept ; skos:inScheme wlc:ModelLayer ; skos:prefLabel "intent" ;
    skos:definition "Declared design layer - what should be true." .
wlc:execution a skos:Concept ; skos:inScheme wlc:ModelLayer ; skos:prefLabel "execution" ;
    skos:definition "Observed execution/VCS layer - tasks, issues, PRs." .
wlc:runtime a skos:Concept ; skos:inScheme wlc:ModelLayer ; skos:prefLabel "runtime" ;
    skos:definition "Observed runtime/deploy layer - artifacts, deployments, environments." .
```

- [ ] **Step 5: Write `rdf/wl/wl-shapes.ttl`**

```turtle
@prefix sh:   <http://www.w3.org/ns/shacl#> .
@prefix wl:   <https://worklode.io/ns/ontology#> .
@prefix wlsh: <https://worklode.io/ns/shapes#> .
@prefix dct:  <http://purl.org/dc/terms/> .
@prefix rdf:  <http://www.w3.org/1999/02/22-rdf-syntax-ns#> .
@prefix skos: <http://www.w3.org/2004/02/skos/core#> .
@prefix xsd:  <http://www.w3.org/2001/XMLSchema#> .

# Closed-world constraints (required fields, cardinality). OWL cannot flag a
# missing field or a duplicate; these shapes catch 0 and the Functional
# axioms catch >1. Enforced by the rdf-registry SHACL gate (ADR-0003).

wlsh:TaskShape a sh:NodeShape ; sh:targetClass wl:Task ;
    sh:property [ sh:path wl:taskKind ; sh:minCount 1 ; sh:maxCount 1 ; sh:class skos:Concept ] ;
    sh:property [ sh:path wl:taskState ; sh:minCount 1 ; sh:maxCount 1 ; sh:datatype xsd:string ] ;
    sh:property [ sh:path wl:inWorkstream ; sh:minCount 1 ] .

wlsh:ComponentShape a sh:NodeShape ; sh:targetClass wl:Component ;
    sh:property [ sh:path wl:reviewer ; sh:minCount 1 ] .

wlsh:DeliverableShape a sh:NodeShape ; sh:targetClass wl:Deliverable ;
    sh:property [ sh:path dct:description ; sh:minCount 1 ] ;
    sh:property [ sh:path dct:relation ; sh:minCount 1 ] ;
    sh:property [ sh:path wl:deliveredBy ; sh:minCount 1 ] .

# wl:Effect inherits DeliverableShape via its class. The Effect-specific
# constraints (a Deployment target, zero Artifact targets) need the
# wl:Deployment/wl:Artifact classes and land with spec 015's plan.

wlsh:AcceptedDeviationShape a sh:NodeShape ; sh:targetClass wl:AcceptedDeviation ;
    sh:property [ sh:path rdf:subject ; sh:minCount 1 ; sh:maxCount 1 ] ;
    sh:property [ sh:path rdf:predicate ; sh:minCount 1 ; sh:maxCount 1 ] ;
    sh:property [ sh:path rdf:object ; sh:minCount 1 ; sh:maxCount 1 ] ;
    sh:property [ sh:path wl:sanctionedBy ; sh:minCount 1 ] .

wlsh:DesignDocShape a sh:NodeShape ; sh:targetClass wl:DesignDoc ;
    sh:property [ sh:path wl:status ; sh:minCount 1 ; sh:maxCount 1 ; sh:class skos:Concept ] .
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./rdf/...`
Expected: PASS (3 tests)

- [ ] **Step 7: Commit**

```bash
git add rdf
git commit -m "Author the wl vocabulary sources for the rdf-registry PR"
```

---

## Task 3: Triples and SPARQL Update rendering

Per-subject `ReplaceSubject` (not a whole-graph PUT) is required because a
Workstream graph holds many tasks; this depends on graph-server's SPARQL
Update surface alongside GSP.

**Files:**
- Create: `internal/graph/triple.go`
- Test: `internal/graph/triple_test.go`

- [ ] **Step 1: Write the failing test**

```go
package graph

import "testing"

func TestTermRendering(t *testing.T) {
	cases := []struct {
		name string
		term Term
		want string
	}{
		{"iri", IRIRef("https://worklode.io/ns/id/task/WL-1"),
			"<https://worklode.io/ns/id/task/WL-1>"},
		{"plain literal", Text("fix login"), `"fix login"`},
		{"quote escaped", Text(`say "hi"`), `"say \"hi\""`},
		{"backslash escaped", Text(`a\b`), `"a\\b"`},
		{"newline escaped", Text("a\nb"), `"a\nb"`},
		{"typed literal", Typed("2026-07-30T00:00:00Z", "http://www.w3.org/2001/XMLSchema#dateTime"),
			`"2026-07-30T00:00:00Z"^^<http://www.w3.org/2001/XMLSchema#dateTime>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.term.String(); got != tc.want {
				t.Fatalf("got %s; want %s", got, tc.want)
			}
		})
	}
}

func TestReplaceSubject(t *testing.T) {
	triples := []Triple{
		{S: "urn:s", P: "urn:p", O: IRIRef("urn:o")},
		{S: "urn:s", P: "urn:q", O: Text("v")},
	}
	got := ReplaceSubject("urn:g", "urn:s", triples)
	want := "DELETE WHERE { GRAPH <urn:g> { <urn:s> ?p ?o } } ;\n" +
		"INSERT DATA { GRAPH <urn:g> {\n" +
		"  <urn:s> <urn:p> <urn:o> .\n" +
		"  <urn:s> <urn:q> \"v\" .\n" +
		"} }"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestReplaceSubjectEmptyDeletesOnly(t *testing.T) {
	got := ReplaceSubject("urn:g", "urn:s", nil)
	want := "DELETE WHERE { GRAPH <urn:g> { <urn:s> ?p ?o } }"
	if got != want {
		t.Fatalf("got %q; want %q", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/graph/...`
Expected: FAIL — `no required module provides package .../internal/graph`

- [ ] **Step 3: Write the implementation**

```go
// Package graph maps backbone rows to RDF triples and talks to the
// knowledge-graph SPARQL endpoint (spec 006 §Projection; spec 009 items
// 2 and 4). It renders per-subject-replace updates: DELETE all quads of a
// subject in one named graph, then INSERT the fresh projection - idempotent
// per (subject, graph).
package graph

import (
	"fmt"
	"strings"
)

// Term is a triple object: an IRI reference or a literal.
type Term struct {
	text  string
	dtype string
	isIRI bool
}

// IRIRef returns an IRI object term.
func IRIRef(iri string) Term { return Term{text: iri, isIRI: true} }

// Text returns a plain string literal term.
func Text(s string) Term { return Term{text: s} }

// Typed returns a literal term with a datatype IRI.
func Typed(s, datatypeIRI string) Term { return Term{text: s, dtype: datatypeIRI} }

// literalEscaper covers the SPARQL string escape set that can occur in task
// titles and bodies.
var literalEscaper = strings.NewReplacer(
	`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`,
)

func (t Term) String() string {
	if t.isIRI {
		return "<" + t.text + ">"
	}
	lit := `"` + literalEscaper.Replace(t.text) + `"`
	if t.dtype != "" {
		lit += "^^<" + t.dtype + ">"
	}
	return lit
}

// Triple is one statement. S and P are IRIs (never literals).
type Triple struct {
	S string
	P string
	O Term
}

// InsertData renders an INSERT DATA update putting the triples into the
// named graph.
func InsertData(graphIRI string, triples []Triple) string {
	var b strings.Builder
	fmt.Fprintf(&b, "INSERT DATA { GRAPH <%s> {\n", graphIRI)
	for _, tr := range triples {
		fmt.Fprintf(&b, "  <%s> <%s> %s .\n", tr.S, tr.P, tr.O)
	}
	b.WriteString("} }")
	return b.String()
}

// ReplaceSubject renders one SPARQL 1.1 update that atomically replaces
// every triple with the given subject in the given named graph by the given
// triples (spec 006 §Projection: per-subject replace). With no triples it
// renders only the delete.
func ReplaceSubject(graphIRI, subjectIRI string, triples []Triple) string {
	del := fmt.Sprintf("DELETE WHERE { GRAPH <%s> { <%s> ?p ?o } }", graphIRI, subjectIRI)
	if len(triples) == 0 {
		return del
	}
	return del + " ;\n" + InsertData(graphIRI, triples)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/graph/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/graph
git commit -m "Render triples and per-subject-replace SPARQL updates"
```

---

## Task 4: Task row → triples

**Files:**
- Create: `internal/graph/task.go`
- Test: `internal/graph/task_test.go`

- [ ] **Step 1: Write the failing test**

```go
package graph

import (
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestTaskTriples(t *testing.T) {
	created := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	task := store.Task{
		ID: "WL-7", ProjectID: "worklode", Title: "wire the projector",
		Priority: "high", Kind: "feature", State: "in_progress",
		Concern: "completeness", CreatedBy: "stig",
		CreatedAt: created, UpdatedAt: created.Add(time.Hour),
	}
	out := []store.Edge{
		{FromTask: "WL-7", ToTask: "WL-2", Type: "child_of"},
		{FromTask: "WL-7", ToTask: "WL-9", Type: "blocks"},
	}
	in := []store.Edge{
		{FromTask: "WL-3", ToTask: "WL-7", Type: "blocks"},
		{FromTask: "WL-8", ToTask: "WL-7", Type: "child_of"}, // a child; no triple here
	}

	got := map[string]bool{}
	for _, tr := range TaskTriples(task, out, in) {
		if tr.S != iri.Task("WL-7") {
			t.Errorf("subject %q; TaskTriples must be subject-complete for the task", tr.S)
		}
		got[tr.P+" "+tr.O.String()] = true
	}

	want := []string{
		RDFType + " <" + iri.Term("Task") + ">",
		DCTTitle + ` "wire the projector"`,
		iri.Term("taskState") + ` "in_progress"`,
		iri.Term("taskKind") + " <" + iri.Concept("feature") + ">",
		iri.Term("priority") + ` "high"`,
		iri.Term("concern") + ` "completeness"`,
		iri.Term("inWorkstream") + " <" + iri.Workstream("worklode") + ">",
		ProvWasAttributedTo + " <" + iri.Agent("stig") + ">",
		DCTCreated + ` "2026-07-30T10:00:00Z"^^<` + XSDDateTime + ">",
		DCTModified + ` "2026-07-30T11:00:00Z"^^<` + XSDDateTime + ">",
		DCTIsPartOf + " <" + iri.Task("WL-2") + ">",
		iri.Term("blocks") + " <" + iri.Task("WL-9") + ">",
		iri.Term("dependsOn") + " <" + iri.Task("WL-3") + ">",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing triple %s", w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d distinct triples; want %d", len(got), len(want))
	}
}

func TestTaskTriplesOmitsEmptyOptionals(t *testing.T) {
	task := store.Task{
		ID: "WL-1", ProjectID: "p", Title: "t", Priority: "low",
		Kind: "chore", State: "ready",
	}
	for _, tr := range TaskTriples(task, nil, nil) {
		if tr.P == iri.Term("concern") || tr.P == ProvWasAttributedTo {
			t.Errorf("unexpected optional triple %s %s", tr.P, tr.O)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/graph/ -run TestTaskTriples`
Expected: FAIL — `undefined: TaskTriples` (and the vocabulary constants)

- [ ] **Step 3: Write the implementation**

```go
package graph

import (
	"time"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// Reused vocabulary IRIs (spec 006 reuse-vs-mint table).
const (
	RDFType             = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
	DCTTitle            = "http://purl.org/dc/terms/title"
	DCTCreated          = "http://purl.org/dc/terms/created"
	DCTModified         = "http://purl.org/dc/terms/modified"
	DCTIsPartOf         = "http://purl.org/dc/terms/isPartOf"
	ProvWasAttributedTo = "http://www.w3.org/ns/prov#wasAttributedTo"
	XSDDateTime         = "http://www.w3.org/2001/XMLSchema#dateTime"
)

// TaskTriples maps one backbone task row plus its edges to the task's
// triples (spec 006 §Projection table). It is subject-complete: every
// triple whose subject is the task IRI and no others, so ReplaceSubject
// over the result is a faithful re-projection.
//
// Directions: "A blocks B" is stored as from=A, to=B (store.Edge), so an
// out-edge emits wl:blocks and an in-edge emits wl:dependsOn. "A child_of
// B" emits dct:isPartOf on the child only; the parent's subtree needs no
// triple with the parent as subject.
func TaskTriples(t store.Task, out, in []store.Edge) []Triple {
	s := iri.Task(t.ID)
	ts := []Triple{
		{S: s, P: RDFType, O: IRIRef(iri.Term("Task"))},
		{S: s, P: DCTTitle, O: Text(t.Title)},
		{S: s, P: iri.Term("taskState"), O: Text(t.State)},
		{S: s, P: iri.Term("taskKind"), O: IRIRef(iri.Concept(t.Kind))},
		{S: s, P: iri.Term("priority"), O: Text(t.Priority)},
		{S: s, P: iri.Term("inWorkstream"), O: IRIRef(iri.Workstream(t.ProjectID))},
		{S: s, P: DCTCreated, O: Typed(t.CreatedAt.UTC().Format(time.RFC3339), XSDDateTime)},
		{S: s, P: DCTModified, O: Typed(t.UpdatedAt.UTC().Format(time.RFC3339), XSDDateTime)},
	}
	if t.Concern != "" {
		ts = append(ts, Triple{S: s, P: iri.Term("concern"), O: Text(t.Concern)})
	}
	if t.CreatedBy != "" {
		ts = append(ts, Triple{S: s, P: ProvWasAttributedTo, O: IRIRef(iri.Agent(t.CreatedBy))})
	}
	for _, e := range out {
		switch e.Type {
		case "child_of":
			ts = append(ts, Triple{S: s, P: DCTIsPartOf, O: IRIRef(iri.Task(e.ToTask))})
		case "blocks":
			ts = append(ts, Triple{S: s, P: iri.Term("blocks"), O: IRIRef(iri.Task(e.ToTask))})
		}
	}
	for _, e := range in {
		if e.Type == "blocks" {
			ts = append(ts, Triple{S: s, P: iri.Term("dependsOn"), O: IRIRef(iri.Task(e.FromTask))})
		}
	}
	return ts
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/graph/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/graph
git commit -m "Map a backbone task row to its graph triples"
```

---

## Task 5: The SPARQL client

**Files:**
- Create: `internal/graph/client.go`
- Test: `internal/graph/client_test.go`

- [ ] **Step 1: Write the failing test**

```go
package graph_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"

	"github.com/sunstoneinstitute/worklode/internal/graph"
)

// record captures one request.
type record struct {
	path, query, contentType, accept, body string
}

// recordingServer answers every request with status and respBody.
func recordingServer(t *testing.T, status int, respBody string) (*httptest.Server, *record) {
	t.Helper()
	rec := &record{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*rec = record{
			path: r.URL.Path, query: r.URL.RawQuery,
			contentType: r.Header.Get("Content-Type"),
			accept:      r.Header.Get("Accept"),
			body:        string(body),
		}
		w.WriteHeader(status)
		io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func TestUpdate(t *testing.T) {
	srv, rec := recordingServer(t, http.StatusNoContent, "")
	c := graph.NewClient(srv.URL, nil)
	if err := c.Update(context.Background(), "INSERT DATA {}"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.path != "/update" || rec.contentType != "application/sparql-update" ||
		rec.body != "INSERT DATA {}" {
		t.Fatalf("request = %+v; want POST /update with the raw update", rec)
	}
}

func TestSelect(t *testing.T) {
	srv, rec := recordingServer(t, http.StatusOK, `{
		"head": {"vars": ["s"]},
		"results": {"bindings": [
			{"s": {"type": "uri", "value": "urn:a"}},
			{"s": {"type": "literal", "value": "ready"}}
		]}
	}`)
	c := graph.NewClient(srv.URL, nil)
	rows, err := c.Select(context.Background(), "SELECT ?s WHERE {}")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if rec.path != "/query" || rec.contentType != "application/sparql-query" ||
		rec.accept != "application/sparql-results+json" {
		t.Fatalf("request = %+v; want POST /query asking for JSON results", rec)
	}
	if len(rows) != 2 || rows[0]["s"] != "urn:a" || rows[1]["s"] != "ready" {
		t.Fatalf("rows = %v; want urn:a and ready", rows)
	}
}

func TestAsk(t *testing.T) {
	srv, _ := recordingServer(t, http.StatusOK, `{"head": {}, "boolean": true}`)
	c := graph.NewClient(srv.URL, nil)
	ok, err := c.Ask(context.Background(), "ASK {}")
	if err != nil || !ok {
		t.Fatalf("Ask = %v, %v; want true, nil", ok, err)
	}
}

func TestLoad(t *testing.T) {
	srv, rec := recordingServer(t, http.StatusNoContent, "")
	c := graph.NewClient(srv.URL, nil)
	if err := c.Load(context.Background(), "urn:g", []byte("@prefix : <urn:x> .")); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.path != "/store" || rec.query != "graph=urn%3Ag" || rec.contentType != "text/turtle" {
		t.Fatalf("request = %+v; want POST /store?graph=urn%%3Ag as text/turtle", rec)
	}
}

func TestErrorStatusSurfacesBody(t *testing.T) {
	srv, _ := recordingServer(t, http.StatusBadRequest, "parse error at line 3")
	c := graph.NewClient(srv.URL, nil)
	err := c.Update(context.Background(), "NOT SPARQL")
	if err == nil {
		t.Fatal("Update on a 400 returned nil error")
	}
	if got := err.Error(); !strings.Contains(got, "parse error at line 3") {
		t.Fatalf("error %q does not carry the endpoint's body", got)
	}
}

func TestBearerToken(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	c := graph.NewClient(srv.URL, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok"}))
	if err := c.Update(context.Background(), "INSERT DATA {}"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if auth != "Bearer tok" {
		t.Fatalf("Authorization = %q; want Bearer tok", auth)
	}
}
```

Add `"strings"` to the test file's import block.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/graph/ -run 'TestUpdate|TestSelect|TestAsk|TestLoad|TestError|TestBearer'`
Expected: FAIL — `undefined: graph.NewClient`

- [ ] **Step 3: Write the implementation**

```go
package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
)

// Client speaks the SPARQL 1.1 Protocol (query + update) plus the Graph
// Store Protocol POST used to load Turtle documents. It targets Oxigraph in
// tests and the data-platform graph-server in prod (spec 009 items 2 and
// 4); both expose /query, /update and /store relative to the base URL.
type Client struct {
	base string
	http *http.Client
}

// NewClient returns a client for the SPARQL endpoint at base. A nil
// TokenSource means unauthenticated (dev Oxigraph); prod passes a Keycloak
// client-credentials source (dataplatform-svc, spec 009 item 4).
func NewClient(base string, src oauth2.TokenSource) *Client {
	hc := &http.Client{}
	if src != nil {
		hc = oauth2.NewClient(context.Background(), src)
	}
	return &Client{base: strings.TrimRight(base, "/"), http: hc}
}

func (c *Client) post(ctx context.Context, path, contentType, accept string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("POST %s: %s: %s", path, resp.Status, bytes.TrimSpace(data))
	}
	return data, nil
}

// Update executes a SPARQL 1.1 update (possibly multiple ;-separated
// operations, which the endpoint applies as one transaction).
func (c *Client) Update(ctx context.Context, update string) error {
	_, err := c.post(ctx, "/update", "application/sparql-update", "", []byte(update))
	return err
}

// sparqlResults is the application/sparql-results+json envelope.
type sparqlResults struct {
	Boolean *bool `json:"boolean"`
	Results struct {
		Bindings []map[string]struct {
			Value string `json:"value"`
		} `json:"bindings"`
	} `json:"results"`
}

func (c *Client) query(ctx context.Context, q string) (*sparqlResults, error) {
	data, err := c.post(ctx, "/query", "application/sparql-query",
		"application/sparql-results+json", []byte(q))
	if err != nil {
		return nil, err
	}
	var res sparqlResults
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("decode sparql results: %w", err)
	}
	return &res, nil
}

// Select runs a SELECT query, flattening each solution to variable → value.
func (c *Client) Select(ctx context.Context, q string) ([]map[string]string, error) {
	res, err := c.query(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]string, 0, len(res.Results.Bindings))
	for _, b := range res.Results.Bindings {
		row := make(map[string]string, len(b))
		for k, v := range b {
			row[k] = v.Value
		}
		out = append(out, row)
	}
	return out, nil
}

// Ask runs an ASK query.
func (c *Client) Ask(ctx context.Context, q string) (bool, error) {
	res, err := c.query(ctx, q)
	if err != nil {
		return false, err
	}
	if res.Boolean == nil {
		return false, fmt.Errorf("ASK response carries no boolean")
	}
	return *res.Boolean, nil
}

// Load POSTs a Turtle document into a named graph via the Graph Store
// Protocol. Tests use it as the parse gate for the rdf/wl sources.
func (c *Client) Load(ctx context.Context, graphIRI string, turtle []byte) error {
	_, err := c.post(ctx, "/store?graph="+url.QueryEscape(graphIRI), "text/turtle", "", turtle)
	return err
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/graph/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/graph
git commit -m "Add the SPARQL protocol client"
```

---

## Task 6: Oxigraph harness and graph-layer integration tests

**Files:**
- Modify: `docker-compose.yml` (new service)
- Modify: `.github/workflows/_test.yml:48-53`
- Create: `internal/graph/graphtest/graphtest.go`
- Test: `internal/graph/oxigraph_test.go`

- [ ] **Step 1: Add Oxigraph to docker-compose**

Append to the `services:` map in `docker-compose.yml`:

```yaml
  # SPARQL endpoint for knowledge-graph integration tests (stand-in for the
  # data-platform graph-server; both speak SPARQL 1.1 protocol + GSP).
  oxigraph:
    image: ghcr.io/oxigraph/oxigraph:latest
    command: ["serve", "--location", "/data", "--bind", "0.0.0.0:7878"]
    ports:
      - "127.0.0.1:7878:7878"
    volumes:
      - ./data/oxigraph:/data
    restart: unless-stopped
```

- [ ] **Step 2: Start Oxigraph in CI**

In `.github/workflows/_test.yml`, insert a step before `go build` (after the
cache-restore step) and add the env var to the `go test` step:

```yaml
      - name: Start Oxigraph
        run: |
          docker run -d --name oxigraph -p 7878:7878 \
            ghcr.io/oxigraph/oxigraph:latest \
            serve --location /data --bind 0.0.0.0:7878
```

and in the existing `go test` step's `env:` block:

```yaml
          TEST_SPARQL_URL: http://localhost:7878
```

(A `docker run` step rather than a `services:` entry because service
containers cannot override the image command, and the Oxigraph image needs
`serve …` as its command.)

- [ ] **Step 3: Write the graphtest helper**

`internal/graph/graphtest/graphtest.go`:

```go
// Package graphtest connects integration tests to the SPARQL endpoint the
// docker-compose oxigraph service exposes, mirroring store.OpenTestStore's
// skip-unless-CI contract.
package graphtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graph"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
)

// Endpoint returns the SPARQL base URL tests run against. Default matches
// the docker-compose oxigraph service. Skips the test if the endpoint is
// unreachable and CI is not set.
func Endpoint(t *testing.T) string {
	t.Helper()
	base := os.Getenv("TEST_SPARQL_URL")
	if base == "" {
		base = "http://localhost:7878"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := graph.NewClient(base, nil).Ask(ctx, "ASK {}"); err != nil {
		if os.Getenv("CI") == "" {
			t.Skipf("sparql endpoint unreachable at %s: %v", base, err)
		}
		t.Fatalf("sparql endpoint unreachable at %s: %v", base, err)
	}
	return base
}

// UniqueGraph returns a fresh named-graph IRI so tests sharing one Oxigraph
// instance never see each other's quads.
func UniqueGraph(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("random graph name: %v", err)
	}
	return iri.GraphNS + "test/" + hex.EncodeToString(buf)
}
```

- [ ] **Step 4: Write the integration tests**

`internal/graph/oxigraph_test.go`:

```go
package graph_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graph"
	"github.com/sunstoneinstitute/worklode/internal/graph/graphtest"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// rdfWLDir resolves rdf/wl relative to this source file.
func rdfWLDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "rdf", "wl")
}

// TestVocabularySourcesParse is the parse gate for the staged rdf-registry
// sources: Oxigraph rejects the POST on any Turtle syntax error.
func TestVocabularySourcesParse(t *testing.T) {
	c := graph.NewClient(graphtest.Endpoint(t), nil)
	g := graphtest.UniqueGraph(t)
	ctx := t.Context()

	for _, name := range []string{"ontology.ttl", "concept.ttl", "wl-shapes.ttl"} {
		data, err := os.ReadFile(filepath.Join(rdfWLDir(), name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := c.Load(ctx, g, data); err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
	}

	// The layer tags make the model queryable by layer (criterion 9).
	rows, err := c.Select(ctx, `SELECT ?c WHERE { GRAPH <`+g+`> { ?c <`+
		iri.Term("layer")+`> <`+iri.Concept("intent")+`> } }`)
	if err != nil {
		t.Fatalf("layer query: %v", err)
	}
	found := false
	for _, r := range rows {
		if r["c"] == iri.Term("Component") {
			found = true
		}
	}
	if !found {
		t.Fatalf("wl:Component not tagged wlc:intent; layer rows = %v", rows)
	}
}

func TestReplaceSubjectRoundTrip(t *testing.T) {
	c := graph.NewClient(graphtest.Endpoint(t), nil)
	g := graphtest.UniqueGraph(t)
	ctx := t.Context()

	s := iri.Task("WL-1")
	other := iri.Task("WL-2")
	if err := c.Update(ctx, graph.InsertData(g, []graph.Triple{
		{S: s, P: graph.DCTTitle, O: graph.Text("old title")},
		{S: s, P: iri.Term("taskState"), O: graph.Text("ready")},
		{S: other, P: graph.DCTTitle, O: graph.Text("bystander")},
	})); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := c.Update(ctx, graph.ReplaceSubject(g, s, []graph.Triple{
		{S: s, P: graph.DCTTitle, O: graph.Text("new title")},
	})); err != nil {
		t.Fatalf("replace: %v", err)
	}

	rows, err := c.Select(ctx, `SELECT ?p ?o WHERE { GRAPH <`+g+`> { <`+s+`> ?p ?o } }`)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(rows) != 1 || rows[0]["o"] != "new title" {
		t.Fatalf("subject rows = %v; want only the new title", rows)
	}
	if ok, err := c.Ask(ctx, `ASK { GRAPH <`+g+`> { <`+other+`> ?p ?o } }`); err != nil || !ok {
		t.Fatalf("bystander subject disturbed (ok=%v, err=%v)", ok, err)
	}
}

// TestTaskInTwoWorkstreamGraphs proves acceptance criterion 8 at the graph
// layer: the same task's quads in two Workstream graphs, and a per-subject
// re-projection in one graph leaving the other (and other tasks) untouched.
func TestTaskInTwoWorkstreamGraphs(t *testing.T) {
	c := graph.NewClient(graphtest.Endpoint(t), nil)
	ctx := t.Context()
	g1 := graphtest.UniqueGraph(t)
	g2 := graphtest.UniqueGraph(t)

	now := time.Now().UTC()
	task := store.Task{ID: "WL-9", ProjectID: "alpha", Title: "two homes",
		Priority: "high", Kind: "chore", State: "ready", CreatedAt: now, UpdatedAt: now}
	sibling := store.Task{ID: "WL-10", ProjectID: "alpha", Title: "sibling",
		Priority: "low", Kind: "chore", State: "ready", CreatedAt: now, UpdatedAt: now}

	for _, g := range []string{g1, g2} {
		if err := c.Update(ctx, graph.ReplaceSubject(g, iri.Task(task.ID),
			graph.TaskTriples(task, nil, nil))); err != nil {
			t.Fatalf("project into %s: %v", g, err)
		}
	}
	if err := c.Update(ctx, graph.ReplaceSubject(g1, iri.Task(sibling.ID),
		graph.TaskTriples(sibling, nil, nil))); err != nil {
		t.Fatalf("project sibling: %v", err)
	}

	// Re-project WL-9 in g1 only, with a new state.
	task.State = "in_progress"
	if err := c.Update(ctx, graph.ReplaceSubject(g1, iri.Task(task.ID),
		graph.TaskTriples(task, nil, nil))); err != nil {
		t.Fatalf("re-project: %v", err)
	}

	stateIn := func(g string) string {
		t.Helper()
		rows, err := c.Select(ctx, `SELECT ?s WHERE { GRAPH <`+g+`> { <`+
			iri.Task("WL-9")+`> <`+iri.Term("taskState")+`> ?s } }`)
		if err != nil || len(rows) != 1 {
			t.Fatalf("state in %s: rows=%v err=%v; want exactly one", g, rows, err)
		}
		return rows[0]["s"]
	}
	if got := stateIn(g1); got != "in_progress" {
		t.Fatalf("g1 state = %q; want in_progress", got)
	}
	if got := stateIn(g2); got != "ready" {
		t.Fatalf("g2 state = %q; want the untouched ready", got)
	}
	if ok, err := c.Ask(ctx, `ASK { GRAPH <`+g1+`> { <`+iri.Task("WL-10")+`> ?p ?o } }`); err != nil || !ok {
		t.Fatalf("sibling task disturbed by re-projection (ok=%v, err=%v)", ok, err)
	}
}
```

- [ ] **Step 5: Run the tests**

Run: `docker compose up -d oxigraph && go test ./internal/graph/...`
Expected: PASS (integration tests skip instead if Oxigraph is not running)

- [ ] **Step 6: Commit**

```bash
git add docker-compose.yml .github/workflows/_test.yml internal/graph
git commit -m "Prove the graph layer against Oxigraph"
```
