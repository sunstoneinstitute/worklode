package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
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
	// ExpectedGitHubLogin is the GitHub login Keycloak asserts for this actor
	// via the realm's github_username user attribute (spec 001 §9.2), re-synced
	// on every login. Empty when the Keycloak account carries no such
	// attribute.
	ExpectedGitHubLogin string
}

// tokenPrefix marks plaintext bearer tokens so they are visually
// distinguishable from a raw hex hash (see RevokeToken).
const tokenPrefix = "wl_"

// sha256Hex returns the lowercase hex SHA-256 digest of s.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// CreateActor registers a new actor. admin grants it the right to manage
// projects, actors, and tokens.
func (s *Store) CreateActor(ctx context.Context, id, kind, displayName string, admin bool) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO actors (id, kind, display_name, admin) VALUES ($1, $2, $3, $4)`,
		id, kind, displayName, admin,
	)
	if err != nil {
		return fmt.Errorf("insert actor %s: %w", id, err)
	}
	return nil
}

// UpsertHumanActor inserts a human actor, or on repeat login updates its
// display name, admin flag, and expected GitHub login. Admin and
// expectedGitHubLogin are both re-synced on every login, so a Keycloak
// demotion or a cleared github_username attribute takes effect the next time
// the user logs in. expectedGitHubLogin is stored as SQL NULL when empty.
// Kind is set to 'human' on insert and left unchanged on update.
func (s *Store) UpsertHumanActor(ctx context.Context, id, displayName string, admin bool, expectedGitHubLogin string) error {
	var ghLogin sql.NullString
	if expectedGitHubLogin != "" {
		ghLogin = sql.NullString{String: expectedGitHubLogin, Valid: true}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO actors (id, kind, display_name, admin, expected_github_login) VALUES ($1, 'human', $2, $3, $4)
		 ON CONFLICT (id) DO UPDATE SET display_name = excluded.display_name, admin = excluded.admin, expected_github_login = excluded.expected_github_login`,
		id, displayName, admin, ghLogin,
	)
	if err != nil {
		return fmt.Errorf("upsert human actor %s: %w", id, err)
	}
	return nil
}

// GetActor looks up an actor by id. Returns ErrNotFound if it does not exist.
func (s *Store) GetActor(ctx context.Context, id string) (*Actor, error) {
	var a Actor
	var displayName sql.NullString
	var ghLogin sql.NullString
	row := s.db.QueryRowContext(ctx,
		`SELECT id, kind, display_name, admin, expected_github_login FROM actors WHERE id = $1`, id)
	if err := row.Scan(&a.ID, &a.Kind, &displayName, &a.Admin, &ghLogin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get actor %s: %w", id, err)
	}
	a.DisplayName = displayName.String
	a.ExpectedGitHubLogin = ghLogin.String
	return &a, nil
}

// CreateToken mints a new bearer token for actorID and returns the plaintext
// exactly once ("wl_" + 40 lowercase hex chars, i.e. 20 random bytes). Only
// the SHA-256 hex digest of the plaintext is persisted. A nil expiresAt means
// the token never expires.
func (s *Store) CreateToken(ctx context.Context, actorID, description string, expiresAt *time.Time) (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	plaintext := tokenPrefix + hex.EncodeToString(raw)
	hash := sha256Hex(plaintext)

	var expiresAtArg sql.NullTime
	if expiresAt != nil {
		expiresAtArg = sql.NullTime{Time: expiresAt.UTC(), Valid: true}
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tokens (token_hash, actor_id, description, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		hash, actorID, description, s.nowFn().UTC(), expiresAtArg,
	)
	if err != nil {
		return "", fmt.Errorf("insert token for actor %s: %w", actorID, err)
	}
	return plaintext, nil
}

// bootstrapTokenRe is the required shape of a bootstrap token: the same
// "wl_" + 40 lowercase hex form CreateToken mints. Anything else (e.g. a
// missing prefix) would be hashed differently by tokenHashOf and silently
// fail every later request with 401.
var bootstrapTokenRe = regexp.MustCompile(`^wl_[0-9a-f]{40}$`)

// BootstrapAdmin creates the initial "admin" service actor (admin = true)
// with the given plaintext token — but only if the actors table is empty. On
// a store that already has actors it is a no-op, so serve can call it
// unconditionally at startup with the LODE_BOOTSTRAP_TOKEN env value. A token
// not matching bootstrapTokenRe is an error even on the no-op path: fail at
// startup, not with silent 401s later.
func (s *Store) BootstrapAdmin(ctx context.Context, plaintextToken string) error {
	if !bootstrapTokenRe.MatchString(plaintextToken) {
		return fmt.Errorf("bootstrap token must match %s (e.g. wl_$(openssl rand -hex 20)): %w",
			bootstrapTokenRe, ErrInvalidInput)
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM actors`).Scan(&n); err != nil {
			return fmt.Errorf("count actors: %w", err)
		}
		if n > 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO actors (id, kind, display_name, admin) VALUES ('admin', 'service', 'bootstrap admin', true)`,
		); err != nil {
			return fmt.Errorf("insert bootstrap admin: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tokens (token_hash, actor_id, description, created_at)
			 VALUES ($1, 'admin', 'bootstrap token', $2)`,
			sha256Hex(plaintextToken), s.nowFn().UTC(),
		); err != nil {
			return fmt.Errorf("insert bootstrap token: %w", err)
		}
		return nil
	})
}

// RevokeToken revokes a token, identified by either its plaintext ("wl_"
// prefix) or its stored hex hash.
func (s *Store) RevokeToken(ctx context.Context, plaintextOrHash string) error {
	hash := tokenHashOf(plaintextOrHash)
	res, err := s.db.ExecContext(ctx,
		`UPDATE tokens SET revoked_at = $1 WHERE token_hash = $2 AND revoked_at IS NULL`,
		s.nowFn().UTC(), hash,
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
	var revokedAt sql.NullTime
	var expiresAt sql.NullTime
	row := s.db.QueryRowContext(ctx,
		`SELECT actor_id, revoked_at, expires_at FROM tokens WHERE token_hash = $1`, hash)
	if err := row.Scan(&actorID, &revokedAt, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("look up token: %w", err)
	}

	if revokedAt.Valid {
		return nil, ErrNotFound
	}
	if expiresAt.Valid && expiresAt.Time.Before(s.nowFn()) {
		return nil, ErrNotFound
	}

	return s.GetActor(ctx, actorID)
}

// tokenHashOf accepts either a plaintext token (with the "wl_" prefix) or an
// already-hashed hex digest, and returns the hex hash to look up in tokens.
func tokenHashOf(plaintextOrHash string) string {
	if strings.HasPrefix(plaintextOrHash, tokenPrefix) {
		return sha256Hex(plaintextOrHash)
	}
	return plaintextOrHash
}
