---
status: draft
covers:
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-6.1
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-25-research-work-6-crew-lifecycle.md
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-6.2
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-25-research-work-6-crew-lifecycle.md
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-8.2
    coverage: none
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-8.4
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-25-research-work-7-chat-crew-spaces.md
  - spec: docs/specs/032-project-cockpit.md#sec-6
    coverage: partial
  - spec: docs/specs/032-project-cockpit.md#sec-10
    coverage: none
  - spec: docs/specs/032-project-cockpit.md#sec-11
    coverage: none
isRequiredBy:
  - docs/plans/2026-08-14-approvals-1-table-and-web-act.md
  - docs/plans/2026-08-14-home-project-list.md
---

# Project Crew: participants and lead

Plan B of the four-plan series from the 2026-08-14 design brief (Home as
project list + cockpit frame unification). It builds 029 §6.1's stored
participants — the Crew — and 029 §6.2's identity-claim capture at login:
a `project_participants` table with at most one lead per project, the full
Keycloak `groups` claim and `email` stored on `actors`, store readers and
event-emitting mutations, and three surfaces: the participants JSON API,
`lode project crew` CLI verbs, and `/projects/{id}/crew` replacing its
placeholder.

**Series.** This plan blocks `docs/plans/2026-08-14-home-project-list.md`
(plan D): D's Home cards consume `ProjectsForActor` (roles, lead flag) built
here, and plan C (approvals) consumes the stored `groups` claim to resolve
role-based approvals. B is independent of plans A and C and can execute in
parallel with them. That ordering is this document-level note — never a task
number across files.

## Coverage notes

Why each `partial` above is partial — each remainder is a deferred item
outside this whole series, so no `fullCoverageWith` sibling exists to name:

- **029 §6.1 / 032 §6** — deferred: invited participants with no Keycloak
  actor, the lead-handoff acceptance ceremony, and the derived *contributors*
  surface. For 032 §6 additionally: no invited-expert display and no
  "Worklode, on behalf of _User_" activity labelling. Consequences of the
  deferred ceremony: adding a second lead is refused, and the current lead
  cannot be removed.
- **029 §6.2** — stores the full `groups` claim and `email` at login; does
  not upgrade migration 0014's `expected_github_login` into a token-mapped
  `githubUsername`.
- **029 §8.4** — the event-emission half only: this plan emits
  `crew.member_added` / `crew.member_removed` from every mutating call site.
  The `gchat-crew-space` subscriber itself is a future plan.
- **029 §8.2** (`none`) — binding constraint: no producing handler gains a
  hardcoded notifier. Crew mutations emit events and nothing else.
- **032 §10 / §11** (`none`) — standing rules that govern the crew page and
  the e2e task without being implemented here.

Removal is member-level only: dropping a single role label is remove +
re-add. That is a design decision, recorded in Task 7 rather than as a gap.

One thing the coverage claims above cannot express, so Task 9 records it in
`docs/follow-ups.md`: 029 §6.1's "any Crew member may add or remove an
ordinary Crew member" cannot be enforced until authz decisions are
project-scoped, so the new `crew.write` permission is granted to all users —
wider than the spec wants. A conflict between spec and system, not a
planned partial.

## Global constraints

- **Event spellings are pinned:** exactly `crew.member_added` and
  `crew.member_removed`. Verified against 029 §8.4: "Emitting these two event
  types is Crew's job, not this subscriber's" — every call site that mutates
  Crew writes them through `Store.RecordEvent` (`internal/store/events.go`),
  the same `events` path as everything else in 004. A future Google Chat plan
  depends on these two strings; do not vary them. Event `source` is `"cli"`
  on the JSON-API path and `"web"` on the cockpit-form path, matching
  `internal/api/assign.go` and `internal/api/webform.go`. Event payload for
  both types: `{"project": ..., "actor": ..., "roles": [...], "lead": bool,
  "by": ...}` where `by` is the acting actor id (`""` on an open instance).
- **Metrics:** one new counter in `internal/api/metrics.go` (nil-safe struct,
  `prometheus.Registerer` threaded from `serve.go`, per spec 022):
  `worklode_crew_changes_total{surface, action, outcome}` with
  `surface ∈ {api, web}`, `action ∈ {add, remove}`, `outcome` from the
  existing `formOutcome`-style mapping (`ok`, `rejected`, `error`). Bounded
  labels only — never a project, actor, or role value. Web forms additionally
  call `observeFormSubmission` with `form ∈ {crew_add, crew_remove}`.
- **Authz:** the new permission is added to `routeGuards`
  (`internal/api/router.go`) and the `grants` table (`internal/api/authz.go`)
  only — never a check inside a handler.
- **Migrations** are new numbered `.up.sql`/`.down.sql` pairs (next free
  numbers — 0020/0021 at time of writing; `./scripts/check-migrations.sh`
  renumbers on collision), listed in `deploy/base/kustomization.yaml`. Never
  edit a shipped migration. Store tests build their template database from
  the migrations directory automatically (`internal/store/testhelpers.go`).
- **Store tests need Postgres with pgvector** (default DSN in CLAUDE.md,
  override `TEST_POSTGRES_DSN`). A skipped store test proved nothing — run
  with Postgres up.
- **`e2e/` drives public surfaces only** (HTTP API, web pages) — never a
  direct store write.
- **The templ/Tailwind toolchain is fixed** by 032 §12, already covered in
  full by `docs/plans/2026-08-10-cockpit-templ-htmx-tailwind.md`: `.templ`
  sources in `internal/ui`, `go generate ./...` regenerates the committed
  `*_templ.go` and `internal/ui/assets/app.css`; commit the generated files.
  `internal/api` imports `internal/ui`, never the reverse.
- **Every task leaves `go test ./...` green** and ends with a commit. Route
  or placeholder moves update their test assertions in the same task.
- All tasks are specified for a Sonnet-tier implementer per
  `MODEL_SELECTION.md`; none carries a known open design decision. Escalate
  on the first sign the plan does not match the code.

## Tasks

### Task 1 — Migrations: actor identity claims and project_participants

```yaml
kind: chore
priority: high
skills:
  - golang-migrate:migration
blockedBy: [ ]
```

Two new migration pairs in `deploy/base/migrations/`, both listed in
`deploy/base/kustomization.yaml` (resources list, after the 0019 entries).

`0020_actor_identity_claims.up.sql`:

```sql
-- Keycloak identity claims recorded in full at login (spec 029 §6.2).
-- groups is the raw groups claim as a JSON array; email is the email claim.
-- NULL means the actor has not logged in since these columns shipped.
ALTER TABLE actors ADD COLUMN groups jsonb;
ALTER TABLE actors ADD COLUMN email text;
```

`0020_actor_identity_claims.down.sql` drops the two columns in reverse order.

`0021_project_participants.up.sql`:

```sql
-- Project Crew (spec 029 §6.1): role-labelled participant rows, visible
-- before any task is picked up. One actor may hold several role labels
-- (one row each); at most one row per project carries is_lead.
CREATE TABLE project_participants (
    project_id text        NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    actor_id   text        NOT NULL REFERENCES actors (id)   ON DELETE RESTRICT,
    role       text        NOT NULL,
    is_lead    boolean     NOT NULL DEFAULT false,
    added_at   timestamptz NOT NULL,
    added_by   text        REFERENCES actors (id) ON DELETE RESTRICT,
    PRIMARY KEY (project_id, actor_id, role)
);

CREATE UNIQUE INDEX project_participants_one_lead
    ON project_participants (project_id) WHERE is_lead;
```

`0021_project_participants.down.sql` drops the table (the index goes with it).

`added_by` is nullable because an instance with no login provider has an
anonymous subject (`authOpen`) with no actor id.

- [ ] Write the four files and the two kustomization entries.
- [ ] `./scripts/check-migrations.sh --no-fix` — exits 0.
- [ ] Round-trip up → down → up against a scratch database (the
      `golang-migrate:test-roundtrip` skill, or manually with the compose
      Postgres) — both migrations reverse cleanly.
- [ ] `go test ./internal/store -run TestStore -count=1` with Postgres up —
      existing tests still pass against the extended schema
      (`ok  github.com/sunstoneinstitute/worklode/internal/store`).
- [ ] Commit: `Add actor identity-claim columns and project_participants`.

### Task 2 — Store the groups claim and email at login

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

029 §6.2: the groups claim is stored on the actor **in full** at login (not
filtered to `user`/`admin`), and the `email` claim beside it. One code path —
`provisionActor` (`internal/api/oidcauth.go`) is shared by the CLI token
exchange and the web callback, so this is a single change site.

- `internal/oidc/oidc.go`: add `Email string \`json:"email"\`` to `Claims`.
- `internal/store/actors.go`: add `Email string` and `Groups []string` to
  `Actor`. Extend `UpsertHumanActor` to
  `UpsertHumanActor(ctx, id, displayName string, admin bool, expectedGitHubLogin, email string, groups []string)`;
  marshal `groups` to jsonb the way `SetProjectFocus` handles
  `projects.focus` (`internal/store/projects.go`, `scanProjectFocus`), with
  nil marshalled as `[]`; store empty `email` as SQL NULL. Both columns are
  re-synced on every login, replacing the stored value in full. Extend
  `GetActor`'s SELECT and scan.
- `internal/api/oidcauth.go`: `provisionActor` passes `c.Email` and
  `c.Groups`.
- Update every other `UpsertHumanActor` caller and test fixture
  (`grep -rn UpsertHumanActor internal/`).

First test, in `internal/store/actors_test.go`:

```go
func TestUpsertHumanActorStoresIdentityClaims(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	groups := []string{"user", "science-lead"}
	err := s.UpsertHumanActor(ctx, "ada", "Ada", false, "adal", "ada@example.org", groups)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.GetActor(ctx, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if a.Email != "ada@example.org" || !slices.Equal(a.Groups, groups) {
		t.Fatalf("claims not stored: %+v", a)
	}

	// Re-login with narrower claims replaces the stored value in full —
	// Keycloak stays the sole authority (029 §6.2).
	err = s.UpsertHumanActor(ctx, "ada", "Ada", false, "adal", "", []string{"user"})
	if err != nil {
		t.Fatal(err)
	}
	a, err = s.GetActor(ctx, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if a.Email != "" || !slices.Equal(a.Groups, []string{"user"}) {
		t.Fatalf("claims not replaced: %+v", a)
	}
}
```

Also extend the existing provisioning test in
`internal/api/oidcauth_test.go`: give the test claims an `email` and a
multi-group `groups` value, assert `GetActor` returns both after the
exchange.

- [ ] Red: the store test fails to compile (new signature), then passes.
- [ ] `go test ./internal/store ./internal/api ./internal/oidc` — all green.
- [ ] Commit: `Store full groups claim and email on actors at login`.

### Task 3 — Store readers: ListParticipants and ProjectsForActor

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

New `internal/store/participants.go` + `participants_test.go`. Two readers,
UI-neutral, one per question:

```go
// Participant is one Crew member of a project, aggregated over their
// role-labelled rows (spec 029 §6.1).
type Participant struct {
	ActorID     string
	DisplayName string
	Roles       []string  // sorted
	IsLead      bool
	AddedAt     time.Time // earliest role row
}

// ActorProject is one project an actor participates in. Plan D's Home
// project list consumes this.
type ActorProject struct {
	Project Project
	Roles   []string // sorted
	IsLead  bool
}

func (s *Store) ListParticipants(ctx context.Context, projectID string) ([]Participant, error)
func (s *Store) ProjectsForActor(ctx context.Context, actorID string) ([]ActorProject, error)
```

`ListParticipants` first checks the project exists (`ErrNotFound` otherwise,
so the API 404s like every other project route), then joins `actors` for
`display_name` and aggregates per actor: lead first, then by `AddedAt`, then
actor id. `ProjectsForActor` returns one row per project ordered by project
id (deterministic; plan D re-sorts by its own tiers), with `Project` loaded
via the same column set `GetProject` uses. An actor on no projects returns an
empty slice, not an error.

Tests are in-package (`package store`), so seed `project_participants` with
raw `s.db.ExecContext` INSERTs — the exported writers arrive with the
mutation tasks, and store tests are the one place direct writes are
legitimate. First test:

```go
func TestProjectsForActor(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	// Seed: projects p1, p2; actors ada, bob (CreateProject / CreateActor).
	// Raw rows: ada leads p1 as "editor", is also "reporter" on p1,
	// and is a plain "member" of p2. bob is "member" of p1.
	seedParticipant(t, s, "p1", "ada", "editor", true)
	seedParticipant(t, s, "p1", "ada", "reporter", false)
	seedParticipant(t, s, "p2", "ada", "member", false)
	seedParticipant(t, s, "p1", "bob", "member", false)

	got, err := s.ProjectsForActor(ctx, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 projects, got %d", len(got))
	}
	if got[0].Project.ID != "p1" || !got[0].IsLead ||
		!slices.Equal(got[0].Roles, []string{"editor", "reporter"}) {
		t.Fatalf("p1 row wrong: %+v", got[0])
	}
	if got[1].Project.ID != "p2" || got[1].IsLead {
		t.Fatalf("p2 row wrong: %+v", got[1])
	}
}
```

Add the matching `TestListParticipants` (aggregation, ordering, empty roster,
unknown project → `ErrNotFound`) and the `seedParticipant` helper in the test
file.

- [ ] `go test ./internal/store -run 'TestProjectsForActor|TestListParticipants' -count=1`
      with Postgres up — green.
- [ ] Commit: `Add crew store readers ListParticipants and ProjectsForActor`.

### Task 4 — Store reader: OpenWorkOwnedBy, the removal guard's fact query

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

029 §6.1: before removal, every open task, decision and review the member
owns must be reassigned or explicitly left unassigned. Today the only ownable
fact in the schema is `tasks.assignee` (migration 0010); approvals arrive
with plan C and decisions later. One reader owns the question so the guard
(Task 7) and the responsibility listing (032 §6, same task) cannot disagree,
and future fact families extend this query rather than adding a second one.

In `internal/store/participants.go`:

```go
// OwnedWork is one open item a Crew member owns, blocking their removal
// (spec 029 §6.1). Kind is "task" today; approvals (spec 029 §7, plan C)
// and decisions join this query when their tables exist.
type OwnedWork struct {
	Kind  string // "task"
	ID    string
	Title string
	State string
}

func (s *Store) OpenWorkOwnedBy(ctx context.Context, projectID, actorID string) ([]OwnedWork, error)
```

Implement the query in an unexported function taking a
`QueryContext`-capable value (both `*sql.DB` and `*sql.Tx` satisfy it — add
a small local interface, or reuse one if `internal/store` already defines
it), so Task 7 can run the same query inside the mutation transaction.
"Open" means the task's state is not in `deliveredStateSet` and not
`abandoned` (`internal/store/tasks.go` — `abandoned` is already a member of
that set); build the excluded-state list from `deliveredStateSet` itself,
sorted, as quoted literals (they are compile-time constants), so the guard
and the state machine cannot drift. Filter `project_id = $1 AND assignee =
$2`, order by task id.

First test, in `internal/store/participants_test.go`:

```go
func TestOpenWorkOwnedBy(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	// Seed project p1, actor ada; three tasks in p1: one open assigned to
	// ada, one done assigned to ada, one open assigned to nobody.
	// Use CreateTask + the assign/lifecycle store paths, not raw SQL.

	got, err := s.OpenWorkOwnedBy(ctx, "p1", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "task" {
		t.Fatalf("want exactly the open assigned task, got %+v", got)
	}
}
```

- [ ] `go test ./internal/store -run TestOpenWorkOwnedBy -count=1` — green.
- [ ] Commit: `Add OpenWorkOwnedBy reader for the crew removal guard`.

### Task 5 — Crew web page replaces its placeholder (the tracer)

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

`/projects/{id}/crew` becomes a real read page joining Tasks 1–3 over real
data. Mutations (forms) arrive in Tasks 6–7; this task renders the roster
and the honest empty state.

- `internal/api/web.go`: **remove the `crew` entry from both
  `projectSections` and `projectSectionTitles`.**
- `internal/api/crew.go` (new): `crewPage` handler for
  `GET /projects/{id}/crew` — `projectHeader` (see
  `internal/api/webform.go`) for the shell identity, `ListParticipants` for
  the roster, map into the new view type. Unknown project 404s via the
  `ErrNotFound` path like every project route.
- `internal/api/router.go`: register the route beside the other project web
  routes and add `"GET /projects/{id}/crew": guarded(permWebRead)` to
  `routeGuards`. Go's mux prefers the literal `crew` segment over the
  `GET /projects/{id}/{section}` wildcard — mirror the existing comment
  pattern near the deliverables route.
- `internal/ui/views.go`: add

```go
type CrewView struct {
	Project CockpitProject
	Members []CrewMember
}

type CrewMember struct {
	ActorID     string
	DisplayName string
	Roles       []string
	IsLead      bool
}
```

- `internal/ui/crew.templ` (new): `ui.Crew(CrewView)`, following
  `deliverables.templ`'s project-page skeleton — project shell with the
  project sidebar, active section `"crew"` (the existing `localLink` in
  `layout.templ` already points at `/projects/{id}/crew`; the
  one-`aria-current` invariant and the `>Crew<` nav marker must keep
  holding). Roster: one row per member — display name (fall back to actor
  id), role labels, and a visually distinct **Lead** badge on the lead
  (032 §6). Empty roster renders "No Crew yet" with no fabricated record,
  form, or count.
- Tests, `internal/api/web_test.go`: delete `crew` from the placeholder
  table (~line 241) and its placeholder-specific assertions; add
  `TestCrewPage`: empty roster shows the empty state; a roster seeded
  through the store test helpers (raw seeding is not available here — use
  `CreateProject`/`CreateActor` plus a direct `RecordEvent` is not available
  either, so assert only the empty state and 404 in this task; the
  populated-roster assertion lands in Task 6 with the writer). Keep the
  seven-global/eight-project nav marker assertions passing unchanged.
- `e2e/cockpit_test.go` (~line 286): remove `/projects/proj/crew` from the
  placeholder expectations and assert instead that the page returns 200 with
  the empty-state text — the full journey is Task 9. (e2e runs in CI with
  the build tag, so this must land in the same commit as the page.)
- [ ] `go generate ./...` — regenerate `*_templ.go`; commit the generated
      files.
- [ ] `go test ./internal/api -run 'TestCrewPage|TestWeb' -count=1` — green;
      `go test ./...` — green.
- [ ] Commit: `Replace the crew placeholder with the roster page`.

### Task 6 — Add-member mutation, one task across every surface

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [5]
```

One mutation, landed on every surface at once so event provenance is never
half-wired: store write + `crew.member_added` event + metric + API route +
CLI verb + web form.

**Store** (`internal/store/participants.go`), the `AssignTask` pattern —
tx-scoped, provenance via `LogChange` with `entity_kind: "project"`:

```go
// AddParticipant adds one role-labelled Crew row inside the given ingest
// transaction (spec 029 §6.1). Callers reach it through RecordEvent with
// event type "crew.member_added" (spec 029 §8.4) — never directly.
func AddParticipant(tx *sql.Tx, now time.Time, projectID, actorID, role string, isLead bool, addedBy string, eventID int64) error
```

Validation: project and actor must exist (`ErrNotFound`); `role` is trimmed,
non-empty, ≤ 100 chars, a free-form label — never an enum, never a metric
label (`ErrInvalidInput`); a duplicate `(project, actor, role)` row and a
second lead (unique violation on `project_participants_one_lead`) both map to
`ErrInvalidInput` with messages naming the conflict. Empty `addedBy` stores
NULL. First test, `internal/store/participants_test.go`:

```go
func TestAddParticipant(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	// Seed project p1; actors ada, bob.

	add := func(actor, role string, lead bool) error {
		_, _, err := s.RecordEvent(ctx, "test", newExternalID(t), "crew.member_added", nil,
			func(tx *sql.Tx, eventID int64) error {
				return AddParticipant(tx, s.Now(), "p1", actor, role, lead, "ada", eventID)
			})
		return err
	}

	if err := add("ada", "editor", true); err != nil {
		t.Fatal(err)
	}
	if err := add("bob", "reporter", false); err != nil {
		t.Fatal(err)
	}
	// One actor, several role labels (029 §6.1).
	if err := add("bob", "data-scientist", false); err != nil {
		t.Fatal(err)
	}
	// The same role twice is invalid input.
	if err := add("bob", "reporter", false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate role: got %v", err)
	}
	// A second lead is refused (lead handoff is deferred).
	if err := add("bob", "co-lead", true); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("second lead: got %v", err)
	}
}
```

**Authz**: `permCrewWrite Permission = "crew.write"` in
`internal/api/authz.go`, granted `{RoleUser, RoleAdmin}`, with a comment
noting the project-scoped "any Crew member" rule awaits project-scoped authz
(declared gap, see Coverage notes).

**API** (`internal/api/crew.go`): `POST /api/v1/projects/{id}/participants`,
`routeGuards` entry `guarded(permCrewWrite)`. Body
`{"actor": "...", "role": "member", "lead": false}` — `role` defaults to
`"member"`, `actor` is required. Follow `assignTask`
(`internal/api/assign.go`): `randomExternalID()`, then
`RecordEvent(ctx, "cli", extID, "crew.member_added", payload, apply)` with
the Global Constraints payload shape and `by` = `actorFrom(r).ID`. Respond
201 with the member's JSON (same shape as Task 8's list entries). Errors via
`mapStoreErr` (`ErrInvalidInput` → 422).

**Metric**: add `worklode_crew_changes_total` to `internal/api/metrics.go`
per Global Constraints; observe with `surface="api"`, `action="add"` here,
and `surface="web"` from the form handler below. Extend the metrics tests.

**CLI**: `internal/cli/client.go` gains

```go
type CrewMember struct {
	Actor       string    `json:"actor"`
	DisplayName string    `json:"display_name"`
	Roles       []string  `json:"roles"`
	Lead        bool      `json:"lead"`
	AddedAt     time.Time `json:"added_at"`
}

func (c *Client) AddCrewMember(ctx context.Context, project, actor, role string, lead bool) (CrewMember, []byte, error)
```

`internal/cmd/project.go`: a new `crew` subcommand group under `project`
(`newProjectCrewCmd`, added in `newProjectCmd`'s `AddCommand`), with
`lode project crew add <project> <actor> [--role member] [--lead]` following
the existing `newProjectAddRepoCmd` shape. Cover with a cmd test against the
stub-server pattern the other project cmd tests use.

**Web**: an add-member form on the crew page (actor id, role, lead checkbox)
posting to `POST /projects/{id}/crew` — `routeGuards` entry
`guarded(permWebWrite)`, `sameOriginForm` check, POST-redirect-GET back to
the crew page, rejected submits re-render with the message and typed values
(`internal/api/webform.go` conventions; `webActor(r)` for `by`). Event source
`"web"`. `observeFormSubmission("crew_add", ...)`. Extend `CrewView` with the
form-state fields this needs.

Now the deferred roster assertion: extend `TestCrewPage` to seed via the real
writer (`RecordEvent` + `AddParticipant` from the test, or the POST route)
and assert the rendered roster shows both members, role labels, and exactly
one Lead badge.

- [ ] Store test above red → green; handler, cmd, form, and metrics tests.
- [ ] `go generate ./...` for the form markup; commit generated files.
- [ ] `go test ./...` — green.
- [ ] Commit: `Add crew members across store, API, CLI and web`.

### Task 7 — Remove-member mutation with the §6.1 guard, every surface

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [4, 6]
```

Member-level removal (all the member's role rows in one act, one event).
Dropping a single role label stays out of scope: remove + re-add.

**Store**:

```go
// RemoveParticipant removes actorID from projectID's Crew — all role rows —
// inside the given ingest transaction. Callers reach it through RecordEvent
// with event type "crew.member_removed" (spec 029 §8.4). It refuses while
// the member owns open work (spec 029 §6.1) and refuses to remove the lead
// (lead handoff is deferred).
func RemoveParticipant(tx *sql.Tx, now time.Time, projectID, actorID, removedBy string, eventID int64) error
```

Rules, in order: not a member → `ErrNotFound`; member is the lead →
`ErrInvalidInput` ("project lead cannot be removed; lead handoff is not
implemented"); `openWorkOwnedBy` (Task 4's unexported query, run on the tx)
returns rows → `ErrInvalidInput` whose message lists each item as
`<id> (<kind>, <state>)` so the CLI and API responses carry the
responsibility list. Otherwise delete the rows and `LogChange`. First test:

```go
func TestRemoveParticipantGuard(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	// Seed p1 with lead ada and member bob (via AddParticipant, as Task 6);
	// one open task in p1 assigned to bob.

	remove := func(actor string) error {
		_, _, err := s.RecordEvent(ctx, "test", newExternalID(t), "crew.member_removed", nil,
			func(tx *sql.Tx, eventID int64) error {
				return RemoveParticipant(tx, s.Now(), "p1", actor, "ada", eventID)
			})
		return err
	}

	err := remove("bob")
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "task") {
		t.Fatalf("open work must block removal with the item listed: %v", err)
	}
	// Unassign the task; removal now succeeds and clears every role row.
	// ... UnassignTask via RecordEvent, then:
	if err := remove("bob"); err != nil {
		t.Fatal(err)
	}
	// The lead is never removable while handoff is deferred.
	if err := remove("ada"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("lead removal: got %v", err)
	}
}
```

**API**: `DELETE /api/v1/projects/{id}/participants/{actor}`,
`guarded(permCrewWrite)`; `RecordEvent` source `"cli"`, type
`crew.member_removed`, payload per Global Constraints (`roles` = the rows
being removed); 204 on success, guard failures 422 via `mapStoreErr` with
the item list in the message.

**Metric**: `worklode_crew_changes_total` with `action="remove"` from both
surfaces.

**CLI**: `(c *Client) RemoveCrewMember(ctx, project, actor string)` and
`lode project crew remove <project> <actor>`; a guard failure prints the
server's item list verbatim.

**Web**: a Remove button per non-lead member (small per-row form posting to
`POST /projects/{id}/crew/remove` with a hidden actor field —
`guarded(permWebWrite)`, `sameOriginForm`, PRG). On a guard failure,
re-render the crew page with a responsibility list — the member's
`OpenWorkOwnedBy` items with task links — which is 032 §6's responsibility
review in its minimal honest form. `observeFormSubmission("crew_remove", …)`,
event source `"web"`. Extend `CrewView` and `crew.templ` accordingly; add a
handler test for the blocked-removal render.

- [ ] Store test red → green; handler, cmd, form, metrics tests.
- [ ] `go generate ./...`; `go test ./...` — green.
- [ ] Commit: `Remove crew members with the open-work guard, every surface`.

### Task 8 — Participants read API and the crew CLI listing

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [6]
```

The read fan-out over Task 3's reader.

**API**: `GET /api/v1/projects/{id}/participants`,
`guarded(permProjectRead)`, responding
`{"participants": [{"actor", "display_name", "roles", "lead", "added_at"}]}`
(the `CrewMember` JSON shape from Task 6), empty list for an empty roster,
404 for an unknown project. Handler in `internal/api/crew.go`, mapping
`store.Participant` directly.

**CLI**: `(c *Client) ListCrew(ctx, project string) ([]CrewMember, []byte, error)`;
`lode project crew <project>` lists the roster as a table (name, roles
comma-joined, `lead` marker) via the `internal/cli` rendering helpers the
other project listings use. Cobra runs the `add`/`remove` subcommands when
the first argument names one, so give the parent `crew` command `RunE` +
`cobra.ExactArgs(1)` for the listing — the same parent-with-RunE shape used
elsewhere in `internal/cmd` (if no precedent exists in the repo, a plain
`ExactArgs(1)` parent RunE is still correct cobra; nothing to design).

Handler test: seed two members through `RecordEvent` + `AddParticipant`,
assert the JSON body (roles sorted, lead flag, 404 case). Cmd test against
the stub server.

- [ ] `go test ./internal/api ./internal/cli ./internal/cmd` — green.
- [ ] Commit: `List project crew over the API and CLI`.

### Task 9 — e2e crew journey and follow-ups

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [2, 7, 8]
```

`e2e/crew_test.go` (build tag `e2e`), public surfaces only — HTTP API and
web pages, never a store write:

1. Bootstrap-token API: create a project and two actors; POST both as
   participants (one `--lead` equivalent via the JSON body).
2. `GET /api/v1/projects/{id}/participants` — both present, roles and lead
   flag correct.
3. `GET /projects/{id}/crew` — page 200, both display names present,
   exactly one `Lead` badge, no placeholder text.
4. Create a task in the project and assign it to the non-lead member
   (existing assign API); `DELETE .../participants/{actor}` — 422, body
   names the open task.
5. Unassign; DELETE again — 204; the list and the page no longer show the
   member; the lead remains.
6. `DELETE` of the lead — 422.

Then record in `docs/follow-ups.md` (one short entry, per its format) the
one gap coverage cannot express: `crew.write` is granted to every user
because authz decisions are not project-scoped, so 029 §6.1's "any Crew
member may add or remove an ordinary Crew member" is enforced more widely
than the spec wants. That is a spec-versus-reality conflict, not a planned
partial.

Do **not** add entries for invited participants without a Keycloak actor,
the lead-handoff acceptance ceremony, the derived contributors surface, or
member-level-only removal. The first three are exactly what this plan's
`coverage: partial` on 029 §6.1 already declares, in machine-readable form
a coverage query can read; the fourth is a design decision recorded in
Task 7. Copying them into `docs/follow-ups.md` duplicates the claim in a
place nothing queries, and the two copies drift.

- [ ] `go test -race -count=1 -tags e2e ./e2e/ -run TestCrew` — green
      (`ok  github.com/sunstoneinstitute/worklode/e2e`); full
      `go test -race -count=1 -tags e2e ./e2e/` still green.
- [ ] `go test ./...` — green.
- [ ] Commit: `Prove the crew journey end to end`.
