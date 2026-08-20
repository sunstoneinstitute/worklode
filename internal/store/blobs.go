package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// InsertBlob records a blob, returning the existing row unchanged if the
// hash is already known. Idempotent by construction: identical bytes hash
// identically, so a re-upload must not create a second row or restamp the
// first -- created_at is the orphan sweep's grace-period clock.
func (s *Store) InsertBlob(ctx context.Context, hash, mediaType string, size int64) (model.Blob, error) {
	var b model.Blob
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO blobs (hash, media_type, size, created_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (hash) DO UPDATE SET hash = EXCLUDED.hash
		 RETURNING hash, media_type, size, created_at`,
		hash, mediaType, size, s.nowFn().UTC(),
	).Scan(&b.Hash, &b.MediaType, &b.Size, &b.CreatedAt)
	if err != nil {
		return model.Blob{}, fmt.Errorf("insert blob: %w", err)
	}
	return b, nil
}

// GetBlob returns one blob by hash, or ErrNotFound.
func (s *Store) GetBlob(ctx context.Context, hash string) (model.Blob, error) {
	var b model.Blob
	err := s.db.QueryRowContext(ctx,
		`SELECT hash, media_type, size, created_at FROM blobs WHERE hash = $1`, hash,
	).Scan(&b.Hash, &b.MediaType, &b.Size, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Blob{}, ErrNotFound
	}
	if err != nil {
		return model.Blob{}, fmt.Errorf("get blob: %w", err)
	}
	return b, nil
}
