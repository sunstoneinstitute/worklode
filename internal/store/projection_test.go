package store

import (
	"database/sql"
	"testing"
	"time"
)

func TestProjectionCheckpointRoundTrip(t *testing.T) {
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
	s := outboxStore(t) // from outbox_test.go: projects alpha and beta
	ctx := t.Context()
	a := makeTask(t, s, "d1", "alpha", "first")
	makeTask(t, s, "d2", "beta", "second")

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

	// The limit bounds the scan, and through only covers what was read.
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

func TestProjectionFailuresRoundTrip(t *testing.T) {
	s := outboxStore(t) // projects alpha and beta
	ctx := t.Context()

	if fails, err := s.ProjectionFailures(ctx); err != nil || len(fails) != 0 {
		t.Fatalf("initial quarantine = %v, %v; want empty", fails, err)
	}

	t0 := time.Now().UTC().Truncate(time.Microsecond) // Postgres timestamptz resolution
	first := ProjectionFailure{
		ProjectID: "alpha", Attempts: 1,
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
	if got := fails[0]; got.ProjectID != "alpha" || got.Attempts != 1 ||
		!got.FirstFailedAt.Equal(t0) || !got.NextAttemptAt.Equal(t0) ||
		got.LastError != "put graph: 500" {
		t.Fatalf("row = %+v; want %+v", got, first)
	}

	// A repeat failure updates the row in place and keeps FirstFailedAt: it
	// is how long the project has been stuck, which Attempts alone omits.
	t1 := t0.Add(time.Minute)
	second := ProjectionFailure{
		ProjectID: "alpha", Attempts: 2,
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
