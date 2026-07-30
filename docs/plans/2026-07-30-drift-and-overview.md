# Drift & overview (spec 007) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make drift queryable: observed-layer derivers that materialize
reality into `observed/*` named graphs, the standing queries over the
declared-vs-observed diff, estimate-free critical path, and the
`lode overview`/`drift`/`gaps`/`frontier`/`critical-path` + web surface.

**Architecture:** Derivers are pure row/file→triple functions in a new
`internal/derive` package, serialized as deterministic N-Triples
(`internal/graphproj.Render`) and written by a shared runner that does an
atomic Graph Store Protocol `PUT` per source graph, short-circuited by a
content hash stored as a triple inside the graph itself (no Postgres
migration). Repo-local derivers (go-imports, repo-layout) run via a new
`lode derive` command in CI; DB-backed derivers (pr-affects, deploy) run
server-side behind an admin `POST /api/v1/derive`. Standing queries live in a
new `internal/overview` package as SPARQL reads through
`internal/graph.Client`; critical path is computed in Go on each read by
joining backbone `blocks` edges with KG `wl:dependsOn` edges. The
authoritative ready frontier stays on the backbone (`store.rankTasks`); the
overview only mirrors it.

**Tech Stack:** Go 1.26, cobra CLI, PostgreSQL via `database/sql`,
standard-library testing, SPARQL 1.1 Protocol + Graph Store Protocol over
`net/http`, Oxigraph (docker) as the test endpoint, `html/template` for the
web view.

**Spec:** `docs/specs/007-drift-and-overview.md`, read with its amendments:
`docs/specs/014-design-documents-as-graph-objects.md` §5–§6, §10 and
`docs/specs/015-runtime-layer.md` §2–§6. All `ls:`/`lsc:`/`lsid:` prefixes in
the spec read as `wl:`/`wlc:`/`wlid:` (014 §1).

---

## Prerequisites — sibling plans this one builds on

This plan assumes the following 2026-07-30 plans have executed. It re-plans
none of their packages; it only calls them.

| Plan | Provides (consumed here) |
|---|---|
| `docs/plans/2026-07-30-knowledge-graph.md` | `internal/graph` (`Client.Update/Select/Ask/Load`, `Triple`, `graphtest` Oxigraph harness), `rdf/wl/*.ttl`, projector env vars `LODE_GRAPH_URL`/`LODE_GRAPH_TOKEN_URL`, migration 0008 |
| `docs/plans/2026-07-30-platform-graph-design.md` | `internal/kg/iri` (IRI grammar, `GraphNS`), `internal/kg/manifest` (`Parse`, `(*Manifest).Match` — first-match-wins `**` globs over `.worklode/components.yaml`, spec 007 §2), Worklode's own manifest |
| `docs/plans/2026-07-30-runtime-layer.md` | `internal/graphproj` (`Triple`, `Render`, `ArtifactTriples`, `DeploymentTriples`, `EnvironmentTriples`, `CommitTriples`, `ReleaseCoversTriples`, `CommitKnown`) — exactly the row→triple functions 015 says "007's observed/deploy deriver will emit" |
| `docs/plans/2026-07-30-reconciliation.md` | nothing consumed directly; noted because it owns `lode doctor` and `internal/reconcile`, which this plan must not touch |
| `docs/plans/2026-07-30-design-documents-as-graph-objects.md` | nothing consumed; owns everything this plan defers to "the 014 plan" — `internal/kg/implements`, the `observed/repo-implements` deriver, sections, `lode doc` |
| `docs/plans/2026-07-30-data-platform-kg-requirements.md` | nothing consumed; owns `internal/graphserver` (the prod graph-server client — GSP + read-only `/sparql` only) and the spec 009 hand-off issues |

If a prerequisite type's final name differs slightly from the plan text it
came from (these plans are unlanded), adapt the call site mechanically — the
responsibility split stands.

## Already implemented vs. remaining

Shipped on main today, reused as-is:

- Ready-set + ranking (the authoritative frontier, D8/D9):
  `internal/store/ranking.go:61` (`readyCandidates`), `:179` (`rankTasks`,
  key `(is_critical, concern_rank, priority, fan_out)` where backbone
  `is_critical` = `priority == "critical"`, `:185`), `:18`
  (`BlockingFanOut`, transitive over `blocks`).
- Deploy/runtime ingestion (deriver 4's input, D6): `internal/hooks/flux.go`,
  `internal/hooks/deployment.go`, `internal/hooks/github.go:400`
  (`applyRelease`); rows in `internal/store/artifacts.go` (`Artifact`,
  `Deployment`, `ListDeployments`), `internal/store/delivery.go`
  (`main_commits`, `release_frontiers`).
- PR→Task join (deriver 3's join): `internal/store/changes.go:99`
  (`TaskIDFromRef`, branch `wt/<id>-<slug>`), `:118` (`TaskIDFromBody`);
  `UpsertPR` binds `pull_requests.task_id` at ingest. The spec's resolved
  Q1 (join via mirrored Issues / `Closes #N`) waits on Task↔Issue mirroring
  (004/008); until then the existing relational join is the join.
- Read-only web UI to extend: `internal/api/server.go:234-239` routes,
  `internal/api/web.go`, `internal/api/templates/`.
- GitHub App installation tokens for server-side API reads:
  `internal/githubauth/app.go:94` (`InstallationToken`), held by the api
  server (`internal/api/server.go:116`).

Not implemented anywhere (this plan's scope): every deriver, every standing
query, critical path, `lode overview/drift/gaps/frontier/critical-path`, the
drift web view.

**Spec correction found while grounding this plan:** spec 007 deriver 3 says
PR changed-file lists are "already ingested by `internal/hooks/github.go`".
They are not — no changed-file data exists anywhere in the schema or hooks
(the webhook payload doesn't carry file lists). This plan fetches them from
the GitHub API at derive time instead (design call 4 below).

## Scope

**In:** the two-layer named-graph wiring; derivers 1–4 (go-imports,
repo-layout, pr-affects, deploy); standing queries 4.1 (both directions +
deviation suppression), 4.2 (doc gaps + unmatched paths), 4.5 (frontier
mirror); critical path v1 (depth, fan-out, cycle finding); the CLI surface
`overview`/`drift`/`gaps`/`frontier`/`critical-path`; web views for the
drift board, doc gaps, ready frontier and critical path. (The spec's fifth
web view — spec status — is 4.3/4.4-dependent and deferred with them.)

**Out (owned elsewhere — do not build):**

- Queries 4.3 and 4.4 and `lode specs --drifted/--unimplemented`: 014 §6
  supersedes 4.3 with the section-scoped stale-claim query and re-points 4.4
  at per-section coverage; both need `wl:Section`, `wl:lastRevisedIn` and
  `.worklode/implements.yaml` — all owned by the (unwritten) 014 plan, along
  with deriver 5 (`observed/repo-implements`) and `lode drift --docs`.
- Declared-layer authoring (the `declared/<doc>` graph writers) — 008/014.
  This plan only reads declared graphs; tests seed them directly.
- The prod SPARQL endpoint, write auth, outbox materializer — spec 009,
  cross-repo. Everything here targets whatever `LODE_GRAPH_URL` names
  (Oxigraph in dev/tests).
- The atomic `claim --next` ordering — 005, shipped on the backbone;
  untouched.

## Design calls this plan makes

1. **IRI package: `internal/kg/iri`** (platform-graph-design plan). The
   sibling plans previously disagreed on where the grammar lives; that is
   now resolved in favor of `internal/kg/iri` (see Overlaps below), so this
   plan takes it as a prerequisite rather than binding to the
   knowledge-graph plan's own package. Runtime-node IRIs come via
   `internal/graphproj` (which the runtime plan pairs with the row→triple
   functions), not re-minted.
2. **No migration.** The deriver no-op short circuit stores the input hash as
   a triple inside the deriver's own graph
   (`<graphIRI> dct:identifier "sha256:…"`), read back with a SELECT before
   each PUT. Nothing else needs Postgres schema. If a checkpoint table ever
   becomes necessary, it takes whatever id is next free when this plan
   actually executes — migration ids are provisional, assigned sequentially
   at execution time by the migration-id script.
3. **Serialization: N-Triples via `graphproj.Render`** for every deriver
   (deterministic sorted+deduped output; GSP PUT with
   `Content-Type: application/n-triples`). One renderer, no new one.
4. **PR files and manifests are fetched at derive time** through a narrow
   `RepoReader` interface (GitHub implementation over
   `githubauth.AppAuth.InstallationToken`); derivers stay pull-based and
   cheap to re-run, and no new ingestion or table is added.
5. **go-imports v1 emits intra-repo cross-component edges only.** Cross-repo
   edges need a module-path→component index spanning all manifests; that is
   an open question below, not silently half-built.
6. **Repo instance IRIs**: `internal/kg/iri` has no repo pattern; this plan adds
   `iri.Repo(host, owner, name)` → `id/repo/<host>/<owner>/<name>` (flagged
   for spec 006).
7. **`wl:unmatchedPath` is minted** (one DatatypeProperty appended to
   `rdf/wl/ontology.ttl`) so deriver 2 can report coverage gaps in-graph, as
   4.2 requires.
8. **The clock is bound from `lode`**: deviation-expiry comparison
   (`dct:valid < today`) injects today's date into the query text rather than
   relying on `NOW()`, keeping query output deterministic under test.

---

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `internal/derive/run.go` | deriver contract: hash short-circuit + atomic GSP PUT into one `observed/*` graph; `Result` |
| `internal/derive/run_test.go` | skip-on-match, write-on-change, confinement, hash triple round-trip (httptest fake endpoint) |
| `internal/derive/imports.go` | deriver 1: `go list -deps -json` stream → `dct:requires` between components |
| `internal/derive/imports_test.go` | fixture JSON stream + manifest → exact N-Triples; intra-component edges dropped |
| `internal/derive/layout.go` | deriver 2: manifest + file walk → `dct:hasPart`, `a wl:Component`, `wl:unmatchedPath` |
| `internal/derive/layout_test.go` | temp dir fixtures: multi-component repo, unmatched paths, whole-repo manifest |
| `internal/derive/praffects.go` | deriver 3: task-bound PRs × changed files × manifest → `wl:affects` |
| `internal/derive/praffects_test.go` | fake `RepoReader`; join, mapping, missing-manifest skip |
| `internal/derive/deploy.go` | deriver 4: store rows → `graphproj` runtime triples in `observed/deploy` |
| `internal/derive/deploy_test.go` | seeded store rows → exact triple set (needs Postgres) |
| `internal/derive/github.go` | `RepoReader` over the GitHub API with installation tokens |
| `internal/derive/github_test.go` | httptest GitHub: files pagination, contents decode, 404 |
| `internal/overview/queries.go` | standing queries 4.1/4.2 as SPARQL over `graph.Client`; row types |
| `internal/overview/queries_test.go` | query-text golden checks (prefix/graph confinement, injected date) |
| `internal/overview/critpath.go` | SCC cycle detection, longest-path depth, transitive fan-out, critical set |
| `internal/overview/critpath_test.go` | known DAG, planted cycle excluded + surfaced |
| `internal/overview/service.go` | `Service`: store + optional graph client → the five overview reads |
| `internal/overview/oxigraph_test.go` | acceptance vs. Oxigraph: planted violation, stale intent, deviation suppression + expiry, gaps |
| `internal/api/overview.go` | handlers: overview, drift, gaps, frontier, critical-path, derive |
| `internal/api/overview_test.go` | auth gates, JSON shapes, 503 when graph unconfigured, frontier ordering |
| `internal/api/templates/drift.html` | drift board web view (violations + stale intent, read-only) |
| `internal/cmd/overview.go` | cobra glue for `lode overview/drift/gaps/frontier/critical-path/derive` |
| `internal/cmd/overview_test.go` | flag wiring + `--json` passthrough against a fake server |

**Modified files**

| Path | Change |
|---|---|
| `internal/kg/iri/iri.go` | add `DeclaredGraph`, `ObservedGraph`, `Repo` |
| `internal/graph/client.go` | add `Replace` (GSP `PUT`) next to `Load` |
| `rdf/wl/ontology.ttl` | append `wl:unmatchedPath` |
| `rdf/vocab_test.go` | add `wl:unmatchedPath` to the mint-set check |
| `internal/store/ranking.go` | export `Frontier` (ranked ready set + fan-out, no claim) |
| `internal/store/tasks.go` | add `AllBlockEdges` |
| `internal/store/changes.go` | add `TaskPRs` |
| `internal/store/artifacts.go` | add `AllDeployments`, `AllArtifactsByID` |
| `internal/store/delivery.go` | add `HasMainCommit`, `AllReleaseFrontiers` |
| `internal/api/server.go` | routes for the five reads + `POST /api/v1/derive`; `GET /drift` web route |
| `internal/api/web.go` | `driftPage` handler |
| `internal/cmd/serve.go` | wire `overview.Service` + server-side derivers from `LODE_GRAPH_URL` env |
| `internal/cli/client.go` | `Overview`, `Drift`, `Gaps`, `Frontier`, `CriticalPath`, `RunDerive` |
| `README.md` | document the commands, the deriver contract, and `POST /api/v1/derive` |

**Test commands**

- Pure packages (no services): `go test ./internal/derive/... ./internal/overview/... ./internal/kg/iri/... ./internal/graph/ ./rdf/...`
- Postgres-backed: `docker compose up -d postgres && go test ./internal/store/... ./internal/api/... ./internal/cmd/...`
- Oxigraph-backed (skip when `TEST_SPARQL_URL` unset, per `graphtest`):
  `docker compose up -d oxigraph && go test ./internal/overview/... ./internal/derive/...`
- Everything: `docker compose up -d postgres oxigraph && go test ./...`

---

## Task 1: Layer graph names and the repo IRI

**Files:**
- Modify: `internal/kg/iri/iri.go`
- Test: `internal/kg/iri/iri_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to the `cases` table in `TestGrammar` (`internal/kg/iri/iri_test.go`):

```go
		{"declared graph", iri.DeclaredGraph("adr-worklode-0007"),
			base + "graph/declared/adr-worklode-0007"},
		{"observed graph", iri.ObservedGraph("go-imports"),
			base + "graph/observed/go-imports"},
		{"repo", iri.Repo("github.com", "sunstoneinstitute", "worklode"),
			base + "id/repo/github.com/sunstoneinstitute/worklode"},
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/kg/iri/...`
Expected: FAIL — `undefined: iri.DeclaredGraph`

- [ ] **Step 3: Write the implementation**

Append to `internal/kg/iri/iri.go`:

```go
// DeclaredGraph returns the named graph holding one design doc's declared
// edges (spec 007 §Representation: one graph per design doc, so acceptance
// gating and re-authoring replace exactly one graph).
func DeclaredGraph(docSlug string) string { return GraphNS + "declared/" + docSlug }

// ObservedGraph returns the named graph one deriver owns (spec 007: a
// deriver must confine its writes to its own observed/* graph). Sources:
// go-imports, repo-layout, pr-affects, deploy, repo-implements (014 §6).
func ObservedGraph(source string) string { return GraphNS + "observed/" + source }

// Repo returns a repository's instance IRI (the doap:Project node, D4).
// Spec 006 defines no repo pattern; this package fixes
// id/repo/<host>/<owner>/<name>.
func Repo(host, owner, name string) string {
	return IDNS + "repo/" + host + "/" + owner + "/" + name
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/kg/iri/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/kg/iri
git commit -m "Add declared/observed graph names and the repo IRI"
```

---

## Task 2: Atomic graph replace on the SPARQL client

**Files:**
- Modify: `internal/graph/client.go`
- Test: `internal/graph/client_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/graph/client_test.go` (reuse its `recordingServer`
helper; extend `record` with a `method` field set from `r.Method` if it does
not carry one yet):

```go
func TestReplacePutsGraph(t *testing.T) {
	srv, rec := recordingServer(t, http.StatusNoContent, "")
	c := graph.NewClient(srv.URL, nil)
	err := c.Replace(context.Background(), "urn:g",
		"application/n-triples", []byte("<urn:s> <urn:p> <urn:o> .\n"))
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if rec.method != http.MethodPut || rec.path != "/store" ||
		rec.query != "graph=urn%3Ag" || rec.contentType != "application/n-triples" {
		t.Fatalf("request = %+v; want PUT /store?graph=urn%%3Ag as n-triples", rec)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/graph/ -run TestReplace`
Expected: FAIL — `c.Replace undefined`

- [ ] **Step 3: Write the implementation**

In `internal/graph/client.go`, generalize the private request helper to take
a method (mechanical: `post(ctx, path, …)` becomes
`send(ctx, method, path, …)`; `post` stays as a one-line wrapper), then add
next to `Load`:

```go
// Replace PUTs a document as the entire new content of a named graph (Graph
// Store Protocol). This is the atomic full-graph replace spec 007's deriver
// contract requires: no stale triple survives a run.
func (c *Client) Replace(ctx context.Context, graphIRI, contentType string, doc []byte) error {
	_, err := c.send(ctx, http.MethodPut,
		"/store?graph="+url.QueryEscape(graphIRI), contentType, "", doc)
	return err
}
```

- [ ] **Step 4: Run the graph suite**

Run: `go test ./internal/graph/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/graph
git commit -m "Add atomic named-graph replace to the SPARQL client"
```

---

## Task 3: The deriver runner (hash short-circuit + confinement)

**Files:**
- Create: `internal/derive/run.go`
- Test: `internal/derive/run_test.go`

- [ ] **Step 1: Write the failing test**

```go
package derive_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/graph"
)

// fakeEndpoint is a minimal SPARQL/GSP endpoint: it answers the hash SELECT
// with storedHash and counts PUTs. There is deliberately no /update route —
// the prod graph-server exposes only GSP writes plus a read-only SPARQL
// proxy (spec 009), so Run must never need one.
type fakeEndpoint struct {
	storedHash string
	puts       atomic.Int32
	lastPut    atomic.Pointer[string]
}

func (f *fakeEndpoint) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/query":
			bindings := ""
			if f.storedHash != "" {
				bindings = `{"h": {"type": "literal", "value": "` + f.storedHash + `"}}`
			}
			w.Header().Set("Content-Type", "application/sparql-results+json")
			io.WriteString(w, `{"head":{"vars":["h"]},"results":{"bindings":[`+bindings+`]}}`)
		case r.URL.Path == "/store" && r.Method == http.MethodPut:
			f.puts.Add(1)
			body, _ := io.ReadAll(r.Body)
			s := string(body)
			f.lastPut.Store(&s)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestRunWritesPayloadWithEmbeddedHash(t *testing.T) {
	f := &fakeEndpoint{}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := graph.NewClient(srv.URL, nil)

	res, err := derive.Run(context.Background(), c, "urn:g",
		[]byte("<urn:s> <urn:p> <urn:o> .\n"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Skipped || res.Graph != "urn:g" || res.Hash == "" {
		t.Fatalf("result = %+v; want an unskipped write with a hash", res)
	}
	if f.puts.Load() != 1 {
		t.Fatalf("PUTs = %d; want exactly 1 (write must be a single atomic PUT)", f.puts.Load())
	}
	got := *f.lastPut.Load()
	if !strings.Contains(got, "<urn:s> <urn:p> <urn:o> .") {
		t.Fatalf("PUT body = %q; want the payload", got)
	}
	// The hash triple rides inside the same PUT, so it lands atomically with
	// the data and needs no SPARQL Update (which prod graph-server lacks).
	if !strings.Contains(got, `<urn:g> <http://purl.org/dc/terms/identifier> "`+res.Hash+`"`) {
		t.Fatalf("PUT body = %q; want the embedded hash triple", got)
	}
}

func TestRunSkipsOnMatchingHash(t *testing.T) {
	payload := []byte("<urn:s> <urn:p> <urn:o> .\n")
	f := &fakeEndpoint{storedHash: derive.HashOf(payload)}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := graph.NewClient(srv.URL, nil)

	res, err := derive.Run(context.Background(), c, "urn:g", payload)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Skipped {
		t.Fatalf("result = %+v; want Skipped", res)
	}
	if f.puts.Load() != 0 || f.updates.Load() != 0 {
		t.Fatalf("puts=%d updates=%d; a matching hash must write nothing",
			f.puts.Load(), f.updates.Load())
	}
}

func TestRunRewritesOnChangedHash(t *testing.T) {
	f := &fakeEndpoint{storedHash: "sha256:stale"}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := graph.NewClient(srv.URL, nil)

	res, err := derive.Run(context.Background(), c, "urn:g", []byte("<urn:s> <urn:p> <urn:o> .\n"))
	if err != nil || res.Skipped {
		t.Fatalf("Run = %+v, %v; want a fresh write", res, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/derive/...`
Expected: FAIL — `no required module provides package .../internal/derive`

- [ ] **Step 3: Write the implementation**

```go
// Package derive implements spec 007's observed-layer derivers. Every
// deriver is a pure function producing the complete N-Triples document for
// its source; Run performs the shared contract — idempotent, full-replace,
// cheap to re-run, confined to one observed/* named graph.
package derive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/graph"
)

// dctIdentifier stores the input hash of a deriver run as a triple about
// the graph, inside the graph — replaced atomically with everything else,
// no checkpoint table needed.
const dctIdentifier = "http://purl.org/dc/terms/identifier"

// Result reports one deriver run.
type Result struct {
	Graph   string `json:"graph"`
	Hash    string `json:"hash"`
	Skipped bool   `json:"skipped"`
	Bytes   int    `json:"bytes"`
}

// HashOf returns the content hash Run compares against.
func HashOf(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// storedHash reads the hash triple of a graph's previous run ("" if none).
func storedHash(ctx context.Context, c *graph.Client, graphIRI string) (string, error) {
	rows, err := c.Select(ctx, fmt.Sprintf(
		"SELECT ?h WHERE { GRAPH <%s> { <%s> <%s> ?h } }",
		graphIRI, graphIRI, dctIdentifier))
	if err != nil {
		return "", fmt.Errorf("read stored hash of %s: %w", graphIRI, err)
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0]["h"], nil
}

// Run applies the deriver contract: if the payload's hash matches the
// graph's stored hash the run is a no-op; otherwise the graph is atomically
// replaced (one GSP PUT) by the payload plus a triple recording the new
// hash. Embedding the hash in the PUT body keeps the write a single atomic
// operation and works against endpoints that expose only GSP + read-only
// SPARQL (prod graph-server, spec 009) — no SPARQL Update is ever needed.
// Run never touches any graph other than graphIRI.
func Run(ctx context.Context, c *graph.Client, graphIRI string, payload []byte) (Result, error) {
	hash := HashOf(payload)
	prev, err := storedHash(ctx, c, graphIRI)
	if err != nil {
		return Result{}, err
	}
	if prev == hash {
		return Result{Graph: graphIRI, Hash: hash, Skipped: true}, nil
	}
	doc := fmt.Sprintf("%s<%s> <%s> %q .\n", payload, graphIRI, dctIdentifier, hash)
	if err := c.Replace(ctx, graphIRI, "application/n-triples", []byte(doc)); err != nil {
		return Result{}, fmt.Errorf("replace %s: %w", graphIRI, err)
	}
	return Result{Graph: graphIRI, Hash: hash, Bytes: len(payload)}, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/derive/...`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/derive
git commit -m "Add the observed-layer deriver runner"
```

---

## Task 4: Deriver 1 — go-imports

**Files:**
- Create: `internal/derive/imports.go`
- Test: `internal/derive/imports_test.go`

Input is the JSON stream of `go list -deps -json ./...` (concatenated
objects). Packages are mapped to components by matching the package's first
Go file's repo-relative path against the manifest; import edges between
distinct components of the repo become `<a> dct:requires <b>`. Edges inside
one component, to unmatched packages, and to packages outside the module are
dropped (design call 5: cross-repo edges are an open question, not built).

- [ ] **Step 1: Write the failing test**

```go
package derive_test

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
)

const importsManifest = `
repo: github.com/sunstoneinstitute/research-stack
components:
  - iri: https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/ingest
    name: ingest
    paths: ["cmd/ingest/**", "internal/ingest/**"]
  - iri: https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/graphsrv
    name: graphsrv
    paths: ["cmd/graph-server/**", "internal/graph/**"]
`

// goListStream mimics go list -deps -json ./... : one JSON object per
// package. Dir/Module.Dir use the module root /r.
const goListStream = `
{"ImportPath":"example.com/rs/internal/ingest","Dir":"/r/internal/ingest",
 "GoFiles":["ingest.go"],"Module":{"Path":"example.com/rs","Dir":"/r"},
 "Imports":["example.com/rs/internal/graph","fmt"]}
{"ImportPath":"example.com/rs/internal/graph","Dir":"/r/internal/graph",
 "GoFiles":["graph.go"],"Module":{"Path":"example.com/rs","Dir":"/r"},
 "Imports":["fmt"]}
{"ImportPath":"example.com/rs/internal/ingest/parse","Dir":"/r/internal/ingest/parse",
 "GoFiles":["parse.go"],"Module":{"Path":"example.com/rs","Dir":"/r"},
 "Imports":["example.com/rs/internal/ingest"]}
{"ImportPath":"fmt","Dir":"/goroot/src/fmt","GoFiles":["print.go"],"Imports":[]}
`

func TestImportsTriples(t *testing.T) {
	m, err := manifest.Parse([]byte(importsManifest))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	doc, err := derive.ImportsTriples(strings.NewReader(goListStream), "/r", m)
	if err != nil {
		t.Fatalf("ImportsTriples: %v", err)
	}
	got := string(doc)
	want := "<https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/ingest> " +
		"<http://purl.org/dc/terms/requires> " +
		"<https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/graphsrv> .\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant exactly the one cross-component edge:\n%s", got, want)
	}
}

func TestImportsTriplesDropsIntraComponentAndForeign(t *testing.T) {
	m, _ := manifest.Parse([]byte(importsManifest))
	doc, err := derive.ImportsTriples(strings.NewReader(goListStream), "/r", m)
	if err != nil {
		t.Fatalf("ImportsTriples: %v", err)
	}
	for _, banned := range []string{"fmt", "ingest/parse"} {
		if strings.Contains(string(doc), banned) {
			t.Fatalf("output mentions %q; stdlib and intra-component edges must be dropped:\n%s",
				banned, doc)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/derive/ -run TestImports`
Expected: FAIL — `undefined: derive.ImportsTriples`

- [ ] **Step 3: Write the implementation**

```go
package derive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
)

const dctRequires = "http://purl.org/dc/terms/requires"

// listedPackage is the subset of `go list -json` output the deriver reads.
type listedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Imports    []string
	Module     *struct {
		Path string
		Dir  string
	}
}

// componentOf maps a package to its owning component IRI via the manifest.
// The match key is the repo-relative path of the package's first Go file,
// because manifest globs (cmd/ingest/**) match files, not directories.
// "" means: not in this module, or matched by no component.
func componentOf(p listedPackage, moduleRoot string, m *manifest.Manifest) string {
	if p.Module == nil || p.Module.Dir != moduleRoot || len(p.GoFiles) == 0 {
		return ""
	}
	rel, err := filepath.Rel(moduleRoot, filepath.Join(p.Dir, p.GoFiles[0]))
	if err != nil {
		return ""
	}
	c, ok := m.Match(filepath.ToSlash(rel))
	if !ok {
		return ""
	}
	return c.IRI
}

// ImportsTriples turns a `go list -deps -json ./...` stream into the
// observed/go-imports document (spec 007 deriver 1): one
// <a> dct:requires <b> per pair of distinct components with at least one
// package-level import between them. Same-component and unmapped edges are
// dropped; graphproj.Render sorts and dedupes, so output is deterministic.
func ImportsTriples(goList io.Reader, moduleRoot string, m *manifest.Manifest) ([]byte, error) {
	dec := json.NewDecoder(goList)
	var pkgs []listedPackage
	byImportPath := map[string]string{} // import path → component IRI
	for {
		var p listedPackage
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("parse go list output: %w", err)
		}
		pkgs = append(pkgs, p)
		byImportPath[p.ImportPath] = componentOf(p, moduleRoot, m)
	}

	var ts []graphproj.Triple
	for _, p := range pkgs {
		from := byImportPath[p.ImportPath]
		if from == "" {
			continue
		}
		for _, imp := range p.Imports {
			if to := byImportPath[imp]; to != "" && to != from {
				ts = append(ts, graphproj.Triple{S: from, P: dctRequires, O: to})
			}
		}
	}
	return graphproj.Render(ts), nil
}

// GoListDeps runs `go list -deps -json ./...` in repoRoot and returns its
// stdout, for DeriveImports; split out so tests feed a fixture stream.
func GoListDeps(ctx context.Context, repoRoot string) (io.Reader, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-json", "./...")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		msg := ""
		if ee, ok := err.(*exec.ExitError); ok {
			msg = ": " + strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("go list -deps -json in %s: %w%s", repoRoot, err, msg)
	}
	return strings.NewReader(string(out)), nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/derive/ -run TestImports -v`
Expected: PASS (2 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/derive
git commit -m "Derive component dependencies from Go imports"
```

---

## Task 5: Deriver 2 — repo layout

**Files:**
- Create: `internal/derive/layout.go`
- Test: `internal/derive/layout_test.go`
- Modify: `rdf/wl/ontology.ttl`, `rdf/vocab_test.go`

- [ ] **Step 1: Mint `wl:unmatchedPath`**

Append to `rdf/wl/ontology.ttl`:

```turtle
wl:unmatchedPath a owl:DatatypeProperty ; wl:layer wlc:execution ;
    rdfs:range xsd:string ;
    rdfs:comment "A repo-relative path prefix matched by no component in the repo's .worklode/components.yaml — a coverage gap (spec 007 §2, §4.2). Subject is the repo (doap:Project) node; written only by the repo-layout deriver." .
```

Add `"wl:unmatchedPath a owl:DatatypeProperty"` to `mintedDeclarations` in
`rdf/vocab_test.go`, then run `go test ./rdf/...` — expected PASS.

- [ ] **Step 2: Write the failing deriver test**

```go
package derive_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
)

// writeTree creates the given empty files under a temp root.
func writeTree(t *testing.T, paths ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return root
}

func TestLayoutTriplesMultiComponent(t *testing.T) {
	m, err := manifest.Parse([]byte(importsManifest)) // fixture from imports_test.go
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	root := writeTree(t,
		"cmd/ingest/main.go", "internal/ingest/ingest.go",
		"internal/graph/graph.go",
		"scripts/build.sh", "README.md",
	)
	doc, err := derive.LayoutTriples(root, "github.com", "sunstoneinstitute", "research-stack", m)
	if err != nil {
		t.Fatalf("LayoutTriples: %v", err)
	}
	got := string(doc)
	repo := "<https://worklode.io/ns/id/repo/github.com/sunstoneinstitute/research-stack>"
	for _, line := range []string{
		repo + " <http://purl.org/dc/terms/hasPart> <https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/ingest> .",
		repo + " <http://purl.org/dc/terms/hasPart> <https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/graphsrv> .",
		"<https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/ingest> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <https://worklode.io/ns/ontology#Component> .",
		repo + ` <https://worklode.io/ns/ontology#unmatchedPath> "README.md" .`,
		repo + ` <https://worklode.io/ns/ontology#unmatchedPath> "scripts" .`,
	} {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("missing line:\n%s\ngot:\n%s", line, got)
		}
	}
	if strings.Contains(got, `"cmd"`) || strings.Contains(got, `"internal"`) {
		t.Errorf("matched trees reported as gaps:\n%s", got)
	}
}

func TestLayoutTriplesSkipsDotGit(t *testing.T) {
	m, _ := manifest.Parse([]byte(importsManifest))
	root := writeTree(t, ".git/config", ".worklode/components.yaml", "internal/ingest/i.go")
	doc, err := derive.LayoutTriples(root, "github.com", "o", "r", m)
	if err != nil {
		t.Fatalf("LayoutTriples: %v", err)
	}
	if strings.Contains(string(doc), ".git") || strings.Contains(string(doc), ".worklode") {
		t.Fatalf("dotdirs reported as gaps:\n%s", doc)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/derive/ -run TestLayout`
Expected: FAIL — `undefined: derive.LayoutTriples`

- [ ] **Step 4: Write the implementation**

```go
package derive

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
)

const (
	rdfType     = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
	dctHasPart  = "http://purl.org/dc/terms/hasPart"
	wlComponent = "https://worklode.io/ns/ontology#Component"
	wlUnmatched = "https://worklode.io/ns/ontology#unmatchedPath"
)

// LayoutTriples derives the observed/repo-layout document (spec 007
// deriver 2): the repo's dct:hasPart edge to each manifest component, each
// component typed wl:Component, and every unmatched path collapsed to its
// top-level prefix as a wl:unmatchedPath gap. Dot-directories (.git,
// .worklode, .github, …) are infrastructure, not coverage gaps.
func LayoutTriples(root, host, owner, name string, m *manifest.Manifest) ([]byte, error) {
	repo := iri.Repo(host, owner, name)
	var ts []graphproj.Triple
	for _, c := range m.Components {
		ts = append(ts,
			graphproj.Triple{S: repo, P: dctHasPart, O: c.IRI},
			graphproj.Triple{S: c.IRI, P: rdfType, O: wlComponent},
		)
	}

	gaps := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || rel == "." {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		if _, ok := m.Match(rel); !ok {
			top, _, _ := strings.Cut(rel, "/")
			gaps[top] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}

	prefixes := make([]string, 0, len(gaps))
	for p := range gaps {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	for _, p := range prefixes {
		ts = append(ts, graphproj.Triple{S: repo, P: wlUnmatched, O: p, Lit: true})
	}
	return graphproj.Render(ts), nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/derive/ -run TestLayout -v && go test ./rdf/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/derive rdf
git commit -m "Derive repo layout and coverage gaps from the component manifest"
```

---

## Task 6: `lode derive` for the repo-local derivers

**Files:**
- Create: `internal/cmd/overview.go` (starts here with the `derive` command; overview reads arrive in Task 12)
- Test: `internal/cmd/overview_test.go`

The command runs from a repo checkout (CI/cron per the spec's trigger
contract): loads `.worklode/components.yaml`, resolves the repo coordinates
from the origin remote (`internal/repourl.Normalize` + the existing
`gitRemoteURL` in `internal/cli`), computes both documents, and PUTs them to
`LODE_GRAPH_URL` (flag `--graph-url` overrides). `--dry-run` prints the
N-Triples and writes nothing.

- [ ] **Step 1: Write the failing test**

```go
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveDryRunPrintsTriples(t *testing.T) {
	// A minimal repo: manifest + one Go file per component is not needed for
	// layout; imports are skipped when go list fails (reported, not fatal).
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".worklode"), 0o755); err != nil {
		t.Fatal(err)
	}
	man := `repo: github.com/acme/app
components:
  - iri: https://worklode.io/ns/id/component/github.com/acme/app
    name: app
    paths: ["**"]
`
	if err := os.WriteFile(filepath.Join(root, ".worklode", "components.yaml"),
		[]byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runDeriveLocal(t.Context(), root, "github.com", "acme", "app", true, nil)
	if err != nil {
		t.Fatalf("runDeriveLocal: %v", err)
	}
	if !strings.Contains(out, "id/repo/github.com/acme/app") ||
		!strings.Contains(out, "dc/terms/hasPart") {
		t.Fatalf("dry-run output missing layout triples:\n%s", out)
	}
}

func TestDeriveRequiresManifest(t *testing.T) {
	_, err := runDeriveLocal(t.Context(), t.TempDir(), "github.com", "acme", "app", true, nil)
	if err == nil || !strings.Contains(err.Error(), "components.yaml") {
		t.Fatalf("err = %v; want a missing-manifest error naming the file", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cmd/ -run TestDerive`
Expected: FAIL — `undefined: runDeriveLocal`

- [ ] **Step 3: Write the implementation**

Create `internal/cmd/overview.go`:

```go
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/graph"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
	"github.com/sunstoneinstitute/worklode/internal/repourl"
)

// runDeriveLocal computes the repo-local observed documents (go-imports,
// repo-layout) for the repo at root. With dryRun it returns the rendered
// N-Triples; otherwise it Runs each through the deriver contract against c.
// A repo that is not a Go module derives layout only (reported inline).
func runDeriveLocal(ctx context.Context, root, host, owner, name string, dryRun bool, c *graph.Client) (string, error) {
	manPath := filepath.Join(root, ".worklode", "components.yaml")
	data, err := os.ReadFile(manPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w (spec 007 §2: every derived repo needs a component-boundary manifest)", manPath, err)
	}
	m, err := manifest.Parse(data)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", manPath, err)
	}

	docs := map[string][]byte{} // observed source → document
	layout, err := derive.LayoutTriples(root, host, owner, name, m)
	if err != nil {
		return "", err
	}
	docs["repo-layout"] = layout

	var notes []string
	if stream, err := derive.GoListDeps(ctx, root); err != nil {
		notes = append(notes, fmt.Sprintf("go-imports skipped: %v", err))
	} else {
		imports, err := derive.ImportsTriples(stream, root, m)
		if err != nil {
			return "", err
		}
		docs["go-imports"] = imports
	}

	var b strings.Builder
	for _, source := range []string{"go-imports", "repo-layout"} {
		doc, ok := docs[source]
		if !ok {
			continue
		}
		if dryRun {
			fmt.Fprintf(&b, "# %s\n%s", iri.ObservedGraph(source), doc)
			continue
		}
		res, err := derive.Run(ctx, c, iri.ObservedGraph(source), doc)
		if err != nil {
			return b.String(), err
		}
		fmt.Fprintf(&b, "%s: hash=%s skipped=%v\n", res.Graph, res.Hash, res.Skipped)
	}
	for _, n := range notes {
		fmt.Fprintln(&b, n)
	}
	return b.String(), nil
}

// newDeriveCmd wires `lode derive`: run the repo-local derivers from a
// checkout, in CI or by hand. Server-side derivers (pr-affects, deploy) run
// via POST /api/v1/derive instead.
func newDeriveCmd() *cobra.Command {
	var graphURL string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "derive",
		Short: "Run the repo-local observed-layer derivers (go-imports, repo-layout)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := os.Getwd()
			if err != nil {
				return err
			}
			remote := gitRemoteOrigin(root) // helper below
			coord, err := repourl.Normalize(remote)
			if err != nil {
				return fmt.Errorf("resolve repo from origin remote %q: %w", remote, err)
			}
			owner, name, _ := strings.Cut(coord, "/")

			var c *graph.Client
			if !dryRun {
				if graphURL == "" {
					graphURL = os.Getenv("LODE_GRAPH_URL")
				}
				if graphURL == "" {
					return errors.New("no graph endpoint: set --graph-url or LODE_GRAPH_URL (or use --dry-run)")
				}
				c = graph.NewClient(graphURL, nil)
			}
			out, err := runDeriveLocal(cmd.Context(), root, "github.com", owner, name, dryRun, c)
			fmt.Fprint(cmd.OutOrStdout(), out)
			return err
		},
	}
	cmd.Flags().StringVar(&graphURL, "graph-url", "", "SPARQL endpoint base URL (default $LODE_GRAPH_URL)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the N-Triples instead of writing")
	return cmd
}
```

`gitRemoteOrigin` is three lines of `exec.Command("git", "-C", dir,
"remote", "get-url", "origin")` mirroring `internal/cli/gitremote.go`
(that function is unexported in another package; do not export it, copy the
pattern). Register per the package convention (`board.go:52`):

```go
func init() { rootCmd.AddCommand(newDeriveCmd()) }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cmd/ -run TestDerive -v`
Expected: PASS (2 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/cmd
git commit -m "Add lode derive for the repo-local derivers"
```

---

## Task 7: Store reads for the server-side derivers and the frontier

**Files:**
- Modify: `internal/store/changes.go`, `internal/store/artifacts.go`, `internal/store/delivery.go`, `internal/store/tasks.go`, `internal/store/ranking.go`
- Test: `internal/store/overview_test.go` (create)

- [ ] **Step 1: Write the failing test**

`internal/store/overview_test.go` (`package store`; use the existing
`OpenTestStore` and task/edge helpers from `tasks_test.go` /
`ranking_test.go` — create tasks through the same fixture path those tests
use):

```go
package store

import (
	"context"
	"testing"
)

func TestTaskPRs(t *testing.T) {
	s := OpenTestStore(t)
	// Seed one PR bound to a task and one unbound, through the event log as
	// hooks do (mirror the seeding style of changes_test.go).
	seedTaskWithID(t, s, "WL-1")
	seedPR(t, s, "acme/app", 1, "wt/WL-1-fix")   // branch join → task_id=WL-1
	seedPR(t, s, "acme/app", 2, "unrelated-branch")

	prs, err := s.TaskPRs(context.Background())
	if err != nil {
		t.Fatalf("TaskPRs: %v", err)
	}
	if len(prs) != 1 || prs[0].Repo != "acme/app" || prs[0].Number != 1 || prs[0].TaskID != "WL-1" {
		t.Fatalf("TaskPRs = %+v; want the one task-bound PR", prs)
	}
}

func TestAllBlockEdges(t *testing.T) {
	s := OpenTestStore(t)
	seedTaskWithID(t, s, "WL-1")
	seedTaskWithID(t, s, "WL-2")
	seedEdge(t, s, "WL-1", "WL-2", "blocks")
	seedEdge(t, s, "WL-2", "WL-1", "child_of") // must not appear

	edges, err := s.AllBlockEdges(context.Background())
	if err != nil {
		t.Fatalf("AllBlockEdges: %v", err)
	}
	if len(edges) != 1 || edges[0].FromTask != "WL-1" || edges[0].ToTask != "WL-2" {
		t.Fatalf("AllBlockEdges = %+v; want exactly WL-1 blocks WL-2", edges)
	}
}

func TestFrontierMirrorsClaimNextOrder(t *testing.T) {
	s := OpenTestStore(t)
	seedReadyTask(t, s, "WL-1", "low")
	seedReadyTask(t, s, "WL-2", "critical")

	tasks, fanOut, err := s.Frontier(context.Background(), "")
	if err != nil {
		t.Fatalf("Frontier: %v", err)
	}
	if len(tasks) != 2 || tasks[0].ID != "WL-2" {
		t.Fatalf("Frontier order = %v; critical priority must sort first", ids(tasks))
	}
	_ = fanOut

	// The mirror contract: Frontier's head equals ClaimNext's dry-run pick.
	res, err := s.ClaimNext(context.Background(), ClaimNextOpts{DryRun: true})
	if err != nil || res.Task == nil {
		t.Fatalf("ClaimNext dry run: %+v, %v", res, err)
	}
	if res.Task.ID != tasks[0].ID {
		t.Fatalf("frontier head %s != claim-next pick %s", tasks[0].ID, res.Task.ID)
	}
}

func TestAllDeploymentsAndFrontiers(t *testing.T) {
	s := OpenTestStore(t)
	seedDeployment(t, s, "prod", "flux_kustomization", "graph-server")

	ds, err := s.AllDeployments(context.Background())
	if err != nil || len(ds) != 1 {
		t.Fatalf("AllDeployments = %v, %v; want 1 row", ds, err)
	}
	if _, err := s.AllArtifactsByID(context.Background()); err != nil {
		t.Fatalf("AllArtifactsByID: %v", err)
	}
	if _, err := s.AllReleaseFrontiers(context.Background()); err != nil {
		t.Fatalf("AllReleaseFrontiers: %v", err)
	}
	if ok, err := s.HasMainCommit(context.Background(), "acme/app", "deadbeef"); err != nil || ok {
		t.Fatalf("HasMainCommit(unknown) = %v, %v; want false, nil", ok, err)
	}
}
```

Write the small `seed*`/`ids` helpers at the bottom of the file against the
actual fixture helpers the store tests already use (`tasks_test.go`,
`ranking_test.go`, `changes_test.go`, `artifacts_test.go` each contain the
insertion pattern to copy — event-log wrapped, exactly as production writes).

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run 'TestTaskPRs|TestAllBlock|TestFrontier|TestAllDeployments'`
Expected: FAIL — undefined methods.

- [ ] **Step 3: Write the implementations**

`internal/store/changes.go`:

```go
// PRRef is the minimal PR identity the pr-affects deriver needs.
type PRRef struct {
	Repo   string
	Number int64
	TaskID string
}

// TaskPRs returns every pull request bound to a task, ordered by repo then
// number. Unbound PRs are invisible to the deriver: with no task join there
// is no wl:affects subject.
func (s *Store) TaskPRs(ctx context.Context) ([]PRRef, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT repo, number, task_id FROM pull_requests
		 WHERE task_id IS NOT NULL ORDER BY repo, number`)
	if err != nil {
		return nil, fmt.Errorf("task prs: %w", err)
	}
	defer rows.Close()
	var out []PRRef
	for rows.Next() {
		var p PRRef
		if err := rows.Scan(&p.Repo, &p.Number, &p.TaskID); err != nil {
			return nil, fmt.Errorf("scan task pr: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
```

`internal/store/tasks.go`:

```go
// AllBlockEdges returns every 'blocks' edge, for the overview critical-path
// join (spec 007: the DAG spans backbone blocks + KG requires).
func (s *Store) AllBlockEdges(ctx context.Context) ([]Edge, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT from_task, to_task FROM task_edges WHERE type = 'blocks'`)
	if err != nil {
		return nil, fmt.Errorf("all block edges: %w", err)
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		e := Edge{Type: "blocks"}
		if err := rows.Scan(&e.FromTask, &e.ToTask); err != nil {
			return nil, fmt.Errorf("scan block edge: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
```

`internal/store/ranking.go` (compose the existing private pieces; keep
`ClaimNext` untouched):

```go
// Frontier returns the ready, unblocked, unleased tasks in the exact rank
// order ClaimNext consumes, plus the blocking fan-out map — the read-only
// overview mirror of the authoritative frontier (spec 007 §4.5). It claims
// nothing.
func (s *Store) Frontier(ctx context.Context, projectID string) ([]Task, map[string]int, error) {
	candidates, err := s.readyCandidates(ctx, projectID)
	if err != nil {
		return nil, nil, err
	}
	fanOut, err := s.BlockingFanOut(ctx)
	if err != nil {
		return nil, nil, err
	}
	projectIDs := make([]string, 0, len(candidates))
	for _, t := range candidates {
		projectIDs = append(projectIDs, t.ProjectID)
	}
	focus, err := s.projectFocusMap(ctx, projectIDs)
	if err != nil {
		return nil, nil, err
	}
	in := make([]rankInput, len(candidates))
	for i, t := range candidates {
		in[i] = rankInput{Task: t, Focus: focus[t.ProjectID], FanOut: fanOut[t.ID]}
	}
	return rankTasks(in, false), fanOut, nil
}
```

`internal/store/artifacts.go`:

```go
// AllDeployments returns every deployments row, for the deploy deriver's
// full-replace projection.
func (s *Store) AllDeployments(ctx context.Context) ([]Deployment, error) {
	// Same SELECT and scanDeployment as ListDeployments, without the
	// environment filter and ordered by (environment, target_kind, target_name).
}

// AllArtifactsByID returns every artifacts row keyed by id, so the deploy
// deriver can resolve deployments.artifact_id → prov:used in one pass.
func (s *Store) AllArtifactsByID(ctx context.Context) (map[int64]Artifact, error) {
	// SELECT the artifact columns scanArtifact reads; build the map.
}
```

(Both bodies are mechanical copies of the adjacent `ListDeployments` /
`scanArtifact` code with the filter dropped — write them in full, matching
the existing column lists exactly.)

`internal/store/delivery.go`:

```go
// ReleaseFrontier row for the deploy deriver's wl:covers projection.
type ReleaseFrontierRow struct {
	Repo string
	Tag  string
	SHA  string
}

// AllReleaseFrontiers returns each repo's release frontier rows joined to
// the frontier commit's sha.
func (s *Store) AllReleaseFrontiers(ctx context.Context) ([]ReleaseFrontierRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT rf.repo, rf.tag, mc.sha
		FROM release_frontiers rf JOIN main_commits mc ON mc.id = rf.main_id
		ORDER BY rf.repo, rf.tag`)
	// scan loop as above
}

// HasMainCommit reports whether sha is a recorded main_commits row for
// repo — the CommitKnown guard graphproj.ArtifactTriples requires (015 §6).
func (s *Store) HasMainCommit(ctx context.Context, repo, sha string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM main_commits WHERE repo = $1 AND sha = $2`,
		repo, sha).Scan(&n)
	return n > 0, err
}
```

Match `release_frontiers`/`main_commits` column names against
`deploy/base/migrations/0005_delivery.up.sql:29-66` before finalizing the
SQL.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/store/ -run 'TestTaskPRs|TestAllBlock|TestFrontier|TestAllDeployments' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "Add overview and deriver store reads"
```

---

## Task 8: Deriver 4 — deploy projection

**Files:**
- Create: `internal/derive/deploy.go`
- Test: `internal/derive/deploy_test.go`

- [ ] **Step 1: Write the failing test**

`internal/derive/deploy_test.go` (needs Postgres; skip like the store suite
by going through `store.OpenTestStore`):

```go
package derive_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestDeployTriplesProjectsRows(t *testing.T) {
	s := store.OpenTestStore(t)
	seedArtifactAndDeployment(t, s) // helper: one docker_image artifact +
	// one prod flux_kustomization deployment referencing it, seeded through
	// the event log exactly as artifacts_test.go does.

	doc, err := derive.DeployTriples(context.Background(), s)
	if err != nil {
		t.Fatalf("DeployTriples: %v", err)
	}
	got := string(doc)
	for _, want := range []string{
		"ontology#Deployment", "ontology#Artifact",
		"id/environment/prod", "id/environment/dev",
		"ontology#toEnvironment",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestDeployTriplesDeterministic(t *testing.T) {
	s := store.OpenTestStore(t)
	seedArtifactAndDeployment(t, s)
	a, err := derive.DeployTriples(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	b, err := derive.DeployTriples(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("re-deriving unchanged rows is not byte-identical")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/derive/ -run TestDeploy`
Expected: FAIL — `undefined: derive.DeployTriples`

- [ ] **Step 3: Write the implementation**

```go
package derive

import (
	"context"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// DeployTriples derives the observed/deploy document (spec 007 deriver 4,
// vocabulary and guards per spec 015 §2–§6): projection of the already-
// ingested artifacts, deployments, environments, commit links and release
// frontiers. Projection, not new build (D6) — every triple comes from a row.
func DeployTriples(ctx context.Context, s *store.Store) ([]byte, error) {
	var ts []graphproj.Triple
	ts = append(ts, graphproj.EnvironmentTriples()...)

	artifacts, err := s.AllArtifactsByID(ctx)
	if err != nil {
		return nil, fmt.Errorf("deploy deriver: %w", err)
	}
	for _, a := range artifacts {
		a := a
		known := func(sha string) bool {
			ok, err := s.HasMainCommit(ctx, a.Repo, sha)
			return err == nil && ok
		}
		ts = append(ts, graphproj.ArtifactTriples(a, known)...)
		if a.Repo != "" && a.SourceSHA != "" && known(a.SourceSHA) {
			ts = append(ts, graphproj.CommitTriples(graphproj.GitHubHost, a.Repo, a.SourceSHA)...)
		}
	}

	deployments, err := s.AllDeployments(ctx)
	if err != nil {
		return nil, fmt.Errorf("deploy deriver: %w", err)
	}
	for _, d := range deployments {
		var artifact *store.Artifact
		if d.ArtifactID != nil {
			if a, ok := artifacts[*d.ArtifactID]; ok {
				artifact = &a
			}
		}
		ts = append(ts, graphproj.DeploymentTriples(d, artifact)...)
	}

	frontiers, err := s.AllReleaseFrontiers(ctx)
	if err != nil {
		return nil, fmt.Errorf("deploy deriver: %w", err)
	}
	for _, f := range frontiers {
		ts = append(ts, graphproj.ReleaseCoversTriples(f.Repo, f.Tag, f.SHA)...)
	}
	return graphproj.Render(ts), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `docker compose up -d postgres && go test ./internal/derive/ -run TestDeploy -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/derive
git commit -m "Derive the observed deploy projection from ingested rows"
```

---

## Task 9: Deriver 3 — pr-affects (with the GitHub RepoReader)

**Files:**
- Create: `internal/derive/praffects.go`, `internal/derive/github.go`
- Test: `internal/derive/praffects_test.go`, `internal/derive/github_test.go`

- [ ] **Step 1: Write the failing deriver test**

```go
package derive_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// fakeRepoReader serves manifests and PR file lists from maps.
type fakeRepoReader struct {
	manifests map[string]string   // repo → components.yaml
	files     map[string][]string // "repo#number" → changed paths
}

func (f *fakeRepoReader) FileAt(_ context.Context, repo, path string) ([]byte, error) {
	if path != ".worklode/components.yaml" {
		return nil, errors.New("unexpected path " + path)
	}
	m, ok := f.manifests[repo]
	if !ok {
		return nil, derive.ErrNotFound
	}
	return []byte(m), nil
}

func (f *fakeRepoReader) PRFiles(_ context.Context, repo string, number int64) ([]string, error) {
	return f.files[repoNum(repo, number)], nil
}

func repoNum(repo string, n int64) string { return repo + "#" + string(rune('0'+n)) }

func TestPRAffectsTriples(t *testing.T) {
	prs := []store.PRRef{
		{Repo: "sunstoneinstitute/research-stack", Number: 1, TaskID: "WL-7"},
		{Repo: "sunstoneinstitute/unmapped", Number: 2, TaskID: "WL-8"},
	}
	rr := &fakeRepoReader{
		manifests: map[string]string{"sunstoneinstitute/research-stack": importsManifest},
		files: map[string][]string{
			repoNum("sunstoneinstitute/research-stack", 1): {
				"internal/ingest/x.go", "internal/graph/y.go", "README.md",
			},
		},
	}
	doc, skipped, err := derive.PRAffectsTriples(context.Background(), prs, rr)
	if err != nil {
		t.Fatalf("PRAffectsTriples: %v", err)
	}
	got := string(doc)
	task := "<https://worklode.io/ns/id/task/WL-7>"
	for _, comp := range []string{"research-stack/ingest", "research-stack/graphsrv"} {
		if !strings.Contains(got,
			task+" <https://worklode.io/ns/ontology#affects> <https://worklode.io/ns/id/component/github.com/sunstoneinstitute/"+comp+"> .") {
			t.Errorf("missing wl:affects to %s in:\n%s", comp, got)
		}
	}
	if strings.Contains(got, "WL-8") {
		t.Errorf("PR in a manifest-less repo produced triples:\n%s", got)
	}
	if len(skipped) != 1 || skipped[0] != "sunstoneinstitute/unmapped" {
		t.Fatalf("skipped = %v; want the manifest-less repo reported", skipped)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/derive/ -run TestPRAffects`
Expected: FAIL — `undefined: derive.PRAffectsTriples`

- [ ] **Step 3: Write the deriver**

`internal/derive/praffects.go`:

```go
package derive

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

const wlAffects = "https://worklode.io/ns/ontology#affects"

// ErrNotFound is returned by RepoReader implementations for a missing file.
var ErrNotFound = errors.New("not found")

// RepoReader is the slice of the forge API the pr-affects deriver needs.
// Spec 007 deriver 3's inputs are pulled fresh on each run — derivers are
// cheap to re-run and hold no state.
type RepoReader interface {
	// FileAt fetches a file at the repo's default branch head.
	FileAt(ctx context.Context, repo, path string) ([]byte, error)
	// PRFiles lists a pull request's changed file paths.
	PRFiles(ctx context.Context, repo string, number int64) ([]string, error)
}

// PRAffectsTriples derives the observed/pr-affects document: for every
// task-bound PR, each changed path is mapped to a component through the
// repo's manifest and emitted as <task> wl:affects <component>. Repos
// without a manifest are skipped and reported, never fatal.
func PRAffectsTriples(ctx context.Context, prs []store.PRRef, rr RepoReader) (doc []byte, skippedRepos []string, err error) {
	manifests := map[string]*manifest.Manifest{}
	skipped := map[string]bool{}
	var ts []graphproj.Triple
	for _, pr := range prs {
		m, ok := manifests[pr.Repo]
		if !ok && !skipped[pr.Repo] {
			data, ferr := rr.FileAt(ctx, pr.Repo, ".worklode/components.yaml")
			switch {
			case errors.Is(ferr, ErrNotFound):
				skipped[pr.Repo] = true
			case ferr != nil:
				return nil, nil, fmt.Errorf("manifest for %s: %w", pr.Repo, ferr)
			default:
				if m, ferr = manifest.Parse(data); ferr != nil {
					return nil, nil, fmt.Errorf("manifest for %s: %w", pr.Repo, ferr)
				}
				manifests[pr.Repo] = m
			}
		}
		m = manifests[pr.Repo]
		if m == nil {
			continue
		}
		files, ferr := rr.PRFiles(ctx, pr.Repo, pr.Number)
		if ferr != nil {
			return nil, nil, fmt.Errorf("files of %s#%d: %w", pr.Repo, pr.Number, ferr)
		}
		for _, f := range files {
			if c, ok := m.Match(f); ok {
				ts = append(ts, graphproj.Triple{S: iri.Task(pr.TaskID), P: wlAffects, O: c.IRI})
			}
		}
	}
	for r := range skipped {
		skippedRepos = append(skippedRepos, r)
	}
	sort.Strings(skippedRepos)
	return graphproj.Render(ts), skippedRepos, nil
}
```

- [ ] **Step 4: Write the GitHub RepoReader with its test**

`internal/derive/github_test.go` — an `httptest` server asserting: `FileAt`
GETs `/repos/{repo}/contents/{path}`, decodes the base64 `content` field,
maps 404 to `ErrNotFound`; `PRFiles` GETs `/repos/{repo}/pulls/{n}/files`
with `per_page=100` and follows `Link: rel="next"` pagination, collecting
`filename` fields; both send `Authorization: Bearer <installation token>`.
Write the three tests in the established `recordingServer` style.

`internal/derive/github.go`:

```go
package derive

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
)

// GitHubReader implements RepoReader over the GitHub REST API using App
// installation tokens (internal/githubauth/app.go:94). BaseURL is
// overridable in tests, mirroring AppAuth.
type GitHubReader struct {
	Auth    *githubauth.AppAuth
	BaseURL string // default https://api.github.com
}

func (g *GitHubReader) get(ctx context.Context, repo, url string, out any) (status int, next string, err error) {
	tok, err := g.Auth.InstallationToken(ctx, repo)
	if err != nil {
		return 0, "", fmt.Errorf("installation token for %s: %w", repo, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<22))
	if resp.StatusCode == http.StatusOK && out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return resp.StatusCode, "", fmt.Errorf("decode %s: %w", url, err)
		}
	}
	return resp.StatusCode, nextLink(resp.Header.Get("Link")), nil
}

// FileAt implements RepoReader via the contents API.
func (g *GitHubReader) FileAt(ctx context.Context, repo, path string) ([]byte, error) {
	var payload struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	status, _, err := g.get(ctx, repo,
		g.base()+"/repos/"+repo+"/contents/"+path, &payload)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("GET contents %s/%s: HTTP %d", repo, path, status)
	}
	return base64.StdEncoding.DecodeString(payload.Content)
}

// PRFiles implements RepoReader via the PR files API, following pagination.
func (g *GitHubReader) PRFiles(ctx context.Context, repo string, number int64) ([]string, error) {
	url := fmt.Sprintf("%s/repos/%s/pulls/%d/files?per_page=100", g.base(), repo, number)
	var out []string
	for url != "" {
		var page []struct {
			Filename string `json:"filename"`
		}
		status, next, err := g.get(ctx, repo, url, &page)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("GET pr files %s#%d: HTTP %d", repo, number, status)
		}
		for _, f := range page {
			out = append(out, f.Filename)
		}
		url = next
	}
	return out, nil
}

func (g *GitHubReader) base() string {
	if g.BaseURL != "" {
		return g.BaseURL
	}
	return "https://api.github.com"
}

// nextLink extracts the rel="next" URL from a Link header ("" if none).
func nextLink(h string) string {
	for _, part := range strings.Split(h, ",") {
		if strings.Contains(part, `rel="next"`) {
			if i, j := strings.Index(part, "<"), strings.Index(part, ">"); i >= 0 && j > i {
				return part[i+1 : j]
			}
		}
	}
	return ""
}
```

(Add `"strings"` to the imports.)

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/derive/ -run 'TestPRAffects|TestGitHubReader' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/derive
git commit -m "Derive task-affects-component edges from PR files"
```

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

| Spec acceptance criterion | Covered by |
|---|---|
| Two-layer wiring, atomic per-graph replace, round-trip | Tasks 1–3, 11 (Oxigraph tests) |
| Derivers idempotent + confined; multi-component repo resolves; unmatched reported | Tasks 3–9 (research-stack-shaped manifest fixture in Tasks 4–5) |
| 4.1 both directions on a seeded graph | Task 11 (`TestDriftBothDirections`) |
| Drift suppression, `--acknowledged`, expiry re-surfacing | Task 11 (`TestDeviationSuppressesUntilExpiry`), Task 13 |
| 4.3 / 4.4 | **deferred to the 014 plan** (superseded/re-pointed by 014 §6) |
| Critical path correct; cycle detected, excluded, surfaced | Task 10 |
| Ordering contract: frontier matches the backbone | Task 7 (`TestFrontierMirrorsClaimNextOrder`), Task 12 |
| Deterministic `--json` everywhere; read-only web view, no mutation affordance | Tasks 13–14 |

## Overlaps and open questions

1. **IRI-grammar package: resolved to `internal/kg/iri`.** The sibling plans
   previously disagreed on where the grammar lives: this plan bound to a
   knowledge-graph-plan package of the same short name, another line ran
   through `internal/kg/iri` (2026-07-30-platform-graph-design, extended by
   2026-07-30-design-documents-as-graph-objects, and named canonical by
   2026-07-30-data-platform-kg-requirements), and `internal/graphproj`
   (2026-07-30-runtime-layer) covered runtime nodes only. Resolved at the
   planning tier in favor of `internal/kg/iri`, owned by the
   platform-graph-design plan; this plan now takes it as a prerequisite, and
   `internal/graphproj` keeps runtime-node IRIs (paired with its triple
   functions) — the grammar itself is unchanged (base
   `https://worklode.io/ns/`, `id/`, `graph/` families). A narrower conflict
   remains above all five plans: data-platform ADR-0003 and Worklode spec
   006/014 now agree on the schema base (`https://worklode.io/ns/ontology#`)
   but still fix different authorities for instances and named graphs (see
   the data-platform-kg-requirements plan, Overlaps §2) — resolve at the
   planning tier before any deriver writes to prod.
2. **`.worklode/components.yaml` parser** is owned by
   2026-07-30-platform-graph-design (`internal/kg/manifest`); this plan only
   calls `Parse`/`Match` and assumes the `Component.IRI` field name.
3. **Spec 007 correction:** deriver 3's claim that PR changed-file lists are
   "already ingested" is false in this codebase; this plan fetches them at
   derive time. If ingestion is preferred later, `PRAffectsTriples` keeps
   its signature and only the `RepoReader` implementation changes.
4. **PR→Task join** uses the shipped relational binding
   (`pull_requests.task_id` via `wt/<id>-<slug>` / body ref,
   `internal/store/changes.go:99,118`), not the spec's resolved-Q1 Issue
   mirror — mirroring does not exist yet (004/008). Swap the join when it
   lands.
5. **Cross-repo import edges** (deriver 1) are not built: they need a
   module-path→component index across all repos' manifests, which no spec
   currently places. v1 emits intra-repo cross-component edges only.
6. **Deferred to the 014 plan** (now written:
   `docs/plans/2026-07-30-design-documents-as-graph-objects.md`, which
   builds `internal/kg/implements` and itself defers the graph-side
   queries): deriver 5 (`observed/repo-implements`), the stale-claim query
   replacing 4.3, per-section 4.4 coverage,
   `lode specs --drifted/--unimplemented`, `lode drift --docs`, per-section
   web badges (014 §10). When those queries land, they slot into
   `internal/overview` beside 4.1/4.2.
7. **Migration numbers:** this plan needs none. Migration ids are
   provisional and assigned sequentially at execution time by the
   migration-id script; if a checkpoint table is ever needed, it takes
   whatever id is next free when this plan actually executes.
8. **New IRI pattern flagged for spec 006:** `id/repo/<host>/<owner>/<name>`
   (Task 1) — the repo/doap:Project node had no minted pattern.
9. **New vocabulary term flagged for spec 006/007:** `wl:unmatchedPath`
   (Task 5) — 4.2 requires unmatched paths be reportable, but no spec names
   the predicate that carries them.
10. **Deriver scheduling** is left to CI/cron + on-demand
    (`lode derive` in a repo's CI; `POST /api/v1/derive` from a cron or
    hook), matching the spec's trigger contract without a background loop.
    If a loop is wanted, hang it off the projector ticker the
    knowledge-graph plan adds to `lode serve`.
11. **Prod endpoint flavor.** This plan writes and reads through
    `internal/graph.Client` (Oxigraph-native `/store`, `/query`), which is
    what dev/tests expose. Prod graph-server (spec 009) exposes
    branch-scoped GSP (`PUT /branches/main/graphs?graph=…`) plus a
    read-only `/sparql` proxy — the data-platform-kg-requirements plan's
    `internal/graphserver` client. The deriver contract was designed for
    that surface (single PUT, hash embedded in the payload, reads via
    SELECT only — Task 3), so pointing production at graph-server is a
    client/path swap in `derive.Run`'s and `overview`'s constructor wiring,
    not a redesign.
