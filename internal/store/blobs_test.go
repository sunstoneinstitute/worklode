package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// TestBlobsSchema asserts migration 0032 created both tables with the
// constraints spec 021 §1 relies on: the CHECK that a task_blobs row must be
// referenced somehow, and RESTRICT on the blobs foreign key.
func TestBlobsSchema(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	// blobs_hash_format requires 64 lowercase hex chars; a short literal
	// like "aa" would violate the migration's own CHECK before the
	// assertions below get a chance to run.
	hash := "aa" + strings.Repeat("b", 62)

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO blobs (hash, media_type, size, created_at)
		 VALUES ($1, 'image/png', 1, now())`, hash); err != nil {
		t.Fatalf("insert blob: %v", err)
	}

	if err := seedTask(t, s, "WL-1"); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	// Neither embedded nor attached violates task_blobs_referenced.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO task_blobs (task_id, hash, filename, embedded, attached, created_at)
		 VALUES ('WL-1', $1, 'x.png', false, false, now())`, hash)
	if err == nil {
		t.Fatal("expected task_blobs_referenced CHECK to reject an unreferenced row")
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO task_blobs (task_id, hash, filename, embedded, attached, created_at)
		 VALUES ('WL-1', $1, 'x.png', true, false, now())`, hash); err != nil {
		t.Fatalf("insert task_blob: %v", err)
	}

	// RESTRICT: a referenced blob cannot be deleted.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blobs WHERE hash = $1`, hash); err == nil {
		t.Fatal("expected ON DELETE RESTRICT to block deleting a referenced blob")
	}

	// Deleting the task cascades its reference, freeing the blob.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = 'WL-1'`); err != nil {
		t.Fatalf("delete task: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blobs WHERE hash = $1`, hash); err != nil {
		t.Fatalf("delete blob after cascade: %v", err)
	}
}

// seedTask inserts a minimal project + task pair directly, bypassing the
// event machinery these schema assertions do not need.
func seedTask(t *testing.T, s *Store, id string) error {
	t.Helper()
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (id, name, key)
		 VALUES ('p', 'P', 'PP') ON CONFLICT DO NOTHING`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tasks (id, project_id, title, priority, kind, state, created_at, updated_at)
		 VALUES ($1, 'p', 'T', 'medium', 'feature', 'ready', now(), now())`, id)
	return err
}

// TestInsertBlobIdempotent asserts a second insert of the same hash returns
// the existing row rather than erroring or duplicating -- the dedup the
// upload handler relies on.
func TestInsertBlobIdempotent(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	hash := "ab" + strings.Repeat("c", 62)

	b1, err := s.InsertBlob(ctx, hash, "image/png", 1234)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if b1.Hash != hash || b1.MediaType != "image/png" || b1.Size != 1234 {
		t.Fatalf("unexpected blob: %+v", b1)
	}

	b2, err := s.InsertBlob(ctx, hash, "image/png", 1234)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if !b2.CreatedAt.Equal(b1.CreatedAt) {
		t.Fatal("second insert replaced the row; want the original returned")
	}

	got, err := s.GetBlob(ctx, hash)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Size != 1234 {
		t.Fatalf("GetBlob size = %d, want 1234", got.Size)
	}

	if _, err := s.GetBlob(ctx, strings.Repeat("f", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetBlob(missing) error = %v, want ErrNotFound", err)
	}
}

// TestReconcileEmbedded asserts the derived half of the reference graph:
// embedded tracks the body exactly, and a row that ends up neither embedded
// nor attached is deleted rather than left to violate the CHECK.
func TestReconcileEmbedded(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := seedTask(t, s, "WL-1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// created_by references actors(id); ReconcileEmbedded and AttachBlob
	// enforce that FK, so the actor must exist before either is called.
	if err := s.CreateActor(ctx, "alice", "human", "Alice", false); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	h1 := "a1" + strings.Repeat("0", 62)
	h2 := "b2" + strings.Repeat("0", 62)
	for _, h := range []string{h1, h2} {
		if _, err := s.InsertBlob(ctx, h, "image/png", 1); err != nil {
			t.Fatalf("insert blob: %v", err)
		}
	}

	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return ReconcileEmbedded(tx, s.Now(), "WL-1", []string{h1, h2}, "alice")
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := mustListHashes(t, s, "WL-1"); len(got) != 2 {
		t.Fatalf("after first reconcile: %v, want 2", got)
	}

	// Drop h2 from the body: its row must go.
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return ReconcileEmbedded(tx, s.Now(), "WL-1", []string{h1}, "alice")
	}); err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	got := mustListHashes(t, s, "WL-1")
	if len(got) != 1 || got[0] != h1 {
		t.Fatalf("after second reconcile: %v, want [%s]", got, h1)
	}
}

// TestAttachSurvivesBodyEdit is the declared half: an attached blob is not
// in the body, so reconciliation must not touch it.
func TestAttachSurvivesBodyEdit(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := seedTask(t, s, "WL-2"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// created_by references actors(id); ReconcileEmbedded and AttachBlob
	// enforce that FK, so the actor must exist before either is called.
	if err := s.CreateActor(ctx, "alice", "human", "Alice", false); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	h := "cc" + strings.Repeat("0", 62)
	if _, err := s.InsertBlob(ctx, h, "text/plain", 5); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.AttachBlob(ctx, "WL-2", h, "crash.log", "alice"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return ReconcileEmbedded(tx, s.Now(), "WL-2", nil, "alice")
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	refs, err := s.ListTaskBlobs(ctx, "WL-2")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(refs) != 1 || refs[0].Embedded || !refs[0].Attached {
		t.Fatalf("refs = %+v, want one attached, non-embedded row", refs)
	}
	if refs[0].Filename != "crash.log" || refs[0].MediaType != "text/plain" {
		t.Fatalf("refs[0] = %+v, want filename and media type joined in", refs[0])
	}

	if err := s.DetachBlob(ctx, "WL-2", h); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if refs, _ := s.ListTaskBlobs(ctx, "WL-2"); len(refs) != 0 {
		t.Fatalf("after detach: %+v, want none", refs)
	}
}

func mustListHashes(t *testing.T, s *Store, taskID string) []string {
	t.Helper()
	refs, err := s.ListTaskBlobs(context.Background(), taskID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var out []string
	for _, r := range refs {
		out = append(out, r.Hash)
	}
	return out
}
