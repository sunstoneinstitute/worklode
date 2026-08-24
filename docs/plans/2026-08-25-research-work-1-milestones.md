---
status: draft
covers:
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-0
    coverage: none
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-2
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-25-research-work-3-intake-and-promotion.md
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-3
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-25-research-work-4-deliverable-state.md
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-4
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-25-research-work-2-identifiers-and-references.md
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-9
    coverage: none
  - spec: docs/specs/032-project-cockpit.md#sec-7
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-25-approvals-2-flows-and-requirements.md
      - docs/plans/2026-08-25-approvals-3-revision-binding-and-gates.md
  - spec: docs/specs/032-project-cockpit.md#sec-10
    coverage: none
  - spec: docs/specs/032-project-cockpit.md#sec-11
    coverage: none
---

# Research work 1: milestones

Part 1 of the nine-part plan series for spec 029. It makes 029 §2's container
hierarchy real: `project → milestone → {task, deliverable}`. A `milestones`
table minting `<KEY>-MILE-<n>` ids from the existing `project_entity_seq`
counter, nullable `milestone_id` columns on `tasks` and `deliverables`, a pure
progress derivation over a milestone's tasks and its deliverables' reported
state, store readers and event-emitting mutations, the milestones JSON API and
`lode milestone` CLI verbs, a project-local Milestones page, milestone
grouping on the Deliverables page, and `lode show WL-MILE-2` rendering a
milestone instead of erroring.

**Series.** P1 has no ordering edge — it executes first against today's
schema. P2 (`2026-08-25-research-work-2-identifiers-and-references`), P4
(`…-research-work-3-intake-and-promotion`) and P6
(`…-research-work-4-deliverable-state`) declare `blockedBy` on this plan; that
ordering lives in their frontmatter, never in a task number across files.

## Coverage notes

Why each claim above is what it is:

- **029 §0** (`none`) — the four named problems bind the whole series;
  this part implements none of them end to end.
- **029 §2** (`partial`, full with P4) — this plan builds the milestone
  entity, the two `milestone_id` columns, and the progress-is-a-query rule.
  The default two-milestone / five-deliverable template minted at promotion
  belongs to P4's promotion transaction. §2's `decision` task kind and
  task → subtask hierarchy already exist and are untouched.
- **029 §3** (`partial`, full with P6) — deliverables gain their milestone
  parent and a reparent mutation here. Deliverable state stays exactly what
  `internal/hooks/catalog.go` already files: push-reported, observed-only.
  By-label identity, the poll prober, and the `user_reported` write path are
  P6's.
- **029 §4** (`partial`, full with P2) — the `MILE` kind joins
  `project_entity_seq` and `lode show` learns to render milestones. The
  SPEC/ADR/PLAN counters, the 0037 CHECK replacement, and the
  `lode show --plan` cutover are P2's.
- **029 §9** (`none`) — the out-of-scope list binds this part (no
  per-project access control, no probing-as-verification) and nothing in it
  is built.
- **032 §7** (`partial`, full with approvals-2 and approvals-3) — the
  Overview's definition of done needs deliverables grouped under milestones,
  which this plan provides; the review lanes, revision binding, and approval
  views are the approvals parts'.
- **032 §10 / §11** (`none`) — standing rules. The Milestones page and the
  grouped Deliverables page inherit the narrow-width and keyboard rules;
  the e2e task inherits §11's public-surfaces-only rule.

Not claimed but binding: **029 §5's containment rule** — a milestone's tasks
and deliverables are its own, never another project's. This plan enforces it
(Global constraints below); the typed edge table that lets a milestone
*reference* a foreign deliverable is P2's.

Deliberately not built in P1, recorded here rather than as follow-ups:
milestone rename, reorder, and delete. A milestone stores identity, title,
and ordering only; the first real project that needs to fix a typo'd title
motivates the mutation, and P4's template mints titles nobody types.

## Global constraints

- **Event spellings are pinned.** Creating a milestone records
  `milestone.created` (payload `{"project", "id", "title", "position",
  "created_by"}`). Attaching or detaching a task rides the existing
  `task.updated` event with a `LogChange` field `"milestone"`. Attaching or
  detaching a deliverable records `deliverable.updated` (payload `{"id",
  "project", "milestone"}`, `milestone` `""` on detach). Event `source` is
  `"cli"` on the JSON-API path and `"web"` on a cockpit-form path, matching
  `internal/api/deliverables.go`'s `recordDeliverable`.
- **The progress rule is pinned, one definition** (029 §2: a milestone
  stores no state of its own; progress is a query). A task counts as closed
  when its state is in `deliveredStateSet` (`internal/store/tasks.go`:
  `merged`, `deployed_dev`, `deployed_prod`, `released`, `abandoned`). A
  deliverable counts as live when its `ReportedState` is `published` or
  `updated` — the same two states `internal/ui/views.go`'s `deliverableChip`
  colours `ok`. Only `ComputeMilestoneProgress` (Task 2) applies these
  buckets; no other surface re-derives them and no column stores the result.
- **Containment never crosses a project boundary** (029 §5). Every mutation
  that sets a `milestone_id` verifies the milestone belongs to the same
  project as the task or deliverable, refusing with `ErrInvalidInput` the way
  `internal/store/hierarchy.go`'s `checkHierarchy` refuses a cross-project
  `child_of` (line 139).
- **Tasks without a milestone stay legal everywhere** (029 §2: ongoing
  maintenance, the norm for engineering projects). No server path requires
  attachment; the sunstone-way skill, not this repo, nags research projects.
- **Metrics** (spec 022): one new counter in `internal/api/metrics.go`
  (nil-safe struct, `prometheus.Registerer` threaded from `serve.go`):
  `worklode_milestone_changes_total{action, outcome}` with
  `action ∈ {create, task_attach, deliverable_attach}` and
  `outcome ∈ {ok, rejected, error}`. Bounded labels only — never a project,
  milestone, or actor id.
- **Authz:** new permissions `milestone.read` / `milestone.write` land in
  `routeGuards` (`internal/api/router.go`) and the `grants` table
  (`internal/api/authz.go`) only — both granted `{RoleUser, RoleAdmin}` —
  never a check inside a handler.
- **Migrations:** this part owns 0052 (0053 is reserved and unused). New
  numbered `.up.sql`/`.down.sql` pair, listed in
  `deploy/base/kustomization.yaml`; never edit a shipped migration.
  `./scripts/check-migrations.sh --no-fix` before committing.
- **One model** (ADR 036): every shape crossing the HTTP boundary is
  declared once in `internal/model`, stdlib imports only, wire field names.
  `internal/model/rule_test.go` enforces it.
- **`internal/cmd` decides, `internal/cli` renders:** every human-readable
  view is a `cli.*Table`/`cli.*Render` function in `internal/cli/render.go`;
  no tabwriter or timestamp formatting under `internal/cmd`.
- **CLI surface changes** (new `lode milestone` verbs, the `lode show`
  cutover, the `lode task edit --milestone` flag) follow the checklist in
  `docs/agent-surfaces.md`;
  `go test -trimpath ./internal/cmd -run TestAgentSurfaces` stays green.
- **Store tests need Postgres with pgvector** (default DSN in CLAUDE.md,
  override `TEST_POSTGRES_DSN`). A skipped store test proved nothing.
- **`e2e/` drives public surfaces only** — HTTP API and web pages, never a
  direct store write.
- **The templ/Tailwind toolchain is fixed:** `.templ` sources in
  `internal/ui`, `go generate ./...` regenerates the committed `*_templ.go`
  and `internal/ui/assets/app.css`; commit the generated files.
  `internal/api` imports `internal/ui`, never the reverse.
- **Every task leaves `go test ./...` green** and ends with a commit.
- All tasks are specified for a Sonnet-tier implementer per
  `MODEL_SELECTION.md`; none carries an open design decision. Escalate on
  the first sign the plan does not match the code.

## Tasks

### Task 1 — Migration 0052: milestones, milestone_id, MILE counter kind

```yaml
kind: chore
priority: high
skills:
  - golang-migrate:migration
blockedBy: [ ]
```

One migration pair in `deploy/base/migrations/`, listed in
`deploy/base/kustomization.yaml` after the 0051 entry.

`0052_milestones.up.sql`:

```sql
-- Milestones (spec 029 §2): project → milestone → {task, deliverable}.
-- A milestone stores identity, title, and ordering only — progress is a
-- query over its tasks and its deliverables' reported state, never a column.
CREATE TABLE milestones (
    id          text PRIMARY KEY,               -- <KEY>-MILE-<n>
    project_id  text NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    title       text NOT NULL,
    position    integer NOT NULL,
    created_by  text REFERENCES actors (id) ON DELETE RESTRICT,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL
);

CREATE INDEX milestones_project_idx ON milestones (project_id, position, id);

-- Containment (029 §2): nullable on both — a task with no milestone is
-- ongoing maintenance, a deliverable may be declared before its milestone.
-- ON DELETE SET NULL: removing a milestone orphans its children back to the
-- project rather than deleting work.
ALTER TABLE tasks        ADD COLUMN milestone_id text REFERENCES milestones (id) ON DELETE SET NULL;
ALTER TABLE deliverables ADD COLUMN milestone_id text REFERENCES milestones (id) ON DELETE SET NULL;

CREATE INDEX tasks_milestone_idx        ON tasks (milestone_id)        WHERE milestone_id IS NOT NULL;
CREATE INDEX deliverables_milestone_idx ON deliverables (milestone_id) WHERE milestone_id IS NOT NULL;

-- The MILE kind joins the per-project ordinal counter (029 §4), exactly the
-- CHECK widening migration 0015 promised when the entity arrived.
ALTER TABLE project_entity_seq DROP CONSTRAINT project_entity_seq_kind_check;
ALTER TABLE project_entity_seq ADD CONSTRAINT project_entity_seq_kind_check
    CHECK (kind IN ('DEL','MILE'));
```

`0052_milestones.down.sql` reverses in order: restore the CHECK to
`('DEL')` (delete any `MILE` counter rows first, or the ADD CONSTRAINT
fails), drop the two partial indexes, drop the two columns, drop the table.

Existing code is unaffected by construction: `internal/store/deliverables.go`
selects by the explicit `deliverableColumns` list, never `*`, so the new
column is invisible until Task 7 reads it.

- [ ] Write the two files and the kustomization entry.
- [ ] `./scripts/check-migrations.sh --no-fix` — exits 0.
- [ ] Round-trip up → down → up against a scratch database
      (`golang-migrate:test-roundtrip`, or manually with the compose
      Postgres) — reverses cleanly, including with a `MILE` counter row
      present.
- [ ] `go test -trimpath ./internal/store` with Postgres up — existing tests
      green against the extended schema.
- [ ] Commit: `Add milestones table, milestone_id columns, and MILE counter kind`.

### Task 2 — Model shapes and the pure progress derivation

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

The pure layer first: the wire shapes and the one function that decides what
"progress" means, table-tested with no database.

New `internal/model/milestone.go`:

```go
package model

import "time"

// Milestone is one ordered container in a project (spec 029 §2). It stores
// identity, title, and ordering only; Progress is derived on read from its
// tasks and its deliverables' reported state, never stored.
type Milestone struct {
	ID        string    `json:"id"`      // <KEY>-MILE-<n>
	Project   string    `json:"project"`
	Title     string    `json:"title"`
	Position  int       `json:"position"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Progress  MilestoneProgress `json:"progress"`
}

// MilestoneProgress is the derived query 029 §2 makes of a milestone's
// children. Closed and live follow the pinned buckets in the plan's global
// constraints; ComputeMilestoneProgress is the only producer.
type MilestoneProgress struct {
	TasksTotal        int `json:"tasks_total"`
	TasksClosed       int `json:"tasks_closed"`
	DeliverablesTotal int `json:"deliverables_total"`
	DeliverablesLive  int `json:"deliverables_live"`
}

// MilestoneListResponse is GET /api/v1/projects/{id}/milestones.
type MilestoneListResponse struct {
	Milestones []Milestone `json:"milestones"`
}

// MilestoneDetail is GET /api/v1/milestones/{id}: the milestone plus the
// children the progress was derived from.
type MilestoneDetail struct {
	Milestone
	Tasks        []Task        `json:"tasks"`
	Deliverables []Deliverable `json:"deliverables"`
}

// CreateMilestoneInput is POST /api/v1/projects/{id}/milestones. Position 0
// means append after the project's last milestone.
type CreateMilestoneInput struct {
	Title    string `json:"title"`
	Position int    `json:"position,omitempty"`
}
```

New `internal/store/milestone_progress.go`, following the
`internal/store/approval_rules.go` precedent (pure functions in the store
package, no `*sql` anywhere):

```go
// ComputeMilestoneProgress derives 029 §2's milestone progress from the
// children's states. taskStates are the milestone's tasks' State values;
// deliverableStates are its deliverables' ReportedState values ("" when
// nothing has reported). Closed means deliveredStateSet; live means
// published or updated — the buckets are pinned in the milestones plan and
// applied nowhere else.
func ComputeMilestoneProgress(taskStates, deliverableStates []string) model.MilestoneProgress
```

First test, `internal/store/milestone_progress_test.go` (no `OpenTestStore`,
no database — it must run green with Postgres down):

```go
func TestComputeMilestoneProgress(t *testing.T) {
	for _, tt := range []struct {
		name  string
		tasks, dels []string
		want  model.MilestoneProgress
	}{
		{"empty milestone", nil, nil, model.MilestoneProgress{}},
		{"open tasks only",
			[]string{"draft", "ready", "in_progress", "in_review"}, nil,
			model.MilestoneProgress{TasksTotal: 4}},
		{"every delivered state counts closed, abandoned included",
			[]string{"merged", "deployed_dev", "deployed_prod", "released", "abandoned", "ready"}, nil,
			model.MilestoneProgress{TasksTotal: 6, TasksClosed: 5}},
		{"published and updated are live; the rest are not",
			nil, []string{"published", "updated", "deprecated", "removed", "failed", ""},
			model.MilestoneProgress{DeliverablesTotal: 6, DeliverablesLive: 2}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ComputeMilestoneProgress(tt.tasks, tt.dels); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
```

Implement closed membership by reading `deliveredStateSet` directly — the
compile-time set and the derivation cannot drift. `internal/model/rule_test.go`
and `deps_test.go` must stay green (stdlib imports only in `model`).

- [ ] Red: the table test fails to compile, then passes without Postgres.
- [ ] `go test -trimpath ./internal/model ./internal/store -run 'TestComputeMilestoneProgress|TestRule'` — green.
- [ ] Commit: `Add milestone model shapes and the pure progress derivation`.

### Task 3 — Store readers: ListMilestones and GetMilestone

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1, 2]
```

New `internal/store/milestones.go` + `milestones_test.go`. Two readers, one
per question, both computing progress through Task 2's function and nothing
else:

```go
// ListMilestones returns a project's milestones ordered by position then id,
// each with derived progress. An unknown project yields an empty slice —
// callers that need the project to exist load it first, as ListDeliverables'
// callers do.
func (s *Store) ListMilestones(ctx context.Context, projectID string) ([]model.Milestone, error)

// GetMilestone returns one milestone with its tasks, its deliverables, and
// the progress derived from exactly those children. ErrNotFound when the id
// names no milestone.
func (s *Store) GetMilestone(ctx context.Context, id string) (*model.MilestoneDetail, error)
```

Implementation notes:

- `ListMilestones` loads the milestone rows, then the child facts in two
  bulk queries — task `(milestone_id, state)` pairs for the project, and the
  project's deliverables via the existing `deliverableSelect` +
  `deliverableFrom` projection (the reported-state join already exists
  there; do not write a second evidence join) — groups them in Go, and calls
  `ComputeMilestoneProgress` per milestone.
- `GetMilestone` loads the milestone row, its tasks through the same column
  set and scan the task list readers use (`internal/store/tasks.go`), and
  its deliverables with `WHERE milestone_id = $1` over the shared
  projection. `Deliverable.Milestone` scanning arrives in Task 7; here the
  filter column is enough.

Tests are in-package, so seed `milestones` and the two `milestone_id`
columns with raw `ExecContext` INSERTs — the writers arrive in Tasks 4/6/7,
and store tests are the one place direct writes are legitimate. First test:

```go
func TestListMilestonesProgress(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	// Seed project p1 (key "P1"); milestones P1-MILE-1 (position 1),
	// P1-MILE-2 (position 2). Tasks: two on MILE-1 (one "merged", one
	// "ready"), none on MILE-2, one project task with milestone_id NULL.
	// Deliverables: one on MILE-1 with artifact evidence state "published"
	// (seed artifact_declarations + artifact_evidence the way
	// deliverables_test.go does), one on MILE-1 unreported.

	got, err := s.ListMilestones(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "P1-MILE-1" || got[1].ID != "P1-MILE-2" {
		t.Fatalf("order wrong: %+v", got)
	}
	want := model.MilestoneProgress{TasksTotal: 2, TasksClosed: 1,
		DeliverablesTotal: 2, DeliverablesLive: 1}
	if got[0].Progress != want {
		t.Fatalf("MILE-1 progress = %+v, want %+v", got[0].Progress, want)
	}
	if got[1].Progress != (model.MilestoneProgress{}) {
		t.Fatalf("empty milestone must derive zero progress: %+v", got[1].Progress)
	}
}
```

Add `TestGetMilestone` (children listed, unattached task excluded,
`ErrNotFound` for an unknown id) and `TestListMilestonesEmpty` (project with
none → empty slice, not nil error).

- [ ] `go test -trimpath ./internal/store -run 'TestListMilestones|TestGetMilestone'`
      with Postgres up — green.
- [ ] Commit: `Add milestone store readers with derived progress`.

### Task 4 — Create-milestone mutation across store, API, and CLI

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1, 2]
```

One mutation, every surface at once: store write + `milestone.created` event
+ metric + API route + CLI verb. (No web form — the cockpit stays
read-mostly here; P4's promotion transaction is the bulk creator.)

**Store** (`internal/store/milestones.go`), mirroring `CreateDeliverable`
(`internal/store/deliverables.go:81`) line for line where it can:

```go
// milestoneSeqKind is the milestone's row key in project_entity_seq and the
// type segment of its id (spec 029 §4's COW-MILE-2).
const milestoneSeqKind = "MILE"

// CreateMilestone allocates the next <KEY>-MILE-<n> id from the project's
// MILE counter and inserts the milestone inside the given transaction.
// position 0 appends after the project's current last position. A blank
// title is ErrInvalidInput and an unknown project ErrNotFound, both checked
// before the id is allocated so a rejected input never burns an ordinal.
func CreateMilestone(tx *sql.Tx, now time.Time, projectID, title string, position int, createdBy string) (*model.Milestone, error)
```

Validation: title trimmed, non-empty, ≤ 200 runes (the deliverable-name
bound). The counter upsert is the exact `project_entity_seq` statement
`CreateDeliverable` uses with `milestoneSeqKind`; position 0 resolves to
`COALESCE(MAX(position), 0) + 1` over the project's milestones, inside the
same transaction. First test, `internal/store/milestones_test.go`:

```go
func TestCreateMilestone(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	// Seed project p1 with key "P1".

	create := func(title string, pos int) (*model.Milestone, error) {
		var m *model.Milestone
		_, _, err := s.RecordEvent(ctx, "test", newExternalID(t), "milestone.created", nil,
			func(tx *sql.Tx, _ int64) error {
				var err error
				m, err = CreateMilestone(tx, s.Now(), "p1", title, pos, "ada")
				return err
			})
		return m, err
	}

	m1, err := create("Internal review", 0)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := create("Publication", 0)
	if err != nil {
		t.Fatal(err)
	}
	if m1.ID != "P1-MILE-1" || m2.ID != "P1-MILE-2" {
		t.Fatalf("ordinals: %s, %s", m1.ID, m2.ID)
	}
	if m1.Position != 1 || m2.Position != 2 {
		t.Fatalf("append positions: %d, %d", m1.Position, m2.Position)
	}
	if _, err := create("   ", 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("blank title: got %v", err)
	}
	if _, err := create("x", 0); err != nil {
		t.Fatal(err)
	} // ordinal 3: the blank rejection burned nothing
}
```

**API**: new `internal/api/milestones.go` with a `recordMilestone` helper
following `recordDeliverable` (`internal/api/deliverables.go:98`) — event
type `milestone.created`, payload per Global constraints — and a
`POST /api/v1/projects/{id}/milestones` handler: load the project first
(404 via `mapStoreErr`), decode `model.CreateMilestoneInput`, respond 201
with the created `model.Milestone`. `routeGuards` entry
`guarded(permMilestoneWrite)`; `permMilestoneRead`/`permMilestoneWrite`
into `authz.go`'s constants and `grants` per Global constraints.

**Metric**: add `worklode_milestone_changes_total` to
`internal/api/metrics.go` (nil-safe, per spec 022); observe
`action="create"` with the `formOutcome`-style outcome mapping. Extend the
metrics tests.

**CLI**: `internal/cli/client.go` gains
`(c *Client) CreateMilestone(ctx context.Context, project string, in model.CreateMilestoneInput) (model.Milestone, []byte, error)`.
New `internal/cmd/milestone.go`: a `milestone` command group with
`lode milestone add <title> [--position N]`, project-scoped the way the
other project-scoped verbs are (`scopeFlags`, see `newTaskAddCmd`). The
confirmation line renders through `internal/cli` (`cli.LocalTime` for any
time; no hand formatting in `internal/cmd`). Cover with a cmd test against
the stub-server pattern the other cmd tests use, and follow
`docs/agent-surfaces.md` for the new verb.

- [ ] Store test red → green; handler, cmd, and metrics tests.
- [ ] `go test ./...` — green (includes `TestAgentSurfaces`).
- [ ] Commit: `Create milestones across store, API and CLI`.

### Task 5 — The Milestones page (the tracer)

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
  - worklode-cockpit-ui
blockedBy: [3, 4]
```

`GET /projects/{id}/milestones` joins Tasks 1–4 over real data: every
milestone as a section — title, derived progress, its tasks, its
deliverables — in position order. One page carries list and detail; a
research project holds a handful of milestones (P4 mints two), so a
per-milestone page would be chrome without a reader.

- `internal/ui/views.go`:

```go
// MilestonesView is the project-local Milestones destination (spec 029 §2).
// An empty Milestones slice renders an honest empty state, never a
// fabricated row.
type MilestonesView struct {
	Page         PageProps
	CanonicalURL string
	Project      CockpitProject
	Milestones   []MilestoneSection
}

// MilestoneSection is one milestone with the children its progress was
// derived from. The counts echo model.MilestoneProgress; the view repeats
// the numbers, never the derivation.
type MilestoneSection struct {
	ID                string
	Title             string
	TasksTotal        int
	TasksClosed       int
	DeliverablesTotal int
	DeliverablesLive  int
	Tasks             []MilestoneTaskRow
	Deliverables      []DeliverableRow
}

type MilestoneTaskRow struct {
	ID       string
	Title    string
	State    string
	Assignee string
}
```

- `internal/ui/milestones.templ` (new): `ui.Milestones(MilestonesView)`
  following `deliverables.templ`'s project-page skeleton — `projectShell`,
  active section `"milestones"`. Each section: heading with the milestone id
  and title, a progress line ("2/3 tasks closed · 1/2 deliverables live" —
  plain counts, no invented percentage bar), the task rows linking to
  `/tasks/{id}` the way other pages do, and the deliverable rows reusing the
  existing chip helpers. Empty state: "No milestones yet". A milestone with
  no children says so instead of rendering empty tables. Tables that can
  grow wide scroll inside their own labelled container (032 §10).
- `internal/ui/layout.templ`: add
  `@navLink(..., "Milestones", "milestones", active)` to `localNav` between
  Crew and the Work anchor — milestones contain both work and deliverables,
  so they sit ahead of both lists.
- `internal/api`: handler in `internal/api/milestones.go` —
  `projectHeader` for the shell, `ListMilestones` + `GetMilestone`-free
  composition (the list reader already carries progress; load the child rows
  with the same two bulk queries the reader exposes — if that means a small
  exported `ListMilestoneChildren` helper is cleaner than N× `GetMilestone`,
  do that in `internal/store/milestones.go` and test it there). Unknown
  project 404s. Register the route ahead of the `{section}` wildcard the way
  crew is, with `routeGuards` entry
  `"GET /projects/{id}/milestones": guarded(permWebRead)`.
- Tests, `internal/api` web tests: `TestMilestonesPage` — empty state; a
  seeded page (via `RecordEvent` + `CreateMilestone`, tasks through the
  task-create path with raw `milestone_id` update or Task 6's writer once it
  lands — assert milestone titles, the progress counts, and section order);
  404 for an unknown project. Keep the nav-marker invariants
  (`internal/api/web_test.go`) passing — the local nav grows to ten
  destinations, so update the counted assertions in the same commit.
- `e2e/cockpit_test.go`: if the placeholder expectations enumerate
  project-local destinations, `milestones` was never among them (it 404'd
  until now); add a 200-with-empty-state assertion beside the crew one.

- [ ] `go generate ./...`; commit the generated files.
- [ ] `go test -trimpath ./internal/api -run 'TestMilestonesPage|TestWeb'` — green;
      `go test ./...` — green.
- [ ] Commit: `Add the project Milestones page`.

### Task 6 — Attach a task to a milestone, every surface

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
  - worklode-obsidian-mirror
blockedBy: [4]
```

The task side of containment, riding the existing edit path end to end:
`tasks.milestone_id` through `PATCH /api/v1/tasks/{id}` and
`lode task edit --milestone`.

- `internal/model/task.go`: `Task` gains
  `Milestone string \`json:"milestone,omitempty"\``;
  `EditTaskInput` gains `Milestone *string \`json:"milestone,omitempty"\``.
- `internal/store/tasks.go`: the task column list and scan gain
  `milestone_id` (NULL ↔ `""`). `UpdateTaskFields`
  (`internal/store/tasks.go:389`) gains a `milestone *string` parameter:
  `""` (or `"none"`, normalized by the API like `concern`) clears; a
  non-empty value must name an existing milestone **in the task's own
  project** — load both project ids inside the tx and refuse a mismatch
  with `ErrInvalidInput`, message shaped like `checkHierarchy`'s
  (`cross-project milestone %s (%s) for task %s (%s)`); an unknown
  milestone is also `ErrInvalidInput` (it is a bad reference in the body,
  not a missing URL target). Update every `UpdateTaskFields` caller
  (`grep -rn UpdateTaskFields internal/`).
- `internal/api/tasks.go` `patchTask`: thread `req.Milestone` through
  (normalize `"none"` → `""`), include `"milestone"` in the `LogChange`
  field loop, and observe `worklode_milestone_changes_total` with
  `action="task_attach"` when the field is present. The existing
  `task.updated` event carries it — no new event type.
- **CLI**: `lode task edit <id> --milestone <id|none>`
  (`internal/cmd/task.go`, `newTaskEditCmd` — a `Changed`-guarded string
  flag exactly like `--concern`); extend the "nothing to edit" list.
- **Obsidian mirror**: `plugins/obsidian/src/api/types.ts`'s `Task`
  interface hand-mirrors `model.Task` — add `milestone?: string;` per the
  `worklode-obsidian-mirror` skill.
- Render: `cli.TaskDetailRender` (`internal/cli/render.go`) prints a
  `Milestone:` line when set; `internal/ui/task.templ`'s detail shows it
  linking to the project's Milestones page.

First test, in `internal/store` (extend the tasks test file the existing
`UpdateTaskFields` tests live in):

```go
func TestUpdateTaskMilestone(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	// Seed projects p1, p2; milestone P1-MILE-1 in p1 (CreateMilestone via
	// RecordEvent); tasks t1 in p1 and t2 in p2.

	set := func(task, milestone string) error {
		_, _, err := s.RecordEvent(ctx, "test", newExternalID(t), "task.updated", nil,
			func(tx *sql.Tx, _ int64) error {
				return UpdateTaskFields(tx, s.Now(), task, nil, nil, nil, nil, nil, nil, nil, &milestone)
			})
		return err
	}

	if err := set(t1, "P1-MILE-1"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetTask(ctx, t1)
	if got.Milestone != "P1-MILE-1" {
		t.Fatalf("milestone not stored: %+v", got)
	}
	// 029 §5: containment never crosses a project boundary.
	if err := set(t2, "P1-MILE-1"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cross-project attach: got %v", err)
	}
	if err := set(t1, "P1-MILE-9"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown milestone: got %v", err)
	}
	if err := set(t1, ""); err != nil {
		t.Fatal(err)
	} // detach is always legal (029 §2)
}
```

(Adjust the `UpdateTaskFields` argument list to the real signature when
adding the parameter.) Handler test: PATCH with `{"milestone": ...}` — 200
sets it, cross-project 422, `"none"` clears; cmd test for the flag.

- [ ] Store test red → green; handler, cmd tests; metrics test extended.
- [ ] `pnpm` build for the Obsidian mirror if its CI job would run
      (`worklode-obsidian-mirror` skill has the loop).
- [ ] `go test ./...` — green.
- [ ] Commit: `Attach tasks to milestones through the edit path`.

### Task 7 — Reparent deliverables and group the Deliverables page

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
  - worklode-cockpit-ui
blockedBy: [3, 4, 5]
```

The deliverable side: milestone at declaration, a reparent mutation for the
deliverables that already exist, and the project Deliverables page grouped
by milestone.

**Model**: `model.Deliverable` gains
`Milestone string \`json:"milestone,omitempty"\``;
`model.CreateDeliverableInput` gains `Milestone string \`json:"milestone,omitempty"\``.
New request body `model.EditDeliverableInput` with exactly
`Milestone *string \`json:"milestone"\`` — the three descriptive fields stay
immutable in P1, so the PATCH accepts one field and says so.

**Store** (`internal/store/deliverables.go`):

- `DeliverableInput` gains `MilestoneID string`; `CreateDeliverable`
  validates it like Task 6 (same project, exists, `ErrInvalidInput`
  otherwise — checked before the ordinal is allocated) and inserts it.
- `deliverableColumns`/`scanDeliverable` gain `milestone_id`.
- New mutation:

```go
// SetDeliverableMilestone reparents one deliverable ("" detaches) inside
// the given transaction. Same-project containment per 029 §5; bumps
// updated_at. Callers reach it through RecordEvent with event type
// "deliverable.updated".
func SetDeliverableMilestone(tx *sql.Tx, now time.Time, id, milestoneID string) error
```

Unknown deliverable → `ErrNotFound`; unknown or cross-project milestone →
`ErrInvalidInput` with the Task 6 message shape. First test mirrors
`TestUpdateTaskMilestone` for the deliverable (attach, cross-project
refusal, detach, `updated_at` bumped).

**API**:

- `POST /api/v1/projects/{id}/deliverables` passes `Milestone` through
  `validateDeliverable` (existence/containment stays a store check; the
  validator only trims).
- New `PATCH /api/v1/deliverables/{id}` in `internal/api/deliverables.go`:
  decode `model.EditDeliverableInput`, refuse an empty body ("no fields to
  update"), `RecordEvent` source `"cli"`, type `deliverable.updated`,
  payload per Global constraints; respond with the updated deliverable.
  `routeGuards` entry `guarded(permDeliverableWrite)`. Observe
  `worklode_milestone_changes_total{action="deliverable_attach"}`.

**CLI** (`internal/cmd/milestone.go`): `lode milestone attach <milestone>
<deliverable>` and `lode milestone detach <deliverable>`, both calling a new
`(c *Client) SetDeliverableMilestone(ctx, deliverable, milestone string)`
against the PATCH route. A task id passed as `<deliverable>` gets an error
pointing at `lode task edit --milestone` — `typedID`'s grammar
(`internal/cmd/show.go:20`) already distinguishes the shapes.

**Web**:

- `deliverablesPage` (`internal/api/webform.go:302`) also loads
  `ListMilestones` and groups: `ui.DeliverablesView.Deliverables` becomes
  `Groups []DeliverableGroup{MilestoneID, MilestoneTitle string; Rows
  []DeliverableRow}` — one group per milestone in position order (only
  milestones that have deliverables), then an unattached group last. With no
  milestones in the project, the single unattached group renders headerless,
  exactly today's flat page. An unattached deliverable always shows —
  `milestone_id` is nullable and the page must never hide a declared row.
- The declare form (`GET /projects/{id}/deliverables/new`) gains an
  optional milestone `<select>` over the project's milestones, default "No
  milestone"; the POST handler passes it into `DeliverableInput`. Event
  source stays `"web"`.
- Update `deliverables.templ`, `views.go`, and the existing deliverables
  web tests for the grouped shape; add a grouping assertion (two
  milestones + one unattached row → three groups in order).

- [ ] Store tests red → green; handler, cmd, form tests; metrics test.
- [ ] `go generate ./...`; commit generated files.
- [ ] `go test ./...` — green.
- [ ] Commit: `Reparent deliverables and group the Deliverables page by milestone`.

### Task 8 — Milestone read API and CLI listing

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [3, 4]
```

The read fan-out over Task 3's readers.

**API** (`internal/api/milestones.go`):

- `GET /api/v1/projects/{id}/milestones` → `model.MilestoneListResponse`;
  empty list for a project with none, 404 for an unknown project (load the
  project first). `routeGuards`: `guarded(permMilestoneRead)`.
- `GET /api/v1/milestones/{id}` → `model.MilestoneDetail`; 404 via
  `mapStoreErr`. `routeGuards`: `guarded(permMilestoneRead)`.

**CLI**:

- `internal/cli/client.go`:
  `ListMilestones(ctx, project string) (model.MilestoneListResponse, []byte, error)`
  and `GetMilestone(ctx, id string) (model.MilestoneDetail, []byte, error)`.
- `internal/cli/render.go`: `MilestoneTable(w io.Writer, ms []model.Milestone)`
  — columns `ID`, `POS`, `TITLE`, `TASKS` (`closed/total`), `DELIVERABLES`
  (`live/total`) — and `MilestoneRender(w io.Writer, d model.MilestoneDetail)`
  — the header fields via `cli.LocalTime`, then the progress line, then the
  task rows (reuse `TaskTable`) and deliverable rows.
- `internal/cmd/milestone.go`: `lode milestone list` (project-scoped,
  `--json` or `MilestoneTable`) beside Task 4's `add`. Cmd tests against
  the stub server; `docs/agent-surfaces.md` updated for the finished verb
  set (`add`, `list`, `attach`, `detach`).

Handler test: seed two milestones with children through the Task 4/6/7
writers, assert the list JSON carries derived progress and position order,
and the detail JSON carries the children; 404 cases for both routes.

- [ ] `go test -trimpath ./internal/api ./internal/cli ./internal/cmd` — green.
- [ ] Commit: `List and get milestones over the API and CLI`.

### Task 9 — lode show renders milestones

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [8]
```

`lode show WL-MILE-2` and `lode show --milestone 2` stop erroring
(029 §4: the type segment is what `lode show` dispatches on). All in
`internal/cmd/show.go` and its tests — the pinned error strings at
`internal/cmd/show_test.go:640-656` and the flag-shape tests near `:952-956`
change in this same task.

- `classify`: move `"MILE"` out of the `targetUnshowable` arm into a new
  `targetMilestone` kind. `PLAN` and `DEL` stay unshowable — P2 owns the
  plan cutover, and a deliverable still renders on the surfaces its reason
  string names.
- `unshowableKindWords` and `unshowableReason` drop their milestone
  entries; `notYetAnEntity` stays (the plan entry still uses it). Update
  the comment above `unshowableReason` — it currently says milestones do
  not exist.
- `dispatchShowPositional`: `targetMilestone` → `runMilestoneShow(cmd, arg)`
  — fetch via `Client.GetMilestone`, render via `cli.MilestoneRender`
  (`--json` prints the raw body, matching the other show arms).
- `dispatchShowKind`: the `"milestone"` case leaves the unshowable arm and
  builds the full id from the configured project key exactly as
  `runDocShowByOrdinal` does (`cfg.ProjectKey` + `-MILE-` + ordinal); with
  no key configured, error: `no project key configured; pass the full id
  (e.g. WL-MILE-2) positionally`. `showOrdinalShape` already admits the
  bare ordinal — unchanged.
- Flag help: `--milestone` drops "not showable yet"; the `Long` text's
  flag list stays truthful.
- Tests: the pinned `WL-MILE-2` error test becomes a success-path test
  against the stub server (assert the rendered title and progress line);
  the `--milestone` bare-ordinal shape errors (`:952-956` pattern) keep
  their strings; add the no-project-key error case. `TestAgentSurfaces`
  and `docs/agent-surfaces.md` cover the changed surface.

- [ ] `go test -trimpath ./internal/cmd -run 'TestShow|TestAgentSurfaces'` — green.
- [ ] `go test ./...` — green.
- [ ] Commit: `Render milestones from lode show`.

### Task 10 — e2e milestone journey and follow-ups alignment

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [5, 6, 7, 9]
```

`e2e/milestones_test.go` (build tag `e2e`), public surfaces only:

1. Bootstrap-token API: create project A (and project B for the refusal
   step); `POST /api/v1/projects/A/milestones` twice — ids `…-MILE-1`,
   `…-MILE-2`, positions 1 and 2.
2. Create two tasks in A; `PATCH /api/v1/tasks/{id}` attaches one to
   MILE-1. Attaching A's other task to a B milestone — 422, body names the
   cross-project refusal.
3. Declare one deliverable in A with `"milestone"` set at creation;
   declare a second without, then `PATCH /api/v1/deliverables/{id}` —
   reparented onto MILE-1.
4. `GET /api/v1/projects/A/milestones` — MILE-1 shows
   `tasks_total: 1, deliverables_total: 2`; MILE-2 all zeros.
   `GET /api/v1/milestones/{MILE-1}` — the children are listed.
5. Web: `GET /projects/A/milestones` — 200, both milestone titles and the
   progress counts render; `GET /projects/A/deliverables` — grouped, the
   MILE-1 header present, and after detaching one deliverable
   (PATCH `"milestone": ""`) it reappears under the unattached group —
   nullable means never hidden.
6. Drive the task with no milestone through the ordinary task API — it
   stays legal everywhere; nothing demands attachment.

Then align the standing records:

- `docs/follow-ups.md:179` (`project_entity_seq` carries only `DEL`):
  rewrite for the remaining truth — the CHECK now admits `MILE`; SPEC, ADR
  and PLAN arrive with plan `2026-08-25-research-work-2-identifiers-and-
  references`.
- `docs/follow-ups.md` WL-238 entry ("eight destinations … renders nine"):
  update the count — Milestones is a further addition to 032 §2's list, so
  the owed amendment now covers two extra destinations. Do not file new
  entries for milestone rename/reorder/delete or the P4 template — the
  Coverage notes and `covers:` block already carry those claims where a
  coverage query reads them.

- [ ] `go test -race -count=1 -tags e2e ./e2e/ -run TestMilestone` — green;
      full `-tags e2e ./e2e/` still green.
- [ ] `go test ./...` — green.
- [ ] Commit: `Prove the milestone journey end to end`.

## Deferred to later parts

Named here so nobody reads silence as an oversight; each is claimed by the
`covers:`/`fullCoverageWith:` contract above, not by a follow-ups entry:

- **The default two-milestone / five-deliverable template** minted at
  promotion for `kind=sunstone-story` projects, and its configuration
  snapshot — P4 (`2026-08-25-research-work-3-intake-and-promotion`), which
  owns the promotion transaction that calls Task 4's `CreateMilestone`.
- **Deliverable state beyond what `internal/hooks/catalog.go` already
  files** — by-label identity, the poll prober, the `user_reported` write
  path — P6 (`2026-08-25-research-work-4-deliverable-state`).
- **SPEC/ADR/PLAN counters, the 0037 CHECK replacement, and the
  `lode show --plan` cutover** — P2
  (`2026-08-25-research-work-2-identifiers-and-references`), together with
  the typed edge table that lets a milestone reference another project's
  deliverable (029 §5) — containment stayed same-project here by
  construction.
