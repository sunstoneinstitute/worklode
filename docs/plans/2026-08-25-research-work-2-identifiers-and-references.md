---
status: accepted
covers:
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-1
    coverage: none
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-4
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-25-research-work-1-milestones.md
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-5
    coverage: full
blockedBy:
  - 2026-08-25-research-work-1-milestones.md
---

# Research work part 2 — per-kind identifiers and cross-project references

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** two halves. First, what remains of 029 §4's identifier cutover:
specs and ADRs draw their numbers from the project's `(kind)` counter the way
deliverables and plans already do — `lode doc new` without `--number` gets the
next number allocated, and an explicit number (the importer's case: a spec or
ADR filename carries its identity) advances the counter past itself. Second,
029 §5's one typed edge table, `entity_edges`, carrying the references that
legitimately cross a project boundary — a milestone depending on a deliverable
produced under another project, and `seeded_by` from a project to its intake
task — surfaced on the milestone page part 1 builds so the half is
demonstrable, not just schema.

**Shipped ahead of this plan** (WL-336, PR #321, migration 0052): the CTO
decided on 2026-08-25 that every document id is flat `<KEY>-<KIND>-<N>` —
`COW-PLAN-7`, not the two-part `COW-PLAN-4-1` this plan's first draft was
built on — and that landing rewrote 029 §4 (amending 025 §14.3 and §16.3) and
shipped the plan half of the cutover: plans backfilled in corpus order,
`docs.number` NOT NULL, the `PLAN` counter, the `(SPEC|ADR|PLAN)` shorthand
with kind-aware resolution, `lode show COW-PLAN-7` / `--plan 7` /
`/docs/COW-PLAN-7`, and the autolinker. What that leaves for this plan is the
SPEC/ADR counter half above; do not rebuild any of the shipped list.

**Series:** part 2 of the nine-part 2026-08-25 set planning spec 029.
`blockedBy` part 1 (`2026-08-25-research-work-1-milestones`): part 1's
migration 0053 widens the `project_entity_seq.kind` CHECK to
`('DEL','PLAN','MILE')` — this plan's 0055 widens that CHECK again — and its
cockpit milestone page is where Task 5 surfaces deliverable references. Part 4
(`2026-08-25-research-work-3-intake-and-promotion`) consumes this part twice:
the promotion transaction writes the `seeded_by` edge through Task 3's store
path, and 029 §1's project metadata (labels, `horizon`) is entirely part 4's.

**Coverage, declared:** §4 is `partial` because part 1 owns the milestone
(`MILE`) half of the identifier table — its counter widening and its
`lode show` support; this part owns the SPEC/ADR counters. Together they are
`full`: `DEL` and `PLAN` already draw from counters (`PLAN` via WL-336), and
bare task ids are explicitly unchanged. §5 is `full`: the table, the
store/API surface, and both crossing reference kinds are built here — part 4
merely inserts `seeded_by` rows through them. §1 is `none`: this plan is
bound by §1's definition of `seeded_by` as a reference (it shapes Task 2's
rel vocabulary) but builds none of §1 — labels, `horizon`, and the promotion
that writes `seeded_by` are part 4's.

**Tech stack:** Go 1.26, `net/http` mux, pgx against Postgres,
`templ`-rendered pages, Prometheus client. Store and `internal/api` tests
need Postgres with pgvector.

**Read first:**
- `docs/specs/inlined/029-research-work-in-the-backbone.md` §4, §5
- `internal/store/docs.go` — `CreateDoc` (the create path this plan rewires;
  its plan arm already allocates from the `PLAN` counter and is the template
  for the SPEC/ADR arm), `resolveDocRef` (shorthand resolution, unchanged
  here)
- `internal/store/deliverables.go` — `CreateDeliverable`'s counter upsert,
  the mint pattern every allocation copies
- `internal/cmd/docimport.go` — the importer that keeps supplying explicit
  numbers (`importLeadingNumber`, and the walk abort for a spec/ADR file
  with no leading number)
- `deploy/base/migrations/0052_plan_numbers.up.sql` — the shipped plan half;
  this plan's 0055 is its small sibling
- `deploy/base/migrations/0015_deliverables.up.sql` (`project_entity_seq`)

## Global Constraints

- **Exact spellings, quoted once.** Id grammar (029 §4): flat
  `<KEY>-<KIND>-<N>` for every document kind — `COW-SPEC-4`, `COW-ADR-1`,
  `COW-PLAN-7`; bare task ids (`[A-Z][A-Z0-9]*-[0-9]+`) are untouched — only
  tasks appear in branch names, `Worklode-Task:` trailers, and merge
  correlation. `project_entity_seq.kind` CHECK after this plan:
  `('DEL','PLAN','MILE','SPEC','ADR')`. Edge rels, the full vocabulary this
  plan admits: `'depends_on'` (milestone → deliverable), `'seeded_by'`
  (project → task). Routes: `GET/POST /api/v1/references`,
  `POST /projects/{id}/milestones/{mid}/references`. Permissions:
  `reference.read`, `reference.write`. Event type: `reference.created`.
  Metric: `worklode_reference_writes_total{rel,outcome}` — both labels
  bounded, never a project or entity id.
- **No id moves.** Every existing spec and ADR keeps the number its filename
  states; this plan only seeds the counters past them. The plan backfill
  already happened (migration 0052) and is not revisited.
- **Migrations:** each is a new numbered `.up.sql`/`.down.sql` pair listed in
  `deploy/base/kustomization.yaml`, never an edit to a shipped one. This
  plan's numbers are 0055 and 0056 (nominal; the series claims numbers in
  parallel and `./scripts/check-migrations.sh` renumbers on collision).
- **Every mutation is one event.** Reference creation wraps its store write
  in the same `RecordEvent` + apply-callback transaction `recordDeliverable`
  (`internal/api/deliverables.go:98`) uses. Document creation keeps its
  existing `doc.created` event; allocation changes the number's origin, not
  the event.
- **One model (ADR 036).** `model.EntityEdge` is declared in
  `internal/model` with wire-name fields; `internal/model/rule_test.go` and
  `deps_test.go` stay green.
- **Every route is named in `routeGuards`** (`internal/api/router.go`);
  `NewServer` refuses to boot otherwise. Role checks stay in the `grants`
  table, never in handlers.
- **Metrics** (spec 022): nil-safe metrics struct in the owning package's
  `metrics.go`, `prometheus.Registerer` threaded from `serve.go`,
  `worklode_` prefix, bounded labels, tests.
- **Store tests need Postgres with pgvector**; they skip silently without it
  unless `CI` is set — a green run without Postgres proved nothing.
- **`e2e/` drives public surfaces only** — HTTP API, signed webhooks, web
  pages; never a direct store write.
- **Every task leaves `go test ./...` green** and ends with a commit.

## Decisions this plan executes (settled here; do not reopen)

- **Explicit numbers stay accepted for import.** "Sequence-allocated, not
  author-supplied" governs authoring: `lode doc new` without `--number` gets
  the counter's next value. The corpus importer still states each spec and
  ADR's number — the filename carries identity, and the walk still aborts on
  a spec/ADR file with no leading number — and an explicit insert advances
  the counter past it (`GREATEST`), so allocation after an import continues
  from the corpus, never behind it. Plans take no explicit number anywhere,
  exactly as `CreateDoc` already enforces.
- **`task_edges` stays where it is.** Its four rels are task-only, carry real
  foreign keys into `tasks`, and feed the hierarchy and blocking queries on
  the hottest path; folding them into a polymorphic `(kind, id)` table would
  trade FK integrity for uniformity nobody asked for. `entity_edges` carries
  only what cannot live there: references whose ends are different kinds.
- **The web add-reference form takes a typed deliverable id** (`COW-DEL-3`),
  validated server-side, rather than a cross-project dropdown — a picker over
  every deliverable in every project is part 4-era UI at the earliest.

## Tasks

### Task 1 — Migration 0055: SPEC/ADR counters, and allocation in CreateDoc

```yaml
kind: feature
priority: high
skills:
  - worklode-migrations
  - golang-migrate:test-roundtrip
blockedBy: []
```

Create `deploy/base/migrations/0055_doc_number_allocation.up.sql` /
`.down.sql`, listed in `deploy/base/kustomization.yaml`:

```sql
-- Specs and ADRs join the per-project ordinal counters (029 §4's second
-- half). 0052 put PLAN on the counters and left SPEC/ADR author-supplied;
-- 0053 added MILE. This widens the CHECK and seeds each project's SPEC and
-- ADR counters past everything already minted, tombstones included so a
-- purged number is never reissued. No ON CONFLICT: the old CHECK made
-- SPEC/ADR rows impossible, so none exist.
ALTER TABLE project_entity_seq DROP CONSTRAINT project_entity_seq_kind_check;
ALTER TABLE project_entity_seq ADD CONSTRAINT project_entity_seq_kind_check
    CHECK (kind IN ('DEL','PLAN','MILE','SPEC','ADR'));

INSERT INTO project_entity_seq (project_id, kind, next)
SELECT project_id, upper(kind), max(number) + 1
  FROM docs WHERE kind IN ('spec','adr')
 GROUP BY project_id, kind;
```

Down (golang-migrate:gen-down): delete the `SPEC`/`ADR` counter rows, then
restore the `('DEL','PLAN','MILE')` CHECK.

Store changes, same commit — `internal/store/docs.go` `CreateDoc`:

- Drop the `in.Kind != "plan" && in.Number <= 0` refusal: zero now means
  allocate. A spec or ADR with `Number == 0` draws from the
  `(project, SPEC|ADR)` counter with the exact upsert the plan arm and
  `CreateDeliverable` use.
- An explicit `Number > 0` inserts it and advances the counter:
  `INSERT ... ON CONFLICT (project_id, kind) DO UPDATE SET
  next = GREATEST(project_entity_seq.next, $n + 1)`.
- The plan arm is untouched — it already allocates and already refuses an
  explicit number.

CLI surface, same commit:

- `internal/cmd/doc.go`: the `--number` help still says "omit for a plan,
  which carries none" — now `corpus number (spec/adr only; omit to allocate
  the next number)`. No validation change — the server decides.
- `internal/cmd/docimport.go`: behaviour unchanged — the importer states
  every spec/ADR number from the filename and aborts the walk on a file
  without one; plans import with number 0 and the server allocates. Update
  the stale comment on the `number` field ("0 for a plan, which carries
  none (025 §14.3)") to the allocation truth; verify, don't rebuild.

First test of the new behaviour, in `internal/store/docs_test.go`:

```go
func TestCreateDocAllocatesNumbers(t *testing.T) {
	st := OpenTestStore(t)
	// ... project fixture with key "WL" ...
	spec := mustCreateDoc(t, st, DocInput{Kind: "spec", Slug: "001-a", Body: specBody})
	if spec.Number != 1 {
		t.Fatalf("first spec number = %d; want 1 (allocated)", spec.Number)
	}
	// Explicit number advances the counter past itself.
	mustCreateDoc(t, st, DocInput{Kind: "spec", Slug: "007-b", Number: 7, Body: specBody})
	next := mustCreateDoc(t, st, DocInput{Kind: "spec", Slug: "008-c", Body: specBody})
	if next.Number != 8 {
		t.Fatalf("allocated after explicit 7 = %d; want 8", next.Number)
	}
}
```

plus an ADR case and one asserting an explicit number *below* the counter
(`Number: 2` after the above) inserts without rewinding `next`.

- [ ] Write both migration files; add the two lines to
      `deploy/base/kustomization.yaml`.
- [ ] `./scripts/check-migrations.sh --no-fix` — exit 0 (or accept the
      renumber and update this plan's references to 0055).
- [ ] Roundtrip: up → down → up applies cleanly against a scratch database.
- [ ] Store and CLI changes above; extend `docs_test.go` with the allocation
      cases.
- [ ] `lode doc new --kind spec --slug test-alloc --file -` against the local
      stack: response shows the allocated next number.
- [ ] `go test -trimpath ./...` green; commit:
      `Allocate spec and ADR numbers from per-kind counters (029 §4)`.

### Task 2 — Migration 0056: the `entity_edges` table

```yaml
kind: feature
priority: high
skills:
  - worklode-migrations
  - golang-migrate:test-roundtrip
blockedBy: []
```

Create `deploy/base/migrations/0056_entity_edges.up.sql` / `.down.sql`,
listed in `deploy/base/kustomization.yaml`:

```sql
-- References across projects (029 §5). Containment never crosses a project
-- boundary; these references do, which is why the ends are (kind, id) pairs
-- with no project column and no FK — each side's per-kind table carries
-- identity, and there is deliberately no unified entities table.
-- task_edges is NOT folded in: its rels are task-only, with real FKs into
-- tasks and the hierarchy/blocking queries tuned to them.
CREATE TABLE entity_edges (
    from_kind  text NOT NULL CHECK (from_kind IN ('project','milestone')),
    from_id    text NOT NULL,
    to_kind    text NOT NULL CHECK (to_kind IN ('deliverable','task')),
    to_id      text NOT NULL,
    rel        text NOT NULL CHECK (rel IN ('depends_on','seeded_by')),
    created_at timestamptz NOT NULL,
    created_by text REFERENCES actors (id) ON DELETE RESTRICT,
    PRIMARY KEY (from_kind, from_id, to_kind, to_id, rel)
);

-- The read is always "references touching this entity", from either end.
CREATE INDEX entity_edges_to_idx ON entity_edges (to_kind, to_id);
```

The PK doubles as the from-side index. The kind CHECKs carry only the kinds
the two admitted rels use, widened when a rel arrives that needs more —
the same convention as `project_entity_seq.kind`. Down: `DROP TABLE
entity_edges;`.

- [ ] Both files + kustomization lines;
      `./scripts/check-migrations.sh --no-fix` exit 0.
- [ ] Roundtrip up → down → up clean.
- [ ] `go test -trimpath ./internal/store -run TestMigrations` ok.
- [ ] Commit: `Add the entity_edges reference table (029 §5)`.

### Task 3 — Store and model: create and read references

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

`internal/model/reference.go`:

```go
// EntityEdge is one typed reference between entities of different kinds
// (029 §5), the only edges that may cross a project boundary.
type EntityEdge struct {
	FromKind  string    `json:"from_kind"`
	From      string    `json:"from"`
	ToKind    string    `json:"to_kind"`
	To        string    `json:"to"`
	Rel       string    `json:"rel"` // depends_on | seeded_by
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}
```

`internal/store/references.go`:

- `referenceShapes` — the rel vocabulary as data, one row per rel naming its
  end kinds: `depends_on: milestone → deliverable`,
  `seeded_by: project → task`. A rel outside the table, or ends of the wrong
  kind, is `ErrInvalidInput` naming what was expected.
- `CreateEntityEdge(tx, now, in) (*model.EntityEdge, error)` — validates the
  shape, checks both ends exist in their kind's table (`milestones`,
  `deliverables`, `projects`, `tasks`; a missing end is `ErrNotFound` naming
  kind and id), inserts, and maps the PK violation to a named
  `ErrReferenceExists`. Cross-project ends are the point — no project check.
  Transaction-scoped like `CreateDeliverable`, for part 4's promotion
  transaction to compose.
- `(s *Store) ReferencesFor(ctx, kind, id) ([]model.EntityEdge, error)` —
  edges where either end matches, from-side first, stable order.
- `(s *Store) MilestoneDeliverableRefs(ctx, milestoneID)
  ([]model.Deliverable, error)` — the one bulk reader Task 5's page needs:
  join `entity_edges` (`rel = 'depends_on'`) to `deliverables` through the
  existing `deliverableFrom` projection, so the referenced deliverable's
  reported state comes along and "blocked on an unpublished table" is
  visible, not just a bare id.

First test:

```go
func TestCreateEntityEdgeShapes(t *testing.T) {
	st := OpenTestStore(t)
	// fixtures: project A with milestone M, project B with deliverable D, task T
	edge := mustRef(t, st, "milestone", mID, "deliverable", dID, "depends_on")
	if edge.CreatedAt.IsZero() {
		t.Fatal("created_at not stamped")
	}
	// wrong shape, unknown rel, dangling end, duplicate
	refuse(t, st, "milestone", mID, "task", tID, "depends_on")   // ErrInvalidInput
	refuse(t, st, "milestone", mID, "deliverable", dID, "eats")  // ErrInvalidInput
	refuse(t, st, "milestone", mID, "deliverable", "B-DEL-99", "depends_on") // ErrNotFound
	refuseDup(t, st, "milestone", mID, "deliverable", dID, "depends_on")     // ErrReferenceExists
}
```

plus a `seeded_by` case (project → task, across projects) — part 4 writes
that row in production; the shape is proven here.

- [ ] Model type (`rule_test.go` green), store file, tests including
      `ReferencesFor` from both ends and `MilestoneDeliverableRefs` carrying
      reported state.
- [ ] `go test -trimpath ./...` green; commit:
      `Store references in entity_edges with a typed rel vocabulary (029 §5)`.

### Task 4 — API: reference routes, event, metric

```yaml
kind: feature
priority: high
skills: []
blockedBy: [3]
```

`internal/api/references.go`, following `deliverables.go`'s shape:

- `POST /api/v1/references` — body is `model.EntityEdge` (only the five
  identity fields read; `created_by` is the authenticated actor). Wraps
  `CreateEntityEdge` in `RecordEvent(..., "reference.created", ...)` exactly
  as `recordDeliverable` does, with the same source derivation. 201 with the
  created edge; store sentinels map through `mapStoreErr` (`ErrInvalidInput`
  422, `ErrNotFound` 404, `ErrReferenceExists` 409).
- `GET /api/v1/references?kind=<k>&id=<id>` — `ReferencesFor`; both params
  required, 422 otherwise.
- `routeGuards` rows: `guarded(permReferenceRead)` /
  `guarded(permReferenceWrite)`; both permissions granted to
  `{RoleUser, RoleAdmin}` in `grants` — any crew member may declare a
  dependency, matching deliverable creation.
- `internal/api/metrics.go` (or the existing api metrics struct):
  `worklode_reference_writes_total{rel,outcome}`, `rel` bounded by the
  vocabulary, `outcome` `ok|error`. Test with a registry as the existing
  metric tests do.

Handler tests: create via `POST` and read back via `GET` from each end;
assert the `reference.created` event row exists with the actor; assert the
guard table boots (`NewServer` in tests already fails on an unguarded
route).

- [ ] Handlers, guards, grants, metric, tests.
- [ ] `go test -trimpath ./internal/api` ok.
- [ ] `go test -trimpath ./...` green; commit:
      `Reference API: POST/GET /api/v1/references (029 §5)`.

### Task 5 — Deliverable references on the milestone page

```yaml
kind: feature
priority: medium
skills:
  - worklode-cockpit-ui
blockedBy: [4]
```

Part 1's plan builds the cockpit milestone surface under
`/projects/{id}/milestones...`; this task extends its detail view (rebase to
part 1's exact route and templ component names as landed):

- A **References** section on the milestone detail: one row per
  `MilestoneDeliverableRefs` result — deliverable id (its project key makes
  the cross-project origin visible), name, and reported state, with the id
  linking to that project's Deliverables page. Empty state: `No deliverable
  references.`
- An add form: one text input named `deliverable` taking a typed id
  (`COW-DEL-3`), posting to
  `POST /projects/{id}/milestones/{mid}/references` — a `webform.go`
  handler that parses the id, resolves it to a deliverable, and writes
  through the same `RecordEvent` + `CreateEntityEdge` path as the JSON
  route (`reference.created`, source `web`). Unknown id → the form re-renders
  with the store's error text. Route guard: `guarded(permWebWrite)`.
- View types stay in `internal/ui/views.go`; `internal/ui` keeps its
  stdlib + `internal/model` + templ-runtime dependency set. Regenerate
  `*_templ.go` and the stylesheet if touched (`go generate ./...`).

Handler test: milestone page renders a seeded cross-project reference with
its reported state; the POST creates the edge and redirects back; a bad id
re-renders with the error.

- [ ] Store reader is Task 3's — reuse, don't re-derive.
- [ ] Templ + handler + guard + tests; `go generate ./...` diff committed.
- [ ] Screenshot or curl the page against the local stack: the reference row
      shows id, name, and state.
- [ ] `go test -trimpath ./...` green; commit:
      `Show and add deliverable references on the milestone page (029 §5)`.

### Task 6 — e2e journey and docs alignment

```yaml
kind: chore
priority: medium
skills:
  - worklode-lode-plugin
blockedBy: [1, 5]
```

One e2e test in `e2e/` driving public surfaces only:

- Create a project; `POST /api/v1/docs` a spec with no number → number 1
  allocated; a second with an explicit number → the next allocation lands
  past it.
- Create a milestone (part 1's API) in project A and a deliverable in
  project B; `POST /api/v1/references` the `depends_on` edge; `GET` it back
  from the deliverable end; load the milestone page and assert the
  reference row.

Docs alignment, in the same commit — the surfaces register
(`docs/agent-surfaces.md`) names the checklist; walk it for the changed CLI:

- `docs/authoring-design-docs.md`: WL-336 already rewrote the plan-id prose;
  what remains is any statement that a spec or ADR *requires* `--number` —
  rewrite to "optional, server-allocated; the importer states it from the
  filename". Keep the edits short; this file is a checklist, not a spec.
- `docs/agent-surfaces.md`: check the skills and plugin commands that
  hardcode `lode doc new` invocations; update any that require `--number`.
- `docs/follow-ups.md`: the `project_entity_seq` counter entry (part 1's
  Task 10 rewrites it to "SPEC and ADR arrive with this plan") is
  discharged here — strike it.

- [ ] `make test-e2e` (TEST_POSTGRES_DSN reachable) — the new journey
      passes.
- [ ] Docs edits above; `./scripts/secfmt.py -l` clean.
- [ ] `go test -trimpath ./...` green; commit:
      `e2e: allocated ids and cross-project references; align docs (029 §4, §5)`.

## What this part does not do

- **029 §1's project metadata**: `labels`, `horizon`, and the promotion
  transaction that stamps them and writes the `seeded_by` edge — part 4
  (`2026-08-25-research-work-3-intake-and-promotion`). This part only makes
  the edge row possible and proves its shape.
- **Milestones themselves** — table, API, page, `MILE` counter and
  `lode show` support: part 1.
- **Anything about approval flows** (029 §7) — parts 3 and 5.
- **A CLI verb for references**: the API, the web form, and part 4's
  transaction are the three writers 029 names; a `lode` verb waits for a
  caller that needs one.
- **Re-doing the plan half of the identifier cutover** — plan numbers, the
  `PLAN` shorthand, `lode show --plan`: shipped under WL-336 before this
  plan executes.
