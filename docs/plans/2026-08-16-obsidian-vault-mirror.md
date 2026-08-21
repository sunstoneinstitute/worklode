---
status: draft
covers: NO-SPEC
---
# Obsidian vault mirror — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** A worklode instance becomes browsable as an Obsidian vault: one
folder per project, one note per doc and per task, linked to each other by
their backbone ids. Read-only in one direction (backbone → vault), but
serialized so the reverse is mechanical later. Two list endpoints gain the
expansion parameters that make the mirror's sync loop a fixed `1 + 2N`
requests instead of an N+1 walk.

**Architecture:** Two halves, sequenced. First, Go: `GET /api/v1/docs?body=true`
stops blanking `body`/`frontmatter`, and `GET /api/v1/tasks?detail=true` adds
`blocked` and `edges` to each row on the back of one new bulk store reader.
Second, TypeScript: a `plugins/obsidian/` directory holding an Obsidian
plugin whose only writable territory is one configured subfolder of the user's
vault. Inside the plugin, three pure modules (wire client, note serializer,
mirror diff) sit behind one thin Obsidian-aware shell, so everything load-bearing
is unit-testable without an Obsidian runtime.

**Tech Stack:** Go 1.26, `net/http` mux, `internal/api` + `internal/store`
(Postgres via pgx). Plugin: TypeScript 5, esbuild, vitest, the `yaml` package,
`obsidian` API types. `internal/api` and `internal/store` tests need Postgres
with pgvector (`TEST_POSTGRES_DSN`); they skip silently without it, and a
skipped run proves nothing.

**Spec:** None — `covers: NO-SPEC`. The API expansion is a parameter addition
to two endpoints already specified (004 §6.7, 025 §16); the plugin is an
external client of the public read API and constrains no backbone behaviour.
Task 10 files the follow-up to fold the two parameters into their specs' API
sections the next time those specs are amended.

**Read first:**
- `internal/api/tasks.go:231-344` — `listTasks`, `getTask`, `toTaskJSON`,
  `taskDetailJSON`; the shapes this plan extends
- `internal/api/docs.go:60-213` — `listDocs`, `getDoc`, `toDocJSON`
- `internal/store/tasks.go:53-71,469-545,664,785` — `TaskFilter`,
  `ListTasks`, `ListEdges`, `BlockedTaskIDs`
- `internal/store/docs.go:258-382` — `GetDoc`, `ListDocs`, `DocFilter`
- `internal/cli/client.go:620-691,1409-1488` — `TaskListFilter`, `ListTasks`,
  `TaskDetail`, `Doc`, `ListDocs`, `GetDoc`
- `internal/api/metrics.go` — `initMetrics` and the nil-safe observer idiom
  (`observeDocSync`, line 346)
- `.github/workflows/pr-checks.yml`, `_lint.yml` — the gate job and the
  reusable-workflow pattern task 4 follows
- `CLAUDE.md` — "Metrics" and the `internal/model` (ADR 036) direction

## Decision: what `?detail=true` does and does not return

`getTask` assembles five field groups on top of the base task, via five store
calls: `blocked` (`BlockedTaskIDs`), `edges` (`ListEdges`), `lease`
(`ActiveLease`), and `hierarchy.parent` / `hierarchy.progress` (`ParentOf`,
`ChildProgress`). Only the first is a bulk reader; the other four take a single
task id and have no plural variant anywhere in `internal/store`.

So `?detail=true` returns **`blocked` and `edges` only**, and it does not reuse
`taskDetailJSON`:

- `blocked` is free — `BlockedTaskIDs` already returns every blocked id in the
  store in one query, and `getTask` itself just does a map lookup against it.
- `edges` needs exactly one new bulk reader (task 2). It is also the field a
  list consumer genuinely cannot reconstruct.
- `hierarchy` is **derivable from the edges** by any client holding the list: a
  task's parent is its `child_of` out-edge, its children are its `child_of`
  in-edges, and their states are on the sibling rows. Adding it server-side
  would mean two more bulk readers to return data the caller already has.
- `lease` is per-task ephemeral state. A mirror that caches who holds a lease
  is stale the moment it is written; a client that needs it should ask
  `GET /api/v1/tasks/{id}`.

A response type that returned zeroed `hierarchy` and absent `lease` would be
lying about what it knows, so the expanded row gets its own type
(`taskListDetailJSON`) rather than borrowing the detail one. Anything wanting
the full five groups keeps calling `GET /api/v1/tasks/{id}`, unchanged.

## Decision: the mount folder is machine-owned

The plugin owns exactly one folder inside the vault (default `Worklode/`) and
regenerates everything under it, deleting whatever no longer corresponds to a
backbone object. The user's own notes live outside it and link in.

This is what makes "read-only" true rather than aspirational. Obsidian has no
per-file read-only bit, so the alternative — mirroring into the vault root, or
scattering notes among user content — means every sync must decide whether a
changed file is a stale mirror or the user's work. Confining the mirror to one
declared folder removes that decision entirely: inside the mount, the backbone
always wins; outside it, the plugin never writes.

## Serialization contract

One reserved top-level frontmatter key, `wl`, holds everything the backbone
owns. A document's own frontmatter is written alongside it, verbatim. `aliases`
is added only when the source frontmatter does not already have it.

**The round-trip rule, in one sentence:** strip `wl:`, strip `aliases:` when
`wl.aliases_added` is true, unwrap `[[...]]` from any relation value, and what
remains is the document exactly as the backbone holds it. Task 6 proves this
with a `parseNote(render(x)) === x` test, which is how "round-trippable" becomes
a checked property rather than a claim. Writing back is out of scope; the parser
exists so the property is testable now.

`wl.aliases_added` is load-bearing and not decoration. The plugin adds
`aliases` only when the source frontmatter lacks it, so on the way back there is
otherwise no way to tell a plugin-added alias from one the author wrote —
and guessing would silently delete an author's `aliases` on the first
write-back. Recording the one bit at render time is what makes the inverse
decidable.

Relations are written as Obsidian wikilink strings (`"[[WL-44]]"`) rather than
bare ids, because Obsidian resolves wikilinks inside frontmatter values and
that is what puts the edges into graph view — the whole point of the exercise.

## Global Constraints

- **Exact reserved key names.** The frontmatter block is `wl`. Its
  `type` is exactly `task` | `doc` | `project` | `index`. `serializer: 1` is
  the format version and is bumped only by a change that breaks `parseNote`.
  `etag` is the first 16 hex characters of the SHA-256 of the canonical
  (key-sorted) JSON of the backbone payload the note was rendered from.
- **Exact vault layout**, relative to the configured mount root:
  `<root>/<root-basename>.md` is the index; `<root>/<project>/<project>.md` is
  the project note; docs go in `<root>/<project>/docs/<DOC-ID>.md`; tasks go in
  `<root>/<project>/tasks/<TASK-ID>.md`. Filenames are the backbone id and
  nothing else, so `[[WL-42]]` and `[[WL-SPEC-025]]` resolve from anywhere in
  the vault.
- **The mount root is the only writable territory.** Every write and every
  delete is under it. A path that escapes it (`..`, an absolute path, an id
  containing `/`) is a bug — task 8's `desiredPath` sanitizes ids and its test
  pins that behaviour.
- **Never invent a value.** An absent backbone field renders as an absent
  frontmatter key or an empty list, never a placeholder, a `null` string, or a
  guessed default. A task with no body gets a note with no body.
- **Bodies are verbatim.** The backbone's `body` is written byte-for-byte after
  the frontmatter block and an `# <title>` H1. The plugin never reformats,
  re-wraps, or rewrites links inside a body.
- **Boolean query params use the repo's existing idiom**:
  `q.Get("x") == "true"` (`internal/api/tasks.go:331`,
  `internal/api/skills.go:76`). Any other value silently means false; it is
  never a 422.
- **Metrics** (CLAUDE.md, spec 022): one new instrument,
  `worklode_list_expansions_total{endpoint,expansion}`, with `endpoint` bounded
  to exactly `tasks` | `docs` and `expansion` to exactly `detail` | `body`.
  Incremented only when the expansion is requested. Nil-safe observer on
  `internal/api/metrics.go` following `observeDocSync` (line 346);
  `prometheus.Registerer` is already threaded. No per-project or per-id label,
  ever.
- **No Go dependency on the plugin.** `plugins/obsidian/` is not imported by the Go
  build, not embedded in the binary, and adds no Go module dependency. It ships
  no secrets: the token lives in Obsidian's per-vault `data.json`, in plaintext,
  and the README says so plainly rather than pretending otherwise.
- **Plugin module boundary.** `src/api/`, `src/serialize/` and `src/sync/`
  import nothing from `obsidian`. Only `src/vault/writer.ts` and `src/main.ts`
  do. This is what keeps the three testable under vitest with no Obsidian
  runtime; a task that breaks it has broken the test strategy.
- **Every task leaves `go test ./...` green** (and, from task 4 on,
  `npm --prefix plugins/obsidian test` green) and ends in its own commit. Commit
  messages describe the change, never the plan file, and never carry
  `Co-authored-by:` trailers.

---

## Tasks

### Task 1 — `GET /api/v1/docs?body=true` returns body and frontmatter

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

`store.ListDocs` does not select the `body` column at all, and `listDocs`
blanks `Body`/`Frontmatter` on every row. Make both conditional on a new filter
field.

In `internal/store/docs.go`, add to `DocFilter`:

```go
// DocFilter narrows ListDocs; zero fields do not filter.
type DocFilter struct {
	Project, Kind, Status string
	// Body includes the body column in the result rows. Off by default:
	// body is the large column, and most list callers only want the
	// catalogue. Frontmatter is selected either way (it always was).
	Body bool
}
```

In `ListDocs`, build the column list and the scan destinations together so they
cannot drift:

```go
cols := `project, kind, ordinal, doc_id, status, title, frontmatter,
         version, source_branch, source_dirty, synced_at, created_at, updated_at`
if f.Body {
	cols += `, body`
}
q := `SELECT ` + cols + ` FROM docs`
```

and in the row loop, collect the existing `&d.Field` arguments into a
`dest := []any{...}` slice in the current order, append `&d.Body` when
`f.Body`, and call `rows.Scan(dest...)`. Update the doc comment: rows are
bodyless unless `f.Body`.

In `internal/api/docs.go`, `listDocs` reads the param and stops blanking:

```go
f := store.DocFilter{
	Project: q.Get("project"),
	Kind:    q.Get("kind"),
	Status:  q.Get("status"),
	Body:    q.Get("body") == "true",
}
...
for i := range docs {
	dj := toDocJSON(&docs[i])
	if !f.Body {
		// List rows omit body and frontmatter unless body=true.
		dj.Body = ""
		dj.Frontmatter = nil
	}
	out = append(out, dj)
}
```

and `s.observeListExpansion("docs", "body")` when `f.Body` — add the instrument
now, in `internal/api/metrics.go`, since task 3 reuses it:

```go
// in initMetrics, alongside the other worklode_* counters:
s.listExpansions = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "worklode_list_expansions_total",
	Help: "List endpoint requests that asked for an expansion, by endpoint (tasks, docs) and expansion (detail, body).",
}, []string{"endpoint", "expansion"})

// observeListExpansion records one expanded list request. Nil-safe: tests
// build a *server directly without initMetrics.
func (s *server) observeListExpansion(endpoint, expansion string) {
	if s.listExpansions == nil {
		return
	}
	s.listExpansions.WithLabelValues(endpoint, expansion).Inc()
}
```

Also update `listDocs`'s doc comment to name the new param, matching how
`listTasks:297` documents its own.

Tests in `internal/api/docs_test.go` (Postgres required — follow the package's
existing server helper and the way other doc tests seed via the sync path):

```go
func TestListDocsBodyExpansion(t *testing.T) {
	// Seed one doc with a non-empty body and frontmatter through the
	// existing sync path used by the other tests in this file.

	// Default: catalogue only.
	rr := doReq(t, h, "GET", "/api/v1/docs?project=proj", token, nil)
	// assert 200; docs[0].body == "" and no "frontmatter" key in the row

	// Expanded: body and frontmatter present and equal to what GetDoc returns.
	rr = doReq(t, h, "GET", "/api/v1/docs?project=proj&body=true", token, nil)
	// assert 200; docs[0].body == the seeded body verbatim;
	// docs[0].frontmatter deep-equals the seeded frontmatter

	// Anything that is not exactly "true" means false.
	rr = doReq(t, h, "GET", "/api/v1/docs?project=proj&body=1", token, nil)
	// assert docs[0].body == ""
}
```

Add a store-level `TestListDocsBody` in `internal/store/docs_test.go`: upsert
two docs, assert `ListDocs(ctx, DocFilter{})` returns `Body == ""` for both and
`ListDocs(ctx, DocFilter{Body: true})` returns each body verbatim, with row
order unchanged between the two calls.

- [ ] Write `TestListDocsBody` (store) and `TestListDocsBodyExpansion` (api); watch them fail
- [ ] Add `DocFilter.Body` and the conditional column/scan in `ListDocs`
- [ ] Add the metric + `observeListExpansion`; wire `listDocs`
- [ ] `go test ./internal/store -run TestListDocs -count=1` → `ok` (not `SKIP`)
- [ ] `go test ./internal/api -run TestListDocs -count=1` → `ok`
- [ ] Commit: e.g. `Return doc bodies from the list endpoint on request`

### Task 2 — Bulk edge reader `ListEdgesForTasks`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

`ListEdges(ctx, taskID)` is single-task. Task 3 needs every edge touching a set
of tasks in one query. Add it next to `ListEdges` in `internal/store/tasks.go`,
following the shape `BlockedTaskIDs` already established for an
unscoped-then-looked-up bulk read:

```go
// TaskEdges is one task's edges, in the same split ListEdges returns.
type TaskEdges struct {
	Out []Edge
	In  []Edge
}

// ListEdgesForTasks returns the edges touching each of ids, keyed by task id,
// in one query. The bulk form of ListEdges: a list endpoint that reported
// edges by calling ListEdges per row would issue one query per task. Tasks
// with no edges are absent from the map; ids is empty-safe.
//
// Ordering within each slice matches ListEdges (from_task, to_task, type) so
// callers see the same sequence whichever reader they used.
func (s *Store) ListEdgesForTasks(ctx context.Context, ids []string) (map[string]TaskEdges, error)
```

Implement with a single `SELECT from_task, to_task, type FROM task_edges WHERE
from_task = ANY($1) OR to_task = ANY($1) ORDER BY from_task, to_task, type`.
Bind `ids` as a plain `[]string` — pgx handles the array conversion, and
`internal/store/hierarchy.go:95` is the existing precedent for exactly this
shape (`WHERE to_task = ANY($1) AND type = 'child_of'`). No `pq.Array` wrapper;
this repo does not import `lib/pq`. Append each row to the `Out` of its
`from_task` and the `In` of its
`to_task`, both only when that id is in the requested set — an edge to a task
outside the set contributes to the in-set end only. Return an empty (non-nil)
map for empty `ids` without hitting the database.

Test in `internal/store/tasks_test.go` (Postgres required):

```go
func TestListEdgesForTasks(t *testing.T) {
	// Seed WL-1 blocks WL-2; WL-3 child_of WL-1; WL-4 blocks WL-5
	// (WL-5 deliberately left out of the requested set).
	m, err := s.ListEdgesForTasks(ctx, []string{"WL-1", "WL-2", "WL-3", "WL-4"})
	// assert: WL-1 has Out=[blocks WL-2], In=[child_of from WL-3]
	// assert: WL-2 has In=[blocks from WL-1], no Out
	// assert: WL-4 has Out=[blocks WL-5] — the far end is out of set, the
	//         near end still reports it
	// assert: WL-5 is absent from the map entirely
	// assert: a task with no edges at all is absent, not an empty entry
}

func TestListEdgesForTasksEmpty(t *testing.T) {
	m, err := s.ListEdgesForTasks(ctx, nil)
	// assert: err == nil, len(m) == 0
}
```

Add a third assertion comparing `ListEdgesForTasks(ctx, []string{"WL-1"})["WL-1"]`
against `ListEdges(ctx, "WL-1")` element-for-element — the two readers must not
drift.

- [ ] Write the three tests; watch them fail to compile
- [ ] Implement `TaskEdges` and `ListEdgesForTasks`
- [ ] `go test ./internal/store -run TestListEdgesForTasks -count=1` → `ok` (not `SKIP`)
- [ ] Commit: e.g. `Add bulk edge reader for task lists`

### Task 3 — `GET /api/v1/tasks?detail=true` adds blocked and edges

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

In `internal/api/tasks.go`, next to `taskDetailJSON`, add the expanded list row.
It deliberately carries fewer fields than `taskDetailJSON` — see the Decision
section, and say so in the comment:

```go
// taskListDetailJSON is one row of GET /api/v1/tasks?detail=true: the base
// task plus the two field groups a list consumer cannot cheaply reconstruct.
// It is not taskDetailJSON: hierarchy is derivable from the child_of edges
// below, and lease is per-task ephemeral state that a cached list would
// misreport. Both stay on GET /api/v1/tasks/{id}.
type taskListDetailJSON struct {
	taskJSON
	Blocked bool `json:"blocked"`
	Edges   struct {
		Out []edgeOut `json:"out"`
		In  []edgeIn  `json:"in"`
	} `json:"edges"`
}
```

In `listTasks`, after the existing `s.st.ListTasks` call, branch on the param.
Keep the unexpanded path byte-identical to today's — it is the one every
existing client uses:

```go
if q.Get("detail") != "true" {
	// ... the existing resp/loop/writeJSON, unchanged
	return
}
s.observeListExpansion("tasks", "detail")

ids := make([]string, 0, len(tasks))
for i := range tasks {
	ids = append(ids, tasks[i].ID)
}
blocked, err := s.st.BlockedTaskIDs(r.Context())
if err != nil {
	s.mapStoreErr(w, err)
	return
}
edges, err := s.st.ListEdgesForTasks(r.Context(), ids)
if err != nil {
	s.mapStoreErr(w, err)
	return
}
resp := struct {
	Tasks []taskListDetailJSON `json:"tasks"`
}{Tasks: make([]taskListDetailJSON, 0, len(tasks))}
for i := range tasks {
	row := taskListDetailJSON{taskJSON: toTaskJSON(&tasks[i]), Blocked: blocked[tasks[i].ID]}
	te := edges[tasks[i].ID]
	row.Edges.Out = make([]edgeOut, 0, len(te.Out))
	for _, e := range te.Out {
		row.Edges.Out = append(row.Edges.Out, edgeOut{To: e.ToTask, Type: e.Type})
	}
	row.Edges.In = make([]edgeIn, 0, len(te.In))
	for _, e := range te.In {
		row.Edges.In = append(row.Edges.In, edgeIn{From: e.FromTask, Type: e.Type})
	}
	resp.Tasks = append(resp.Tasks, row)
}
writeJSON(w, http.StatusOK, resp)
```

`edges` and `out`/`in` must always be present arrays, never `null` — the
`make(..., 0, n)` calls above are load-bearing for the TypeScript client, which
treats them as required.

Update `listTasks`'s doc comment to name `detail` alongside the other params.

**`internal/cli/client.go` is deliberately untouched, in this task and in task
1.** No Go caller wants either expansion: the consumer is the TypeScript plugin,
which speaks HTTP directly. Adding `TaskListFilter.Detail`, a `TaskListDetail`
type, and a widened `ListDocs` signature would be four pieces of dead code and,
in the docs case, a breaking change to a positional signature
(`ListDocs(ctx, project, kind, status string)`, `client.go:1455`) for no
caller's benefit. When a Go caller does appear, `detail` is a one-field addition
to `TaskListFilter` and `body` is the argument that turns `ListDocs` into a
`DocListFilter` struct — both cheap then, both speculative now.

Tests in `internal/api/tasks_test.go` (Postgres required):

```go
func TestListTasksDetailExpansion(t *testing.T) {
	// Seed: WL-1 blocks WL-2, WL-3 child_of WL-1, and leave WL-4 edgeless.

	rr := doReq(t, h, "GET", "/api/v1/tasks?project=proj", token, nil)
	// assert: no "blocked" and no "edges" key on any row (the unexpanded
	// path is unchanged)

	rr = doReq(t, h, "GET", "/api/v1/tasks?project=proj&detail=true", token, nil)
	// assert: WL-2 has blocked == true, edges.in == [{from: WL-1, type: blocks}]
	// assert: WL-1 has edges.out == [{to: WL-2, type: blocks}] and
	//         edges.in == [{from: WL-3, type: child_of}]
	// assert: WL-4 has edges.out == [] and edges.in == [] (arrays, not null)
	// assert: row order is identical to the unexpanded response
}
```

Add `TestListTasksDetailMatchesGetTask`: for one task, assert the expanded row's
`blocked` and `edges` deep-equal what `GET /api/v1/tasks/{id}` reports — the
regression guard against the two paths drifting. And extend the existing
metrics test to assert `worklode_list_expansions_total{endpoint="tasks",
expansion="detail"}` increments only on the expanded request.

- [ ] Write the three tests; watch them fail
- [ ] Add `taskListDetailJSON` and the `listTasks` branch
- [ ] Add `TaskListFilter.Detail`, `TaskListDetail`, `ListTasksDetail`
- [ ] `go test ./internal/api -run TestListTasks -count=1` → `ok`
- [ ] `go test ./... -count=1` → green
- [ ] Commit: e.g. `Expand task list rows with blocked and edges on request`

### Task 4 — `plugins/obsidian/` scaffold, build, test harness and CI

```yaml
kind: chore
priority: high
skills: [ ]
blockedBy: [ ]
```

First TypeScript in the repo. Create `plugins/obsidian/` under the existing
`plugins/` directory, beside `plugins/claude/` — it shares no code with the Go
build and must not be importable from it.

`plugins/obsidian/package.json`:

```json
{
  "name": "obsidian-worklode",
  "version": "0.1.0",
  "private": true,
  "description": "Mirror a Worklode instance into an Obsidian vault.",
  "type": "module",
  "scripts": {
    "build": "node esbuild.config.mjs production",
    "dev": "node esbuild.config.mjs",
    "test": "vitest run",
    "typecheck": "tsc --noEmit"
  },
  "dependencies": {
    "yaml": "^2.5.0"
  },
  "devDependencies": {
    "@types/node": "^22.0.0",
    "builtin-modules": "^4.0.0",
    "esbuild": "^0.24.0",
    "obsidian": "^1.7.2",
    "typescript": "^5.6.0",
    "vitest": "^2.1.0"
  }
}
```

`plugins/obsidian/manifest.json` — the id is what names the plugin folder inside a
vault, so it is fixed here and never changed:

```json
{
  "id": "worklode",
  "name": "Worklode",
  "version": "0.1.0",
  "minAppVersion": "1.5.0",
  "description": "Mirror a Worklode instance into this vault: a folder per project, a note per document and task.",
  "author": "Sunstone Institute",
  "authorUrl": "https://sunstone.institute",
  "isDesktopOnly": false
}
```

`plugins/obsidian/tsconfig.json`: `target`/`module` `ES2022`, `moduleResolution`
`bundler`, `strict: true`, `noImplicitOverride: true`, `noUnusedLocals: true`,
`lib: ["DOM", "ES2022"]`, `include: ["src", "test"]`.

`plugins/obsidian/esbuild.config.mjs`: the standard Obsidian sample-plugin config —
entry `src/main.ts`, `bundle: true`, `format: "cjs"`, `target: "es2022"`,
`outfile: "main.js"`, `external: ["obsidian", "electron", ...builtinModules]`,
sourcemap `inline` in dev and off in production, watch mode when the
`production` argument is absent.

`plugins/obsidian/.gitignore`: `node_modules/`, `main.js`, `*.js.map`. The build output
is not committed — this plugin is installed by building, not by downloading a
release, and a committed `main.js` would rot silently.

`plugins/obsidian/vitest.config.ts`: environment `node`, `include: ["test/**/*.test.ts"]`.

Prove the harness with one real assertion rather than a smoke test that cannot
fail — `test/scaffold.test.ts` reads `manifest.json` and asserts its `id` is
`worklode` and its `version` equals `package.json`'s, which is a genuine
invariant (they drift, and Obsidian silently ignores a plugin whose manifest
version does not match its release).

CI: new `.github/workflows/_obsidian.yml`, a reusable workflow mirroring
`_lint.yml`'s structure — `actions/checkout` (pinned by SHA, as every other
workflow in this repo does), `actions/setup-node` with `node-version: 22` and
`cache: npm` / `cache-dependency-path: plugins/obsidian/package-lock.json`, then
`npm ci`, `npm run typecheck`, `npm test`, `npm run build`, all with
`working-directory: plugins/obsidian`. Wire it into `.github/workflows/pr-checks.yml`
as a job named `obsidian` with `needs: gate` and
`if: needs.gate.outputs.run == 'true'`, alongside `lint` and `test`. No path
filter — a reusable workflow cannot take one, and the job is under a minute.
Commit `package-lock.json` (`npm ci` requires it).

- [ ] Create the scaffold files; `cd plugins/obsidian && npm install`
- [ ] Write `test/scaffold.test.ts`
- [ ] `npm --prefix plugins/obsidian run typecheck` → no errors
- [ ] `npm --prefix plugins/obsidian test` → 1 passed
- [ ] `npm --prefix plugins/obsidian run build` → `main.js` written
- [ ] Add `_obsidian.yml` and the `obsidian` job in `pr-checks.yml`
- [ ] Commit **including `package-lock.json`, excluding `main.js`**: e.g.
      `Add the Obsidian plugin scaffold and its CI job`

### Task 5 — Wire types and the read-only API client

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [4]
```

`plugins/obsidian/src/api/types.ts` mirrors the Go wire shapes — `Project`,
`RepoMapping`, `Task` (all fifteen `taskJSON` fields), `TaskListDetail`
(`Task` + `blocked` + `edges`), `TaskEdgeOut`/`TaskEdgeIn`, and `Doc` (the
`docJSON` fields, with `body`/`frontmatter` optional since an unexpanded list
omits them). Head the file with a comment naming
`internal/cli/client.go` as the source of truth and ADR 036 as the direction of
travel, so the next reader knows which side to change first.

`plugins/obsidian/src/api/client.ts` — no Obsidian import; the transport is injected so
tests need no network and no Obsidian:

```ts
/** The subset of Obsidian's requestUrl that this client needs. */
export interface HttpTransport {
	(req: { url: string; method: string; headers: Record<string, string> }): Promise<{
		status: number;
		text: string;
	}>;
}

export class WorklodeClient {
	constructor(
		private readonly baseUrl: string,
		private readonly token: string,
		private readonly http: HttpTransport,
	) {}

	listProjects(): Promise<Project[]>;
	/** GET /api/v1/tasks?project=&detail=true */
	listTasks(project: string): Promise<TaskListDetail[]>;
	/** GET /api/v1/docs?project=&body=true */
	listDocs(project: string): Promise<Doc[]>;
}
```

Every call sends `Authorization: Bearer <token>` and `Accept: application/json`.
A non-2xx status throws a `WorklodeApiError` carrying status, the request path,
and the response body — 401 in particular must be legible in the status bar,
because a pasted-token typo is the most likely failure and "sync failed" alone
would send the user hunting.

`baseUrl` is normalized once in the constructor by stripping trailing slashes,
so `https://lode.example.com/` and `https://lode.example.com` behave the same.

Tests, `plugins/obsidian/test/client.test.ts`, with a fake transport recording requests:

```ts
it("requests the expanded shapes and sends the bearer token", async () => {
	// assert listTasks("worklode") hits
	//   /api/v1/tasks?project=worklode&detail=true
	// assert listDocs("worklode") hits
	//   /api/v1/docs?project=worklode&body=true
	// assert both carry Authorization: Bearer wl_<...>
});

it("normalizes a base URL with a trailing slash", async () => { /* ... */ });

it("throws WorklodeApiError with the status and path on 401", async () => {
	// assert the message contains 401 and the path
});

it("parses a task row's edges", async () => {
	// feed a real captured response body; assert edges.out/.in map through
});
```

- [ ] Write `test/client.test.ts`; watch it fail to compile
- [ ] Write `src/api/types.ts` and `src/api/client.ts`
- [ ] `npm --prefix plugins/obsidian test` → all passing
- [ ] Commit: e.g. `Add the Worklode read API client for the Obsidian plugin`

### Task 6 — Task note serialization and the round-trip property

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [5]
```

`plugins/obsidian/src/serialize/note.ts` — pure, no Obsidian, no I/O. This task builds
the task half and the shared machinery; task 7 adds the other three note kinds.

```ts
/** A rendered note: where it goes, what it contains, and what it was made from. */
export interface Note {
	path: string;   // relative to the mount root
	content: string;
	etag: string;   // first 16 hex chars of sha256(canonical JSON of the source)
}

/** The reserved frontmatter block. Everything the backbone owns lives here. */
export interface WlBlock {
	type: "task" | "doc" | "project" | "index";
	serializer: 1;
	etag: string;
	/** True when the plugin added the note's `aliases` key. See the
	 *  round-trip rule: without this bit, a write-back cannot tell a
	 *  plugin-added alias from an author's own. */
	aliases_added: boolean;
	[key: string]: unknown;
}

export function taskToNote(t: TaskListDetail): Note;

/** Split a rendered note back into its wl block, the surrounding
 *  frontmatter, and the verbatim body. The inverse of the *ToNote
 *  functions; the write-back half is not implemented, but this is what
 *  makes round-trippability testable. */
export function parseNote(content: string): {
	wl: WlBlock;
	frontmatter: Record<string, unknown>;  // wl and plugin-added aliases removed
	body: string;
};
```

The rendered task note, exactly:

```markdown
---
aliases:
  - Fix the thing
wl:
  type: task
  serializer: 1
  aliases_added: true
  id: WL-42
  project: worklode
  title: Fix the thing
  state: ready
  kind: feature
  priority: medium
  concern: ""
  assignee: stig
  branch: WL-42-fix-the-thing
  blocked: false
  needs_decomposition: false
  skills: []
  created_by: stig
  created_at: 2026-08-01T10:00:00Z
  updated_at: 2026-08-14T12:00:00Z
  parent: "[[WL-7]]"
  children:
    - "[[WL-43]]"
  blocks:
    - "[[WL-44]]"
  blocked_by:
    - "[[WL-41]]"
  etag: 3f2a1c9d8e7b6a50
---
# Fix the thing

body markdown verbatim
```

Derivations, all from the single `TaskListDetail` row:

- `parent` — the `to` of the single `child_of` out-edge; the key is **omitted**
  when the task is a root, never written as `""` or `null`.
- `children` — the `from` of every `child_of` in-edge, sorted.
- `blocks` — the `to` of every `blocks` out-edge, sorted.
- `blocked_by` — the `from` of every `blocks` in-edge, sorted.
- Any other edge type is ignored rather than guessed at; a new backbone edge
  type must be added here deliberately.
- Every relation value is wrapped as `[[<id>]]`. Sorting is by
  `Intl.Collator`-free plain `<` on the id string — deterministic output is
  what makes the etag stable, so this must not depend on locale.

Serialize the frontmatter with the `yaml` package (`stringify`) rather than by
hand: quoting rules for values like `"[[WL-7]]"` and titles containing `:` are
exactly the kind of thing a hand-rolled emitter gets wrong.

Tests, `plugins/obsidian/test/note.test.ts`:

```ts
it("renders a task note with relations as wikilinks", () => {
	// full fixture in, exact string out — assert against the block above
});

it("omits parent for a root task and renders empty relation lists", () => {
	// assert "parent" is absent from the yaml, and children/blocks/
	// blocked_by are `[]`, not missing
});

it("writes the body verbatim", () => {
	// a body containing --- , tabs, trailing whitespace and a code fence
	// survives byte-for-byte
});

it("round-trips: parseNote(taskToNote(t)) recovers the wl block and body", () => {
	// assert deep equality of the wl block and exact equality of the body
});

it("changes the etag when any backbone field changes, and not otherwise", () => {
	// same task twice -> same etag; one field changed -> different etag
});
```

The round-trip and etag-stability tests are the two that matter; the rest are
regression fences around them.

- [ ] Write `test/note.test.ts`; watch it fail to compile
- [ ] Implement `Note`, `WlBlock`, `taskToNote`, `parseNote`, and the etag helper
- [ ] `npm --prefix plugins/obsidian test` → all passing
- [ ] Commit: e.g. `Serialize Worklode tasks as Obsidian notes`

### Task 7 — Doc, project and index note serialization

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [6]
```

Still `plugins/obsidian/src/serialize/note.ts`:

```ts
export function docToNote(d: Doc): Note;
export function projectToNote(p: Project, docs: Doc[], tasks: TaskListDetail[]): Note;
export function indexToNote(projects: Project[], rootName: string): Note;
```

**Doc notes** are the case the serialization contract exists for. The doc's own
frontmatter (the backbone's `frontmatter` field, already a JSON object) is
written at the top level verbatim — same keys, same values, same nesting — and
the `wl` block sits beside it:

```markdown
---
status: draft
covers: docs/specs/025-documents-in-the-backbone.md
aliases:
  - Documents in the backbone
wl:
  type: doc
  serializer: 1
  aliases_added: true
  id: WL-SPEC-025
  project: worklode
  kind: spec
  ordinal: "025"
  status: draft
  title: Documents in the backbone
  version: 3
  source_branch: main
  source_dirty: false
  synced_at: 2026-08-16T09:12:00Z
  etag: a91b3c5d7e9f0246
---
# Documents in the backbone

body markdown verbatim
```

Three rules the tests pin:

- `aliases` is added **only** when the doc's own frontmatter has no `aliases`
  key, and `wl.aliases_added` records which happened. If the doc has one, it is
  left exactly as it is — the document's frontmatter is never edited, only
  accompanied.
- A doc whose own frontmatter happens to contain a `wl` key is a collision the
  plugin must not silently resolve: keep the backbone block and record the
  conflict in the sync report so it surfaces, rather than dropping either.
- `ordinal` stays a string (`"025"`, not `25`). It is an identifier, and a YAML
  emitter that renders it as a number breaks the id.

**Project notes** (`<root>/<project>/<project>.md`) carry a `wl` block with
`type: project`, the project's id/name/key/repos, and counts; the body is a
generated summary — the project's docs as a wikilink list, then its tasks
grouped by state. Because the body is generated rather than backbone-owned,
the round-trip rule does not apply to it, and the note says so in one line:
`> Generated by the Worklode plugin. Edits here are overwritten on sync.`

**The index note** (`<root>/<root-basename>.md`) is the same idea one level up:
a `wl` block with `type: index` and the sync timestamp, and a body listing every
project as a wikilink with its doc and task counts.

Tests, extending `plugins/obsidian/test/note.test.ts`:

```ts
it("preserves the doc's own frontmatter verbatim", () => {
	// including a nested map, a list, and a key the plugin knows nothing about
});

it("does not add aliases when the doc already has them", () => {
	// assert the author's aliases survive unchanged and
	// wl.aliases_added === false; the sibling case asserts true
});

it("keeps ordinal a string", () => {
	// assert the yaml contains `ordinal: "025"`, not `ordinal: 025`
});

it("reports a wl key collision instead of dropping either", () => { /* ... */ });

it("round-trips a doc note back to its own frontmatter and body", () => {
	// parseNote(docToNote(d)).frontmatter deep-equals d.frontmatter
});

it("renders project and index notes with wikilinks to their members", () => {
	// assert [[WL-SPEC-025]] and [[WL-42]] appear in the project note body
});
```

- [ ] Write the new tests; watch them fail
- [ ] Implement `docToNote`, `projectToNote`, `indexToNote`
- [ ] `npm --prefix plugins/obsidian test` → all passing
- [ ] Commit: e.g. `Serialize Worklode docs, projects and the vault index`

### Task 8 — The mirror: desired set, diff, and apply

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [7]
```

`plugins/obsidian/src/sync/mirror.ts` — pure logic against an injected writer
interface, no Obsidian import:

```ts
/** The file operations the mirror needs. Implemented against Obsidian's
 *  vault in src/vault/writer.ts, and against a map in the tests. */
export interface VaultWriter {
	/** Every .md path under root, relative to root. */
	list(root: string): Promise<string[]>;
	read(root: string, path: string): Promise<string>;
	write(root: string, path: string, content: string): Promise<void>;
	remove(root: string, path: string): Promise<void>;
}

export interface MirrorStats {
	written: number;
	skipped: number;
	removed: number;
	conflicts: string[];  // paths where a wl key collision was found
}

/** Every note the backbone currently implies, in deterministic path order. */
export function desiredNotes(
	projects: Project[],
	byProject: Map<string, { docs: Doc[]; tasks: TaskListDetail[] }>,
	rootName: string,
): Note[];

/** Write what changed, delete what no longer belongs, leave the rest alone. */
export function applyMirror(
	writer: VaultWriter,
	root: string,
	desired: Note[],
): Promise<MirrorStats>;
```

`applyMirror`, in order:

1. `writer.list(root)` for the current file set.
2. For each desired note: if a file exists at that path, read it and extract
   `wl.etag` (via `parseNote`, tolerating a parse failure as "not a mirror
   file, rewrite it"); skip when the etags match, write otherwise.
3. Delete every existing `.md` path under root that is not in the desired set.
4. Return the counts.

`desiredPath` sanitizes ids before they become filenames: reject any id
containing `/`, `\`, or `..`, or that is empty, by skipping the object and
recording it in `conflicts` rather than writing outside the mount. This is the
one place a hostile or malformed backbone id could escape the mount root, so it
is tested directly.

Tests, `plugins/obsidian/test/mirror.test.ts`, against an in-memory `VaultWriter`
backed by a `Map<string, string>`:

```ts
it("writes every note on a first sync", () => { /* written === n, removed === 0 */ });

it("skips a file whose etag is unchanged", async () => {
	// sync twice with identical input -> second pass: written 0, skipped n
});

it("rewrites a file whose etag changed", async () => {
	// change one task's title -> exactly one written, the rest skipped
});

it("rewrites a file a user edited, discarding the edit", async () => {
	// the mount is machine-owned; this is the documented behaviour, not a bug
});

it("removes a note whose backbone object disappeared", async () => { /* ... */ });

it("never touches a path outside the mount root", async () => {
	// a project id of "../escape" and a task id of "a/b" are skipped and
	// reported in conflicts; assert the writer saw no path outside root
});
```

- [ ] Write `test/mirror.test.ts`; watch it fail to compile
- [ ] Implement `desiredNotes`, `desiredPath`, `applyMirror`
- [ ] `npm --prefix plugins/obsidian test` → all passing
- [ ] Commit: e.g. `Add the vault mirror diff and apply pass`

### Task 9 — The Obsidian shell: settings, commands, status bar

```yaml
kind: feature
priority: high
skills: [ ]
blockedBy: [8]
```

The only two files that import `obsidian`.

`plugins/obsidian/src/vault/writer.ts` — `ObsidianVaultWriter implements VaultWriter`
over `app.vault.adapter` (`list`, `read`, `write`, `remove`, `mkdir`). Create
parent folders before writing, and after deletions remove folders left empty
under the root so a deleted project does not leave a husk. Use the adapter
rather than `app.vault.create`/`modify` — the adapter's path-based API is the
one that maps cleanly onto `VaultWriter` and does not require a `TFile` handle
for a file the plugin is about to overwrite.

`plugins/obsidian/src/main.ts`:

```ts
interface WorklodeSettings {
	baseUrl: string;      // "" until configured
	token: string;        // wl_<40 hex>
	mountRoot: string;    // default "Worklode"
	projects: string;     // comma-separated allow-list; "" means all
	syncOnStartup: boolean;   // default false
	intervalMinutes: number;  // 0 = manual only; default 0
}
```

- `onload`: load settings, register the settings tab, register the commands,
  add the status-bar item, and — only when `syncOnStartup` — kick one sync.
  Register any interval through `this.registerInterval(window.setInterval(...))`
  so Obsidian clears it on unload.
- Commands: `Worklode: Sync now` and `Worklode: Purge the Worklode folder`
  (the latter asks for confirmation through a `Modal`, since it deletes).
- The sync path: build a `WorklodeClient` with `requestUrl` adapted to
  `HttpTransport` (`requestUrl` bypasses CORS, which a plain `fetch` from the
  renderer does not), fetch projects, filter by the allow-list, fetch tasks and
  docs per project, `desiredNotes`, `applyMirror`, then report.
- Status bar shows `Worklode: n notes · HH:MM` on success and
  `Worklode: sync failed` on error; every sync also fires a `Notice` with the
  counts, or with the `WorklodeApiError` message on failure. A run that
  returned conflicts says so explicitly — a silent partial sync is the one
  outcome that would make the mirror untrustworthy.
- Refuse to sync with an empty `baseUrl` or `token`, with a `Notice` naming
  which is missing, rather than issuing a request that will 401.
- Guard against overlapping runs with a simple in-flight boolean; a slow sync
  plus a short interval must not stack.

No tests here — this is the thin Obsidian-bound shell, and the logic it drives
is covered by tasks 5–8. Verification is task 10's manual pass.

- [ ] Implement `src/vault/writer.ts`
- [ ] Implement `src/main.ts` and the settings tab
- [ ] `npm --prefix plugins/obsidian run typecheck` → no errors
- [ ] `npm --prefix plugins/obsidian test` → still all passing
- [ ] `npm --prefix plugins/obsidian run build` → `main.js` written
- [ ] Commit: e.g. `Add the Obsidian plugin shell, settings and sync command`

### Task 10 — Install docs, manual verification, and the spec follow-up

```yaml
kind: chore
priority: medium
skills:
  - filing-follow-ups
blockedBy: [9]
```

`plugins/obsidian/README.md`, short and precise — no debugging diary:

- What it does and the one-sentence machine-owned-mount rule.
- Build and install: `npm ci && npm run build`, then
  `mkdir -p "$VAULT/.obsidian/plugins/worklode" && cp manifest.json main.js "$VAULT/.obsidian/plugins/worklode/"`,
  then enable it in Obsidian's Community Plugins pane (it needs Restricted Mode
  off). Say that `main.js` is not committed, so a fresh clone must build.
- Settings: what each of the six does, with the token minted by
  `lode actor token create --actor <id>`.
- Two limits, stated plainly: the mirror is read-only and rewrites anything
  under the mount folder, and the token sits in the vault's
  `.obsidian/plugins/worklode/data.json` in plaintext.

Add a `## The Obsidian mirror` paragraph to the root `CLAUDE.md` Architecture
section — three sentences: where it lives, that it is a read-only client of the
public API, and that `internal/cli/client.go` is the source of truth its wire
types mirror.

Manual verification against a local stack (`docker compose up -d` per
CLAUDE.md), recorded in the commit message:

- sync a vault, confirm the folder layout matches the Global Constraints;
- open graph view and confirm task-to-task edges are drawn from the frontmatter
  wikilinks — this is the thing the whole concept rests on, so if it does not
  render, stop and report rather than working around it;
- edit a mirrored note, sync, confirm the edit is discarded;
- close a task in the backbone, sync, confirm the note updates and nothing else
  is rewritten (`skipped` should be everything else).

Follow-ups — use the `filing-follow-ups` skill, and check `docs/follow-ups.md`
first so these are not filed twice:

- `GET /api/v1/tasks?detail=true` and `GET /api/v1/docs?body=true` are
  implemented but not written into their specs' API sections (004 §6.7, 025
  §16). Fold them in the next time either spec is amended.
- The plugin's wire types in `plugins/obsidian/src/api/types.ts` duplicate
  `internal/cli/client.go` by hand. ADR 036 makes `internal/model` the one
  declaration of every shape crossing the HTTP boundary; generating the
  TypeScript from it is the eventual fix.
- Write-back (vault → backbone) is unimplemented. `parseNote` and the
  serialization contract exist so it is mechanical, not speculative.
- `/api/v1/events/stream` is admin-only, so the mirror polls. Incremental sync
  for a non-admin client needs either an `event.read`-scoped stream or an
  `updated_at` filter on the list endpoints.

- [ ] Write `plugins/obsidian/README.md` and the `CLAUDE.md` paragraph
- [ ] Run the four manual checks against a local stack
- [ ] File the four follow-ups via the skill
- [ ] `go test ./... -count=1` and `npm --prefix plugins/obsidian test` → both green
- [ ] Commit: e.g. `Document the Obsidian mirror and record its known gaps`
