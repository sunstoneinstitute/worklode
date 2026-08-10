---
status: accepted
task: WL-7
covers: docs/specs/007-drift-and-overview.md
---
# Drift & overview 3/3 (spec 007): overview engine & surfaces — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 3 of 3. Task numbering is global across the series: this plan
holds Tasks 10–15; `2026-07-30-drift-and-overview-1-repo-derivers.md` (Tasks
1–6) and `2026-07-30-drift-and-overview-2-server-derivers.md` (Tasks 7–9)
must both be merged first.

**Goal:** Make drift visible: critical path v1, standing queries 4.1/4.2/4.5
over the declared-vs-observed diff, the overview service, the API + CLI
surface (`lode overview`/`drift`/`gaps`/`frontier`/`critical-path`,
`POST /api/v1/derive`), and the read-only drift web view.

**Architecture:** Standing queries live in a new `internal/overview` package
as SPARQL reads through `internal/graph.Client`; critical path is computed in
Go on each read by joining backbone `blocks` edges with KG `wl:dependsOn`
edges. The authoritative ready frontier stays on the backbone
(`store.rankTasks`); the overview only mirrors it via part 2's
`store.Frontier`. `lode serve` wires `overview.Service` and the server-side
derivers from `LODE_GRAPH_URL`; the handlers, CLI commands, and drift board
are read-only (the one mutation, `POST /api/v1/derive`, is admin-gated).

**Tech Stack:** Go 1.26, cobra CLI, PostgreSQL via `database/sql`,
standard-library testing, SPARQL 1.1 Protocol over `net/http`, Oxigraph
(docker) as the test endpoint, `html/template` for the web view.

**Spec:** `docs/specs/007-drift-and-overview.md`, read with its amendments:
`docs/specs/014-design-documents-as-graph-objects.md` §5–§6, §10 and
`docs/specs/015-runtime-layer.md` §2–§6. All `ls:`/`lsc:`/`lsid:` prefixes in
the spec read as `wl:`/`wlc:`/`wlid:` (014 §1). See part 1's header for the
full series scope, sibling-plan prerequisites, prior-art map, design calls,
and what is owned elsewhere.

**Prerequisites (landed by parts 1–2):** all four derivers behind
`derive.Run`, `internal/cmd/overview.go` holding the `derive` command
(extended here with the read commands), and the store reads
(`store.Frontier`, `AllBlockEdges`, `TaskPRs`, `AllDeployments`,
`AllArtifactsByID`, `HasMainCommit`, `AllReleaseFrontiers`).

Design calls this plan inherits (recorded in part 1, restated because they
shape Tasks 11–13):

- **The clock is bound from `lode`:** the deviation-expiry comparison
  (`dct:valid < today`) injects today's date into the query text rather than
  relying on `NOW()`, keeping query output deterministic under test.
- **Queries 4.3/4.4 are not built here** — superseded/re-pointed by 014 §6
  and owned by the 014 plan; when its section-scoped queries land they slot
  into `internal/overview` beside 4.1/4.2.

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `internal/overview/queries.go` | standing queries 4.1/4.2 as SPARQL over `graph.Client`; row types |
| `internal/overview/queries_test.go` | query-text golden checks (prefix/graph confinement, injected date) |
| `internal/overview/critpath.go` | SCC cycle detection, longest-path depth, transitive fan-out, critical set |
| `internal/overview/critpath_test.go` | known DAG, planted cycle excluded + surfaced |
| `internal/overview/service.go` | `Service`: store + optional graph client → the five overview reads |
| `internal/overview/oxigraph_test.go` | acceptance vs. Oxigraph: planted violation, stale intent, deviation suppression + expiry, gaps |
| `internal/api/overview.go` | handlers: overview, drift, gaps, frontier, critical-path, derive |
| `internal/api/overview_test.go` | auth gates, JSON shapes, 503 when graph unconfigured, frontier ordering |
| `internal/api/templates/drift.html` | drift board web view (violations + stale intent, read-only) |

**Modified files**

| Path | Change |
|---|---|
| `internal/api/server.go` | routes for the five reads + `POST /api/v1/derive`; `GET /drift` web route |
| `internal/api/web.go` | `driftPage` handler |
| `internal/cmd/serve.go` | wire `overview.Service` + server-side derivers from `LODE_GRAPH_URL` env |
| `internal/cmd/overview.go` | add `lode overview/drift/gaps/frontier/critical-path` beside part 1's `derive` |
| `internal/cmd/overview_test.go` | flag wiring + `--json` passthrough against a fake server |
| `internal/cli/client.go` | `Overview`, `Drift`, `Gaps`, `Frontier`, `CriticalPath`, `RunDerive` |
| `README.md` | document the commands, the deriver contract, and `POST /api/v1/derive` |

**Test commands**

- Pure packages (no services): `go test ./internal/overview/...`
- Postgres-backed: `docker compose up -d postgres && go test ./internal/api/... ./internal/cmd/...`
- Oxigraph-backed (skip when `TEST_SPARQL_URL` unset, per `graphtest`):
  `docker compose up -d oxigraph && go test ./internal/overview/...`
- Everything: `docker compose up -d postgres oxigraph && go test ./...`

---

## Task 10: Critical path v1

**Files:**
- Create: `internal/overview/critpath.go`
- Test: `internal/overview/critpath_test.go`

- [ ] **Step 1: Write the failing test**

```go
package overview

import (
	"reflect"
	"sort"
	"testing"
)

// Edges are (from, to) = "from must be done before to" — a blocks edge or a
// reversed dependsOn edge, normalized by the caller.
func TestAnalyzeKnownDAG(t *testing.T) {
	//   A → B → C
	//        ↘  D
	//   E (isolated)
	a := Analyze([][2]string{{"A", "B"}, {"B", "C"}, {"B", "D"}}, []string{"E"})

	wantDepth := map[string]int{"A": 0, "B": 1, "C": 2, "D": 2, "E": 0}
	if !reflect.DeepEqual(a.Depth, wantDepth) {
		t.Fatalf("Depth = %v; want %v", a.Depth, wantDepth)
	}
	wantFan := map[string]int{"A": 3, "B": 2, "C": 0, "D": 0, "E": 0}
	if !reflect.DeepEqual(a.FanOut, wantFan) {
		t.Fatalf("FanOut = %v; want %v", a.FanOut, wantFan)
	}
	// Longest chain is length 2 (A→B→C and A→B→D): A, B, C, D all critical.
	for _, n := range []string{"A", "B", "C", "D"} {
		if !a.Critical[n] {
			t.Errorf("%s not critical; want on a longest chain", n)
		}
	}
	if a.Critical["E"] {
		t.Error("isolated E marked critical")
	}
	if len(a.Cycles) != 0 {
		t.Fatalf("Cycles = %v; want none", a.Cycles)
	}
}

func TestAnalyzeExcludesAndSurfacesCycle(t *testing.T) {
	// X ↔ Y is a cycle; A → B is healthy and must keep correct numbers.
	a := Analyze([][2]string{{"X", "Y"}, {"Y", "X"}, {"A", "B"}}, nil)

	if len(a.Cycles) != 1 {
		t.Fatalf("Cycles = %v; want one", a.Cycles)
	}
	got := append([]string(nil), a.Cycles[0]...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"X", "Y"}) {
		t.Fatalf("cycle members = %v; want [X Y]", got)
	}
	for _, n := range []string{"X", "Y"} {
		if _, ok := a.Depth[n]; ok {
			t.Errorf("%s in Depth; cycle members must be excluded, not looped over", n)
		}
	}
	if a.Depth["B"] != 1 || a.FanOut["A"] != 1 {
		t.Fatalf("healthy chain wrong: depth[B]=%d fanout[A]=%d", a.Depth["B"], a.FanOut["A"])
	}
}

func TestAnalyzeSelfLoopIsACycle(t *testing.T) {
	a := Analyze([][2]string{{"X", "X"}}, nil)
	if len(a.Cycles) != 1 || len(a.Cycles[0]) != 1 || a.Cycles[0][0] != "X" {
		t.Fatalf("Cycles = %v; want [[X]]", a.Cycles)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/overview/...`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the implementation**

```go
// Package overview implements spec 007's read side: standing queries over
// the two-layer graph, the frontier mirror, and estimate-free critical path
// (D12). Everything is computed on read — nothing is cached or stored.
package overview

import "sort"

// Analysis is the result of one critical-path pass over the combined
// dependency DAG (blocks ∪ requires, unit weights).
type Analysis struct {
	// Depth is the longest predecessor-chain length ending at each node.
	Depth map[string]int
	// FanOut counts the distinct nodes transitively downstream of each node.
	FanOut map[string]int
	// Critical marks nodes lying on some longest chain.
	Critical map[string]bool
	// Cycles lists strongly connected components with a cycle (size > 1, or
	// a self-loop) — data errors excluded from the numbers above and
	// surfaced as their own finding (spec 007 §Cycle handling).
	Cycles [][]string
}

// Analyze runs the single longest-path + transitive-closure pass of spec
// 007 §Critical path v1 over edges (from must precede to) plus any isolated
// extra nodes (tasks with no edges still appear with depth 0).
func Analyze(edges [][2]string, extraNodes []string) Analysis {
	nodes := map[string]bool{}
	for _, e := range edges {
		nodes[e[0]], nodes[e[1]] = true, true
	}
	for _, n := range extraNodes {
		nodes[n] = true
	}

	cycles := cyclicSCCs(edges)
	inCycle := map[string]bool{}
	for _, scc := range cycles {
		for _, n := range scc {
			inCycle[n] = true
		}
	}

	succ := map[string][]string{}
	pred := map[string][]string{}
	indeg := map[string]int{}
	for n := range nodes {
		if !inCycle[n] {
			indeg[n] = 0
		}
	}
	for _, e := range edges {
		if inCycle[e[0]] || inCycle[e[1]] {
			continue
		}
		succ[e[0]] = append(succ[e[0]], e[1])
		pred[e[1]] = append(pred[e[1]], e[0])
		indeg[e[1]]++
	}

	// Kahn topological order over the acyclic remainder.
	var order, queue []string
	for n, d := range indeg {
		if d == 0 {
			queue = append(queue, n)
		}
	}
	sort.Strings(queue) // determinism
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		order = append(order, n)
		for _, m := range succ[n] {
			if indeg[m]--; indeg[m] == 0 {
				queue = append(queue, m)
			}
		}
	}

	depth := map[string]int{}
	for _, n := range order {
		d := 0
		for _, p := range pred[n] {
			if depth[p]+1 > d {
				d = depth[p] + 1
			}
		}
		depth[n] = d
	}

	// down[n] = longest chain length from n forward; critical iff
	// depth[n] + down[n] == max chain length.
	down := map[string]int{}
	for i := len(order) - 1; i >= 0; i-- {
		n := order[i]
		d := 0
		for _, m := range succ[n] {
			if down[m]+1 > d {
				d = down[m] + 1
			}
		}
		down[n] = d
	}
	maxChain := 0
	for _, n := range order {
		if depth[n]+down[n] > maxChain {
			maxChain = depth[n] + down[n]
		}
	}
	critical := map[string]bool{}
	for _, n := range order {
		critical[n] = maxChain > 0 && depth[n]+down[n] == maxChain
	}

	// Transitive fan-out by reverse-topological set union.
	reach := map[string]map[string]bool{}
	fanOut := map[string]int{}
	for i := len(order) - 1; i >= 0; i-- {
		n := order[i]
		r := map[string]bool{}
		for _, m := range succ[n] {
			r[m] = true
			for x := range reach[m] {
				r[x] = true
			}
		}
		reach[n] = r
		fanOut[n] = len(r)
	}

	return Analysis{Depth: depth, FanOut: fanOut, Critical: critical, Cycles: cycles}
}

// cyclicSCCs returns Tarjan strongly connected components that contain a
// cycle: size > 1, or a single node with a self-loop.
func cyclicSCCs(edges [][2]string) [][]string {
	succ := map[string][]string{}
	selfLoop := map[string]bool{}
	nodes := map[string]bool{}
	for _, e := range edges {
		succ[e[0]] = append(succ[e[0]], e[1])
		nodes[e[0]], nodes[e[1]] = true, true
		if e[0] == e[1] {
			selfLoop[e[0]] = true
		}
	}
	var (
		index, lowlink = map[string]int{}, map[string]int{}
		onStack        = map[string]bool{}
		stack          []string
		counter        int
		out            [][]string
		strongconnect  func(v string)
	)
	strongconnect = func(v string) {
		index[v], lowlink[v] = counter, counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range succ[v] {
			if _, seen := index[w]; !seen {
				strongconnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] && index[w] < lowlink[v] {
				lowlink[v] = index[w]
			}
		}
		if lowlink[v] == index[v] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			if len(scc) > 1 || selfLoop[scc[0]] {
				out = append(out, scc)
			}
		}
	}
	ordered := make([]string, 0, len(nodes))
	for n := range nodes {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)
	for _, n := range ordered {
		if _, seen := index[n]; !seen {
			strongconnect(n)
		}
	}
	return out
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/overview/ -run TestAnalyze -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/overview
git commit -m "Add estimate-free critical-path analysis"
```

---

## Task 11: Standing queries and the overview service

**Files:**
- Create: `internal/overview/queries.go`, `internal/overview/service.go`
- Test: `internal/overview/queries_test.go`, `internal/overview/oxigraph_test.go`

- [ ] **Step 1: Write the query layer**

`internal/overview/queries.go`:

```go
package overview

import (
	"context"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graph"
)

const sparqlPrefixes = `PREFIX wl:  <https://worklode.io/ns/ontology#>
PREFIX dct: <http://purl.org/dc/terms/>
PREFIX rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#>
PREFIX xsd: <http://www.w3.org/2001/XMLSchema#>
`

const (
	declaredFamily = "https://worklode.io/ns/graph/declared/"
	observedFamily = "https://worklode.io/ns/graph/observed/"
)

// DriftEdge is one dct:requires edge present in exactly one layer.
type DriftEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Deviation is one wl:AcceptedDeviation (spec 006 §Accepted deviations).
type Deviation struct {
	From         string `json:"from"`
	To           string `json:"to"`
	SanctionedBy string `json:"sanctioned_by"`
	ValidUntil   string `json:"valid_until,omitempty"`
	Expired      bool   `json:"expired"`
}

// Gap is a 4.2 finding: a component with no governing doc, or an unmatched
// repo path.
type Gap struct {
	Component string `json:"component,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Path      string `json:"path,omitempty"`
}

// violationsQuery is spec 007 §4.1 (violation direction):
// observed − declared − un-expired acknowledged. The layer partition is the
// graph-name family; today's date is injected from Go (design call 8).
func violationsQuery(today string) string {
	return sparqlPrefixes + fmt.Sprintf(`SELECT DISTINCT ?from ?to WHERE {
  GRAPH ?og { ?from dct:requires ?to . }
  FILTER(STRSTARTS(STR(?og), %q))
  FILTER NOT EXISTS {
    GRAPH ?dg { ?from dct:requires ?to . }
    FILTER(STRSTARTS(STR(?dg), %q))
  }
  FILTER NOT EXISTS {
    ?dev a wl:AcceptedDeviation ;
         rdf:subject ?from ; rdf:predicate dct:requires ; rdf:object ?to .
    FILTER NOT EXISTS { ?dev dct:valid ?exp . FILTER (?exp < %q^^xsd:date) }
  }
} ORDER BY ?from ?to`, observedFamily, declaredFamily, today)
}

// staleIntentQuery is §4.1's other direction: declared − observed.
func staleIntentQuery() string {
	return sparqlPrefixes + fmt.Sprintf(`SELECT DISTINCT ?from ?to WHERE {
  GRAPH ?dg { ?from dct:requires ?to . }
  FILTER(STRSTARTS(STR(?dg), %q))
  FILTER NOT EXISTS {
    GRAPH ?og { ?from dct:requires ?to . }
    FILTER(STRSTARTS(STR(?og), %q))
  }
} ORDER BY ?from ?to`, declaredFamily, observedFamily)
}

// acknowledgedQuery lists every deviation, active and expired
// (`lode drift --acknowledged`).
const acknowledgedQuery = sparqlPrefixes + `SELECT ?from ?to ?by ?exp WHERE {
  ?dev a wl:AcceptedDeviation ;
       rdf:subject ?from ; rdf:predicate dct:requires ; rdf:object ?to ;
       wl:sanctionedBy ?by .
  OPTIONAL { ?dev dct:valid ?exp }
} ORDER BY ?from ?to`

// docGapsQuery is §4.2: components with no governing DesignDoc.
const docGapsQuery = sparqlPrefixes + `SELECT ?c WHERE {
  ?c a wl:Component .
  FILTER NOT EXISTS { ?d a wl:DesignDoc ; wl:governs ?c . }
} ORDER BY ?c`

// unmatchedQuery reads deriver 2's coverage gaps.
const unmatchedQuery = sparqlPrefixes + `SELECT ?repo ?path WHERE {
  ?repo wl:unmatchedPath ?path .
} ORDER BY ?repo ?path`

// taskRequiresQuery pulls the KG half of the critical-path DAG:
// wl:dependsOn is the projected task dependency (subPropertyOf
// dct:requires; queried directly — no reasoner, spec 006).
const taskRequiresQuery = sparqlPrefixes + `SELECT ?from ?to WHERE {
  ?from wl:dependsOn ?to .
} ORDER BY ?from ?to`

// today formats the injected query clock.
func today() string { return time.Now().UTC().Format("2006-01-02") }

func driftEdges(rows []map[string]string) []DriftEdge {
	out := make([]DriftEdge, 0, len(rows))
	for _, r := range rows {
		out = append(out, DriftEdge{From: r["from"], To: r["to"]})
	}
	return out
}

// Violations runs the 4.1 violation query.
func Violations(ctx context.Context, c *graph.Client) ([]DriftEdge, error) {
	rows, err := c.Select(ctx, violationsQuery(today()))
	if err != nil {
		return nil, fmt.Errorf("drift violations: %w", err)
	}
	return driftEdges(rows), nil
}

// StaleIntent runs the 4.1 stale-intent query.
func StaleIntent(ctx context.Context, c *graph.Client) ([]DriftEdge, error) {
	rows, err := c.Select(ctx, staleIntentQuery())
	if err != nil {
		return nil, fmt.Errorf("stale intent: %w", err)
	}
	return driftEdges(rows), nil
}

// Acknowledged lists accepted deviations, marking expiry against the
// injected clock.
func Acknowledged(ctx context.Context, c *graph.Client) ([]Deviation, error) {
	rows, err := c.Select(ctx, acknowledgedQuery)
	if err != nil {
		return nil, fmt.Errorf("acknowledged deviations: %w", err)
	}
	now := today()
	out := make([]Deviation, 0, len(rows))
	for _, r := range rows {
		d := Deviation{From: r["from"], To: r["to"], SanctionedBy: r["by"], ValidUntil: r["exp"]}
		d.Expired = d.ValidUntil != "" && d.ValidUntil < now
		out = append(out, d)
	}
	return out, nil
}

// Gaps runs the 4.2 doc-gap and unmatched-path queries.
func Gaps(ctx context.Context, c *graph.Client) ([]Gap, error) {
	var out []Gap
	rows, err := c.Select(ctx, docGapsQuery)
	if err != nil {
		return nil, fmt.Errorf("doc gaps: %w", err)
	}
	for _, r := range rows {
		out = append(out, Gap{Component: r["c"]})
	}
	rows, err = c.Select(ctx, unmatchedQuery)
	if err != nil {
		return nil, fmt.Errorf("unmatched paths: %w", err)
	}
	for _, r := range rows {
		out = append(out, Gap{Repo: r["repo"], Path: r["path"]})
	}
	return out, nil
}
```

`internal/overview/queries_test.go` — cheap invariants, no endpoint:

```go
package overview

import (
	"strings"
	"testing"
)

func TestQueriesConfineLayersByGraphFamily(t *testing.T) {
	v := violationsQuery("2026-07-30")
	for _, want := range []string{
		`"https://worklode.io/ns/graph/observed/"`,
		`"https://worklode.io/ns/graph/declared/"`,
		`"2026-07-30"^^xsd:date`,
		"wl:AcceptedDeviation",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("violations query missing %s:\n%s", want, v)
		}
	}
	if !strings.Contains(staleIntentQuery(), "graph/declared/") {
		t.Error("stale-intent query does not scope to the declared family")
	}
}
```

- [ ] **Step 2: Write the service**

`internal/overview/service.go`:

```go
package overview

import (
	"context"
	"errors"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/graph"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// ErrNoGraph is returned by graph-backed reads when no SPARQL endpoint is
// configured; the API maps it to 503.
var ErrNoGraph = errors.New("knowledge graph not configured (LODE_GRAPH_URL)")

// Service is the read-only overview surface. Store is always present;
// Graph is nil when LODE_GRAPH_URL is unset, which disables the
// graph-backed reads but not the frontier (backbone-authoritative).
type Service struct {
	Store *store.Store
	Graph *graph.Client
}

// FrontierTask is one row of the frontier mirror, annotated with the
// overview-only critical-path measures (never consumed by claim --next).
type FrontierTask struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Project    string `json:"project"`
	Priority   string `json:"priority"`
	Concern    string `json:"concern,omitempty"`
	FanOut     int    `json:"fan_out"`
	Depth      int    `json:"depth"`
	IsCritical bool   `json:"is_critical"`
}

// CriticalPath is the `lode critical-path` payload.
type CriticalPath struct {
	MaxDepth int             `json:"max_depth"`
	Tasks    []FrontierTask  `json:"tasks"` // critical tasks, by depth
	Cycles   [][]string      `json:"cycles,omitempty"`
}

// taskDAG joins backbone blocks edges with KG wl:dependsOn edges into
// (before, after) pairs. A dependsOn edge reverses: the dependency comes
// first. With no graph configured the KG half is empty, not an error — the
// backbone half alone is still meaningful.
func (s *Service) taskDAG(ctx context.Context) ([][2]string, error) {
	edges, err := s.Store.AllBlockEdges(ctx)
	if err != nil {
		return nil, err
	}
	pairs := make([][2]string, 0, len(edges))
	for _, e := range edges {
		pairs = append(pairs, [2]string{e.FromTask, e.ToTask})
	}
	if s.Graph != nil {
		rows, err := s.Graph.Select(ctx, taskRequiresQuery)
		if err != nil {
			return nil, fmt.Errorf("kg requires edges: %w", err)
		}
		for _, r := range rows {
			from, to := taskIDFromIRI(r["from"]), taskIDFromIRI(r["to"])
			if from != "" && to != "" {
				pairs = append(pairs, [2]string{to, from}) // dependency precedes dependent
			}
		}
	}
	return pairs, nil
}

// taskIDFromIRI inverts iri.Task ("" for a non-task IRI).
func taskIDFromIRI(s string) string {
	const p = iri.IDNS + "task/"
	if len(s) > len(p) && s[:len(p)] == p {
		return s[len(p):]
	}
	return ""
}

// Frontier returns the ranked ready set (backbone order, spec 007 §4.5)
// annotated with depth/fan-out/is_critical from the combined DAG.
func (s *Service) Frontier(ctx context.Context, projectID string) ([]FrontierTask, error) {
	tasks, fanOut, err := s.Store.Frontier(ctx, projectID)
	if err != nil {
		return nil, err
	}
	pairs, err := s.taskDAG(ctx)
	if err != nil {
		return nil, err
	}
	a := Analyze(pairs, nil)
	out := make([]FrontierTask, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, FrontierTask{
			ID: t.ID, Title: t.Title, Project: t.ProjectID,
			Priority: t.Priority, Concern: t.Concern,
			FanOut: fanOut[t.ID], Depth: a.Depth[t.ID], IsCritical: a.Critical[t.ID],
		})
	}
	return out, nil
}

// CriticalPath computes the enriched cross-store critical path (overview
// only, D12) plus any cycles found.
func (s *Service) CriticalPath(ctx context.Context) (*CriticalPath, error) {
	pairs, err := s.taskDAG(ctx)
	if err != nil {
		return nil, err
	}
	a := Analyze(pairs, nil)
	cp := &CriticalPath{Cycles: a.Cycles}
	for id, crit := range a.Critical {
		if !crit {
			continue
		}
		cp.Tasks = append(cp.Tasks, FrontierTask{
			ID: id, Depth: a.Depth[id], FanOut: a.FanOut[id], IsCritical: true,
		})
		if a.Depth[id] > cp.MaxDepth {
			cp.MaxDepth = a.Depth[id]
		}
	}
	sort.Slice(cp.Tasks, func(i, j int) bool {
		if cp.Tasks[i].Depth != cp.Tasks[j].Depth {
			return cp.Tasks[i].Depth < cp.Tasks[j].Depth
		}
		return cp.Tasks[i].ID < cp.Tasks[j].ID
	})
	return cp, nil
}

// Drift bundles the three 4.1 reads.
type Drift struct {
	Violations   []DriftEdge `json:"violations"`
	StaleIntent  []DriftEdge `json:"stale_intent"`
	Acknowledged []Deviation `json:"acknowledged,omitempty"`
}

// DriftReport runs 4.1 (both directions), optionally including deviations.
func (s *Service) DriftReport(ctx context.Context, acknowledged bool) (*Drift, error) {
	if s.Graph == nil {
		return nil, ErrNoGraph
	}
	v, err := Violations(ctx, s.Graph)
	if err != nil {
		return nil, err
	}
	st, err := StaleIntent(ctx, s.Graph)
	if err != nil {
		return nil, err
	}
	d := &Drift{Violations: v, StaleIntent: st}
	if acknowledged {
		if d.Acknowledged, err = Acknowledged(ctx, s.Graph); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// GapReport runs 4.2.
func (s *Service) GapReport(ctx context.Context) ([]Gap, error) {
	if s.Graph == nil {
		return nil, ErrNoGraph
	}
	return Gaps(ctx, s.Graph)
}

// Overview is the one-screen roll-up.
type Overview struct {
	Violations   int            `json:"violations"`
	StaleIntent  int            `json:"stale_intent"`
	Gaps         int            `json:"gaps"`
	FrontierSize int            `json:"frontier_size"`
	Cycles       [][]string     `json:"cycles,omitempty"`
	CriticalHead *FrontierTask  `json:"critical_head,omitempty"`
	GraphEnabled bool           `json:"graph_enabled"`
}

// Roll computes the `lode overview` counts. Graph-backed counts degrade to
// zero with GraphEnabled=false rather than failing the whole screen.
func (s *Service) Roll(ctx context.Context, projectID string) (*Overview, error) {
	o := &Overview{GraphEnabled: s.Graph != nil}
	fr, err := s.Frontier(ctx, projectID)
	if err != nil {
		return nil, err
	}
	o.FrontierSize = len(fr)
	for i := range fr {
		if fr[i].IsCritical {
			o.CriticalHead = &fr[i]
			break
		}
	}
	cp, err := s.CriticalPath(ctx)
	if err != nil {
		return nil, err
	}
	o.Cycles = cp.Cycles
	if s.Graph != nil {
		d, err := s.DriftReport(ctx, false)
		if err != nil {
			return nil, err
		}
		o.Violations, o.StaleIntent = len(d.Violations), len(d.StaleIntent)
		g, err := s.GapReport(ctx)
		if err != nil {
			return nil, err
		}
		o.Gaps = len(g)
	}
	return o, nil
}
```

(Add `"sort"` to the imports.)

- [ ] **Step 3: Write the Oxigraph acceptance test**

`internal/overview/oxigraph_test.go`, using the knowledge-graph plan's
harness (`internal/graph/graphtest`; it skips unless `TEST_SPARQL_URL` is
set — adapt the constructor call to the harness's landed API):

```go
package overview_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graph"
	"github.com/sunstoneinstitute/worklode/internal/graph/graphtest"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/overview"
)

const (
	compA = "https://worklode.io/ns/id/component/github.com/acme/app/a"
	compB = "https://worklode.io/ns/id/component/github.com/acme/app/b"
	compC = "https://worklode.io/ns/id/component/github.com/acme/app/c"
)

// seed plants: declared A→B; observed A→B (agreement), A→C (violation),
// and declared B→C with no observed counterpart (stale intent). Components
// are typed; only A is governed by a doc (B, C are doc gaps).
func seed(t *testing.T, c *graph.Client) {
	t.Helper()
	declared := iri.DeclaredGraph("adr-test-0001")
	observed := iri.ObservedGraph("go-imports")
	update := fmt.Sprintf(`
	PREFIX wl:  <https://worklode.io/ns/ontology#>
	PREFIX dct: <http://purl.org/dc/terms/>
	INSERT DATA {
	  GRAPH <%s> {
	    <%s> dct:requires <%s> .
	    <%s> dct:requires <%s> .
	    <urn:doc:1> a wl:DesignDoc ; wl:governs <%s> .
	  }
	  GRAPH <%s> {
	    <%s> dct:requires <%s> .
	    <%s> dct:requires <%s> .
	    <%s> a wl:Component . <%s> a wl:Component . <%s> a wl:Component .
	  }
	}`, declared, compA, compB, compB, compC, compA,
		observed, compA, compB, compA, compC, compA, compB, compC)
	if err := c.Update(context.Background(), update); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestDriftBothDirections(t *testing.T) {
	c := graphtest.Client(t)
	seed(t, c)

	v, err := overview.Violations(context.Background(), c)
	if err != nil {
		t.Fatalf("Violations: %v", err)
	}
	if len(v) != 1 || v[0].From != compA || v[0].To != compC {
		t.Fatalf("violations = %+v; want exactly A requires C", v)
	}

	st, err := overview.StaleIntent(context.Background(), c)
	if err != nil {
		t.Fatalf("StaleIntent: %v", err)
	}
	if len(st) != 1 || st[0].From != compB || st[0].To != compC {
		t.Fatalf("stale intent = %+v; want exactly B requires C", st)
	}
}

func TestDeviationSuppressesUntilExpiry(t *testing.T) {
	c := graphtest.Client(t)
	seed(t, c)
	declared := iri.DeclaredGraph("adr-test-0001")

	// Active deviation for A→C (expires next year): 4.1 must drop it.
	future := time.Now().UTC().AddDate(1, 0, 0).Format("2006-01-02")
	plant := fmt.Sprintf(`
	PREFIX wl:  <https://worklode.io/ns/ontology#>
	PREFIX dct: <http://purl.org/dc/terms/>
	PREFIX rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#>
	PREFIX xsd: <http://www.w3.org/2001/XMLSchema#>
	INSERT DATA { GRAPH <%s> {
	  <urn:dev:1> a wl:AcceptedDeviation ;
	      rdf:subject <%s> ; rdf:predicate dct:requires ; rdf:object <%s> ;
	      wl:sanctionedBy <urn:doc:1> ;
	      dct:valid "%s"^^xsd:date .
	} }`, declared, compA, compC, future)
	if err := c.Update(context.Background(), plant); err != nil {
		t.Fatalf("plant deviation: %v", err)
	}

	v, err := overview.Violations(context.Background(), c)
	if err != nil {
		t.Fatalf("Violations: %v", err)
	}
	if len(v) != 0 {
		t.Fatalf("violations = %+v; the active deviation must suppress A→C", v)
	}
	// Stale intent is unaffected by suppression (the deviation never
	// asserts the edge into the declared layer).
	st, _ := overview.StaleIntent(context.Background(), c)
	if len(st) != 1 {
		t.Fatalf("stale intent = %+v; must be unchanged by the deviation", st)
	}
	// It is listable.
	ack, err := overview.Acknowledged(context.Background(), c)
	if err != nil || len(ack) != 1 || ack[0].Expired {
		t.Fatalf("acknowledged = %+v, %v; want one active deviation", ack, err)
	}

	// Expire it: the violation re-surfaces.
	expire := fmt.Sprintf(`
	PREFIX dct: <http://purl.org/dc/terms/>
	PREFIX xsd: <http://www.w3.org/2001/XMLSchema#>
	DELETE WHERE { GRAPH <%s> { <urn:dev:1> dct:valid ?v } } ;
	INSERT DATA { GRAPH <%s> { <urn:dev:1> dct:valid "2020-01-01"^^xsd:date } }`,
		declared, declared)
	if err := c.Update(context.Background(), expire); err != nil {
		t.Fatalf("expire deviation: %v", err)
	}
	v, _ = overview.Violations(context.Background(), c)
	if len(v) != 1 {
		t.Fatalf("violations after expiry = %+v; want A→C re-surfaced", v)
	}
	ack, _ = overview.Acknowledged(context.Background(), c)
	if len(ack) != 1 || !ack[0].Expired {
		t.Fatalf("acknowledged after expiry = %+v; want it listed as expired", ack)
	}
}

func TestGaps(t *testing.T) {
	c := graphtest.Client(t)
	seed(t, c)
	gaps, err := overview.Gaps(context.Background(), c)
	if err != nil {
		t.Fatalf("Gaps: %v", err)
	}
	// B and C have no governing doc; A does.
	if len(gaps) != 2 {
		t.Fatalf("gaps = %+v; want the two ungoverned components", gaps)
	}
}
```

Note: `graphtest` must give each test an isolated graph set (its plan says
"unique graphs"). If the landed harness namespaces graphs per test, route
`iri.DeclaredGraph`/`ObservedGraph` values through its namespacing helper;
if it wipes the store per test, the code above works as written.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/overview/ -v` (query/text tests) and
`docker compose up -d oxigraph && TEST_SPARQL_URL=http://localhost:7878 go test ./internal/overview/ -v`
Expected: PASS; Oxigraph tests skip without `TEST_SPARQL_URL`.

- [ ] **Step 5: Commit**

```bash
git add internal/overview
git commit -m "Add the standing drift, gap and overview queries"
```

---

## Task 12: API surface

**Files:**
- Create: `internal/api/overview.go`
- Test: `internal/api/overview_test.go`
- Modify: `internal/api/server.go`, `internal/cmd/serve.go`

- [ ] **Step 1: Write the failing test**

`internal/api/overview_test.go` (`package api_test`, reusing `newTestServer`
and `doReq`):

```go
func TestOverviewEndpointsRequireAuth(t *testing.T) {
	_, h, _ := newTestServer(t)
	for _, path := range []string{
		"/api/v1/overview", "/api/v1/drift", "/api/v1/gaps",
		"/api/v1/frontier", "/api/v1/critical-path",
	} {
		if rec := doReq(t, h, http.MethodGet, path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without token: %d; want 401", path, rec.Code)
		}
	}
}

func TestGraphBackedReadsWithoutGraphAre503(t *testing.T) {
	_, h, token := newTestServer(t) // test server config has no graph client
	for _, path := range []string{"/api/v1/drift", "/api/v1/gaps"} {
		if rec := doReq(t, h, http.MethodGet, path, token, nil); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s without graph: %d; want 503", path, rec.Code)
		}
	}
}

func TestFrontierEndpointOrdersAndAnnotates(t *testing.T) {
	st, h, token := newTestServer(t)
	seedReadyTaskAPI(t, st, "WL-1", "low")
	seedReadyTaskAPI(t, st, "WL-2", "critical")

	rec := doReq(t, h, http.MethodGet, "/api/v1/frontier", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("frontier: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Tasks []struct {
			ID     string `json:"id"`
			FanOut int    `json:"fan_out"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tasks) != 2 || resp.Tasks[0].ID != "WL-2" {
		t.Fatalf("frontier = %+v; want WL-2 (critical) first", resp.Tasks)
	}
}

func TestDeriveEndpointRequiresAdmin(t *testing.T) {
	_, h, token := newTestServer(t) // non-admin token
	if rec := doReq(t, h, http.MethodPost, "/api/v1/derive", token, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("derive as non-admin: %d; want 403", rec.Code)
	}
}
```

(`seedReadyTaskAPI` mirrors the task-creation helper the existing
`internal/api/tasks_test.go` uses; write it against that file's actual
helper. If `newTestServer`'s token is admin, follow the pattern the admin
tests use to obtain a non-admin token for the 403 case.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run 'TestOverview|TestGraphBacked|TestFrontierEndpoint|TestDeriveEndpoint'`
Expected: FAIL — routes unregistered (404s).

- [ ] **Step 3: Write the handlers**

`internal/api/overview.go`:

```go
package api

import (
	"errors"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/overview"
)

// mapOverviewErr converts service errors: an unconfigured graph is 503
// (the deployment lacks the endpoint, the request was fine).
func (s *server) mapOverviewErr(w http.ResponseWriter, err error) {
	if errors.Is(err, overview.ErrNoGraph) {
		writeErr(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeErr(w, http.StatusInternalServerError, err.Error())
}

// getOverview handles GET /api/v1/overview?project=<id>.
func (s *server) getOverview(w http.ResponseWriter, r *http.Request) {
	o, err := s.overview.Roll(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		s.mapOverviewErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// getDrift handles GET /api/v1/drift?acknowledged=1.
func (s *server) getDrift(w http.ResponseWriter, r *http.Request) {
	d, err := s.overview.DriftReport(r.Context(), r.URL.Query().Get("acknowledged") != "")
	if err != nil {
		s.mapOverviewErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// getGaps handles GET /api/v1/gaps.
func (s *server) getGaps(w http.ResponseWriter, r *http.Request) {
	g, err := s.overview.GapReport(r.Context())
	if err != nil {
		s.mapOverviewErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"gaps": g})
}

// getFrontier handles GET /api/v1/frontier?project=<id> — the read-only
// mirror of the backbone frontier, pre-sorted by the D9 key.
func (s *server) getFrontier(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.overview.Frontier(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		s.mapOverviewErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

// getCriticalPath handles GET /api/v1/critical-path.
func (s *server) getCriticalPath(w http.ResponseWriter, r *http.Request) {
	cp, err := s.overview.CriticalPath(r.Context())
	if err != nil {
		s.mapOverviewErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cp)
}

// postDerive handles POST /api/v1/derive: run the server-side derivers
// (pr-affects, deploy) on demand. Admin-gated — it writes to the graph.
func (s *server) postDerive(w http.ResponseWriter, r *http.Request) {
	if s.runDerivers == nil {
		writeErr(w, http.StatusServiceUnavailable, overview.ErrNoGraph.Error())
		return
	}
	results, err := s.runDerivers(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
```

In `internal/api/server.go`:
- add fields to the `server` struct: `overview *overview.Service` and
  `runDerivers func(context.Context) ([]derive.Result, error)`;
- add to `Config`: `Overview *overview.Service`,
  `RunDerivers func(context.Context) ([]derive.Result, error)`; copy both in
  `NewServer` (default `Overview` to `&overview.Service{Store: st}` when
  nil, so the frontier works without any graph);
- register routes next to the existing `/api/v1` block:

```go
	mux.Handle("GET /api/v1/overview", s.auth(s.getOverview))
	mux.Handle("GET /api/v1/drift", s.auth(s.getDrift))
	mux.Handle("GET /api/v1/gaps", s.auth(s.getGaps))
	mux.Handle("GET /api/v1/frontier", s.auth(s.getFrontier))
	mux.Handle("GET /api/v1/critical-path", s.auth(s.getCriticalPath))
	mux.Handle("POST /api/v1/derive", s.auth(requireAdmin(s.postDerive)))
```

In `internal/cmd/serve.go`, where the knowledge-graph plan constructs the
projector's graph client from `LODE_GRAPH_URL`/`LODE_GRAPH_TOKEN_URL`, reuse
the same client for the overview service and derivers:

```go
	svc := &overview.Service{Store: st, Graph: graphClient} // graphClient may be nil
	cfg.Overview = svc
	if graphClient != nil && appAuth != nil {
		reader := &derive.GitHubReader{Auth: appAuth}
		cfg.RunDerivers = func(ctx context.Context) ([]derive.Result, error) {
			var out []derive.Result
			doc, err := derive.DeployTriples(ctx, st)
			if err != nil {
				return out, err
			}
			res, err := derive.Run(ctx, graphClient, iri.ObservedGraph("deploy"), doc)
			if err != nil {
				return out, err
			}
			out = append(out, res)

			prs, err := st.TaskPRs(ctx)
			if err != nil {
				return out, err
			}
			doc, _, err = derive.PRAffectsTriples(ctx, prs, reader)
			if err != nil {
				return out, err
			}
			res, err = derive.Run(ctx, graphClient, iri.ObservedGraph("pr-affects"), doc)
			if err != nil {
				return out, err
			}
			return append(out, res), nil
		}
	}
```

(`appAuth` is built the way `internal/api/server.go:85` builds it; hoist a
shared constructor if serve.go does not already have one.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/api/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/api internal/cmd
git commit -m "Expose overview, drift, gaps, frontier and derive over the API"
```

---

## Task 13: CLI surface

**Files:**
- Modify: `internal/cli/client.go`, `internal/cmd/overview.go`, `internal/cmd/root.go`
- Test: `internal/cmd/overview_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/cmd/overview_test.go`, in the style of the existing
fake-server command tests (`internal/cmd/task_test.go` has the pattern for
standing up a fake `LODE_SERVER` and capturing command output):

```go
func TestDriftCommandJSON(t *testing.T) {
	srv := fakeServer(t, map[string]string{
		"GET /api/v1/drift": `{"violations":[{"from":"urn:a","to":"urn:b"}],"stale_intent":[]}`,
	})
	out := runLode(t, srv, "drift", "--json")
	if !strings.Contains(out, `"from": "urn:a"`) && !strings.Contains(out, `"from":"urn:a"`) {
		t.Fatalf("drift --json output missing the violation:\n%s", out)
	}
}

func TestFrontierCommandPassesProject(t *testing.T) {
	var gotQuery string
	srv := fakeServerFunc(t, func(r *http.Request) (int, string) {
		gotQuery = r.URL.RawQuery
		return 200, `{"tasks":[]}`
	})
	runLode(t, srv, "frontier", "--project", "worklode", "--json")
	if !strings.Contains(gotQuery, "project=worklode") {
		t.Fatalf("query = %q; want project=worklode", gotQuery)
	}
}

func TestDriftAcknowledgedFlag(t *testing.T) {
	var gotQuery string
	srv := fakeServerFunc(t, func(r *http.Request) (int, string) {
		gotQuery = r.URL.RawQuery
		return 200, `{"violations":[],"stale_intent":[],"acknowledged":[]}`
	})
	runLode(t, srv, "drift", "--acknowledged", "--json")
	if !strings.Contains(gotQuery, "acknowledged=1") {
		t.Fatalf("query = %q; want acknowledged=1", gotQuery)
	}
}
```

(`fakeServer`/`fakeServerFunc`/`runLode` — reuse or extract the equivalents
already living in this package's tests; if none are general enough, write
these three helpers once at the top of the file.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cmd/ -run 'TestDriftCommand|TestFrontierCommand|TestDriftAcknowledged'`
Expected: FAIL — unknown commands.

- [ ] **Step 3: Add the client methods**

In `internal/cli/client.go` (next to the other GET helpers; follow the
`ListIssues` raw+decoded return convention):

```go
// getRaw fetches path with optional query values, returning the raw JSON.
func (c *Client) getRaw(ctx context.Context, path string, q url.Values) ([]byte, error) {
	return c.do(ctx, http.MethodGet, withQuery(path, q), nil)
}

// Overview, Drift, Gaps, Frontier, CriticalPath fetch the spec 007 read
// surface; RunDerive triggers the server-side derivers.
func (c *Client) Overview(ctx context.Context, project string) ([]byte, error) {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	return c.getRaw(ctx, "/api/v1/overview", q)
}

func (c *Client) Drift(ctx context.Context, acknowledged bool) ([]byte, error) {
	q := url.Values{}
	if acknowledged {
		q.Set("acknowledged", "1")
	}
	return c.getRaw(ctx, "/api/v1/drift", q)
}

func (c *Client) Gaps(ctx context.Context) ([]byte, error) {
	return c.getRaw(ctx, "/api/v1/gaps", nil)
}

func (c *Client) Frontier(ctx context.Context, project string) ([]byte, error) {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	return c.getRaw(ctx, "/api/v1/frontier", q)
}

func (c *Client) CriticalPath(ctx context.Context) ([]byte, error) {
	return c.getRaw(ctx, "/api/v1/critical-path", nil)
}

func (c *Client) RunDerive(ctx context.Context) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/derive", nil)
}
```

- [ ] **Step 4: Add the commands**

Append to `internal/cmd/overview.go`, using the package's established
helpers exactly as `internal/cmd/board.go:11-53` does: client via
`newAPIClientWithConfig()` (`root.go:61`), scope via `scopeFlags` +
`addScopeFlags(cmd, &scope, "…")` + `resolveScope(cmd.Context(), cmd, c,
cfg, &scope)` (`scope.go`), JSON via the persistent root `--json` flag read
by `jsonOut(cmd)` and emitted with `printRaw(cmd, raw)` (`root.go:36,45,75`).
Registration follows the package convention: a `func init() {
rootCmd.AddCommand(…) }` (`board.go:52`). `lode status` in `lifecycle.go`
is untouched — `lode overview` is the roll-up the spec names.

```go
// newOverviewCmd wires `lode overview` — the one-screen roll-up.
func newOverviewCmd() *cobra.Command {
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:   "overview",
		Short: "One-screen roll-up: drift counts, gaps, frontier, critical head",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			raw, err := c.Overview(cmd.Context(), sc.Project)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			var o struct {
				Violations   int        `json:"violations"`
				StaleIntent  int        `json:"stale_intent"`
				Gaps         int        `json:"gaps"`
				FrontierSize int        `json:"frontier_size"`
				Cycles       [][]string `json:"cycles"`
				CriticalHead *struct {
					ID string `json:"id"`
				} `json:"critical_head"`
				GraphEnabled bool `json:"graph_enabled"`
			}
			if err := json.Unmarshal(raw, &o); err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
			fmt.Fprintf(w, "violations\t%d\nstale intent\t%d\ngaps\t%d\nready frontier\t%d\n",
				o.Violations, o.StaleIntent, o.Gaps, o.FrontierSize)
			if o.CriticalHead != nil {
				fmt.Fprintf(w, "critical head\t%s\n", o.CriticalHead.ID)
			}
			for _, cyc := range o.Cycles {
				fmt.Fprintf(w, "CYCLE\t%s\n", strings.Join(cyc, " -> "))
			}
			if !o.GraphEnabled {
				fmt.Fprintf(w, "note\tgraph not configured; drift/gap counts unavailable\n")
			}
			return w.Flush()
		},
	}
	addScopeFlags(cmd, &scope, "roll up one project")
	return cmd
}

// newDriftCmd wires `lode drift [--component <iri>] [--acknowledged]`.
func newDriftCmd() *cobra.Command {
	var acknowledged bool
	var component string
	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Architectural drift: violations and stale intent (spec 007 §4.1)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			raw, err := c.Drift(cmd.Context(), acknowledged)
			if err != nil {
				return err
			}
			if jsonOut(cmd) && component == "" {
				printRaw(cmd, raw)
				return nil
			}
			var d struct {
				Violations  []struct{ From, To string } `json:"violations"`
				StaleIntent []struct{ From, To string } `json:"stale_intent"`
				Acknowledged []struct {
					From, To     string
					SanctionedBy string `json:"sanctioned_by"`
					ValidUntil   string `json:"valid_until"`
					Expired      bool
				} `json:"acknowledged"`
			}
			if err := json.Unmarshal(raw, &d); err != nil {
				return err
			}
			keep := func(from string) bool { return component == "" || from == component }
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
			fmt.Fprintln(w, "# violations (observed - declared - acknowledged)")
			for _, e := range d.Violations {
				if keep(e.From) {
					fmt.Fprintf(w, "%s\trequires\t%s\n", e.From, e.To)
				}
			}
			fmt.Fprintln(w, "# stale intent (declared - observed)")
			for _, e := range d.StaleIntent {
				if keep(e.From) {
					fmt.Fprintf(w, "%s\trequires\t%s\n", e.From, e.To)
				}
			}
			if acknowledged {
				fmt.Fprintln(w, "# acknowledged deviations")
				for _, a := range d.Acknowledged {
					state := "active"
					if a.Expired {
						state = "EXPIRED"
					}
					fmt.Fprintf(w, "%s\trequires\t%s\tby %s\t%s %s\n",
						a.From, a.To, a.SanctionedBy, state, a.ValidUntil)
				}
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&acknowledged, "acknowledged", false, "include accepted deviations (active + expired)")
	cmd.Flags().StringVar(&component, "component", "", "filter edges from this component IRI")
	return cmd
}

// newGapsCmd wires `lode gaps` (spec 007 §4.2).
func newGapsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gaps",
		Short: "Doc gaps and unmatched-path coverage gaps",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			raw, err := c.Gaps(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			var g struct {
				Gaps []struct{ Component, Repo, Path string } `json:"gaps"`
			}
			if err := json.Unmarshal(raw, &g); err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
			for _, x := range g.Gaps {
				if x.Component != "" {
					fmt.Fprintf(w, "no governing doc\t%s\n", x.Component)
				} else {
					fmt.Fprintf(w, "unmatched path\t%s\t%s\n", x.Repo, x.Path)
				}
			}
			return w.Flush()
		},
	}
}

// newFrontierCmd wires `lode frontier` (alias `ready`): the ranked ready
// set, pre-sorted by the D9 ordering the backbone computes (spec 007 §4.5).
func newFrontierCmd() *cobra.Command {
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:     "frontier",
		Aliases: []string{"ready"},
		Short:   "Ready, unblocked tasks in pickup order",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			raw, err := c.Frontier(cmd.Context(), sc.Project)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			var f struct {
				Tasks []struct {
					ID, Title, Priority, Concern string
					FanOut                       int  `json:"fan_out"`
					Depth                        int  `json:"depth"`
					IsCritical                   bool `json:"is_critical"`
				} `json:"tasks"`
			}
			if err := json.Unmarshal(raw, &f); err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tPRIO\tCONCERN\tFAN-OUT\tDEPTH\tCRIT\tTITLE")
			for _, t := range f.Tasks {
				crit := ""
				if t.IsCritical {
					crit = "*"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
					t.ID, t.Priority, t.Concern, t.FanOut, t.Depth, crit, t.Title)
			}
			return w.Flush()
		},
	}
	addScopeFlags(cmd, &scope, "list one project's frontier")
	return cmd
}

// newCriticalPathCmd wires `lode critical-path [--task <id>]`; cycles are
// findings, not silent drops (spec 007 §Cycle handling). --task narrows the
// table to that task's row (its depth and fan-out), client-side.
func newCriticalPathCmd() *cobra.Command {
	var task string
	cmd := &cobra.Command{
		Use:   "critical-path",
		Short: "Estimate-free critical path over blocks + requires (D12)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			raw, err := c.CriticalPath(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut(cmd) && task == "" {
				printRaw(cmd, raw)
				return nil
			}
			var cp struct {
				MaxDepth int `json:"max_depth"`
				Tasks    []struct {
					ID     string
					Depth  int `json:"depth"`
					FanOut int `json:"fan_out"`
				} `json:"tasks"`
				Cycles [][]string `json:"cycles"`
			}
			if err := json.Unmarshal(raw, &cp); err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
			if task == "" {
				fmt.Fprintf(w, "chain length\t%d\n", cp.MaxDepth)
			}
			for _, t := range cp.Tasks {
				if task != "" && t.ID != task {
					continue
				}
				fmt.Fprintf(w, "%d\t%s\tfan-out %d\n", t.Depth, t.ID, t.FanOut)
			}
			for _, cyc := range cp.Cycles {
				fmt.Fprintf(w, "CYCLE\t%s\n", strings.Join(cyc, " -> "))
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&task, "task", "", "show only this task's criticality")
	return cmd
}

func init() {
	rootCmd.AddCommand(newOverviewCmd(), newDriftCmd(), newGapsCmd(),
		newFrontierCmd(), newCriticalPathCmd())
}
```

(`newDeriveCmd` was already registered by Task 6's `init`; keep the two
`init` funcs or merge them — either compiles.)

Imports for the file grow `encoding/json`, `text/tabwriter` — the client
and scope helpers are already package-local (`root.go`, `scope.go`).

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/cmd/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/cli internal/cmd
git commit -m "Add the lode overview, drift, gaps, frontier and critical-path commands"
```

---

## Task 14: Drift board web view

**Files:**
- Create: `internal/api/templates/drift.html`
- Modify: `internal/api/web.go`, `internal/api/server.go`
- Test: `internal/api/web_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/api/web_test.go`, following its existing page-test
pattern:

```go
func TestDriftPageRendersWithoutGraph(t *testing.T) {
	_, h, _ := newTestServer(t)
	rec := doReq(t, h, http.MethodGet, "/drift", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /drift: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "knowledge graph not configured") {
		t.Fatalf("page without graph must say so:\n%s", rec.Body.String())
	}
	// Read-only by construction: no form, no POST affordance.
	if strings.Contains(rec.Body.String(), "<form") {
		t.Fatal("drift page contains a mutation affordance")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestDriftPage`
Expected: FAIL — 404.

- [ ] **Step 3: Implement**

`internal/api/templates/drift.html` — same skeleton as `board.html` (head,
nav, styles); body renders four of the spec's five views (spec status is
deferred with 4.3/4.4). The frontier and critical path come from the
backbone and render even without a graph endpoint:

```html
<h2>Ready frontier</h2>
<table>
  <tr><th>id</th><th>priority</th><th>concern</th><th>fan-out</th><th>depth</th><th>critical</th></tr>
  {{range .Frontier}}<tr><td>{{.ID}}</td><td>{{.Priority}}</td><td>{{.Concern}}</td>
    <td>{{.FanOut}}</td><td>{{.Depth}}</td><td>{{if .IsCritical}}*{{end}}</td></tr>{{end}}
</table>

<h2>Critical path <small>chain length {{.CriticalPath.MaxDepth}}</small></h2>
<table>{{range .CriticalPath.Tasks}}<tr><td>{{.Depth}}</td><td>{{.ID}}</td><td>fan-out {{.FanOut}}</td></tr>{{end}}</table>
{{range .CriticalPath.Cycles}}<p class="error">cycle: {{range .}}{{.}} {{end}}</p>{{end}}

{{if not .GraphEnabled}}
  <p class="empty">knowledge graph not configured — set LODE_GRAPH_URL and run the derivers for drift and gap views.</p>
{{else}}
  <h2>Violations <small>observed − declared − acknowledged</small></h2>
  <table>{{range .Drift.Violations}}<tr><td>{{.From}}</td><td>requires</td><td>{{.To}}</td></tr>{{end}}</table>
  <h2>Stale intent <small>declared − observed</small></h2>
  <table>{{range .Drift.StaleIntent}}<tr><td>{{.From}}</td><td>requires</td><td>{{.To}}</td></tr>{{end}}</table>
  <h2>Gaps</h2>
  <table>{{range .Gaps}}<tr><td>{{.Component}}{{.Repo}}</td><td>{{.Path}}</td></tr>{{end}}</table>
{{end}}
```

In `internal/api/web.go` add `driftPage` (mirror `boardPage`): build the
view model from `s.overview.Frontier`, `s.overview.CriticalPath`,
`s.overview.DriftReport` and `s.overview.GapReport`, treating
`overview.ErrNoGraph` from the latter two as `GraphEnabled: false` rather
than an error; execute a `tmplDrift` parsed in `NewServer` like the other
three templates. Register in `server.go` next to the other web routes:

```go
	mux.HandleFunc("GET /drift", s.webAuth(s.driftPage))
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/api/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "Add the read-only drift board web view"
```

---

## Task 15: README and final verification

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document**

Add a "Drift & overview" section to `README.md`: the two-layer model in two
sentences; the deriver contract (idempotent, full-replace, hash
short-circuit, one graph each); `lode derive` in CI for repo-local sources
and `POST /api/v1/derive` for server-side ones; the five read commands with
`--json`; `LODE_GRAPH_URL`. Keep it under 40 lines.

- [ ] **Step 2: Full suite**

Run: `docker compose up -d postgres oxigraph && TEST_SPARQL_URL=http://localhost:7878 go test ./...`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "Document the drift and overview surface"
```

---

## Acceptance criteria → task map

The full-series map is split across the three parts; this part covers:

| Spec acceptance criterion | Covered by |
|---|---|
| Two-layer round-trip on a seeded graph | Task 11 (Oxigraph tests) |
| 4.1 both directions on a seeded graph | Task 11 (`TestDriftBothDirections`) |
| Drift suppression, `--acknowledged`, expiry re-surfacing | Task 11 (`TestDeviationSuppressesUntilExpiry`), Task 13 |
| 4.3 / 4.4 | **deferred to the 014 plan** (superseded/re-pointed by 014 §6) |
| Critical path correct; cycle detected, excluded, surfaced | Task 10 |
| Ordering contract: frontier matches the backbone | Task 12 (the store-level check landed in part 2, Task 7) |
| Deterministic `--json` everywhere; read-only web view, no mutation affordance | Tasks 13–14 |
