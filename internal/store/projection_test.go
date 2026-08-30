package store

import (
	"database/sql"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestProjectionCheckpointRoundTrip(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := t.Context()

	cp, err := s.ProjectionCheckpoint(ctx)
	if err != nil || cp != 0 {
		t.Fatalf("initial checkpoint = %d, %v; want 0, nil", cp, err)
	}
	if err := s.SetProjectionCheckpoint(ctx, 42); err != nil {
		t.Fatalf("set: %v", err)
	}
	if cp, err = s.ProjectionCheckpoint(ctx); err != nil || cp != 42 {
		t.Fatalf("checkpoint = %d, %v; want 42, nil", cp, err)
	}
}

func TestDirtyProjects(t *testing.T) {
	t.Parallel()
	s := outboxStore(t) // from outbox_test.go: projects alpha and beta
	ctx := t.Context()
	a := makeTask(t, s, "d1", "alpha", "first")
	makeTask(t, s, "d2", "beta", "second")
	AwaitCommitHorizon(t, s) // the watermark only advances through it

	projects, through, err := s.DirtyProjects(ctx, 0, 100)
	if err != nil {
		t.Fatalf("dirty: %v", err)
	}
	if len(projects) != 2 || projects[0] != "alpha" || projects[1] != "beta" {
		t.Fatalf("projects = %v; want [alpha beta] in first-touched order", projects)
	}
	if through == 0 {
		t.Fatal("through = 0; must advance past the scanned rows")
	}

	// Nothing new after the watermark.
	projects, again, err := s.DirtyProjects(ctx, through, 100)
	if err != nil || len(projects) != 0 || again != through {
		t.Fatalf("after watermark: projects=%v through=%d err=%v; want none, unchanged", projects, again, err)
	}

	// Repeat changes to one project dedupe to one entry.
	for i, move := range [][2]string{{"ready", "in_progress"}, {"in_progress", "ready"}} {
		_, _, err = s.RecordEvent(ctx, "cli", "d-move-"+move[1], "task.transition", nil,
			func(tx *sql.Tx, eventID int64) error {
				return Transition(tx, time.Now().UTC(), a.ID, move[0], move[1], eventID)
			})
		if err != nil {
			t.Fatalf("transition %d: %v", i, err)
		}
	}
	projects, _, err = s.DirtyProjects(ctx, through, 100)
	if err != nil || len(projects) != 1 || projects[0] != "alpha" {
		t.Fatalf("dirty after transitions = %v, %v; want just [alpha]", projects, err)
	}

	// A cross-project edge dirties both projects (Task 1 logs both endpoints).
	c := makeTask(t, s, "d3", "beta", "cross blocker")
	_, edgeFrom, err := s.DirtyProjects(ctx, 0, 1000)
	if err != nil {
		t.Fatalf("watermark before edge: %v", err)
	}
	_, _, err = s.RecordEvent(ctx, "cli", "d-edge", "task.edge_added", nil,
		func(tx *sql.Tx, eventID int64) error {
			return AddEdge(tx, time.Now().UTC(), c.ID, a.ID, "blocks", eventID)
		})
	if err != nil {
		t.Fatalf("add edge: %v", err)
	}
	projects, _, err = s.DirtyProjects(ctx, edgeFrom, 100)
	if err != nil || len(projects) != 2 {
		t.Fatalf("dirty after cross-project edge = %v, %v; want both projects", projects, err)
	}

	// The limit bounds the scan (in transactions), and through only covers
	// what was read.
	projects, part, err := s.DirtyProjects(ctx, 0, 1)
	if err != nil || len(projects) != 1 || projects[0] != "alpha" {
		t.Fatalf("limited scan = %v, %v; want just [alpha]", projects, err)
	}
	if part >= through {
		t.Fatalf("limited through = %d; want < %d (only one row covered)", part, through)
	}

	// Resuming from part must still surface the not-yet-read work (beta):
	// a batch boundary must never silently skip unprojected changes.
	rest, _, err := s.DirtyProjects(ctx, part, 100)
	if err != nil {
		t.Fatalf("resume from part: %v", err)
	}
	found := false
	for _, p := range rest {
		if p == "beta" {
			found = true
		}
	}
	if !found {
		t.Fatalf("resume from part = %v; want beta still present", rest)
	}
}

// TestDirtyProjectsKeepsWatermarkBehindOpenTransaction reproduces WL-119.
// state_log ids are assigned at INSERT time, so a transaction that took the
// lower id can commit after one that took the higher id: tx A inserts row N
// and stays open, tx B inserts row N+1 and commits. A watermark that advanced
// to the highest id it saw would checkpoint at N+1 while beta was projected,
// and row N would never be scanned once A committed — alpha's graph would
// stay stale until its next event. Checkpointing only through the commit
// horizon holds the watermark below both, so alpha is still dirty afterwards.
func TestDirtyProjectsKeepsWatermarkBehindOpenTransaction(t *testing.T) {
	t.Parallel()
	s := outboxStore(t)
	ctx := t.Context()

	AwaitCommitHorizon(t, s)
	_, base, err := s.DirtyProjects(ctx, 0, 100)
	if err != nil {
		t.Fatalf("baseline watermark: %v", err)
	}

	// tx A takes the lower state_log id and stays open.
	txA, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx A: %v", err)
	}
	defer txA.Rollback() //nolint:errcheck // committed below; this is the failure path
	var eventA int64
	if err := txA.QueryRowContext(ctx,
		`INSERT INTO events (source, external_id, type, payload, received_at)
		 VALUES ('cli', 'horizon-alpha', 'task.created', NULL, now()) RETURNING id`).
		Scan(&eventA); err != nil {
		t.Fatalf("insert event in tx A: %v", err)
	}
	if _, err := CreateTask(txA, time.Now().UTC(), TaskInput{
		ProjectID: "alpha", Title: "committed last", Priority: "medium", Kind: "feature",
	}, eventA); err != nil {
		t.Fatalf("create task in tx A: %v", err)
	}

	// tx B commits with the higher state_log id.
	makeTask(t, s, "horizon-beta", "beta", "committed first")

	// beta projects right away — dirtiness is read from what is visible —
	// but the watermark may not pass a transaction that is still open.
	projects, through, err := s.DirtyProjects(ctx, base, 100)
	if err != nil {
		t.Fatalf("dirty with tx A open: %v", err)
	}
	if len(projects) != 1 || projects[0] != "beta" {
		t.Fatalf("dirty with tx A open = %v; want just [beta]", projects)
	}
	if through != base {
		t.Fatalf("watermark moved to %d with tx A still open; want %d — "+
			"alpha's lower state_log id has not committed yet", through, base)
	}

	if err := txA.Commit(); err != nil {
		t.Fatalf("commit tx A: %v", err)
	}

	// The row that committed late is still ahead of the watermark, so alpha
	// is picked up rather than skipped.
	projects, _, err = s.DirtyProjects(ctx, through, 100)
	if err != nil {
		t.Fatalf("dirty after commit: %v", err)
	}
	if len(projects) != 2 || projects[0] != "alpha" || projects[1] != "beta" {
		t.Fatalf("dirty after tx A committed = %v; want [alpha beta] — "+
			"alpha's lower state_log id must not have been skipped", projects)
	}
}

func TestProjectionFailuresRoundTrip(t *testing.T) {
	t.Parallel()
	s := outboxStore(t) // projects alpha and beta
	ctx := t.Context()

	if fails, err := s.ProjectionFailures(ctx); err != nil || len(fails) != 0 {
		t.Fatalf("initial quarantine = %v, %v; want empty", fails, err)
	}

	t0 := time.Now().UTC().Truncate(time.Microsecond) // Postgres timestamptz resolution
	first := model.ProjectionFailure{
		Project: "alpha", Attempts: 1,
		FirstFailedAt: t0, LastFailedAt: t0, NextAttemptAt: t0,
		LastError: "put graph: 500",
	}
	if err := s.RecordProjectionFailure(ctx, first); err != nil {
		t.Fatalf("record: %v", err)
	}
	fails, err := s.ProjectionFailures(ctx)
	if err != nil || len(fails) != 1 {
		t.Fatalf("quarantine = %v, %v; want one row", fails, err)
	}
	if got := fails[0]; got.Project != "alpha" || got.Attempts != 1 ||
		!got.FirstFailedAt.Equal(t0) || !got.NextAttemptAt.Equal(t0) ||
		got.LastError != "put graph: 500" {
		t.Fatalf("row = %+v; want %+v", got, first)
	}

	// A repeat failure updates the row in place and keeps FirstFailedAt: it
	// is how long the project has been stuck, which Attempts alone omits.
	t1 := t0.Add(time.Minute)
	second := model.ProjectionFailure{
		Project: "alpha", Attempts: 2,
		FirstFailedAt: t0, LastFailedAt: t1, NextAttemptAt: t1.Add(time.Minute),
		LastError: "put graph: 400",
	}
	if err := s.RecordProjectionFailure(ctx, second); err != nil {
		t.Fatalf("re-record: %v", err)
	}
	fails, err = s.ProjectionFailures(ctx)
	if err != nil || len(fails) != 1 {
		t.Fatalf("quarantine after repeat = %v, %v; want one row", fails, err)
	}
	if got := fails[0]; got.Attempts != 2 || !got.FirstFailedAt.Equal(t0) ||
		!got.LastFailedAt.Equal(t1) || got.LastError != "put graph: 400" {
		t.Fatalf("row after repeat = %+v; want %+v", got, second)
	}

	// Clearing is idempotent, so the projector can call it unconditionally.
	if err := s.ClearProjectionFailure(ctx, "alpha"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := s.ClearProjectionFailure(ctx, "alpha"); err != nil {
		t.Fatalf("clear a second time: %v", err)
	}
	if fails, err := s.ProjectionFailures(ctx); err != nil || len(fails) != 0 {
		t.Fatalf("quarantine after clear = %v, %v; want empty", fails, err)
	}
}
