---
status: draft
covers:
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-7.1
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-25-approvals-2-flows-and-requirements.md
      - docs/plans/2026-08-25-approvals-3-revision-binding-and-gates.md
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-7.3
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-25-approvals-3-revision-binding-and-gates.md
  - spec: docs/specs/032-project-cockpit.md#sec-10
    coverage: partial
  - spec: docs/specs/032-project-cockpit.md#sec-11
    coverage: none
requires:
  - docs/plans/2026-08-14-project-crew-participants.md
isRequiredBy:
  - docs/plans/2026-08-14-home-project-list.md
---

# Approvals part 1 — the table, the PR ingest, and the web approve act

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** 029 §7.1's single `approvals` table exists; the one requirement
source in scope — `internal/hooks`' existing `pull_request_review` path —
materializes an `awaiting` row when a task-correlated PR opens and resolves it
when the review lands (029 §7.3, final bullet); and approving becomes the web
UI's session-gated mutation (029 §7.3, first bullet): `POST
/approvals/{id}/decide`, refused to bearer tokens and to the open-instance
subject, with author self-approval refused by default (029 §7.1).

**The load-bearing property** (029 §7.1): a *missing* approval is a visible
`awaiting` row. "What is waiting on whom" is a query over rows that exist, so
an absent sign-off can never be indistinguishable from a not-required one.
This is why a merged-but-unreviewed PR's row deliberately stays `awaiting`
(see Decisions below) and why the queue page renders real rows, never a
computed absence.

**Series:** part 1 of the approvals series. A later part 2 carries 029 §7.2
(flows, the rule engine, the `approval_flow` snapshot) and the remaining §7.3
gates (CI, CMS). This plan is plan **C** of the 2026-08-14 four-plan set:

- **B blocks C.** `docs/plans/2026-08-14-project-crew-participants.md` adds
  the full Keycloak `groups` claim (and `email`) to `actors` at login. Task 4
  reads that column; it cannot land before B's migration and login-path change
  exist. This is a document-level dependency, not a task number.
- **C blocks D.** `docs/plans/2026-08-14-home-project-list.md` consumes Task
  6's `ApprovalsAwaiting(actorID, groups)` per-project counts for Home's top
  sort tier. D reads it; D claims no coverage of 029 §7 (consumption is not
  coverage).

These dependencies are stated here rather than in `requires:` frontmatter
because B's and D's files are being authored in parallel and a dangling
reference fails `secmeta.py`; add `requires:`/`isRequiredBy:` when all four
files exist in one branch.

**Coverage gaps, declared** (why every `covers` level is `partial` or
`none`): for 029 §7.1, this plan ships `subject_revision` and hard-refuses
author self-approval, but defers reopen-on-material-change, impact review,
and the policy-permitted self-review exception flow; "materialized as
`awaiting` when the entity is created" holds only for the PR-ingest source.
For 029 §7.3, this plan delivers bullet 1 (the web act) and bullet 4 (the PR
ingest); the CI gate and CMS gate are deferred to part 2. For 032 §10, this
plan covers only the narrow-width behaviour of the surface it builds — the
approvals queue and decide controls (Task 9). 032 §11 binds this plan (e2e
through public surfaces only) while being implemented by none of it.

**Tech stack:** Go 1.26, `net/http` mux, pgx against Postgres,
`templ`-rendered pages, Prometheus client. Store and `internal/api` tests
need Postgres with pgvector.

**Read first:**
- `docs/specs/029-research-work-in-the-backbone.md` §7.1, §7.3, §6.2
- `internal/api/authz.go` — `Subject`, `subjectFromActor`, `webGuard`,
  `Decide`; the permission and grants tables
- `internal/api/router.go` — `routeGuards`; `NewServer` refuses to boot on an
  unguarded route or an unused table entry
- `internal/api/webform.go` — `sameOriginForm`, `parseWebForm`, `webActor`,
  `renderWeb`, and `recordFormTask` (the `RecordEvent` + apply-callback write
  pattern every web mutation follows)
- `internal/hooks/github.go` — `ServeHTTP`'s `RecordEvent` envelope,
  `applyFunc`, `applyPullRequest`, `applyReview`
- `internal/store/changes.go` — `PullRequest`, `UpsertPR`, `getPRTx`
- `deploy/base/migrations/0001_baseline.up.sql` — house schema style
  (identity PKs, `CHECK (state IN (...))`, `text` actor FKs)

## Global Constraints

- **Exact spellings, quoted once.** Approval states:
  `'awaiting'`, `'approved'`, `'rejected'`, `'changes_requested'`. Form
  decision values, in button order: `approve`, `request_changes`, `reject` —
  mapping to states `approved`, `changes_requested`, `rejected`. Entity key:
  `entity_kind = 'pr'`, `entity_id = '<repo>#<number>'` (e.g.
  `sunstoneinstitute/worklode#41`), `subject_revision` = the PR head SHA at
  the time the row was written. Route: `POST /approvals/{id}/decide`.
  Permission: `approval.decide`. Event type: `approval.decided`, source
  `web`. Metric names: `worklode_approvals_ingest_total{action}` (hooks),
  `worklode_approval_decisions_total{decision,outcome}` (api).
- **The decide route is session-only.** `Via == authSession` exactly — not
  `authToken`, not `authOpen`, not `Authenticated()`. 029 §7.3/§6.2: a 30-day
  CLI token carries group claims as stale as the token; a session is at most
  as old as the login that refreshed `actors.groups`. The check is a named
  middleware (`requireSession`, Task 4) applied at route registration in
  `server.go`, never an ad-hoc check inside a handler. There is deliberately
  no CLI verb and no `/api/v1` route for deciding an approval in this plan.
- **Role checks stay out of handlers.** New permission `approval.decide` is a
  `grants` table entry (`{RoleUser, RoleAdmin}`) plus a `routeGuards` row.
  `Decide` stays a pure function; `requireSession` is an authentication-method
  gate, not a role, which is why it is a separate middleware and not a grants
  entry.
- **Every mutation is one event.** The ingest writes ride the existing
  GitHub `RecordEvent` transaction (apply callbacks); the web decide wraps
  its store write in `RecordEvent(ctx, "web", extID, "approval.decided", ...)`
  exactly as `recordFormTask` does. No approval row changes outside an event
  transaction.
- **Migrations:** one new numbered `.up.sql`/`.down.sql` pair, listed in
  `deploy/base/kustomization.yaml`, never an edit to a shipped migration.
  `./scripts/check-migrations.sh` renumbers on collision — plan B is claiming
  a number in parallel, so the number below is nominal.
- **Metrics** (spec 022, `docs/specs/022-prometheus-metrics.md`): nil-safe
  metrics structs in the owning package's `metrics.go`, `worklode_` prefix,
  bounded label values only — never a project id, task id, approval id,
  actor, or group name as a label. Tests for every new metric.
- **Store tests need Postgres with pgvector** (`store.OpenTestStore`); they
  skip silently without it unless `CI` is set — a green run without Postgres
  proved nothing. Run them against the compose Postgres.
- **`e2e/` drives public surfaces only** — HTTP API, signed webhooks, web
  pages; never a direct store write (032 §11).
- **UI toolchain is fixed** by 032 §12, already covered `full` by
  `docs/plans/2026-08-10-cockpit-templ-htmx-tailwind.md`: templ components in
  `internal/ui/*.templ` compiled by `go generate ./...`, styles in
  `internal/ui/styles/app.tailwind.css` compiled to
  `internal/ui/assets/app.css` by the pinned Tailwind CLI. Both generated
  artifacts are committed; regenerate them in any task that touches a
  `.templ` or the stylesheet.
- **Every task leaves `go test ./...` green** and ends with a commit.

## Decisions this plan executes (made in the approved design; do not reopen)

- One requirement source only: the GitHub PR ingest. No flow engine, no
  `approval_flow`, no rule configuration. Part 2 owns 029 §7.2.
- A PR that closes (merged or not) without an approving review keeps its
  `awaiting` row. The gap staying visible is §7.1's point, and it is exactly
  what part 2's CI gate will query.
- `required_actor` on an ingested row is best-effort: the first
  `requested_reviewers[].login` that resolves to an actor via
  `actors.expected_github_login`, else NULL. It drives the "awaiting you"
  signal (plan D); it is not enforced at decide time — GitHub itself lets any
  qualified maintainer review. `required_role`, when set (no source sets it
  in this plan), *is* enforced at decide time via the decider's groups.
- Revision binding is recorded, not yet enforced: `subject_revision` stores
  the head SHA the row was created for, and a review resolves the PR's open
  row regardless of later pushes. Reopen-on-material-change is deferred.

## Tasks

### Task 1 — Migration: the `approvals` table and `pull_requests.author`

```yaml
kind: feature
priority: high
skills:
  - golang-migrate:migration
  - golang-migrate:test-roundtrip
blockedBy: []
```

Create `deploy/base/migrations/0020_approvals.up.sql` / `.down.sql` (number
nominal; the pre-commit collision check renumbers). Two changes:

```sql
-- The PR author's GitHub login, filled by the pull_request ingest from
-- payload user.login. Nullable: rows ingested before this column stay NULL,
-- and the self-approval check degrades to "cannot refuse" on NULL.
ALTER TABLE pull_requests ADD COLUMN author text;

-- Spec 029 §7.1: one table, every approval. A missing approval is a visible
-- 'awaiting' row. entity_kind is unconstrained text: 'pr' is the only value
-- this plan writes; part 2 adds documents/deliverables/tasks without a
-- migration. The UNIQUE key is §7.1's (entity_kind, entity_id,
-- subject_revision); part 2 widens it when one revision needs several lanes.
CREATE TABLE approvals (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_kind      text NOT NULL,
    entity_id        text NOT NULL,
    subject_revision text NOT NULL,
    required_role    text,
    required_actor   text REFERENCES actors (id) ON DELETE RESTRICT,
    resolving_actor  text REFERENCES actors (id) ON DELETE RESTRICT,
    state            text NOT NULL CHECK
        (state IN ('awaiting', 'approved', 'rejected', 'changes_requested')),
    created_at       timestamptz NOT NULL,
    resolved_at      timestamptz,
    UNIQUE (entity_kind, entity_id, subject_revision)
);

CREATE INDEX approvals_awaiting_idx
    ON approvals (entity_kind, entity_id) WHERE state = 'awaiting';
```

Down: `DROP TABLE approvals;` and
`ALTER TABLE pull_requests DROP COLUMN author;`.

- [ ] Write both files; add all four lines under `worklode-migrations` in
      `deploy/base/kustomization.yaml` (up and down, matching the existing
      pattern for 0019).
- [ ] `./scripts/check-migrations.sh --no-fix` — expect exit 0, no output
      about collisions (or run without `--no-fix` and accept the renumber).
- [ ] Roundtrip against a scratch database (golang-migrate:test-roundtrip):
      up → down → up applies cleanly.
- [ ] `go test ./internal/store -run TestMigrations -count=1` (the migration
      harness in `testhelpers.go` applies the full chain on every
      `OpenTestStore`) — expect `ok`.
- [ ] Commit: `Add the approvals table and pull_requests.author (029 §7.1)`.

### Task 2 — Pure decision rules

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: []
```

`internal/store/approval_rules.go` + `approval_rules_test.go`: the three pure
functions the decide path composes, table-tested with no database so part 2's
flows can reuse them against typed inputs.

```go
// DecisionState maps a submitted decision to the approval state it records.
// ok is false for anything but the three defined decisions.
func DecisionState(decision string) (state string, ok bool)
// approve -> approved; request_changes -> changes_requested; reject -> rejected

// QualifiedForRole reports whether an actor holding groups may resolve an
// approval requiring requiredRole. A nil/empty requirement qualifies everyone.
func QualifiedForRole(requiredRole *string, groups []string) bool

// IsSelfApproval reports whether authorLogin and deciderLogin name the same
// GitHub account. GitHub logins are case-insensitive; either side being
// unknown ("") is not self-approval — the check refuses only what it can
// prove (029 §7.1's default refusal, not a guess).
func IsSelfApproval(authorLogin, deciderLogin string) bool
```

First test, verbatim shape:

```go
func TestDecisionState(t *testing.T) {
	cases := []struct {
		decision, state string
		ok              bool
	}{
		{"approve", "approved", true},
		{"request_changes", "changes_requested", true},
		{"reject", "rejected", true},
		{"approved", "", false}, // states are not decisions
		{"", "", false},
	}
	for _, c := range cases {
		state, ok := store.DecisionState(c.decision)
		if state != c.state || ok != c.ok {
			t.Errorf("DecisionState(%q) = %q,%v; want %q,%v",
				c.decision, state, ok, c.state, c.ok)
		}
	}
}
```

Cover `QualifiedForRole` (nil requirement, member, non-member, empty groups)
and `IsSelfApproval` (equal, case-differing equal, different, either empty).

- [ ] `go test ./internal/store -run 'TestDecisionState|TestQualifiedForRole|TestIsSelfApproval' -count=1`
      — expect `ok` (these run without Postgres; they touch no DB).
- [ ] Commit: `Pure approval decision rules (029 §7.1)`.

### Task 3 — Store foundation: insert, resolve, reopen, get

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

`internal/store/approvals.go` + `approvals_test.go`. Tx-scoped functions
(house pattern of `UpsertPR`/`InsertTaskCommit`) so both the GitHub apply
callbacks (Task 5) and the web decide transaction (Task 8) call them inside
`RecordEvent`:

```go
type Approval struct {
	ID              int64
	EntityKind      string
	EntityID        string
	SubjectRevision string
	RequiredRole    *string
	RequiredActor   *string
	ResolvingActor  *string
	State           string
	CreatedAt       time.Time
	ResolvedAt      *time.Time
}

// InsertAwaitingApproval materializes the requirement as an 'awaiting' row.
// ON CONFLICT (entity_kind, entity_id, subject_revision) DO NOTHING:
// a redelivered or reopened PR never duplicates the requirement.
func InsertAwaitingApproval(tx *sql.Tx, now time.Time,
	entityKind, entityID, subjectRevision string,
	requiredRole, requiredActor *string) error

// OpenApprovalForEntity returns the single row for (entityKind, entityID)
// whose state is 'awaiting' or 'changes_requested'; ErrNotFound otherwise.
func OpenApprovalForEntity(tx *sql.Tx, entityKind, entityID string) (*Approval, error)

// resolveApproval stamps state, resolving_actor, resolved_at. Shared by the
// review ingest and the web act, so the two resolution paths cannot drift.
func resolveApproval(tx *sql.Tx, id int64, state string,
	resolvingActor *string, at time.Time) error

// ReopenApproval flips changes_requested back to awaiting (029 §7.1's
// re-request edge), clearing resolving_actor and resolved_at. No-op on any
// other state.
func ReopenApproval(tx *sql.Tx, id int64) error

// SetRequiredActor fills required_actor when it is NULL (a later
// review_requested resolves a reviewer the open ingest could not).
func SetRequiredActor(tx *sql.Tx, id int64, actorID string) error

// ActorIDForGitHubLogin maps a GitHub login to an actor id via
// lower(expected_github_login); "" when no actor matches.
func ActorIDForGitHubLogin(tx *sql.Tx, login string) (string, error)

// GetApproval loads one row by id; ErrNotFound when absent.
func (s *Store) GetApproval(ctx context.Context, id int64) (*Approval, error)
```

First test (Postgres, `store.OpenTestStore(t)`; use `s.DBForTests().Begin()`
for the tx as the existing tx-function tests do):

```go
func TestInsertAwaitingApprovalIsIdempotent(t *testing.T) {
	s := store.OpenTestStore(t)
	tx := mustBegin(t, s)
	now := time.Now().UTC()
	for range 2 { // second insert must be a silent no-op
		if err := store.InsertAwaitingApproval(tx, now,
			"pr", "acme/site#7", "abc123", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	a, err := store.OpenApprovalForEntity(tx, "pr", "acme/site#7")
	if err != nil {
		t.Fatal(err)
	}
	if a.State != "awaiting" || a.SubjectRevision != "abc123" {
		t.Errorf("got state %q revision %q", a.State, a.SubjectRevision)
	}
}
```

Also cover: resolve then `OpenApprovalForEntity` → `ErrNotFound` for
`approved`, still-found for `changes_requested`; `ReopenApproval` clears
`resolving_actor`/`resolved_at` and no-ops on `approved`;
`ActorIDForGitHubLogin` case-insensitive hit and clean miss.

- [ ] `go test ./internal/store -run TestApproval -count=1` (name new tests
      `TestApproval*` / `TestInsertAwaiting*`) against a reachable Postgres —
      expect `ok`, not a skip.
- [ ] Commit: `Approvals store foundation: insert, resolve, reopen (029 §7.1)`.

### Task 4 — Session gate: `requireSession` and `Subject.Groups`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: []
```

**Cross-plan dependency — verify before starting:** this task reads the
`groups` column plan B (`docs/plans/2026-08-14-project-crew-participants.md`)
adds to `actors` and surfaces on `store.Actor`. If `store.Actor` has no
`Groups []string` field when this task runs, stop and escalate — do not stub
the column here.

In `internal/api/authz.go`:

1. Add `Groups []string` to `Subject`; populate it in `subjectFromActor` from
   `store.Actor.Groups`. The zero value everywhere else (token subjects get
   whatever the actor row holds — which is the *stale* copy, and precisely
   why the decide route additionally requires a session; `authOpen` and
   `authNone` subjects have none).
2. Add the middleware — a method next to `webGuard`, applied at registration
   time in `server.go` (Task 8), never inside a handler:

```go
// requireSession refuses any subject not authenticated by a live OIDC
// session cookie. Spec 029 §7.3: approving is a web-session act because the
// session's group claims are at most as old as the login that stored them; a
// bearer token's are as old as the token. authOpen is refused too — an open
// instance has no identity to attribute a decision to.
func (s *server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sub := subjectFrom(r)
		if sub.Via != authSession {
			d := Decision{Reason: "session_required"}
			s.observeAuthz(permApprovalDecide, d)
			s.logDenial(r, sub, permApprovalDecide, d)
			webErr(w, http.StatusForbidden,
				"approving requires a signed-in browser session")
			return
		}
		next(w, r)
	}
}
```

`"session_required"` joins the bounded reason set the
`worklode_authz_decisions_total` metric already labels — no new metric here.

Tests in `authz_internal_test.go` / `authz_test.go`: a table over the four
`authMethod` values driving `requireSession` around a recording handler —
only `authSession` reaches it; the other three get 403 with the message
above. Plus one `subjectFromActor` case asserting `Groups` round-trips.

- [ ] `go test ./internal/api -run 'TestRequireSession|TestSubjectFromActor' -count=1`
      — expect `ok`.
- [ ] Commit: `Session-only gate for approval decisions (029 §7.3)`.

### Task 5 — PR ingest writes and resolves approval rows

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

One mutation, every surface it has: store writes (Task 3's tx functions),
event provenance (free — the apply callbacks already run inside the GitHub
`RecordEvent` transaction), and the hooks metric. All in
`internal/hooks/github.go`, `internal/store/changes.go`, and
`internal/hooks/metrics.go`.

Changes:

1. **Author capture.** Add `Author string` to `store.PullRequest`; `UpsertPR`
   writes it (insert and update — the login does not change, but a
   pre-column row gets backfilled on the next delivery) and `scanPR`/
   `prColumns` read it. `applyPullRequest` parses `pull_request.user.login`
   into it.
2. **Awaiting on open.** In `applyPullRequest`'s
   `action == "opened" || action == "ready_for_review"` branch, after the
   existing task-state transition and only when `pr.TaskID != nil`: resolve
   `required_actor` — first login in `pull_request.requested_reviewers[]`
   for which `store.ActorIDForGitHubLogin` returns non-"" — then
   `store.InsertAwaitingApproval(tx, now, "pr", entityID, gh.Head.SHA, nil,
   requiredActor)` with `entityID := repo + "#" + strconv.FormatInt(gh.Number, 10)`.
   Correlation must never fail the delivery: an insert conflict is already a
   no-op.
3. **Re-request edge** (029 §7.1's `changes_requested → awaiting`). New
   `action == "review_requested"` branch in `applyPullRequest` (add the
   action to the parsed fields, nothing to the switch's other cases): for a
   task-correlated PR with an open approval, `ReopenApproval` if the state is
   `changes_requested`, and `SetRequiredActor` from
   `requested_reviewer.login` when it resolves and the row has none.
4. **Resolve on review.** In `applyReview`, after `UpsertReview`: load the PR
   (`getPRTx`); if task-correlated and `state` is `approved` or
   `changes_requested` (not `commented`), find the open row
   (`OpenApprovalForEntity`) and `resolveApproval` it with
   `resolvingActor := ActorIDForGitHubLogin(reviewer)` (NULL when
   unresolvable) at `submittedAt`. A missing open row is a no-op, not an
   error.
5. **Metric.** `internal/hooks/metrics.go`: counter
   `worklode_approvals_ingest_total` with one `action` label, values from the
   fixed set `opened`, `resolved`, `reopened` — incremented inside the apply
   callback just before it returns nil, so only applies that reach commit
   intent count (the delivery counter's `result="error"` already records
   failed transactions). Nil-safe like the existing `Metrics` methods; test
   in `metrics_test.go`.

Tests in `internal/hooks/github_test.go`, using the existing `env` harness
(`newEnv`, `seedTask`, `deliverOK`, `rawQueryString`) and new testdata
fixtures modelled on the existing `testdata/github/` ones (add
`user.login` and `requested_reviewers` to the PR-opened fixture; a
`review_requested` fixture). First test:

```go
func TestPROpenedMaterializesAwaitingApproval(t *testing.T) {
	e := newEnv(t)
	taskID := e.seedTask(t)
	e.claimTask(t, taskID) // branch-correlated PR fixture targets this task
	deliverOK(t, e, "pull_request", "d-appr-1", "pr_opened.json")

	if got := e.rawQueryString(t,
		`SELECT state FROM approvals WHERE entity_kind = 'pr' AND entity_id = $1`,
		"acme/site#7"); got != "awaiting" {
		t.Errorf("approval state = %q, want awaiting", got)
	}
}
```

Also cover: redelivery stays one row; an uncorrelated PR writes no row; an
`approved` review resolves the row (state, `resolving_actor` from a seeded
actor with `expected_github_login`, `resolved_at`); a `changes_requested`
review then `review_requested` reopens it; a `commented` review leaves it
`awaiting`; `pull_requests.author` lands from the fixture.

- [ ] `go test ./internal/hooks -count=1` against Postgres — expect `ok`.
- [ ] `go test ./internal/store -count=1` — expect `ok` (UpsertPR change).
- [ ] Commit: `PR ingest materializes and resolves approvals (029 §7.3)`.

### Task 6 — Store readers: the queue and the per-project counts

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

Two UI-neutral bulk readers in `internal/store/approvals.go`. One fact
family, one reader each; plan D consumes the second unchanged.

```go
// AwaitingApproval is one queue row: the approval plus what a person needs
// to act on it. PR-kind only until part 2 adds more entity kinds.
type AwaitingApproval struct {
	Approval
	PRTitle, PRURL    string
	PRAuthor          string  // "" when unknown (pre-column row)
	TaskID, ProjectID string
	ProjectName       string
	RequiredActorName *string // display_name, for "awaiting <who>"
}

// ListAwaitingApprovals returns every awaiting approval joined to its PR,
// task, and project, oldest first. The join is per entity_kind; today that
// is one branch ('pr' via pull_requests -> tasks -> projects).
func (s *Store) ListAwaitingApprovals(ctx context.Context) ([]AwaitingApproval, error)

// ApprovalCount is plan D's Home input: awaiting approvals naming the actor
// (required_actor = actorID) or a Keycloak group the actor is in
// (required_role = ANY(groups)), counted per project.
type ApprovalCount struct {
	ProjectID string
	Count     int
}

func (s *Store) ApprovalsAwaiting(ctx context.Context,
	actorID string, groups []string) ([]ApprovalCount, error)
```

`ApprovalsAwaiting` with `actorID == ""` and empty groups returns no rows
(the open-instance subject matches nothing — plan D's honest degradation).
The `entity_id` join uses the same `repo || '#' || number` spelling the
ingest writes; keep both in one place by exporting
`PREntityID(repo string, number int64) string` from `approvals.go` and using
it in Task 5.

First test: seed two projects/tasks/PRs via the existing store fixtures,
insert three awaiting rows (one `required_actor = A`, one
`required_role = 'science-leads'`, one naming nobody), then assert
`ApprovalsAwaiting(A, []string{"science-leads"})` counts 2 across the right
projects, `ApprovalsAwaiting(A, nil)` counts 1, and
`ApprovalsAwaiting("", nil)` counts 0. A second test asserts
`ListAwaitingApprovals` orders oldest-first and excludes resolved rows.

- [ ] `go test ./internal/store -run 'TestApprovalsAwaiting|TestListAwaitingApprovals' -count=1`
      against Postgres — expect `ok`.
- [ ] Commit: `Approvals readers: queue rows and per-project counts`.

### Task 7 — The `/reviews` queue page replaces its placeholder

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [6]
```

The tracer: the first page joining migration, store, and shell over real
data. Read-only in this task — the decide forms arrive with their route in
Task 8, so no rendered control ever points at an unregistered route.

- `internal/ui/approvals.templ`: `Approvals(v ApprovalsView)` rendering the
  global-destination shell (same `.main`/`.canvas` frame as
  `placeholder.templ`'s global branch until plan A's `.shell` unification
  lands — whichever is in the tree when this task runs, match the current
  global pages), a `<ul class="approvalq">` of rows — PR title linking to
  `PRURL` (GitHub remains the review surface: jump-out link, 029 §7.3),
  project name linking to `/projects/{ProjectID}`, task id, "awaiting
  <RequiredActorName>" when set, and relative age — and the honest empty
  state: `<p class="muted">No approvals are waiting.</p>`. View types in the
  same file or `views.go` beside the existing ones.
- `internal/api/render.go`: `approvalsView(rows []store.AwaitingApproval,
  ...)` mapping.
- `internal/api/web.go`: `reviewsPage` handler calling
  `s.st.ListAwaitingApprovals`; `internal/api/server.go`: replace the
  `globalPlaceholder("reviews", ...)` registration with
  `r.web("GET /reviews", s.navWrap("reviews", s.reviewsPage))`. The
  `routeGuards` row (`guarded(permWebRead)`) is unchanged.
- Invariants that must survive (asserted in `web_test.go`): exactly one
  `aria-current="page"` per page; the `>Reviews<` global nav marker. Update
  the test that asserted the reviews placeholder message.

First test, in `internal/api/web_test.go` (existing `newTestServer` harness;
seed one awaiting row through the store the way sibling web tests seed tasks
— this is a unit test, not e2e, so store seeding is fine here):

```go
func TestReviewsPageListsAwaitingApprovals(t *testing.T) {
	st, h := newTestServer(t)
	seedAwaitingPRApproval(t, st, "acme/site#7", "Fix the widget")

	body := getOK(t, h, "/reviews")
	for _, want := range []string{"Fix the widget", "acme/site#7", `aria-current="page"`} {
		if !strings.Contains(body, want) {
			t.Errorf("reviews page missing %q", want)
		}
	}
}
```

- [ ] `go generate ./...` after editing the `.templ`; commit the regenerated
      `*_templ.go` (and `app.css` if the stylesheet changed).
- [ ] `go test ./internal/api -run 'TestReviews|TestWeb' -count=1` — expect
      `ok`, including the nav-marker and aria-current assertions.
- [ ] Commit: `Reviews page: the awaiting-approvals queue (029 §7.1)`.

### Task 8 — The web approve act: `POST /approvals/{id}/decide`

```yaml
kind: feature
priority: critical
skills:
  - superpowers:test-driven-development
blockedBy: [2, 4, 7]
```

The plan's one web mutation, landed across every surface at once: store
write + event, permission + route guard, CSRF, form controls, and metric.

**Store** (`internal/store/approvals.go`, `errors.go`): sentinel errors
`ErrSelfApproval`, `ErrNotQualified`, `ErrApprovalResolved`, and the
tx-scoped decision write composing Task 2's pure rules and Task 3's
`resolveApproval`:

```go
type DecideInput struct {
	ApprovalID int64
	Decision   string   // approve | request_changes | reject
	ActorID    string   // the session actor; never "" (requireSession holds)
	Groups     []string // the actor's stored claim, fresh as of this login
	Now        time.Time
}

// DecideApproval enforces, in order: the row exists (ErrNotFound) and is
// open — awaiting or changes_requested (ErrApprovalResolved); the decider
// is qualified for required_role (ErrNotQualified); and, for entity_kind
// 'pr', the decider is not the PR's author, compared via the decider's
// expected_github_login against pull_requests.author (ErrSelfApproval;
// 029 §7.1's default refusal — the policy-permitted exception flow is
// deferred to part 2). Then it resolves the row.
func DecideApproval(tx *sql.Tx, in DecideInput) (*Approval, error)
```

**API** (`internal/api/`): permission const `permApprovalDecide
Permission = "approval.decide"` with a `grants` entry `{RoleUser,
RoleAdmin}`; `routeGuards` row `"POST /approvals/{id}/decide":
guarded(permApprovalDecide)` (NewServer's boot checks force both);
registration in `server.go`:

```go
r.web("POST /approvals/{id}/decide",
	s.navWrap("approval_decide", s.requireSession(s.decideApproval)))
```

Handler `decideApproval` in `webform.go`, following
`createTaskFromForm` exactly: `sameOriginForm` → 403; `parseWebForm`;
`DecisionState(r.PostFormValue("decision"))` → 422 on `!ok`; parse `{id}` →
404 on non-integer; load the subject's groups from `subjectFrom(r)`
(`Subject.Groups`, Task 4); wrap the write:

```go
_, _, err := s.st.RecordEvent(ctx, "web", extID, "approval.decided", payload,
	func(tx *sql.Tx, _ int64) error {
		_, err := store.DecideApproval(tx, in)
		return err
	})
```

with `payload` marshalling `{approval_id, decision, actor}`. Error mapping:
`ErrNotFound` → 404, `ErrApprovalResolved` → 409, `ErrSelfApproval` /
`ErrNotQualified` → 403 (message states the reason), other → 500. Success →
`http.Redirect(w, r, "/reviews", http.StatusSeeOther)`.

**UI** (`internal/ui/approvals.templ`): per row, one form —

```html
<form method="post" action={ "/approvals/" + strconv.FormatInt(a.ID, 10) + "/decide" } class="decide">
	<button type="submit" name="decision" value="approve" class="btn primary">Approve</button>
	<button type="submit" name="decision" value="request_changes" class="btn">Request changes</button>
	<button type="submit" name="decision" value="reject" class="btn ghost">Reject</button>
</form>
```

Native buttons in a plain POST form: keyboard-operable with no JavaScript,
as 032 §10 requires of a primary workflow.

**Metric** (`internal/api/metrics.go`): counter
`worklode_approval_decisions_total{decision,outcome}` — `decision` ∈
`{approve, request_changes, reject, invalid}`, `outcome` ∈ `{resolved,
refused_self, refused_role, conflict, not_found, invalid, error}` (the
session refusal is already counted by `worklode_authz_decisions_total`
reason `session_required`, Task 4). Nil-safe method `observeApprovalDecision`;
test beside the existing metric tests.

Tests in `internal/api/webform_test.go` + `oidcweb_test.go` patterns
(`newOIDCServer` + `signSession` for a session subject; plain
`newTestServer` for the open instance). First test — the property 029 §7.3
exists for:

```go
func TestDecideApprovalRefusesBearerAndOpenSubjects(t *testing.T) {
	// Open instance: authOpen subject reaches webGuard but not the handler.
	st, h := newTestServer(t)
	id := seedAwaitingPRApproval(t, st, "acme/site#7", "Fix the widget")

	rr := postForm(t, h, fmt.Sprintf("/approvals/%d/decide", id),
		url.Values{"decision": {"approve"}})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("open-instance decide = %d, want 403", rr.Code)
	}
	if got := approvalState(t, st, id); got != "awaiting" {
		t.Errorf("state = %q after refused decide, want awaiting", got)
	}
}
```

Also cover, each its own test: a session subject approves → 303, state
`approved`, `resolving_actor` set, one `approval.decided` event row; the PR
author's own session (actor `expected_github_login` == seeded
`pull_requests.author`) → 403 and untouched row; `required_role` the subject's
groups lack → 403; a second decide → 409; `decision=frobnicate` → 422;
cross-origin (`Sec-Fetch-Site: cross-site`) → 403; metric increments.

- [ ] `go generate ./...`; commit regenerated artifacts.
- [ ] `go test ./internal/api -run 'TestDecideApproval|TestApprovalMetric' -count=1`
      against Postgres — expect `ok`.
- [ ] `go test ./... -count=1` — green; the router boot checks prove the new
      route/guard pair is complete.
- [ ] Commit: `Web approve act: session-gated POST /approvals/{id}/decide (029 §7.3)`.

### Task 9 — Narrow-width and keyboard pass on the approval workflow (032 §10)

```yaml
kind: chore
priority: medium
skills: []
blockedBy: [8]
```

032 §10 names approval as a workflow that must work at narrow browser widths
using reduced columns and full-page detail, not a compressed desktop grid.
This task is that demonstration for the surface this plan built.

In `internal/ui/styles/app.tailwind.css`: `.approvalq` rows lay out
metadata + decide buttons in a row on wide viewports and stack them
(`flex-direction: column`, buttons full-width in source order
Approve/Request changes/Reject) below `720px`; buttons keep ≥24px pointer
targets; state is conveyed by the text labels, never colour alone; focus
styling is the existing global rule (do not override it).

Verification (headless Playwright — do not drive a personal browser):

- [ ] Rebuild `app.css` with the pinned CLI; `go generate ./...`; start the
      compose stack (`LODE_WEB_OPEN=true`) and seed one awaiting approval by
      delivering the Task 5 PR-opened fixture as a signed webhook (public
      surface; `e2e/smoke_test.go`'s `sign` shows the header shape).
- [ ] `npx playwright screenshot --viewport-size=375,740 http://localhost:8080/reviews /tmp/reviews-375.png`
      and the same at `--viewport-size=1440,900` — expect: at 375px the row
      stacks with no horizontal scroll and all three buttons visible; at
      1440px the row layout. Attach both to the PR.
- [ ] Keyboard check on the 1440px page: Tab reaches the PR link, project
      link, and each decide button in order; Enter on a button submits
      (lands on the 403 page under `LODE_WEB_OPEN` — the gate, not the CSS,
      refuses).
- [ ] Commit: `Approval queue narrow-width layout (032 §10)`.

### Task 10 — e2e: the approval lifecycle through public surfaces

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [5, 8]
```

`e2e/approvals_test.go` (build tag `e2e`), using the `smoke_test.go` harness
(`api.NewServer` under `httptest`, `deliverGitHub`, `getPage`) and only
public surfaces: bootstrap token → project/repo/task via `/api/v1`, claim so
the branch correlates, then:

1. Signed `pull_request` `opened` delivery → `GET /reviews` contains the PR
   title (the awaiting row is visible).
2. Extract the decide form action (`/approvals/<id>/decide`) from the page
   HTML; POST it with `decision=approve` and no session → 403, and
   `GET /reviews` still lists the row (the e2e stack runs with
   `LODE_WEB_OPEN=true` and no OIDC issuer, so this proves the session gate
   on the real wire; the session-authenticated success path is proven in
   `internal/api` against the fake issuer — an e2e Keycloak is out of scope).
3. Signed `pull_request_review` `submitted`/`approved` delivery →
   `GET /reviews` no longer lists the row.

```go
//go:build e2e
```

- [ ] `go test -race -count=1 -tags e2e ./e2e/ -run TestApprovalLifecycle`
      against Postgres — expect `ok`.
- [ ] Full suite: `go test -race -count=1 -tags e2e ./e2e/` — green.
- [ ] Commit: `e2e: approval materialized, gated, and resolved over public surfaces`.

## Verification

- `go test ./... -count=1` green with Postgres reachable (a silent skip
  proved nothing).
- `go test -race -count=1 -tags e2e ./e2e/` green.
- `curl -s localhost:9090/metrics | grep -E 'worklode_(approvals_ingest|approval_decisions)_total'`
  on the admin listener shows both new families after exercising the flows.
- The two screenshots from Task 9 attached to the PR.
- `./scripts/secmeta.py docs/plans/2026-08-14-approvals-1-table-and-web-act.md`
  reports no errors.

## Deferred — part 2 of this series, stated so the gap is a decision

- 029 §7.2 entirely: flows, the rule engine, the per-project `approval_flow`
  snapshot, rule-created rows owned by the system `worklode` actor.
- Revision-binding enforcement: reopen-on-material-change and the explicit
  impact review (029 §7.1).
- The policy-permitted self-review exception flow (029 §7.1) — this plan
  refuses author self-approval unconditionally.
- The CI gate and the CMS gate (029 §7.3 bullets 2–3).
- Additional `entity_kind` values (`document`, `deliverable`, `task`) and
  widening the `(entity_kind, entity_id, subject_revision)` unique key for
  multi-lane reviews on one revision.
