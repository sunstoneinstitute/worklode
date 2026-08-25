# Crew deputy / acting-lead Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a project designate one Crew member as deputy — someone who can
act with full lead authority when the lead is unavailable (sick, on leave)
without becoming lead and without diluting who is accountable.

**Architecture:** Mirror the existing `is_lead` mechanism: a boolean column
on `project_participants`, at most one true per project, mutually exclusive
with `is_lead` on the same row. The store folds it into the Crew member's
`Roles` as a read-only virtual label `"acting-lead"` — not a real vocabulary
entry, not settable through `--role` — so every existing consumer (CLI table,
JSON API, cockpit roster page) shows it for free with no per-layer plumbing.
The CLI gets a new `--deputy` flag on `lode project crew add`, mutually
exclusive with `--lead`.

**Tech Stack:** Go, PostgreSQL (golang-migrate), Cobra CLI.

**Spec:** `docs/specs/029-research-work-in-the-backbone.md` §6.1 (edited
directly by Task 5 below — the spec is still `status: draft`, so this is a
direct edit, not an amendment).

## Global Constraints

- Migration files are add-only: never edit a shipped migration
  (`worklode-migrations` skill). Next free number is `0052`.
- Every new migration is listed in `deploy/base/kustomization.yaml`.
- `internal/model` types are stdlib-only and are the one wire shape crossing
  the HTTP boundary (ADR 036) — do not invent a parallel type in `internal/api`
  or `internal/cli`.
- `go build -trimpath` / `go test -trimpath -race -count=1 ./...` are the
  commands that match CI; never run bare `go build`/`go test`.
- Store tests need `TEST_POSTGRES_DSN` reachable Postgres with pgvector; they
  skip silently otherwise unless `CI` is set.
- Editing `docs/specs/029-research-work-in-the-backbone.md` triggers the
  pre-commit hook that regenerates `docs/specs/inlined/`; run
  `./scripts/secfmt.py -l -w && ./scripts/inlinespec.py` before committing to
  avoid a fail-then-restage cycle.

---

### Task 1: Migration — `is_deputy` column

**Files:**
- Create: `deploy/base/migrations/0052_deputy_crew_role.up.sql`
- Create: `deploy/base/migrations/0052_deputy_crew_role.down.sql`
- Modify: `deploy/base/kustomization.yaml` (append the two new paths after
  the `0051_task_scoped_tokens.*` lines, same pattern as every prior pair)

**Interfaces:**
- Produces: column `project_participants.is_deputy boolean NOT NULL DEFAULT
  false`; unique partial index `project_participants_one_deputy` (mirrors
  `project_participants_one_lead`); CHECK constraint
  `project_participants_lead_deputy_exclusive` forbidding a row with both
  `is_lead` and `is_deputy` true.

- [ ] **Step 1: Write the up migration**

```sql
-- deploy/base/migrations/0052_deputy_crew_role.up.sql

-- Deputy Crew designation (spec 029 §6.1): one Crew member per project may
-- act with full lead authority when the lead does not act, without becoming
-- lead — the accountable human stays the lead. Mirrors is_lead's shape: a
-- column on the participant row, at most one true per project, and mutually
-- exclusive with is_lead on the same row (a member cannot hold both).
ALTER TABLE project_participants
    ADD COLUMN is_deputy boolean NOT NULL DEFAULT false;

ALTER TABLE project_participants
    ADD CONSTRAINT project_participants_lead_deputy_exclusive
    CHECK (NOT (is_lead AND is_deputy));

CREATE UNIQUE INDEX project_participants_one_deputy
    ON project_participants (project_id) WHERE is_deputy;
```

- [ ] **Step 2: Write the down migration**

```sql
-- deploy/base/migrations/0052_deputy_crew_role.down.sql

DROP INDEX project_participants_one_deputy;
ALTER TABLE project_participants DROP CONSTRAINT project_participants_lead_deputy_exclusive;
ALTER TABLE project_participants DROP COLUMN is_deputy;
```

- [ ] **Step 3: List the migration in kustomization.yaml**

Append, right after the existing `0051_task_scoped_tokens.*` lines:

```yaml
      - migrations/0052_deputy_crew_role.up.sql
      - migrations/0052_deputy_crew_role.down.sql
```

- [ ] **Step 4: Verify the migration pair is well-formed**

Run: `./scripts/check-migrations.sh --no-fix`
Expected: no collision reported for `0052`.

If you have a local Postgres and want to smoke-test the round trip (optional,
not required to proceed — Task 2's store tests exercise this migration for
real):

```bash
migrate -database "$TEST_POSTGRES_DSN" -path deploy/base/migrations up
migrate -database "$TEST_POSTGRES_DSN" -path deploy/base/migrations down 1
migrate -database "$TEST_POSTGRES_DSN" -path deploy/base/migrations up
```

- [ ] **Step 5: Commit**

```bash
git add deploy/base/migrations/0052_deputy_crew_role.up.sql \
        deploy/base/migrations/0052_deputy_crew_role.down.sql \
        deploy/base/kustomization.yaml
git commit -m "migrate: add project_participants.is_deputy"
```

---

### Task 2: Store — `is_deputy` field, `AddParticipant` param, acting-lead fold

**Files:**
- Modify: `internal/store/participants.go`
- Modify: `internal/store/participants_test.go`

**Interfaces:**
- Consumes: Task 1's `project_participants.is_deputy` column, unique index
  `project_participants_one_deputy`, CHECK
  `project_participants_lead_deputy_exclusive`.
- Produces:
  - `Participant.IsDeputy bool` (new field, alongside existing `IsLead bool`)
  - `ActorProject.IsDeputy bool` (new field, alongside existing `IsLead bool`)
  - `AddParticipant(tx *sql.Tx, now time.Time, projectID, actorID, role string, isLead, isDeputy bool, addedBy string, eventID int64) error`
    — **signature changes**: `isDeputy bool` inserted right after `isLead
    bool`. Every existing caller must be updated (Task 2 covers
    `internal/store`; Task 3 covers `internal/api`).
  - Both `ListParticipants` and `ProjectsForActor` append the literal string
    `"acting-lead"` to a Participant/ActorProject's `Roles` before sorting,
    whenever `IsDeputy` is true — so it sorts into place alphabetically with
    the real role labels and every reader of `Roles` (CLI, JSON API, cockpit
    page) sees it with no further code.
  - `"acting-lead"` stays out of `validParticipantRoles` / `ParticipantRoles()`
    — no vocabulary change — so passing `--role acting-lead` (or
    `"role": "acting-lead"` over the JSON API) is refused the same way any
    other unknown role is, by the existing check in `AddParticipant`. This is
    what makes the label read-only.

- [ ] **Step 1: Read the current file for exact context**

Read `internal/store/participants.go:1-334` (already open in your context if
you're continuing this session; otherwise `Read` it) before editing — the
steps below are diffs against what's there now.

- [ ] **Step 2: Add `IsDeputy` to `Participant` and `ActorProject`**

In `Participant` (around line 19-26):

```go
type Participant struct {
	ProjectID   string
	ActorID     string
	DisplayName string
	Roles       []string // sorted
	IsLead      bool
	IsDeputy    bool
	AddedAt     time.Time // earliest role row for this actor
}
```

In `ActorProject` (around line 30-34):

```go
type ActorProject struct {
	Project  Project
	Roles    []string // sorted
	IsLead   bool
	IsDeputy bool
}
```

- [ ] **Step 3: Scan `is_deputy` and fold `"acting-lead"` in `ListParticipants`**

The query at line ~61 selects `pp.role, pp.is_lead`; add `pp.is_deputy`:

```go
	query := `SELECT pp.project_id, pp.actor_id, a.display_name, pp.role, pp.is_lead, pp.is_deputy, pp.added_at
	            FROM project_participants pp
	            JOIN actors a ON a.id = pp.actor_id`
```

In the scan loop (~line 86-108), add the column and fold it onto the
aggregate:

```go
	for rows.Next() {
		var pID, actorID, role string
		var displayName sql.NullString
		var isLead, isDeputy bool
		var addedAt time.Time
		if err := rows.Scan(&pID, &actorID, &displayName, &role, &isLead, &isDeputy, &addedAt); err != nil {
			return nil, fmt.Errorf("list participants for %s: %w", errDesc, err)
		}
		k := key{pID, actorID}
		p, ok := byKey[k]
		if !ok {
			p = &Participant{ProjectID: pID, ActorID: actorID, DisplayName: displayName.String, AddedAt: addedAt}
			byKey[k] = p
			order = append(order, k)
		}
		p.Roles = append(p.Roles, role)
		if isLead {
			p.IsLead = true
		}
		if isDeputy {
			p.IsDeputy = true
		}
		if addedAt.Before(p.AddedAt) {
			p.AddedAt = addedAt
		}
	}
```

The final assembly loop (~line 113-118) folds the virtual role in before
sorting, so it lands in alphabetical order with the real ones:

```go
	out := make([]Participant, 0, len(order))
	for _, k := range order {
		p := byKey[k]
		if p.IsDeputy {
			p.Roles = append(p.Roles, "acting-lead")
		}
		slices.Sort(p.Roles)
		out = append(out, *p)
	}
	return out, nil
```

- [ ] **Step 4: Same fold in `ProjectsForActor`**

The query at line ~127-133 selects `pp.role, pp.is_lead`; add `pp.is_deputy`:

```go
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+projectColumnsP+`, pp.role, pp.is_lead, pp.is_deputy
		   FROM project_participants pp
		   JOIN projects p ON p.id = pp.project_id
		  WHERE pp.actor_id = $1
		  ORDER BY p.id`,
		actorID)
```

Scan loop (~line 141-158):

```go
	for rows.Next() {
		var role string
		var isLead, isDeputy bool
		p, err := scanProject(appendScan{rows, []any{&role, &isLead, &isDeputy}})
		if err != nil {
			return nil, fmt.Errorf("list projects for actor %s: %w", actorID, err)
		}
		ap, ok := byProject[p.ID]
		if !ok {
			ap = &ActorProject{Project: *p}
			byProject[p.ID] = ap
			order = append(order, p.ID)
		}
		ap.Roles = append(ap.Roles, role)
		if isLead {
			ap.IsLead = true
		}
		if isDeputy {
			ap.IsDeputy = true
		}
	}
```

Final assembly loop (~line 163-169):

```go
	out := make([]ActorProject, 0, len(order))
	for _, id := range order {
		ap := byProject[id]
		if ap.IsDeputy {
			ap.Roles = append(ap.Roles, "acting-lead")
		}
		slices.Sort(ap.Roles)
		out = append(out, *ap)
	}
	return out, nil
```

- [ ] **Step 5: Add `isDeputy` to `AddParticipant`, validate mutual exclusion**

Change the signature (~line 279) and add a guard right after the existing
role-vocabulary switch (~line 280-290), before the "both referenced rows"
existence checks:

```go
func AddParticipant(tx *sql.Tx, now time.Time, projectID, actorID, role string, isLead, isDeputy bool, addedBy string, eventID int64) error {
	role = strings.TrimSpace(role)
	switch {
	case role == "":
		return fmt.Errorf("role is required: %w", ErrInvalidInput)
	case utf8.RuneCountInString(role) > maxParticipantRole:
		return fmt.Errorf("role %q is too long (%d characters at most): %w",
			role, maxParticipantRole, ErrInvalidInput)
	case !validParticipantRoles[role]:
		return fmt.Errorf("unknown role %q; valid roles: %s: %w",
			role, strings.Join(ParticipantRoles(), ", "), ErrInvalidInput)
	}
	if isLead && isDeputy {
		return fmt.Errorf("a Crew member cannot be both lead and deputy: %w", ErrInvalidInput)
	}
```

- [ ] **Step 6: Thread `isDeputy` through the insert and its conflict mapping**

The `tx.Exec` INSERT (~line 315-319) and the unique-violation mapping right
after it (~line 320-327):

```go
	if _, err := tx.Exec(
		`INSERT INTO project_participants (project_id, actor_id, role, is_lead, is_deputy, added_at, added_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		projectID, actorID, role, isLead, isDeputy, now.UTC(), by,
	); err != nil {
		if isUniqueViolationOn(err, "project_participants_pkey") {
			return fmt.Errorf("actor %s already holds role %q on project %s: %w",
				actorID, role, projectID, ErrInvalidInput)
		}
		if isUniqueViolationOn(err, "project_participants_one_lead") {
			return fmt.Errorf("project %s already has a lead: %w", projectID, ErrInvalidInput)
		}
		if isUniqueViolationOn(err, "project_participants_one_deputy") {
			return fmt.Errorf("project %s already has a deputy: %w", projectID, ErrInvalidInput)
		}
		return fmt.Errorf("add participant %s to project %s: %w", actorID, projectID, err)
	}

	return LogChange(tx, "project", projectID, eventID, map[string]any{
		"field": "crew", "op": "add",
		"actor": actorID, "role": role, "lead": isLead, "deputy": isDeputy, "by": addedBy,
	})
```

- [ ] **Step 7: Update the doc comments**

`AddParticipant`'s doc comment (~line 267-278) currently says "a project may
hold at most one lead" — extend it: "a project may hold at most one lead and
at most one deputy, and a member cannot hold both". `Participant`'s doc
comment (~line 15-18) and `ActorProject`'s (~line 28-29): note `IsDeputy`
alongside `IsLead` the same way.

- [ ] **Step 8: Update the one in-tree helper that calls `AddParticipant` directly**

`internal/store/participants_test.go`'s `addParticipant` test helper
(~line 281-289) wraps `AddParticipant` for the 8 existing simple test calls
that only ever pass `lead`. Keep its own signature unchanged (zero churn to
those 8 call sites) and just pass `false` for the new parameter internally:

```go
func addParticipant(t *testing.T, s *Store, projectID, actor, role string, lead bool, by string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "crew.member_added", nil,
		func(tx *sql.Tx, eventID int64) error {
			return AddParticipant(tx, s.Now(), projectID, actor, role, lead, false, by, eventID)
		})
	return err
}
```

(Read the existing body first — this is a one-argument insertion, not a
rewrite; keep whatever the current body actually says around it.)

- [ ] **Step 9: Write the failing test — deputy add, mutual exclusion, one per project, acting-lead fold**

Add to `internal/store/participants_test.go`, after `TestAddParticipant`:

```go
// TestAddParticipantDeputy covers the deputy designation (spec 029 §6.1): it
// is mutually exclusive with lead, at most one per project, and read back as
// a virtual "acting-lead" entry folded into Roles rather than a stored role.
func TestAddParticipantDeputy(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "p1", "P1", "AD1"); err != nil {
		t.Fatalf("CreateProject p1: %v", err)
	}
	for _, id := range []string{"ada", "bob", "cleo"} {
		if err := s.CreateActor(ctx, id, "human", strings.ToUpper(id[:1])+id[1:], false); err != nil {
			t.Fatalf("CreateActor %s: %v", id, err)
		}
	}

	addDeputy := func(actor, role string, lead, deputy bool) error {
		_, _, err := s.RecordEvent(ctx, "cli", nextExt(t), "crew.member_added", nil,
			func(tx *sql.Tx, eventID int64) error {
				return AddParticipant(tx, s.Now(), "p1", actor, role, lead, deputy, "ada", eventID)
			})
		return err
	}

	if err := addDeputy("ada", "editor", true, false); err != nil {
		t.Fatalf("add ada as lead: %v", err)
	}
	// Lead and deputy on the same add is refused.
	if err := addDeputy("bob", "reporter", true, true); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("lead and deputy together: got %v", err)
	}
	if err := addDeputy("bob", "reporter", false, true); err != nil {
		t.Fatalf("add bob as deputy: %v", err)
	}
	// A second deputy is refused.
	if err := addDeputy("cleo", "member", false, true); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("second deputy: got %v", err)
	}
	// acting-lead cannot be set as a role directly — it stays outside the
	// fixed vocabulary.
	if err := addDeputy("cleo", "acting-lead", false, false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("role=acting-lead: got %v", err)
	}

	crew, err := s.ListParticipants(ctx, "p1")
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	if len(crew) != 2 {
		t.Fatalf("crew = %+v, want 2 members", crew)
	}
	bobFound := false
	for _, m := range crew {
		if m.ActorID != "bob" {
			continue
		}
		bobFound = true
		if !m.IsDeputy {
			t.Fatalf("bob.IsDeputy = %v, want true", m.IsDeputy)
		}
		if !slices.Equal(m.Roles, []string{"acting-lead", "reporter"}) {
			t.Fatalf("bob.Roles = %+v, want [acting-lead reporter]", m.Roles)
		}
	}
	if !bobFound {
		t.Fatalf("crew = %+v, want bob present", crew)
	}
}
```

- [ ] **Step 10: Run the store tests**

Run: `go test -trimpath ./internal/store -run TestAddParticipant -v`
Expected: PASS (both `TestAddParticipant` and the new
`TestAddParticipantDeputy`), or a clean skip if `TEST_POSTGRES_DSN` is
unreachable — if it skips, note that in your report; this task is not
verified until it has run against real Postgres at least once.

Run: `go test -trimpath ./internal/store -run 'TestListParticipants|TestProjectsForActor' -v`
Expected: PASS — these exercise the existing (non-deputy) paths and must
stay green after the column/signature changes.

- [ ] **Step 11: Commit**

```bash
git add internal/store/participants.go internal/store/participants_test.go
git commit -m "store: add deputy Crew designation, fold as acting-lead"
```

---

### Task 3: Model + API — wire the deputy flag through

**Files:**
- Modify: `internal/model/crew.go`
- Modify: `internal/api/crew.go`
- Modify: `internal/api/web_test.go` (two direct `store.AddParticipant` calls
  need the new parameter)

**Interfaces:**
- Consumes: Task 2's `AddParticipant(tx, now, projectID, actorID, role string, isLead, isDeputy bool, addedBy string, eventID int64) error`.
- Produces:
  - `model.AddCrewMemberInput.Deputy bool` (new field, `json:"deputy"`)
  - `(s *server) recordCrewAdd(ctx context.Context, source, projectID, actorID, role string, lead, deputy bool, by string) (model.CrewMember, error)`
    — **signature changes**: `deputy bool` inserted right after `lead bool`.
    Both of its callers (`addCrewMember`, `addCrewMemberFromForm`) are updated
    in this task.

- [ ] **Step 1: Add `Deputy` to `AddCrewMemberInput`**

In `internal/model/crew.go`, extend the doc comment and struct (~line 26-36):

```go
// AddCrewMemberInput is the request body of POST
// /api/v1/projects/{id}/participants. Actor is required; an empty Role
// defaults to "member" server-side, so adding someone to the Crew without an
// opinion about what they do is one field. Role is drawn from the fixed
// project-role vocabulary (WL-297; store.ParticipantRoles) — an unknown one
// is refused naming the valid set. Deputy marks the member as the project's
// one deputy (spec 029 §6.1): full lead authority when the lead does not
// act, without becoming lead. Lead and Deputy are mutually exclusive.
type AddCrewMemberInput struct {
	Actor  string `json:"actor"`
	Role   string `json:"role"`
	Lead   bool   `json:"lead"`
	Deputy bool   `json:"deputy"`
}
```

- [ ] **Step 2: Thread `deputy` through `recordCrewAdd`**

In `internal/api/crew.go` (~line 84-118):

```go
func (s *server) recordCrewAdd(ctx context.Context, source, projectID, actorID, role string, lead, deputy bool, by string) (model.CrewMember, error) {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return model.CrewMember{}, fmt.Errorf("actor is required: %w", store.ErrInvalidInput)
	}
	role = strings.TrimSpace(role)
	if role == "" {
		role = defaultCrewRole
	}
	now := s.st.Now()

	if err := s.recordEvent(ctx, source, "crew.member_added", map[string]any{
		"project": projectID, "actor": actorID, "roles": []string{role},
		"lead": lead, "deputy": deputy, "by": by,
	}, func(tx *sql.Tx, eventID int64) error {
		return store.AddParticipant(tx, now, projectID, actorID, role, lead, deputy, by, eventID)
	}); err != nil {
		return model.CrewMember{}, err
	}

	crew, err := s.st.ListParticipants(ctx, projectID)
	if err != nil {
		return model.CrewMember{}, err
	}
	for _, p := range crew {
		if p.ActorID == actorID {
			return toCrewMember(p), nil
		}
	}
	return model.CrewMember{}, fmt.Errorf("crew member %s vanished from project %s after the add", actorID, projectID)
}
```

- [ ] **Step 3: Update `addCrewMember` (JSON API) and `addCrewMemberFromForm` (web)**

`addCrewMember` (~line 153-168): pass `req.Deputy`.

```go
	member, err := s.recordCrewAdd(r.Context(), "cli", r.PathValue("id"), req.Actor, req.Role, req.Lead, req.Deputy, actorIDFrom(r))
```

`addCrewMemberFromForm` (~line 174-213): the roster page's own add form has
no deputy affordance — pass the literal `false`, matching the plan's scope
(deputy is CLI-only for now, per the request this plan implements):

```go
	if _, err := s.recordCrewAdd(ctx, "web", project.ID, values.Actor, values.Role, values.Lead, false, actorIDFrom(r)); err != nil {
```

- [ ] **Step 4: Fix the two direct `store.AddParticipant` callers in `web_test.go`**

`internal/api/web_test.go` (~line 643, 646) — insert `false` for the new
parameter, no behavior change intended:

```go
		if err := store.AddParticipant(tx, st.Now(), "plead", "grace", "engineer", true, false, "alice", eventID); err != nil {
			return err
		}
		return store.AddParticipant(tx, st.Now(), "pmember", "grace", "engineer", false, false, "alice", eventID)
```

- [ ] **Step 5: Build and run the existing Crew API tests**

Run: `go build -trimpath ./...`
Expected: builds clean (this is the step that catches any missed call site).

Run: `go test -trimpath ./internal/api -run TestAddCrewMember -v`
Expected: PASS, or a clean skip if Postgres is unreachable.

Run: `go test -trimpath ./internal/api -run TestHome -v`
Expected: PASS — `web_test.go`'s crew-seeding helper (Step 4) is exercised by
the Home page tests.

- [ ] **Step 6: Commit**

```bash
git add internal/model/crew.go internal/api/crew.go internal/api/web_test.go
git commit -m "api: accept deputy on crew add"
```

---

### Task 4: CLI — `--deputy` flag, mutually exclusive with `--lead`

**Files:**
- Modify: `internal/cli/client.go`
- Modify: `internal/cmd/project.go`
- Modify: `e2e/crew_test.go`

**Interfaces:**
- Consumes: Task 3's `model.AddCrewMemberInput.Deputy` field.
- Produces: `(c *Client) AddCrewMember(ctx context.Context, project, actor,
  role string, lead, deputy bool) (model.CrewMember, []byte, error)` —
  **signature changes**: `deputy bool` inserted right after `lead bool`.

- [ ] **Step 1: Add `deputy` to the CLI client**

In `internal/cli/client.go` (~line 1281-1289):

```go
// AddCrewMember calls POST /api/v1/projects/{id}/participants, adding one
// role-labelled Crew row (spec 029 §6.1). Deputy marks the member as the
// project's one deputy; it is mutually exclusive with lead.
func (c *Client) AddCrewMember(ctx context.Context, project, actor, role string, lead, deputy bool) (model.CrewMember, []byte, error) {
	return doCreate[model.CrewMember](ctx, c, "/api/v1/projects/"+project+"/participants",
		model.AddCrewMemberInput{Actor: actor, Role: role, Lead: lead, Deputy: deputy}, "crew member")
}
```

(Read the existing body first to confirm the exact helper name / call shape
— `doCreate[...]` is what's there today; keep it, only the signature and the
struct literal change.)

- [ ] **Step 2: Add `--deputy` to `lode project crew add`, mutually exclusive with `--lead`**

In `internal/cmd/project.go`, `newProjectCrewAddCmd` (~line 154-190):

```go
func newProjectCrewAddCmd() *cobra.Command {
	var role string
	var lead, deputy bool
	cmd := &cobra.Command{
		Use:   "add <project> <actor>",
		Short: "Add an actor to a project's Crew",
		Long: "Add an actor to a project's Crew with a role label.\n\n" +
			"The role is one of the fixed project-role vocabulary (member, editor,\n" +
			"science-lead, reporter, domain-expert, data-scientist, engineer);\n" +
			"one actor may hold several. A project has at most one lead.\n\n" +
			"--deputy marks the member as the project's one deputy: full lead\n" +
			"authority when the lead does not act, without becoming lead. It is\n" +
			"mutually exclusive with --lead, and shows on the roster as a\n" +
			"read-only \"acting-lead\" role.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			member, raw, err := c.AddCrewMember(cmd.Context(), args[0], args[1], role, lead, deputy)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "added %s to project %s as %s\n",
				member.Actor, args[0], strings.Join(member.Roles, ", "))
			if member.Lead {
				fmt.Fprintf(out, "%s is the project lead\n", member.Actor)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "role for this member: member, editor, science-lead, reporter, domain-expert, data-scientist, or engineer (default: member)")
	cmd.Flags().BoolVar(&lead, "lead", false, "make this member the project lead")
	cmd.Flags().BoolVar(&deputy, "deputy", false, "make this member the project deputy (acts with lead authority when the lead does not)")
	cmd.MarkFlagsMutuallyExclusive("lead", "deputy")
	return cmd
}
```

- [ ] **Step 3: Fix the two existing `e2e` call sites**

`e2e/crew_test.go` (~line 89, 92) — insert `false`, no behavior change:

```go
	if _, _, err := admin.AddCrewMember(ctx, "crewproj", "lucy", "editor", true, false); err != nil {
		t.Fatalf("add lucy as lead: %v", err)
	}
	if _, _, err := admin.AddCrewMember(ctx, "crewproj", "mo", "member", false, false); err != nil {
		t.Fatalf("add mo as member: %v", err)
	}
```

- [ ] **Step 4: Add e2e coverage for the deputy path**

Append a new step to `TestCrewLifecycle` in `e2e/crew_test.go`, right before
"Step 6: the lead can never be removed" (~line 200):

```go
	// --- Step 5b: a deputy is added, shown as acting-lead, and --lead/--deputy stay exclusive ---

	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "deb", Kind: "human", DisplayName: "Deb Deputy",
	}); err != nil {
		t.Fatalf("create actor deb: %v", err)
	}
	if _, _, err := admin.AddCrewMember(ctx, "crewproj", "deb", "member", false, true); err != nil {
		t.Fatalf("add deb as deputy: %v", err)
	}
	crew, _, err = admin.ListCrew(ctx, "crewproj")
	if err != nil {
		t.Fatalf("list crew after adding deb: %v", err)
	}
	deb := findCrewMember(t, crew, "deb")
	if !slices.Contains(deb.Roles, "acting-lead") {
		t.Fatalf("deb.Roles = %+v, want acting-lead present", deb.Roles)
	}

	// Lead and deputy together is refused by the store, independent of the
	// CLI's own --lead/--deputy mutual exclusion.
	if _, _, err := admin.AddCrewMember(ctx, "crewproj", "mo", "editor", true, true); err == nil {
		t.Fatal("add mo as lead and deputy: want error, got success")
	}
```

Add `"slices"` to the import block if it is not already there.

- [ ] **Step 5: Build and run**

Run: `go build -trimpath ./...`
Expected: builds clean.

Run: `go vet ./...`
Expected: clean.

Run (only if `TEST_POSTGRES_DSN` is set to a reachable Postgres):
`go test -trimpath -tags e2e ./e2e -run TestCrewLifecycle -v`
Expected: PASS. If Postgres is unreachable, say so explicitly rather than
claiming this ran.

- [ ] **Step 6: Manually exercise the CLI help and mutual exclusion**

```bash
make build
./bin/lode project crew add --help
```
Expected: shows both `--lead` and `--deputy` in the flag list.

```bash
./bin/lode project crew add somenonexistentproject someactor --lead --deputy
```
Expected: cobra's own mutual-exclusion error (`if any flags in the group
[lead deputy] are set none of the others can be; [--deputy --lead] were all
set`), returned before any API call is attempted.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/client.go internal/cmd/project.go e2e/crew_test.go
git commit -m "cli: add --deputy to lode project crew add"
```

---

### Task 5: Spec — 029 §6.1 direct edit

**Files:**
- Modify: `docs/specs/029-research-work-in-the-backbone.md`

**Interfaces:**
- Consumes: nothing (documents the mechanism Tasks 1-4 built).
- Produces: updated prose in §6.1. No new section, no new anchor — the spec
  is `status: draft`, so this is a direct edit rather than an amendment (the
  freeze/amend machinery in `docs/authoring-design-docs.md` only applies once
  a document is `accepted`).

- [ ] **Step 1: Read the current section for exact context**

Read `docs/specs/029-research-work-in-the-backbone.md:246-284` (the "6.
People" / "6.1 Assignee, participants, contributors" section) before editing.

- [ ] **Step 2: Insert the deputy/acting-lead paragraph**

Insert a new paragraph immediately after the existing paragraph that ends
"...the Editor and Science Lead jointly authorize the handoff." and before
"Closing the project closes the active Crew...":

```markdown
A project may also designate one Crew member as **deputy** — a fact set when
they are added, like the lead flag, and revocable at will. A deputy exercises
full lead authority, including authorizing the handoff above, whenever the
lead does not act; the lead remains the accountable human, and holding the
designation never makes the deputy lead. The lead and deputy positions are
mutually exclusive — no member holds both — and a project has at most one
deputy. Removing a deputy from the Crew is an ordinary removal, subject only
to the open-work guard above: there is no handoff process for it, because the
designation carries no accountability to transfer. The roster shows the
designation as **acting-lead**: a read-only, virtual role label folded into
the member's role list for display. It cannot be set through the role
vocabulary above — a role of `acting-lead` is refused the same way any other
unrecognized role is.
```

- [ ] **Step 3: Regenerate the derived views and check formatting**

```bash
./scripts/secfmt.py -l -w
./scripts/inlinespec.py
```

Expected: `secfmt.py` reports no change to numbering/anchors (this edit adds
prose to an existing section, no heading changed); `inlinespec.py` rewrites
`docs/specs/inlined/029-research-work-in-the-backbone.md` to include the new
paragraph.

- [ ] **Step 4: Verify the inlined view picked up the change**

Run: `grep -n "acting-lead" docs/specs/inlined/029-research-work-in-the-backbone.md`
Expected: at least one match, inside §6.1.

- [ ] **Step 5: Commit**

```bash
git add docs/specs/029-research-work-in-the-backbone.md docs/specs/inlined/029-research-work-in-the-backbone.md
git commit -m "spec: add deputy / acting-lead designation to 029 §6.1"
```
