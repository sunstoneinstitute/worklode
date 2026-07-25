package store

import (
	"context"
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

func TestLandedMainIDNoCommits(t *testing.T) {
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
	if err := BumpEnvDeployFlux(tx, now, "acme/app", "dev", id1); err != nil {
		t.Fatal(err)
	}
	f, err = ConfirmedFrontier(tx, "acme/app", "dev")
	if err != nil || f == nil || *f != id1 {
		t.Fatalf("dual frontier = %v, %v, want min %d", f, err, id1)
	}

	// Watermarks are forward-only.
	if err := BumpEnvDeployFlux(tx, now, "acme/app", "dev", id2); err != nil {
		t.Fatal(err)
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

func TestNormalizeEnvironment(t *testing.T) {
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
