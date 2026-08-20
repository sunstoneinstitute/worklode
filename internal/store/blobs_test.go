package store

import (
	"context"
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
