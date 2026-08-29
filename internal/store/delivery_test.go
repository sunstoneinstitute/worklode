package store

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
	"time"
)

// seedDeliveryTask creates a project, repo mapping, and one ready task,
// returning the task id. Mirrors the setup helpers in tasks_test.go.
func seedDeliveryTask(t *testing.T, s *Store) string {
	t.Helper()
	ctx := context.Background()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO projects (id, name, key, next_task_num) VALUES ('p1','P1','P1',2)`)
	mustExec(`INSERT INTO project_repos (project_id, repo) VALUES ('p1','acme/app')`)
	mustExec(`INSERT INTO tasks (id, project_id, title, priority, kind, state, created_at, updated_at)
	          VALUES ('P1-1','p1','t','high','feature','ready', now(), now())`)
	return "P1-1"
}

func TestMainCommitsAndLanding(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	now := time.Now()

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "aaa1", Source: "branch_push", SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	// duplicate insert is a no-op
	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "aaa1", Source: "branch_push", SeenAt: now}); err != nil {
		t.Fatal(err)
	}

	id1, err := AppendMainCommit(tx, "acme/app", "m1", now)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := AppendMainCommit(tx, "acme/app", "aaa1", now)
	if err != nil {
		t.Fatal(err)
	}
	if id2 <= id1 {
		t.Fatalf("ids not increasing: %d then %d", id1, id2)
	}
	// re-append returns the existing id
	again, err := AppendMainCommit(tx, "acme/app", "m1", now)
	if err != nil || again != id1 {
		t.Fatalf("re-append: got %d, %v; want %d", again, err, id1)
	}

	landed, err := LandedMainID(tx, taskID, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if landed == nil || *landed != id2 {
		t.Fatalf("landed = %v, want %d", landed, id2)
	}

	// deploy sha mapping
	if err := MapDeploySHA(tx, "acme/app", "dep1", id2); err != nil {
		t.Fatal(err)
	}
	mid, err := MainIDForSHA(tx, "acme/app", "dep1")
	if err != nil || mid == nil || *mid != id2 {
		t.Fatalf("MainIDForSHA(dep1) = %v, %v; want %d", mid, err, id2)
	}
	mid, err = MainIDForSHA(tx, "acme/app", "m1")
	if err != nil || mid == nil || *mid != id1 {
		t.Fatalf("MainIDForSHA(m1) = %v, %v; want %d", mid, err, id1)
	}
	mid, err = MainIDForSHA(tx, "acme/app", "nope")
	if err != nil || mid != nil {
		t.Fatalf("MainIDForSHA(nope) = %v, %v; want nil, nil", mid, err)
	}

	repo, mid2, err := MainIDForSHAAnyRepo(tx, "dep1")
	if err != nil || mid2 == nil || repo != "acme/app" {
		t.Fatalf("MainIDForSHAAnyRepo = %q, %v, %v", repo, mid2, err)
	}
}

func TestMainSHAForID(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	now := time.Now()

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	id, err := AppendMainCommit(tx, "acme/app", "abc1230000000000000000000000000000000000", now)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := MainSHAForID(tx, id)
	if err != nil {
		t.Fatal(err)
	}
	if sha != "abc1230000000000000000000000000000000000" {
		t.Fatalf("MainSHAForID = %q, want the appended sha", sha)
	}
}

func TestMainSHAForIDUnknownIsEmpty(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	sha, err := MainSHAForID(tx, 999999)
	if err != nil {
		t.Fatal(err)
	}
	if sha != "" {
		t.Fatalf("MainSHAForID(unknown) = %q, want empty", sha)
	}
}

func TestLandedMainIDNoCommits(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	landed, err := LandedMainID(tx, taskID, "acme/app")
	if err != nil || landed != nil {
		t.Fatalf("got %v, %v; want nil, nil", landed, err)
	}
}

// TestInsertTaskCommitUnknownTask verifies that a task id with no matching
// task is a no-op rather than an FK violation: correlation failures must
// never fail the delivery transaction. The load-bearing assertion is the
// second one — the transaction must still be usable afterward.
func TestInsertTaskCommitUnknownTask(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	seedDeliveryTask(t, s)
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	if err := InsertTaskCommit(tx, TaskCommit{
		TaskID: "NOPE-1", Repo: "acme/app", SHA: "aaa1", Source: "branch_push", SeenAt: now,
	}); err != nil {
		t.Fatalf("insert task_commit for unknown task = %v, want nil", err)
	}

	// No row should have been recorded for the unknown task.
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM task_commits WHERE task_id = 'NOPE-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("task_commits rows for unknown task = %d, want 0", count)
	}

	// The transaction must still be usable: a later statement succeeds.
	if _, err := AppendMainCommit(tx, "acme/app", "m1", now); err != nil {
		t.Fatalf("transaction unusable after unknown-task insert: %v", err)
	}
}

func TestEnvDeployFrontier(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	seedDeliveryTask(t, s)
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	id1, err := AppendMainCommit(tx, "acme/app", "m1", now)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := AppendMainCommit(tx, "acme/app", "m2", now)
	if err != nil {
		t.Fatal(err)
	}

	// No row yet: frontier nil.
	f, err := ConfirmedFrontier(tx, "acme/app", "dev")
	if err != nil || f != nil {
		t.Fatalf("empty frontier = %v, %v", f, err)
	}

	// GH-only (flux never seen): GH signal alone confirms (bootstrap fallback).
	if err := BumpEnvDeployGH(tx, now, "acme/app", "dev", id2); err != nil {
		t.Fatal(err)
	}
	f, err = ConfirmedFrontier(tx, "acme/app", "dev")
	if err != nil || f == nil || *f != id2 {
		t.Fatalf("gh-only frontier = %v, %v, want %d", f, err, id2)
	}

	// First flux signal latches dual-gating: frontier = min(gh, flux).
	latched, err := BumpEnvDeployFlux(tx, now, "acme/app", "dev", id1)
	if err != nil {
		t.Fatal(err)
	}
	if !latched {
		t.Fatal("first flux bump did not report latching")
	}
	f, err = ConfirmedFrontier(tx, "acme/app", "dev")
	if err != nil || f == nil || *f != id1 {
		t.Fatalf("dual frontier = %v, %v, want min %d", f, err, id1)
	}

	// Watermarks are forward-only. Latching is reported once, on the
	// transition only.
	latched, err = BumpEnvDeployFlux(tx, now, "acme/app", "dev", id2)
	if err != nil {
		t.Fatal(err)
	}
	if latched {
		t.Fatal("second flux bump reported latching again")
	}
	if err := BumpEnvDeployGH(tx, now, "acme/app", "dev", id1); err != nil { // stale, ignored
		t.Fatal(err)
	}
	f, err = ConfirmedFrontier(tx, "acme/app", "dev")
	if err != nil || f == nil || *f != id2 {
		t.Fatalf("forward-only frontier = %v, %v, want %d", f, err, id2)
	}

	// The flux_seen latch is permanent: once tripped, a GH bump alone can
	// never move the frontier past the flux watermark, even when gh advances
	// beyond it. This also exercises the dual-gating path once gh > flux.
	id3, err := AppendMainCommit(tx, "acme/app", "m3", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := BumpEnvDeployGH(tx, now, "acme/app", "dev", id3); err != nil {
		t.Fatal(err)
	}
	f, err = ConfirmedFrontier(tx, "acme/app", "dev")
	if err != nil || f == nil || *f != id2 {
		t.Fatalf("latched frontier after gh advance = %v, %v, want flux watermark %d", f, err, id2)
	}
}

func TestReleaseFrontier(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	seedDeliveryTask(t, s)
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	f, err := ReleaseFrontier(tx, "acme/app")
	if err != nil || f != nil {
		t.Fatalf("empty release frontier = %v, %v", f, err)
	}
	id1, err := AppendMainCommit(tx, "acme/app", "m1", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetReleaseFrontier(tx, "acme/app", "v1.0.0", id1, now); err != nil {
		t.Fatal(err)
	}
	// redelivery: same tag again is a no-op
	if err := SetReleaseFrontier(tx, "acme/app", "v1.0.0", id1, now); err != nil {
		t.Fatal(err)
	}
	f, err = ReleaseFrontier(tx, "acme/app")
	if err != nil || f == nil || *f != id1 {
		t.Fatalf("release frontier = %v, %v, want %d", f, err, id1)
	}
}

// TestSetReleaseFrontierIsForwardOnly: re-cutting a tag onto a newer commit
// advances that tag's row; a stale re-publish never moves it back.
func TestSetReleaseFrontierIsForwardOnly(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	seedDeliveryTask(t, s)
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	id1, err := AppendMainCommit(tx, "acme/app", "m1", now)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := AppendMainCommit(tx, "acme/app", "m2", now)
	if err != nil {
		t.Fatal(err)
	}

	// Distinct publish times so the row's timestamp identifies which cut it
	// describes.
	first := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	second := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	publishedAt := func() time.Time {
		t.Helper()
		var ts time.Time
		if err := tx.QueryRow(
			`SELECT published_at FROM release_frontiers WHERE repo = 'acme/app' AND tag = 'v1.0.0'`,
		).Scan(&ts); err != nil {
			t.Fatal(err)
		}
		return ts.UTC()
	}

	if err := SetReleaseFrontier(tx, "acme/app", "v1.0.0", id1, first); err != nil {
		t.Fatal(err)
	}
	// Tag re-cut onto a newer commit: the row advances.
	if err := SetReleaseFrontier(tx, "acme/app", "v1.0.0", id2, second); err != nil {
		t.Fatal(err)
	}
	f, err := ReleaseFrontier(tx, "acme/app")
	if err != nil || f == nil || *f != id2 {
		t.Fatalf("frontier after re-cut = %v, %v, want %d", f, err, id2)
	}
	if ts := publishedAt(); !ts.Equal(second) {
		t.Fatalf("published_at after re-cut = %s, want %s", ts, second)
	}
	// Stale re-publish of the same tag: neither the commit nor the timestamp
	// may move back — the row must keep describing the newer cut.
	if err := SetReleaseFrontier(tx, "acme/app", "v1.0.0", id1, first); err != nil {
		t.Fatal(err)
	}
	f, err = ReleaseFrontier(tx, "acme/app")
	if err != nil || f == nil || *f != id2 {
		t.Fatalf("frontier after stale re-publish = %v, %v, want %d", f, err, id2)
	}
	if ts := publishedAt(); !ts.Equal(second) {
		t.Fatalf("published_at after stale re-publish = %s, want %s (not backdated)", ts, second)
	}
}

// TestConfirmedFrontierFrom exercises the dual-signal rule directly, over
// every combination of watermarks one env_deploys row can hold. Both callers
// — ConfirmedFrontier's own SELECT and attachDeployFacts' widened one — route
// through this, so the rule cannot drift between them.
func TestConfirmedFrontierFrom(t *testing.T) {
	t.Parallel()
	id := func(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: true} }
	var none sql.NullInt64

	cases := []struct {
		name     string
		gh, flux sql.NullInt64
		fluxSeen bool
		want     *int64
	}{
		{"no signal at all", none, none, false, nil},
		{"github only, before any flux signal", id(7), none, false, ptrTo(int64(7))},
		{"flux only, no github watermark", none, id(7), true, nil},
		{"github missing once flux is correlated", none, id(7), true, nil},
		{"flux behind github", id(9), id(7), true, ptrTo(int64(7))},
		{"github behind flux", id(7), id(9), true, ptrTo(int64(7))},
		{"both agree", id(7), id(7), true, ptrTo(int64(7))},
		// flux_seen without a flux watermark cannot confirm: the pair is
		// past bootstrap, so the GitHub fallback no longer applies.
		{"flux seen but no flux watermark", id(9), none, true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := confirmedFrontierFrom(c.gh, c.flux, c.fluxSeen)
			switch {
			case got == nil && c.want == nil:
			case got == nil || c.want == nil:
				t.Fatalf("frontier = %v, want %v", got, c.want)
			case *got != *c.want:
				t.Fatalf("frontier = %d, want %d", *got, *c.want)
			}
		})
	}
}

func ptrTo[T any](v T) *T { return &v }

func TestNormalizeEnvironment(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"dev": "dev", "test": "dev", "development": "dev", "staging": "dev",
		"prod": "prod", "production": "prod", "Production": "prod",
		"copilot": "", "github-pages": "", "pypi": "", "dev-apply": "",
	}
	for in, want := range cases {
		if got := NormalizeEnvironment(in); got != want {
			t.Errorf("NormalizeEnvironment(%q) = %q, want %q", in, got, want)
		}
	}
}

// taskStateForTest reads a task's state directly.
func taskStateForTest(t *testing.T, tx *sql.Tx, id string) string {
	t.Helper()
	var st string
	if err := tx.QueryRow(`SELECT state FROM tasks WHERE id = $1`, id).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

// deliveryEventID inserts an event row inside tx and returns its id.
// Transition writes a state_log row with a NOT NULL FK to events, so
// resolver tests need a real event id, not a zero placeholder.
func deliveryEventID(t *testing.T, tx *sql.Tx) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRow(
		`INSERT INTO events (source, external_id, type, payload, received_at)
		 VALUES ('github', 'ev-' || md5(random()::text), 'push', '{}'::jsonb, now())
		 RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestResolveDeliveryFullFlow(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE project_repos SET done_state = 'deployed_prod' WHERE repo = 'acme/app'`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ev := deliveryEventID(t, tx)

	// Not landed: no-op.
	if err := ResolveDelivery(tx, now, taskID, "acme/app", ev); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "ready" {
		t.Fatalf("state = %s, want ready", st)
	}

	// Land the work.
	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "c1", Source: "branch_push", SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	mid, err := AppendMainCommit(tx, "acme/app", "c1", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", ev); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "merged" {
		t.Fatalf("state = %s, want merged", st)
	}

	// Dev deploy confirmed (gh only, flux never seen) -> deployed_dev.
	if err := BumpEnvDeployGH(tx, now, "acme/app", "dev", mid); err != nil {
		t.Fatal(err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", ev); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "deployed_dev" {
		t.Fatalf("state = %s, want deployed_dev", st)
	}

	// Prod deploy -> deployed_prod (terminal for done_state=deployed_prod).
	if err := BumpEnvDeployGH(tx, now, "acme/app", "prod", mid); err != nil {
		t.Fatal(err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", ev); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "deployed_prod" {
		t.Fatalf("state = %s, want deployed_prod", st)
	}
	// Idempotent re-resolve.
	if err := ResolveDelivery(tx, now, taskID, "acme/app", ev); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "deployed_prod" {
		t.Fatalf("state after re-resolve = %s, want deployed_prod", st)
	}
}

// TestResolveDeliveryOutOfOrderCatchUp checks that one resolve walks every
// hop the facts support: ready -> merged -> deployed_dev -> deployed_prod,
// each a separate legal transition with its own state_log row.
func TestResolveDeliveryOutOfOrderCatchUp(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ev := deliveryEventID(t, tx)

	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "c1", Source: "pr", SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	mid, err := AppendMainCommit(tx, "acme/app", "c1", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := BumpEnvDeployGH(tx, now, "acme/app", "dev", mid); err != nil {
		t.Fatal(err)
	}
	if err := BumpEnvDeployGH(tx, now, "acme/app", "prod", mid); err != nil {
		t.Fatal(err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", ev); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "deployed_prod" {
		t.Fatalf("state = %s, want deployed_prod", st)
	}

	// Every intermediate hop is recorded, not just the endpoints.
	var hops int
	if err := tx.QueryRow(
		`SELECT count(*) FROM state_log WHERE entity_kind = 'task' AND entity_id = $1
		   AND change->>'field' = 'state'`, taskID).Scan(&hops); err != nil {
		t.Fatal(err)
	}
	if hops != 3 {
		t.Fatalf("state_log hops = %d, want 3 (ready->merged->deployed_dev->deployed_prod)", hops)
	}
}

func TestResolveDeliveryReleased(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE project_repos SET done_state = 'released' WHERE repo = 'acme/app'`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ev := deliveryEventID(t, tx)

	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "c1", Source: "marker", SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	mid, err := AppendMainCommit(tx, "acme/app", "c1", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetReleaseFrontier(tx, "acme/app", "v1", mid, now); err != nil {
		t.Fatal(err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", ev); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "released" {
		t.Fatalf("state = %s, want released", st)
	}
}

func TestResolveDeliveryNeverAdvancesDraft(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `UPDATE tasks SET state = 'draft' WHERE id = $1`, taskID); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ev := deliveryEventID(t, tx)
	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "c1", Source: "marker", SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := AppendMainCommit(tx, "acme/app", "c1", now); err != nil {
		t.Fatal(err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", ev); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "draft" {
		t.Fatalf("state = %s, want draft (resolver must not touch drafts)", st)
	}
}

// TestResolveDeliveryReleaseIgnoredForServiceRepo: with done_state=merged a
// release event must not move the task to released.
func TestResolveDeliveryReleaseIgnoredForServiceRepo(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s) // done_state defaults to 'merged'
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ev := deliveryEventID(t, tx)
	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "c1", Source: "pr", SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	mid, err := AppendMainCommit(tx, "acme/app", "c1", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetReleaseFrontier(tx, "acme/app", "v1", mid, now); err != nil {
		t.Fatal(err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", ev); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "merged" {
		t.Fatalf("state = %s, want merged (released gated on done_state)", st)
	}
}

// TestResolveDeliveryProdIgnoredForReleaseRepo: deployed_prod -> released is
// not a legal transition, so advancing a release-based repo's task on a prod
// deploy would strand it one hop short of its done_state forever. A prod
// deploy on such a repo must not advance the task past deployed_dev.
func TestResolveDeliveryProdIgnoredForReleaseRepo(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE project_repos SET done_state = 'released' WHERE repo = 'acme/app'`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ev := deliveryEventID(t, tx)

	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "c1", Source: "pr", SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	mid, err := AppendMainCommit(tx, "acme/app", "c1", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := BumpEnvDeployGH(tx, now, "acme/app", "dev", mid); err != nil {
		t.Fatal(err)
	}
	if err := BumpEnvDeployGH(tx, now, "acme/app", "prod", mid); err != nil {
		t.Fatal(err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", ev); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "deployed_dev" {
		t.Fatalf("state = %s, want deployed_dev (prod must not strand a release repo)", st)
	}

	// The release still lands the task on its done_state.
	if err := SetReleaseFrontier(tx, "acme/app", "v1", mid, now); err != nil {
		t.Fatal(err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", ev); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "released" {
		t.Fatalf("state = %s, want released", st)
	}
}

// TestResolveDeliveryKeepsActiveLease: landing the work advances the task to
// merged and leaves the lease alone. A lease says a worktree is occupied,
// which is still true after a merge — committing to a branch that is already
// merged (or deployed) is ordinary work. Leases end on release, abandon,
// reopen, or the expiry sweep.
func TestResolveDeliveryKeepsActiveLease(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	ctx := context.Background()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO actors (id, kind, display_name) VALUES ('stig','human','Stig')`)
	mustExec(`UPDATE tasks SET state = 'in_progress' WHERE id = $1`, taskID)
	mustExec(`INSERT INTO leases (task_id, actor_id, worktree, acquired_at, expires_at)
	          VALUES ($1, 'stig', 'host:/wt', now(), now() + interval '1 hour')`, taskID)

	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ev := deliveryEventID(t, tx)

	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "c1", Source: "branch_push", SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := AppendMainCommit(tx, "acme/app", "c1", now); err != nil {
		t.Fatal(err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", ev); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "merged" {
		t.Fatalf("state = %s, want merged", st)
	}
	var active int
	if err := tx.QueryRow(
		`SELECT count(*) FROM leases WHERE task_id = $1 AND released_at IS NULL`,
		taskID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("active leases after landing = %d, want 1 (delivery must not close the lease)", active)
	}
}

// permutations returns every ordering of xs.
func permutations(xs []string) [][]string {
	if len(xs) <= 1 {
		return [][]string{slices.Clone(xs)}
	}
	var out [][]string
	for i := range xs {
		rest := slices.Concat(xs[:i], xs[i+1:])
		for _, p := range permutations(rest) {
			out = append(out, append([]string{xs[i]}, p...))
		}
	}
	return out
}

// TestResolveDeliveryArrivalOrder feeds one identical fact set in every
// arrival order and asserts the same final state each time (design spec,
// "Testing"). Each step is one webhook's worth of facts followed by a
// resolve, exactly as handlers do.
//
// Orders where a deploy precedes the main push are skipped, but they do
// occur: GitHub gives no delivery-ordering guarantee, and the
// last-deploy/<env> push that anchors a deploy SHA is a separate delivery.
// A deploy whose SHA is not yet on main cannot resolve to a main id, and v1
// drops that fact rather than parking it (accepted: deploys follow their
// push by minutes here, and the next deploy of the repo re-records the
// frontier, so the affected tasks catch up then).
func TestResolveDeliveryArrivalOrder(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	now := time.Now()

	deploy := func(env string) func(*sql.Tx) error {
		return func(tx *sql.Tx) error {
			mid, err := MainIDForSHA(tx, "acme/app", "c1")
			if err != nil || mid == nil {
				return err
			}
			return BumpEnvDeployGH(tx, now, "acme/app", env, *mid)
		}
	}
	steps := map[string]func(*sql.Tx) error{
		"land": func(tx *sql.Tx) error {
			_, err := AppendMainCommit(tx, "acme/app", "c1", now)
			return err
		},
		"attribute": func(tx *sql.Tx) error {
			return InsertTaskCommit(tx, TaskCommit{
				TaskID: taskID, Repo: "acme/app", SHA: "c1", Source: "pr", SeenAt: now})
		},
		"dev":  deploy("dev"),
		"prod": deploy("prod"),
	}

	cases := 0
	for _, order := range permutations([]string{"land", "attribute", "dev", "prod"}) {
		land := slices.Index(order, "land")
		if land > slices.Index(order, "dev") || land > slices.Index(order, "prod") {
			continue
		}
		cases++
		t.Run(strings.Join(order, "_"), func(t *testing.T) {
			tx, err := s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback() // restores the seeded ready task for the next order
			ev := deliveryEventID(t, tx)
			for _, name := range order {
				if err := steps[name](tx); err != nil {
					t.Fatalf("step %s: %v", name, err)
				}
				if err := ResolveDelivery(tx, now, taskID, "acme/app", ev); err != nil {
					t.Fatalf("resolve after %s: %v", name, err)
				}
			}
			if st := taskStateForTest(t, tx, taskID); st != "deployed_prod" {
				t.Fatalf("order %v: state = %s, want deployed_prod", order, st)
			}
		})
	}
	if cases != 8 {
		t.Fatalf("ran %d orders, want 8", cases)
	}
}

// TestResolveDeliveryIgnoresUnknownTask pins the tolerance for a task id
// that resolves to no row at all (a stale or mistyped commit-message
// correlation, e.g. push.go's merge-message parsing): ResolveDelivery must
// treat it as a no-op, not an error, per InsertTaskCommit's contract that a
// correlation miss must never fail a delivery.
func TestResolveDeliveryIgnoresUnknownTask(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ev := deliveryEventID(t, tx)
	if err := ResolveDelivery(tx, now, "P1-999", "acme/app", ev); err != nil {
		t.Fatalf("ResolveDelivery: %v", err)
	}
}

func TestRepoDoneState(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	seedDeliveryTask(t, s)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	got, err := RepoDoneState(tx, "acme/app")
	if err != nil || got != "merged" {
		t.Fatalf("mapped repo done_state = %q, %v; want merged", got, err)
	}
	if _, err := tx.Exec(`UPDATE project_repos SET done_state = 'released' WHERE repo = 'acme/app'`); err != nil {
		t.Fatal(err)
	}
	got, err = RepoDoneState(tx, "acme/app")
	if err != nil || got != "released" {
		t.Fatalf("done_state = %q, %v; want released", got, err)
	}
	got, err = RepoDoneState(tx, "acme/unmapped")
	if err != nil || got != "merged" {
		t.Fatalf("unmapped repo done_state = %q, %v; want merged", got, err)
	}
}

func TestTasksBelowFrontier(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	seedDeliveryTask(t, s)
	ctx := context.Background()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// P1-1 lands below the frontier, P1-5 lands exactly on it (the common
	// case: a deploy of precisely the task's commit). P1-2 lands above,
	// P1-3 is already released (not advanceable), P1-4 never lands.
	for _, id := range []string{"P1-2", "P1-3", "P1-4", "P1-5"} {
		mustExec(`INSERT INTO tasks (id, project_id, title, priority, kind, state, created_at, updated_at)
		          VALUES ($1,'p1','t','high','feature','ready', now(), now())`, id)
	}
	mustExec(`UPDATE tasks SET state = 'released' WHERE id = 'P1-3'`)
	mustExec(`UPDATE tasks SET state = 'merged' WHERE id = 'P1-5'`)

	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	for _, tc := range []struct{ task, sha string }{
		{"P1-1", "c1"}, {"P1-2", "c3"}, {"P1-3", "c2"}, {"P1-4", "unlanded"}, {"P1-5", "c2"},
	} {
		if err := InsertTaskCommit(tx, TaskCommit{
			TaskID: tc.task, Repo: "acme/app", SHA: tc.sha, Source: "pr", SeenAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	var frontier int64
	for _, sha := range []string{"c1", "c2", "c3"} {
		id, err := AppendMainCommit(tx, "acme/app", sha, now)
		if err != nil {
			t.Fatal(err)
		}
		if sha == "c2" {
			frontier = id
		}
	}

	got, err := TasksBelowFrontier(tx, "acme/app", frontier)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"P1-1", "P1-5"}) {
		t.Fatalf("TasksBelowFrontier = %v, want [P1-1 P1-5]", got)
	}

	// Other repos are never returned.
	got, err = TasksBelowFrontier(tx, "acme/other", frontier)
	if err != nil || len(got) != 0 {
		t.Fatalf("other repo = %v, %v; want empty", got, err)
	}
}

// TestClearTaskCommitsVoidsDelivery: the commits behind a delivered task are
// still on main after a reopen, so without clearing them the next resolve
// snaps the task straight back to its former delivered state.
func TestClearTaskCommitsVoidsDelivery(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ev := deliveryEventID(t, tx)

	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "c1", Source: "pr", SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	mid, err := AppendMainCommit(tx, "acme/app", "c1", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := BumpEnvDeployGH(tx, now, "acme/app", "prod", mid); err != nil {
		t.Fatal(err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", ev); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "deployed_prod" {
		t.Fatalf("state = %s, want deployed_prod", st)
	}

	// Reopen, as POST /tasks/{id}/reopen does.
	if err := Transition(tx, now, taskID, "deployed_prod", "ready", ev); err != nil {
		t.Fatal(err)
	}
	if err := ClearTaskCommits(tx, taskID); err != nil {
		t.Fatal(err)
	}

	landed, err := LandedMainID(tx, taskID, "acme/app")
	if err != nil || landed != nil {
		t.Fatalf("landed after clear = %v, %v; want nil, nil", landed, err)
	}
	if ids, err := TasksBelowFrontier(tx, "acme/app", mid); err != nil || len(ids) != 0 {
		t.Fatalf("TasksBelowFrontier after clear = %v, %v; want empty", ids, err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", ev); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "ready" {
		t.Fatalf("state after re-resolve = %s, want ready (reopen voids the prior delivery)", st)
	}
}
