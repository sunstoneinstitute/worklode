---
status: accepted
covers:
  - docs/specs/025-documents-in-the-backbone.md#sec-2
  - docs/specs/025-documents-in-the-backbone.md#sec-3
requires:
  - 2026-08-03-documents-in-the-backbone-1-kinds-and-containers.md
---
# Documents in the backbone 2/4: the document store

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 2 of 4 (6 tasks; numbering restarts at 1 per part). See
part 1 for the series map. Part 1 must be merged first (the `docs.status`
CHECK is generated from part 1's `ns.DesignDocStatuses`).

**Goal:** Implement 025 §2 and §3: documents become Postgres rows wrapped in
the backbone's event-logged transaction machinery — `docs`, `doc_sections`,
`doc_edges`, and a nullable `tasks.plan_doc` — with the
draft → accepted → superseded lifecycle, assignee-gated acceptance, and 014
§7's anchor constraints enforced server-side at accept time.

**Architecture:** The authored artifact stays one markdown file: `lode doc
new` (part 3) submits body-with-frontmatter, and the server parses it with
`internal/designdoc` — frontmatter becomes columns and `doc_edges` rows,
headings become `doc_sections` rows, and the raw body is stored whole. One
edge row carries both directions (`amends` read backward is `amendedBy`), so
the mirror-agreement check of 014 §11 becomes an import-time concern rather
than a stored invariant that can drift. Revising an accepted spec is a
candidate body in `doc_revisions`; accepting it runs
`designdoc.CompareSections` — the §7 gate as a set diff — and the body swap,
version bump and `last_revised_in` stamps in one transaction. Plans store no
sections and skip the gate entirely (025 §4); accepting a plan is **rejected
in this part** and lands with minting in part 3, so the §5 invariant is never
half-true.

**Tech Stack:** Go 1.25+, Postgres via golang-migrate + `database/sql`,
`gopkg.in/yaml.v3` (already used by `designdoc`), Prometheus client.

**Spec:** `docs/specs/025-documents-in-the-backbone.md` §2–§3 (and 014 §3,
§5, §7 as amended, which §2 adopts wholesale)

**Read first:**
- 025 §2, §3; 014 §3, §5, §7 (the constraints the accept gate enforces)
- `internal/designdoc/designdoc.go` (`Parse`, `Document`, `Section` — the
  parser this part builds on), `internal/designdoc/frontmatter.go`
- `internal/store/events.go:34` (`RecordEvent` — every write goes through it)
- `internal/store/metrics.go` (the nil-safe metrics struct convention,
  spec 022)
- `internal/api/server.go` (route table, `readJSON`, `writeErr`),
  `internal/api/web.go` (unauthenticated read-only pages)

**Conventions:** as part 1. Migration number `0012` is provisional.

**Non-goals:** plan acceptance and task minting (part 3); the `lode doc` CLI
(part 3); graph projection of docs (025 §12 — 006 §6's contract, no projector
exists); crit integration internals (025 §12); corpus import (part 4);
`doc coverage` (needs `.worklode/implements.yaml` machinery that was never
built — see the deferred table in
`2026-07-30-design-documents-as-graph-objects.md`); the 014 §7.1
server-configurable depth limit (a `const DepthLimit = 3` here; the admin
setting is deferred with the 014 surfaces).

---

## Decisions the spec leaves open, taken here

Flagged for the spec's owner rather than buried:

- **Row identity.** 025 §2 gives documents no key. Specs/ADRs have a corpus
  number (014 §11.3); plans deliberately have none. `docs.id` is therefore a
  surrogate identity column, with `(project_id, kind, number)` unique where
  number exists and `(project_id, slug)` unique always; the CLI resolves
  026-style refs to ids.
- **Assignee gating.** 025 §3/AC5 say "assignee only" without defining a
  document's assignee. Here: `docs.assignee` (an actor id), defaulting to the
  creator; `AcceptDoc` rejects any other actor.
- **Supersession.** No verb exists in §10. Here: accepting a document whose
  doc-level `replaces` edges name other documents flips those targets to
  `superseded` in the same transaction (the file corpus's semantics, per
  `scripts/currentspec.py`). Section-level `replaces` stays derived — a
  replaced section is one an effective edge targets; no per-section status
  column.

## File structure

| File | Responsibility |
|---|---|
| `deploy/base/migrations/0012_docs.{up,down}.sql` (new) | the three tables + `tasks.plan_doc` |
| `internal/designdoc/diff.go` (+ test) (new) | `CompareSections` — 014 §7 as a set diff |
| `internal/store/docs.go` (+ `docs_test.go`) (new) | rows, lifecycle, sections, edges, revisions |
| `internal/store/metrics.go` | `worklode_doc_operations_total` |
| `internal/api/docs.go` (+ test) (new) | `/api/v1/docs` handlers |
| `internal/api/web.go`, `internal/api/templates/` | read-only `/docs` pages |
| `e2e/docs_test.go` (new) | the lifecycle through public surfaces |

---

## Tasks

### Task 1 — Add migration `0012_docs`

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Create: `deploy/base/migrations/0012_docs.up.sql`, `.down.sql`
- Modify: `deploy/base/kustomization.yaml`
- Test: `internal/store/docs_test.go` (schema smoke: insert/CHECK/uniques)

- [ ] **Step 1: Write the migration**

`0012_docs.up.sql`:

```sql
-- Documents in the backbone (docs/specs/025-documents-in-the-backbone.md §2).
-- The status CHECK mirrors ns.DesignDocStatuses (generated from ns/concept.ttl);
-- the kind CHECK mirrors the wl:Spec/wl:ADR/wl:Plan classes.

CREATE TABLE docs (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id  text NOT NULL REFERENCES projects(id),
    kind        text NOT NULL CHECK (kind IN ('spec','adr','plan')),
    number      integer,          -- corpus number; NULL for plans (014 §11.3)
    slug        text NOT NULL,
    title       text NOT NULL,
    body        text NOT NULL,    -- the full markdown, frontmatter included
    status      text NOT NULL DEFAULT 'draft'
                CHECK (status IN ('draft','accepted','superseded')),
    version     integer NOT NULL DEFAULT 1,
    issued      date,
    assignee    text REFERENCES actors(id),
    created_by  text REFERENCES actors(id),
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    CHECK (kind = 'plan' OR number IS NOT NULL)
);
CREATE UNIQUE INDEX docs_project_kind_number
    ON docs (project_id, kind, number) WHERE number IS NOT NULL;
CREATE UNIQUE INDEX docs_project_slug ON docs (project_id, slug);

-- Specs and ADRs only (025 §4: plans carry no sections and no anchors).
CREATE TABLE doc_sections (
    doc_id          bigint NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
    anchor          text NOT NULL,
    number          text,
    heading         text NOT NULL,
    depth           integer NOT NULL,
    position        integer NOT NULL,
    last_revised_in integer NOT NULL DEFAULT 1,   -- 014 §4.4
    published       boolean NOT NULL DEFAULT false, -- frozen from first accept (014 §7)
    PRIMARY KEY (doc_id, anchor)
);

-- One row carries both directions: amends read backward is amendedBy, so the
-- 014 §11 mirror cannot disagree by construction. to_external holds a
-- cross-corpus shorthand this backbone cannot resolve (014 §11.3).
CREATE TABLE doc_edges (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    from_doc    bigint NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
    from_anchor text,
    type        text NOT NULL CHECK (type IN
                ('implements','amends','replaces','requires','wasDerivedFrom','blocks')),
    to_doc      bigint REFERENCES docs(id),
    to_anchor   text,
    to_external text,
    CHECK ((to_doc IS NULL) <> (to_external IS NULL)),
    -- blocks orders whole plan documents (025 §2): never section-scoped.
    CHECK (type <> 'blocks' OR (from_anchor IS NULL AND to_anchor IS NULL))
);
CREATE UNIQUE INDEX doc_edges_unique ON doc_edges
    (from_doc, coalesce(from_anchor,''), type,
     coalesce(to_doc, 0), coalesce(to_anchor,''), coalesce(to_external,''));
CREATE INDEX doc_edges_to ON doc_edges (to_doc) WHERE to_doc IS NOT NULL;

-- One open candidate revision per doc (014 §5 as amended by 025 §3: the
-- candidate carries draft implicitly by being here).
CREATE TABLE doc_revisions (
    doc_id     bigint PRIMARY KEY REFERENCES docs(id) ON DELETE CASCADE,
    body       text NOT NULL,
    created_by text REFERENCES actors(id),
    created_at timestamptz NOT NULL
);

-- 025 §5: nullable by design — a task no plan authored carries none. The
-- plan task format's `skills` land in the existing tasks.skills jsonb
-- column (migration 0007), so no new column is needed for them.
ALTER TABLE tasks ADD COLUMN plan_doc bigint REFERENCES docs(id);
CREATE INDEX tasks_plan_doc ON tasks (plan_doc) WHERE plan_doc IS NOT NULL;
```

`.down.sql` drops in reverse order (`ALTER TABLE tasks DROP COLUMN plan_doc`
first). List both files in `deploy/base/kustomization.yaml`.

- [ ] **Step 2: Schema smoke test**

In a new `internal/store/docs_test.go`: insert a spec row, a plan row without
number (passes), a spec without number (CHECK violation), a duplicate
`(project, kind, number)` (unique violation), a `blocks` edge with an anchor
(CHECK violation).

- [ ] **Step 3: Verify and commit**

```bash
./scripts/check-migrations.sh --no-fix
go test ./internal/store/ -run 'TestMigrate|TestDocSchema' -count=1
```

---

### Task 2 — Implement `designdoc.CompareSections`, the §7 gate

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Create: `internal/designdoc/diff.go`, `internal/designdoc/diff_test.go`

The accept gate needs exactly what 014 §7 forbids, computed between the
accepted body and a candidate body. `internal/kg/section` from the unexecuted
014 plan was never built; this is that logic, homed in `designdoc` where the
parser already lives.

- [ ] **Step 1: Write the failing tests**

Table-driven over accepted/candidate source pairs:

| Case | Expectation |
|---|---|
| identical | empty diff, no violations |
| section deleted | `Removed = [sec-2.1]`, violation naming it (§7.1) |
| `## 2.` renumbered `## 3.` under the same anchor | `Renumbered`, violation (§7.3) |
| `## 1a.` letter-suffix insert | `Added = [sec-1a]`, **no** violation |
| body of §2 edited | `Changed = [sec-2]` only — §1 untouched (§7.5) |
| heading reworded, body identical | nothing changed, no violation (014 §3) |
| depth-4 anchored heading with limit 3 | violation naming the anchor (§7.6) |

- [ ] **Step 2: Implement**

```go
// SectionDiff compares an accepted document with a candidate revision
// (014 §7, enforced at accept time by the server per 025 §2). Removed,
// Renumbered and TooDeep are violations; Changed is the last_revised_in
// input; Added is informational.
type SectionDiff struct {
	Added, Removed, Renumbered, Changed, TooDeep []string
}

// DepthLimit is the 014 §7.1 addressability limit. Server-configurable is
// deferred with the rest of the 014 admin surface; 3 is its default.
const DepthLimit = 3

func CompareSections(accepted, candidate *Document, depthLimit int) SectionDiff
func (d SectionDiff) Violations() []string
```

Content comparison hashes each section's `Body` with whitespace trimmed, so a
heading reword never counts as a change. Anchored sections only; the
first occurrence wins on a duplicate anchor (a defect `Lint`-style checks own,
not this diff).

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/designdoc/ -run TestCompareSections -v
```

---

### Task 3 — Build `internal/store/docs.go`: rows, sections, edges

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:**
- Create: `internal/store/docs.go`
- Modify: `internal/store/metrics.go`
- Test: `internal/store/docs_test.go`

- [ ] **Step 1: Write the failing tests**

- `CreateDoc` with a spec body: row created `draft`, `doc_sections` mirror
  the parsed anchors with `position` in source order, frontmatter `requires`
  becomes `doc_edges` rows resolved against existing docs, an unresolvable
  reference lands in `to_external`.
- `CreateDoc` with a plan body: zero `doc_sections` rows.
- `UpdateDocBody` on a draft spec: sections rebuilt; on an **accepted** spec:
  `ErrInvalidInput` ("revise instead"); on an accepted **plan**: allowed —
  plans stay freely mutable (025 §4, AC6).
- `GetDoc`, `ListDocs(DocFilter{Kind, Status, Project})`.
- Metrics: `worklode_doc_operations_total{op="create",outcome="ok"}`
  increments (use a fresh registry as the store metrics tests do).

- [ ] **Step 2: Implement**

```go
// Doc is a backbone document row (025 §2): a spec, ADR, or plan.
type Doc struct {
	ID        int64
	ProjectID string
	Kind      string // spec | adr | plan
	Number    int    // 0 for plans
	Slug      string
	Title     string
	Body      string
	Status    string
	Version   int
	Issued    string // YYYY-MM-DD, "" when unset
	Assignee  string
	CreatedBy string
	CreatedAt, UpdatedAt time.Time
}

type DocInput struct {
	ProjectID, Kind, Slug, Body string
	Number                      int
	Assignee, CreatedBy         string
	// Status is honoured only by the corpus importer (part 4); the API's
	// create path always writes draft.
	Status string
}

func CreateDoc(tx *sql.Tx, now time.Time, in DocInput) (*Doc, error)
func UpdateDocBody(tx *sql.Tx, now time.Time, id int64, body string) error
func (s *Store) GetDoc(ctx context.Context, id int64) (*Doc, error)
func (s *Store) ListDocs(ctx context.Context, f DocFilter) ([]Doc, error)
```

Implementation notes that decide it:

- `CreateDoc` parses `in.Body` with `designdoc.Parse`. Title comes from the
  first H1 line (fall back to the slug); `issued` from frontmatter. A parse
  error or (for specs/ADRs) a `Lint`-grade anchor defect (duplicate anchor,
  anchor/number disagreement — reuse the heading fields `Parse` already
  yields) is `ErrInvalidInput` naming the line.
- One unexported `rebuildSections(tx, docID, doc *designdoc.Document, version int)`
  deletes and re-inserts the section rows, preserving `last_revised_in` and
  `published` for anchors that survive (keyed by anchor). Plans skip it.
- One unexported `rebuildEdges(tx, docID, fm *designdoc.Frontmatter)` maps the
  acting-direction keys (`implements`, `requires`, `amends`, `replaces`,
  `wasDerivedFrom`) to rows; inverse keys (`isRequiredBy`, `amendedBy`,
  `isReplacedBy`) are ignored on write — the row read backward is the
  inverse. Targets resolve by filename-style reference against
  `(project, slug)`/`(project, kind, number)`; misses store the raw string in
  `to_external`.
- Every mutation goes through `RecordEvent` at the caller (API layer), so
  these take `tx` like `CreateTask` does.
- Metrics: extend `storeMetrics` with
  `docOps *prometheus.CounterVec` (`worklode_doc_operations_total`,
  labels `op` ∈ create/update/accept/revise, `outcome` ∈ ok/error), nil-safe
  methods, registered in `newStoreMetrics`. Recorded by the `Store`-level
  wrappers the API calls.

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/store/ -run TestDoc -count=1 -v
```

---

### Task 4 — Implement the editorial lifecycle: accept, revise, supersede

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2, 3]
```

**Files:**
- Modify: `internal/store/docs.go`
- Test: `internal/store/docs_test.go`

- [ ] **Step 1: Write the failing tests**

| Case | Expectation |
|---|---|
| accept a draft spec by its assignee | status `accepted`, every section `published`, event logged |
| accept by a different actor | `ErrForbidden` naming the assignee (add the sentinel to `internal/store/errors.go` if absent) |
| accept an already-accepted doc | `ErrInvalidInput` |
| accept a **plan** | `ErrInvalidInput` "plan acceptance mints its tasks; lands in part 3" — the stub part 3 replaces |
| accept a doc whose doc-level `replaces` edge names doc X | X flips to `superseded` in the same transaction |
| `ReviseDoc` on an accepted spec | candidate row created; a second open revision is rejected |
| `AcceptRevision` with a candidate that deletes a published anchor | rejected, violation text from `SectionDiff.Violations` |
| `AcceptRevision` with a letter-suffix insert and one edited body | body swapped, `version` = 2, `last_revised_in` = 2 on exactly the changed anchor, candidate row gone |
| `ReviseDoc` on a plan | `ErrInvalidInput` — plans are edited in place (`UpdateDocBody`), never revised |

- [ ] **Step 2: Implement**

```go
// AcceptDoc is the manual commit of 025 §3: draft -> accepted, gated on the
// assignee (default: the creator). On a spec or ADR it freezes the published
// anchor set; doc-level replaces edges flip their targets to superseded.
// Plans are rejected here until plan acceptance (part 3) supplies the
// minting half — accepted-without-tasks must never exist (025 §5).
func AcceptDoc(tx *sql.Tx, now time.Time, id int64, actorID string, eventID int64) (*Doc, error)

// ReviseDoc opens a candidate revision against an accepted spec/ADR: a copy
// of the current body to edit. The accepted version stays authoritative
// throughout (014 §5).
func ReviseDoc(tx *sql.Tx, now time.Time, id int64, actorID string) error
func UpdateRevision(tx *sql.Tx, now time.Time, id int64, body string) error

// AcceptRevision runs the 014 §7 gate (designdoc.CompareSections) and, when
// clean, swaps the body, bumps version, stamps last_revised_in on exactly
// the changed anchors, marks new anchors published, and applies any new
// doc-level replaces edges — one transaction, assignee-gated like AcceptDoc.
func AcceptRevision(tx *sql.Tx, now time.Time, id int64, actorID string, eventID int64) (*Doc, error)
```

The anchor-permanence invariant also binds `AcceptRevision`'s section
rebuild: `rebuildSections` must never drop a `published` row — enforce with a
post-rebuild count check that returns an internal error if the diff gate ever
lets one through.

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/store/ -run 'TestDocAccept|TestDocRevi' -count=1 -v
```

---

### Task 5 — Serve the API and web read surface

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [4]
```

**Files:**
- Create: `internal/api/docs.go`, `internal/api/docs_test.go`,
  `internal/api/templates/docs.html`, `internal/api/templates/doc.html`
- Modify: `internal/api/server.go` (routes), `internal/api/web.go`

- [ ] **Step 1: Write the failing tests**

Handler tests in the style of `tasks_test.go` (bearer token, `doReq`):

| Route | Behaviour |
|---|---|
| `POST /api/v1/docs` | body `{project, kind, slug, number?, body, assignee?}` → 201 with the doc JSON; parse defects → 422 naming the line; `status` in the request → 422 (import-only, part 4 adds the gate) |
| `GET /api/v1/docs?project=&kind=&status=` | filtered list |
| `GET /api/v1/docs/{id}` | doc + sections + edges (both directions, the inverse labelled `amendedBy`/`isReplacedBy`/`isRequiredBy`) |
| `PUT /api/v1/docs/{id}/body` | draft spec / any-status plan → 200; accepted spec → 422 pointing at revise |
| `POST /api/v1/docs/{id}/accept` | assignee → 200; other actor → 403; plan → 422 (part 3 lifts) |
| `POST /api/v1/docs/{id}/revise`, `PUT /api/v1/docs/{id}/revision`, `POST /api/v1/docs/{id}/revision/accept` | the Task 4 lifecycle over HTTP; a violating revision-accept → 422 listing the violations |

Web: `GET /docs` lists documents; `GET /docs/{id}` renders title, status
badge, version, and the body (same markdown rendering path the task pages
use). Unauthenticated read-only, matching the existing web posture.

- [ ] **Step 2: Implement**

Each handler wraps its store call in `RecordEvent`
(`doc.created` / `doc.updated` / `doc.accepted` / `doc.revised` /
`doc.revision_accepted`) with a random external id, mirroring `createTask`.
`actorFrom(r)` supplies the acceptor. Register routes beside the task routes
in `server.go`. HTTP metrics ride the existing middleware; the domain
counters landed in Task 3.

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/api/ -count=1
```

---

### Task 6 — Prove the spec lifecycle end to end

```yaml
kind: chore
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [5]
```

**Files:**
- Create: `e2e/docs_test.go`

- [ ] **Step 1: Write the test**

Through `cli.Client`-level HTTP only (add thin client methods as needed —
they are part 3's CLI plumbing either way): create a project and two actors;
actor A creates a spec draft with two anchored sections and
`assignee: actor-a`; actor B's accept fails (403); A accepts; a revision that
renumbers a section is rejected with the violation text; a revision adding
`sec-1a` and editing one body is accepted; `GET` shows version 2 and
`last_revised_in` moved on exactly one anchor. Then the plan half: create a
plan doc, edit its body while draft, and assert accepting it returns the 422
stub (lifted in part 3).

- [ ] **Step 2: Verify and commit**

```bash
go test -race -count=1 -tags e2e ./e2e/ -run TestDocLifecycle
```

---

## Done when (maps to 025 §13)

1. AC5 (spec half): a document under review is a `draft` row plus whatever
   review task points at it — `proposed` exists nowhere; `lode doc accept`'s
   store half is manual and assignee-gated.
2. AC6: plan rows take body edits at any status and hold zero sections; spec
   rows enforce 014 §7 at accept time, proven by the rejected-renumber e2e
   step.
3. `worklode_doc_operations_total` visible on `/metrics` with tests, per
   spec 022's conventions.
