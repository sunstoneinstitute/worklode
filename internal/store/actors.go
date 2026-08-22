package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Actor is a human, an autonomous agent, or a service account that can act
// against the store (create tasks, claim leases, etc.). Admin actors may
// additionally manage projects, actors, and tokens.
//
// Actor is deliberately not model.Actor: ExpectedGitHubLogin, Email, and
// Groups are auth bookkeeping fields this package needs internally (matching
// a Keycloak login) that never cross the wire, so they stay outside the four
// fields model.Actor declares (ADR 036 §3, "store scan plumbing").
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
	// Email is the Keycloak email claim, re-synced on every login (spec 029
	// §6.2). Empty when the account carries no email or has never logged in
	// since migration 0035.
	Email string
	// Groups is the raw groups claim, stored in full (not filtered to
	// user/admin) and re-synced on every login (spec 029 §6.2). Nil when the
	// actor has never logged in since migration 0035.
	Groups []string
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

// EnsureServiceActor creates a service actor if absent. Idempotent, so a
// process that owns a service identity (the doc-lifecycle watcher, which is
// tasks.created_by on everything it mints) can assert it at every boot.
// Unlike UpsertHumanActor it never updates: a service identity has no
// external source of truth to re-sync from.
func (s *Store) EnsureServiceActor(ctx context.Context, id, displayName string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO actors (id, kind, display_name, admin) VALUES ($1, 'service', $2, false)
		 ON CONFLICT (id) DO NOTHING`,
		id, displayName,
	)
	if err != nil {
		return fmt.Errorf("ensure service actor %s: %w", id, err)
	}
	return nil
}

// UpsertHumanActor inserts a human actor, or on repeat login updates its
// display name, admin flag, expected GitHub login, email, and groups. All of
// these are re-synced on every login — Keycloak stays the sole authority
// (spec 029 §6.2) — so a Keycloak demotion, a cleared github_username
// attribute, or a narrower groups claim takes effect the next time the user
// logs in. expectedGitHubLogin and email are stored as SQL NULL when empty;
// groups is stored as jsonb, with nil marshalled as `[]` (matching
// SetProjectFocus's handling of projects.focus). Kind is set to 'human' on
// insert and left unchanged on update.
func (s *Store) UpsertHumanActor(ctx context.Context, id, displayName string, admin bool, expectedGitHubLogin, email string, groups []string) error {
	var ghLogin sql.NullString
	if expectedGitHubLogin != "" {
		ghLogin = sql.NullString{String: expectedGitHubLogin, Valid: true}
	}
	var emailArg sql.NullString
	if email != "" {
		emailArg = sql.NullString{String: email, Valid: true}
	}
	if groups == nil {
		groups = []string{}
	}
	groupsJSON, err := json.Marshal(groups)
	if err != nil {
		return fmt.Errorf("marshal groups for actor %s: %w", id, err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO actors (id, kind, display_name, admin, expected_github_login, email, groups) VALUES ($1, 'human', $2, $3, $4, $5, $6)
		 ON CONFLICT (id) DO UPDATE SET display_name = excluded.display_name, admin = excluded.admin, expected_github_login = excluded.expected_github_login, email = excluded.email, groups = excluded.groups`,
		id, displayName, admin, ghLogin, emailArg, groupsJSON,
	)
	if err != nil {
		return fmt.Errorf("upsert human actor %s: %w", id, err)
	}
	return nil
}

// GetActor looks up an actor by id. Returns ErrNotFound if it does not exist.
func (s *Store) GetActor(ctx context.Context, id string) (*Actor, error) {
	a, err := scanActor(s.db.QueryRowContext(ctx,
		`SELECT `+actorColumns+` FROM actors WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get actor %s: %w", id, err)
	}
	return a, nil
}

// actorColumns is the SELECT list scanActor expects, in order.
const actorColumns = `id, kind, display_name, admin, expected_github_login, email, groups`

// actorColumnsA is actorColumns under the `a` alias, for Authenticate's join.
var actorColumnsA = qualifyColumns(actorColumns, "a")

func scanActor(row rowScanner) (*Actor, error) {
	var a Actor
	var displayName, ghLogin, email sql.NullString
	var groupsRaw []byte
	if err := row.Scan(&a.ID, &a.Kind, &displayName, &a.Admin, &ghLogin, &email, &groupsRaw); err != nil {
		return nil, err
	}
	a.DisplayName = displayName.String
	a.ExpectedGitHubLogin = ghLogin.String
	a.Email = email.String
	groups, err := scanActorGroups(groupsRaw)
	if err != nil {
		return nil, fmt.Errorf("actor %s: %w", a.ID, err)
	}
	a.Groups = groups
	return &a, nil
}

// scanActorGroups unmarshals a jsonb groups column (read as raw bytes) into
// a []string, the same way scanProjectFocus handles projects.focus. An empty
// or null column yields a nil slice.
func scanActorGroups(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var groups []string
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, fmt.Errorf("unmarshal groups: %w", err)
	}
	return groups, nil
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

// CreateTaskToken mints a bearer token bound to one task (001 §2.1,
// WL-306): the same wl_ shape as CreateToken, with tokens.task_id set and a
// required expiry — a task-scoped token that never expires would outlive the
// lease whose lifecycle it exists to follow. The lease-ending paths revoke
// it; Renew extends it.
func (s *Store) CreateTaskToken(ctx context.Context, actorID, taskID, description string, expiresAt time.Time) (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	plaintext := tokenPrefix + hex.EncodeToString(raw)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tokens (token_hash, actor_id, description, created_at, expires_at, task_id)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		sha256Hex(plaintext), actorID, description, s.nowFn().UTC(), expiresAt.UTC(), taskID,
	)
	if err != nil {
		return "", fmt.Errorf("insert task token for %s on %s: %w", actorID, taskID, err)
	}
	return plaintext, nil
}

// EnsureActor creates the actor if it does not exist and is a no-op when it
// does — the idempotent form task-token minting needs for its default agent
// actor (WL-306). It never modifies an existing row.
func (s *Store) EnsureActor(ctx context.Context, id, kind, displayName string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO actors (id, kind, display_name, admin)
		 VALUES ($1, $2, $3, false)
		 ON CONFLICT (id) DO NOTHING`,
		id, kind, displayName)
	if err != nil {
		return fmt.Errorf("ensure actor %s: %w", id, err)
	}
	return nil
}

// revokeTaskTokens revokes every live token bound to taskID, inside the
// caller's transaction — the lease-ending half of 001 §2.1's "expires with
// its lease".
func revokeTaskTokens(tx *sql.Tx, now time.Time, taskID string) error {
	if _, err := tx.Exec(
		`UPDATE tokens SET revoked_at = $1 WHERE task_id = $2 AND revoked_at IS NULL`,
		now.UTC(), taskID); err != nil {
		return fmt.Errorf("revoke task tokens for %s: %w", taskID, err)
	}
	return nil
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
	return requireOneAffected(res, "revoke token", ErrNotFound)
}

// Authenticate looks up the actor for a plaintext bearer token. It returns
// ErrNotFound if the token is unknown, revoked, or expired. taskID is the
// task the token is scoped to (001 §2.1, WL-306), "" for an ordinary
// actor-scoped token.
func (s *Store) Authenticate(ctx context.Context, plaintext string) (a *Actor, taskID string, err error) {
	hash := tokenHashOf(plaintext)

	// One join, not a token read followed by GetActor: this runs on every
	// bearer-token request, and a token whose actor row is missing was
	// ErrNotFound under either shape.
	var revokedAt, expiresAt sql.NullTime
	var boundTask sql.NullString
	a, err = scanActor(appendScan{s.db.QueryRowContext(ctx,
		`SELECT `+actorColumnsA+`, t.revoked_at, t.expires_at, t.task_id
		   FROM tokens t JOIN actors a ON a.id = t.actor_id
		  WHERE t.token_hash = $1`, hash), []any{&revokedAt, &expiresAt, &boundTask}})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("look up token: %w", err)
	}

	if revokedAt.Valid {
		return nil, "", ErrNotFound
	}
	if expiresAt.Valid && expiresAt.Time.Before(s.nowFn()) {
		return nil, "", ErrNotFound
	}
	return a, boundTask.String, nil
}

// tokenHashOf accepts either a plaintext token (with the "wl_" prefix) or an
// already-hashed hex digest, and returns the hex hash to look up in tokens.
func tokenHashOf(plaintextOrHash string) string {
	if strings.HasPrefix(plaintextOrHash, tokenPrefix) {
		return sha256Hex(plaintextOrHash)
	}
	return plaintextOrHash
}
