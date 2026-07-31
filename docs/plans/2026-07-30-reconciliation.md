# Reconciliation & setup diagnosis — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Recover task activity the webhook ingestion path missed (`lode
reconcile`), tell an operator when ingestion is broken for a repo (`lode
project doctor`), and tell a developer why their own setup misbehaves
(`lode doctor`).

**Architecture:** One migration adds `events.applied_at` (apply-completion
marker) and `project_repos.mapped_at` (so doctor can spot deliveries that
predate a mapping). The webhook apply routing moves off the HTTP handler onto
a transport-independent `applier`, shared by the webhook path and a new
replayer (`hooks.Replay`, engine 1). Engine 2 (`reconcile.Poll`) asks GitHub
the current truth about candidate tasks through per-repo installation-token
clients (`githubauth.RepoClient`), writes missing facts through the existing
upserts inside one `source='system'` event per run, then lets
`store.ResolveDelivery` advance states. Three new endpoints —
`GET /api/v1/whoami`, `GET /api/v1/repos/doctor`, `POST /api/v1/reconcile` —
back three new CLI commands.

**Tech Stack:** Go 1.x, cobra CLI, `net/http` mux (Go 1.22 routing patterns),
PostgreSQL via `database/sql`, standard-library testing, `httptest` fakes for
the GitHub API.

**Spec:** `docs/specs/013-reconciliation.md`, read with its amendments from
`docs/specs/014-design-documents-as-graph-objects.md` §6: **engine 3
(spec-doc drift) and the `task_docs` table are superseded and must not be
built.** Only `events.applied_at` survives from the spec's data-model
section. `docs/plans/2026-07-28-lode-install-reorg.md` edited this spec's
text (the `lode install` reference) but implements none of it.

---

## Already implemented vs. remaining

Shipped, reused as-is:

- Idempotent event log with `*.ignored` recording for unmapped repos —
  `internal/store/events.go:34` (`RecordEvent`), `internal/hooks/github.go:129-146`.
- Fact-driven delivery lifecycle — `internal/store/delivery_resolve.go:78`
  (`ResolveDelivery`), fact writers `UpsertPR` (`internal/store/changes.go:151`),
  `InsertTaskCommit` / `AppendMainCommit` (`internal/store/delivery.go:33,91`).
- GitHub App installation tokens with test-overridable `BaseURL` —
  `internal/githubauth/app.go:29-33,94`.
- Auth middleware and admin gate — `internal/api/server.go:374,403`.
- Project scoping, config walk-up, keychain token store — `internal/cli/`.

Not implemented anywhere: `events.applied_at`, replay, GitHub polling,
`/api/v1/whoami`, `/api/v1/reconcile`, `/api/v1/repos/doctor`, `lode doctor`,
`lode project doctor`, `lode reconcile`. That is this plan's scope.

Design calls this plan makes (record here, not re-litigated in tasks):

- **`project_repos.mapped_at` is added** even though the spec's data-model
  section lists only `applied_at`: the acceptance criterion "a repo whose
  last webhook predates its mapping" is undecidable without a mapping
  timestamp. Existing rows backfill to epoch so no current repo retroactively
  alarms; new rows default to `now()` so `addRepo` needs no change.
- **`applied_at` backfill**: pre-existing non-`.ignored` events were applied
  live, so they backfill to `received_at`; `.ignored` events stay NULL and
  are exactly the replay candidates.
- **`--task` does not bound engine 1**: an ignored event's task binding is
  unknown before its apply runs. When `task` is set, replay is skipped and
  only engine 2 runs.
- **Replayed events keep their stored type** (`push.ignored` stays
  `push.ignored`); `applied_at` is the completion marker, and history stays
  intact.

## Not in this plan (owned elsewhere)

- Engine 3 / `task_docs` / spec-drift reporting — superseded by spec 014 §6
  (stale-claim query over `.worklode/implements.yaml`); belongs to the 014
  plan.
- Architectural drift (`lode drift`) — spec 007.
- Promoting untracked work into tasks — `lode inbox`, already shipped.
- KG groundwork (`internal/kg/*`) — `docs/plans/2026-07-30-platform-graph-design.md`.
- Scope-chain resolution — `docs/plans/2026-07-30-project-scoping.md`
  (shipped; `lode doctor` only reads `cfg.CurrentProject`, it does not
  re-implement resolution).
- Scheduled/continuous reconcile runs — spec 013 open question 3, explicitly
  out of scope; the endpoint is synchronous and on-demand.

---

**Migration number:** provisional. Ids are assigned sequentially at execution
time, in the order plans are actually executed, by the migration-id script on
main. `0008` is the current next-free (`0001`–`0005` on main; `0006` and `0007`
claimed by the in-flight `task-hierarchy` and `skills-task3` worktrees), so the
steps below use it and expect renumbering.

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `deploy/base/migrations/0008_reconciliation.up.sql` | `events.applied_at`, `project_repos.mapped_at`, backfills, partial index |
| `deploy/base/migrations/0008_reconciliation.down.sql` | drop both columns and the index |
| `internal/store/reconcile.go` | `MarkEventApplied`, `UnappliedGitHubEvents`, `RepoIngestionHealth`, `UnmappedSenders`, `PollCandidates`, `UnlandedTaskCommits` |
| `internal/store/reconcile_test.go` | the above against ephemeral Postgres |
| `internal/hooks/apply.go` | `applier` — the transport-independent apply router (moved off `githubHandler`) |
| `internal/hooks/replay.go` | engine 1: `Replay` over unapplied github events |
| `internal/hooks/replay_test.go` | replay after mapping; idempotence; provenance; dry-run |
| `internal/githubauth/repoclient.go` | `RepoClient`: per-repo installation-token client — PR, default branch, compare, releases |
| `internal/githubauth/repoclient_test.go` | every method against an `httptest` GitHub |
| `internal/reconcile/poll.go` | engine 2: gather GitHub truth, apply through one `reconcile.poll` system event |
| `internal/reconcile/poll_test.go` | merged-while-down repair; convergence; dry-run; attribution |
| `internal/api/reconcile.go` | handlers: `whoami`, `reposDoctor`, `reconcile` (+ `parseSince`) |
| `internal/api/reconcile_test.go` | auth/admin gates, since parsing, replay wiring, poll-skipped |
| `internal/cmd/doctor.go` | `lode doctor` — client-side checks, each failure names its fix |
| `internal/cmd/doctor_test.go` | table-driven broken-setup fixtures, exit code + fix text |
| `internal/cmd/reconcile.go` | `lode reconcile` cobra glue |
| `internal/cmd/reconcile_test.go` | flag → request body wiring against a fake server |

**Modified files**

| Path | Change |
|---|---|
| `internal/hooks/github.go` | handler holds an `applier`; mapped-repo applies wrapped to set `applied_at` |
| `internal/hooks/push.go`, `internal/hooks/deployment.go` | apply methods' receiver becomes `*applier` (mechanical) |
| `internal/api/server.go` | register the three routes |
| `internal/cli/client.go` | `ConfigOrigins`, `WhoAmI`, `ReposDoctor`, `Reconcile` |
| `internal/cmd/project.go` | `lode project doctor [repo]` subcommand |
| `README.md` | document `doctor`, `project doctor`, `reconcile` |

**Test commands**

- Store/hooks/API/cmd/reconcile suites need Postgres (`store.OpenTestStore`):
  `go test ./internal/store/... ./internal/hooks/... ./internal/api/... ./internal/cmd/... ./internal/reconcile/...`
- No Postgres needed: `go test ./internal/githubauth/... ./internal/cli/...`
- Everything: `go test ./...`

---

## Task 1: Migration 0008 and the event-marker store functions

**Files:**
- Create: `deploy/base/migrations/0008_reconciliation.up.sql`
- Create: `deploy/base/migrations/0008_reconciliation.down.sql`
- Create: `internal/store/reconcile.go`
- Test: `internal/store/reconcile_test.go`

- [ ] **Step 1: Write the migration**

`deploy/base/migrations/0008_reconciliation.up.sql`:

```sql
-- Reconciliation (docs/specs/013-reconciliation.md, amended by 014 §6):
-- events.applied_at marks a completed apply; project_repos.mapped_at lets
-- project doctor spot deliveries that predate the mapping.

ALTER TABLE events ADD COLUMN applied_at timestamptz;

-- Pre-existing non-ignored events were applied live by the webhook path.
-- .ignored events stay NULL: they are exactly the replay candidates.
UPDATE events SET applied_at = received_at WHERE type NOT LIKE '%.ignored';

CREATE INDEX events_unapplied ON events (id) WHERE applied_at IS NULL;

ALTER TABLE project_repos ADD COLUMN mapped_at timestamptz NOT NULL DEFAULT now();

-- Existing mappings predate the column; epoch keeps them from retroactively
-- flagging every old delivery as pre-mapping.
UPDATE project_repos SET mapped_at = to_timestamp(0);
```

`deploy/base/migrations/0008_reconciliation.down.sql`:

```sql
DROP INDEX events_unapplied;
ALTER TABLE events DROP COLUMN applied_at;
ALTER TABLE project_repos DROP COLUMN mapped_at;
```

- [ ] **Step 2: Write the failing store test**

`internal/store/reconcile_test.go` (`package store`, internal — matching
`inbox_test.go`):

```go
package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// recordGitHubEvent inserts one github-source event with the given type and
// payload, returning its id. apply is nil: applied_at stays NULL, as it does
// for a real *.ignored delivery.
func recordGitHubEvent(t *testing.T, s *Store, externalID, typ, payload string) int64 {
	t.Helper()
	id, _, err := s.RecordEvent(context.Background(), "github", externalID, typ,
		[]byte(payload), nil)
	if err != nil {
		t.Fatalf("record event %s: %v", externalID, err)
	}
	return id
}

func TestMarkEventAppliedAndUnappliedQuery(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	early := recordGitHubEvent(t, s, "d-1", "issues.opened.ignored",
		`{"repository":{"full_name":"acme/app"}}`)
	late := recordGitHubEvent(t, s, "d-2", "push.ignored",
		`{"repository":{"full_name":"acme/app"}}`)
	other := recordGitHubEvent(t, s, "d-3", "push.ignored",
		`{"repository":{"full_name":"acme/other"}}`)
	applied := recordGitHubEvent(t, s, "d-4", "issues.opened.ignored",
		`{"repository":{"full_name":"acme/app"}}`)
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return MarkEventApplied(tx, applied, s.Now())
	}); err != nil {
		t.Fatalf("mark applied: %v", err)
	}
	// A non-github source is never a replay candidate.
	if _, _, err := s.RecordEvent(ctx, "cli", "d-5", "task.created", nil, nil); err != nil {
		t.Fatalf("record cli event: %v", err)
	}

	got, err := s.UnappliedGitHubEvents(ctx, UnappliedFilter{})
	if err != nil {
		t.Fatalf("unfiltered: %v", err)
	}
	if ids := eventIDs(got); len(ids) != 3 || ids[0] != early || ids[1] != late || ids[2] != other {
		t.Fatalf("unfiltered ids = %v; want [%d %d %d] in id order", ids, early, late, other)
	}

	got, err = s.UnappliedGitHubEvents(ctx, UnappliedFilter{Repo: "acme/app"})
	if err != nil {
		t.Fatalf("repo filter: %v", err)
	}
	if ids := eventIDs(got); len(ids) != 2 || ids[0] != early || ids[1] != late {
		t.Fatalf("repo filter ids = %v; want [%d %d]", ids, early, late)
	}

	cutoff := time.Now().Add(time.Hour)
	got, err = s.UnappliedGitHubEvents(ctx, UnappliedFilter{Since: &cutoff})
	if err != nil {
		t.Fatalf("since filter: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("since-in-the-future returned %d events; want 0", len(got))
	}
}

func eventIDs(evs []Event) []int64 {
	out := make([]int64, len(evs))
	for i, e := range evs {
		out[i] = e.ID
	}
	return out
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/store/ -run TestMarkEventApplied`
Expected: FAIL — `undefined: MarkEventApplied` (and the migration is applied
by `OpenTestStore`, so the SQL itself is exercised too).

- [ ] **Step 4: Write the implementation**

`internal/store/reconcile.go`:

```go
// Reconciliation queries (docs/specs/013-reconciliation.md): the applied_at
// completion marker, the replay candidate set, and the ingestion-health and
// poll-candidate reads added by later tasks in the same plan.

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// MarkEventApplied records that an event's apply completed, by either the
// webhook path or the replayer. Must run in the same transaction as the
// apply so the marker commits or rolls back with the effect it describes.
func MarkEventApplied(tx *sql.Tx, eventID int64, at time.Time) error {
	if _, err := tx.Exec(`UPDATE events SET applied_at = $2 WHERE id = $1`,
		eventID, at.UTC()); err != nil {
		return fmt.Errorf("mark event %d applied: %w", eventID, err)
	}
	return nil
}

// UnappliedFilter bounds the replay candidate set. Zero values disable each
// filter. Repo matches the delivery payload's repository.full_name.
type UnappliedFilter struct {
	Repo  string
	Since *time.Time
}

// UnappliedGitHubEvents returns github-source events whose apply has not
// completed — *.ignored deliveries and anything the replayer has not reached
// yet — oldest first, so replay preserves arrival order.
func (s *Store) UnappliedGitHubEvents(ctx context.Context, f UnappliedFilter) ([]Event, error) {
	q := `SELECT id, source, external_id, type, payload, received_at
	      FROM events WHERE source = 'github' AND applied_at IS NULL`
	var args []any
	if f.Repo != "" {
		args = append(args, f.Repo)
		q += fmt.Sprintf(` AND payload->'repository'->>'full_name' = $%d`, len(args))
	}
	if f.Since != nil {
		args = append(args, f.Since.UTC())
		q += fmt.Sprintf(` AND received_at >= $%d`, len(args))
	}
	q += ` ORDER BY id`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("unapplied events: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Source, &e.ExternalID, &e.Type, &e.Payload, &e.ReceivedAt); err != nil {
			return nil, fmt.Errorf("scan unapplied event: %w", err)
		}
		e.ReceivedAt = e.ReceivedAt.UTC()
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("unapplied events: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 5: Run the tests, including the migration round trip**

Run: `go test ./internal/store/ -run 'TestMarkEventApplied|Migrat'`
Expected: PASS — the existing up/down round-trip check covers the new
migration pair (the spec's own testing note).

- [ ] **Step 6: Commit**

```bash
git add deploy/base/migrations/0008_reconciliation.up.sql \
        deploy/base/migrations/0008_reconciliation.down.sql \
        internal/store/reconcile.go internal/store/reconcile_test.go
git commit -m "Add events.applied_at and project_repos.mapped_at"
```

---

## Task 2: The webhook path sets applied_at

**Files:**
- Modify: `internal/hooks/github.go:141-147` (the apply wiring in `ServeHTTP`)
- Test: `internal/hooks/github_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/hooks/github_test.go` (helpers `newEnv`, `deliver`,
`deliverBody`, `e.rawQueryInt` already exist there):

```go
// TestAppliedAtMarksMappedDeliveries: a mapped repo's delivery gets
// applied_at (even for an event type with no typed-table effect); an
// unmapped repo's .ignored delivery does not.
func TestAppliedAtMarksMappedDeliveries(t *testing.T) {
	e := newEnv(t)

	rr := deliver(t, e.h, "issues", "d-applied", "issues_opened.json")
	if rr.Code != http.StatusOK {
		t.Fatalf("mapped delivery: %d %s", rr.Code, rr.Body.String())
	}
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM events WHERE external_id = 'd-applied' AND applied_at IS NOT NULL`); n != 1 {
		t.Fatalf("mapped delivery applied_at set = %d rows, want 1", n)
	}

	// "ping" routes to a nil apply but the repo is mapped: still marked, so
	// it never shows up as awaiting replay.
	rr = deliverBody(t, e.h, "ping", "d-ping", []byte(`{"repository":{"full_name":"sunstoneinstitute/demo"}}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("ping delivery: %d %s", rr.Code, rr.Body.String())
	}
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM events WHERE external_id = 'd-ping' AND applied_at IS NOT NULL`); n != 1 {
		t.Fatalf("nil-apply delivery applied_at set = %d rows, want 1", n)
	}

	rr = deliverBody(t, e.h, "issues", "d-ignored", []byte(`{
		"action": "opened",
		"repository": {"full_name": "other/repo"},
		"issue": {"number": 1, "title": "x", "state": "open", "html_url": "u"}
	}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("ignored delivery: %d %s", rr.Code, rr.Body.String())
	}
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM events WHERE external_id = 'd-ignored' AND applied_at IS NULL`); n != 1 {
		t.Fatalf("ignored delivery applied_at NULL = %d rows, want 1", n)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/hooks/ -run TestAppliedAtMarks`
Expected: FAIL — no delivery sets `applied_at` yet (0 rows where 1 wanted).

- [ ] **Step 3: Wrap the apply**

In `internal/hooks/github.go`, replace the apply wiring in `ServeHTTP`
(currently lines 141-147):

```go
	var apply func(tx *sql.Tx, eventID int64) error
	if ignored {
		typ += ".ignored"
	} else {
		// Mapped-repo deliveries always get an apply — at minimum the
		// applied_at marker — so a nil-routed event (unknown type, unhandled
		// action) is still recorded as done rather than awaiting replay.
		apply = markApplied(h.st, h.applyFunc(event, env, body))
	}
```

and add, below `applyFunc`:

```go
// markApplied wraps an apply (possibly nil) so the event's applied_at is set
// in the same transaction, by the webhook path and replayer alike.
func markApplied(st *store.Store, inner func(tx *sql.Tx, eventID int64) error) func(tx *sql.Tx, eventID int64) error {
	return func(tx *sql.Tx, eventID int64) error {
		if inner != nil {
			if err := inner(tx, eventID); err != nil {
				return err
			}
		}
		return store.MarkEventApplied(tx, eventID, st.Now())
	}
}
```

- [ ] **Step 4: Run the hooks suite**

Run: `go test ./internal/hooks/...`
Expected: PASS, including all pre-existing tests.

- [ ] **Step 5: Commit**

```bash
git add internal/hooks/github.go internal/hooks/github_test.go
git commit -m "Set events.applied_at on the webhook path"
```

---

## Task 3: Extract the apply router off the HTTP handler

The spec's required refactor (`013` §Engine 1) and its highest-risk change:
apply routing is a method on `githubHandler`, bound to the HTTP envelope.
Replay needs it transport-independent. This task is behavior-preserving —
no test changes, the whole hooks suite must stay green.

**Files:**
- Create: `internal/hooks/apply.go`
- Modify: `internal/hooks/github.go` (handler delegates to `applier`)
- Modify: `internal/hooks/push.go`, `internal/hooks/deployment.go` (receivers)

- [ ] **Step 1: Create the applier**

`internal/hooks/apply.go`:

```go
// The transport-independent apply router. The webhook handler and the
// replayer (replay.go) both route events through an applier, so a replayed
// event produces exactly the typed-table effect a live delivery would have.

package hooks

import (
	"database/sql"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// applier routes a mapped-repo event to its per-event apply callback,
// independent of how the event arrived.
type applier struct {
	st *store.Store
}

// applyForType routes a stored event type (e.g. "pull_request.closed" or
// "push.ignored") the way applyFunc routes a live delivery. The GitHub event
// name is the type's first dot-separated segment — event names themselves
// never contain dots — and a trailing ".ignored" only recorded the
// unmapped-at-arrival classification.
func (a *applier) applyForType(typ string, env envelope, body []byte) func(tx *sql.Tx, eventID int64) error {
	event, _, _ := strings.Cut(typ, ".")
	return a.applyFunc(event, env, body)
}
```

(with `"strings"` in the import block).

- [ ] **Step 2: Move the routing and apply methods**

Mechanical receiver change, no body edits beyond the receiver variable:

1. Move `applyFunc` (`internal/hooks/github.go:167-207`) into
   `internal/hooks/apply.go` as `func (a *applier) applyFunc(...)`.
2. Change every remaining `func (h *githubHandler) apply...` method to
   `func (a *applier) apply...`, replacing `h.st` with `a.st` in the bodies.
   Find them all with:
   `grep -n 'func (h \*githubHandler)' internal/hooks/*.go`
   — expected: `applyPullRequest`, `applyReview`, `applyWorkflowRun`,
   `applyRelease` in `github.go`, plus the push and deployment applies in
   `push.go` and `deployment.go`. `applyIssue` is already package-level;
   leave it. `ServeHTTP` stays on `githubHandler`.
3. Give the handler an applier and delegate:

```go
type githubHandler struct {
	st     *store.Store
	ap     *applier
	secret string
	log    *slog.Logger
}
```

   In `NewGitHubHandler`: `return &githubHandler{st: st, ap: &applier{st: st}, secret: secret, log: log}`.
   In `ServeHTTP` (the Task 2 wiring): `apply = markApplied(h.st, h.ap.applyFunc(event, env, body))`.

- [ ] **Step 3: Run the full hooks suite to prove behavior is unchanged**

Run: `go build ./... && go test ./internal/hooks/...`
Expected: PASS with zero test edits. Any test change needed here means the
refactor altered behavior — stop and fix the refactor instead.

- [ ] **Step 4: Commit**

```bash
git add internal/hooks/apply.go internal/hooks/github.go internal/hooks/push.go internal/hooks/deployment.go
git commit -m "Extract webhook apply routing onto a transport-independent applier"
```

---

## Task 4: Engine 1 — replay stored events

**Files:**
- Create: `internal/hooks/replay.go`
- Test: `internal/hooks/replay_test.go`

- [ ] **Step 1: Write the failing test**

`internal/hooks/replay_test.go` (`package hooks_test` — reuses the helpers in
`github_test.go`; the seeding mirrors the existing ignored-row tests at
`internal/hooks/github_test.go:558` and `internal/hooks/push_test.go:221`):

```go
package hooks_test

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/hooks"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// newUnmappedEnv is newEnv without the repo mapping: deliveries for
// sunstoneinstitute/demo are recorded *.ignored until mapDemoRepo runs.
func newUnmappedEnv(t *testing.T) *env {
	t.Helper()
	st := store.OpenTestStore(t)
	return &env{st: st, h: hooks.NewGitHubHandler(st, testSecret, slog.Default())}
}

func mapDemoRepo(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	if err := e.st.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := e.st.AddRepo(ctx, "demo", "sunstoneinstitute/demo"); err != nil {
		t.Fatalf("add repo: %v", err)
	}
}

func TestReplayAppliesIgnoredEventsAfterMapping(t *testing.T) {
	e := newUnmappedEnv(t)
	rr := deliverBody(t, e.h, "issues", "d-1", []byte(`{
		"action": "opened",
		"repository": {"full_name": "sunstoneinstitute/demo"},
		"issue": {"number": 7, "title": "late issue", "state": "open", "html_url": "u"}
	}`))
	if rr.Code != http.StatusOK || status(t, rr) != "ignored" {
		t.Fatalf("delivery: %d %s", rr.Code, rr.Body.String())
	}
	mapDemoRepo(t, e)

	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.Candidates != 1 || res.Replayed != 1 || res.StillUnmapped != 0 {
		t.Fatalf("replay result = %+v; want 1 candidate, 1 replayed", res)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM issues WHERE repo = 'sunstoneinstitute/demo' AND number = 7`); n != 1 {
		t.Fatalf("issue rows after replay = %d, want 1", n)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM events WHERE external_id = 'd-1' AND applied_at IS NOT NULL`); n != 1 {
		t.Fatalf("applied_at set = %d rows, want 1", n)
	}

	// Second replay: nothing left to do.
	res, err = hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{})
	if err != nil {
		t.Fatalf("second replay: %v", err)
	}
	if res.Candidates != 0 || res.Replayed != 0 {
		t.Fatalf("second replay = %+v; want a no-op", res)
	}
}

// TestReplayProvenance: a replayed pull_request.opened produces the same
// state_log transition a live delivery would, attributed to the ORIGINAL
// event's id — the timeline reads "applied late", not "invented later".
func TestReplayProvenance(t *testing.T) {
	e := newUnmappedEnv(t)
	// The delivery arrives before the repo is mapped...
	body := []byte(`{
		"action": "opened",
		"repository": {"full_name": "sunstoneinstitute/demo"},
		"pull_request": {
			"number": 12, "title": "fix crash", "state": "open", "merged": false,
			"body": "", "html_url": "u",
			"head": {"ref": "lode/WL-1-fix-crash", "sha": "1111111111111111111111111111111111111111"}
		}
	}`)
	rr := deliverBody(t, e.h, "pull_request", "d-pr", body)
	if rr.Code != http.StatusOK || status(t, rr) != "ignored" {
		t.Fatalf("delivery: %d %s", rr.Code, rr.Body.String())
	}

	// ...then the repo is mapped and the task exists, claimed (in_progress).
	mapDemoRepo(t, e)
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)

	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.Replayed != 1 {
		t.Fatalf("replay result = %+v; want 1 replayed", res)
	}

	var originalID int64
	row := e.st.DB().QueryRow(`SELECT id FROM events WHERE external_id = 'd-pr'`)
	if err := row.Scan(&originalID); err != nil {
		t.Fatalf("original event id: %v", err)
	}
	entries, err := e.st.StateLogForEntity(context.Background(), "task", taskID)
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("no state_log entries for %s; want the in_review transition", taskID)
	}
	last := entries[len(entries)-1]
	if last.EventID != originalID {
		t.Fatalf("transition event_id = %d; want the original event %d", last.EventID, originalID)
	}
}

func TestReplayDryRunWritesNothing(t *testing.T) {
	e := newUnmappedEnv(t)
	deliverBody(t, e.h, "issues", "d-1", []byte(`{
		"action": "opened",
		"repository": {"full_name": "sunstoneinstitute/demo"},
		"issue": {"number": 7, "title": "late issue", "state": "open", "html_url": "u"}
	}`))
	mapDemoRepo(t, e)

	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run replay: %v", err)
	}
	if !res.DryRun || res.Candidates != 1 || res.Replayed != 1 {
		t.Fatalf("dry-run result = %+v; want 1 would-replay", res)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM issues`); n != 0 {
		t.Fatalf("dry-run wrote %d issue rows, want 0", n)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM events WHERE applied_at IS NOT NULL`); n != 0 {
		t.Fatalf("dry-run set applied_at on %d events, want 0", n)
	}
}

func TestReplaySkipsStillUnmapped(t *testing.T) {
	e := newUnmappedEnv(t)
	deliverBody(t, e.h, "issues", "d-1", []byte(`{
		"action": "opened",
		"repository": {"full_name": "never/mapped"},
		"issue": {"number": 1, "title": "x", "state": "open", "html_url": "u"}
	}`))

	res, err := hooks.Replay(context.Background(), e.st, hooks.ReplayOptions{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.Candidates != 1 || res.Replayed != 0 || res.StillUnmapped != 1 {
		t.Fatalf("replay result = %+v; want 1 still-unmapped, 0 replayed", res)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM events WHERE applied_at IS NULL`); n != 1 {
		t.Fatalf("still-unmapped event lost its NULL applied_at")
	}
}
```

If `env` has no `DB()` accessor, use `e.rawQueryInt`-style helpers already in
`github_test.go` to fetch the id (add a `rawQueryInt64` sibling next to it if
needed) — do not add a new exported method to `store.Store` just for a test.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/hooks/ -run TestReplay`
Expected: FAIL — `undefined: hooks.Replay`

- [ ] **Step 3: Write the replayer**

`internal/hooks/replay.go`:

```go
// Engine 1 of lode reconcile (spec 013): re-apply stored events whose apply
// never ran — *.ignored deliveries recorded before their repo was mapped.
// Offline: the payload is intact in events.payload, so no GitHub call is
// needed, and the applies are idempotent upserts with from-state-guarded
// transitions, so re-running is harmless.

package hooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// ReplayOptions bound the candidate set. Zero values disable each bound.
type ReplayOptions struct {
	Repo   string
	Since  *time.Time
	DryRun bool
}

// ReplayResult is one replay run's report, JSON-shaped for the reconcile
// endpoint.
type ReplayResult struct {
	DryRun        bool     `json:"dry_run"`
	Candidates    int      `json:"candidates"`
	Replayed      int      `json:"replayed"`
	StillUnmapped int      `json:"still_unmapped"`
	Errors        []string `json:"errors,omitempty"`
}

// Replay applies every unapplied github event whose repo is now mapped, in
// arrival order, each in its own transaction. The apply receives the
// ORIGINAL event's id, so any resulting state_log transition points at the
// real GitHub event — the timeline reads "applied late". Events whose repo
// is still unmapped are left untouched for a later run. A single failing
// event is reported and skipped, never aborting the run.
func Replay(ctx context.Context, st *store.Store, opts ReplayOptions) (*ReplayResult, error) {
	evs, err := st.UnappliedGitHubEvents(ctx, store.UnappliedFilter{Repo: opts.Repo, Since: opts.Since})
	if err != nil {
		return nil, err
	}
	res := &ReplayResult{DryRun: opts.DryRun, Candidates: len(evs)}
	a := &applier{st: st}

	for _, ev := range evs {
		var env envelope
		if err := json.Unmarshal(ev.Payload, &env); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("event %d: parse payload: %v", ev.ID, err))
			continue
		}
		repo := env.Repository.FullName
		if repo == "" {
			// No repository in the payload: nothing to map it by; leave it.
			res.StillUnmapped++
			continue
		}
		if _, err := st.ProjectForRepo(ctx, repo); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				res.StillUnmapped++
				continue
			}
			return nil, err
		}
		if opts.DryRun {
			res.Replayed++
			continue
		}

		apply := a.applyForType(ev.Type, env, ev.Payload)
		txErr := st.Tx(ctx, func(tx *sql.Tx) error {
			if apply != nil {
				if err := apply(tx, ev.ID); err != nil {
					return err
				}
			}
			return store.MarkEventApplied(tx, ev.ID, st.Now())
		})
		if txErr != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("event %d (%s): %v", ev.ID, ev.Type, txErr))
			continue
		}
		res.Replayed++
	}
	return res, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/hooks/ -run TestReplay -v`
Expected: PASS (4 tests)

- [ ] **Step 5: Run the full hooks suite**

Run: `go test ./internal/hooks/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/hooks/replay.go internal/hooks/replay_test.go internal/hooks/github_test.go
git commit -m "Replay ignored webhook events once their repo is mapped"
```

---

## Task 5: GET /api/v1/whoami

**Files:**
- Create: `internal/api/reconcile.go` (whoami handler; grows in Tasks 8/9)
- Modify: `internal/api/server.go` (route)
- Modify: `internal/cli/client.go` (`WhoAmI`)
- Test: `internal/api/reconcile_test.go`, `internal/cli/client_test.go`

- [ ] **Step 1: Write the failing API test**

`internal/api/reconcile_test.go` (`package api_test`, using the existing
`newTestServer`/`doReq` helpers from `internal/api/server_test.go:26,58`):

```go
package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestWhoami(t *testing.T) {
	_, h, token := newTestServer(t)

	rec := doReq(t, h, http.MethodGet, "/api/v1/whoami", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("whoami: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Admin bool   `json:"admin"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID == "" || got.Kind == "" || !got.Admin {
		t.Fatalf("whoami = %+v; want the bootstrap admin actor", got)
	}
}

func TestWhoamiRequiresAuth(t *testing.T) {
	_, h, _ := newTestServer(t)
	if rec := doReq(t, h, http.MethodGet, "/api/v1/whoami", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d; want 401", rec.Code)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestWhoami`
Expected: FAIL — 404, route unregistered.

- [ ] **Step 3: Write the handler and route**

`internal/api/reconcile.go`:

```go
// Reconciliation & setup-diagnosis endpoints (spec 013): whoami for the CLI
// doctor, the ingestion-health report, and the reconcile run itself.

package api

import (
	"net/http"
)

// whoamiJSON is the wire form of GET /api/v1/whoami.
type whoamiJSON struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Admin bool   `json:"admin"`
}

// whoami handles GET /api/v1/whoami: the calling actor's identity. Auth
// only, no admin gate — this is how the CLI (and lode doctor) asks whether a
// token is accepted and who it belongs to.
func (s *server) whoami(w http.ResponseWriter, r *http.Request) {
	a := actorFrom(r)
	writeJSON(w, http.StatusOK, whoamiJSON{ID: a.ID, Kind: a.Kind, Admin: a.Admin})
}
```

In `internal/api/server.go`, next to the board route (line 304):

```go
	mux.Handle("GET /api/v1/whoami", s.auth(s.whoami))
```

- [ ] **Step 4: Add the client method**

In `internal/cli/client.go`, after `ServerURL` (line 308):

```go
// WhoAmI is the response of GET /api/v1/whoami.
type WhoAmI struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Admin bool   `json:"admin"`
}

// WhoAmI calls GET /api/v1/whoami: which actor the configured token belongs
// to. A *ClientError with Status 401 means the token is not accepted; a
// transport error means the server is unreachable — lode doctor tells those
// two failures apart.
func (c *Client) WhoAmI(ctx context.Context) (WhoAmI, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/whoami", nil)
	if err != nil {
		return WhoAmI{}, nil, err
	}
	var who WhoAmI
	if err := json.Unmarshal(raw, &who); err != nil {
		return WhoAmI{}, nil, fmt.Errorf("decode whoami: %w", err)
	}
	return who, raw, nil
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/api/ -run TestWhoami -v && go test ./internal/cli/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api/reconcile.go internal/api/reconcile_test.go internal/api/server.go internal/cli/client.go
git commit -m "Add GET /api/v1/whoami"
```

---

## Task 6: lode doctor

Client-side only; must produce useful output with the server unreachable and
exit non-zero when any check fails, each failure naming its fix (spec 013
§`lode doctor`).

**Files:**
- Modify: `internal/cli/client.go` (`ConfigOrigins` export)
- Create: `internal/cmd/doctor.go`
- Test: `internal/cmd/doctor_test.go`

- [ ] **Step 1: Export the config-origin probe**

In `internal/cli/client.go`, next to `findRepoConfig` (line 71):

```go
// ConfigOrigins reports where config loading would look from startDir: the
// user config path (and whether the file exists) and the repo-local
// .worklode/.lode config the walk-up found, if any. lode doctor reports
// these; LoadConfig remains the authority on what actually loads.
func ConfigOrigins(startDir string) (userPath string, userFound bool, repoPath string, repoFound bool) {
	if p, err := configPath(); err == nil {
		userPath = p
		if _, statErr := os.Stat(p); statErr == nil {
			userFound = true
		}
	}
	repoPath, repoFound = findRepoConfig(startDir)
	return userPath, userFound, repoPath, repoFound
}
```

- [ ] **Step 2: Write the failing test**

`internal/cmd/doctor_test.go` (`package cmd`, using the existing `runLode`
helper from `internal/cmd/lifecycle_test.go:57` and `setupRepoConfig` from
`internal/cmd/currentproject_test.go:17`; these tests fake the server with
`httptest`, so they need no Postgres):

```go
package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLode serves whoami and the project list the way lode doctor consumes
// them, and points LODE_SERVER/LODE_TOKEN at itself.
func fakeLode(t *testing.T, whoamiStatus int, projects string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/whoami":
			w.WriteHeader(whoamiStatus)
			if whoamiStatus == http.StatusOK {
				io.WriteString(w, `{"id":"stig","kind":"human","admin":true}`)
			} else {
				io.WriteString(w, `{"error":"unauthorized"}`)
			}
		case "/api/v1/projects":
			io.WriteString(w, `{"projects":[`+projects+`]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
}

func TestDoctorHealthySetup(t *testing.T) {
	repo := setupRepoConfig(t, "demo")
	gitInit(t, repo)
	fakeLode(t, http.StatusOK, `{"id":"demo","name":"Demo","key":"WL","repos":[],"focus":[]}`)
	if _, _, err := installGitHooks(repo); err != nil {
		t.Fatalf("install hooks: %v", err)
	}

	out, err := runLode(t, "doctor")
	if err != nil {
		t.Fatalf("healthy doctor exited non-zero: %v\n%s", err, out)
	}
	for _, want := range []string{"config", "server", "token", "current_project", "hooks"} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output missing %q check:\n%s", want, out)
		}
	}
}

func TestDoctorFailuresNameTheirFix(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T)
		wantFix string
	}{
		{
			name: "missing config",
			setup: func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				t.Chdir(t.TempDir())
				t.Setenv("LODE_SERVER", "")
				t.Setenv("LODE_TOKEN", "")
			},
			wantFix: "config.toml",
		},
		{
			name: "unreachable server",
			setup: func(t *testing.T) {
				setupRepoConfig(t, "demo")
				// A closed port: connection refused, not a slow timeout.
				t.Setenv("LODE_SERVER", "http://127.0.0.1:1")
				t.Setenv("LODE_TOKEN", "wl_test")
			},
			wantFix: "server",
		},
		{
			name: "invalid token",
			setup: func(t *testing.T) {
				setupRepoConfig(t, "demo")
				fakeLode(t, http.StatusUnauthorized, ``)
			},
			wantFix: "lode login",
		},
		{
			name: "unset current_project",
			setup: func(t *testing.T) {
				setupRepoConfig(t, "")
				fakeLode(t, http.StatusOK, ``)
			},
			wantFix: "current_project",
		},
		{
			name: "missing git hooks",
			setup: func(t *testing.T) {
				repo := setupRepoConfig(t, "demo")
				gitInit(t, repo)
				fakeLode(t, http.StatusOK, `{"id":"demo","name":"Demo","key":"WL","repos":[],"focus":[]}`)
			},
			wantFix: "lode install",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			out, err := runLode(t, "doctor")
			if err == nil {
				t.Fatalf("doctor exited zero on a broken setup:\n%s", out)
			}
			if !strings.Contains(out, tc.wantFix) {
				t.Fatalf("doctor output does not name the fix %q:\n%s", tc.wantFix, out)
			}
		})
	}
}

// gitInit makes dir a git repo so the hooks checks have a hooks directory.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if out, err := execGit(dir, "init", "-q"); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

// execGit runs one git command in dir.
func execGit(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	return string(out), err
}
```

(with `"os/exec"` in the imports). If the cmd test package already has a
git-runner helper, use it instead of adding `execGit`. Note
`setupRepoConfig` chdirs into the repo; the
"missing config" case chdirs to a plain temp dir with an empty `HOME`, and
clears the env overrides so no server is configured at all.

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/cmd/ -run TestDoctor`
Expected: FAIL — `unknown command "doctor" for "lode"`

- [ ] **Step 4: Write the command**

`internal/cmd/doctor.go`:

```go
// lode doctor: client-side setup diagnosis (spec 013). Runs entirely
// locally, needs no privileges, and stays useful with the server
// unreachable. Each failing check names its fix; any failure exits non-zero
// so hooks and CI can gate on it.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// doctorCheck is one pass/fail line of the report. Fix is set only on
// failure. Skipped checks (e.g. the worktree check outside a worktree, or a
// server-side check with the server unreachable) count as neither.
type doctorCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped,omitempty"`
	Detail  string `json:"detail"`
	Fix     string `json:"fix,omitempty"`
}

func pass(name, detail string) doctorCheck { return doctorCheck{Name: name, OK: true, Detail: detail} }
func fail(name, detail, fix string) doctorCheck {
	return doctorCheck{Name: name, Detail: detail, Fix: fix}
}
func skip(name, detail string) doctorCheck {
	return doctorCheck{Name: name, OK: true, Skipped: true, Detail: detail}
}

// runDoctorChecks runs the spec's six checks in order from dir. Later checks
// still run when earlier ones fail, degrading to skips where they cannot be
// evaluated, so one run reports everything wrong at once.
func runDoctorChecks(ctx context.Context, dir string) []doctorCheck {
	var checks []doctorCheck

	// 1. Config file found — which one, and where the walk-up located it.
	userPath, userFound, repoPath, repoFound := cli.ConfigOrigins(dir)
	switch {
	case repoFound:
		checks = append(checks, pass("config", "repo config "+repoPath))
	case userFound:
		checks = append(checks, pass("config", "user config "+userPath))
	default:
		checks = append(checks, fail("config",
			"no config file found (looked for a repo-local .worklode/.lode config above "+dir+" and "+userPath+")",
			"run `lode login <server-url>` or create "+userPath+" with server = \"https://...\""))
	}

	cfg, cfgErr := cli.LoadConfig()
	if cfgErr != nil {
		checks = append(checks, fail("config-load", cfgErr.Error(), "fix the config file reported above"))
		return checks
	}

	// 2. server set and reachable / 3. token present and accepted — one
	// whoami round trip answers both: a transport error is "unreachable", a
	// 401 is "token rejected", 200 is both green.
	var c *cli.Client
	serverReachable := false
	switch {
	case cfg.ServerURL == "":
		checks = append(checks, fail("server", "server URL not set",
			"set LODE_SERVER or add server = \"https://...\" to the config file"))
	default:
		c = cli.NewClient(cfg)
		who, _, whoErr := c.WhoAmI(ctx)
		var ce *cli.ClientError
		switch {
		case whoErr == nil:
			serverReachable = true
			checks = append(checks, pass("server", cfg.ServerURL+" reachable"))
			if cfg.Token == "" {
				// 200 with no token cannot happen; guard anyway.
				checks = append(checks, fail("token", "no token configured", "run `lode login`"))
			} else {
				checks = append(checks, pass("token", "accepted; you are "+who.ID+" ("+who.Kind+")"))
			}
		case errors.As(whoErr, &ce):
			serverReachable = true
			checks = append(checks, pass("server", cfg.ServerURL+" reachable"))
			if cfg.Token == "" {
				checks = append(checks, fail("token",
					"no token in the OS keychain or LODE_TOKEN", "run `lode login`"))
			} else {
				checks = append(checks, fail("token",
					fmt.Sprintf("server rejected the token (%d)", ce.Status), "run `lode login` to mint a fresh token"))
			}
		default:
			checks = append(checks, fail("server", cfg.ServerURL+" unreachable: "+whoErr.Error(),
				"check the server URL and your network; set LODE_SERVER to override"))
			checks = append(checks, skip("token", "not checked (server unreachable)"))
		}
	}

	// 4. current_project set, and the project exists.
	switch {
	case cfg.CurrentProject == "":
		checks = append(checks, fail("current_project", "not set",
			"add current_project = \"<project-id>\" to .worklode/config.toml (or the user config)"))
	case !serverReachable:
		checks = append(checks, skip("current_project", cfg.CurrentProject+" (existence not checked: server unreachable)"))
	default:
		if _, err := c.GetProject(ctx, cfg.CurrentProject); err != nil {
			checks = append(checks, fail("current_project",
				"project "+cfg.CurrentProject+" not found on the server",
				"fix current_project in the config, or create the project with `lode project add`"))
		} else {
			checks = append(checks, pass("current_project", cfg.CurrentProject))
		}
	}

	// 5. Git hooks installed in this repo.
	if hooksDir, err := resolveHooksDir(dir); err != nil {
		checks = append(checks, skip("hooks", "not in a git repository"))
	} else {
		content, readErr := os.ReadFile(filepath.Join(hooksDir, "pre-commit"))
		if readErr == nil && strings.Contains(string(content), hookMarker) {
			checks = append(checks, pass("hooks", "pre-commit installed in "+hooksDir))
		} else {
			checks = append(checks, fail("hooks", "worklode pre-commit hook not installed",
				"run `lode install` in this repo"))
		}
	}

	// 6. Inside a task worktree: does it map to a task with a live lease.
	root, inRepo := worktree.Root(dir)
	taskID, isTaskWT := "", false
	if inRepo {
		taskID, isTaskWT = worktree.ParseDir(root)
	}
	switch {
	case !isTaskWT:
		checks = append(checks, skip("worktree", "not inside a task worktree"))
	case !serverReachable:
		checks = append(checks, skip("worktree", taskID+" (lease not checked: server unreachable)"))
	default:
		detail, _, err := c.GetTask(ctx, taskID)
		switch {
		case err != nil:
			checks = append(checks, fail("worktree", "worktree names task "+taskID+", which the server does not know",
				"remove the stale worktree, or create/claim the task"))
		case detail.Lease == nil:
			checks = append(checks, fail("worktree", "task "+taskID+" has no live lease",
				"run `lode claim "+taskID+"` from this worktree"))
		default:
			checks = append(checks, pass("worktree", taskID+" leased until "+detail.Lease.ExpiresAt.Format("2006-01-02 15:04")))
		}
	}

	return checks
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose this machine's lode setup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			checks := runDoctorChecks(cmd.Context(), dir)

			failed := 0
			for _, c := range checks {
				if !c.OK {
					failed++
				}
			}
			if jsonOut(cmd) {
				b, err := json.MarshalIndent(struct {
					OK     bool          `json:"ok"`
					Checks []doctorCheck `json:"checks"`
				}{OK: failed == 0, Checks: checks}, "", "  ")
				if err != nil {
					return err
				}
				cmd.Println(string(b))
			} else {
				for _, c := range checks {
					mark := "ok  "
					switch {
					case c.Skipped:
						mark = "skip"
					case !c.OK:
						mark = "FAIL"
					}
					cmd.Printf("%s  %-16s %s\n", mark, c.Name, c.Detail)
					if c.Fix != "" {
						cmd.Printf("      fix: %s\n", c.Fix)
					}
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d check(s) failed", failed)
			}
			return nil
		},
	}
}

func init() {
	rootCmd.AddCommand(newDoctorCmd())
}
```

Adjust the `detail.Lease.ExpiresAt` field name to whatever
`cli.Lease` actually calls it (`internal/cli/client.go:383`) — do not add a
new field.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/cmd/ -run TestDoctor -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/cli/client.go internal/cmd/doctor.go internal/cmd/doctor_test.go
git commit -m "Add lode doctor: client-side setup diagnosis"
```

---

## Task 7: Ingestion-health store queries

**Files:**
- Modify: `internal/store/reconcile.go` (append)
- Test: `internal/store/reconcile_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/reconcile_test.go`:

```go
func TestRepoIngestionHealth(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := s.AddRepo(ctx, "demo", "acme/app"); err != nil {
		t.Fatalf("map acme/app: %v", err)
	}
	if err := s.AddRepo(ctx, "demo", "acme/silent"); err != nil {
		t.Fatalf("map acme/silent: %v", err)
	}

	recordGitHubEvent(t, s, "d-1", "issues.opened.ignored", `{"repository":{"full_name":"acme/app"}}`)
	recordGitHubEvent(t, s, "d-2", "push", `{"repository":{"full_name":"acme/app"}}`)
	recordGitHubEvent(t, s, "d-3", "push.ignored", `{"repository":{"full_name":"acme/unmapped"}}`)

	all, err := s.RepoIngestionHealth(ctx, "")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("health rows = %d, want 2 mapped repos", len(all))
	}
	app, silent := all[0], all[1] // ordered by repo
	if app.Repo != "acme/app" || app.LastEventAt == nil || app.Unapplied != 2 {
		t.Fatalf("acme/app = %+v; want a last event and 2 unapplied", app)
	}
	if len(app.EventTypes) != 2 { // issues.opened.ignored, push
		t.Fatalf("acme/app event types = %v; want 2 distinct types", app.EventTypes)
	}
	if silent.Repo != "acme/silent" || silent.LastEventAt != nil || silent.Unapplied != 0 {
		t.Fatalf("acme/silent = %+v; want no events at all", silent)
	}
	if silent.MappedAt.IsZero() {
		t.Fatalf("mapped_at not populated for a fresh mapping")
	}

	one, err := s.RepoIngestionHealth(ctx, "acme/app")
	if err != nil {
		t.Fatalf("filtered health: %v", err)
	}
	if len(one) != 1 || one[0].Repo != "acme/app" {
		t.Fatalf("filtered health = %+v; want only acme/app", one)
	}

	senders, err := s.UnmappedSenders(ctx)
	if err != nil {
		t.Fatalf("unmapped senders: %v", err)
	}
	if len(senders) != 1 || senders[0].Repo != "acme/unmapped" || senders[0].Events != 1 {
		t.Fatalf("unmapped senders = %+v; want acme/unmapped with 1 event", senders)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run TestRepoIngestionHealth`
Expected: FAIL — `undefined: (*Store).RepoIngestionHealth`

- [ ] **Step 3: Write the implementation**

Append to `internal/store/reconcile.go` (`"encoding/json"` joins the
imports; the `jsonb_agg` scan follows the `scanProjectFocus` pattern in
`internal/store/projects.go:23`):

```go
// RepoIngestion is one mapped repo's ingestion health: what project doctor
// reports (spec 013 §lode project doctor).
type RepoIngestion struct {
	Repo        string
	ProjectID   string
	MappedAt    time.Time
	LastEventAt *time.Time // nil: this repo has never sent a webhook
	EventTypes  []string   // distinct event types seen, sorted
	Unapplied   int        // events still awaiting replay
}

// RepoIngestionHealth returns per-repo ingestion health for every mapped
// repo (or just one, when repo is non-empty), ordered by repo. Events
// correlate to repos by the delivery payload's repository.full_name; this
// scans the events table and is an operator-frequency query, not a hot path.
func (s *Store) RepoIngestionHealth(ctx context.Context, repo string) ([]RepoIngestion, error) {
	q := `SELECT pr.repo, pr.project_id, pr.mapped_at,
	             e.last_event_at, COALESCE(e.event_types, '[]'::jsonb), COALESCE(e.unapplied, 0)
	      FROM project_repos pr
	      LEFT JOIN LATERAL (
	          SELECT max(received_at) AS last_event_at,
	                 jsonb_agg(DISTINCT type) AS event_types,
	                 count(*) FILTER (WHERE applied_at IS NULL) AS unapplied
	          FROM events
	          WHERE source = 'github'
	            AND payload->'repository'->>'full_name' = pr.repo
	      ) e ON true`
	var args []any
	if repo != "" {
		args = append(args, repo)
		q += ` WHERE pr.repo = $1`
	}
	q += ` ORDER BY pr.repo`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("repo ingestion health: %w", err)
	}
	defer rows.Close()

	var out []RepoIngestion
	for rows.Next() {
		var ri RepoIngestion
		var types []byte
		if err := rows.Scan(&ri.Repo, &ri.ProjectID, &ri.MappedAt, &ri.LastEventAt, &types, &ri.Unapplied); err != nil {
			return nil, fmt.Errorf("scan repo ingestion health: %w", err)
		}
		if err := json.Unmarshal(types, &ri.EventTypes); err != nil {
			return nil, fmt.Errorf("decode event types for %s: %w", ri.Repo, err)
		}
		ri.MappedAt = ri.MappedAt.UTC()
		if ri.LastEventAt != nil {
			u := ri.LastEventAt.UTC()
			ri.LastEventAt = &u
		}
		out = append(out, ri)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo ingestion health: %w", err)
	}
	return out, nil
}

// UnmappedSender is a repo that has sent webhooks but maps to no project.
type UnmappedSender struct {
	Repo        string
	Events      int
	LastEventAt time.Time
}

// UnmappedSenders returns repos seen in github deliveries that have no
// project mapping, ordered by repo.
func (s *Store) UnmappedSenders(ctx context.Context) ([]UnmappedSender, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.payload->'repository'->>'full_name', count(*), max(e.received_at)
		 FROM events e
		 WHERE e.source = 'github'
		   AND e.payload->'repository'->>'full_name' IS NOT NULL
		   AND NOT EXISTS (SELECT 1 FROM project_repos pr
		                   WHERE pr.repo = e.payload->'repository'->>'full_name')
		 GROUP BY 1 ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("unmapped senders: %w", err)
	}
	defer rows.Close()

	var out []UnmappedSender
	for rows.Next() {
		var u UnmappedSender
		if err := rows.Scan(&u.Repo, &u.Events, &u.LastEventAt); err != nil {
			return nil, fmt.Errorf("scan unmapped sender: %w", err)
		}
		u.LastEventAt = u.LastEventAt.UTC()
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("unmapped senders: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/store/ -run TestRepoIngestionHealth -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/reconcile.go internal/store/reconcile_test.go
git commit -m "Add per-repo ingestion-health queries"
```

---

## Task 8: GET /api/v1/repos/doctor and lode project doctor

**Files:**
- Modify: `internal/api/reconcile.go` (handler)
- Modify: `internal/api/server.go` (route)
- Modify: `internal/cli/client.go` (`ReposDoctor`)
- Modify: `internal/cmd/project.go` (`lode project doctor`)
- Test: `internal/api/reconcile_test.go` (append), `internal/cmd/project_test.go` (append)

- [ ] **Step 1: Write the failing API test**

Append to `internal/api/reconcile_test.go` (reuse `mapRepo` from
`internal/api/projects_resolve_test.go:244` and `seedIssue`-style event
seeding from `internal/api/inbox_test.go`):

```go
func TestReposDoctor(t *testing.T) {
	st, h, token := newTestServer(t)
	mapRepo(t, h, token, "demo", "WL", "acme/app")

	// One pre-mapping-style unapplied event for the mapped repo, one event
	// from a repo nothing maps.
	seedGitHubEvent(t, st, "d-1", "push.ignored", `{"repository":{"full_name":"acme/app"}}`)
	seedGitHubEvent(t, st, "d-2", "push.ignored", `{"repository":{"full_name":"acme/unmapped"}}`)

	rec := doReq(t, h, http.MethodGet, "/api/v1/repos/doctor", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("repos doctor: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Repos []struct {
			Repo            string  `json:"repo"`
			Project         string  `json:"project"`
			AppInstalled    *bool   `json:"app_installed"`
			LastEventAt     *string `json:"last_event_at"`
			UnappliedEvents int     `json:"unapplied_events"`
			Stale           bool    `json:"stale"`
		} `json:"repos"`
		UnmappedSenders []struct {
			Repo   string `json:"repo"`
			Events int    `json:"events"`
		} `json:"unmapped_senders"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Repos) != 1 || resp.Repos[0].Repo != "acme/app" {
		t.Fatalf("repos = %+v; want acme/app", resp.Repos)
	}
	r := resp.Repos[0]
	if r.AppInstalled != nil {
		t.Fatalf("app_installed = %v; want null (app auth unconfigured in tests)", *r.AppInstalled)
	}
	if r.UnappliedEvents != 1 {
		t.Fatalf("unapplied = %d; want 1", r.UnappliedEvents)
	}
	if len(resp.UnmappedSenders) != 1 || resp.UnmappedSenders[0].Repo != "acme/unmapped" {
		t.Fatalf("unmapped senders = %+v; want acme/unmapped", resp.UnmappedSenders)
	}
}

// TestReposDoctorStale: a mapped repo with no deliveries at all is stale —
// the signal that sends an operator to lode reconcile.
func TestReposDoctorStale(t *testing.T) {
	_, h, token := newTestServer(t)
	mapRepo(t, h, token, "demo", "WL", "acme/silent")

	rec := doReq(t, h, http.MethodGet, "/api/v1/repos/doctor?repo=acme/silent", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("repos doctor: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Repos []struct {
			Stale bool `json:"stale"`
		} `json:"repos"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Repos) != 1 || !resp.Repos[0].Stale {
		t.Fatalf("repos = %+v; want one stale repo", resp.Repos)
	}
}

func TestReposDoctorRequiresAdmin(t *testing.T) {
	st, h, token := newTestServer(t)
	nonAdmin := makeNonAdminToken(t, st, h, token)
	if rec := doReq(t, h, http.MethodGet, "/api/v1/repos/doctor", nonAdmin, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d; want 403", rec.Code)
	}
}

// seedGitHubEvent records one github event with a nil apply (applied_at NULL).
func seedGitHubEvent(t *testing.T, st *store.Store, externalID, typ, payload string) {
	t.Helper()
	if _, _, err := st.RecordEvent(context.Background(), "github", externalID, typ,
		[]byte(payload), nil); err != nil {
		t.Fatalf("seed event %s: %v", externalID, err)
	}
}

// makeNonAdminToken creates a non-admin actor and mints a token for it via
// the admin API.
func makeNonAdminToken(t *testing.T, st *store.Store, h http.Handler, adminToken string) string {
	t.Helper()
	rec := doReq(t, h, http.MethodPost, "/api/v1/actors", adminToken,
		map[string]any{"id": "dev", "kind": "human", "display_name": "Dev", "admin": false})
	if rec.Code >= 300 {
		t.Fatalf("create actor: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/actors/dev/tokens", adminToken,
		map[string]any{"description": "test"})
	if rec.Code >= 300 {
		t.Fatalf("create token: %d %s", rec.Code, rec.Body.String())
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil || tok.Token == "" {
		t.Fatalf("decode token: %v (%s)", err, rec.Body.String())
	}
	return tok.Token
}
```

Match `makeNonAdminToken`'s request bodies to the actual `createActor` /
`createToken` handlers in `internal/api/admin.go:288,318` (field names and
response shape); an equivalent helper may already exist in the api test
package — search for one before adding it.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestReposDoctor`
Expected: FAIL — 404, route unregistered.

- [ ] **Step 3: Write the handler and route**

Append to `internal/api/reconcile.go`:

```go
// repoDoctorJSON is one mapped repo's ingestion health on the wire.
// AppInstalled is nil when the server has no GitHub App configured — the
// check cannot run, which is different from "not installed".
type repoDoctorJSON struct {
	Repo            string     `json:"repo"`
	Project         string     `json:"project"`
	AppInstalled    *bool      `json:"app_installed"`
	AppError        string     `json:"app_error,omitempty"`
	MappedAt        time.Time  `json:"mapped_at"`
	LastEventAt     *time.Time `json:"last_event_at"`
	EventTypes      []string   `json:"event_types"`
	UnappliedEvents int        `json:"unapplied_events"`
	// Stale: this repo has never delivered a webhook, or its last delivery
	// predates the mapping — the signal to run lode reconcile.
	Stale bool `json:"stale"`
}

type unmappedSenderJSON struct {
	Repo        string    `json:"repo"`
	Events      int       `json:"events"`
	LastEventAt time.Time `json:"last_event_at"`
}

type reposDoctorResponse struct {
	Repos           []repoDoctorJSON     `json:"repos"`
	UnmappedSenders []unmappedSenderJSON `json:"unmapped_senders"`
}

// reposDoctor handles GET /api/v1/repos/doctor[?repo=owner/name]: per-repo
// ingestion health. Admin-gated — it reads across the whole org.
func (s *server) reposDoctor(w http.ResponseWriter, r *http.Request) {
	health, err := s.st.RepoIngestionHealth(r.Context(), r.URL.Query().Get("repo"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	senders, err := s.st.UnmappedSenders(r.Context())
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	resp := reposDoctorResponse{Repos: []repoDoctorJSON{}, UnmappedSenders: []unmappedSenderJSON{}}
	for _, ri := range health {
		rj := repoDoctorJSON{
			Repo:            ri.Repo,
			Project:         ri.ProjectID,
			MappedAt:        ri.MappedAt,
			LastEventAt:     ri.LastEventAt,
			EventTypes:      ri.EventTypes,
			UnappliedEvents: ri.Unapplied,
			Stale:           ri.LastEventAt == nil || ri.LastEventAt.Before(ri.MappedAt),
		}
		if rj.EventTypes == nil {
			rj.EventTypes = []string{}
		}
		if s.appAuth != nil {
			// Confirmed by minting an installation token (the spec's check);
			// bounded per repo like addRepo's discovery.
			ctx, cancel := context.WithTimeout(r.Context(), discoveryTimeout)
			_, tokErr := s.appAuth.InstallationToken(ctx, ri.Repo)
			cancel()
			installed := tokErr == nil
			rj.AppInstalled = &installed
			if tokErr != nil {
				rj.AppError = tokErr.Error()
			}
		}
		resp.Repos = append(resp.Repos, rj)
	}
	for _, u := range senders {
		resp.UnmappedSenders = append(resp.UnmappedSenders,
			unmappedSenderJSON{Repo: u.Repo, Events: u.Events, LastEventAt: u.LastEventAt})
	}
	writeJSON(w, http.StatusOK, resp)
}
```

(`"context"` and `"time"` join the file's imports; `discoveryTimeout` already
exists at `internal/api/admin.go:223`.)

In `internal/api/server.go`, next to the repos PATCH route (line 292):

```go
	mux.Handle("GET /api/v1/repos/doctor", s.auth(requireAdmin(s.reposDoctor)))
```

- [ ] **Step 4: Run the API tests**

Run: `go test ./internal/api/ -run TestReposDoctor -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Add the client method and CLI command**

In `internal/cli/client.go`, after `AddRepo`:

```go
// RepoDoctor is one repo's ingestion health from GET /api/v1/repos/doctor.
type RepoDoctor struct {
	Repo            string     `json:"repo"`
	Project         string     `json:"project"`
	AppInstalled    *bool      `json:"app_installed"`
	AppError        string     `json:"app_error,omitempty"`
	MappedAt        time.Time  `json:"mapped_at"`
	LastEventAt     *time.Time `json:"last_event_at"`
	EventTypes      []string   `json:"event_types"`
	UnappliedEvents int        `json:"unapplied_events"`
	Stale           bool       `json:"stale"`
}

// UnmappedSender mirrors the server's unmapped_senders entries.
type UnmappedSender struct {
	Repo        string    `json:"repo"`
	Events      int       `json:"events"`
	LastEventAt time.Time `json:"last_event_at"`
}

// ReposDoctorResponse is the response of GET /api/v1/repos/doctor.
type ReposDoctorResponse struct {
	Repos           []RepoDoctor     `json:"repos"`
	UnmappedSenders []UnmappedSender `json:"unmapped_senders"`
}

// ReposDoctor calls GET /api/v1/repos/doctor. An empty repo reports every
// mapped repo. Admin-only on the server.
func (c *Client) ReposDoctor(ctx context.Context, repo string) (ReposDoctorResponse, []byte, error) {
	q := url.Values{}
	if repo != "" {
		q.Set("repo", repo)
	}
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/repos/doctor", q), nil)
	if err != nil {
		return ReposDoctorResponse{}, nil, err
	}
	var resp ReposDoctorResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ReposDoctorResponse{}, nil, fmt.Errorf("decode repos doctor: %w", err)
	}
	return resp, raw, nil
}
```

In `internal/cmd/project.go`, add the subcommand to the `AddCommand` list in
`newProjectCmd` (line 19) and:

```go
// newProjectDoctorCmd builds `lode project doctor [repo]`: is ingestion
// working for this repo (operator view, admin token required).
func newProjectDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor [repo]",
		Short: "Report webhook-ingestion health per mapped repo",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			repo := ""
			if len(args) == 1 {
				repo = args[0]
			}
			resp, raw, err := c.ReposDoctor(cmd.Context(), repo)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			for _, r := range resp.Repos {
				app := "unchecked (no GitHub App configured)"
				if r.AppInstalled != nil {
					if *r.AppInstalled {
						app = "installed"
					} else {
						app = "NOT INSTALLED (" + r.AppError + ")"
					}
				}
				last := "never"
				if r.LastEventAt != nil {
					last = r.LastEventAt.Format(time.RFC3339)
				}
				cmd.Printf("%s (project %s)\n", r.Repo, r.Project)
				cmd.Printf("  app:        %s\n", app)
				cmd.Printf("  last event: %s (types: %s)\n", last, strings.Join(r.EventTypes, ", "))
				cmd.Printf("  unapplied:  %d\n", r.UnappliedEvents)
				if r.Stale {
					cmd.Printf("  STALE: no delivery since mapping — run `lode reconcile --repo %s`\n", r.Repo)
				}
			}
			for _, u := range resp.UnmappedSenders {
				cmd.Printf("unmapped sender: %s (%d events, last %s)\n",
					u.Repo, u.Events, u.LastEventAt.Format(time.RFC3339))
			}
			return nil
		},
	}
}
```

- [ ] **Step 6: Write a CLI wiring test**

Append to `internal/cmd/project_test.go`:

```go
func TestProjectDoctorRendersReport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/doctor" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"repos": [{
				"repo": "acme/app", "project": "demo",
				"app_installed": null,
				"mapped_at": "2026-07-30T00:00:00Z",
				"last_event_at": null, "event_types": [],
				"unapplied_events": 3, "stale": true
			}],
			"unmapped_senders": [{"repo": "acme/unmapped", "events": 2, "last_event_at": "2026-07-29T00:00:00Z"}]
		}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")

	out, err := runLode(t, "project", "doctor")
	if err != nil {
		t.Fatalf("project doctor: %v\n%s", err, out)
	}
	for _, want := range []string{"acme/app", "STALE", "acme/unmapped"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}

	out, err = runLode(t, "project", "doctor", "--json")
	if err != nil {
		t.Fatalf("project doctor --json: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"stale": true`) && !strings.Contains(out, `"stale":true`) {
		t.Fatalf("--json output does not round-trip stale:\n%s", out)
	}
}
```

(with `"io"`, `"net/http"`, `"net/http/httptest"`, `"strings"` in that test
file's imports as needed).

- [ ] **Step 7: Run everything touched**

Run: `go test ./internal/api/... ./internal/cli/... ./internal/cmd/...`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/api internal/cli/client.go internal/cmd/project.go internal/cmd/project_test.go
git commit -m "Add lode project doctor over GET /api/v1/repos/doctor"
```

---

## Task 9: POST /api/v1/reconcile (engine 1) and lode reconcile

The endpoint ships now with the replay engine; Task 13 adds polling to the
same handler. The response shape is designed for both from the start.

**Files:**
- Modify: `internal/api/reconcile.go` (handler + `parseSince`)
- Modify: `internal/api/server.go` (route)
- Modify: `internal/cli/client.go` (`Reconcile`)
- Create: `internal/cmd/reconcile.go`
- Test: `internal/api/reconcile_test.go` (append), `internal/cmd/reconcile_test.go`

- [ ] **Step 1: Write the failing API test**

Append to `internal/api/reconcile_test.go`:

```go
func TestReconcileReplaysIgnoredEvents(t *testing.T) {
	st, h, token := newTestServer(t)
	// Delivery recorded before mapping...
	seedGitHubEvent(t, st, "d-1", "issues.opened.ignored", `{
		"action": "opened",
		"repository": {"full_name": "acme/app"},
		"issue": {"number": 7, "title": "late", "state": "open", "html_url": "u"}
	}`)
	// ...then the repo is mapped.
	mapRepo(t, h, token, "demo", "WL", "acme/app")

	rec := doReq(t, h, http.MethodPost, "/api/v1/reconcile", token, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("reconcile: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		RunID  string `json:"run_id"`
		DryRun bool   `json:"dry_run"`
		Replay struct {
			Candidates int `json:"candidates"`
			Replayed   int `json:"replayed"`
		} `json:"replay"`
		PollSkipped string `json:"poll_skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.RunID == "" || resp.Replay.Replayed != 1 {
		t.Fatalf("response = %+v; want a run id and 1 replayed", resp)
	}
	if resp.PollSkipped == "" {
		t.Fatalf("poll_skipped empty; want the no-github-app explanation")
	}
}

func TestReconcileValidation(t *testing.T) {
	_, h, token := newTestServer(t)
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"repo and task together", map[string]any{"repo": "a/b", "task": "WL-1"}, http.StatusUnprocessableEntity},
		{"bad since", map[string]any{"since": "yesterday-ish"}, http.StatusUnprocessableEntity},
		{"duration since", map[string]any{"since": "720h", "dry_run": true}, http.StatusOK},
		{"rfc3339 since", map[string]any{"since": "2026-07-01T00:00:00Z", "dry_run": true}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := doReq(t, h, http.MethodPost, "/api/v1/reconcile", token, tc.body); rec.Code != tc.want {
				t.Fatalf("%s: %d %s; want %d", tc.name, rec.Code, rec.Body.String(), tc.want)
			}
		})
	}
}

func TestReconcileRequiresAdmin(t *testing.T) {
	st, h, token := newTestServer(t)
	nonAdmin := makeNonAdminToken(t, st, h, token)
	if rec := doReq(t, h, http.MethodPost, "/api/v1/reconcile", nonAdmin, map[string]any{}); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d; want 403", rec.Code)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestReconcile`
Expected: FAIL — 404, route unregistered.

- [ ] **Step 3: Write the handler**

Append to `internal/api/reconcile.go` (imports gain
`"github.com/sunstoneinstitute/worklode/internal/hooks"`):

```go
// reconcileRequest is the body of POST /api/v1/reconcile. Repo and Task are
// mutually exclusive bounds; Since accepts RFC 3339 or a Go duration,
// resolved against the server clock.
type reconcileRequest struct {
	Repo   string `json:"repo,omitempty"`
	Task   string `json:"task,omitempty"`
	Since  string `json:"since,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// reconcileResponse is one run's report, one section per engine. Poll is
// null when polling did not run; PollSkipped says why.
type reconcileResponse struct {
	RunID       string              `json:"run_id"`
	DryRun      bool                `json:"dry_run"`
	Replay      *hooks.ReplayResult `json:"replay"`
	Poll        any                 `json:"poll"` // *reconcile.PollResult once Task 13 lands
	PollSkipped string              `json:"poll_skipped,omitempty"`
}

// parseSince resolves a --since value against now: an RFC 3339 timestamp is
// taken as-is; a Go duration ("720h") means now minus that duration.
func parseSince(s string, now time.Time) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		u := t.UTC()
		return &u, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil, fmt.Errorf("since %q is neither RFC 3339 nor a Go duration", s)
	}
	u := now.Add(-d).UTC()
	return &u, nil
}

// reconcile handles POST /api/v1/reconcile: engine 1 (replay stored events)
// then engine 2 (poll GitHub — Task "poll" of this plan; skipped until the
// App is configured). Synchronous by design: a scoped run is fast and the
// unscoped run is the scheduled case where waiting is acceptable (spec 013
// §API).
func (s *server) reconcile(w http.ResponseWriter, r *http.Request) {
	var req reconcileRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Repo != "" && req.Task != "" {
		writeErr(w, http.StatusUnprocessableEntity, "repo and task are mutually exclusive")
		return
	}
	since, err := parseSince(req.Since, s.st.Now())
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	runID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	resp := reconcileResponse{RunID: runID, DryRun: req.DryRun}

	// Engine 1. --task cannot bound replay (an ignored event's task binding
	// is unknown before its apply runs), so a task-scoped run goes straight
	// to polling.
	if req.Task == "" {
		replay, err := hooks.Replay(r.Context(), s.st,
			hooks.ReplayOptions{Repo: req.Repo, Since: since, DryRun: req.DryRun})
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		resp.Replay = replay
	}

	// Engine 2 lands in a later task of this plan (Task 13 replaces this
	// line with the reconcile.Poll call).
	resp.PollSkipped = "github app auth not configured"

	writeJSON(w, http.StatusOK, resp)
}
```

In `internal/api/server.go`, next to the whoami route:

```go
	mux.Handle("POST /api/v1/reconcile", s.auth(requireAdmin(s.reconcile)))
```

- [ ] **Step 4: Run the API tests**

Run: `go test ./internal/api/ -run TestReconcile -v`
Expected: PASS

- [ ] **Step 5: Add the client method and CLI command**

In `internal/cli/client.go`:

```go
// ReconcileInput is the request body of POST /api/v1/reconcile.
type ReconcileInput struct {
	Repo   string `json:"repo,omitempty"`
	Task   string `json:"task,omitempty"`
	Since  string `json:"since,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// Reconcile calls POST /api/v1/reconcile and returns the raw run report;
// the CLI renders it. Admin-only on the server; synchronous.
func (c *Client) Reconcile(ctx context.Context, in ReconcileInput) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/reconcile", in)
}
```

`internal/cmd/reconcile.go`:

```go
// lode reconcile: repair task and spec activity the ingestion path missed
// (spec 013). Operator command; the server does the work.

package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func newReconcileCmd() *cobra.Command {
	var repo, task, since string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Repair what webhook ingestion missed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if repo != "" && task != "" {
				return fmt.Errorf("--repo and --task are mutually exclusive")
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			raw, err := c.Reconcile(cmd.Context(), cli.ReconcileInput{
				Repo: repo, Task: task, Since: since, DryRun: dryRun,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			var resp struct {
				RunID  string `json:"run_id"`
				DryRun bool   `json:"dry_run"`
				Replay *struct {
					Candidates    int      `json:"candidates"`
					Replayed      int      `json:"replayed"`
					StillUnmapped int      `json:"still_unmapped"`
					Errors        []string `json:"errors"`
				} `json:"replay"`
				Poll        json.RawMessage `json:"poll"`
				PollSkipped string          `json:"poll_skipped"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("decode reconcile report: %w", err)
			}
			verb := "repaired"
			if resp.DryRun {
				verb = "would repair"
			}
			cmd.Printf("run %s\n", resp.RunID)
			if resp.Replay != nil {
				cmd.Printf("replay: %s %d of %d candidate event(s), %d still unmapped\n",
					verb, resp.Replay.Replayed, resp.Replay.Candidates, resp.Replay.StillUnmapped)
				for _, e := range resp.Replay.Errors {
					cmd.Printf("  error: %s\n", e)
				}
			}
			switch {
			case resp.PollSkipped != "":
				cmd.Printf("poll: skipped (%s)\n", resp.PollSkipped)
			case len(resp.Poll) > 0 && string(resp.Poll) != "null":
				cmd.Printf("poll: %s\n", string(resp.Poll))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "bound the run to one repo (owner/name)")
	cmd.Flags().StringVar(&task, "task", "", "bound the run to one task id")
	cmd.Flags().StringVar(&since, "since", "", "RFC 3339 time or Go duration (e.g. 720h), against the server clock")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report repairs without writing")
	return cmd
}

func init() {
	rootCmd.AddCommand(newReconcileCmd())
}
```

- [ ] **Step 6: Write the CLI wiring test**

`internal/cmd/reconcile_test.go`:

```go
package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReconcileFlagWiring(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/reconcile" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"run_id":"r1","dry_run":true,
			"replay":{"candidates":2,"replayed":2,"still_unmapped":0},
			"poll":null,"poll_skipped":"github app auth not configured"}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")

	out, err := runLode(t, "reconcile", "--repo", "acme/app", "--since", "720h", "--dry-run")
	if err != nil {
		t.Fatalf("reconcile: %v\n%s", err, out)
	}
	if gotBody != `{"repo":"acme/app","since":"720h","dry_run":true}`+"\n" &&
		gotBody != `{"repo":"acme/app","since":"720h","dry_run":true}` {
		t.Fatalf("request body = %s; want the three flags and nothing else", gotBody)
	}
	for _, want := range []string{"would repair 2", "skipped (github app auth not configured)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestReconcileRejectsRepoAndTask(t *testing.T) {
	if out, err := runLode(t, "reconcile", "--repo", "a/b", "--task", "WL-1"); err == nil {
		t.Fatalf("reconcile accepted --repo with --task:\n%s", out)
	}
}
```

- [ ] **Step 7: Run everything touched**

Run: `go test ./internal/api/... ./internal/cli/... ./internal/cmd/...`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/api internal/cli/client.go internal/cmd/reconcile.go internal/cmd/reconcile_test.go
git commit -m "Add POST /api/v1/reconcile and lode reconcile (replay engine)"
```

---

## Task 10: githubauth.RepoClient — the poll engine's GitHub reads

**Files:**
- Create: `internal/githubauth/repoclient.go`
- Test: `internal/githubauth/repoclient_test.go`

- [ ] **Step 1: Write the failing test**

`internal/githubauth/repoclient_test.go` (`package githubauth_test`; model
the fake — installation lookup + token mint routes, RSA key via
`rsa.GenerateKey` — on the existing `internal/githubauth/app_test.go`):

```go
package githubauth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
)

// newFakeGitHub serves the app-auth routes plus per-path canned JSON bodies.
func newFakeGitHub(t *testing.T, routes map[string]string) *githubauth.AppAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/app/installation":
			io.WriteString(w, `{"id": 42}`)
		case "/app/installations/42/access_tokens":
			io.WriteString(w, `{"token": "inst-token"}`)
		default:
			body, ok := routes[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"message":"not found"}`)
				return
			}
			io.WriteString(w, body)
		}
	}))
	t.Cleanup(srv.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &githubauth.AppAuth{AppID: "1", Key: key, BaseURL: srv.URL}
}

func TestRepoClientPR(t *testing.T) {
	app := newFakeGitHub(t, map[string]string{
		"/repos/acme/app/pulls/12": `{
			"number": 12, "title": "fix", "state": "closed", "merged": true,
			"body": "", "html_url": "u",
			"merge_commit_sha": "2222222222222222222222222222222222222222",
			"merged_at": "2026-07-20T10:00:00Z",
			"created_at": "2026-07-19T09:00:00Z",
			"head": {"ref": "lode/WL-1-fix", "sha": "1111111111111111111111111111111111111111"}
		}`,
	})
	rc, err := app.NewRepoClient(t.Context(), "acme/app")
	if err != nil {
		t.Fatalf("new repo client: %v", err)
	}
	pr, err := rc.PR(t.Context(), 12)
	if err != nil {
		t.Fatalf("PR: %v", err)
	}
	if !pr.Merged || pr.MergeCommitSHA == nil || *pr.MergeCommitSHA != "2222222222222222222222222222222222222222" {
		t.Fatalf("PR = %+v; want merged with a merge sha", pr)
	}
	if pr.HeadRef != "lode/WL-1-fix" || pr.MergedAt == nil {
		t.Fatalf("PR = %+v; want head ref and merged_at", pr)
	}
}

func TestRepoClientDefaultBranch(t *testing.T) {
	app := newFakeGitHub(t, map[string]string{
		"/repos/acme/app": `{"default_branch": "main"}`,
	})
	rc, err := app.NewRepoClient(t.Context(), "acme/app")
	if err != nil {
		t.Fatalf("new repo client: %v", err)
	}
	branch, err := rc.DefaultBranch(t.Context())
	if err != nil || branch != "main" {
		t.Fatalf("DefaultBranch = %q, %v; want main", branch, err)
	}
}

func TestRepoClientCommitOnBranch(t *testing.T) {
	sha, off := "2222222222222222222222222222222222222222", "3333333333333333333333333333333333333333"
	app := newFakeGitHub(t, map[string]string{
		"/repos/acme/app/compare/" + sha + "...main": `{"status": "ahead"}`,
		"/repos/acme/app/compare/" + off + "...main": `{"status": "diverged"}`,
	})
	rc, err := app.NewRepoClient(t.Context(), "acme/app")
	if err != nil {
		t.Fatalf("new repo client: %v", err)
	}
	on, err := rc.CommitOnBranch(t.Context(), "main", sha)
	if err != nil || !on {
		t.Fatalf("CommitOnBranch(ancestor) = %v, %v; want true", on, err)
	}
	on, err = rc.CommitOnBranch(t.Context(), "main", off)
	if err != nil || on {
		t.Fatalf("CommitOnBranch(diverged) = %v, %v; want false", on, err)
	}
	// An unknown sha 404s: not on the branch, not an error.
	on, err = rc.CommitOnBranch(t.Context(), "main", "4444444444444444444444444444444444444444")
	if err != nil || on {
		t.Fatalf("CommitOnBranch(unknown) = %v, %v; want false, nil", on, err)
	}
}

func TestRepoClientReleases(t *testing.T) {
	app := newFakeGitHub(t, map[string]string{
		"/repos/acme/app/releases": `[
			{"tag_name": "v2", "target_commitish": "2222222222222222222222222222222222222222", "published_at": "2026-07-21T00:00:00Z"},
			{"tag_name": "v1", "target_commitish": "main", "published_at": "2026-07-01T00:00:00Z"}
		]`,
	})
	rc, err := app.NewRepoClient(t.Context(), "acme/app")
	if err != nil {
		t.Fatalf("new repo client: %v", err)
	}
	rels, err := rc.Releases(t.Context())
	if err != nil || len(rels) != 2 || rels[0].TagName != "v2" {
		t.Fatalf("Releases = %+v, %v; want 2 releases, v2 first", rels, err)
	}
	_ = json.Valid // keep the import honest if unused elsewhere
}
```

(Remove the `json.Valid` line if `encoding/json` ends up unused.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/githubauth/ -run TestRepoClient`
Expected: FAIL — `app.NewRepoClient undefined`

- [ ] **Step 3: Write the implementation**

`internal/githubauth/repoclient.go`:

```go
// RepoClient: authenticated reads against one repo for the reconcile poll
// engine (spec 013 engine 2). One installation token is minted per repo per
// run — the spec's batching unit for rate limits.

package githubauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// RepoClient performs GitHub reads for one repo with an installation token.
type RepoClient struct {
	base string
	path string // escaped "owner/name"
	auth string
}

// NewRepoClient mints an installation token for repo and returns a client
// bound to it. Token minting failing IS the "App not installed" signal.
func (a *AppAuth) NewRepoClient(ctx context.Context, repo string) (*RepoClient, error) {
	path, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	token, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return nil, err
	}
	return &RepoClient{base: a.BaseURL, path: path, auth: "Bearer " + token}, nil
}

// PRFacts is the subset of a GitHub pull request the poll engine writes
// back through store.UpsertPR — the same fields the webhook payload carries.
type PRFacts struct {
	Number         int64      `json:"number"`
	Title          string     `json:"title"`
	State          string     `json:"state"`
	Merged         bool       `json:"merged"`
	Body           string     `json:"body"`
	HTMLURL        string     `json:"html_url"`
	CreatedAt      time.Time  `json:"created_at"`
	MergedAt       *time.Time `json:"merged_at"`
	MergeCommitSHA *string    `json:"merge_commit_sha"`
	Head           struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
}

// HeadRef and HeadSHA give PRFacts the flat accessors the poller uses.
func (p *PRFacts) HeadRef() string { return p.Head.Ref }
func (p *PRFacts) HeadSHA() string { return p.Head.SHA }

// PR reads one pull request's current truth.
func (c *RepoClient) PR(ctx context.Context, number int64) (*PRFacts, error) {
	var pr PRFacts
	u := fmt.Sprintf("%s/repos/%s/pulls/%d", c.base, c.path, number)
	code, err := githubJSON(ctx, http.MethodGet, u, c.auth, &pr)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("get PR %s#%d: status %d", c.path, number, code)
	}
	return &pr, nil
}

// DefaultBranch reads the repo's default branch name.
func (c *RepoClient) DefaultBranch(ctx context.Context) (string, error) {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	code, err := githubJSON(ctx, http.MethodGet, c.base+"/repos/"+c.path, c.auth, &repo)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK || repo.DefaultBranch == "" {
		return "", fmt.Errorf("get repo %s: status %d", c.path, code)
	}
	return repo.DefaultBranch, nil
}

// CommitOnBranch reports whether sha is an ancestor of (i.e. contained in)
// branch, via the compare API: base=sha, head=branch — "ahead" or
// "identical" means the branch contains the sha. A 404 (unknown sha) is
// false, not an error.
func (c *RepoClient) CommitOnBranch(ctx context.Context, branch, sha string) (bool, error) {
	var cmp struct {
		Status string `json:"status"`
	}
	u := fmt.Sprintf("%s/repos/%s/compare/%s...%s",
		c.base, c.path, url.PathEscape(sha), url.PathEscape(branch))
	code, err := githubJSON(ctx, http.MethodGet, u, c.auth, &cmp)
	if err != nil {
		return false, err
	}
	switch code {
	case http.StatusOK:
		return cmp.Status == "ahead" || cmp.Status == "identical", nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("compare %s %s...%s: status %d", c.path, sha, branch, code)
	}
}

// ReleaseFacts is one published release as the poll engine consumes it.
type ReleaseFacts struct {
	TagName         string    `json:"tag_name"`
	TargetCommitish string    `json:"target_commitish"`
	PublishedAt     time.Time `json:"published_at"`
}

// Releases lists the repo's releases, newest first (GitHub's order).
// per_page=100 matches DiscoverDoneState's pagination stance.
func (c *RepoClient) Releases(ctx context.Context) ([]ReleaseFacts, error) {
	var rels []ReleaseFacts
	u := c.base + "/repos/" + c.path + "/releases?per_page=100"
	code, err := githubJSON(ctx, http.MethodGet, u, c.auth, &rels)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("list releases %s: status %d", c.path, code)
	}
	return rels, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/githubauth/... -v -run TestRepoClient`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/githubauth/repoclient.go internal/githubauth/repoclient_test.go
git commit -m "Add per-repo installation-token GitHub reads for reconcile"
```

---

## Task 11: Poll-candidate store queries

**Files:**
- Modify: `internal/store/reconcile.go` (append)
- Test: `internal/store/reconcile_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/reconcile_test.go`:

```go
func TestPollCandidates(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := s.AddRepo(ctx, "demo", "acme/app"); err != nil {
		t.Fatalf("map repo: %v", err)
	}

	// Seed through RecordEvent: Transition logs to state_log, whose event_id
	// is a NOT NULL FK to events (0001_baseline.up.sql:177), so it needs a
	// real event id.
	var inReview, merged string
	if _, _, err := s.RecordEvent(ctx, "cli", "seed-"+t.Name(), "test.seed", nil,
		func(tx *sql.Tx, eventID int64) error {
			now := s.Now()
			t1, err := CreateTask(tx, now, TaskInput{ProjectID: "demo", Title: "a", Priority: "medium", Kind: "bug"})
			if err != nil {
				return err
			}
			inReview = t1.ID
			t2, err := CreateTask(tx, now, TaskInput{ProjectID: "demo", Title: "b", Priority: "medium", Kind: "bug"})
			if err != nil {
				return err
			}
			merged = t2.ID
			// t1: in_review with an open PR. t2: only a task commit, ready.
			if err := Transition(tx, now, inReview, "ready", "in_progress", eventID); err != nil {
				return err
			}
			if err := Transition(tx, now, inReview, "in_progress", "in_review", eventID); err != nil {
				return err
			}
			if _, err := UpsertPR(tx, PullRequest{
				Repo: "acme/app", Number: 12, Title: "fix", State: "open",
				HeadRef: "lode/" + inReview + "-fix",
				HeadSHA: "1111111111111111111111111111111111111111",
				URL:     "u", OpenedAt: now,
			}, ""); err != nil {
				return err
			}
			return InsertTaskCommit(tx, TaskCommit{
				TaskID: merged, Repo: "acme/app",
				SHA: "5555555555555555555555555555555555555555", Source: "pr", SeenAt: now,
			})
		}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	all, err := s.PollCandidates(ctx, "", "", nil)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("candidates = %+v; want both tasks", all)
	}

	one, err := s.PollCandidates(ctx, "", inReview, nil)
	if err != nil {
		t.Fatalf("task-bounded: %v", err)
	}
	if len(one) != 1 || one[0].TaskID != inReview || one[0].Repo != "acme/app" {
		t.Fatalf("task-bounded = %+v; want only %s", one, inReview)
	}

	none, err := s.PollCandidates(ctx, "other/repo", "", nil)
	if err != nil {
		t.Fatalf("repo-bounded: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("repo-bounded = %+v; want none", none)
	}

	unlanded, err := s.UnlandedTaskCommits(ctx, merged, "acme/app")
	if err != nil {
		t.Fatalf("unlanded: %v", err)
	}
	if len(unlanded) != 1 || unlanded[0] != "5555555555555555555555555555555555555555" {
		t.Fatalf("unlanded = %v; want the seeded sha", unlanded)
	}
	// Once the sha is on main, it is no longer unlanded.
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := AppendMainCommit(tx, "acme/app", "5555555555555555555555555555555555555555", s.Now())
		return err
	}); err != nil {
		t.Fatalf("append main commit: %v", err)
	}
	unlanded, err = s.UnlandedTaskCommits(ctx, merged, "acme/app")
	if err != nil {
		t.Fatalf("unlanded after landing: %v", err)
	}
	if len(unlanded) != 0 {
		t.Fatalf("unlanded after landing = %v; want none", unlanded)
	}
}
```

If `UpsertPR`'s branch correlation does not attribute the PR to `inReview`
via the `lode/<id>-` head ref, check `internal/store/changes.go:151` for the
correlation rule it actually applies (branch prefix vs. body marker) and
adjust the seeded `HeadRef`/body accordingly — the store is the authority,
not this test.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run TestPollCandidates`
Expected: FAIL — `undefined: (*Store).PollCandidates`

- [ ] **Step 3: Write the implementation**

Append to `internal/store/reconcile.go`:

```go
// PollCandidate is one (task, repo) pair the poll engine should ask GitHub
// about.
type PollCandidate struct {
	TaskID string
	State  string
	Repo   string
}

// PollCandidates returns tasks whose delivery state can still advance
// (the same advanceable set TasksBelowFrontier uses) paired with each repo
// they have recorded activity in — a PR or a task commit; a task with
// neither has nothing to poll. repo/task/since bound the set (spec 013);
// since compares tasks.updated_at against the server clock.
//
// Spec 013 open question 1: this set may be too large for an unscoped
// org-wide run; --since/--repo are the intended controls.
func (s *Store) PollCandidates(ctx context.Context, repo, task string, since *time.Time) ([]PollCandidate, error) {
	q := `SELECT DISTINCT t.id, t.state, x.repo
	      FROM tasks t
	      JOIN (SELECT task_id, repo FROM pull_requests WHERE task_id IS NOT NULL
	            UNION
	            SELECT task_id, repo FROM task_commits) x ON x.task_id = t.id
	      WHERE t.state IN ('ready','in_progress','in_review','merged','deployed_dev')`
	var args []any
	if repo != "" {
		args = append(args, repo)
		q += fmt.Sprintf(` AND x.repo = $%d`, len(args))
	}
	if task != "" {
		args = append(args, task)
		q += fmt.Sprintf(` AND t.id = $%d`, len(args))
	}
	if since != nil {
		args = append(args, since.UTC())
		q += fmt.Sprintf(` AND t.updated_at >= $%d`, len(args))
	}
	q += ` ORDER BY t.id, x.repo`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("poll candidates: %w", err)
	}
	defer rows.Close()

	var out []PollCandidate
	for rows.Next() {
		var c PollCandidate
		if err := rows.Scan(&c.TaskID, &c.State, &c.Repo); err != nil {
			return nil, fmt.Errorf("scan poll candidate: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("poll candidates: %w", err)
	}
	return out, nil
}

// UnlandedTaskCommits returns a task's recorded commit shas in repo that are
// not yet known to be on the default branch (absent from main_commits),
// sorted. These are what the poll engine checks against GitHub.
func (s *Store) UnlandedTaskCommits(ctx context.Context, taskID, repo string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT tc.sha FROM task_commits tc
		 WHERE tc.task_id = $1 AND tc.repo = $2
		   AND NOT EXISTS (SELECT 1 FROM main_commits mc
		                   WHERE mc.repo = tc.repo AND mc.sha = tc.sha)
		 ORDER BY tc.sha`, taskID, repo)
	if err != nil {
		return nil, fmt.Errorf("unlanded commits for %s in %s: %w", taskID, repo, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, fmt.Errorf("scan unlanded commit: %w", err)
		}
		out = append(out, sha)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("unlanded commits for %s in %s: %w", taskID, repo, err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/store/ -run TestPollCandidates -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/reconcile.go internal/store/reconcile_test.go
git commit -m "Add poll-candidate queries for reconcile engine 2"
```

---

## Task 12: Engine 2 — poll GitHub

**Files:**
- Create: `internal/reconcile/poll.go`
- Test: `internal/reconcile/poll_test.go`

- [ ] **Step 1: Write the failing test**

`internal/reconcile/poll_test.go` (`package reconcile_test`; Postgres via
`store.OpenTestStore`, GitHub via the Task 10 fake — copy `newFakeGitHub`
here or lift it into a shared exported test helper only if `githubauth`
already exports one; do not export new production API for tests):

```go
package reconcile_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/reconcile"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

const (
	headSHA  = "1111111111111111111111111111111111111111"
	mergeSHA = "2222222222222222222222222222222222222222"
)

func newFakeGitHub(t *testing.T, routes map[string]string) *githubauth.AppAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/repos/acme/app/installation":
			io.WriteString(w, `{"id": 42}`)
		case r.URL.Path == "/app/installations/42/access_tokens":
			io.WriteString(w, `{"token": "inst-token"}`)
		default:
			body, ok := routes[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"message":"not found"}`)
				return
			}
			io.WriteString(w, body)
		}
	}))
	t.Cleanup(srv.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &githubauth.AppAuth{AppID: "1", Key: key, BaseURL: srv.URL}
}

// seedStaleTask: the backbone believes the PR is open (task in_review), but
// GitHub will report it merged onto main — ingestion was down for the
// pull_request.closed and push webhooks.
func seedStaleTask(t *testing.T, st *store.Store) (taskID string) {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := st.AddRepo(ctx, "demo", "acme/app"); err != nil {
		t.Fatalf("map repo: %v", err)
	}
	// state_log.event_id is a NOT NULL FK to events, so the seed transitions
	// run under a real seed event.
	if _, _, err := st.RecordEvent(ctx, "cli", "seed-"+t.Name(), "test.seed", nil,
		func(tx *sql.Tx, eventID int64) error {
			now := st.Now()
			task, err := store.CreateTask(tx, now, store.TaskInput{
				ProjectID: "demo", Title: "fix crash", Priority: "medium", Kind: "bug",
			})
			if err != nil {
				return err
			}
			taskID = task.ID
			if err := store.Transition(tx, now, taskID, "ready", "in_progress", eventID); err != nil {
				return err
			}
			if err := store.Transition(tx, now, taskID, "in_progress", "in_review", eventID); err != nil {
				return err
			}
			_, err = store.UpsertPR(tx, store.PullRequest{
				Repo: "acme/app", Number: 12, Title: "fix", State: "open",
				HeadRef: "lode/" + taskID + "-fix", HeadSHA: headSHA,
				URL: "u", OpenedAt: now,
			}, "")
			return err
		}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return taskID
}

func mergedPRRoutes() map[string]string {
	return map[string]string{
		"/repos/acme/app": `{"default_branch": "main"}`,
		"/repos/acme/app/pulls/12": `{
			"number": 12, "title": "fix", "state": "closed", "merged": true,
			"body": "", "html_url": "u",
			"merge_commit_sha": "` + mergeSHA + `",
			"merged_at": "2026-07-20T10:00:00Z", "created_at": "2026-07-19T09:00:00Z",
			"head": {"ref": "lode/WL-1-fix", "sha": "` + headSHA + `"}
		}`,
		"/repos/acme/app/compare/" + mergeSHA + "...main": `{"status": "ahead"}`,
		"/repos/acme/app/compare/" + headSHA + "...main":  `{"status": "diverged"}`,
		"/repos/acme/app/releases":                        `[]`,
	}
}

func taskState(t *testing.T, st *store.Store, taskID string) string {
	t.Helper()
	task, err := st.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	return task.State
}

func TestPollRepairsMergedWhileDown(t *testing.T) {
	st := store.OpenTestStore(t)
	taskID := seedStaleTask(t, st)
	app := newFakeGitHub(t, mergedPRRoutes())
	ctx := context.Background()

	res, err := reconcile.Poll(ctx, st, app, reconcile.Options{RunID: "run-1"})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if res.Candidates != 1 || len(res.Repaired) != 1 {
		t.Fatalf("result = %+v; want 1 candidate repaired", res)
	}
	if got := taskState(t, st, taskID); got != "merged" {
		t.Fatalf("task state = %q; want merged (repo done_state defaults to merged)", got)
	}

	// The transition attributes to the reconcile.poll system event.
	entries, err := st.StateLogForEntity(ctx, "task", taskID)
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no state_log entries")
	}
	evs := eventByID(t, st, entries[len(entries)-1].EventID)
	if evs.Source != "system" || evs.Type != "reconcile.poll" || evs.ExternalID != "run-1" {
		t.Fatalf("attributed event = %+v; want the reconcile.poll run event", evs)
	}

	// Convergence: a second run records its run event but changes nothing.
	before := len(entries)
	if _, err := reconcile.Poll(ctx, st, app, reconcile.Options{RunID: "run-2"}); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	entries, err = st.StateLogForEntity(ctx, "task", taskID)
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	if len(entries) != before {
		t.Fatalf("second run added %d state_log entries; want 0", len(entries)-before)
	}
}

func TestPollDryRunReportsWithoutWriting(t *testing.T) {
	st := store.OpenTestStore(t)
	taskID := seedStaleTask(t, st)
	app := newFakeGitHub(t, mergedPRRoutes())

	res, err := reconcile.Poll(context.Background(), st, app, reconcile.Options{RunID: "run-dry", DryRun: true})
	if err != nil {
		t.Fatalf("dry-run poll: %v", err)
	}
	if !res.DryRun || len(res.Repaired) != 1 {
		t.Fatalf("dry-run result = %+v; want the same 1 repair reported", res)
	}
	if got := taskState(t, st, taskID); got != "in_review" {
		t.Fatalf("dry-run advanced the task to %q; want untouched in_review", got)
	}
}

// eventByID reads one event row for attribution assertions.
func eventByID(t *testing.T, st *store.Store, id int64) store.Event {
	t.Helper()
	ev, err := st.EventByID(context.Background(), id)
	if err != nil {
		t.Fatalf("event %d: %v", id, err)
	}
	return *ev
}
```

Add the small read `EventByID` to `internal/store/reconcile.go` (it is
generally useful for attribution checks and the timeline):

```go
// EventByID returns one event row.
func (s *Store) EventByID(ctx context.Context, id int64) (*Event, error) {
	var e Event
	err := s.db.QueryRowContext(ctx,
		`SELECT id, source, external_id, type, payload, received_at
		 FROM events WHERE id = $1`, id).
		Scan(&e.ID, &e.Source, &e.ExternalID, &e.Type, &e.Payload, &e.ReceivedAt)
	if err != nil {
		return nil, fmt.Errorf("event %d: %w", id, err)
	}
	e.ReceivedAt = e.ReceivedAt.UTC()
	return &e, nil
}
```

As in Task 11, verify the seeded `HeadRef` actually correlates the PR to the
task under `UpsertPR`'s rules before trusting a failure.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/reconcile/...`
Expected: FAIL — `no required module provides package .../internal/reconcile`

- [ ] **Step 3: Write the poller**

`internal/reconcile/poll.go`:

```go
// Package reconcile implements engine 2 of lode reconcile (spec 013): ask
// GitHub the current truth about candidate tasks, write the missing facts
// through the existing upserts, and let store.ResolveDelivery advance the
// state. Because ResolveDelivery derives delivery state from recorded facts,
// repairing facts is sufficient — no event ordering to replay.
//
// Two phases per run: gather (network reads, no writes) then apply (one
// store.RecordEvent transaction under a single source='system' event of type
// "reconcile.poll", external_id = run id). Facts and transitions attribute
// to that event: the task advanced because reconcile observed it.
package reconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// Options bound one poll run. RunID is the system event's external_id and
// must be unique per run.
type Options struct {
	Repo   string
	Task   string
	Since  *time.Time
	DryRun bool
	RunID  string
}

// TaskRepair is what the run did (or would do) for one task.
type TaskRepair struct {
	TaskID        string   `json:"task_id"`
	Repo          string   `json:"repo"`
	State         string   `json:"state"` // state before the run
	PRsUpdated    []int64  `json:"prs_updated,omitempty"`
	CommitsLanded []string `json:"commits_landed,omitempty"`
}

// PollResult is one run's report.
type PollResult struct {
	RunID      string       `json:"run_id"`
	DryRun     bool         `json:"dry_run"`
	Candidates int          `json:"candidates"`
	Repaired   []TaskRepair `json:"repaired"`
	Errors     []string     `json:"errors,omitempty"`
}

// repoFacts is everything gathered for one repo before the apply phase.
type repoFacts struct {
	repo          string
	prs           []store.PullRequest // fresh facts, ready for UpsertPR
	prBodies      map[int64]string
	landedSHAs    []string // shas GitHub confirms are on the default branch
	mergedCommits []store.TaskCommit
	releases      []githubauth.ReleaseFacts
	tasks         []store.PollCandidate
}

// Poll runs engine 2. app must be non-nil; the API layer skips polling (with
// an explanation) when the GitHub App is not configured.
func Poll(ctx context.Context, st *store.Store, app *githubauth.AppAuth, opts Options) (*PollResult, error) {
	candidates, err := st.PollCandidates(ctx, opts.Repo, opts.Task, opts.Since)
	if err != nil {
		return nil, err
	}
	res := &PollResult{RunID: opts.RunID, DryRun: opts.DryRun, Candidates: len(candidates)}
	if len(candidates) == 0 {
		return res, nil
	}

	byRepo := map[string][]store.PollCandidate{}
	for _, c := range candidates {
		byRepo[c.Repo] = append(byRepo[c.Repo], c)
	}

	var gathered []*repoFacts
	for repo, tasks := range byRepo {
		facts, err := gatherRepo(ctx, st, app, repo, tasks)
		if err != nil {
			// One repo failing (App not installed there, rate limit) must not
			// abort the run for every other repo.
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", repo, err))
			continue
		}
		gathered = append(gathered, facts)
	}

	for _, f := range gathered {
		for _, c := range f.tasks {
			repair := TaskRepair{TaskID: c.TaskID, Repo: f.repo, State: c.State}
			for _, pr := range f.prs {
				if pr.TaskID != nil && *pr.TaskID == c.TaskID {
					repair.PRsUpdated = append(repair.PRsUpdated, pr.Number)
				}
			}
			repair.CommitsLanded = f.landedSHAs
			if len(repair.PRsUpdated) > 0 || len(repair.CommitsLanded) > 0 {
				res.Repaired = append(res.Repaired, repair)
			}
		}
	}
	if opts.DryRun || len(res.Repaired) == 0 {
		return res, nil
	}

	summary, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("encode run summary: %w", err)
	}
	_, _, err = st.RecordEvent(ctx, "system", opts.RunID, "reconcile.poll", summary,
		func(tx *sql.Tx, eventID int64) error {
			return applyFacts(tx, st.Now(), eventID, gathered)
		})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// gatherRepo reads GitHub once per repo: one installation token, then the
// PRs, default-branch membership, and releases for that repo's candidate
// tasks. Read-only.
func gatherRepo(ctx context.Context, st *store.Store, app *githubauth.AppAuth, repo string, tasks []store.PollCandidate) (*repoFacts, error) {
	rc, err := app.NewRepoClient(ctx, repo)
	if err != nil {
		return nil, err
	}
	defaultBranch, err := rc.DefaultBranch(ctx)
	if err != nil {
		return nil, err
	}

	f := &repoFacts{repo: repo, prBodies: map[int64]string{}, tasks: tasks}
	now := st.Now()
	shasToCheck := map[string]bool{}

	for _, c := range tasks {
		prs, err := st.PRsForTask(ctx, c.TaskID)
		if err != nil {
			return nil, err
		}
		for _, known := range prs {
			if known.Repo != repo {
				continue
			}
			gh, err := rc.PR(ctx, known.Number)
			if err != nil {
				return nil, err
			}
			state := "open"
			if gh.State == "closed" {
				state = "closed"
				if gh.Merged {
					state = "merged"
				}
			}
			openedAt := gh.CreatedAt
			if openedAt.IsZero() {
				openedAt = now
			}
			taskID := c.TaskID
			f.prs = append(f.prs, store.PullRequest{
				Repo: repo, Number: gh.Number, Title: gh.Title, State: state,
				TaskID: &taskID, HeadRef: gh.HeadRef(), HeadSHA: gh.HeadSHA(),
				MergeSHA: gh.MergeCommitSHA, URL: gh.HTMLURL,
				OpenedAt: openedAt, MergedAt: gh.MergedAt,
			})
			f.prBodies[gh.Number] = gh.Body
			if gh.Merged {
				if sha := gh.HeadSHA(); sha != "" {
					f.mergedCommits = append(f.mergedCommits, store.TaskCommit{
						TaskID: c.TaskID, Repo: repo, SHA: sha, Source: "pr", SeenAt: now,
					})
				}
				if gh.MergeCommitSHA != nil && *gh.MergeCommitSHA != "" {
					f.mergedCommits = append(f.mergedCommits, store.TaskCommit{
						TaskID: c.TaskID, Repo: repo, SHA: *gh.MergeCommitSHA, Source: "pr", SeenAt: now,
					})
					shasToCheck[*gh.MergeCommitSHA] = true
				}
			}
		}
		// Commits the backbone recorded that never showed up on main.
		unlanded, err := st.UnlandedTaskCommits(ctx, c.TaskID, repo)
		if err != nil {
			return nil, err
		}
		for _, sha := range unlanded {
			shasToCheck[sha] = true
		}
	}

	for sha := range shasToCheck {
		on, err := rc.CommitOnBranch(ctx, defaultBranch, sha)
		if err != nil {
			return nil, err
		}
		if on {
			f.landedSHAs = append(f.landedSHAs, sha)
		}
	}

	// Releases only matter for release-terminated repos; asking costs one
	// request and applyFacts ignores unresolvable ones, so ask uniformly.
	rels, err := rc.Releases(ctx)
	if err != nil {
		return nil, err
	}
	f.releases = rels
	return f, nil
}

// applyFacts writes one run's gathered facts inside the reconcile.poll
// event's transaction: PR upserts, task commits, main-branch appends,
// release frontiers, then ResolveDelivery per candidate. Every write is an
// upsert or a from-state-guarded transition, so a re-run converges.
func applyFacts(tx *sql.Tx, now time.Time, eventID int64, gathered []*repoFacts) error {
	for _, f := range gathered {
		for _, pr := range f.prs {
			if _, err := store.UpsertPR(tx, pr, f.prBodies[pr.Number]); err != nil {
				return err
			}
		}
		for _, tc := range f.mergedCommits {
			if err := store.InsertTaskCommit(tx, tc); err != nil {
				return err
			}
		}
		for _, sha := range f.landedSHAs {
			// Guarded: only append shas main_commits does not already know,
			// so re-running never duplicates the frontier.
			known, err := store.MainIDForSHA(tx, f.repo, sha)
			if err != nil {
				return err
			}
			if known == nil {
				if _, err := store.AppendMainCommit(tx, f.repo, sha, now); err != nil {
					return err
				}
			}
		}
		for _, rel := range f.releases {
			mainID, err := store.MainIDForSHA(tx, f.repo, rel.TargetCommitish)
			if err != nil {
				return err
			}
			if mainID == nil {
				// target_commitish is often a branch name; without a
				// resolvable sha there is no frontier to record. Conservative:
				// skip rather than guess (the webhook path's LatestMainID
				// fallback is only correct at delivery time).
				continue
			}
			publishedAt := rel.PublishedAt
			if publishedAt.IsZero() {
				publishedAt = now
			}
			if err := store.SetReleaseFrontier(tx, f.repo, rel.TagName, *mainID, publishedAt); err != nil {
				return err
			}
		}
		for _, c := range f.tasks {
			if err := store.ResolveDelivery(tx, now, c.TaskID, f.repo, eventID); err != nil {
				return err
			}
		}
	}
	return nil
}
```

If `store.SetReleaseFrontier` is not forward-only on re-runs, check its body
(`internal/store/delivery.go:280`) before assuming — the convergence test
will catch a regression either way.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/reconcile/... -v`
Expected: PASS (2 tests). The convergence assertion is the important one: a
second run must add no state_log entries.

- [ ] **Step 5: Commit**

```bash
git add internal/reconcile internal/store/reconcile.go internal/store/reconcile_test.go
git commit -m "Poll GitHub for missed delivery facts (reconcile engine 2)"
```

---

## Task 13: Wire engine 2 into the endpoint; finish the surface

**Files:**
- Modify: `internal/api/reconcile.go` (poll wiring)
- Modify: `README.md`
- Test: `internal/api/reconcile_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/api/reconcile_test.go`:

```go
// TestReconcilePollSkippedWithoutApp: with no GitHub App configured the
// endpoint still runs replay and says why polling did not happen.
func TestReconcilePollSkippedWithoutApp(t *testing.T) {
	_, h, token := newTestServer(t)
	rec := doReq(t, h, http.MethodPost, "/api/v1/reconcile", token, map[string]any{"dry_run": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("reconcile: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Poll        json.RawMessage `json:"poll"`
		PollSkipped string          `json:"poll_skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(resp.Poll) != "null" || resp.PollSkipped == "" {
		t.Fatalf("poll = %s, skipped = %q; want null + an explanation", resp.Poll, resp.PollSkipped)
	}
}
```

A full-stack poll-through-the-endpoint test needs an `api.NewServer` built
with a fake App key against a fake GitHub — the poll behavior itself is
already covered in `internal/reconcile/poll_test.go`, so the API layer test
only asserts the wiring branch. If `newTestServer` cannot be parameterized
with an `api.Config` without churn, this skipped-branch test plus the
poll-package tests are sufficient; do not rebuild the server fixture for one
assertion.

- [ ] **Step 2: Wire the poller in**

In `internal/api/reconcile.go`, replace the Task 9 placeholder tail of
`reconcile` (the `resp.PollSkipped = ...` line and the guard) with:

```go
	if s.appAuth == nil {
		resp.PollSkipped = "github app auth not configured (LODE_GITHUB_APP_ID / LODE_GITHUB_APP_PRIVATE_KEY)"
	} else {
		poll, err := reconcile.Poll(r.Context(), s.st, s.appAuth, reconcile.Options{
			Repo: req.Repo, Task: req.Task, Since: since, DryRun: req.DryRun, RunID: runID,
		})
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		resp.Poll = poll
	}
```

with `"github.com/sunstoneinstitute/worklode/internal/reconcile"` in the
imports, and tighten the response type:

```go
	Poll *reconcile.PollResult `json:"poll"`
```

- [ ] **Step 3: Run the API and cmd suites**

Run: `go test ./internal/api/... ./internal/cmd/...`
Expected: PASS — the `lode reconcile` renderer from Task 9 already prints
the poll section when present.

- [ ] **Step 4: Document the commands**

In `README.md`, in the CLI command section, add three short entries (match
the file's existing style and brevity):

- `lode doctor` — client-side setup checks; exits non-zero on any failure
  and names each fix; works with the server unreachable.
- `lode project doctor [repo]` — per-repo webhook-ingestion health
  (admin): App installation, last delivery, unapplied events, unmapped
  senders; a stale repo is the cue to run reconcile.
- `lode reconcile [--repo X | --task Y] [--since D] [--dry-run]` — repair
  what ingestion missed (admin): replays stored `*.ignored` events, then
  polls GitHub for missed PR/merge/release facts; `--since` takes RFC 3339
  or a Go duration against the server clock.

- [ ] **Step 5: Full verification**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS across the repo (Postgres required for the store-backed
suites, as usual).

- [ ] **Step 6: Commit**

```bash
git add internal/api README.md
git commit -m "Run the poll engine from POST /api/v1/reconcile"
```

---

## Acceptance criteria → tasks

| Spec acceptance criterion | Covered by |
|---|---|
| Replay of a pre-mapping `*.ignored` event matches a live delivery; original event id in `state_log`; `applied_at` set; second replay a no-op | Task 4 tests |
| Poll advances a merged-while-down task; attribution to `reconcile.poll`; second run a no-op; `--dry-run` reports the same repair, writes nothing | Task 12 tests |
| Spec-drift criteria | Obsolete (014 §6) — deliberately not built |
| `project doctor`: no App installation / last webhook predates mapping / unmapped senders | Task 8 (`app_installed`+`app_error`, `stale`, `unmapped_senders`) |
| `lode doctor` exits non-zero and names the fix for each failure class | Task 6 table test |
| Deterministic `--json` on every command | root `--json` + sorted store queries (`ORDER BY` in every reconcile query) |
