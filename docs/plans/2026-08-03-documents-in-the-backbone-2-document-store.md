---
status: accepted
covers:
  - docs/specs/025-documents-in-the-backbone.md#sec-5
  - docs/specs/025-documents-in-the-backbone.md#sec-6
  - docs/specs/025-documents-in-the-backbone.md#sec-7
requires:
  - 2026-08-03-documents-in-the-backbone-1-kinds-and-containers.md
---
# Documents in the backbone 2/4: the document store

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 2 of 4 (6 tasks; numbering restarts at 1 per part). See
part 1 for the series map. Part 1 has landed apart from its Task 1 (`ns/`
codegen and `internal/ns`, filed as WL-70 and still unexecuted), so
`internal/ns` does not exist: write the `docs.status` and `docs.kind` CHECKs
literally, with a comment naming the generated constant that replaces them
once WL-70 lands. `internal/designdoc/plantasks.go` already makes the same
accommodation for `planMintableKinds`.

**Goal:** Implement 025 §5, §6 and §7: documents become Postgres rows wrapped
in the backbone's event-logged transaction machinery — `docs`, `doc_sections`,
`doc_edges`, and a nullable `tasks.plan_doc` — with the
draft → accepted → superseded lifecycle, assignee-gated acceptance, and §6's
anchor constraints enforced server-side at accept time.

**This part replaces the sync on-ramp's tables.** Migrations `0011_docs` and
`0012_doc_edges_covers` created `docs`, `doc_sections` and `doc_edges` for
025 §16's git→backbone sync — a different subsystem that took the same three
table names. That sync is retired (§16 superseded, its Go surface deleted) and
the tables are empty in every deployment, so Task 1 **drops** them and creates
this part's schema in their place. There is no second owner to reconcile with
and no data to preserve.

Why the authoring schema is the one that survives: 025 §9.2 requires
`tasks.plan_doc REFERENCES docs(id)`. Against this part's surrogate key that
is one `bigint` column per task row; against the on-ramp's composite
`(project, kind, ordinal)` primary key it is a three-column foreign key on
every task row, and the same again for `doc_edges.to_doc`. The on-ramp's
provenance columns (`source_branch`, `source_dirty`, `synced_at`, all NOT
NULL) also have no meaning for a document authored in the backbone rather than
projected from a file.

**Architecture:** The authored artifact stays one markdown file: `lode doc
new` (part 3) submits body-with-frontmatter, and the server parses it with
`internal/designdoc` — frontmatter becomes columns and `doc_edges` rows,
headings become `doc_sections` rows, and the raw body is stored whole. One
edge row carries both directions (`amends` read backward is `amendedBy`), so
the mirror-agreement check of 025 §14 becomes an import-time concern rather
than a stored invariant that can drift. Revising an accepted spec is a
candidate body in `doc_revisions`; accepting it runs
`designdoc.CompareSections` — the §6 gate as a set diff — and the body swap,
version bump and `last_revised_in` stamps in one transaction. Plans store no
sections and skip the gate entirely (025 §9); accepting a plan is **rejected
in this part** and lands with minting in part 3, so the §5 invariant is never
half-true.

**Tech Stack:** Go 1.25+, Postgres via golang-migrate + `database/sql`,
`gopkg.in/yaml.v3` (already used by `designdoc`), Prometheus client.

**Spec:** `docs/specs/025-documents-in-the-backbone.md` §3, §5, §6, §7

**Read first:**
- 025 §3 (anchors and the letter-suffix convention), §5 (the store), §6 (the
  constraints the accept gate enforces), §7 (the editorial lifecycle)
- `internal/designdoc/designdoc.go` (`Parse`, `Document`, `Section` — the
  parser this part builds on), `internal/designdoc/frontmatter.go`
  (`Frontmatter`, `CoverageEntries` — `covers` with `implements` as its
  retired spelling, 026 §5.1)
- `internal/store/events.go` (`(*Store).RecordEvent` — every write goes
  through it; it is a `Store` method, not a package function)
- `internal/store/metrics.go` (the nil-safe metrics struct convention,
  spec 022)
- `internal/api/router.go` (`routeGuards` — a route the table does not name
  panics at boot; `permDocRead`/`permDocWrite` already exist in
  `internal/api/authz.go`), `internal/api/server.go` (`readJSON`, `writeErr`,
  `actorFrom`), `internal/api/web.go` + `internal/ui/*.templ` (the cockpit's
  pages: templ components, session-gated when a login provider is configured
  — there is no `internal/api/templates/` directory and no unauthenticated
  page surface)

**Conventions:** as part 1. The migration is `0022_docs` — the head is
`0021_event_log`, and `0011`/`0012` are the tables this one drops. Confirm
with `./scripts/check-migrations.sh --no-fix` before committing and take the
number it settles on; both files must be listed in
`deploy/base/kustomization.yaml`.

**Interaction with ADR 036 (one model, not one per package):** 036 puts every
shape that crosses the HTTP boundary in `internal/model`. That package does
not exist on `main` — its plan is unexecuted (branch
`WL-48-execute-plan-one-model-across-packages-a`) — so the type sketches below
declare `Doc` and friends in `internal/store` as the rest of the tree still
does. If `internal/model` exists when this part runs, declare them there
instead and let `store`/`api`/`cli` share the one declaration; do not create a
`Doc`/`docJSON`/`cli.Doc` triplicate, which 036 names as the work to undo
rather than the pattern to copy.

**Non-goals:** plan acceptance and task minting (part 3); the `lode doc` CLI
(part 3); graph projection of docs (025 §22 — 006 §11's contract, no projector
exists); crit integration internals (025 §22); corpus import (part 4);
`doc coverage` (needs `.worklode/implements.yaml` machinery that was never
built — see the deferred table in
`2026-07-30-design-documents-as-graph-objects.md`); the 025 §6.1
server-configurable depth limit (a `const DepthLimit = 3` here; the admin
setting is deferred with the 014 surfaces).

---

## Decisions the spec leaves open, taken here

Flagged for the spec's owner rather than buried:

- **Row identity.** 025 §5 gives documents no key. Specs/ADRs have a corpus
  number (025 §14.3); plans deliberately have none. `docs.id` is therefore a
  surrogate identity column, with `(project_id, kind, number)` unique where
  number exists and `(project_id, slug)` unique always; the CLI resolves
  026-style refs to ids.
- **Assignee gating.** 025 §7/AC5 say "assignee only" without defining a
  document's assignee. Here: `docs.assignee` (an actor id), defaulting to the
  creator; `AcceptDoc` rejects any other actor.
- **Edge vocabulary.** 026 §6.2 split `covers` (a plan's promise about spec
  sections) from `implements` (a component's evidence about its code), and
  migration `0012_doc_edges_covers` widened the on-ramp's rel enum for it.
  This part's `doc_edges.type` therefore admits **both**: `covers` is what a
  plan's frontmatter emits, `implements` is retained for components and as
  `covers`'s retired spelling (`Frontmatter.CoverageEntries` already
  implements that precedence). Every query over plan coverage — part 3's
  `NeedsPlanning` included — reads `covers`.
- **Supersession.** No verb exists in §10. Here: accepting a document whose
  doc-level `replaces` edges name other documents flips those targets to
  `superseded` in the same transaction (the file corpus's semantics, per
  `scripts/currentspec.py`). Section-level `replaces` stays derived — a
  replaced section is one an effective edge targets; no per-section status
  column.

## File structure

| File | Responsibility |
|---|---|
| `deploy/base/migrations/0022_docs.{up,down}.sql` (new) | drop the on-ramp's tables; create the four + `tasks.plan_doc` |
| `deploy/base/kustomization.yaml` | list both migration files |
| `internal/designdoc/diff.go` (+ test) (new) | `CompareSections` — 025 §6 as a set diff |
| `internal/store/docs.go`, `docs_test.go` | rewritten: rows, lifecycle, sections, edges, revisions |
| `internal/store/metrics.go` | `worklode_doc_operations_total` replaces `worklode_doc_upserts_total` |
| `internal/api/docs.go`, `docs_test.go` | rewritten: `/api/v1/docs` handlers |
| `internal/api/router.go` | `routeGuards` entries for the new routes |
| `internal/api/web.go`, `internal/api/render.go`, `internal/ui/docs.templ` (new) | read-only `/docs` cockpit pages |
| `e2e/docs_test.go` (new) | the lifecycle through public surfaces |

**On the files marked "rewritten".** `internal/store/docs.go`,
`internal/api/docs.go`, `internal/cmd/doc.go` and `e2e/docsync_test.go` exist
today as the sync on-ramp's implementation, and the doc-sync retirement
removes the sync-only half of each (`lode doc sync`, `POST
/api/v1/docs/sync`, `store.ApplyDocSync`, `store.DocSyncOutcomes`, the sync
config keys and their tests). Whatever of those files still reads the dropped
tables — `store.GetDoc`/`ListDocs` over `(project, kind, ordinal)`, the
`GET /api/v1/docs` handlers, `lode doc list`, `store.Doc`/`DocSection`/
`DocEdge`/`DocFilter`/`validDocEdgeRels` — is replaced by this part's Task 3
and Task 5 equivalents and part 3's CLI verbs. Task 1 is not done until
nothing in the tree selects a dropped column. If the retirement has already
deleted a file outright, create it.

---

## Tasks

### Task 1 — Replace the sync tables with migration `0022_docs`

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Create: `deploy/base/migrations/0022_docs.up.sql`, `.down.sql`
- Modify: `deploy/base/kustomization.yaml`
- Test: `internal/store/docs_test.go` (schema smoke: insert/CHECK/uniques)

- [ ] **Step 1: Write the migration**

`0022_docs.up.sql` drops the on-ramp's tables before creating this part's.
The drop is unconditional and takes no backup: 025 §16 is superseded, `lode
doc sync` never ran against any deployment, and the three tables are empty
everywhere. A `DROP TABLE` (not `IF EXISTS`) is deliberate — if the tables are
missing, the migration history is not what this one assumes and failing is the
correct outcome.

```sql
-- Documents in the backbone (docs/specs/025-documents-in-the-backbone.md §5).
--
-- Replaces the git→backbone sync on-ramp's tables from 0011_docs and
-- 0012_doc_edges_covers. That subsystem (025 §16, superseded) projected files
-- into rows keyed (project, kind, ordinal) and carried sync provenance; this
-- one authors documents in the backbone and needs a single-column identity
-- that tasks.plan_doc and doc_edges.to_doc can reference. The tables are
-- empty, so this is a replacement and not a data migration.
--
-- The status CHECK mirrors ns.DesignDocStatuses (generated from ns/concept.ttl
-- once WL-70 lands); the kind CHECK mirrors the wl:Spec/wl:ADR/wl:Plan classes.

DROP TABLE doc_edges;
DROP TABLE doc_sections;
DROP TABLE docs;

CREATE TABLE docs (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id  text NOT NULL REFERENCES projects(id),
    kind        text NOT NULL CHECK (kind IN ('spec','adr','plan')),
    number      integer,          -- corpus number; NULL for plans (025 §14.3)
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

-- Specs and ADRs only (025 §9: plans carry no sections and no anchors).
CREATE TABLE doc_sections (
    doc_id          bigint NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
    anchor          text NOT NULL,
    number          text,
    heading         text NOT NULL,
    depth           integer NOT NULL,
    position        integer NOT NULL,
    last_revised_in integer NOT NULL DEFAULT 1,   -- 025 §4.4
    published       boolean NOT NULL DEFAULT false, -- frozen from first accept (025 §6)
    PRIMARY KEY (doc_id, anchor)
);

-- One row carries both directions: amends read backward is amendedBy, so the
-- 025 §14 mirror cannot disagree by construction. to_external holds a
-- cross-corpus shorthand this backbone cannot resolve (025 §14.3).
-- covers is a plan's promise about spec sections; implements is a component's
-- evidence about its code and covers's retired spelling (026 §5.1, §6.2).
CREATE TABLE doc_edges (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    from_doc    bigint NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
    from_anchor text,
    type        text NOT NULL CHECK (type IN
                ('covers','implements','amends','replaces','requires','wasDerivedFrom','blocks')),
    to_doc      bigint REFERENCES docs(id),
    to_anchor   text,
    to_external text,
    CHECK ((to_doc IS NULL) <> (to_external IS NULL)),
    -- blocks orders whole plan documents (025 §5): never section-scoped.
    CHECK (type <> 'blocks' OR (from_anchor IS NULL AND to_anchor IS NULL))
);
CREATE UNIQUE INDEX doc_edges_unique ON doc_edges
    (from_doc, coalesce(from_anchor,''), type,
     coalesce(to_doc, 0), coalesce(to_anchor,''), coalesce(to_external,''));
CREATE INDEX doc_edges_to ON doc_edges (to_doc) WHERE to_doc IS NOT NULL;

-- One open candidate revision per doc (025 §7: the candidate carries draft
-- implicitly by being here).
CREATE TABLE doc_revisions (
    doc_id     bigint PRIMARY KEY REFERENCES docs(id) ON DELETE CASCADE,
    body       text NOT NULL,
    created_by text REFERENCES actors(id),
    created_at timestamptz NOT NULL
);

-- 025 §9.2: nullable by design — a task no plan authored carries none. The
-- plan task format's `skills` land in the existing tasks.skills jsonb
-- column (migration 0007), so no new column is needed for them.
ALTER TABLE tasks ADD COLUMN plan_doc bigint REFERENCES docs(id);
CREATE INDEX tasks_plan_doc ON tasks (plan_doc) WHERE plan_doc IS NOT NULL;
```

`.down.sql` reverses it: `ALTER TABLE tasks DROP COLUMN plan_doc`, drop the
four tables, then re-create the on-ramp's three exactly as `0011_docs.up.sql`
and `0012_doc_edges_covers.up.sql` left them (copy the DDL — the shipped files
stay in the tree as history and must not be edited). Add both new files to
`deploy/base/kustomization.yaml`.

- [ ] **Step 2: Schema smoke test**

In `internal/store/docs_test.go` (it exists and tests the sync schema; the
sync-era cases go with the retirement): insert a spec row, a plan row without
number (passes), a spec without number (CHECK violation), a duplicate
`(project, kind, number)` (unique violation), a `blocks` edge with an anchor
(CHECK violation), a `covers` edge (accepted).

- [ ] **Step 3: Verify and commit**

Renumber if the collision check says so, and use the number it settles on.
The build must be clean, which means nothing left in the tree selects a
dropped column — see "On the files marked rewritten" above.

```bash
./scripts/check-migrations.sh --no-fix
go build ./... && go vet ./...
go test ./internal/store/ -run 'TestMigrate|TestDocSchema' -count=1
```

---

### Task 2 — Implement `designdoc.CompareSections`, the §6 gate

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Create: `internal/designdoc/diff.go`, `internal/designdoc/diff_test.go`

The accept gate needs exactly what 025 §6 forbids, computed between the
accepted body and a candidate body. `internal/kg/section` from the unexecuted
014 plan was never built; this is that logic, homed in `designdoc` where the
parser already lives.

- [ ] **Step 1: Write the failing tests**

Table-driven over accepted/candidate source pairs:

| Case | Expectation |
|---|---|
| identical | empty diff, no violations |
| section deleted | `Removed = [sec-2.1]`, violation naming it (§6 rule 1) |
| `## 2.` renumbered `## 3.` under the same anchor | `Renumbered`, violation (§6 rule 3) |
| `## 1a.` letter-suffix insert | `Added = [sec-1a]`, **no** violation |
| body of §2 edited | `Changed = [sec-2]` only — §1 untouched (§6 rule 5) |
| heading reworded, body identical | nothing changed, no violation (025 §3) |
| depth-4 anchored heading with limit 3 | violation naming the anchor (§6.1) |

- [ ] **Step 2: Implement**

```go
// SectionDiff compares an accepted document with a candidate revision
// (025 §6, enforced at accept time by the server per 025 §5). Removed,
// Renumbered and TooDeep are violations; Changed is the last_revised_in
// input; Added is informational.
type SectionDiff struct {
	Added, Removed, Renumbered, Changed, TooDeep []string
}

// DepthLimit is the 025 §6.1 addressability limit. Server-configurable is
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
- Modify: `internal/store/docs.go` (replace the sync-era `Doc`, `DocSection`,
  `DocEdge`, `DocFilter`, `GetDoc`, `ListDocs` and `validDocEdgeRels`),
  `internal/store/metrics.go`
- Test: `internal/store/docs_test.go`

- [ ] **Step 1: Write the failing tests**

- `CreateDoc` with a spec body: row created `draft`, `doc_sections` mirror
  the parsed anchors with `position` in source order, frontmatter `requires`
  becomes `doc_edges` rows resolved against existing docs, an unresolvable
  reference lands in `to_external`.
- `CreateDoc` with a plan body: zero `doc_sections` rows.
- `UpdateDocBody` on a draft spec: sections rebuilt; on an **accepted** spec:
  `ErrInvalidInput` ("revise instead"); on an accepted **plan**: allowed —
  plans stay freely mutable (025 §9, AC6).
- `GetDoc`, `ListDocs(DocFilter{Kind, Status, Project})`.
- Metrics: `worklode_doc_operations_total{op="create",outcome="ok"}`
  increments (use a fresh registry as the store metrics tests do).

- [ ] **Step 2: Implement**

```go
// Doc is a backbone document row (025 §5): a spec, ADR, or plan.
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
  acting-direction keys (`covers`, `requires`, `amends`, `replaces`,
  `wasDerivedFrom`) to rows; inverse keys (`isRequiredBy`, `amendedBy`,
  `isReplacedBy`) are ignored on write — the row read backward is the
  inverse. Coverage comes from `fm.CoverageEntries()`, which already reads
  `implements` as `covers`'s retired spelling (026 §5.1), and is written as
  type `covers`; the `implements` edge type stays reserved for components.
  Targets resolve by filename-style reference against
  `(project, slug)`/`(project, kind, number)`; misses store the raw string in
  `to_external`.
- Every mutation goes through `(*Store).RecordEvent` at the caller (API
  layer), so these take `tx` like `CreateTask` does.
- Metrics: extend `storeMetrics` with
  `docOps *prometheus.CounterVec` (`worklode_doc_operations_total`,
  labels `op` ∈ create/update/accept/revise, `outcome` ∈ ok/error), nil-safe
  methods, registered in `newStoreMetrics`. Recorded by the `Store`-level
  wrappers the API calls. This replaces the on-ramp's
  `worklode_doc_upserts_total` (`storeMetrics.docUpserts`) — delete it with
  its last caller rather than leaving a counter nothing increments.

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
// AcceptDoc is the manual commit of 025 §7: draft -> accepted, gated on the
// assignee (default: the creator). On a spec or ADR it freezes the published
// anchor set; doc-level replaces edges flip their targets to superseded.
// Plans are rejected here until plan acceptance (part 3) supplies the
// minting half — accepted-without-tasks must never exist (025 §9.2).
func AcceptDoc(tx *sql.Tx, now time.Time, id int64, actorID string, eventID int64) (*Doc, error)

// ReviseDoc opens a candidate revision against an accepted spec/ADR: a copy
// of the current body to edit. The accepted version stays authoritative
// throughout (025 §7).
func ReviseDoc(tx *sql.Tx, now time.Time, id int64, actorID string) error
func UpdateRevision(tx *sql.Tx, now time.Time, id int64, body string) error

// AcceptRevision runs the 025 §6 gate (designdoc.CompareSections) and, when
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
- Modify: `internal/api/docs.go`, `internal/api/docs_test.go` (both exist as
  the sync on-ramp's handlers), `internal/api/router.go` (`routeGuards`),
  `internal/api/web.go`, `internal/api/render.go`
- Create: `internal/ui/docs.templ` (and its generated `docs_templ.go`)

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
use). Read-only, and gated by `permWebRead` like every other cockpit page —
the cockpit is session-gated whenever a login provider is configured, so
"unauthenticated read-only pages" is no longer the web posture.

- [ ] **Step 2: Implement**

Each handler wraps its store call in `(*Store).RecordEvent`
(`doc.created` / `doc.updated` / `doc.accepted` / `doc.revised` /
`doc.revision_accepted`) with a random external id, mirroring `createTask`.
`actorFrom(r)` supplies the acceptor. HTTP metrics ride the existing
middleware; the domain counters landed in Task 3.

Routes go in `internal/api/router.go`'s `routeGuards`, not `server.go`:
`NewServer` panics on a registered route the table does not name and fails on
a table entry no route uses, so the guard rows and the registrations land
together. `permDocRead`/`permDocWrite` already exist (`internal/api/authz.go`)
and are what the on-ramp's rows used; the `POST /api/v1/docs/sync` row goes
with the sync retirement, `GET /api/v1/docs` and `GET /api/v1/docs/{id}` are
reused, and the write routes are new rows under `permDocWrite`. The `/docs`
pages take `permWebRead`.

The cockpit pages are templ components in `internal/ui`, taking view types
`internal/api/render.go` builds — `internal/ui` imports nothing beyond stdlib
and the templ runtime, never `internal/api`. Regenerate the committed
`*_templ.go` and the stylesheet with `go generate ./...`.

- [ ] **Step 3: Verify and commit**

```bash
go generate ./... && git diff --exit-code internal/ui
go test ./internal/api/ ./internal/ui/ -count=1
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
- Create: `e2e/docs_test.go` (the on-ramp's `e2e/docsync_test.go` goes with
  the sync retirement; this is not a rename of it)

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

## Done when (maps to 025 §24)

1. AC5 (spec half): a document under review is a `draft` row plus whatever
   review task points at it — `proposed` exists nowhere; `lode doc accept`'s
   store half is manual and assignee-gated.
2. AC6: plan rows take body edits at any status and hold zero sections; spec
   rows enforce 025 §6 at accept time, proven by the rejected-renumber e2e
   step.
3. `worklode_doc_operations_total` visible on `/metrics` with tests, per
   spec 022's conventions.
