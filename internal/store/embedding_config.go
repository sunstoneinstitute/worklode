package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// EmbeddingProviderID returns the embedding space the stored skill vectors
// belong to, or "" when none has been recorded yet. An absent row is the
// normal first-boot state, not an error.
func (s *Store) EmbeddingProviderID(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT provider_id FROM embedding_config WHERE singleton`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get embedding provider id: %w", err)
	}
	return id, nil
}

// SetEmbeddingProviderID records the embedding space the stored skill vectors
// belong to. Callers must have made the vectors match it first.
func (s *Store) SetEmbeddingProviderID(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO embedding_config (singleton, provider_id) VALUES (true, $1)
		ON CONFLICT (singleton) DO UPDATE SET provider_id = excluded.provider_id`, id); err != nil {
		return fmt.Errorf("set embedding provider id %s: %w", id, err)
	}
	return nil
}
