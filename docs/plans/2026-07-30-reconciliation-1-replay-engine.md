---
status: accepted
task: WL-10
covers: docs/specs/013-reconciliation.md
---
# Reconciliation 1/3: data model & replay engine — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 1 of 3. Task numbering is global across the series: this plan
holds Tasks 1–4; `2026-07-30-reconciliation-2-cli-surface.md` holds Tasks 5–9;
`2026-07-30-reconciliation-3-poll-engine.md` holds Tasks 10–13. Each part must
be merged before the next starts.

**Goal:** Land the reconciliation data model (`events.applied_at`,
`project_repos.mapped_at`) and engine 1: recover task activity the webhook
ingestion path missed by replaying stored `*.ignored` events. Server-internal —
the endpoints and CLI commands ship in parts 2 and 3.

**Architecture:** One migration adds `events.applied_at` (apply-completion
marker) and `project_repos.mapped_at` (so doctor can spot deliveries that
predate a mapping). The webhook apply routing moves off the HTTP handler onto
a transport-independent `applier`, shared by the webhook path and a new
replayer (`hooks.Replay`, engine 1).

**Tech Stack:** Go 1.x, `net/http` mux (Go 1.22 routing patterns),
PostgreSQL via `database/sql`, standard-library testing.

**Spec:** `docs/specs/013-reconciliation.md`, read with its amendments from
`docs/specs/025-documents-in-the-backbone.md` §6: **engine 3
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
`lode project doctor`, `lode reconcile`. That is the series' scope; this part
owns the migration, the applier refactor, and replay.

Design calls this plan makes (record here, not re-litigated in tasks):

- **`project_repos.mapped_at` is added** even though the spec's data-model
  section lists only `applied_at`: the acceptance criterion "a repo whose
  last webhook predates its mapping" is undecidable without a mapping
  timestamp. Existing rows backfill to epoch so no current repo retroactively
  alarms; new rows default to `now()` so `addRepo` needs no change.
- **`applied_at` backfill**: pre-existing non-`.ignored` events were applied
  live, so they backfill to `received_at`; `.ignored` events stay NULL and
  are exactly the replay candidates.
- **Replayed events keep their stored type** (`push.ignored` stays
  `push.ignored`); `applied_at` is the completion marker, and history stays
  intact.

## Not in this series (owned elsewhere)

- Engine 3 / `task_docs` / spec-drift reporting — superseded by spec 025 §11
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
| `internal/store/reconcile.go` | `MarkEventApplied`, `UnappliedGitHubEvents` (this plan); doctor and poll queries appended in parts 2–3 |
| `internal/store/reconcile_test.go` | the above against ephemeral Postgres |
| `internal/hooks/apply.go` | `applier` — the transport-independent apply router (moved off `githubHandler`) |
| `internal/hooks/replay.go` | engine 1: `Replay` over unapplied github events |
| `internal/hooks/replay_test.go` | replay after mapping; idempotence; provenance; dry-run |

**Modified files**

| Path | Change |
|---|---|
| `internal/hooks/github.go` | handler holds an `applier`; mapped-repo applies wrapped to set `applied_at` |
| `internal/hooks/push.go`, `internal/hooks/deployment.go` | apply methods' receiver becomes `*applier` (mechanical) |

**Test commands**

- Both suites need Postgres (`store.OpenTestStore`):
  `go test ./internal/store/... ./internal/hooks/...`
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
-- Reconciliation (docs/specs/013-reconciliation.md, amended by 025 §11):
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

## Acceptance criteria → tasks

| Spec acceptance criterion | Covered by |
|---|---|
| Replay of a pre-mapping `*.ignored` event matches a live delivery; original event id in `state_log`; `applied_at` set; second replay a no-op | Task 4 tests |
| Spec-drift criteria | Obsolete (025 §11) — deliberately not built |
