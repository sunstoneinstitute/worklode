---
status: accepted
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
(`internal/graphproj.Document`) and written by a shared runner that does an
atomic Graph Store Protocol `PUT` per source graph through
`internal/graphserver` (the production graph-server client), short-circuited
by a content hash stored as a triple inside the graph itself (no Postgres
migration). This part builds the runner and the repo-local derivers
(go-imports, repo-layout), which run via a new `lode derive` command in CI.
The DB-backed derivers ship in part 2; the standing queries, critical path,
and read surface in part 3.

**Tech Stack:** Go 1.26, cobra CLI, standard-library testing, SPARQL 1.1
Protocol + Graph Store Protocol over `net/http`, Oxigraph (docker) as the
test endpoint.

**Spec:** `docs/specs/007-drift-and-overview.md`, read with its amendments:
`docs/specs/025-documents-in-the-backbone.md` §5–§6, §10 and
`docs/specs/006-knowledge-graph.md` §2–§6. All `ls:`/`lsc:`/`lsid:` prefixes in
the spec read as `wl:`/`wlc:`/`wlid:` (025 §17).

---

## Prerequisites — sibling plans this series builds on

This series assumes the following 2026-07-30 plans have executed. It re-plans
none of their packages; it only calls them.

| Plan | Provides (consumed here) |
|---|---|
| `docs/plans/2026-07-30-knowledge-graph-{1-graph-foundations,2-projector}.md` (landed — WL-25/WL-26) | part 1: `internal/kg/iri` (plain-string constructors + the exported namespace constants), `internal/graphproj` (`Term`/`Triple`/`Document`, the `graphproj/graphtest` Oxigraph harness), the `ns/*.ttl` vocabulary checks; part 2: `internal/projector` and the `LODE_GRAPHSERVER_*` client wiring in `serve.go` (`graphserver.FromEnv`) |
| `docs/plans/2026-08-19-component-boundary-manifest.md` (successor to the superseded `2026-07-30-platform-graph-design.md` Tasks 2–3; planned under WL-109, execution task WL-120 — unlanded) | `internal/kg/manifest` (`Parse`, `(*Manifest).Match` — first-match-wins `**` globs over `.worklode/components.yaml`, spec 007 §2.2), Worklode's own manifest. `internal/kg/iri` (IRI grammar) moved to `2026-07-30-knowledge-graph-1-graph-foundations.md` and has landed (WL-25) |
| `docs/plans/2026-07-30-runtime-layer.md` (landed — WL-27) | the runtime row→triple functions in `internal/graphproj/runtime.go` (`ArtifactTriples`, `DeploymentTriples`, `EnvironmentTriples`, `CommitTriples`, `ReleaseCutFromTriples`; the commit guard `CommitKnown` landed as `func(repo, sha string) bool` — see that plan's execution notes) — exactly the row→triple functions 006 §11.1 says "007's observed/deploy deriver will emit". (`Triple`/`Document` themselves landed with knowledge-graph part 1.) |
| `docs/plans/2026-07-30-reconciliation-{1-replay-engine,2-cli-surface,3-poll-engine}.md` | nothing consumed directly; noted because the series owns `lode doctor` and `internal/reconcile`, which this series must not touch |
| `docs/plans/2026-07-30-design-documents-as-graph-objects.md` | nothing consumed; owns everything this series defers to "the 014 plan" — `internal/kg/implements`, the `observed/repo-implements` deriver, sections, `lode doc` |
| `docs/plans/2026-07-30-data-platform-kg-requirements.md` | owns `internal/graphserver` (the prod graph-server client — GSP + read-only `/sparql` only; landed) and the spec 009 hand-off issues. This series writes and reads through that client — see design call 9 |

If a prerequisite type's final name differs slightly from the plan text it
came from (these plans are unlanded), adapt the call site mechanically — the
responsibility split stands.

## Already implemented vs. remaining

Shipped on main today, reused as-is:

- Ready-set + ranking (the authoritative frontier, D8/D9):
  `internal/store/ranking.go` — `readyCandidates`; `rankTasks` (key
  `(is_critical, concern_rank, priority, fan_out)` where backbone
  `is_critical` = `priority == "critical"`); `BlockingFanOut` (transitive
  over `blocks`).
- Deploy/runtime ingestion (deriver 4's input, D6): `internal/hooks/flux.go`,
  `internal/hooks/deployment.go`, `internal/hooks/github.go`
  (`applyRelease`); rows in `internal/store/artifacts.go` (`Artifact`,
  `Deployment`, `ListDeployments`), `internal/store/delivery.go`
  (`main_commits`, `release_frontiers`).
- PR→Task join (deriver 3's join): `TaskIDFromRef`
  (`internal/store/branchname.go`, matching the configured branch template —
  default `{{ .id }}-{{ .slug }}`) and `TaskIDFromBody`
  (`internal/store/changes.go`); `UpsertPR` binds `pull_requests.task_id` at
  ingest. The spec's resolved Q1 (join via mirrored Issues / `Closes #N`)
  waits on Task↔Issue mirroring (004/008); until then the existing
  relational join is the join.
- Read-only web cockpit to extend: handlers in `internal/api/web.go`, pages
  as `templ` components in `internal/ui`; every new route must be named in
  `internal/api/router.go`'s `routeGuards` table or the server refuses to
  boot.
- GitHub App installation tokens for server-side API reads:
  `internal/githubauth/app.go` (`AppAuth.InstallationToken`), built by the
  api server (`newAppAuth`, `internal/api/server.go`).

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

- Queries 4.3 and 4.4 and `lode specs --drifted/--unimplemented`: 025 §11
  supersedes 4.3 with the section-scoped stale-claim query and re-points 4.4
  at per-section coverage; both need `wl:Section`, `wl:lastRevisedIn` and
  `.worklode/implements.yaml` — all owned by the (unwritten) 014 plan, along
  with deriver 5 (`observed/repo-implements`) and `lode drift --docs`.
- Declared-layer authoring (the `declared/<doc>` graph writers) — 008/014.
  This series only reads declared graphs; tests seed them directly.
- The graph-server itself, write auth, outbox materializer — spec 009,
  cross-repo. Everything here targets whatever `LODE_GRAPHSERVER_URL` names
  (in tests: `httptest` fakes, or Oxigraph behind the translating proxy —
  design call 9).
- The atomic `claim --next` ordering — 005, shipped on the backbone;
  untouched.

## Design calls this series makes

1. **IRI package: `internal/kg/iri`** (owned by
   `2026-07-30-knowledge-graph-1-graph-foundations.md`; landed — WL-25). The
   sibling plans previously disagreed on where the grammar lives; that is
   now resolved in favor of `internal/kg/iri` (see Overlaps below), so this
   plan takes it as a prerequisite rather than binding a package of its own.
   Constructors are plain-string, pure and non-validating (that plan's
   design call 5); the runtime-node patterns (`iri.Artifact`,
   `iri.Deployment`, `iri.Environment`, `iri.Commit`) are already there,
   not re-minted.
2. **No migration.** The deriver no-op short circuit stores the input hash as
   a triple inside the deriver's own graph
   (`<graphIRI> dct:identifier "sha256:…"`), read back with a SELECT before
   each PUT. Nothing else needs Postgres schema. If a checkpoint table ever
   becomes necessary, it takes whatever id is next free when the owning part
   actually executes — migration ids are provisional, assigned sequentially
   at execution time by the migration-id script.
3. **Serialization: N-Triples via `graphproj.Document`** for every deriver
   (deterministic sorted+deduped output). One renderer, no new one. Writes
   go through `graphserver.PutGraph`, which sends `text/turtle` — N-Triples
   is a syntactic subset of Turtle, so the same bytes are the payload.
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
   `ns/ontology.ttl` — `ns/` is the vocabulary's only home; the `rdf/wl/`
   route through rdf-registry was cancelled) so deriver 2 can report coverage
   gaps in-graph, as 4.2 requires. Per CLAUDE.md's ordering, amend spec
   006/007 first in the same commit (Overlaps item 9 flags the term), then
   mirror it in `ns/` and check with `riot --validate ns/*.ttl`.
8. **The clock is bound from `lode`**: deviation-expiry comparison
   (`dct:valid < today`) injects today's date into the query text rather than
   relying on `NOW()`, keeping query output deterministic under test.
9. **All graph I/O goes through `internal/graphserver`** (knowledge-graph
   part 1's design call 4: no other production SPARQL client is built).
   Writes are `PutGraph` — graph-server's whole-graph GSP replace, which is
   exactly the atomic full-replace the deriver contract needs — and reads
   are `Select` against the read-only `/sparql` proxy. graph-server has no
   SPARQL Update endpoint, so the runner embeds its hash triple in the PUT
   body rather than patching (Task 3). Unit tests fake the two routes with
   `httptest`; Oxigraph-backed tests bridge with a translating proxy, the
   `internal/projector/oxigraph_test.go` pattern.

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
| `docs/specs/006-knowledge-graph.md` / `007-drift-and-overview.md` | amendment minting `wl:unmatchedPath` (Overlaps item 9) |
| `ns/ontology.ttl` | mirror `wl:unmatchedPath` in the same commit |

**Test commands**

- Pure packages (no services): `go test -trimpath ./internal/derive/... ./internal/kg/iri/... ./internal/graphproj/...`
- Postgres-backed: `docker compose up -d postgres && go test -trimpath ./internal/cmd/...`
- Oxigraph-backed (skip when unreachable, per `graphproj/graphtest`):
  `docker compose up -d oxigraph && go test -trimpath ./internal/derive/... ./internal/graphproj/...`
- Vocabulary: `riot --validate ns/*.ttl`

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
// go-imports, repo-layout, pr-affects, deploy, repo-implements (025 §11).
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

## Task 2: Atomic graph replace — already landed, verify only

**Files:** none.

An earlier revision of this plan added a `Replace` (GSP `PUT`) method to a
worklode-local SPARQL client. That client was never built: knowledge-graph
part 1's design call 4 makes `internal/graphserver` the only production
graph client, and its `PutGraph(ctx, branch, graphIRI, turtle)` *is* the
atomic whole-graph replace — graph-server's writes replace or merge whole
named graphs, and there is no SPARQL Update endpoint to drift back to.
`Select` (the read-only `/sparql` proxy) covers the runner's hash read.
Both landed with `internal/graphserver` (`client.go`).

- [ ] **Step 1: Verify the surface this series needs exists**

Run: `go doc ./internal/graphserver Client.PutGraph && go doc ./internal/graphserver Client.Select`
Expected: both print — nothing to build. Continue to Task 3.

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
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
)

// fakeGraphServer fakes graph-server's two relevant routes: the read-only
// POST /sparql answers the hash SELECT with storedHash, and the branch-scoped
// GSP PUT /branches/main/graphs counts writes. There is deliberately no
// update route — graph-server exposes only whole-graph GSP writes plus the
// read-only SPARQL proxy (spec 009), so Run must never need one.
type fakeGraphServer struct {
	storedHash string
	puts       atomic.Int32
	lastPut    atomic.Pointer[string]
}

func (f *fakeGraphServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/sparql" && r.Method == http.MethodPost:
			bindings := ""
			if f.storedHash != "" {
				bindings = `{"h": {"type": "literal", "value": "` + f.storedHash + `"}}`
			}
			w.Header().Set("Content-Type", "application/sparql-results+json")
			io.WriteString(w, `{"head":{"vars":["h"]},"results":{"bindings":[`+bindings+`]}}`)
		case r.URL.Path == "/branches/main/graphs" && r.Method == http.MethodPut:
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
	f := &fakeGraphServer{}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := graphserver.New(srv.URL, nil)

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
	// the data and needs no SPARQL Update (which graph-server lacks).
	if !strings.Contains(got, `<urn:g> <http://purl.org/dc/terms/identifier> "`+res.Hash+`"`) {
		t.Fatalf("PUT body = %q; want the embedded hash triple", got)
	}
}

func TestRunSkipsOnMatchingHash(t *testing.T) {
	payload := []byte("<urn:s> <urn:p> <urn:o> .\n")
	f := &fakeGraphServer{storedHash: derive.HashOf(payload)}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := graphserver.New(srv.URL, nil)

	res, err := derive.Run(context.Background(), c, "urn:g", payload)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Skipped {
		t.Fatalf("result = %+v; want Skipped", res)
	}
	if f.puts.Load() != 0 {
		t.Fatalf("puts=%d; a matching hash must write nothing", f.puts.Load())
	}
}

func TestRunRewritesOnChangedHash(t *testing.T) {
	f := &fakeGraphServer{storedHash: "sha256:stale"}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := graphserver.New(srv.URL, nil)

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

	"github.com/sunstoneinstitute/worklode/internal/graphserver"
)

// Branch is the fixed graph-server branch the work graph lives on — the
// same value as projector.Branch (spec 006 §13.2 item 5).
const Branch = "main"

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
func storedHash(ctx context.Context, c *graphserver.Client, graphIRI string) (string, error) {
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
// replaced (one GSP PUT via graphserver.PutGraph — N-Triples is a Turtle
// subset, so the rendered document is the payload as-is) by the payload plus
// a triple recording the new hash. Embedding the hash in the PUT body keeps
// the write a single atomic operation and works against graph-server's
// GSP-plus-read-only-SPARQL surface (spec 009) — no SPARQL Update is ever
// needed. Run never touches any graph other than graphIRI.
func Run(ctx context.Context, c *graphserver.Client, graphIRI string, payload []byte) (Result, error) {
	hash := HashOf(payload)
	prev, err := storedHash(ctx, c, graphIRI)
	if err != nil {
		return Result{}, err
	}
	if prev == hash {
		return Result{Graph: graphIRI, Hash: hash, Skipped: true}, nil
	}
	doc := fmt.Sprintf("%s<%s> <%s> %q .\n", payload, graphIRI, dctIdentifier, hash)
	if _, err := c.PutGraph(ctx, Branch, graphIRI, []byte(doc)); err != nil {
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
// dropped; graphproj.Document sorts and dedupes, so output is deterministic.
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
				ts = append(ts, graphproj.Triple{S: from, P: dctRequires, O: graphproj.IRIRef(to)})
			}
		}
	}
	return graphproj.Document(ts), nil
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
- Modify: `docs/specs/007-drift-and-overview.md` (amendment), `ns/ontology.ttl`

> **Corrected contract (WL-267, landed after WL-28).** The deriver's input is
> the repo's **tracked files** (`git ls-files` through `internal/gitexec`),
> not the filesystem walk Steps 2–4 below specify; a root that is not inside a
> git work tree falls back to the walk, which is what keeps the temp-dir
> fixtures in Step 2 valid. A walk enumerates `.gitignore`d build output —
> `bin/`, `data/`, `wl`, `*.db` in worklode's own repo, none dot-prefixed — so
> the document's content hash changed with whether anyone had run a build,
> violating §2's "Deterministic. Same inputs -> same triples": Task 3's hash
> short-circuit never fired, and part 3's `lode gaps` (Task 13) would report `bin` as a
> component coverage gap. `LayoutTriples` therefore takes a `context.Context`
> first argument. Read the code below as the shape of the triple emission
> only; `internal/derive/layout.go` is the enumeration of record.

- [ ] **Step 1: Mint `wl:unmatchedPath`**

Per CLAUDE.md's ordering, amend the spec first (a short amendment on spec
007's reporting section naming the predicate — Overlaps item 9), then mirror
the term in `ns/ontology.ttl` in the same commit, next to the other
`wl:` datatype properties:

```turtle
wl:unmatchedPath a owl:DatatypeProperty ; wl:layer wlc:execution ;
    rdfs:range xsd:string ;
    rdfs:comment "A repo-relative path prefix matched by no component in the repo's .worklode/components.yaml — a coverage gap (spec 007 §1, §4.2). Subject is the repo (doap:Project) node; written only by the repo-layout deriver." .
```

Check with `riot --validate ns/*.ttl` and
`go test -trimpath ./internal/graphproj/` (the Oxigraph `ns/` parse gate;
skips when no endpoint is reachable) — expected PASS.

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
			graphproj.Triple{S: repo, P: dctHasPart, O: graphproj.IRIRef(c.IRI)},
			graphproj.Triple{S: c.IRI, P: rdfType, O: graphproj.IRIRef(wlComponent)},
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
		ts = append(ts, graphproj.Triple{S: repo, P: wlUnmatched, O: graphproj.Text(p)})
	}
	return graphproj.Document(ts), nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -trimpath ./internal/derive/ -run TestLayout -v && riot --validate ns/*.ttl`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/derive ns docs/specs
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
the graph-server named by the `LODE_GRAPHSERVER_*` environment
(`graphserver.FromEnv`; flag `--graph-url` overrides with an
unauthenticated client). `--dry-run` prints the N-Triples and writes
nothing.

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
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
	"github.com/sunstoneinstitute/worklode/internal/repourl"
)

// runDeriveLocal computes the repo-local observed documents (go-imports,
// repo-layout) for the repo at root. With dryRun it returns the rendered
// N-Triples; otherwise it Runs each through the deriver contract against c.
// A repo that is not a Go module derives layout only (reported inline).
func runDeriveLocal(ctx context.Context, root, host, owner, name string, dryRun bool, c *graphserver.Client) (string, error) {
	manPath := filepath.Join(root, ".worklode", "components.yaml")
	data, err := os.ReadFile(manPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w (spec 007 §1: every derived repo needs a component-boundary manifest)", manPath, err)
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

			var c *graphserver.Client
			if !dryRun {
				switch {
				case graphURL != "":
					c = graphserver.New(graphURL, nil)
				case os.Getenv("LODE_GRAPHSERVER_URL") != "":
					if c, err = graphserver.FromEnv(); err != nil {
						return err
					}
				default:
					return errors.New("no graph endpoint: set --graph-url or LODE_GRAPHSERVER_URL (or use --dry-run)")
				}
			}
			out, err := runDeriveLocal(cmd.Context(), root, "github.com", owner, name, dryRun, c)
			fmt.Fprint(cmd.OutOrStdout(), out)
			return err
		},
	}
	cmd.Flags().StringVar(&graphURL, "graph-url", "", "graph-server base URL, unauthenticated (default: the LODE_GRAPHSERVER_* env via graphserver.FromEnv)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the N-Triples instead of writing")
	return cmd
}
```

`gitRemoteOrigin` is three lines calling
`gitexec.CmdContext(ctx, dir, "remote", "get-url", "origin").Output()`,
mirroring `internal/cli/gitremote.go` (that function is unexported in
another package; do not export it, copy the pattern). Never a direct
`exec.Command("git", ...)` — `internal/gitexec` is the one place worklode
shells out to git, and its rule test fails the build on any other call
site. Register per the package convention (the `init` in `board.go`):

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

1. **IRI-grammar package: resolved to `internal/kg/iri`, landed.** The
   sibling plans previously disagreed on where the grammar lives; it is
   owned by `2026-07-30-knowledge-graph-1-graph-foundations.md` and landed
   under WL-25 (the superseding of 2026-07-30-platform-graph-design records
   the hand-over). The API is plain-string — pure, non-validating
   constructors plus exported namespace constants — and already includes
   the runtime-node patterns, so this series takes it as a prerequisite and
   adds only the three graph/repo patterns of Task 1, on the same
   convention. The grammar itself is unchanged (base
   `https://worklode.io/ns/`, `id/`, `graph/` families). A narrower conflict
   remains above all five plans: data-platform ADR-0003 and Worklode spec
   006/014 now agree on the schema base (`https://worklode.io/ns/ontology#`)
   but still fix different authorities for instances and named graphs (see
   the data-platform-kg-requirements plan, Overlaps §2) — resolve at the
   planning tier before any deriver writes to prod.
2. **`.worklode/components.yaml` parser** is owned by
   2026-08-19-component-boundary-manifest (`internal/kg/manifest`; successor
   to the superseded platform-graph-design plan's Tasks 2–3 — WL-109); this
   series only calls `Parse`/`Match` and assumes the `Component.IRI` field
   name.
3. **Spec 007 correction:** deriver 3's claim that PR changed-file lists are
   "already ingested" is false in this codebase; part 2 fetches them at
   derive time. If ingestion is preferred later, `PRAffectsTriples` keeps
   its signature and only the `RepoReader` implementation changes.
4. **PR→Task join** uses the shipped relational binding
   (`pull_requests.task_id` via branch-template head ref / body ref —
   `TaskIDFromRef` in `internal/store/branchname.go`, `TaskIDFromBody` in
   `internal/store/changes.go`), not the spec's resolved-Q1 Issue mirror —
   mirroring does not exist yet (004/008). Swap the join when it lands.
5. **Cross-repo import edges** (deriver 1) are not built: they need a
   module-path→component index across all repos' manifests, which no spec
   currently places. v1 emits intra-repo cross-component edges only.
6. **Deferred to the 014 plan** (now written:
   `docs/plans/2026-07-30-design-documents-as-graph-objects.md`, which
   builds `internal/kg/implements` and itself defers the graph-side
   queries): deriver 5 (`observed/repo-implements`), the stale-claim query
   replacing 4.3, per-section 4.4 coverage,
   `lode specs --drifted/--unimplemented`, `lode drift --docs`, per-section
   web badges (025 §18). When those queries land, they slot into
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
11. **Endpoint flavor: graph-server everywhere.** This series writes and
    reads through `internal/graphserver` (branch-scoped GSP
    `PUT /branches/main/graphs?graph=…` plus the read-only `/sparql`
    proxy) — there is no worklode-local SPARQL client (knowledge-graph
    part 1, design call 4). The deriver contract was designed for exactly
    that surface: single PUT, hash embedded in the payload, reads via
    SELECT only (Task 3). Unit tests fake the two routes; Oxigraph-backed
    tests (which speak plain GSP `/store` + `/query`) bridge with the small
    translating proxy `internal/projector/oxigraph_test.go` established,
    extended with a `/sparql`→`/query` forward where a test drives the
    production client end to end.
