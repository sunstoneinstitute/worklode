# Data-platform KG requirements (spec 009) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Discharge Worklode's side of spec 009 — a graph-server client
(`internal/graphserver`) and a repeatable acceptance harness that proves the
spec's acceptance criterion end-to-end — and hand the rest off as tracked
issues in data-platform and rdf-registry.

**Architecture:** Spec 009 is a requirements hand-off: four of its five
must-haves are data-platform or rdf-registry work, and most are already
delivered in dev (see the status table below). What Worklode owes is the
external-caller half of items 4–5 and the acceptance criterion: a small Go
client speaking graph-server's actual surface — branch-scoped Graph Store
Protocol writes plus the read-only `/sparql` proxy, authenticated with
Keycloak client credentials — and an env-gated e2e test that PUTs a Worklode
named graph to `main`, reads it back, and answers the drift question over
SPARQL. The remaining cross-repo work becomes five precisely-scoped issues.

**Tech Stack:** Go 1.x, standard-library testing, `net/http`,
`golang.org/x/oauth2/clientcredentials` (`go.mod:14`), `gh` CLI for the
hand-off issues.

**Spec:** `docs/specs/009-data-platform-kg-requirements.md`

---

## Most of this spec is not Worklode work

Spec 009 states requirements Worklode places on the data-platform
(`~/git/sunstone/data-platform`) and rdf-registry
(`~/git/sunstone/rdf-registry`) repos. This plan builds only the
Worklode-side obligations; everything else is handed off (Task 6). The
spec's Context section is also stale — status verified 2026-07-30 against
both repos:

| # | Requirement (spec 009) | Owner | Verified state |
|---|---|---|---|
| 1 | Prod deployment of graph-server | data-platform | **Open.** `deploy/overlays/prod/kustomization.yaml` deploys the Nessie/Iceberg base only — no graph-server, Oxigraph, or materializer resources. The dev and hzdev overlays have all three (`deploy/overlays/dev/graph-server-deployment.yaml`, `oxigraph-deployment.yaml`, `oxigraph-materializer-deployment.yaml`). → Issue A. |
| 2 | Query/read path (Oxigraph + outbox materializer) | data-platform | **Done in dev** — the spec's "Oxigraph is not deployed" is outdated. Materializer deployed (`deploy/overlays/dev/oxigraph-materializer-deployment.yaml`); `/sparql` is a read-only proxy to Oxigraph (`crates/graph-server/src/proxy.rs:25`). Prod pending item 1. |
| 3 | Stable, agreed IRI scheme + rdf-registry base-URL override | Worklode spec 006/014 + rdf-registry + data-platform | **Open, narrowed.** Data-platform ADR-0003 (`docs/adr/0003-hosting-worklode-kg-iri-scheme.md:105-109`) now agrees with Worklode spec 006 as amended by 014 §1 on the schema base, but still disagrees on instance and named-graph authority — see Overlaps §2. The rdf-registry base-URL override exists only as an unlanded plan (branch `worklode-io-spec`, commit 56768ba, `docs/superpowers/plans/2026-07-22-worklode-ns-base-override.md`) and predates the 014 §1 base. → Issues B and C. |
| 4 | External-service write auth confirmed | data-platform + Worklode | **Done in dev, manually.** Runbook `docs/runbooks/2026-07-22-worklode-projector-acceptance.md` proves `dataplatform-svc` client-credentials → GSP PUT → GET → SPARQL against dev. The Worklode-side, repeatable form is this plan's harness (Tasks 1–4). |
| 5 | Writable fixed branch | data-platform | **Done.** `PUT /branches/main/graphs?graph=<iri>` (`crates/graph-server/src/app.rs:53-59`); ADR-0003 §2: "every project writes to the one fixed writable branch `main`". |
| 6–7 | If-Match/ETag CAS; per-branch write ACLs (should-have) | data-platform | ACLs have a plan (`docs/plans/2026-07-22-graph-server-write-acls.md`); no CAS plan found. Non-blocking for v1 (single projector + per-branch lock). → Issue D. |

**Worklode-side build scope** (all of it in this plan):

- `internal/graphserver` — a client for graph-server's real API. This is
  **not** the same surface as the knowledge-graph plan's
  `internal/graph/client.go`, which speaks Oxigraph-native `/update` and
  `/query`; graph-server exposes GSP and a read-only `/sparql` only (see
  Overlaps §3).
- `e2e/graphserver_test.go` — the spec's Acceptance section as an env-gated
  test, runnable against dev today and prod when Issue A lands.
- README documentation and the five hand-off issues.

No migration is needed. (For the record: migration ids are assigned
sequentially at execution time by a migration-id script; 0001–0005 are on
main, and 0006/0007 are currently claimed by in-flight worktrees
(`task-hierarchy`, `skills-task3`). Any id a sibling plan mentions is
provisional until it actually executes — not this plan's concern.)

## Overlaps and open questions

1. **Where the IRI grammar lives in Go: resolved to `internal/kg/iri`.**
   Sibling plans previously disagreed: `internal/iri`
   (2026-07-30-knowledge-graph), `internal/kg/iri`
   (2026-07-30-platform-graph-design, extended by
   2026-07-30-design-documents-as-graph-objects), `internal/graphproj/iri.go`
   (2026-07-30-runtime-layer). Resolved at the planning tier in favor of
   `internal/kg/iri`, consistent with this plan's existing assumption — two
   plans already built on it, and the design-documents plan's Overlaps §1
   already directs consolidation toward a single minting path. This plan
   sidesteps the collision regardless: `internal/graphserver` treats graph
   and node IRIs as opaque strings, and the e2e fixture hard-codes its IRIs
   (they are test data, not production minting).
2. **Published IRI authority: schema now agrees, instances and named graphs
   still conflict.** Worklode spec 006 (amended by 014 §1;
   `docs/specs/000-umbrella-architecture.md:94`) puts schema, concepts, and
   instances under `https://worklode.io/ns/` (`wl:` = `…/ontology#`,
   `wlid:` = `…/id/…`) — the schema and concept half now matches
   Data-platform ADR-0003's schema base `https://worklode.io/ns/ontology#`.
   The instance and named-graph halves still conflict: ADR-0003 fixes
   instances at `https://data.sunstone.institute/wl/id/<type>/<local-id>`
   and named graphs at
   `https://data.sunstone.institute/wl/graph/project/<name>`, explicitly
   *rejecting* minting instances under `worklode.io`; Worklode mints
   instances at `…/ns/id/…` and (per the knowledge-graph plan) projection
   graphs at `…/ns/graph/workstream/<project-id>`. This plan's harness
   follows the Worklode specs (graph-server stores any well-formed IRI, so
   nothing breaks), and Issue B carries the narrowed decision — instance and
   named-graph authority only — to the data-platform team. Spec 009 item 3
   says the grammar "must be fixed and agreed"; the schema half now is, the
   instance half is not.
3. **Write-path mismatch with the knowledge-graph plan.** That plan's
   projector maintains tasks by per-subject `DELETE`/`INSERT` through a
   SPARQL Update endpoint (`internal/graph/client.go` posts to `/update`).
   graph-server has no update endpoint: writes are whole-named-graph
   PUT/POST/DELETE (`crates/graph-server/src/app.rs:53-59`), and `/sparql`
   proxies reads only (`proxy.rs:25` — writing Oxigraph directly would
   diverge from the SoR). Before the projector targets graph-server it must
   either write whole Workstream graphs via `graphserver.PutGraph`, or
   graph-server must grow branch-scoped SPARQL Update. Flagged in Issue E;
   the knowledge-graph plan should be revised at planning tier when it lands.
4. **The `observed/repo-implements` deriver stays with spec 007.** The
   design-documents plan builds its pure half (`internal/kg/implements`) and
   its deferral table assigns the deriver itself (fetching, named-graph PUT,
   triggers) to spec 007's plan. Cross-references sometimes route it via 009
   because the PUT needs a graph server; this plan supplies the write client
   the deriver will use and builds nothing more of it.
5. **Spec 009's Context block needs an amendment callout** recording the
   status table above (Oxigraph/materializer deployed in dev; runbook
   exists; prod still missing graph-server). Spec edits are planning-tier
   work, deliberately not a task here.

---

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `internal/graphserver/client.go` | graph-server client: branch-scoped GSP `PutGraph`/`GetGraph`/`DeleteGraph`, `Select` via `/sparql`, bearer auth |
| `internal/graphserver/client_test.go` | httptest: exact paths, query encoding, methods, content types, auth header, status handling |
| `internal/graphserver/env.go` | `FromEnv`: `LODE_GRAPHSERVER_*` → client with Keycloak client-credentials TokenSource |
| `internal/graphserver/env_test.go` | unset/partial env errors; unauthenticated mode; token flows into requests |
| `e2e/graphserver_test.go` | the spec 009 Acceptance sequence, env-gated, against a live graph-server |

**Modified files**

| Path | Change |
|---|---|
| `README.md` | "Graph-server acceptance" subsection under `## Development` (`README.md:340`) |

**Test commands**

- This plan's package (no Postgres, no network): `go test ./internal/graphserver/...`
- Acceptance harness (skips cleanly without env — CI already runs
  `go test -race -count=1 -tags e2e ./e2e/`, `.github/workflows/_test.yml:55`):
  `go test -tags e2e ./e2e/ -run TestGraphServerAcceptance -v`
- Everything: `go test ./...` (store/API/cmd suites need Postgres via `store.OpenTestStore`)

---

## Task 1: GSP client — PutGraph, GetGraph, DeleteGraph

**Files:**
- Create: `internal/graphserver/client.go`
- Test: `internal/graphserver/client_test.go`

graph-server's write surface, per `crates/graph-server/src/app.rs:53-59` and
the runbook: `PUT|GET|DELETE /branches/{branch}/graphs?graph=<url-encoded
IRI>`; PUT answers 201 on create, 204 on replace
(`crates/graph-server/src/gsp.rs:128-133`); GET of an empty graph answers
404 "graph has no visible quads" (`gsp.rs:147`).

- [ ] **Step 1: Write the failing test**

```go
package graphserver_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/sunstoneinstitute/worklode/internal/graphserver"
)

// record captures one request.
type record struct {
	method, path, rawQuery, contentType, accept, auth, body string
}

// recordingServer answers every request with status and respBody.
func recordingServer(t *testing.T, status int, respBody string) (*httptest.Server, *record) {
	t.Helper()
	rec := &record{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*rec = record{
			method: r.Method, path: r.URL.Path, rawQuery: r.URL.RawQuery,
			contentType: r.Header.Get("Content-Type"),
			accept:      r.Header.Get("Accept"),
			auth:        r.Header.Get("Authorization"),
			body:        string(b),
		}
		w.WriteHeader(status)
		io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

const graphIRI = "https://worklode.io/ns/graph/workstream/acme"

func authed(srvURL string) *graphserver.Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok"})
	return graphserver.New(srvURL, ts)
}

func TestPutGraphCreated(t *testing.T) {
	srv, rec := recordingServer(t, http.StatusCreated, "")
	created, err := authed(srv.URL).PutGraph(context.Background(), "main", graphIRI, []byte("<urn:a> <urn:b> <urn:c> ."))
	if err != nil {
		t.Fatalf("PutGraph: %v", err)
	}
	if !created {
		t.Fatal("created = false; want true on 201")
	}
	if rec.method != http.MethodPut || rec.path != "/branches/main/graphs" {
		t.Fatalf("request = %s %s; want PUT /branches/main/graphs", rec.method, rec.path)
	}
	if rec.rawQuery != "graph=https%3A%2F%2Fworklode.io%2Fns%2Fwl%2Fgraph%2Fworkstream%2Facme" {
		t.Fatalf("query = %q; want the url-encoded graph IRI", rec.rawQuery)
	}
	if rec.contentType != "text/turtle" {
		t.Fatalf("content type = %q; want text/turtle", rec.contentType)
	}
	if rec.body != "<urn:a> <urn:b> <urn:c> ." {
		t.Fatalf("body = %q", rec.body)
	}
	if rec.auth != "Bearer tok" {
		t.Fatalf("auth = %q; want Bearer tok", rec.auth)
	}
}

func TestPutGraphReplaced(t *testing.T) {
	srv, _ := recordingServer(t, http.StatusNoContent, "")
	created, err := authed(srv.URL).PutGraph(context.Background(), "main", graphIRI, []byte("<urn:a> <urn:b> <urn:c> ."))
	if err != nil {
		t.Fatalf("PutGraph: %v", err)
	}
	if created {
		t.Fatal("created = true; want false on 204 (idempotent re-PUT)")
	}
}

func TestPutGraphError(t *testing.T) {
	srv, _ := recordingServer(t, http.StatusForbidden, "missing readwrite role")
	_, err := authed(srv.URL).PutGraph(context.Background(), "main", graphIRI, nil)
	if err == nil {
		t.Fatal("PutGraph on 403: want an error")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "missing readwrite role") {
		t.Fatalf("error = %v; want status and body", err)
	}
}

func TestGetGraph(t *testing.T) {
	srv, rec := recordingServer(t, http.StatusOK, "<urn:a> <urn:b> <urn:c> .")
	body, err := authed(srv.URL).GetGraph(context.Background(), "main", graphIRI)
	if err != nil {
		t.Fatalf("GetGraph: %v", err)
	}
	if string(body) != "<urn:a> <urn:b> <urn:c> ." {
		t.Fatalf("body = %q", body)
	}
	if rec.method != http.MethodGet || rec.path != "/branches/main/graphs" {
		t.Fatalf("request = %s %s; want GET /branches/main/graphs", rec.method, rec.path)
	}
	if rec.accept != "text/turtle" {
		t.Fatalf("accept = %q; want text/turtle", rec.accept)
	}
}

func TestGetGraphNotFound(t *testing.T) {
	srv, _ := recordingServer(t, http.StatusNotFound, "graph has no visible quads")
	_, err := authed(srv.URL).GetGraph(context.Background(), "main", graphIRI)
	if !errors.Is(err, graphserver.ErrNotFound) {
		t.Fatalf("error = %v; want ErrNotFound", err)
	}
}

func TestDeleteGraph(t *testing.T) {
	srv, rec := recordingServer(t, http.StatusNoContent, "")
	if err := authed(srv.URL).DeleteGraph(context.Background(), "main", graphIRI); err != nil {
		t.Fatalf("DeleteGraph: %v", err)
	}
	if rec.method != http.MethodDelete || rec.path != "/branches/main/graphs" {
		t.Fatalf("request = %s %s; want DELETE /branches/main/graphs", rec.method, rec.path)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/graphserver/...`
Expected: FAIL — `no required module provides package .../internal/graphserver`

- [ ] **Step 3: Write the implementation**

```go
// Package graphserver is a client for the data-platform graph-server
// (data-platform crates/graph-server) — the knowledge-graph system of
// record that spec 009 requires the data-platform to host.
//
// graph-server's surface, unlike a plain SPARQL endpoint, is:
//   - branch-scoped Graph Store Protocol writes:
//     PUT/GET/POST/DELETE /branches/{branch}/graphs?graph=<iri>
//     (PUT answers 201 on create, 204 on replace);
//   - a read-only POST /sparql proxying the Oxigraph materialization.
//
// There is no SPARQL Update endpoint: writes replace or merge whole named
// graphs. IRIs are opaque strings here; minting is owned elsewhere
// (internal/kg/iri once the platform-graph-design plan lands).
package graphserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
)

// ErrNotFound is returned when the named graph has no visible quads on the
// requested branch.
var ErrNotFound = errors.New("graph not found")

// ErrSPARQLUnavailable is returned when /sparql answers 503 — Oxigraph or
// its materializer is not serving (yet). Callers may retry.
var ErrSPARQLUnavailable = errors.New("sparql endpoint unavailable")

// Client talks to one graph-server instance.
type Client struct {
	base string
	http *http.Client
}

// New returns a client for the graph-server at base
// (e.g. https://graph.dev.sunstoneinstitute.ai). A non-nil ts attaches a
// Bearer token to every request; nil means unauthenticated (tests, or a
// server running without AUTH_ENFORCE).
func New(base string, ts oauth2.TokenSource) *Client {
	hc := http.DefaultClient
	if ts != nil {
		hc = oauth2.NewClient(context.Background(), ts)
	}
	return &Client{base: strings.TrimRight(base, "/"), http: hc}
}

func (c *Client) graphURL(branch, graphIRI string) string {
	return c.base + "/branches/" + url.PathEscape(branch) +
		"/graphs?graph=" + url.QueryEscape(graphIRI)
}

// PutGraph replaces the named graph on branch with the given Turtle.
// The bool reports whether the graph was created (201) as opposed to
// replaced (204); an idempotent re-PUT returns false.
func (c *Client) PutGraph(ctx context.Context, branch, graphIRI string, turtle []byte) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.graphURL(branch, graphIRI), bytes.NewReader(turtle))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "text/turtle")
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated:
		return true, nil
	case http.StatusNoContent:
		return false, nil
	default:
		return false, httpError("PUT graph", resp)
	}
}

// GetGraph returns the named graph on branch as Turtle. ErrNotFound means
// the graph has no visible quads there.
func (c *Client) GetGraph(ctx context.Context, branch, graphIRI string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.graphURL(branch, graphIRI), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/turtle")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return io.ReadAll(resp.Body)
	case http.StatusNotFound:
		return nil, fmt.Errorf("graph %s on %s: %w", graphIRI, branch, ErrNotFound)
	default:
		return nil, httpError("GET graph", resp)
	}
}

// DeleteGraph removes the named graph from branch.
func (c *Client) DeleteGraph(ctx context.Context, branch, graphIRI string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.graphURL(branch, graphIRI), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("graph %s on %s: %w", graphIRI, branch, ErrNotFound)
	default:
		return httpError("DELETE graph", resp)
	}
}

// httpError folds a non-2xx response into one error carrying status and a
// bounded body excerpt.
func httpError(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("%s: %d %s: %s", op, resp.StatusCode,
		http.StatusText(resp.StatusCode), strings.TrimSpace(string(body)))
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/graphserver/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/graphserver
git commit -m "Add a graph-server GSP client"
```

---

## Task 2: SPARQL SELECT via the /sparql proxy

**Files:**
- Modify: `internal/graphserver/client.go`
- Test: `internal/graphserver/client_test.go`

`POST /sparql` with `application/sparql-query`, answers
`application/sparql-results+json`; 503 while Oxigraph or the materializer is
down (runbook: "Without it, /sparql returns 503").

- [ ] **Step 1: Write the failing test**

Append to `internal/graphserver/client_test.go`:

```go
func TestSelect(t *testing.T) {
	srv, rec := recordingServer(t, http.StatusOK, `{
		"head": {"vars": ["component"]},
		"results": {"bindings": [
			{"component": {"type": "uri", "value": "https://worklode.io/ns/id/component/comp-b"}}
		]}
	}`)
	rows, err := authed(srv.URL).Select(context.Background(), "SELECT ?component WHERE {}")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/sparql" {
		t.Fatalf("request = %s %s; want POST /sparql", rec.method, rec.path)
	}
	if rec.contentType != "application/sparql-query" {
		t.Fatalf("content type = %q; want application/sparql-query", rec.contentType)
	}
	if rec.accept != "application/sparql-results+json" {
		t.Fatalf("accept = %q", rec.accept)
	}
	if rec.body != "SELECT ?component WHERE {}" {
		t.Fatalf("body = %q; want the raw query", rec.body)
	}
	want := []map[string]string{{"component": "https://worklode.io/ns/id/component/comp-b"}}
	if len(rows) != 1 || rows[0]["component"] != want[0]["component"] {
		t.Fatalf("rows = %v; want %v", rows, want)
	}
}

func TestSelectUnavailable(t *testing.T) {
	srv, _ := recordingServer(t, http.StatusServiceUnavailable, "oxigraph unavailable")
	_, err := authed(srv.URL).Select(context.Background(), "SELECT * WHERE {}")
	if !errors.Is(err, graphserver.ErrSPARQLUnavailable) {
		t.Fatalf("error = %v; want ErrSPARQLUnavailable", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/graphserver/...`
Expected: FAIL — `c.Select undefined`

- [ ] **Step 3: Write the implementation**

Append to `internal/graphserver/client.go` (add `"encoding/json"` to the
imports):

```go
// Select runs a SPARQL SELECT against /sparql (the Oxigraph read path) and
// returns one map per solution, variable name → bound value.
// ErrSPARQLUnavailable means Oxigraph or its materializer is not serving.
func (c *Client) Select(ctx context.Context, query string) ([]map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/sparql", strings.NewReader(query))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/sparql-query")
	req.Header.Set("Accept", "application/sparql-results+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusServiceUnavailable:
		return nil, fmt.Errorf("select: %w", ErrSPARQLUnavailable)
	default:
		return nil, httpError("select", resp)
	}
	var out struct {
		Results struct {
			Bindings []map[string]struct {
				Value string `json:"value"`
			} `json:"bindings"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("select: decode results: %w", err)
	}
	rows := make([]map[string]string, 0, len(out.Results.Bindings))
	for _, b := range out.Results.Bindings {
		row := make(map[string]string, len(b))
		for v, cell := range b {
			row[v] = cell.Value
		}
		rows = append(rows, row)
	}
	return rows, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/graphserver/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/graphserver
git commit -m "Add SPARQL SELECT to the graph-server client"
```

---

## Task 3: Client from environment, with Keycloak client credentials

**Files:**
- Create: `internal/graphserver/env.go`
- Test: `internal/graphserver/env_test.go`

Follows the server's `LODE_*` env convention (`internal/cmd/serve.go:67-81`).
Spec 009 item 4 fixes the auth mechanism: Keycloak client-credentials
(`dataplatform-svc`).

- [ ] **Step 1: Write the failing test**

```go
package graphserver_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/graphserver"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"LODE_GRAPHSERVER_URL", "LODE_GRAPHSERVER_TOKEN_URL",
		"LODE_GRAPHSERVER_CLIENT_ID", "LODE_GRAPHSERVER_CLIENT_SECRET",
	} {
		t.Setenv(k, "")
	}
}

func TestFromEnvUnset(t *testing.T) {
	clearEnv(t)
	if _, err := graphserver.FromEnv(); err == nil {
		t.Fatal("FromEnv without LODE_GRAPHSERVER_URL: want an error")
	}
}

func TestFromEnvPartialAuth(t *testing.T) {
	clearEnv(t)
	t.Setenv("LODE_GRAPHSERVER_URL", "https://graph.example")
	t.Setenv("LODE_GRAPHSERVER_CLIENT_ID", "dataplatform-svc")
	if _, err := graphserver.FromEnv(); err == nil {
		t.Fatal("FromEnv with a partial credential set: want an error")
	}
}

func TestFromEnvUnauthenticated(t *testing.T) {
	clearEnv(t)
	srv, rec := recordingServer(t, http.StatusCreated, "")
	t.Setenv("LODE_GRAPHSERVER_URL", srv.URL)
	c, err := graphserver.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if _, err := c.PutGraph(context.Background(), "main", graphIRI, nil); err != nil {
		t.Fatalf("PutGraph: %v", err)
	}
	if rec.auth != "" {
		t.Fatalf("auth = %q; want none without token config", rec.auth)
	}
}

func TestFromEnvClientCredentials(t *testing.T) {
	clearEnv(t)
	tok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"cc-token","token_type":"Bearer","expires_in":3600}`)
	}))
	t.Cleanup(tok.Close)
	srv, rec := recordingServer(t, http.StatusCreated, "")
	t.Setenv("LODE_GRAPHSERVER_URL", srv.URL)
	t.Setenv("LODE_GRAPHSERVER_TOKEN_URL", tok.URL)
	t.Setenv("LODE_GRAPHSERVER_CLIENT_ID", "dataplatform-svc")
	t.Setenv("LODE_GRAPHSERVER_CLIENT_SECRET", "s3cret")
	c, err := graphserver.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if _, err := c.PutGraph(context.Background(), "main", graphIRI, nil); err != nil {
		t.Fatalf("PutGraph: %v", err)
	}
	if rec.auth != "Bearer cc-token" {
		t.Fatalf("auth = %q; want the client-credentials token", rec.auth)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/graphserver/...`
Expected: FAIL — `undefined: graphserver.FromEnv`

- [ ] **Step 3: Write the implementation**

```go
package graphserver

import (
	"context"
	"errors"
	"os"

	"golang.org/x/oauth2/clientcredentials"
)

// FromEnv builds a Client from the environment:
//
//	LODE_GRAPHSERVER_URL            base URL, e.g. https://graph.dev.sunstoneinstitute.ai (required)
//	LODE_GRAPHSERVER_TOKEN_URL      Keycloak token endpoint (client-credentials)
//	LODE_GRAPHSERVER_CLIENT_ID      OAuth2 client id, e.g. dataplatform-svc
//	LODE_GRAPHSERVER_CLIENT_SECRET  OAuth2 client secret
//
// The three auth variables must be set together or not at all; absent, the
// client is unauthenticated (a server without AUTH_ENFORCE).
func FromEnv() (*Client, error) {
	base := os.Getenv("LODE_GRAPHSERVER_URL")
	if base == "" {
		return nil, errors.New("LODE_GRAPHSERVER_URL is not set")
	}
	tokenURL := os.Getenv("LODE_GRAPHSERVER_TOKEN_URL")
	id := os.Getenv("LODE_GRAPHSERVER_CLIENT_ID")
	secret := os.Getenv("LODE_GRAPHSERVER_CLIENT_SECRET")
	if tokenURL == "" && id == "" && secret == "" {
		return New(base, nil), nil
	}
	if tokenURL == "" || id == "" || secret == "" {
		return nil, errors.New("LODE_GRAPHSERVER_TOKEN_URL, LODE_GRAPHSERVER_CLIENT_ID and LODE_GRAPHSERVER_CLIENT_SECRET must be set together")
	}
	cc := clientcredentials.Config{ClientID: id, ClientSecret: secret, TokenURL: tokenURL}
	return New(base, cc.TokenSource(context.Background())), nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/graphserver/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/graphserver
git commit -m "Configure the graph-server client from the environment"
```

---

## Task 4: The acceptance harness

**Files:**
- Create: `e2e/graphserver_test.go`

The spec's Acceptance section as a repeatable test: authenticate, PUT a
Worklode named graph to the fixed `main` branch under the spec-006/014 IRI
scheme, read it back over GSP, and answer "components with no governing
DesignDoc" over SPARQL. Mirrors the data-platform runbook
(`docs/runbooks/2026-07-22-worklode-projector-acceptance.md`) but under
Worklode's IRI grammar and with a graph-scoped query, unique per run, cleaned
up afterwards. Env-gated so CI's `go test -tags e2e ./e2e/`
(`.github/workflows/_test.yml:55`) skips it; the same file runs against prod
unchanged once Issue A lands.

- [ ] **Step 1: Write the test**

```go
//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graphserver"
)

// TestGraphServerAcceptance is spec 009's acceptance criterion against a
// live graph-server. Skipped unless LODE_GRAPHSERVER_URL is set; see the
// README "Graph-server acceptance" section for the full env.
func TestGraphServerAcceptance(t *testing.T) {
	if os.Getenv("LODE_GRAPHSERVER_URL") == "" {
		t.Skip("LODE_GRAPHSERVER_URL not set")
	}
	client, err := graphserver.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	ctx := context.Background()

	// Unique fixture per run: comp-a is governed, comp-b is the drift.
	// IRIs follow spec 006 as amended by 014 §1 (base worklode.io/ns/);
	// the workstream graph family follows the knowledge-graph plan.
	nonce := fmt.Sprintf("e2e-%d", time.Now().UnixNano())
	graphIRI := "https://worklode.io/ns/graph/workstream/" + nonce
	compA := "https://worklode.io/ns/id/component/" + nonce + "/comp-a"
	compB := "https://worklode.io/ns/id/component/" + nonce + "/comp-b"
	doc := "https://worklode.io/ns/id/doc/" + nonce + "-doc"
	turtle := fmt.Sprintf(`@prefix wl: <https://worklode.io/ns/ontology#> .
<%s> a wl:Component .
<%s> a wl:Component .
<%s> a wl:DesignDoc ; wl:governs <%s> .
`, compA, compB, doc, compA)

	// Step 1+2 — authenticate (the token source) and PUT to fixed main.
	created, err := client.PutGraph(ctx, "main", graphIRI, []byte(turtle))
	if err != nil {
		t.Fatalf("PutGraph: %v", err)
	}
	if !created {
		t.Fatalf("first PUT of %s: created = false; nonce collision?", graphIRI)
	}
	t.Cleanup(func() {
		if err := client.DeleteGraph(context.Background(), "main", graphIRI); err != nil {
			t.Logf("cleanup DeleteGraph: %v", err)
		}
	})

	// Idempotent re-PUT (spec 009 item 4: the atomic per-branch write).
	if created, err = client.PutGraph(ctx, "main", graphIRI, []byte(turtle)); err != nil {
		t.Fatalf("re-PUT: %v", err)
	}
	if created {
		t.Fatal("re-PUT: created = true; want 204 replace")
	}

	// Step 3 — read back over GSP.
	body, err := client.GetGraph(ctx, "main", graphIRI)
	if err != nil {
		t.Fatalf("GetGraph: %v", err)
	}
	for _, iri := range []string{compA, compB, doc} {
		if !strings.Contains(string(body), iri) {
			t.Fatalf("read-back is missing %s:\n%s", iri, body)
		}
	}

	// Step 4 — the drift question over SPARQL, scoped to this run's graph.
	// The materializer is asynchronous, so poll until it catches up.
	query := fmt.Sprintf(`PREFIX wl: <https://worklode.io/ns/ontology#>
SELECT ?component WHERE {
  GRAPH <%s> {
    ?component a wl:Component .
    FILTER NOT EXISTS { ?doc a wl:DesignDoc ; wl:governs ?component . }
  }
}`, graphIRI)
	deadline := time.Now().Add(90 * time.Second)
	for {
		rows, err := client.Select(ctx, query)
		switch {
		case errors.Is(err, graphserver.ErrSPARQLUnavailable) || (err == nil && len(rows) == 0):
			if time.Now().After(deadline) {
				t.Fatalf("materializer did not catch up: rows=%v err=%v", rows, err)
			}
			time.Sleep(3 * time.Second)
			continue
		case err != nil:
			t.Fatalf("Select: %v", err)
		}
		if len(rows) != 1 || rows[0]["component"] != compB {
			t.Fatalf("drift query = %v; want exactly the ungoverned %s", rows, compB)
		}
		return
	}
}
```

- [ ] **Step 2: Verify the skip path (what CI will do)**

Run: `go test -tags e2e ./e2e/ -run TestGraphServerAcceptance -v`
Expected: compiles; `--- SKIP: TestGraphServerAcceptance` with
"LODE_GRAPHSERVER_URL not set". This is the fail-safe CI path.

- [ ] **Step 3: Run against dev graph-server**

Needs network access to dev and the `dataplatform-svc` secret (1Password
item `dataplatform-svc`; run the `op` command directly — do not probe
sign-in first). If either is unavailable in this session, stop after the
skip-path check and note it in the task report; the run below is then an
operator step.

```bash
export LODE_GRAPHSERVER_URL=https://graph.dev.sunstoneinstitute.ai
export LODE_GRAPHSERVER_TOKEN_URL=https://auth.sunstoneinstitute.ai/realms/sunstone/protocol/openid-connect/token
export LODE_GRAPHSERVER_CLIENT_ID=dataplatform-svc
export LODE_GRAPHSERVER_CLIENT_SECRET="$(op item get dataplatform-svc --fields credential --reveal)"
go test -tags e2e ./e2e/ -run TestGraphServerAcceptance -v
```

Expected: PASS in well under the 90 s materializer deadline. A FAIL in the
SPARQL step with `sparql endpoint unavailable` for the whole window means
the dev materializer is down — a data-platform ops issue, not a harness bug;
report it rather than loosening the test.

- [ ] **Step 4: Commit**

```bash
git add e2e/graphserver_test.go
git commit -m "Add the spec 009 acceptance harness against graph-server"
```

---

## Task 5: Document the harness

**Files:**
- Modify: `README.md` (under `## Development`, `README.md:340`)

- [ ] **Step 1: Add the section**

Insert at the end of the `## Development` section (before `### CI gate`,
`README.md:370`):

````markdown
### Graph-server acceptance (spec 009)

`e2e/graphserver_test.go` proves the knowledge-graph hand-off end-to-end
against a live data-platform graph-server: Keycloak client-credentials
auth, a named-graph PUT to the fixed `main` branch, a GSP read-back, and a
drift query over `/sparql`. It skips unless configured:

```bash
export LODE_GRAPHSERVER_URL=https://graph.dev.sunstoneinstitute.ai
export LODE_GRAPHSERVER_TOKEN_URL=https://auth.sunstoneinstitute.ai/realms/sunstone/protocol/openid-connect/token
export LODE_GRAPHSERVER_CLIENT_ID=dataplatform-svc
export LODE_GRAPHSERVER_CLIENT_SECRET="$(op item get dataplatform-svc --fields credential --reveal)"
go test -tags e2e ./e2e/ -run TestGraphServerAcceptance -v
```

Each run writes one uniquely-named graph and deletes it afterwards. Point
`LODE_GRAPHSERVER_URL` at prod to re-certify after a graph-server deploy.
The client behind it lives in `internal/graphserver`; the manual
equivalent is the data-platform runbook
`docs/runbooks/2026-07-22-worklode-projector-acceptance.md`.
````

- [ ] **Step 2: Run the full suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "Document the graph-server acceptance harness"
```

---

## Task 6: File the cross-repo hand-off issues

**Files:** none in this repo (issues in `sunstoneinstitute/data-platform`
and `sunstoneinstitute/rdf-registry`).

One issue per open spec item, each self-contained with its acceptance
criterion. Titles carry the spec reference so the data-platform team can
trace them back.

- [ ] **Step 1: Issue A — prod deployment (must-haves 1 + 2)**

```bash
gh issue create -R sunstoneinstitute/data-platform \
  --title "Prod deployment of graph-server, Oxigraph and the materializer (Worklode spec 009, must-haves 1-2)" \
  --body "$(cat <<'EOF'
Worklode's knowledge graph treats graph-server as its system of record
(worklode docs/specs/009-data-platform-kg-requirements.md). The dev and
hzdev overlays carry the full stack (graph-server-deployment.yaml,
oxigraph-deployment.yaml, oxigraph-materializer-deployment.yaml,
graph-server-migrate-job.yaml, ingress/services/PVC), but
deploy/overlays/prod/kustomization.yaml deploys only the Nessie/Iceberg
base — the KG cannot be authoritative on a dev-only service.

Needed: the graph-server + Oxigraph + materializer resources in the prod
overlay (auth enforced, prod ingress host, prod Keycloak client roles for
dataplatform-svc mirroring dataplatform-dev:readwrite).

Acceptance: docs/runbooks/2026-07-22-worklode-projector-acceptance.md
passes against the prod URL, and Worklode's automated harness
(worklode e2e/graphserver_test.go, `go test -tags e2e -run
TestGraphServerAcceptance`) passes with LODE_GRAPHSERVER_URL pointed at
prod.
EOF
)"
```

- [ ] **Step 2: Issue B — the instance/named-graph IRI authority conflict (must-have 3)**

```bash
gh issue create -R sunstoneinstitute/data-platform \
  --title "Align Worklode instance/named-graph IRI authority: ADR-0003 vs Worklode spec 006/014 (spec 009, must-have 3)" \
  --body "$(cat <<'EOF'
Two accepted documents fix conflicting IRI grammars for instances and named
graphs. The schema half already agrees — both fix
https://worklode.io/ns/ontology#<Term>:

- docs/adr/0003-hosting-worklode-kg-iri-scheme.md: instances
  https://data.sunstone.institute/wl/id/<type>/<local-id>; named graphs
  https://data.sunstone.institute/wl/graph/project/<name>. It explicitly
  rejects minting instances under worklode.io.
- Worklode spec 006 as amended by 014 §1 (the canonical scheme per spec
  009 item 3): instances https://worklode.io/ns/id/…, concepts
  https://worklode.io/ns/concept/…, and (per Worklode's knowledge-graph
  plan) projection graphs https://worklode.io/ns/graph/workstream/<project-id>.

graph-server stores any well-formed IRI, so no code changes hinge on the
outcome — but spec 009 requires the grammar to be "fixed and agreed", and
today the instance and named-graph authority is fixed twice, differently.
Worklode's acceptance harness and future projector write the
worklode.io/ns/ form.

Needed: a joint decision on instance and named-graph authority, then either
an ADR-0003 amendment + runbook update (adopting Worklode's grammar) or a
Worklode spec 006/014 amendment (adopting ADR-0003's split-authority
grammar). Worklode-side contact: worklode docs/specs/006-knowledge-graph.md
§Canonical IRI scheme.
EOF
)"
```

- [ ] **Step 3: Issue C — the rdf-registry base-URL override (must-have 3)**

```bash
gh issue create -R sunstoneinstitute/rdf-registry \
  --title "Publish the wl ontology under the worklode.io/ns/ base (Worklode spec 009, must-have 3)" \
  --body "$(cat <<'EOF'
The wl ontology sources stay in rdf-registry but must publish under
Worklode's base, not sunstone.institute/rdf/ — ADR-0006's implicit
"repo path = host path" mapping does not hold for a foreign domain, so the
pipeline needs a base-URL override.

A plan already exists on branch worklode-io-spec (commit 56768ba,
docs/superpowers/plans/2026-07-22-worklode-ns-base-override.md) but
predates Worklode spec 014 §1, which moved the sources to rdf/wl/ and the
published base to https://worklode.io/ns/ (sources stay under rdf/wl/; the
published base has no wl/ segment — schema namespace is
https://worklode.io/ns/ontology#). Update the plan to that base and land it.

Not a runtime blocker for graph-server hosting; required for
https://worklode.io/ns/ontology to dereference as a document.
Coordinates with Worklode's rdf/wl/ vocabulary staging (worklode
docs/plans/2026-07-30-knowledge-graph-1-graph-foundations.md, Task 2),
which is the content of the eventual PR.
EOF
)"
```

- [ ] **Step 4: Issue D — should-haves 6 and 7**

```bash
gh issue create -R sunstoneinstitute/data-platform \
  --title "If-Match/ETag CAS and per-branch write ACLs before a second work-graph writer (Worklode spec 009, should-haves 6-7)" \
  --body "$(cat <<'EOF'
Non-blocking for Worklode v1: a single projector plus the per-branch write
lock contains lost-update risk. Wanted before any second writer touches
the work graph:

- If-Match / ETag compare-and-swap on GSP writes (graph-server spec v1.1;
  no plan found in this repo yet).
- Per-branch / per-namespace write ACLs scoping Worklode's graphs from
  other producers — docs/plans/2026-07-22-graph-server-write-acls.md
  already covers this; this issue tracks Worklode's dependency on it.

Source: worklode docs/specs/009-data-platform-kg-requirements.md,
"Should-have".
EOF
)"
```

- [ ] **Step 5: Issue E — write surface for the Worklode projector**

```bash
gh issue create -R sunstoneinstitute/data-platform \
  --title "Question: will graph-server expose branch-scoped SPARQL Update, or is GSP the only write surface?" \
  --body "$(cat <<'EOF'
graph-server writes are whole-named-graph PUT/POST/DELETE on
/branches/{branch}/graphs; /sparql proxies reads to the Oxigraph
materialization only. Worklode's projector design (worklode
docs/plans/2026-07-30-knowledge-graph-2-projector.md) currently maintains
tasks by per-subject DELETE/INSERT via SPARQL Update, which this surface
cannot express — a task update would rewrite its whole Workstream graph.

No action needed yet; Worklode needs to know which way to converge:

- if GSP stays the only write path, Worklode re-plans the projector to
  whole-graph PUT granularity (fine at current volumes);
- if branch-scoped SPARQL Update (or a per-subject patch operation) is on
  the roadmap, the per-subject design stands.

Source: worklode docs/specs/009-data-platform-kg-requirements.md items
2/4; worklode plan overlap notes.
EOF
)"
```

- [ ] **Step 6: Record the issue URLs**

Paste the five created issue URLs into the task report (and into
`lode`/the tracking system if the executing session has one). No repo file
changes; nothing to commit.

---

## Self-Review Notes

Spec sections and where they are handled:

| Spec 009 item | Handled by |
|---|---|
| Must-have 1 (prod graph-server) | status table; Issue A (Task 6) |
| Must-have 2 (query/read path) | verified done in dev (status table); prod via Issue A; exercised by Task 4's SPARQL step |
| Must-have 3 (IRI scheme + base-URL override) | Issues B + C (Task 6); Overlaps §2; grammar itself owned by specs 006/014 and sibling plans |
| Must-have 4 (external write auth) | Tasks 1–4 (the repeatable external-caller proof); dev already proven by the data-platform runbook |
| Must-have 5 (fixed writable branch) | verified done (status table); Task 4 writes to `main` |
| Should-haves 6–7 (CAS, ACLs) | Issue D (Task 6) |
| Explicitly-not-required list | nothing built here — leases stay in the backbone, no merge/diff, no markdown-as-asset |
| Acceptance | Task 4, end to end |

Deliberate choices:

- `internal/graphserver` is a new package rather than an extension of the
  knowledge-graph plan's `internal/graph` because that package does not
  exist yet and targets a different protocol surface (Oxigraph-native
  `/update`/`/query`); Overlaps §3 records the convergence obligation.
- The harness hard-codes fixture IRIs instead of waiting for
  `internal/kg/iri` (unimplemented sibling) — they are test data, and this
  keeps the plan free of cross-plan build dependencies.
- The drift query is graph-scoped (`GRAPH <…>`), unlike the runbook's
  dataset-wide query: it makes the test safe on a shared dev instance and
  additionally verifies the materializer preserves named graphs.
- No spec 009 edit: the stale Context block is flagged in Overlaps §5 for a
  planning-tier amendment callout, not silently rewritten here.
