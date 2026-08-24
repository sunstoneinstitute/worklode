---
status: draft
covers:
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-6
    coverage: full
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-6.1
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-14-project-crew-participants.md
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-6.2
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-14-project-crew-participants.md
  - spec: docs/specs/032-project-cockpit.md#sec-6
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-14-project-crew-participants.md
  - spec: docs/specs/032-project-cockpit.md#sec-10
    coverage: none
  - spec: docs/specs/032-project-cockpit.md#sec-11
    coverage: none
blockedBy:
  - 2026-08-14-project-crew-participants.md
  - 2026-08-25-research-work-3-intake-and-promotion.md
---

# Crew lifecycle: handoff, invitations, contributors, close

Part 6 of the spec-029 plan series (2026-08-25). It finishes what
`docs/plans/2026-08-14-project-crew-participants.md` deliberately deferred:
the lead-handoff acceptance ceremony, invited participants without a Keycloak
actor, the derived contributors surface, the removal guard widened to reviews
and decisions, the crew half of project close, and §6.2's remaining identity
piece — migration 0014's `expected_github_login` upgraded into the
`githubUsername` user attribute stored on the actor. With that plan, this one
makes 029 §6.1/§6.2 and 032 §6 full.

**Series.** Blocked by `2026-08-14-project-crew-participants` (the stored
Crew this plan extends) and by P4 of this series
(`2026-08-25-research-work-3-intake-and-promotion`), whose migration 0059
adds the `decision` task kind and `task_decisions` (025 §10/§10.1) that
Task 4's widened removal guard reads. P4 also owns project closure itself;
this plan ships the crew half as a store function P4's close transaction
calls (Task 9), a seam named in both plans.
P9 (`2026-08-25-research-work-7-chat-crew-spaces`)
subscribes to the two existing crew event spellings; nothing here varies
them.

## Coverage notes

Why each claim above is what it is:

- **029 §6 (`full`)** — §6 is the People section head; its content lives in
  §6.1/§6.2, and the two plans together leave nothing of it owed.
- **029 §6.1 / 032 §6 (`partial`, full with the 2026-08-14 crew plan)** —
  that plan built the stored participants, the add/remove mutations, and the
  task-only removal guard; this one adds every named remainder: lead
  handoff, invitations, contributors, the widened guard, crew-on-close, and
  032 §6's presentation rules (invited-expert display, the
  "Worklode, on behalf of _User_" activity label, agents never shown as
  Crew).
- **029 §6.2 (`partial`, full with the same plan)** — the earlier plan
  stored the full `groups` claim and `email` at login. This one upgrades the
  GitHub correspondence: the `github_username` token claim (001 §9.2) stops
  being an "expected login for a future link flow" and becomes the actor's
  `githubUsername` attribute — renamed, unique, and read by every GitHub
  fact path (Task 2).
- **032 §10 / §11 (`none`)** — standing rules that govern the crew pages
  and the e2e task without being implemented here.

**The `crew.write` conflict stays recorded, not fixed.**
`docs/follow-ups.md` (WL-52 entry) records that `permCrewWrite` is granted
to every authenticated user, wider than 029 §6.1's "any Crew member". The
participant rows this rule would scope on now exist, and `Decide` already
takes the `Request.Resource` it would need — but narrowing one permission
here would fork authz into two models mid-series while every other
project-scoped permission (task edit, deliverable create, approvals) stays
global, and P3/P4 are adding more of them in parallel. Project-scoped authz
is one change across all permissions at once, in its own plan, on the seam
`authz.go` already documents. The new lead-handoff and invitation routes land
under the same wide grant and the same recorded conflict; Task 11 extends
the follow-up entry to say so. Who may *accept* or *authorize* a handoff is
not authz-table policy at all — it is domain logic in the store, like the
self-approval check (`internal/store/approvals.go`), and is enforced there.

Two scope decisions recorded here rather than as gaps: an invitation carries
exactly one role label (the external-expert case 029 §6.1 names; a
multi-role invitee is invited once and given further roles after linking),
and the "decision" the widened guard sees is an open `decision`-kind task —
the kind and its `task_decisions` side table are P4's migration 0059
(025 §10/§10.1), which is why P4 is in `blockedBy:`. The curated
next-decision card (migration 0013) stays out of the guard: it is free text
that names no actor reliably, and real decision tasks supersede it.

## Global constraints

- **Event spellings are pinned.** Existing, unchanged: `crew.member_added`,
  `crew.member_removed` (P9 depends on these two strings). New in this plan:
  `crew.invited`, `crew.invite_linked`, `crew.invite_withdrawn`,
  `crew.lead_handoff_requested`, `crew.lead_handoff_accepted`,
  `crew.lead_handoff_authorized`, `crew.lead_handoff_cancelled`, and
  `crew.lead_changed` (emitted by the act that completes a handoff, in place
  of its accepted/authorized spelling). Every `crew.*` payload carries
  `"project"` — Task 10's crew-history reader filters on it. All are written
  through the same `s.recordEvent` → `Store.RecordEvent` path as the
  existing pair; event `source` is `"cli"` on the JSON-API path and `"web"`
  on the cockpit-form path.
- **Metrics:** extend `worklode_crew_changes_total{surface, action,
  outcome}` (`internal/api/metrics.go`) — `action` gains `invite`, `link`,
  `withdraw`, `handoff_request`, `handoff_accept`, `handoff_authorize`,
  `handoff_cancel`. Bounded labels only; never a project, actor, or role
  value. Web forms additionally call `observeFormSubmission` with `form ∈
  {crew_invite, crew_link, crew_withdraw, crew_handoff, crew_handoff_accept,
  crew_handoff_authorize, crew_handoff_cancel}`.
- **The role vocabulary is fixed** (WL-297, migration 0046): exactly
  `member`, `editor`, `science-lead`, `reporter`, `domain-expert`,
  `data-scientist`, `engineer`. `crew_invitations.role` reuses it verbatim.
- **Migration number is 0064, assigned by the series brief** — parts 1–9 of
  this series each own a block, so do not let
  `./scripts/check-migrations.sh` renumber it; run with `--no-fix` and keep
  0064. One pair carries all of this part's DDL (the 0013 precedent), listed
  in `deploy/base/kustomization.yaml`. Never edit a shipped migration.
- **Authz:** new routes get `routeGuards` entries only (`permCrewWrite` on
  the JSON API, `permWebWrite` on web forms) — never a check inside a
  handler. Accept/authorize are additionally `requireSession`, the
  `POST /approvals/{id}/decide` pattern (029 §7.3's web-session-act rule).
- Every shape crossing the HTTP boundary is declared once in
  `internal/model` (ADR 036); `internal/model/rule_test.go` enforces it.
- `internal/cmd` decides, `internal/cli` renders — no tabwriter or
  timestamp formatting under `internal/cmd`.
- Store tests need Postgres with pgvector (default DSN in CLAUDE.md,
  override `TEST_POSTGRES_DSN`); a skipped store test proved nothing.
- `e2e/` drives public surfaces only — HTTP API, signed webhooks, web
  pages — never a direct store write.
- **Every task leaves `go test ./...` green** and ends with a commit.
  Pages regenerate with `go generate ./...`; commit the generated files.
- All tasks are specified for a Sonnet-tier implementer per
  `MODEL_SELECTION.md`; escalate on the first sign the plan does not match
  the code.

## Tasks

### Task 1 — Migration 0064: github_username, invitations, lead handoffs

```yaml
kind: chore
priority: high
skills:
  - golang-migrate:migration
blockedBy: [ ]
```

One migration pair, `deploy/base/migrations/0064_crew_lifecycle.up.sql` /
`.down.sql`, listed in `deploy/base/kustomization.yaml` after the 0051
entries. Three concerns, in this order:

**1. The §6.2 identity upgrade.** Migration 0014's column stops being an
"expected login for a future link flow" and becomes the actor's
`githubUsername` attribute — the identity GitHub facts attach to:

```sql
-- The githubUsername user attribute (spec 029 §6.2), mapped into the token
-- as the github_username claim (001 §9.2) and re-synced at every login.
-- Renamed from 0014's expected_github_login: GitHub facts (PR authors,
-- reviewers, commits) attach to the person through it, so it is an identity,
-- not an expectation. The unique index makes the login → actor
-- correspondence a function; it fails loudly on pre-existing duplicate
-- claims, which are a Keycloak misconfiguration to fix, not data to keep.
ALTER TABLE actors RENAME COLUMN expected_github_login TO github_username;
CREATE UNIQUE INDEX actors_github_username_unique
    ON actors (lower(github_username)) WHERE github_username IS NOT NULL;
```

**2. Invitations** (029 §6.1: an external expert may begin as an invited
participant without a Keycloak actor):

```sql
-- An invited participant before identity linkage (spec 029 §6.1). Not a
-- project_participants row: that table FKs actors, and an invitee has none
-- yet. One role label per invitation; no lead flag at all — an invitation
-- cannot act as project lead, structurally. linked_actor set means the
-- invitation is history for that actor; the row is never deleted by
-- linking (029 §6.1: linking preserves the invitation).
CREATE TABLE crew_invitations (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id   text NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    display_name text NOT NULL,
    email        text,
    role         text NOT NULL CHECK (role IN ('member','editor','science-lead',
        'reporter','domain-expert','data-scientist','engineer')),
    invited_at   timestamptz NOT NULL,
    invited_by   text REFERENCES actors (id) ON DELETE RESTRICT,
    linked_actor text REFERENCES actors (id) ON DELETE RESTRICT,
    linked_at    timestamptz
);

CREATE UNIQUE INDEX crew_invitations_open_email
    ON crew_invitations (project_id, lower(email))
    WHERE linked_actor IS NULL AND email IS NOT NULL;
```

**3. Lead handoffs** (029 §6.1: acceptance by both leads, or joint
Editor + Science Lead authorization when the outgoing lead is unavailable).
The ceremony has a pending state that outlives any one request, so it is a
row, with the acceptance facts as columns:

```sql
-- One lead-handoff ceremony (spec 029 §6.1). state 'pending' is the open
-- ceremony; completion requires incoming_accepted_at plus either
-- outgoing_accepted_at or both authorization slots. The partial unique
-- index allows one open ceremony per project.
CREATE TABLE crew_lead_handoffs (
    id                         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id                 text NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    from_actor                 text NOT NULL REFERENCES actors (id) ON DELETE RESTRICT,
    to_actor                   text NOT NULL REFERENCES actors (id) ON DELETE RESTRICT,
    state                      text NOT NULL CHECK (state IN ('pending','completed','cancelled')),
    outgoing_accepted_at       timestamptz,
    incoming_accepted_at       timestamptz,
    editor_authorized_by       text REFERENCES actors (id) ON DELETE RESTRICT,
    editor_authorized_at       timestamptz,
    science_lead_authorized_by text REFERENCES actors (id) ON DELETE RESTRICT,
    science_lead_authorized_at timestamptz,
    requested_by               text REFERENCES actors (id) ON DELETE RESTRICT,
    requested_at               timestamptz NOT NULL,
    resolved_at                timestamptz
);

CREATE UNIQUE INDEX crew_lead_handoffs_one_pending
    ON crew_lead_handoffs (project_id) WHERE state = 'pending';
```

`0064_crew_lifecycle.down.sql` drops the two tables, drops
`actors_github_username_unique`, and renames the column back.

- [ ] Write the pair and the two kustomization entries.
- [ ] `./scripts/check-migrations.sh --no-fix` — exits 0 (the gap from 0051
      to 0064 is the series allocation, not a collision).
- [ ] Round-trip up → down → up against a scratch database
      (`golang-migrate:test-roundtrip`, or the compose Postgres).
- [ ] `go test -trimpath ./internal/store` with Postgres up — existing tests
      still green against the extended schema.
- [ ] Commit: `Add crew lifecycle migration: github_username, invitations, lead handoffs`.

### Task 2 — githubUsername through the code: one identity, one spelling

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

Rename the correspondence end to end and give its new uniqueness a clear
failure. The token claim stays spelled `github_username` (that is 001 §9.2's
Keycloak mapper, untouched); the column, the Go field, and the comments stop
saying "expected".

- `internal/store/actors.go`: `Actor.ExpectedGitHubLogin` becomes
  `Actor.GitHubUsername`; `UpsertHumanActor`'s parameter, SQL column list,
  and `actorColumns` follow. Map a unique violation on
  `actors_github_username_unique` to `ErrInvalidInput` naming the login and
  both facts ("github login X is already claimed by another actor") — two
  Keycloak accounts asserting one GitHub identity is a misconfiguration the
  second login must surface, not a 500.
- `internal/store/approvals.go`: `gitHubLoginForActor` and
  `ActorIDForGitHubLogin` read `github_username`; update both doc comments.
  The self-approval check (`resolveApprovalDecision`) is a caller and needs
  no further change.
- `internal/hooks/github.go`: the three `ActorIDForGitHubLogin` call sites
  compile unchanged; update any comment naming the old column.
- `internal/api/oidcauth.go` and migration-0014-era comments: same sweep —
  `grep -rn "expected_github_login\|ExpectedGitHubLogin" internal/ e2e/`
  must end empty.
- Spec 056's cross-project inbox will read this same correspondence when it
  is implemented; nothing to change there yet — the point of doing the
  rename now is that 056 never sees the old spelling.

First test, in `internal/store/actors_test.go`:

```go
func TestGitHubUsernameIsUniqueAcrossActors(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	if err := s.UpsertHumanActor(ctx, "ada", "Ada", false, "adal", "", nil); err != nil {
		t.Fatal(err)
	}
	// A second actor claiming the same login (any case) is refused with a
	// message naming the login, not a raw constraint error.
	err := s.UpsertHumanActor(ctx, "bob", "Bob", false, "AdaL", "", nil)
	if !errors.Is(err, store.ErrInvalidInput) || !strings.Contains(err.Error(), "adal") {
		t.Fatalf("duplicate github login: got %v", err)
	}
	// Re-login by the holder is not a conflict with itself.
	if err := s.UpsertHumanActor(ctx, "ada", "Ada", false, "adal", "", nil); err != nil {
		t.Fatal(err)
	}
}
```

(Adjust the errors package reference to the in-package form if the test file
is `package store`.)

- [ ] Red: the new test; green after the rename and the violation mapping.
- [ ] `go test -trimpath ./internal/store ./internal/api ./internal/hooks` — green.
- [ ] Commit: `Store the githubUsername attribute on actors, unique per login`.

### Task 3 — Store reader: derived contributors

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

029 §6.1: contributors are everyone who was ever assigned a task in the
project, and derivation is the point — a query, never a stored list
(025 §1). The facts already exist: `tasks.assignee` for current assignments,
and every assignment change is a `state_log` row with
`change->>'field' = 'assignee'` (`internal/store/assign.go`). In
`internal/store/participants.go`:

```go
// Contributor is one person ever assigned a task in a project (spec 029
// §6.1). Derived from tasks.assignee and the state_log assignment history —
// never stored, so an engineer who fixed one pipeline task gets credit
// without anyone maintaining a list (025 §1).
type Contributor struct {
	ActorID     string
	DisplayName string
	Tasks       int // distinct tasks ever assigned to them here
}

func (s *Store) ListContributors(ctx context.Context, projectID string) ([]Contributor, error)
```

The query is a UNION of the two fact sources, joined to `actors` filtered
`kind = 'human'` (agents execute delegated work and are not contributors any
more than they are Crew), grouped per actor with `COUNT(DISTINCT task_id)`,
excluding soft-deleted tasks (`tasks.deleted_at IS NULL` — withdrawn work
is withdrawn) and empty `new` values (unassignment rows). Order by task
count descending, then actor id. Unknown project → `ErrNotFound`, the
`ListParticipants` pattern. ponytail: the `state_log` scan has no expression
index on `change->>'field'`; add one only when the log's volume makes this
reader measurably slow.

First test, in `internal/store/participants_test.go`:

```go
func TestListContributors(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	// Seed project p1, actors ada and bob, three tasks. Assign t1 to ada,
	// then reassign t1 to bob (AssignTask via RecordEvent, twice); assign
	// t2 to ada and close it through the lifecycle; leave t3 unassigned.

	got, err := s.ListContributors(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	// ada was ever-assigned t1 and t2 (the t1 reassignment does not erase
	// her); bob was ever-assigned t1. Closed tasks still count.
	if len(got) != 2 || got[0].ActorID != "ada" || got[0].Tasks != 2 ||
		got[1].ActorID != "bob" || got[1].Tasks != 1 {
		t.Fatalf("contributors wrong: %+v", got)
	}
}
```

- [ ] `go test -trimpath ./internal/store -run TestListContributors` — green.
- [ ] Commit: `Derive project contributors from assignment history`.

### Task 4 — Widen the removal guard; agents never join Crew

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

`openWorkOwnedBy` (`internal/store/participants.go`) sees open tasks only;
its own comment promised approvals and decisions "when their tables exist".
They do now. 029 §6.1: before removal, every open task, decision and review
the member owns must be reassigned or explicitly left unassigned.

**Reviews.** An open approval the member is the required reviewer of:
`approvals` rows in `('awaiting','changes_requested')` with
`required_actor = actor`, scoped to the project via
`pull_requests` (`repo || '#' || number = entity_id`, the established
`prEntityIDSQL`) joined to `project_repos` (`repo` is UNIQUE, so a repo maps
to one project). Rows whose `required_role` names a role but no actor are
owned by nobody and stay out. `Kind: "review"`, `ID` = the approval's
`entity_id` (`repo#number` — what a human acts on), `Title` from the PR,
`State` from the approval.

**Decisions.** A decision is a task: P4's migration 0059 adds the
`decision` kind to the `tasks` CHECK and the `task_decisions` side table
(025 §10/§10.1), and this plan is blocked on it. An open decision assigned
to the member is therefore already caught by the existing task clause —
no second query. What changes is honesty in the listing: select `kind`
alongside the other columns and report `Kind: "decision"` when
`tasks.kind = 'decision'`, `"task"` otherwise, so the refusal names each
item in §6.1's own words. The curated next-decision card (migration 0013)
stays out of the guard — free text that names no actor reliably, and real
decision tasks supersede it.

Both join `openWorkOwnedBy`'s result as further queries on the same
`rowQueryer`, so the removal guard (`RemoveParticipant`) and the
responsibility listing (`OpenWorkOwnedBy`) stay one fact query. Update the
`OwnedWork` doc comment — the "when their tables exist" promise is now kept.

**The refusal must show the new kinds.** `renderCrewRemovalRefusal`
(`internal/api/crew.go`) and `crewResponsibilities` (`internal/ui/crew.templ`)
already iterate `OwnedWork`; what changes is the link per kind — add an
`Href` field to `ui.CrewWorkItem`, filled in the api layer: `task` and
`decision` → `/tasks/{id}`, `review` → `/reviews`. The
CLI and JSON API already carry the store's message, which now names the new
items; no change there.

**Agents never join Crew** (029 §6.1: "Agents execute delegated work but
are never crew members"). `AddParticipant` checks the actor exists but not
what it is. After `requireActor`, read `actors.kind` and refuse non-`human`
with `ErrInvalidInput`: "agents execute delegated work and cannot join the
Crew (spec 029 §6.1)". Forward-only — no backfill, since nothing has been
adding agents.

First test, extending `internal/store/participants_test.go`:

```go
func TestOpenWorkOwnedBySeesReviewsAndDecisions(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	// Seed project p1 with repo "org/data" (AddProjectRepo), actor ada with
	// github login "adal"; ingest a PR for that repo and an awaiting
	// approval whose required_actor is ada (InsertAwaitingApproval inside
	// RecordEvent, as approvals-1's tests do). Mint a decision-kind task in
	// p1 (CreateTask with kind "decision", migration 0059) and assign it to
	// ada (AssignTask via RecordEvent).

	got, err := s.OpenWorkOwnedBy(ctx, "p1", "ada")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, w := range got {
		kinds[w.Kind] = true
	}
	if !kinds["review"] || !kinds["decision"] {
		t.Fatalf("want review and decision items, got %+v", got)
	}
}
```

Add the companion cases: resolving the approval, or recording the
decision's answer and closing its task, drops the item;
`RemoveParticipant` of a member holding only a review refuses with
`review` named in the message; adding an agent-kind actor to the Crew is
`ErrInvalidInput`. Extend the blocked-removal handler test in
`internal/api/crew_test.go` for the per-kind hrefs.

- [ ] Store tests red → green; handler test for the widened listing.
- [ ] `go generate ./...`; `go test -trimpath ./...` — green.
- [ ] Commit: `Widen the crew removal guard to reviews and decisions`.

### Task 5 — Invitations: store mutations and readers

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

029 §6.1: an external expert may begin as an invited participant without a
Keycloak actor; the invitation cannot own work, resolve an approval, or act
as project lead (all three hold structurally — `tasks.assignee`,
`approvals.required_actor`, and `is_lead` all live on actor-backed rows an
invitation does not have); linking preserves the invitation and
participation history rather than replacing it. In
`internal/store/participants.go`, the `AddParticipant` pattern — tx-scoped,
reached only through `RecordEvent`:

```go
// CrewInvitation is one invited participant before (or after) identity
// linkage (spec 029 §6.1). LinkedActor == "" means the invitation is open.
type CrewInvitation struct {
	ID          int64
	ProjectID   string
	DisplayName string
	Email       string
	Role        string
	InvitedAt   time.Time
	InvitedBy   string
	LinkedActor string
	LinkedAt    time.Time
}

// InviteParticipant records one invitation (event "crew.invited").
func InviteParticipant(tx *sql.Tx, now time.Time, projectID, displayName, email, role, invitedBy string, eventID int64) (int64, error)

// LinkInvitation attaches an invitation to an authenticated identity
// (event "crew.invite_linked"): the actor joins the Crew with the
// invitation's role via AddParticipant, and the invitation row survives as
// history with linked_actor and linked_at set.
func LinkInvitation(tx *sql.Tx, now time.Time, invitationID int64, actorID, by string, eventID int64) error

// WithdrawInvitation deletes an open invitation (event
// "crew.invite_withdrawn"); the event is its history.
func WithdrawInvitation(tx *sql.Tx, now time.Time, invitationID int64, by string, eventID int64) error

func (s *Store) ListInvitations(ctx context.Context, projectID string) ([]CrewInvitation, error)
```

Rules: `InviteParticipant` — project must exist (`ErrNotFound`);
`displayName` trimmed, non-empty; `role` validated against
`validParticipantRoles`; empty `email` stores NULL; a duplicate open
invitation for the same email (unique violation on
`crew_invitations_open_email`) is `ErrInvalidInput` naming the conflict.
`LinkInvitation` — the invitation must exist and be open (`ErrNotFound` /
`ErrInvalidInput` "already linked"); the actor must exist and be human
(Task 4's check, via `AddParticipant`); the Crew insert goes through
`AddParticipant` with the invitation's role and `isLead: false`, so
validation, the change log, and the duplicate-role refusal are the same code
path; then set `linked_actor`/`linked_at` — never delete. `LogChange` on
link records the original `invited_at`, keeping the participation history
attributable. `WithdrawInvitation` — open invitations only; linked ones are
history and refuse (`ErrInvalidInput`). `ListInvitations` orders open first,
then by `invited_at`.

First test:

```go
func TestInvitationLifecycle(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	// Seed project p1, human actor eva.

	var invID int64
	record := func(typ string, apply func(tx *sql.Tx, eventID int64) error) error {
		_, _, err := s.RecordEvent(ctx, "test", newExternalID(t), typ, nil, apply)
		return err
	}
	err := record("crew.invited", func(tx *sql.Tx, eventID int64) error {
		var err error
		invID, err = InviteParticipant(tx, s.Now(), "p1", "Dr. Eva Ng", "eva@example.org", "domain-expert", "ada", eventID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// Linking adds the Crew row and preserves the invitation (029 §6.1).
	err = record("crew.invite_linked", func(tx *sql.Tx, eventID int64) error {
		return LinkInvitation(tx, s.Now(), invID, "eva", "ada", eventID)
	})
	if err != nil {
		t.Fatal(err)
	}
	invs, err := s.ListInvitations(ctx, "p1")
	if err != nil || len(invs) != 1 || invs[0].LinkedActor != "eva" {
		t.Fatalf("invitation must survive linking: %+v (%v)", invs, err)
	}
	crew, err := s.ListParticipants(ctx, "p1")
	if err != nil || len(crew) != 1 || crew[0].ActorID != "eva" ||
		!slices.Equal(crew[0].Roles, []string{"domain-expert"}) {
		t.Fatalf("linked member wrong: %+v (%v)", crew, err)
	}

	// A linked invitation cannot be withdrawn; it is history now.
	err = record("crew.invite_withdrawn", func(tx *sql.Tx, eventID int64) error {
		return WithdrawInvitation(tx, s.Now(), invID, "ada", eventID)
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("withdraw after link: got %v", err)
	}
}
```

Add the companion cases: duplicate open email refused; unknown role refused;
withdrawing an open invitation deletes it; linking to an agent-kind actor
refused (through Task 4's check).

- [ ] `go test -trimpath ./internal/store -run TestInvitation` — green.
- [ ] Commit: `Add crew invitations: invite, link, withdraw in the store`.

### Task 6 — Invitations across API, CLI and the crew page

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [5]
```

The three invitation mutations landed on every surface at once, plus the
roster read widened, following `internal/api/crew.go`'s existing
`recordCrewAdd`/`recordCrewRemove` split (one write function per mutation,
both surfaces call it; source `"cli"` vs `"web"`).

**Model** (`internal/model/crew.go`): `CrewInvitation` (wire form: `id`,
`display_name`, `email`, `role`, `invited_at`, `invited_by`, `linked_actor`,
`linked_at`), `InviteCrewInput` (`display_name`, `email`, `role`),
`LinkInvitationInput` (`actor`); `ParticipantListResponse` gains
`Invitations []CrewInvitation `json:"invitations"``.

**API** (`routeGuards` entries, all `guarded(permCrewWrite)`):
- `POST /api/v1/projects/{id}/invitations` — 201 with the invitation.
- `POST /api/v1/projects/{id}/invitations/{inv}/link` — 200 with the
  member as the roster now shows them (`recordCrewAdd`'s read-back
  pattern).
- `DELETE /api/v1/projects/{id}/invitations/{inv}` — 204.
`GET /api/v1/projects/{id}/participants` (existing, `permProjectRead`) now
also returns `invitations`. Errors via `mapStoreErr` (`ErrInvalidInput` →
422).

**Metrics:** `observeCrewChange` with `action ∈ {invite, link, withdraw}`
from both surfaces.

**CLI** (`internal/cli/client.go` + `internal/cmd/project.go`, under the
existing `crew` group):
`lode project crew invite <project> <display-name> [--email ...] [--role member]`,
`lode project crew link <project> <invitation-id> <actor>`,
`lode project crew uninvite <project> <invitation-id>`. `lode project crew
<project>` lists open invitations under the roster ("Invited" marker) — the
rendering is a `cli.*Table` extension in `internal/cli/render.go`, per the
seam. Cmd tests against the stub-server pattern.

**Web** (crew page, `internal/ui/crew.templ` + `views.go`): invited people
render in the Crew list marked **Invited** — same displayed person before
and after linking (032 §6) — with no Lead badge and no Remove button; their
row carries a Link form (actor id input) and a Withdraw button instead, the
ownership and approval affordances an actor-backed row would have being
absent rather than disabled-in-place (nothing to disable exists on this
page; the structural guarantee is the store's). An invite form (name, email,
role dropdown reusing `formOptions(store.ParticipantRoles(), ...)`) sits
beside the existing add form. Routes `POST /projects/{id}/crew/invite`,
`/crew/link`, `/crew/withdraw` — `guarded(permWebWrite)`,
`beginFormPost`/PRG/re-render-on-refusal per the existing crew forms, event
source `"web"`, `observeFormSubmission` per Global Constraints.

Handler tests: invite → page shows the invited row with Link and Withdraw;
link → the same person renders as a member with the invitation's role; the
JSON roster carries `invitations`; refusals re-render with the message.

- [ ] `go generate ./...`; commit generated files.
- [ ] `go test -trimpath ./...` — green.
- [ ] Commit: `Invite, link and withdraw crew invitations on every surface`.

### Task 7 — Lead handoff: the store ceremony

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

029 §6.1: changing the project lead requires acceptance by the outgoing and
incoming leads; if the outgoing lead is unavailable, the Editor and Science
Lead jointly authorize the handoff. The pending state lives in
`crew_lead_handoffs` (Task 1) — a durable row, because the ceremony spans
sessions and people, and each step must be an event against something with
identity. In `internal/store/participants.go` (or a new `handoffs.go` in
the package), tx-scoped like every crew mutation:

```go
type LeadHandoff struct {
	ID         int64
	ProjectID  string
	FromActor  string
	ToActor    string
	State      string // pending | completed | cancelled
	OutgoingAcceptedAt, IncomingAcceptedAt   time.Time // zero = not yet
	EditorAuthorizedBy, ScienceAuthorizedBy  string
	RequestedBy string
	RequestedAt time.Time
	ResolvedAt  time.Time
}

func RequestLeadHandoff(tx *sql.Tx, now time.Time, projectID, toActor, by string, eventID int64) (int64, error)
func AcceptLeadHandoff(tx *sql.Tx, now time.Time, handoffID int64, actingActor string, eventID int64) (completed bool, err error)
func AuthorizeLeadHandoff(tx *sql.Tx, now time.Time, handoffID int64, actingActor string, eventID int64) (completed bool, err error)
func CancelLeadHandoff(tx *sql.Tx, now time.Time, handoffID int64, by string, eventID int64) error
func (s *Store) PendingLeadHandoff(ctx context.Context, projectID string) (*LeadHandoff, error)
```

Rules, enforced here (domain logic, like the self-approval check — never in
a handler):

- **Request:** the project must have a lead (`from_actor` is read from the
  `is_lead` row, `FOR UPDATE`; a leadless project sets its first lead
  through `AddParticipant` and needs no ceremony); `toActor` must be a
  human Crew member of the project and ≠ the lead; a second pending
  ceremony is refused (unique violation on
  `crew_lead_handoffs_one_pending` → `ErrInvalidInput`).
- **Accept:** pending only; `actingActor == FromActor` stamps
  `outgoing_accepted_at`, `== ToActor` stamps `incoming_accepted_at`,
  anyone else is refused (`ErrInvalidInput` naming who may accept); a
  second accept of the same side is refused ("already accepted").
- **Authorize** (the outgoing-lead-unavailable path): `actingActor` must
  hold the `editor` or `science-lead` role on this project's Crew and must
  not be `ToActor` (the incoming lead cannot authorize their own handoff).
  It fills the empty slot the actor qualifies for — `editor` first when
  they hold both — and the two slots must be filled by **different**
  actors (`ErrInvalidInput` otherwise): "jointly" is two-person control,
  and one person holding both role labels is still one person.
- **Completion**, checked inside the same transaction after every accept
  and authorize: `incoming_accepted_at` set AND (`outgoing_accepted_at`
  set OR both authorization slots filled). Completing flips the lead
  atomically — clear `is_lead` on the outgoing lead's row, set it on the
  incoming lead's earliest role row (their Crew rows locked
  `FOR UPDATE`) — and marks the ceremony `completed` with `resolved_at`.
  If the incoming lead left the Crew while the ceremony was pending, the
  completing act is refused instead.
- **Cancel:** pending only; `by` must be `FromActor`, `ToActor`, or
  `RequestedBy` (Task 9's project-close path calls the unexported core
  without that check). State `cancelled`, `resolved_at` stamped.
- Every function `LogChange`s under `entity_kind: "project"` with
  `"field": "lead_handoff"` and the op.

**`RemoveParticipant`'s lead refusal changes its sentence:** from "lead
handoff is not implemented" to "hand the lead off first (lode project crew
handoff)" — same `ErrInvalidInput`, updated tests. `AddParticipant`'s
second-lead refusal stays as is.

First test:

```go
func TestLeadHandoffAcceptance(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	// Seed p1 with lead ada (editor) and member bob, via AddParticipant.

	var hid int64
	record := func(typ string, apply func(tx *sql.Tx, eventID int64) error) error {
		_, _, err := s.RecordEvent(ctx, "test", newExternalID(t), typ, nil, apply)
		return err
	}
	err := record("crew.lead_handoff_requested", func(tx *sql.Tx, eventID int64) error {
		var err error
		hid, err = RequestLeadHandoff(tx, s.Now(), "p1", "bob", "ada", eventID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// A bystander cannot accept; each named party accepts once; the second
	// acceptance completes the ceremony and flips the lead.
	accept := func(typ, actor string) (completed bool, err error) {
		err = record(typ, func(tx *sql.Tx, eventID int64) error {
			completed, err = AcceptLeadHandoff(tx, s.Now(), hid, actor, eventID)
			return err
		})
		return
	}
	if _, err := accept("crew.lead_handoff_accepted", "carol"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bystander accept: got %v", err)
	}
	if done, err := accept("crew.lead_handoff_accepted", "ada"); err != nil || done {
		t.Fatalf("outgoing accept: done=%v err=%v", done, err)
	}
	if done, err := accept("crew.lead_changed", "bob"); err != nil || !done {
		t.Fatalf("incoming accept must complete: done=%v err=%v", done, err)
	}

	crew, err := s.ListParticipants(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range crew {
		if p.IsLead != (p.ActorID == "bob") {
			t.Fatalf("lead did not move: %+v", crew)
		}
	}
}
```

Add the companion cases: the joint-authorization path (editor + science-lead
by two different actors completes after the incoming accept; one actor
holding both labels cannot fill both slots; the incoming lead cannot
authorize); second pending ceremony refused; cancel; the lead is removable
after handing off, and the old refusal message no longer applies to the new
lead's predecessor.

- [ ] `go test -trimpath ./internal/store -run TestLeadHandoff` — green.
- [ ] Commit: `Add the lead-handoff ceremony to the store`.

### Task 8 — Lead handoff surfaces: request anywhere, decide in the browser

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [7]
```

Request and cancel land on both surfaces; accept and authorize are
web-session acts only, the `POST /approvals/{id}/decide` precedent — consent
to accountability is a person at a browser, not a bearer token in a script.

**Event-type selection races are handled the `recordCrewRemove` way:** the
handler reads the pending ceremony first to decide whether this act
completes it (`crew.lead_changed`) or not (`crew.lead_handoff_accepted` /
`_authorized`), then the store call inside `RecordEvent` re-checks under
`FOR UPDATE` — the store stays the authority on what happened; the only
race residue is an event-type one act stale, documented at the write
function.

**Model:** `model.LeadHandoff` (wire form of the store row),
`RequestLeadHandoffInput` (`to`). `ParticipantListResponse` gains
`LeadHandoff *LeadHandoff `json:"lead_handoff,omitempty"`` — the pending
ceremony is roster state.

**API:** `POST /api/v1/projects/{id}/lead-handoff` (201, the ceremony) and
`DELETE /api/v1/projects/{id}/lead-handoff` (cancel pending, 204), both
`guarded(permCrewWrite)`. No API accept/authorize route — deliberate, per
the paragraph above; say so at the registration site the way
`webform.go` documents the missing API decide route.

**Metrics:** `observeCrewChange` `action ∈ {handoff_request,
handoff_accept, handoff_authorize, handoff_cancel}`.

**CLI:** `lode project crew handoff <project> <to-actor>` requests;
`lode project crew handoff <project> --cancel` cancels. The confirmation
names both leads and says acceptance happens in the cockpit (`cli.LocalTime`
for any time shown). `lode project crew <project>` shows the pending
ceremony line.

**Web:** the crew page renders a pending-handoff banner — from → to, which
acceptances and authorizations are already recorded, and four small forms:
Accept, Authorize, Cancel, and (when no ceremony is pending) a Request form
on the lead's row. Routes `POST /projects/{id}/crew/handoff`,
`/crew/handoff/accept`, `/crew/handoff/authorize`, `/crew/handoff/cancel` —
all `guarded(permWebWrite)`; accept and authorize additionally
`requireSession`. The forms render unconditionally in the banner (the store
refuses the wrong actor and the page re-renders with its message — simpler
and no less safe than predicting the viewer's standing). Refusals re-render
per the existing crew-form pattern; successes PRG.

Handler tests: request → banner renders; accept by the wrong actor
re-renders with the store's message; the completing accept (session-stubbed
the way `approvaldecide_test.go` builds a session subject) redirects and the
roster shows the moved Lead badge; the API request/cancel routes; 403 on
accept without a session.

- [ ] `go generate ./...`; commit generated files.
- [ ] `go test -trimpath ./...` — green.
- [ ] Commit: `Surface the lead handoff: request anywhere, decide in the browser`.

### Task 9 — CloseCrew: the crew half of project close

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [7]
```

029 §6.1: closing the project closes the active Crew but preserves its
roster, role history and derived contributions. Project closure itself is
P4's (`2026-08-25-research-work-3-intake-and-promotion`); this task ships
the crew half as one store function P4's close transaction calls, so
neither plan duplicates the other.

What "closes the active Crew" means against this schema, and why nothing
more is needed: the roster **is** preserved by doing nothing to it — the
`project_participants` rows, the linked invitations, the change log and the
events are the roster, the role history, and (with Task 3) the derived
contributions, and deleting any of it is exactly what the spec forbids. The
one piece of live ceremony state is a pending lead handoff, which must not
complete against a closed project. So:

```go
// CloseCrew is the crew half of closing a project (spec 029 §6.1): it
// cancels any pending lead-handoff ceremony and records the crew as closed
// in the change log. It deletes nothing — the participant rows, linked
// invitations, and events are the preserved roster, role history, and
// derived contributions. The caller is the project-close transaction
// (2026-08-25-research-work-3-intake-and-promotion), which also owns
// refusing new crew mutations on a closed project; this function is written
// to be callable from that transaction and idempotent when nothing is
// pending.
func CloseCrew(tx *sql.Tx, now time.Time, projectID, by string, eventID int64) error
```

It cancels via the unexported cancel core (no who-may-cancel check — the
close authority already decided), `LogChange`s
`{"field": "crew", "op": "close", "by": ...}`, and returns nil when there is
no pending ceremony. The refusal of *new* crew mutations on a closed
project needs P4's project-state fact and is P4's wiring; this plan's
functions need no change for it because P4 gates before calling them. That
split is the named seam — record it in the function comment exactly as
above so the two plans cannot drift.

Test, direct-call (the caller does not exist yet):

```go
func TestCloseCrewCancelsPendingHandoff(t *testing.T) {
	// Seed p1 with lead ada, member bob, a pending handoff ada → bob.
	// RecordEvent("crew.lead_handoff_cancelled", CloseCrew(...)) — the
	// ceremony is cancelled, the roster and its rows are untouched, and
	// calling CloseCrew again is a no-op, not an error.
}
```

- [ ] `go test -trimpath ./internal/store -run TestCloseCrew` — green.
- [ ] Commit: `Add CloseCrew for the project-close transaction to call`.

### Task 10 — Crew page: contributors and crew history, honestly labelled

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

Two read sections on the crew page, and the labelling rules 032 §6 fixes.

**Contributors.** A "Contributors" section under the roster, from Task 3's
`ListContributors`: name and task count per person, with a one-line caption
saying it is derived from assignment history ("derivation is the point" —
render it as a projection, no add/remove affordances, no overlap-dedupe
against the roster: a Crew member who did assigned work appears in both,
which is two true statements). The JSON surface is
`GET /api/v1/projects/{id}/contributors` — `guarded(permProjectRead)`,
`model.Contributor` (`actor`, `display_name`, `tasks`), empty list for a
project with no assignment history, 404 unknown project. No CLI verb —
contributors are a display projection; the roster verb stays what it is.

**Crew history.** 032 §6: crew changes and lead handoffs remain visible in
project history. The crew events are that history; render them. A store
reader in `internal/store/participants.go`:

```go
// ListCrewEvents returns a project's crew.* events, newest first, capped.
// Filters on the payload's "project" key — every crew.* event carries it
// (this plan's Global Constraints). ponytail: a jsonb scan over the crew.*
// slice of the events table; give it an index only when a project's event
// volume makes this page measurably slow.
func (s *Store) ListCrewEvents(ctx context.Context, projectID string, limit int) ([]Event, error)
```

The section renders one line per event: local time, a plain-language
sentence derived from the type and payload ("Ada added Bob (reporter)",
"Lead handed off from Ada to Bob", "Dr. Eva Ng invited as domain-expert"),
and the acting actor resolved from the payload's `by`.

**The actor label rules (032 §6), in one helper each:**

- Where the acting identity is an automatic one — the event's `source` is
  neither `"cli"` nor `"web"`, or `by` names a service actor — the label is
  exactly **"Worklode, on behalf of _User_"** with the on-behalf user from
  the payload's `by`, linked to the event (the line names the event id; the
  "effective authorization" link target arrives with 032 §8's automation
  surfaces and until then the event **is** the recorded authorization).
  Pin the spelling in a `ui` helper (`ui.AutomaticActorLabel(user string)`)
  with a test on the exact string, so later surfaces reuse it rather than
  re-deriving it.
- An agent is a delegate on work, never a Crew member or substitute owner:
  the store now refuses agent Crew rows (Task 4) and work rows already
  render the delegate who-line (`workRowActors`, `internal/ui/views.go`);
  add the handler-test assertion that the crew page never renders an
  agent-kind actor in the roster, pinning the presentation rule to the
  guard.
- An approver or reviewer appears in Crew only when the participant facts
  say so: already structural (the roster renders `project_participants` and
  nothing else — approvals-1 never writes to it); assert it in the same
  test by ingesting an approval for a non-member and checking the roster is
  unchanged.

Handler tests: contributors section renders from seeded assignment history
and the JSON route agrees; the history section shows a seeded add, remove,
and handoff in plain language; an event recorded with a non-interactive
source renders the pinned "Worklode, on behalf of ..." label.

- [ ] `go generate ./...`; commit generated files.
- [ ] `go test -trimpath ./...` — green.
- [ ] Commit: `Show contributors and crew history on the crew page`.

### Task 11 — e2e crew lifecycle journey and follow-ups

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [2, 4, 6, 8, 9, 10]
```

Extend `e2e/crew_test.go` (build tag `e2e`), public surfaces only. The
suite has no login provider, so web-session acts are proven the way
`e2e/approvals_test.go` proves the decide act: the affordance renders, and
an unsessioned POST is refused — completion is the handler tests' job.

1. Invite an external expert over the API; the crew page shows the Invited
   row with Link and Withdraw and no Lead badge; the participants JSON
   carries `invitations`.
2. Create the actor, link the invitation over the API; the page shows the
   same displayed person as a member with the invitation's role, and the
   invitation remains in the JSON with `linked_actor` set.
3. Request a lead handoff over the API; the page renders the pending
   banner; a second request is 422; `POST /projects/{id}/crew/handoff/accept`
   without a session is 403; cancel over the API.
4. The widened guard: ingest a PR through the signed GitHub webhook with a
   `review_requested` for a member's `github_username` (the Task 2 rename,
   exercised end to end); `DELETE .../participants/{actor}` is 422 and the
   body names the review; resolve the review through the webhook; the
   removal then succeeds.
5. Contributors: assign and complete a task; `GET .../contributors` lists
   the member; the crew page shows the section.
6. The crew history section lists the journey's events in plain language.

Then two documentation alignments:

- Extend the WL-52 `crew.write` entry in `docs/follow-ups.md` (do not add a
  new one): the lead-handoff and invitation routes now sit under the same
  wide grant, and this plan's Coverage notes record the decision to keep
  the conflict open until authz goes project-scoped in one move.
- Do **not** file follow-ups for anything this plan's `covers:` already
  declares; the frontmatter is the machine-readable claim and a second copy
  drifts.

- [ ] `go test -race -count=1 -tags e2e ./e2e/ -run TestCrew` — green;
      full `-tags e2e ./e2e/` still green.
- [ ] `go test -trimpath ./...` — green.
- [ ] Commit: `Prove the crew lifecycle end to end`.
