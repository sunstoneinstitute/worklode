package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Actor is a human, an autonomous agent, or a service account that can act
// against the store (create tasks, claim leases, etc.). Admin actors may
// additionally manage projects, actors, and tokens.
type Actor struct {
	ID          string
	Kind        string
	DisplayName string
	Admin       bool
}

// tokenPrefix marks plaintext bearer tokens so they are visually
// distinguishable from a raw hex hash (see RevokeToken).
const tokenPrefix = "wt_"

// sha256Hex returns the lowercase hex SHA-256 digest of s.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// CreateActor registers a new actor. admin grants it the right to manage
// projects, actors, and tokens.
func (s *Store) CreateActor(ctx context.Context, id, kind, displayName string, admin bool) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO actors (id, kind, display_name, admin) VALUES (?, ?, ?, ?)`,
		id, kind, displayName, admin,
	)
	if err != nil {
		return fmt.Errorf("insert actor %s: %w", id, err)
	}
	return nil
}

// GetActor looks up an actor by id. Returns ErrNotFound if it does not exist.
func (s *Store) GetActor(ctx context.Context, id string) (*Actor, error) {
	var a Actor
	var displayName sql.NullString
	row := s.db.QueryRowContext(ctx,
		`SELECT id, kind, display_name, admin FROM actors WHERE id = ?`, id)
	if err := row.Scan(&a.ID, &a.Kind, &displayName, &a.Admin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get actor %s: %w", id, err)
	}
	a.DisplayName = displayName.String
	return &a, nil
}

// CreateToken mints a new bearer token for actorID and returns the plaintext
// exactly once ("wt_" + 40 lowercase hex chars, i.e. 20 random bytes). Only
// the SHA-256 hex digest of the plaintext is persisted. A nil expiresAt means
// the token never expires.
func (s *Store) CreateToken(ctx context.Context, actorID, description string, expiresAt *time.Time) (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	plaintext := tokenPrefix + hex.EncodeToString(raw)
	hash := sha256Hex(plaintext)

	var expiresAtStr sql.NullString
	if expiresAt != nil {
		expiresAtStr = sql.NullString{String: expiresAt.UTC().Format(time.RFC3339), Valid: true}
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tokens (token_hash, actor_id, description, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		hash, actorID, description, s.nowFn().Format(time.RFC3339), expiresAtStr,
	)
	if err != nil {
		return "", fmt.Errorf("insert token for actor %s: %w", actorID, err)
	}
	return plaintext, nil
}

// BootstrapAdmin creates the initial "admin" service actor (admin = true)
// with the given plaintext token — but only if the actors table is empty. On a store that
// already has actors it is a no-op, so serve can call it unconditionally at
// startup with the WT_BOOTSTRAP_TOKEN env value.
func (s *Store) BootstrapAdmin(ctx context.Context, plaintextToken string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM actors`).Scan(&n); err != nil {
			return fmt.Errorf("count actors: %w", err)
		}
		if n > 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO actors (id, kind, display_name, admin) VALUES ('admin', 'service', 'bootstrap admin', 1)`,
		); err != nil {
			return fmt.Errorf("insert bootstrap admin: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tokens (token_hash, actor_id, description, created_at)
			 VALUES (?, 'admin', 'bootstrap token', ?)`,
			sha256Hex(plaintextToken), s.nowFn().Format(time.RFC3339),
		); err != nil {
			return fmt.Errorf("insert bootstrap token: %w", err)
		}
		return nil
	})
}

// RevokeToken revokes a token, identified by either its plaintext ("wt_"
// prefix) or its stored hex hash.
func (s *Store) RevokeToken(ctx context.Context, plaintextOrHash string) error {
	hash := tokenHashOf(plaintextOrHash)
	res, err := s.db.ExecContext(ctx,
		`UPDATE tokens SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`,
		s.nowFn().Format(time.RFC3339), hash,
	)
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke token rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// Authenticate looks up the actor for a plaintext bearer token. It returns
// ErrNotFound if the token is unknown, revoked, or expired.
func (s *Store) Authenticate(ctx context.Context, plaintext string) (*Actor, error) {
	hash := tokenHashOf(plaintext)

	var actorID string
	var revokedAt sql.NullString
	var expiresAt sql.NullString
	row := s.db.QueryRowContext(ctx,
		`SELECT actor_id, revoked_at, expires_at FROM tokens WHERE token_hash = ?`, hash)
	if err := row.Scan(&actorID, &revokedAt, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("look up token: %w", err)
	}

	if revokedAt.Valid {
		return nil, ErrNotFound
	}
	if expiresAt.Valid {
		exp, err := time.Parse(time.RFC3339, expiresAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse token expiry: %w", err)
		}
		if exp.Before(s.nowFn()) {
			return nil, ErrNotFound
		}
	}

	return s.GetActor(ctx, actorID)
}

// tokenHashOf accepts either a plaintext token (with the "wt_" prefix) or an
// already-hashed hex digest, and returns the hex hash to look up in tokens.
func tokenHashOf(plaintextOrHash string) string {
	if strings.HasPrefix(plaintextOrHash, tokenPrefix) {
		return sha256Hex(plaintextOrHash)
	}
	return plaintextOrHash
}
