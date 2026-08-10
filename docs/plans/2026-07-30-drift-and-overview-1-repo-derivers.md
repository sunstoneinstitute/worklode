---
status: accepted
task: WL-5
covers: docs/specs/007-drift-and-overview.md
---
# Drift & overview 1/3 (spec 007): graph wiring & repo-local derivers — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 1 of 3. Task numbering is global across the series: this plan
holds Tasks 1–6; `2026-07-30-drift-and-overview-2-server-derivers.md` holds
Tasks 7–9; `2026-07-30-drift-and-overview-3-overview-and-surfaces.md` holds
Tasks 10–15. Each part must be merged before the next starts. This part
carries the series-wide context — sibling-plan prerequisites, prior art,
scope, design calls, and the overlaps/open-questions record; parts 2–3
restate only what shapes their own tasks.

**Goal:** Land the two-layer named-graph wiring and the repo-local half of
drift: layer graph names and the repo IRI, atomic per-graph replace, the
deriver runner (hash short-circuit + confinement), derivers 1–2 (go-imports,
repo-layout), and `lode derive` so a repo's CI can materialize its
`observed/*` graphs.

**Architecture:** Derivers are pure row/file→triple functions in a new
`internal/derive` package, serialized as deterministic N-Triples
(`internal/graphproj.Render`) and written by a shared runner that does an
atomic Graph Store Protocol `PUT` per source graph, short-circuited by a
content hash stored as a triple inside the graph itself (no Postgres
migration). This part builds the runner and the repo-local derivers
(go-imports, repo-layout), which run via a new `lode derive` command in CI.
The DB-backed derivers ship in part 2; the standing queries, critical path,
and read surface in part 3.

**Tech Stack:** Go 1.26, cobra CLI, standard-library testing, SPARQL 1.1
Protocol + Graph Store Protocol over `net/http`, Oxigraph (docker) as the
test endpoint.

**Spec:** `docs/specs/007-drift-and-overview.md`, read with its amendments:
`docs/specs/014-design-documents-as-graph-objects.md` §5–§6, §10 and
`docs/specs/015-runtime-layer.md` §2–§6. All `ls:`/`lsc:`/`lsid:` prefixes in
the spec read as `wl:`/`wlc:`/`wlid:` (014 §1).

---

## Prerequisites — sibling plans this series builds on

This series assumes the following 2026-07-30 plans have executed. It re-plans
none of their packages; it only calls them.

| Plan | Provides (consumed here) |
|---|---|
| `docs/plans/2026-07-30-knowledge-graph-{1-graph-foundations,2-projector}.md` | part 1: `internal/graph` (`Client.Update/Select/Ask/Load`, `Triple`, `graphtest` Oxigraph harness), `rdf/wl/*.ttl`; part 2: projector env vars `LODE_GRAPH_URL`/`LODE_GRAPH_TOKEN_URL`, migration 0008 |
| `docs/plans/2026-07-30-platform-graph-design.md` | `internal/kg/iri` (IRI grammar, `GraphNS`), `internal/kg/manifest` (`Parse`, `(*Manifest).Match` — first-match-wins `**` globs over `.worklode/components.yaml`, spec 007 §2), Worklode's own manifest |
| `docs/plans/2026-07-30-runtime-layer.md` | `internal/graphproj` (`Triple`, `Render`, `ArtifactTriples`, `DeploymentTriples`, `EnvironmentTriples`, `CommitTriples`, `ReleaseCoversTriples`, `CommitKnown`) — exactly the row→triple functions 015 says "007's observed/deploy deriver will emit" |
| `docs/plans/2026-07-30-reconciliation-{1-replay-engine,2-cli-surface,3-poll-engine}.md` | nothing consumed directly; noted because the series owns `lode doctor` and `internal/reconcile`, which this series must not touch |
| `docs/plans/2026-07-30-design-documents-as-graph-objects.md` | nothing consumed; owns everything this series defers to "the 014 plan" — `internal/kg/implements`, the `observed/repo-implements` deriver, sections, `lode doc` |
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

Not implemented anywhere (this series' scope): every deriver, every standing
query, critical path, `lode overview/drift/gaps/frontier/critical-path`, the
drift web view.

**Spec correction found while grounding this series:** spec 007 deriver 3 says
PR changed-file lists are "already ingested by `internal/hooks/github.go`".
They are not — no changed-file data exists anywhere in the schema or hooks
(the webhook payload doesn't carry file lists). Part 2 fetches them from
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
  This series only reads declared graphs; tests seed them directly.
- The prod SPARQL endpoint, write auth, outbox materializer — spec 009,
  cross-repo. Everything here targets whatever `LODE_GRAPH_URL` names
  (Oxigraph in dev/tests).
- The atomic `claim --next` ordering — 005, shipped on the backbone;
  untouched.

## Design calls this series makes

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
   becomes necessary, it takes whatever id is next free when the owning part
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
6. **Repo instance IRIs**: `internal/kg/iri` has no repo pattern; Task 1 adds
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
| `internal/cmd/overview.go` | cobra glue — created here with `lode derive`; the overview read commands arrive in part 3 |
| `internal/cmd/overview_test.go` | `derive` flag wiring, `--dry-run`, repo-coordinate resolution |

**Modified files**

| Path | Change |
|---|---|
| `internal/kg/iri/iri.go` | add `DeclaredGraph`, `ObservedGraph`, `Repo` |
| `internal/graph/client.go` | add `Replace` (GSP `PUT`) next to `Load` |
| `rdf/wl/ontology.ttl` | append `wl:unmatchedPath` |
| `rdf/vocab_test.go` | add `wl:unmatchedPath` to the mint-set check |

**Test commands**

- Pure packages (no services): `go test ./internal/derive/... ./internal/kg/iri/... ./internal/graph/ ./rdf/...`
- Postgres-backed: `docker compose up -d postgres && go test ./internal/cmd/...`
- Oxigraph-backed (skip when `TEST_SPARQL_URL` unset, per `graphtest`):
  `docker compose up -d oxigraph && go test ./internal/derive/...`

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

## Acceptance criteria → task map

The full-series map is split across the three parts; this part covers:

| Spec acceptance criterion | Covered by |
|---|---|
| Two-layer wiring, atomic per-graph replace | Tasks 1–3 (the seeded round-trip acceptance runs against Oxigraph in part 3, Task 11) |
| Derivers idempotent + confined; multi-component repo resolves; unmatched reported | Tasks 3–6 (research-stack-shaped manifest fixture in Tasks 4–5); derivers 3–4 follow in part 2 |

## Overlaps and open questions

1. **IRI-grammar package: resolved to `internal/kg/iri`.** The sibling plans
   previously disagreed on where the grammar lives: this series bound to a
   knowledge-graph-plan package of the same short name, another line ran
   through `internal/kg/iri` (2026-07-30-platform-graph-design, extended by
   2026-07-30-design-documents-as-graph-objects, and named canonical by
   2026-07-30-data-platform-kg-requirements), and `internal/graphproj`
   (2026-07-30-runtime-layer) covered runtime nodes only. Resolved at the
   planning tier in favor of `internal/kg/iri`, owned by the
   platform-graph-design plan; this series now takes it as a prerequisite, and
   `internal/graphproj` keeps runtime-node IRIs (paired with its triple
   functions) — the grammar itself is unchanged (base
   `https://worklode.io/ns/`, `id/`, `graph/` families). A narrower conflict
   remains above all five plans: data-platform ADR-0003 and Worklode spec
   006/014 now agree on the schema base (`https://worklode.io/ns/ontology#`)
   but still fix different authorities for instances and named graphs (see
   the data-platform-kg-requirements plan, Overlaps §2) — resolve at the
   planning tier before any deriver writes to prod.
2. **`.worklode/components.yaml` parser** is owned by
   2026-07-30-platform-graph-design (`internal/kg/manifest`); this series only
   calls `Parse`/`Match` and assumes the `Component.IRI` field name.
3. **Spec 007 correction:** deriver 3's claim that PR changed-file lists are
   "already ingested" is false in this codebase; part 2 fetches them at
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
7. **Migration numbers:** this series needs none. Migration ids are
   provisional and assigned sequentially at execution time by the
   migration-id script; if a checkpoint table is ever needed, it takes
   whatever id is next free when the owning part actually executes.
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
11. **Prod endpoint flavor.** This series writes and reads through
    `internal/graph.Client` (Oxigraph-native `/store`, `/query`), which is
    what dev/tests expose. Prod graph-server (spec 009) exposes
    branch-scoped GSP (`PUT /branches/main/graphs?graph=…`) plus a
    read-only `/sparql` proxy — the data-platform-kg-requirements plan's
    `internal/graphserver` client. The deriver contract was designed for
    that surface (single PUT, hash embedded in the payload, reads via
    SELECT only — Task 3), so pointing production at graph-server is a
    client/path swap in `derive.Run`'s and `overview`'s constructor wiring,
    not a redesign.
