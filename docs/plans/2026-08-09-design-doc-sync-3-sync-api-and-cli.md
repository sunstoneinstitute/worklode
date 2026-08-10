---
status: draft
covers: docs/specs/034-design-doc-sync.md
---
# Design-doc sync, part 3 — API surface and lode doc

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** The `/api/v1/docs` surface (bulk-upsert sync target, get, list) with
its spec-022 metrics, the client methods, the git default-branch/clean-tree
gate, and the `lode doc sync` / `lode doc list` commands — completing spec 034
§12 acceptance items 2, 3, 4, 5, and 8.

**Architecture:** Depends on parts 1 and 2
(`2026-08-09-design-doc-sync-1-client-foundations.md`,
`2026-08-09-design-doc-sync-2-document-store.md`). Handlers stay thin
(parse/validate → `RecordEvent` → store → respond), matching
`internal/api/tasks.go`. The gate is client-side: `lode doc sync` reads the
current branch, the remote's default branch, and porcelain cleanliness through
new `internal/worktree` helpers, refuses off-default or dirty without
`--force`, and always ships provenance (source branch + dirty flag) so the
server records it (034 §3). `--dry-run` is served by the store's read-only
`DocSyncOutcomes`. The event payload is a slim summary (project, provenance,
counts), not the document bodies — the docs table already holds the content,
and events must stay bounded.

**Tech Stack:** Go 1.26, net/http ServeMux routes, cobra, prometheus,
`e2e` build-tagged tests over public surfaces only.

## Global constraints

- `lode doc sync [--force] [-n|--dry-run] [--json]`; `lode doc list
  [--kind spec|adr|plan] [--status <s>] [--project <id>] [--json]` (034 §3,
  §6; `--needs-planning`/`--needs-execution` are spec 026's read surface and
  are NOT built here).
- Default branch from the remote's HEAD; cleanliness from
  `git status --porcelain` (034 §3).
- Sync is one-way, git → backbone; the API performs no editorial transitions.
- Wire outcomes are exactly `added` / `updated` / `unchanged`; a document with
  unparseable frontmatter fails the whole sync client-side (034 §3, via part
  1's loader).
- Metrics per spec 022: `worklode_` prefix, owning package's `metrics.go`
  pattern (here: `internal/api/metrics.go`), nil-safe, bounded labels, tests.
- e2e tests drive public surfaces only — HTTP API via `cli.Client` — never
  direct store writes.
- Store tests and e2e need Postgres (`TEST_POSTGRES_DSN`, default
  `postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable`).
- Run `go build ./...` and the named tests before every commit. Never put
  `Co-authored-by` or any agent advertisement in commit messages.

## Tasks

### Task 1 — POST /api/v1/docs/sync with metrics

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
```

**Files:**
- Create: `internal/api/docs.go`
- Modify: `internal/api/server.go` (route table, after the board route ~L420)
- Modify: `internal/api/metrics.go` (`initMetrics`, plus a new observe helper)
- Test: `internal/api/docs_test.go`, `internal/api/metrics_internal_test.go`

**Interfaces produced (the wire contract Tasks 3 and 6 consume):**

```
POST /api/v1/docs/sync   (bearer auth)
request  {"project": "wl", "source_branch": "main", "dirty": false,
          "force": false, "dry_run": false,
          "docs": [{"kind": "spec", "ordinal": "34", "status": "accepted",
                    "title": "...", "body": "...", "frontmatter": {...},
                    "sections": [{"anchor": "sec-1", "heading": "Scope",
                                  "depth": 2, "position": 0}],
                    "edges": [{"src_anchor": "sec-1", "rel": "amends",
                               "target": "025-x.md", "target_anchor": "sec-2"}]}]}
response {"dry_run": false, "added": 1, "updated": 0, "unchanged": 0,
          "results": [{"id": "WL-SPEC-34", "kind": "spec", "outcome": "added"}]}
```

Errors: 422 for store validation (`ErrInvalidInput`), 404 for an unknown
project, 400 for a bad body — all through the existing `mapStoreErr` /
`writeBodyErr`.

- [ ] **Step 1: Write the failing tests** — create
  `internal/api/docs_test.go` (external test package, same as
  `tasks_test.go`; uses `newTestServer` and `doReq` from `server_test.go`):

```go
package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func specDocPayload() map[string]any {
	return map[string]any{
		"kind": "spec", "ordinal": "34", "status": "accepted",
		"title":       "Spec 034 — Design-doc sync",
		"body":        "---\nstatus: accepted\n---\n# Spec 034 — Design-doc sync\n\n## 1. Scope {#sec-1}\n\nBody.\n",
		"frontmatter": map[string]any{"status": "accepted"},
		"sections": []map[string]any{
			{"anchor": "sec-1", "heading": "Scope", "depth": 2, "position": 0},
		},
		"edges": []map[string]any{
			{"src_anchor": "sec-1", "rel": "amends",
				"target": "025-documents-in-the-backbone.md", "target_anchor": "sec-2"},
		},
	}
}

func syncBody(dryRun bool, docs ...map[string]any) map[string]any {
	return map[string]any{
		"project": "wl", "source_branch": "main", "dirty": false,
		"force": false, "dry_run": dryRun, "docs": docs,
	}
}

func createDocProject(t *testing.T, h http.Handler, token string) {
	t.Helper()
	rr := doReq(t, h, http.MethodPost, "/api/v1/projects", token,
		map[string]any{"id": "wl", "name": "Worklode", "key": "WL"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rr.Code, rr.Body)
	}
}

func TestDocsSync(t *testing.T) {
	_, h, token := newTestServer(t)
	createDocProject(t, h, token)

	rr := doReq(t, h, http.MethodPost, "/api/v1/docs/sync", token, syncBody(false, specDocPayload()))
	if rr.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", rr.Code, rr.Body)
	}
	var resp struct {
		DryRun    bool `json:"dry_run"`
		Added     int  `json:"added"`
		Unchanged int  `json:"unchanged"`
		Results   []struct{ ID, Kind, Outcome string }
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Added != 1 || len(resp.Results) != 1 || resp.Results[0].ID != "WL-SPEC-34" ||
		resp.Results[0].Outcome != "added" {
		t.Fatalf("resp = %+v", resp)
	}

	// Idempotent: second run reports unchanged (034 §12.2).
	rr = doReq(t, h, http.MethodPost, "/api/v1/docs/sync", token, syncBody(false, specDocPayload()))
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Unchanged != 1 || resp.Added != 0 {
		t.Fatalf("second sync = %+v, want one unchanged", resp)
	}
}

func TestDocsSyncDryRunWritesNothing(t *testing.T) {
	_, h, token := newTestServer(t)
	createDocProject(t, h, token)

	rr := doReq(t, h, http.MethodPost, "/api/v1/docs/sync", token, syncBody(true, specDocPayload()))
	if rr.Code != http.StatusOK {
		t.Fatalf("dry run: %d %s", rr.Code, rr.Body)
	}
	var resp struct {
		DryRun bool `json:"dry_run"`
		Added  int  `json:"added"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.DryRun || resp.Added != 1 {
		t.Fatalf("dry-run resp = %+v", resp)
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/docs/WL-SPEC-34", token, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("dry run wrote: GET = %d", rr.Code) // relies on Task 2's GET; until it lands, assert via a real sync + unchanged=0 instead
	}
}

func TestDocsSyncErrors(t *testing.T) {
	_, h, token := newTestServer(t)
	createDocProject(t, h, token)

	bad := specDocPayload()
	bad["kind"] = "memo"
	if rr := doReq(t, h, http.MethodPost, "/api/v1/docs/sync", token, syncBody(false, bad)); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad kind: %d, want 422", rr.Code)
	}
	body := syncBody(false, specDocPayload())
	body["project"] = "nope"
	if rr := doReq(t, h, http.MethodPost, "/api/v1/docs/sync", token, body); rr.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d, want 404", rr.Code)
	}
	if rr := doReq(t, h, http.MethodPost, "/api/v1/docs/sync", "", syncBody(false)); rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", rr.Code)
	}
}
```

(The GET assertion in the dry-run test lands with Task 2; if executing tasks
strictly in order, write that one line as the fallback the comment names and
tighten it in Task 2.)

Metrics test — append to `internal/api/metrics_internal_test.go` (internal
package; construct the server like its existing tests do):

```go
func TestObserveDocSync(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := &server{}
	s.initMetrics(reg)

	s.observeDocSync([]store.DocSyncResult{
		{DocID: "WL-SPEC-34", Kind: "spec", Outcome: "added"},
		{DocID: "WL-PLAN-34-1", Kind: "plan", Outcome: "unchanged"},
	}, true, nil, 40*time.Millisecond)

	if got := testutil.ToFloat64(s.docSyncDocs.WithLabelValues("spec", "added")); got != 1 {
		t.Errorf("docSyncDocs{spec,added} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(s.docSyncForced); got != 1 {
		t.Errorf("docSyncForced = %v, want 1", got)
	}
	if got := testutil.ToFloat64(s.docSyncRuns.WithLabelValues("ok")); got != 1 {
		t.Errorf("docSyncRuns{ok} = %v, want 1", got)
	}

	// Nil-safe: a server built without initMetrics must not panic.
	(&server{}).observeDocSync(nil, false, nil, 0)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/api -run 'TestDocsSync|TestObserveDocSync' -v`
Expected: compile errors / 404s — handler, routes, and instruments missing.

- [ ] **Step 3: Implement.** `internal/api/docs.go`:

```go
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

type docSectionJSON struct {
	Anchor   string `json:"anchor"`
	Heading  string `json:"heading"`
	Depth    int    `json:"depth"`
	Position int    `json:"position"`
}

type docEdgeJSON struct {
	SrcAnchor    string `json:"src_anchor"`
	Rel          string `json:"rel"`
	Target       string `json:"target"`
	TargetAnchor string `json:"target_anchor"`
}

type docUpsertJSON struct {
	Kind        string           `json:"kind"`
	Ordinal     string           `json:"ordinal"`
	Status      string           `json:"status"`
	Title       string           `json:"title"`
	Body        string           `json:"body"`
	Frontmatter json.RawMessage  `json:"frontmatter"`
	Sections    []docSectionJSON `json:"sections"`
	Edges       []docEdgeJSON    `json:"edges"`
}

type docSyncRequest struct {
	Project      string          `json:"project"`
	SourceBranch string          `json:"source_branch"`
	Dirty        bool            `json:"dirty"`
	Force        bool            `json:"force"`
	DryRun       bool            `json:"dry_run"`
	Docs         []docUpsertJSON `json:"docs"`
}

type docResultJSON struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Outcome string `json:"outcome"`
}

type docSyncResponse struct {
	DryRun    bool            `json:"dry_run"`
	Added     int             `json:"added"`
	Updated   int             `json:"updated"`
	Unchanged int             `json:"unchanged"`
	Results   []docResultJSON `json:"results"`
}

func toStoreUpserts(in []docUpsertJSON) []store.DocUpsert {
	out := make([]store.DocUpsert, 0, len(in))
	for _, d := range in {
		u := store.DocUpsert{
			Kind: d.Kind, Ordinal: d.Ordinal, Status: d.Status,
			Title: d.Title, Body: d.Body, Frontmatter: d.Frontmatter,
		}
		for _, sec := range d.Sections {
			u.Sections = append(u.Sections, store.DocSection(sec))
		}
		for _, e := range d.Edges {
			u.Edges = append(u.Edges, store.DocEdge(e))
		}
		out = append(out, u)
	}
	return out
}

func toSyncResponse(dryRun bool, results []store.DocSyncResult) docSyncResponse {
	resp := docSyncResponse{DryRun: dryRun, Results: []docResultJSON{}}
	for _, r := range results {
		resp.Results = append(resp.Results, docResultJSON{ID: r.DocID, Kind: r.Kind, Outcome: r.Outcome})
		switch r.Outcome {
		case "added":
			resp.Added++
		case "updated":
			resp.Updated++
		case "unchanged":
			resp.Unchanged++
		}
	}
	return resp
}

// syncDocs handles POST /api/v1/docs/sync — spec 034 §3/§4's bulk upsert.
func (s *server) syncDocs(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req docSyncRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	upserts := toStoreUpserts(req.Docs)

	if req.DryRun {
		results, err := s.st.DocSyncOutcomes(r.Context(), req.Project, upserts)
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toSyncResponse(true, results))
		return
	}

	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	// Slim payload: the docs table holds the content; the event holds the act.
	payload, err := json.Marshal(map[string]any{
		"project": req.Project, "source_branch": req.SourceBranch,
		"dirty": req.Dirty, "force": req.Force, "doc_count": len(req.Docs),
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	now := s.st.Now()
	prov := store.DocSyncProvenance{SourceBranch: req.SourceBranch, Dirty: req.Dirty}

	var results []store.DocSyncResult
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "docs.synced", payload,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			results, err = s.st.ApplyDocSync(tx, now, eventID, req.Project, prov, upserts)
			return err
		})
	s.observeDocSync(results, req.Force, err, time.Since(start))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSyncResponse(false, results))
}
```

Route in `server.go`, after the board route:

```go
	mux.Handle("POST /api/v1/docs/sync", s.auth(s.syncDocs))
```

Instruments in `internal/api/metrics.go` — server fields:

```go
	docSyncRuns     *prometheus.CounterVec
	docSyncDuration prometheus.Histogram
	docSyncDocs     *prometheus.CounterVec
	docSyncForced   prometheus.Counter
```

in `initMetrics`:

```go
	s.docSyncRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_doc_sync_runs_total",
		Help: "Doc sync requests by result.",
	}, []string{"result"})
	s.docSyncDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "worklode_doc_sync_duration_seconds",
		Help:    "Doc sync request duration.",
		Buckets: []float64{0.05, 0.1, 0.5, 1, 5, 15},
	})
	s.docSyncDocs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_doc_sync_docs_total",
		Help: "Documents synced, by kind and outcome.",
	}, []string{"kind", "outcome"})
	s.docSyncForced = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "worklode_doc_sync_forced_total",
		Help: "Forced (--force) doc syncs accepted.",
	})
```

(add the four to the `reg.MustRegister(...)` call; pre-initialise
`docSyncRuns` ok/error), plus:

```go
// observeDocSync records one sync request. Nil-safe: tests build a *server
// directly without initMetrics.
func (s *server) observeDocSync(results []store.DocSyncResult, forced bool, err error, d time.Duration) {
	if s.docSyncRuns == nil {
		return
	}
	s.docSyncDuration.Observe(d.Seconds())
	result := "ok"
	if err != nil {
		result = "error"
	}
	s.docSyncRuns.WithLabelValues(result).Inc()
	if forced {
		s.docSyncForced.Inc()
	}
	for _, r := range results {
		s.docSyncDocs.WithLabelValues(r.Kind, r.Outcome).Inc()
	}
}
```

(add `"github.com/sunstoneinstitute/worklode/internal/store"` to
metrics.go's imports; kind and outcome are both closed three-value sets, so
labels stay bounded.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/api -run 'TestDocsSync|TestObserveDocSync' -v`
then `go test ./internal/api`.
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/docs.go internal/api/docs_test.go internal/api/server.go \
        internal/api/metrics.go internal/api/metrics_internal_test.go
git commit -m "api: POST /api/v1/docs/sync with spec-022 metrics (spec 034 §4, §10)"
```

### Task 2 — GET /api/v1/docs and GET /api/v1/docs/{id}

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:**
- Modify: `internal/api/docs.go`
- Modify: `internal/api/server.go` (two routes)
- Test: `internal/api/docs_test.go`

**Interfaces produced (wire contract for Tasks 3 and 6):**

```
GET /api/v1/docs?project=wl&kind=spec&status=draft
  → {"docs": [{"id", "project", "kind", "ordinal", "status", "title",
               "version", "source_branch", "source_dirty", "synced_at"}]}
     (no body in list rows)
GET /api/v1/docs/{id}
  → one docJSON plus "body", "frontmatter", "sections": [docSectionJSON],
    "edges": [docEdgeJSON]; 404 when unknown
```

- [ ] **Step 1: Write the failing tests** — append to
  `internal/api/docs_test.go`:

```go
func TestDocsListAndGet(t *testing.T) {
	_, h, token := newTestServer(t)
	createDocProject(t, h, token)
	plan := map[string]any{
		"kind": "plan", "ordinal": "34-1", "status": "draft", "title": "Part 1",
		"body": "---\nstatus: draft\n---\n# Part 1\n",
		"frontmatter": map[string]any{"status": "draft"},
		"edges": []map[string]any{
			{"rel": "implements", "target": "docs/specs/034-design-doc-sync.md"},
		},
	}
	doReq(t, h, http.MethodPost, "/api/v1/docs/sync", token, syncBody(false, specDocPayload(), plan))

	rr := doReq(t, h, http.MethodGet, "/api/v1/docs?project=wl", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body)
	}
	var list struct {
		Docs []struct {
			ID, Kind, Status, Title, Body string
		} `json:"docs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Docs) != 2 {
		t.Fatalf("list = %+v, want 2 docs", list.Docs)
	}
	for _, d := range list.Docs {
		if d.Body != "" {
			t.Errorf("%s: list row carries a body", d.ID)
		}
	}

	rr = doReq(t, h, http.MethodGet, "/api/v1/docs?project=wl&kind=plan", token, nil)
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Docs) != 1 || list.Docs[0].ID != "WL-PLAN-34-1" {
		t.Fatalf("kind filter = %+v", list.Docs)
	}

	rr = doReq(t, h, http.MethodGet, "/api/v1/docs/WL-SPEC-34", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rr.Code, rr.Body)
	}
	var got struct {
		ID       string `json:"id"`
		Body     string `json:"body"`
		Sections []struct{ Anchor string }
		Edges    []struct{ Rel string }
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "WL-SPEC-34" || got.Body == "" || len(got.Sections) != 1 || len(got.Edges) != 1 {
		t.Fatalf("get = %+v", got)
	}

	if rr := doReq(t, h, http.MethodGet, "/api/v1/docs/WL-SPEC-999", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("missing doc: %d, want 404", rr.Code)
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/docs?kind=memo", token, nil); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad kind filter: %d, want 422", rr.Code)
	}
}
```

Also tighten `TestDocsSyncDryRunWritesNothing`'s final assertion to the GET
form shown in Task 1 if it was left in fallback form.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/api -run TestDocsListAndGet -v`
Expected: FAIL — 404 (routes absent).

- [ ] **Step 3: Implement** in `internal/api/docs.go`:

```go
// docJSON is the wire form of a stored doc. Body and Frontmatter are omitted
// from list responses.
type docJSON struct {
	ID           string           `json:"id"`
	Project      string           `json:"project"`
	Kind         string           `json:"kind"`
	Ordinal      string           `json:"ordinal"`
	Status       string           `json:"status"`
	Title        string           `json:"title"`
	Version      int              `json:"version"`
	SourceBranch string           `json:"source_branch"`
	SourceDirty  bool             `json:"source_dirty"`
	SyncedAt     time.Time        `json:"synced_at"`
	Body         string           `json:"body,omitempty"`
	Frontmatter  json.RawMessage  `json:"frontmatter,omitempty"`
	Sections     []docSectionJSON `json:"sections,omitempty"`
	Edges        []docEdgeJSON    `json:"edges,omitempty"`
}

func toDocJSON(d *store.Doc) docJSON {
	return docJSON{
		ID: d.DocID, Project: d.Project, Kind: d.Kind, Ordinal: d.Ordinal,
		Status: d.Status, Title: d.Title, Version: d.Version,
		SourceBranch: d.SourceBranch, SourceDirty: d.SourceDirty,
		SyncedAt: d.SyncedAt, Body: d.Body, Frontmatter: d.Frontmatter,
	}
}

// listDocs handles GET /api/v1/docs.
func (s *server) listDocs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.DocFilter{Project: q.Get("project"), Kind: q.Get("kind"), Status: q.Get("status")}
	if f.Kind != "" && f.Kind != "spec" && f.Kind != "adr" && f.Kind != "plan" {
		writeErr(w, http.StatusUnprocessableEntity, "invalid kind: must be spec, adr, or plan")
		return
	}
	docs, err := s.st.ListDocs(r.Context(), f)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	out := make([]docJSON, 0, len(docs))
	for i := range docs {
		out = append(out, toDocJSON(&docs[i])) // list rows: store leaves Body ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"docs": out})
}

// getDoc handles GET /api/v1/docs/{id}.
func (s *server) getDoc(w http.ResponseWriter, r *http.Request) {
	d, secs, edges, err := s.st.GetDoc(r.Context(), r.PathValue("id"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	out := toDocJSON(d)
	for _, sec := range secs {
		out.Sections = append(out.Sections, docSectionJSON(sec))
	}
	for _, e := range edges {
		out.Edges = append(out.Edges, docEdgeJSON(e))
	}
	writeJSON(w, http.StatusOK, out)
}
```

Routes in `server.go`, beside the sync route (the literal `sync` segment must
be registered so Go's mux prefers it over `{id}` — same trick as
`/api/v1/projects/resolve`):

```go
	mux.Handle("GET /api/v1/docs", s.auth(s.listDocs))
	mux.Handle("GET /api/v1/docs/{id}", s.auth(s.getDoc))
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/api -run 'TestDocs' -v`, then `go test ./internal/api`.
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/docs.go internal/api/docs_test.go internal/api/server.go
git commit -m "api: GET /api/v1/docs list and get (spec 034 §4, §6)"
```

### Task 3 — Client methods: SyncDocs, ListDocs, GetDoc, DocTable

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

**Files:**
- Modify: `internal/cli/client.go` (new `--- docs ---` section after the
  inbox section)
- Modify: `internal/cli/render.go` (`DocTable`)
- Test: `internal/cli/client_test.go`, `internal/cli/render_test.go`

**Interfaces produced (consumed by Task 5):**

```go
type DocSection struct {
	Anchor   string `json:"anchor"`
	Heading  string `json:"heading"`
	Depth    int    `json:"depth"`
	Position int    `json:"position"`
}

type DocEdge struct {
	SrcAnchor    string `json:"src_anchor"`
	Rel          string `json:"rel"`
	Target       string `json:"target"`
	TargetAnchor string `json:"target_anchor"`
}

type DocUpsert struct {
	Kind        string          `json:"kind"`
	Ordinal     string          `json:"ordinal"`
	Status      string          `json:"status"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	Frontmatter json.RawMessage `json:"frontmatter"`
	Sections    []DocSection    `json:"sections,omitempty"`
	Edges       []DocEdge       `json:"edges,omitempty"`
}

type DocSyncInput struct {
	Project      string
	SourceBranch string
	Dirty        bool
	Force        bool
	DryRun       bool
	Docs         []DocUpsert
}

type DocSyncResult struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Outcome string `json:"outcome"`
}

type DocSyncReport struct {
	DryRun    bool            `json:"dry_run"`
	Added     int             `json:"added"`
	Updated   int             `json:"updated"`
	Unchanged int             `json:"unchanged"`
	Results   []DocSyncResult `json:"results"`
}

// Doc is the wire form of a stored document (list rows have Body == "").
type Doc struct {
	ID           string          `json:"id"`
	Project      string          `json:"project"`
	Kind         string          `json:"kind"`
	Ordinal      string          `json:"ordinal"`
	Status       string          `json:"status"`
	Title        string          `json:"title"`
	Version      int             `json:"version"`
	SourceBranch string          `json:"source_branch"`
	SourceDirty  bool            `json:"source_dirty"`
	SyncedAt     time.Time       `json:"synced_at"`
	Body         string          `json:"body,omitempty"`
	Frontmatter  json.RawMessage `json:"frontmatter,omitempty"`
	Sections     []DocSection    `json:"sections,omitempty"`
	Edges        []DocEdge       `json:"edges,omitempty"`
}

type DocListResponse struct {
	Docs []Doc `json:"docs"`
}

func (c *Client) SyncDocs(ctx context.Context, in DocSyncInput) (DocSyncReport, []byte, error) // POST /api/v1/docs/sync
func (c *Client) ListDocs(ctx context.Context, project, kind, status string) (DocListResponse, []byte, error) // GET /api/v1/docs
func (c *Client) GetDoc(ctx context.Context, id string) (Doc, []byte, error) // GET /api/v1/docs/{id}

// render.go:
func DocTable(w io.Writer, docs []Doc)
```

- [ ] **Step 1: Write the failing tests.** Client method test (append to
  `internal/cli/client_test.go`, the `httptest.NewServer` capture style of
  `TestResolveRemoteSendsRawURL` L1308):

```go
func TestSyncDocsWire(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"dry_run":false,"added":1,"updated":0,"unchanged":0,
			"results":[{"id":"WL-SPEC-34","kind":"spec","outcome":"added"}]}`)
	}))
	defer srv.Close()

	c := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "wl_" + strings.Repeat("a", 40)})
	rep, _, err := c.SyncDocs(context.Background(), cli.DocSyncInput{
		Project: "wl", SourceBranch: "main", Force: true,
		Docs: []cli.DocUpsert{{Kind: "spec", Ordinal: "34", Status: "accepted",
			Title: "T", Body: "B", Frontmatter: json.RawMessage(`{"status":"accepted"}`)}},
	})
	if err != nil {
		t.Fatalf("SyncDocs: %v", err)
	}
	if gotPath != "/api/v1/docs/sync" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["project"] != "wl" || gotBody["force"] != true || gotBody["source_branch"] != "main" {
		t.Errorf("body = %v", gotBody)
	}
	if rep.Added != 1 || rep.Results[0].ID != "WL-SPEC-34" {
		t.Errorf("report = %+v", rep)
	}
}
```

Render test (append to `internal/cli/render_test.go` — an *internal* test
file, `package cli`, so no qualifier):

```go
func TestDocTable(t *testing.T) {
	var buf bytes.Buffer
	DocTable(&buf, []Doc{
		{ID: "WL-SPEC-34", Kind: "spec", Status: "accepted", Version: 2,
			SourceDirty: true, Title: "Design-doc sync"},
	})
	out := buf.String()
	for _, want := range []string{"ID", "KIND", "STATUS", "V", "DIRTY", "TITLE",
		"WL-SPEC-34", "accepted", "yes", "Design-doc sync"} {
		if !strings.Contains(out, want) {
			t.Errorf("DocTable output missing %q:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/cli -run 'TestSyncDocsWire|TestDocTable' -v`
Expected: compile error — the doc types are undefined.

- [ ] **Step 3: Implement.** In `client.go`, a `--- docs ---` section with
  the types above and:

```go
// SyncDocs calls POST /api/v1/docs/sync — the git→backbone bulk upsert
// (spec 034 §3).
func (c *Client) SyncDocs(ctx context.Context, in DocSyncInput) (DocSyncReport, []byte, error) {
	body := map[string]any{
		"project":       in.Project,
		"source_branch": in.SourceBranch,
		"dirty":         in.Dirty,
		"force":         in.Force,
		"dry_run":       in.DryRun,
		"docs":          in.Docs,
	}
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/docs/sync", body)
	if err != nil {
		return DocSyncReport{}, nil, err
	}
	var rep DocSyncReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		return DocSyncReport{}, nil, fmt.Errorf("decode sync report: %w", err)
	}
	return rep, raw, nil
}

// ListDocs calls GET /api/v1/docs. Empty filter values do not filter.
func (c *Client) ListDocs(ctx context.Context, project, kind, status string) (DocListResponse, []byte, error) {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	if kind != "" {
		q.Set("kind", kind)
	}
	if status != "" {
		q.Set("status", status)
	}
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/docs", q), nil)
	if err != nil {
		return DocListResponse{}, nil, err
	}
	var resp DocListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return DocListResponse{}, nil, fmt.Errorf("decode doc list: %w", err)
	}
	return resp, raw, nil
}

// GetDoc calls GET /api/v1/docs/{id}.
func (c *Client) GetDoc(ctx context.Context, id string) (Doc, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/docs/"+url.PathEscape(id), nil)
	if err != nil {
		return Doc{}, nil, err
	}
	var d Doc
	if err := json.Unmarshal(raw, &d); err != nil {
		return Doc{}, nil, fmt.Errorf("decode doc: %w", err)
	}
	return d, raw, nil
}
```

In `render.go`:

```go
// DocTable prints one row per synced document: id, kind, status, version, a
// dirty-provenance marker (034 §3), and title.
func DocTable(w io.Writer, docs []Doc) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "ID\tKIND\tSTATUS\tV\tDIRTY\tTITLE")
	for _, d := range docs {
		dirty := "-"
		if d.SourceDirty {
			dirty = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", d.ID, d.Kind, d.Status, d.Version, dirty, d.Title)
	}
	tw.Flush()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/client.go internal/cli/client_test.go internal/cli/render.go internal/cli/render_test.go
git commit -m "cli: doc sync/list/get client methods and table (spec 034 §4)"
```

### Task 4 — Git helpers: CurrentBranch, DefaultBranch, IsClean

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
```

The gate's three facts (034 §3): current branch, the default branch from the
remote's HEAD, and porcelain cleanliness. They live beside the existing git
helpers (`Root`, `Identity`, `GitDir`) in `internal/worktree`.

**Files:**
- Modify: `internal/worktree/worktree.go`
- Test: `internal/worktree/worktree_test.go` (`initGitRepo` helper exists at
  L169)

**Interfaces produced (consumed by Task 5):**

```go
func CurrentBranch(root string) (string, error) // symbolic-ref --short HEAD; error when detached
func DefaultBranch(root string) (string, error) // from refs/remotes/origin/HEAD
func IsClean(root string) (bool, error)         // git status --porcelain is empty
```

- [ ] **Step 1: Write the failing tests** — append to
  `internal/worktree/worktree_test.go`:

```go
func TestCurrentBranchAndIsClean(t *testing.T) {
	dir := initGitRepo(t)

	branch, err := worktree.CurrentBranch(dir)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	// initGitRepo commits on git's default init branch; whatever it is
	// called locally, it must be non-empty and match git's own answer.
	out, _ := exec.Command("git", "-C", dir, "symbolic-ref", "--short", "HEAD").Output()
	if want := strings.TrimSpace(string(out)); branch != want || branch == "" {
		t.Errorf("CurrentBranch = %q, want %q", branch, want)
	}

	clean, err := worktree.IsClean(dir)
	if err != nil || !clean {
		t.Fatalf("IsClean(fresh) = %v, %v; want true", clean, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "junk.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	clean, err = worktree.IsClean(dir)
	if err != nil || clean {
		t.Fatalf("IsClean(dirty) = %v, %v; want false", clean, err)
	}
}

func TestDefaultBranch(t *testing.T) {
	dir := initGitRepo(t)

	// No origin/HEAD recorded: a named error telling the user how to fix it.
	if _, err := worktree.DefaultBranch(dir); err == nil ||
		!strings.Contains(err.Error(), "git remote set-head") {
		t.Fatalf("DefaultBranch without origin/HEAD: err = %v, want set-head hint", err)
	}

	// Record origin/HEAD the way `git remote set-head origin --auto` would;
	// no network needed.
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", dir).CombinedOutput(); err != nil {
		t.Fatalf("remote add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "symbolic-ref",
		"refs/remotes/origin/HEAD", "refs/remotes/origin/main").CombinedOutput(); err != nil {
		t.Fatalf("set origin/HEAD: %v\n%s", err, out)
	}
	got, err := worktree.DefaultBranch(dir)
	if err != nil || got != "main" {
		t.Fatalf("DefaultBranch = %q, %v; want main", got, err)
	}
}
```

(`worktree_test.go` already imports `os`, `os/exec`, `path/filepath`,
`strings` — verify and add any missing.)

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/worktree -run 'TestCurrentBranch|TestDefaultBranch' -v`
Expected: compile error — the three functions are undefined.

- [ ] **Step 3: Implement** — append to `internal/worktree/worktree.go`:

```go
// CurrentBranch returns the branch checked out at root. A detached HEAD is
// an error: the doc-sync gate (spec 034 §3) needs a branch to compare and to
// record as provenance.
func CurrentBranch(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("resolve current branch (detached HEAD?): %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// DefaultBranch returns the repository's default branch as recorded by the
// remote's HEAD (spec 034 §3), read from refs/remotes/origin/HEAD — local
// state git clone writes, so no network round trip. A repo without it (an
// old clone, or `git init` with a remote added by hand) gets an error naming
// the fix.
func DefaultBranch(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "symbolic-ref", "refs/remotes/origin/HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("no default branch recorded for origin; run `git remote set-head origin --auto` (needs network) and retry")
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "refs/remotes/origin/"), nil
}

// IsClean reports whether root's working tree has no uncommitted changes,
// untracked files included — `git status --porcelain` prints nothing (034 §3).
func IsClean(root string) (bool, error) {
	out, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil {
		return false, fmt.Errorf("git status --porcelain: %w", err)
	}
	return len(strings.TrimSpace(string(out))) == 0, nil
}
```

One wrinkle the test surfaces: `initGitRepo`'s default branch may be `master`
or `main` depending on the developer's git config. `TestDefaultBranch` pins
`origin/HEAD` to `refs/remotes/origin/main` explicitly, so it is
config-independent; `TestCurrentBranchAndIsClean` compares against git's own
answer rather than a literal.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/worktree -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worktree/worktree.go internal/worktree/worktree_test.go
git commit -m "worktree: CurrentBranch/DefaultBranch/IsClean for the sync gate (spec 034 §3)"
```

### Task 5 — lode doc sync and lode doc list

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3, 4]
```

The command tree. The gate and payload assembly are pure functions with their
own tests; the cobra `RunE`s stay thin glue.

**Files:**
- Create: `internal/cmd/doc.go`
- Test: `internal/cmd/doc_test.go`

**Interfaces produced:**

```go
// syncGate is the observed git state the default-branch gate judges (034 §3).
type syncGate struct {
	Branch        string
	DefaultBranch string // "" when origin/HEAD is unrecorded
	DefaultErr    error  // the DefaultBranch error, when any
	Clean         bool
}

// checkSyncGate returns nil when the sync may proceed.
func checkSyncGate(g syncGate, force bool) error

// corpusToUpserts maps part 1's loader output onto the wire type.
func corpusToUpserts(docs []designdoc.CorpusDoc) []cli.DocUpsert
```

- [ ] **Step 1: Write the failing tests** — create
  `internal/cmd/doc_test.go`:

```go
package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

func TestCheckSyncGate(t *testing.T) {
	for name, tc := range map[string]struct {
		g       syncGate
		force   bool
		wantErr string // "" = allowed
	}{
		"default clean":         {g: syncGate{Branch: "main", DefaultBranch: "main", Clean: true}},
		"off default":           {g: syncGate{Branch: "feat", DefaultBranch: "main", Clean: true}, wantErr: "not on the default branch"},
		"dirty":                 {g: syncGate{Branch: "main", DefaultBranch: "main", Clean: false}, wantErr: "working tree is dirty"},
		"no origin head":        {g: syncGate{Branch: "main", DefaultErr: errors.New("no default branch recorded"), Clean: true}, wantErr: "no default branch recorded"},
		"forced off default":    {g: syncGate{Branch: "feat", DefaultBranch: "main", Clean: false}, force: true},
		"forced no origin head": {g: syncGate{Branch: "feat", DefaultErr: errors.New("x"), Clean: false}, force: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkSyncGate(tc.g, tc.force)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("gate refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), "--force") {
				t.Errorf("gate error %q does not mention --force", err)
			}
		})
	}
}

func TestCorpusToUpserts(t *testing.T) {
	in := []designdoc.CorpusDoc{{
		Filename: "034-x.md", Kind: "spec", Ordinal: "34",
		Status: "accepted", Title: "T", Source: []byte("body"),
		FrontmatterJSON: []byte(`{"status":"accepted"}`),
		Sections:        []designdoc.SectionMeta{{Anchor: "sec-1", Heading: "S", Depth: 2, Position: 0}},
		Edges:           []designdoc.EdgeMeta{{SrcAnchor: "sec-1", Rel: "amends", Target: "025-y.md", TargetAnchor: "sec-2"}},
	}}
	out := corpusToUpserts(in)
	if len(out) != 1 {
		t.Fatalf("len = %d", len(out))
	}
	u := out[0]
	if u.Kind != "spec" || u.Ordinal != "34" || u.Body != "body" ||
		string(u.Frontmatter) != `{"status":"accepted"}` ||
		len(u.Sections) != 1 || u.Sections[0].Anchor != "sec-1" ||
		len(u.Edges) != 1 || u.Edges[0].Rel != "amends" {
		t.Errorf("upsert = %+v", u)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/cmd -run 'TestCheckSyncGate|TestCorpusToUpserts' -v`
Expected: compile error — `syncGate`, `checkSyncGate`, `corpusToUpserts`
undefined.

- [ ] **Step 3: Implement** — create `internal/cmd/doc.go`:

```go
package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

func newDocCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doc",
		Short: "Sync and list design documents (specs, ADRs, plans)",
	}
	cmd.AddCommand(newDocSyncCmd(), newDocListCmd())
	return cmd
}

func init() {
	rootCmd.AddCommand(newDocCmd())
}

// syncGate is the observed git state the default-branch gate judges (034 §3).
type syncGate struct {
	Branch        string
	DefaultBranch string
	DefaultErr    error
	Clean         bool
}

// checkSyncGate enforces spec 034 §3: without --force, sync only from the
// default branch with a clean tree. Every refusal names --force as the
// escape hatch.
func checkSyncGate(g syncGate, force bool) error {
	if force {
		return nil
	}
	if g.DefaultErr != nil {
		return fmt.Errorf("%w (or pass --force to sync from %s anyway)", g.DefaultErr, g.Branch)
	}
	if g.Branch != g.DefaultBranch {
		return fmt.Errorf("not on the default branch (%s, default %s): the store is a projection of the reviewed corpus; pass --force to push a preview", g.Branch, g.DefaultBranch)
	}
	if !g.Clean {
		return errors.New("working tree is dirty: the store is a projection of the reviewed corpus; commit (or pass --force to push a preview)")
	}
	return nil
}

// corpusToUpserts maps the loader's corpus onto the wire type 1:1.
func corpusToUpserts(docs []designdoc.CorpusDoc) []cli.DocUpsert {
	out := make([]cli.DocUpsert, 0, len(docs))
	for _, d := range docs {
		u := cli.DocUpsert{
			Kind: d.Kind, Ordinal: d.Ordinal, Status: d.Status, Title: d.Title,
			Body: string(d.Source), Frontmatter: d.FrontmatterJSON,
		}
		for _, s := range d.Sections {
			u.Sections = append(u.Sections, cli.DocSection{
				Anchor: s.Anchor, Heading: s.Heading, Depth: s.Depth, Position: s.Position,
			})
		}
		for _, e := range d.Edges {
			u.Edges = append(u.Edges, cli.DocEdge{
				SrcAnchor: e.SrcAnchor, Rel: e.Rel, Target: e.Target, TargetAnchor: e.TargetAnchor,
			})
		}
		out = append(out, u)
	}
	return out
}

func newDocSyncCmd() *cobra.Command {
	var force, dryRun bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Push the configured git corpora to the backbone (spec 034)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			corpora, err := cli.CorporaFrom(cwd)
			if err != nil {
				return err
			}
			if corpora.SpecDir == "" && corpora.PlanDir == "" {
				return errors.New(`nothing configured to sync: set spec_corpus and/or plan_corpus in .worklode/config.toml (spec 034 §2)`)
			}

			root, ok := worktree.Root(cwd)
			if !ok {
				return errors.New("not inside a git worktree")
			}
			g := syncGate{}
			if g.Branch, err = worktree.CurrentBranch(root); err != nil {
				return err
			}
			g.DefaultBranch, g.DefaultErr = worktree.DefaultBranch(root)
			if g.Clean, err = worktree.IsClean(root); err != nil {
				return err
			}
			if err := checkSyncGate(g, force); err != nil {
				return err
			}

			docs, err := designdoc.LoadSyncCorpus(corpora.SpecDir, corpora.PlanDir)
			if err != nil {
				return err
			}

			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			if cfg.CurrentProject == "" {
				return errors.New(`no project: set current_project in .worklode/config.toml`)
			}
			rep, raw, err := c.SyncDocs(cmd.Context(), cli.DocSyncInput{
				Project:      cfg.CurrentProject,
				SourceBranch: g.Branch,
				Dirty:        !g.Clean,
				Force:        force,
				DryRun:       dryRun,
				Docs:         corpusToUpserts(docs),
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			renderSyncReport(cmd, rep)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "bypass the default-branch/clean-tree gate; provenance records the source branch and dirty flag")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "report what would change without writing")
	return cmd
}

// renderSyncReport prints the per-doc outcomes and a summary line.
func renderSyncReport(cmd *cobra.Command, rep cli.DocSyncReport) {
	out := cmd.OutOrStdout()
	for _, r := range rep.Results {
		if r.Outcome != "unchanged" {
			fmt.Fprintf(out, "%-9s %s\n", r.Outcome, r.ID)
		}
	}
	verb := "synced"
	if rep.DryRun {
		verb = "would sync"
	}
	fmt.Fprintf(out, "%s %d docs: %d added, %d updated, %d unchanged\n",
		verb, rep.Added+rep.Updated+rep.Unchanged, rep.Added, rep.Updated, rep.Unchanged)
}

func newDocListCmd() *cobra.Command {
	var project, kind, status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List synced design documents from the backbone",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			if project == "" {
				project = cfg.CurrentProject
			}
			resp, raw, err := c.ListDocs(cmd.Context(), project, kind, status)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.DocTable(cmd.OutOrStdout(), resp.Docs)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "filter by project id (default: current project)")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind: spec, adr, plan")
	cmd.Flags().StringVar(&status, "status", "", "filter by status (e.g. draft, accepted)")
	return cmd
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cmd -run 'TestCheckSyncGate|TestCorpusToUpserts' -v`,
then `go build ./... && go test ./internal/cmd`.
Expected: PASS.

- [ ] **Step 5: Manual smoke against the local stack** (compose stack up,
  `LODE_BOOTSTRAP_TOKEN` set; skip if no local stack — the e2e task covers
  the wire):

```bash
go run . doc sync --dry-run
go run . doc sync
go run . doc list
```

Expected: dry-run reports the repo's own corpus as `would sync`; the real run
adds them; `doc list` shows `WL-SPEC-*`/`WL-ADR-*`/`WL-PLAN-*` rows.

- [ ] **Step 6: Commit**

```bash
git add internal/cmd/doc.go internal/cmd/doc_test.go
git commit -m "cmd: lode doc sync / lode doc list with the default-branch gate (spec 034 §3, §6)"
```

### Task 6 — e2e: the sync round trip over public surfaces

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [5]
```

Prove acceptance 034 §12.2-.5 end to end: real Postgres, real server, real
HTTP, `cli.Client` only — no direct store writes. This drives the API the way
`lode doc sync` does; the git gate itself is client-local and already covered
by Task 5's unit tests.

**Files:**
- Create: `e2e/docsync_test.go`

- [ ] **Step 1: Write the test** — `e2e/docsync_test.go`, booting the stack
  exactly as `TestFullChain` (e2e/smoke_test.go:123) does:

```go
//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestDocSyncRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})

	if _, _, err := admin.CreateProject(ctx, cli.CreateProjectInput{
		ID: "docsync", Name: "Doc Sync", Key: "DS"}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	spec := cli.DocUpsert{
		Kind: "spec", Ordinal: "34", Status: "accepted",
		Title: "Spec 034 — Design-doc sync",
		Body:  "---\nstatus: accepted\n---\n# Spec 034 — Design-doc sync\n\n## 1. Scope {#sec-1}\n\nBody.\n",
		Frontmatter: json.RawMessage(`{"status":"accepted"}`),
		Sections:    []cli.DocSection{{Anchor: "sec-1", Heading: "Scope", Depth: 2}},
	}
	plan := cli.DocUpsert{
		Kind: "plan", Ordinal: "34-1", Status: "draft", Title: "Part 1",
		Body:        "---\nstatus: draft\nimplements: docs/specs/034-design-doc-sync.md\n---\n# Part 1\n",
		Frontmatter: json.RawMessage(`{"status":"draft"}`),
		Edges:       []cli.DocEdge{{Rel: "implements", Target: "docs/specs/034-design-doc-sync.md"}},
	}
	input := cli.DocSyncInput{Project: "docsync", SourceBranch: "main", Docs: []cli.DocUpsert{spec, plan}}

	// First sync: everything added (034 §12.2).
	rep, _, err := admin.SyncDocs(ctx, input)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if rep.Added != 2 || rep.Updated != 0 || rep.Unchanged != 0 {
		t.Fatalf("first sync = %+v, want 2 added", rep)
	}

	// Second sync: no changes (034 §12.2).
	if rep, _, err = admin.SyncDocs(ctx, input); err != nil || rep.Unchanged != 2 {
		t.Fatalf("second sync = %+v, %v; want 2 unchanged", rep, err)
	}

	// Dry run of a change: reported, not written (034 §12.4).
	changed := input
	changed.DryRun = true
	changed.Docs = append([]cli.DocUpsert{}, spec, plan)
	changed.Docs[0].Body += "\nmore\n"
	if rep, _, err = admin.SyncDocs(ctx, changed); err != nil || !rep.DryRun || rep.Updated != 1 {
		t.Fatalf("dry run = %+v, %v; want 1 updated", rep, err)
	}
	d, _, err := admin.GetDoc(ctx, "DS-SPEC-34")
	if err != nil || d.Version != 1 {
		t.Fatalf("after dry run: version = %d, %v; want 1 (nothing written)", d.Version, err)
	}

	// Forced sync records provenance (034 §12.3's server half).
	forced := input
	forced.SourceBranch, forced.Dirty, forced.Force = "feature-x", true, true
	if _, _, err = admin.SyncDocs(ctx, forced); err != nil {
		t.Fatalf("forced sync: %v", err)
	}
	if d, _, err = admin.GetDoc(ctx, "DS-SPEC-34"); err != nil ||
		d.SourceBranch != "feature-x" || !d.SourceDirty {
		t.Fatalf("forced provenance = %q/%v, %v; want feature-x/true", d.SourceBranch, d.SourceDirty, err)
	}

	// List returns the derived ids from the store (034 §12.5).
	list, _, err := admin.ListDocs(ctx, "docsync", "", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	ids := map[string]bool{}
	for _, doc := range list.Docs {
		ids[doc.ID] = true
	}
	if !ids["DS-SPEC-34"] || !ids["DS-PLAN-34-1"] {
		t.Fatalf("list ids = %v, want DS-SPEC-34 and DS-PLAN-34-1", ids)
	}
}
```

- [ ] **Step 2: Run it** (Postgres must be reachable)

Run: `go test -race -count=1 -tags e2e ./e2e/ -run TestDocSyncRoundTrip -v`
Expected: PASS. Then the whole suite:
`go test -race -count=1 -tags e2e ./e2e/`.

- [ ] **Step 3: Full verification sweep**

```bash
go build ./...
go test ./...
go test -race -count=1 -tags e2e ./e2e/
```

Expected: green everywhere. Walk spec 034 §12 once more and check each of the
eight criteria against the landed code (1, 6, 7 from part 1; 2-5, 8 from
parts 2-3).

- [ ] **Step 4: Commit**

```bash
git add e2e/docsync_test.go
git commit -m "e2e: doc sync round trip over the public API (spec 034 §12)"
```
