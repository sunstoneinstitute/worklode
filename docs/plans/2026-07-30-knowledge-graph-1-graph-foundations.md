---
status: accepted
task: WL-25
covers:
  - spec: docs/specs/006-knowledge-graph.md#sec-3
    coverage: partial
  - spec: docs/specs/006-knowledge-graph.md#sec-10
    coverage: partial
  - spec: docs/specs/006-knowledge-graph.md#sec-10.1
    coverage: partial
  - spec: docs/specs/006-knowledge-graph.md#sec-11
    coverage: partial
---
# Knowledge graph 1/2 (spec 006): IRI grammar, vocabulary gaps & the projection mapping — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 1 of 2. This part ends when the graph side — IRIs, the two
missing vocabulary terms, the task→triples mapping, and deterministic Turtle
rendering — is built and proven against Oxigraph in tests, with no backbone
polling or server coupling yet. Part 2
(`2026-07-30-knowledge-graph-2-projector.md`, executed by WL-26, rewritten
onto this part's decisions under WL-110) wires it to the backbone. Task
numbers restart at 1 in each part (`docs/authoring-design-docs.md`).

**Rewritten 2026-08-19 (WL-108).** The version accepted 2026-07-31 (`7a61bc6`)
became unexecutable: `ns/*.ttl` became the vocabulary home (`d2267f3`) and the
rdf-registry route was cancelled (rdf-registry#31, see `docs/follow-ups.md`
"Publishing ns/*.ttl … is unowned"), `wl:Workstream`/`wl:OngoingMaintenance`/
`wl:inWorkstream` were retired for `wl:inProject` (`70d2139`, 025 acceptance
criterion 20), `internal/graphserver` shipped the graph-server client
(`7135b59`…`8289031`), and the corpus fold (`e9065b5`) renumbered the
acceptance criteria the old plan cited. The design calls below are the settled
replacements; WL-110 and WL-111 carry the same corrections into the sibling
plans.

**Goal:** Ship the still-missing Worklode half of spec 006's graph
foundations: the canonical IRI grammar as a Go package with one settled API,
the two projected-literal predicates the projection table needs but the
vocabulary never declared, and a pure backbone-row→triples→Turtle mapping,
proven against Oxigraph with the reads spec 006 promises (transitive
`wl:dependsOn+`, exactly one `wl:inProject`).

**Architecture:** `internal/kg/iri` fixes the IRI grammar as pure plain-string
constructors. Spec 006 §3 is amended to declare `wl:priority` and
`wl:concern`, then `ns/ontology.ttl`/`ns/shapes.ttl` mirror them (spec first,
then `ns/` — CLAUDE.md). A new pure package `internal/graphproj` (the name the
runtime-layer plan already targets) turns a `model.Task` row plus its edges
into subject-complete triples and renders a project's triples as a
deterministic Turtle document — the unit a GSP `PutGraph` full-replace takes.
No client is built: `internal/graphserver` already speaks graph-server's
branch-scoped GSP + `/sparql`, and tests reach Oxigraph through a small
test-only loader instead.

**Tech Stack:** Go 1.26, standard-library testing only (no new module
dependencies), Oxigraph (docker) as the test endpoint via its GSP
`/store?graph=` and `/query` endpoints.

**Spec:** `docs/specs/006-knowledge-graph.md` — read it via
`docs/specs/inlined/006-knowledge-graph.md`.

---

## Already built — do not recreate

- **The vocabulary.** `ns/{ontology,concept,shapes}.ttl` hold the `wl:`
  classes/properties, the SKOS schemes and the SHACL shapes — a strict
  superset of what the original plan set out to mint, including every runtime
  term. The only §11-projection terms missing are `wl:priority` and
  `wl:concern` (Task 2). Never author `rdf/wl/`; `ns/` owns the schema
  (025 §17) and serving it is a tracked follow-up, not this plan's problem.
- **The client.** `internal/graphserver` (214 lines + tests + an e2e
  acceptance test in `e2e/graphserver_test.go`) does branch-scoped
  `PutGraph`/`GetGraph`/`DeleteGraph` plus SPARQL `Select`, with Keycloak
  client-credentials via `FromEnv`. It has no production caller yet — part 2's
  projector is that caller.
- **The projection sources.** `events` + `state_log`, `tasks`, `task_edges`
  (`child_of`/`blocks`/`follow_up_to` since migration 0018) all exist
  relationally. Completing the outbox and polling it is part 2.

## Design calls

1. **Vocabulary home is `ns/` — Turtle is minted nowhere else.** CLAUDE.md and
   025 §17 make `ns/` the owner; writing `rdf/wl/` would create the two-owner
   split the repo bans. Spec 006 §14 and §13.2 item 3 still describe the
   cancelled rdf-registry route; that amendment is already flagged in
   `docs/follow-ups.md` and is not owed here.
2. **Project, not Workstream.** Membership is `wl:inProject`
   (`owl:FunctionalProperty` — exactly one per task, 025 acceptance criterion
   20); a task's quads live in the one named graph
   `https://worklode.io/ns/graph/project/<project-id>`, the family
   `e2e/graphserver_test.go` already uses. The instance IRI is
   `id/project/<project-id>`. Spec 006 §10's grammar table has no Project or
   Agent row — Task 2 adds them; the old multi-workstream proof
   (`TestTaskInTwoWorkstreamGraphs`) is forbidden by the vocabulary and is
   replaced by the §11-shaped proofs in Task 5.
3. **`wl:priority` and `wl:concern` are minted.** Spec 006 §11's projection
   table projects both (`Task node + concern/priority/state/wl:taskKind`,
   v1), so the predicates must exist; they are datatype properties,
   projection-only literal mirrors of the backbone enums, following
   `wl:taskState`'s pattern exactly (no SKOS scheme — the backbone owns the
   enum, 006 open question 3). Ordering per CLAUDE.md: amend 006 §3 first,
   mirror `ns/` in the same commit. This settles the half of WL-65 that was
   still open.
4. **No SPARQL client is built, and the write unit is the whole project
   graph.** `internal/graphserver` is the only production client. graph-server
   has **no SPARQL Update endpoint** (its writes replace or merge whole named
   graphs — `internal/graphserver/client.go` package doc), so 006 §11's
   "per-subject `DELETE`/`INSERT`" mechanism sentence is unimplementable
   against the system of record; Task 2 amends it to what the projector will
   actually do: recompute every task of a dirty project from the backbone
   (which stays the source of truth — no read-modify-write) and `PutGraph` the
   full project graph. Deterministic rendering (Task 3) makes an unchanged
   re-projection byte-identical, the same idempotence §10.1 demands of row
   projection. Single projector + graph-server's per-branch lock keeps
   If-Match CAS a should-have (§13.3 item 6, unchanged).
5. **The `iri` API is plain-string.** Pure, non-validating
   `func(...) string` constructors plus exported untyped namespace constants
   (`Base`, `Ontology`, `ConceptNS`, `IDNS`, `GraphNS`). Grounds: §10.1's own
   principle ("projection is a pure function of the row"); inputs come from
   CHECK-constrained columns, so an error return is dead weight — the
   superseded platform-graph-design plan's `(string, error)` signature dies
   with it; 31 of the 52 downstream call sites already assume plain strings,
   and drift-3's `const p = iri.IDNS + "task/"` needs the exported constant.
   `Concept` is a *function* (`iri.Concept("feature")`); the namespace
   constant is `ConceptNS`. WL-111 sweeps the six consuming plans onto this
   signature; their additions (`Section`, `DocVersion`, `DeclaredGraph`,
   `ObservedGraph`, `Repo`) follow the same convention in their own plans.
6. **`internal/kg/manifest` is not this plan's.** WL-109 owns deciding where
   the component-manifest parser lands; nothing here touches it.
7. **Coverage is declared per section of the folded spec** (frontmatter
   above), replacing the old plan's references to acceptance criteria that no
   longer exist under those numbers. All four claims are `partial` by design:
   §3's remaining terms already live in `ns/`, §10/§10.1 hosting and
   byte-identical row projection belong to the follow-ups and the
   runtime-layer plan, and §11's projector half is part 2 (WL-110 requalifies
   part 2's `covers:` to complete the §11 claim).

## Deliberately not in this part

- **The projector loop, outbox completion, checkpointing** — part 2 (WL-26,
  after WL-110's rewrite).
- **`wl:produces` and `wl:affects` emission.** Both sit in §11's v1 table, but
  the backbone stores no task→deliverable and no task→component relation to
  project. Deliverables now exist as rows (spec 029) with no task edge;
  components have no backbone representation at all. WL-110 scopes `produces`;
  `affects` waits for its source. Emitting a predicate without a source would
  be fabrication, so the mapping omits both and says so.
- **`wl:mirrors`** — needs the Task↔Issue mirror lifecycle (004 Q5 / 008
  Q008.4), still open.
- **`wl:requiresSkill`** — `tasks.skills` exists, but no Skill IRI pattern is
  fixed in §10; projecting it is 016's concern, not fixed here.
- **Runtime-node projection functions** — the runtime-layer plan's
  `internal/graphproj` additions (WL-27, after WL-111).
- **Serving `ns/` under `worklode.io/ns/`** — tracked in
  `docs/follow-ups.md`; unowned, deliberately.
- **Drift queries (spec 007), Deliverable auto-confirmation (v2).**

---

## File structure

**New files**

| Path | Responsibility |
|---|---|
| `internal/kg/iri/iri.go` | the canonical IRI grammar: namespace constants + pure constructors |
| `internal/kg/iri/iri_test.go` | table test over every pattern |
| `internal/graphproj/triple.go` | `Term`/`Triple`, literal escaping, deterministic Turtle `Document` rendering |
| `internal/graphproj/triple_test.go` | escaping, exact rendering, byte-identical determinism |
| `internal/graphproj/task.go` | `model.Task` + edges → subject-complete triples; `model.Project` → Project node |
| `internal/graphproj/task_test.go` | mapping: kind concept, literals, edge directions, omitted optionals |
| `internal/graphproj/graphtest/graphtest.go` | test-only Oxigraph loader (GSP `/store`, `/query`); skip-unless-CI |
| `internal/graphproj/oxigraph_test.go` | ns/ parse gate; project-graph replace round-trip; `dependsOn+`; one-`inProject` proof |

**Modified files**

| Path | Change |
|---|---|
| `docs/specs/006-knowledge-graph.md` | §3: declare `wl:priority`/`wl:concern`; §10: Project + Agent grammar rows; §11 + §15 item 4: whole-project-graph GSP replace |
| `ns/ontology.ttl` | mirror the two new datatype properties |
| `ns/shapes.ttl` | `wl:TaskShape`: priority required (CHECK-set `sh:in`), concern optional (CHECK-set `sh:in`) |
| `docker-compose.yml` | `oxigraph` service for local integration tests |
| `.github/workflows/_test.yml` | start Oxigraph + set `TEST_SPARQL_URL`, both guarded `if: contains(inputs.runs-on, 'ubuntu-latest')` like Postgres |

No migration: this part writes no backbone state.

**Test commands**

- Pure packages: `go test -trimpath ./internal/kg/iri/... ./internal/graphproj/...`
  (integration tests skip when Oxigraph is unreachable and `CI` is unset)
- With Oxigraph: `docker compose up -d oxigraph && go test -trimpath ./internal/graphproj/...`
- Everything: `make test`

---

## Tasks

### Task 1 — The IRI grammar package

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

Create `internal/kg/iri`: the single owner of the IRI grammar of spec 006 §10
and §10.1, under the API of design call 5. Every constructor is a pure
concatenation — no validation, no error return — and the five namespace roots
are exported untyped constants so callers can write
`const p = iri.IDNS + "task/"`.

Exported surface (all of it; signatures are the contract WL-111 sweeps the
other plans onto):

```go
const (
    Base      = "https://worklode.io/ns/"
    Ontology  = Base + "ontology#"  // wl:  (hash namespace)
    ConceptNS = Base + "concept/"   // wlc:
    IDNS      = Base + "id/"        // wlid:
    GraphNS   = Base + "graph/"     // named-graph families
)

func Term(local string) string     // Ontology + local
func Concept(local string) string  // ConceptNS + local

func Task(id string) string                 // id/task/<id>
func Project(projectID string) string       // id/project/<project-id>
func ProjectGraph(projectID string) string  // graph/project/<project-id>
func Agent(actorID string) string           // id/agent/<actor-id>
func Component(slug string) string          // id/component/<slug>
func Doc(slug string) string                // id/doc/<slug>
func Deliverable(id string) string          // id/deliverable/<id>
func Issue(host, owner, repo string, number int64) string // id/issue/<host>/<owner>/<repo>/<n>
func PR(host, owner, repo string, number int64) string    // id/pr/<host>/<owner>/<repo>/<n>

// §10.1 runtime grammar — kind-first, mirroring each relational natural key.
func Artifact(kind, name, version string) string          // id/artifact/<kind>/<name>/<version>
func Deployment(env, targetKind, targetName string) string // id/deployment/<env>/<kind>/<name>
func Environment(name string) string                       // id/environment/<name>
func Commit(host, owner, repo, sha string) string          // id/commit/<host>/<owner>/<repo>/<sha>
```

`Build` is deliberately absent (§10.1: reserved, no v1 source), as are
`Section`/`DocVersion` (design-documents plan) and the spec-007 graph families
(drift plans) — later plans add them here following the same convention.
Slashes inside a local id are permitted (slash namespace, opaque path); the
package doc says so and cites 006 §10.

- [ ] **Step 1: Write the failing test** — `internal/kg/iri/iri_test.go`:

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
		{"project", iri.Project("worklode"), base + "id/project/worklode"},
		{"project graph", iri.ProjectGraph("worklode"), base + "graph/project/worklode"},
		{"agent", iri.Agent("stig"), base + "id/agent/stig"},
		{"component slug with slashes", iri.Component("github.com/sunstoneinstitute/worklode"),
			base + "id/component/github.com/sunstoneinstitute/worklode"},
		{"doc", iri.Doc("spec-worklode-006"), base + "id/doc/spec-worklode-006"},
		{"deliverable", iri.Deliverable("WL-DEL-1"), base + "id/deliverable/WL-DEL-1"},
		{"issue", iri.Issue("github.com", "sunstoneinstitute", "worklode", 42),
			base + "id/issue/github.com/sunstoneinstitute/worklode/42"},
		{"pr", iri.PR("github.com", "sunstoneinstitute", "worklode", 42),
			base + "id/pr/github.com/sunstoneinstitute/worklode/42"},
		{"artifact kind-first", iri.Artifact("docker_image", "ghcr.io/sunstoneinstitute/graph-server", "v1"),
			base + "id/artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1"},
		{"deployment", iri.Deployment("prod", "flux_kustomization", "graph-server"),
			base + "id/deployment/prod/flux_kustomization/graph-server"},
		{"environment", iri.Environment("prod"), base + "id/environment/prod"},
		{"commit", iri.Commit("github.com", "sunstoneinstitute", "worklode", "a16c2a7"),
			base + "id/commit/github.com/sunstoneinstitute/worklode/a16c2a7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q; want %q", tc.got, tc.want)
			}
		})
	}
}

// The namespace roots are exported untyped constants: downstream packages
// build prefixes from them (drift-3's iri.IDNS + "task/").
const _ = iri.IDNS + "task/"
```

- [ ] **Step 2: Run** `go test -trimpath ./internal/kg/iri/...` — expect FAIL
      (package missing)
- [ ] **Step 3: Implement `internal/kg/iri/iri.go`** — the constants and
      one-line constructors above, package doc citing 006 §10/§10.1 and 025
      §17, `fmt.Sprintf` only where an int is formatted
- [ ] **Step 4: Run the test again** — expect PASS
- [ ] **Step 5: Commit** — `git add internal/kg && git commit`

### Task 2 — Declare wl:priority and wl:concern; align spec 006 §10/§11 with the shipped surfaces

```yaml
kind: design
priority: high
blockedBy: [ ]
```

Amend `docs/specs/006-knowledge-graph.md` (the source, never `inlined/`) and
mirror `ns/` in the same commit — spec first, then the mirror (CLAUDE.md).
Three edits, no new sections, no renumbering (all anchors are frozen); this
follows the in-place precedent of `70d2139`, which retired the Workstream
terms from the same spec:

1. **§3 (`{#sec-3}`)**: declare the two projected literal mirrors alongside
   `wl:taskState`'s existing declaration, with its exact rationale pattern:

   ```turtle
   wl:priority a owl:DatatypeProperty, owl:FunctionalProperty ;
       wl:layer wlc:execution ;
       rdfs:domain wl:Task ; rdfs:range xsd:string ;
       rdfs:comment "Projected literal mirror of the backbone priority enum (spec 005): critical/high/medium/low. Deliberately not a SKOS scheme — the backbone owns the enum (Open Q3)." .

   wl:concern a owl:DatatypeProperty ;
       wl:layer wlc:execution ;
       rdfs:domain wl:Task ; rdfs:range xsd:string ;
       rdfs:comment "Projected literal mirror of the backbone concern enum (spec 005); absent when the task has none." .
   ```

2. **§10 (`{#sec-10}`)**: add two rows to the instance-grammar table —
   Project (`id/project/<project-id>`, the backbone project id) and Agent
   (`id/agent/<actor-id>`, a `foaf:`/`prov:` Agent) — and note beneath the
   table that a Project's projection named graph is
   `graph/project/<project-id>` (the family §11 anchors and
   `e2e/graphserver_test.go` already exercises).

3. **§11 (`{#sec-11}`) mechanism paragraph + §15 (`{#sec-15}`) item 4**:
   replace the per-subject-replace sentence. graph-server exposes no SPARQL
   Update (`internal/graphserver/client.go`: "writes replace or merge whole
   named graphs"), so per-subject `DELETE`/`INSERT` cannot run against the
   system of record. New mechanism text: the projector recomputes **every
   task of a dirty Project from the backbone** and replaces that Project's
   named graph wholesale via GSP `PUT` — deterministic rendering makes an
   unchanged re-projection byte-identical, so the write is idempotent per
   project graph; a single projector plus graph-server's per-branch lock
   keeps If-Match CAS a should-have (§13.3 item 6). Update §15 item 4's
   resolution note to match (it currently restates the per-subject
   mechanism).

Then mirror in `ns/`:

- `ns/ontology.ttl`: the same two property declarations, in the Layer-2
  execution block next to `wl:taskState`.
- `ns/shapes.ttl` `wl:TaskShape`: a required `wl:priority` property
  (`sh:minCount 1; sh:maxCount 1;
  sh:in ( "critical" "high" "medium" "low" )`) and an optional `wl:concern`
  (`sh:maxCount 1;
  sh:in ( "completeness" "performance" "usability" "security" )`), each with
  a comment naming the CHECK constraint it mirrors, in `wl:taskState`'s
  existing style.

- [ ] **Step 1: Edit the spec** (three edits above)
- [ ] **Step 2: Mirror `ns/ontology.ttl` and `ns/shapes.ttl`**
- [ ] **Step 3: Validate** — `riot --validate ns/*.ttl` if riot is installed
      (Task 5's Oxigraph parse gate re-proves it in CI either way);
      `./scripts/secfmt.py -l` and `./scripts/secmeta.py` must pass
- [ ] **Step 4: Commit** — the pre-commit hook regenerates
      `docs/specs/inlined/`; stage the regenerated views with the edit

### Task 3 — Triples and deterministic Turtle rendering

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

Create `internal/graphproj` (the pure projection package the runtime-layer
plan also targets — one package, one `Triple` type): `Term`/`Triple` with
correct literal escaping, and `Document`, which renders a set of triples as a
GSP-PUT-ready Turtle document. Rendering is the idempotence lever of design
call 4, so it must be **deterministic**: triples are emitted one per line in
N-Triples form (a subset of Turtle — no prefixes to keep stable), sorted
lexicographically by rendered line, duplicates dropped. Same triples in any
order → byte-identical document.

- [ ] **Step 1: Write the failing test** — `internal/graphproj/triple_test.go`:

```go
package graphproj

import (
	"bytes"
	"testing"
)

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

func TestDocumentIsDeterministic(t *testing.T) {
	fwd := []Triple{
		{S: "urn:s", P: "urn:p", O: IRIRef("urn:o")},
		{S: "urn:s", P: "urn:q", O: Text("v")},
		{S: "urn:a", P: "urn:p", O: Text("w")},
	}
	rev := []Triple{fwd[2], fwd[1], fwd[0], fwd[0]} // reordered + duplicate
	want := "<urn:a> <urn:p> \"w\" .\n" +
		"<urn:s> <urn:p> <urn:o> .\n" +
		"<urn:s> <urn:q> \"v\" .\n"
	if got := Document(fwd); string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if !bytes.Equal(Document(fwd), Document(rev)) {
		t.Fatal("Document is order- or duplicate-sensitive; must be byte-identical")
	}
}
```

- [ ] **Step 2: Run** `go test -trimpath ./internal/graphproj/...` — expect
      FAIL (package missing)
- [ ] **Step 3: Implement `internal/graphproj/triple.go`**: `Term` as an
      IRI-or-literal struct with `IRIRef`/`Text`/`Typed` constructors and a
      `String()` using a `strings.NewReplacer` over `\`, `"`, `\n`, `\r`,
      `\t`; `Triple{S, P string; O Term}`; `Document` rendering each triple
      as `<S> <P> O .`, sorting lines with `slices.Sort`, dropping adjacent
      duplicates with `slices.Compact`. Package doc: cites 006 §11 and design
      call 4 (whole-project-graph GSP replace; determinism is the idempotence
      guarantee)
- [ ] **Step 4: Run the test again** — expect PASS
- [ ] **Step 5: Commit**

### Task 4 — Task and Project rows → triples

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1, 2, 3]
```

Add `internal/graphproj/task.go`: `TaskTriples(t model.Task, out, in
[]model.Edge) []Triple` (subject-complete for the task IRI — every triple
whose subject is the task and no others, so a graph rebuilt task-by-task is a
faithful full projection) and `ProjectTriples(p model.Project) []Triple` (the
Project node §11's table projects: `rdf:type wl:Project`, `dct:title` from
`p.Name`).

The predicate set follows spec 006 §11 as amended by Task 2 and the in-force
`ns/ontology.ttl`:

| Backbone fact | Triple |
|---|---|
| row exists | `rdf:type` → `wl:Task` |
| `Title` | `dct:title` literal |
| `State` | `wl:taskState` literal |
| `Kind` | `wl:taskKind` → `iri.Concept(kind)` (the six post-0025 kinds; `design`, not `spec`) |
| `Priority` | `wl:priority` literal |
| `Concern` (omit empty) | `wl:concern` literal |
| `Project` | `wl:inProject` → `iri.Project(...)` |
| `CreatedBy` (omit empty) | **`prov:wasAssociatedWith`** → `iri.Agent(...)` |
| `CreatedAt` / `UpdatedAt` | `dct:created` / `dct:modified`, `xsd:dateTime`, UTC RFC3339 |
| out-edge `child_of` | `dct:isPartOf` → parent task |
| out-edge `blocks` | `wl:blocks` → blocked task |
| in-edge `blocks` | `wl:dependsOn` → blocking task |
| out-edge `follow_up_to` | `wl:followUpTo` → origin task |

`prov:wasAssociatedWith`, **not** `prov:wasAttributedTo`: `wl:Task` is a
`prov:Activity` and PROV declares Activity/Entity disjoint — the ontology's
`wl:Task` comment spells this out, and the owlrl consistency check would flag
the wrong edge. `wl:produces`, `wl:affects`, `wl:mirrors` and
`wl:requiresSkill` are deliberately not emitted ("Deliberately not in this
part").

- [ ] **Step 1: Write the failing test** — `internal/graphproj/task_test.go`,
      in the old plan's set-comparison style: build a `model.Task` with every
      field populated plus out-edges (`child_of`, `blocks`, `follow_up_to`)
      and in-edges (`blocks`, plus a `child_of` in-edge that must produce
      nothing), collect `P + " " + O.String()` into a set, assert the exact
      expected set (every subject is `iri.Task(id)`; count matches — no
      extras). Second test: empty `Concern`/`CreatedBy` emit no triple. Third:
      `ProjectTriples` yields exactly the typed node + title
- [ ] **Step 2: Run** — expect FAIL (`undefined: TaskTriples`)
- [ ] **Step 3: Implement** `task.go` with the reused-vocabulary IRIs as
      package constants (`RDFType`, `DCTTitle`, `DCTCreated`, `DCTModified`,
      `DCTIsPartOf`, `ProvWasAssociatedWith`, `XSDDateTime`) and the mapping
      above; document the edge-direction convention ("A blocks B" is stored
      from=A,to=B; a child's `child_of` out-edge emits `dct:isPartOf` on the
      child only)
- [ ] **Step 4: Run the test again** — expect PASS; also
      `go vet ./internal/graphproj/...`
- [ ] **Step 5: Commit**

### Task 5 — Oxigraph harness and graph-layer integration proof

```yaml
kind: feature
priority: high
blockedBy: [4]
```

Prove the vocabulary and the mapping against a real triple store. Oxigraph is
the test endpoint only (graph-server is proven separately by
`e2e/graphserver_test.go`); the helper is a **test-only loader**, deliberately
not a production client — production writes go through `internal/graphserver`.

- [ ] **Step 1: docker-compose service** — append to `services:` in
      `docker-compose.yml`:

```yaml
  # SPARQL endpoint for knowledge-graph integration tests only (the production
  # write path is internal/graphserver; Oxigraph stands in for validation).
  oxigraph:
    image: ghcr.io/oxigraph/oxigraph:latest
    command: ["serve", "--location", "/data", "--bind", "0.0.0.0:7878"]
    ports:
      - "127.0.0.1:7878:7878"
    volumes:
      - ./data/oxigraph:/data
    restart: unless-stopped
```

- [ ] **Step 2: CI** — in `.github/workflows/_test.yml`, add a `docker run`
      step for Oxigraph **guarded with
      `if: contains(inputs.runs-on, 'ubuntu-latest')`**, exactly like the
      ephemeral-Postgres step (the self-hosted runner is deliberately
      Docker-less), and set `TEST_SPARQL_URL: http://localhost:7878` in the
      `go test` step's `env:`. A `docker run` rather than `services:` because
      service containers cannot override the image command
- [ ] **Step 3: `internal/graphproj/graphtest/graphtest.go`** — a package with
      three helpers over plain `net/http` against Oxigraph's endpoints:
      `Endpoint(t)` (reads `TEST_SPARQL_URL`, default `localhost:7878`,
      probes with an `ASK {}` POST to `/query`; skip when unreachable unless
      `CI` is set — `store.OpenTestStore`'s contract), `PutGraph(t, base,
      graphIRI, turtle)` (HTTP PUT `/store?graph=…` as `text/turtle`; PUT so a
      re-load replaces), and `Select(t, base, query)` (POST `/query`,
      `application/sparql-results+json`, flattened to
      `[]map[string]string`). Package doc states it is test-only and why
- [ ] **Step 4: `internal/graphproj/oxigraph_test.go`** — three tests:
      1. **`TestNSVocabularyParses`** — the in-repo parse gate for
         `ns/{ontology,concept,shapes}.ttl`: load each into a unique graph
         (Oxigraph 400s on any Turtle syntax error), then SELECT terms
         carrying `wl:layer wlc:execution` and assert `wl:priority` and
         `wl:concern` are among them (proves Task 2's mirror landed);
      2. **`TestProjectGraphReplaceRoundTrip`** — render two tasks (one
         blocking the other, both `wl:inProject` alpha) plus the Project node
         with `Document`, PUT as graph `iri.ProjectGraph("alpha")`; re-render
         with one task's state changed and PUT again; read back: exactly one
         `wl:taskState` per task, new state present, sibling untouched, and a
         third task projected into `iri.ProjectGraph("beta")` unaffected —
         the §11 mechanism (whole-project-graph replace, per-project
         isolation) as amended by Task 2;
      3. **`TestDependsOnPath`** — seed WL-1←WL-2←WL-3 blocks-chains in one
         project graph and prove `?t wl:dependsOn+ ?x` reaches WL-1 from
         WL-3 (the §3 transitive-property promise, query-time, no reasoner),
         and that every projected task binds exactly one `wl:inProject`
         (COUNT per task = 1 — 025 acceptance criterion 20's shape)
- [ ] **Step 5: Run** — `docker compose up -d oxigraph &&
      go test -trimpath ./internal/graphproj/...` PASS; without Oxigraph the
      integration tests skip and the pure tests still pass; `make test` green
- [ ] **Step 6: Commit**
