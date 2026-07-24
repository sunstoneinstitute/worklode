package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// UpsertGitHubUserToken stores (or replaces) the opaque encrypted GitHub token
// blob for actorID. The store never inspects the ciphertext; encryption is the
// caller's responsibility.
func (s *Store) UpsertGitHubUserToken(ctx context.Context, actorID string, ciphertext []byte) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO github_user_tokens (actor_id, ciphertext, updated_at) VALUES ($1, $2, $3)
		 ON CONFLICT (actor_id) DO UPDATE SET ciphertext = excluded.ciphertext, updated_at = excluded.updated_at`,
		actorID, ciphertext, s.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("upsert github token for %s: %w", actorID, err)
	}
	return nil
}

// GetGitHubUserToken returns the stored ciphertext for actorID, or ErrNotFound.
func (s *Store) GetGitHubUserToken(ctx context.Context, actorID string) ([]byte, error) {
	var ct []byte
	row := s.db.QueryRowContext(ctx,
		`SELECT ciphertext FROM github_user_tokens WHERE actor_id = $1`, actorID)
	if err := row.Scan(&ct); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get github token for %s: %w", actorID, err)
	}
	return ct, nil
}
