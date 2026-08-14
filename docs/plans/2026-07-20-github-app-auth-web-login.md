---
status: superseded
covers: docs/specs/001-identity-and-authentication.md
---
# GitHub App Auth — Web Login Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add "Sign in with GitHub" to worklode's web UI as a second identity provider alongside Keycloak, deriving roles from GitHub org/team membership and storing a per-user GitHub token (encrypted) for future "act on behalf of user" calls.

**Architecture:** A new `internal/githubauth` package mirrors `internal/oidc` (authorize-URL construction, code exchange, identity + membership fetch) without touching it. New `githubweb.go` handlers serve namespaced routes (`/auth/github/login`, `/auth/github/callback`) reusing the existing signed-cookie session machinery. GitHub user tokens are encrypted with AES-GCM (`internal/tokencrypt`) and persisted in a new `github_user_tokens` table. Keycloak (`internal/oidc`, `/auth/login`, `/auth/callback`) is left entirely unchanged.

**Tech Stack:** Go, `golang.org/x/oauth2` (+ `oauth2/github` endpoint), `golang-migrate` (embedded SQLite migrations), `crypto/aes`+`crypto/cipher`, `net/http`, `net/http/httptest`.

**Scope:** hzdev web login only. Out of scope (follow-up plans, see end): CLI device flow + OS keychain, deploy/secrets wiring, GitHub App creation, and the first concrete outbound GitHub action (token *refresh* and use).

---

## Source spec

`docs/specs/001-identity-and-authentication.md` — Sections A, B, D, E.

## File Structure

| File | Responsibility | Create/Modify |
|---|---|---|
| `internal/store/migrations/0003_github_user_tokens.up.sql` | Table for encrypted per-actor GitHub tokens | Create |
| `internal/store/migrations/0003_github_user_tokens.down.sql` | Reverse the table | Create |
| `internal/store/github_tokens.go` | Store methods to upsert/get the opaque ciphertext blob | Create |
| `internal/store/github_tokens_test.go` | Round-trip persistence tests | Create |
| `internal/tokencrypt/tokencrypt.go` | AES-GCM seal/open of a byte slice under a 32-byte key | Create |
| `internal/tokencrypt/tokencrypt_test.go` | Encrypt/decrypt round-trip + tamper/short-key tests | Create |
| `internal/githubauth/githubauth.go` | GitHub OAuth config, code exchange, identity + org/team membership → roles | Create |
| `internal/githubauth/githubauth_test.go` | Unit tests against an httptest GitHub | Create |
| `internal/api/githubweb.go` | `/auth/github/login` + `/auth/github/callback` handlers, `provisionGitHubActor` | Create |
| `internal/api/githubweb_test.go` | Handler tests with a fake GitHub | Create |
| `internal/api/server.go` | Config fields, `server.gh` field, `NewServer` wiring, route registration | Modify |
| `internal/cmd/serve.go` | Read new `WL_GITHUB_*` / `WL_TOKEN_ENC_KEY` env vars into Config | Modify |

Design notes locked here:
- **Encryption boundary:** the store persists an **opaque ciphertext blob**; the api layer holds `WL_TOKEN_ENC_KEY` and does all encrypt/decrypt via `internal/tokencrypt`. The store never sees plaintext or the key.
- **Actor id namespacing:** GitHub actors are keyed `github:<numeric-id>` so they cannot collide with Keycloak actors keyed on `preferred_username`.
- **Provider independence:** GitHub config is gated on `WL_GITHUB_APP_CLIENT_ID` + `WL_GITHUB_APP_CLIENT_SECRET` being set, exactly as OIDC is gated on its two vars. Either, both, or neither provider may be enabled.

---

## Phase 1 — Encrypted token storage

### Task 1: Migration for `github_user_tokens`

**Files:**
- Create: `internal/store/migrations/0003_github_user_tokens.up.sql`
- Create: `internal/store/migrations/0003_github_user_tokens.down.sql`

- [ ] **Step 1: Write the up migration**

`internal/store/migrations/0003_github_user_tokens.up.sql`:

```sql
CREATE TABLE github_user_tokens (
    actor_id   TEXT PRIMARY KEY REFERENCES actors (id) ON DELETE CASCADE,
    ciphertext BLOB NOT NULL,
    updated_at TEXT NOT NULL
);
```

- [ ] **Step 2: Write the down migration**

`internal/store/migrations/0003_github_user_tokens.down.sql`:

```sql
DROP TABLE github_user_tokens;
```

- [ ] **Step 3: Verify migrations apply on a fresh DB**

Run: `go test -run TestMigrate -count=1 ./internal/store/`
Expected: PASS (existing migration test opens a DB and calls `Migrate()`; if there is no such test, this step is a no-op and Task 3's test exercises the schema instead).

- [ ] **Step 4: Commit**

```bash
git add internal/store/migrations/0003_github_user_tokens.up.sql internal/store/migrations/0003_github_user_tokens.down.sql
git commit -m "store: add github_user_tokens migration"
```

### Task 2: `internal/tokencrypt` AES-GCM helper

**Files:**
- Create: `internal/tokencrypt/tokencrypt.go`
- Test: `internal/tokencrypt/tokencrypt_test.go`

- [ ] **Step 1: Write the failing test**

`internal/tokencrypt/tokencrypt_test.go`:

```go
package tokencrypt

import (
	"bytes"
	"testing"
)

func testKey() []byte { return bytes.Repeat([]byte{0x2a}, 32) }

func TestSealOpenRoundTrip(t *testing.T) {
	c, err := New(testKey())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	plaintext := []byte(`{"access":"gho_x","refresh":"ghr_y"}`)
	ct, err := c.Seal(plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(ct, plaintext) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := c.Open(ct)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round trip mismatch: got %q", got)
	}
}

func TestSealIsNondeterministic(t *testing.T) {
	c, _ := New(testKey())
	a, _ := c.Seal([]byte("same"))
	b, _ := c.Seal([]byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext were identical (nonce reuse)")
	}
}

func TestOpenRejectsTamper(t *testing.T) {
	c, _ := New(testKey())
	ct, _ := c.Seal([]byte("secret"))
	ct[len(ct)-1] ^= 0xff
	if _, err := c.Open(ct); err == nil {
		t.Fatal("expected error opening tampered ciphertext")
	}
}

func TestNewRejectsShortKey(t *testing.T) {
	if _, err := New([]byte("too-short")); err == nil {
		t.Fatal("expected error for non-32-byte key")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tokencrypt/`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write the implementation**

`internal/tokencrypt/tokencrypt.go`:

```go
// Package tokencrypt seals and opens small secrets (GitHub user tokens) with
// AES-256-GCM under a single 32-byte key supplied via WL_TOKEN_ENC_KEY. The
// nonce is random per Seal and prepended to the ciphertext.
package tokencrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

// Cipher seals/opens byte slices under a fixed key.
type Cipher struct {
	aead cipher.AEAD
}

// New builds a Cipher from a 32-byte key. Any other length is an error.
func New(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("token encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Seal returns nonce||ciphertext for plaintext.
func (c *Cipher) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Open reverses Seal. It errors on a too-short input or a failed auth tag.
func (c *Cipher) Open(blob []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(blob) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := blob[:ns], blob[ns:]
	pt, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	return pt, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tokencrypt/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tokencrypt/
git commit -m "tokencrypt: AES-GCM seal/open for github tokens"
```

### Task 3: Store methods for the ciphertext blob

**Files:**
- Create: `internal/store/github_tokens.go`
- Test: `internal/store/github_tokens_test.go`

- [ ] **Step 1: Write the failing test**

`internal/store/github_tokens_test.go` (follows the existing store-test setup: open a temp DB and `Migrate()`; match the helper other `_test.go` files in this package use — if they call a shared `newTestStore(t)`, use it instead of the inline open below):

```go
package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "wl.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestGitHubUserTokenRoundTrip(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.UpsertHumanActor(ctx, "github:42", "octocat", false); err != nil {
		t.Fatalf("actor: %v", err)
	}

	if err := st.UpsertGitHubUserToken(ctx, "github:42", []byte("cipher-a")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := st.GetGitHubUserToken(ctx, "github:42")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != "cipher-a" {
		t.Fatalf("got %q", got)
	}

	if err := st.UpsertGitHubUserToken(ctx, "github:42", []byte("cipher-b")); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _ = st.GetGitHubUserToken(ctx, "github:42")
	if string(got) != "cipher-b" {
		t.Fatalf("upsert did not overwrite: got %q", got)
	}
}

func TestGetGitHubUserTokenNotFound(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.GetGitHubUserToken(context.Background(), "github:nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestGitHubUserToken -count=1 ./internal/store/`
Expected: FAIL — `undefined: st.UpsertGitHubUserToken`.

- [ ] **Step 3: Write the implementation**

`internal/store/github_tokens.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// UpsertGitHubUserToken stores (or replaces) the opaque encrypted GitHub token
// blob for actorID. The store never inspects the ciphertext; encryption is the
// caller's responsibility.
func (s *Store) UpsertGitHubUserToken(ctx context.Context, actorID string, ciphertext []byte) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO github_user_tokens (actor_id, ciphertext, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (actor_id) DO UPDATE SET ciphertext = excluded.ciphertext, updated_at = excluded.updated_at`,
		actorID, ciphertext, s.Now().UTC().Format(time.RFC3339),
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
		`SELECT ciphertext FROM github_user_tokens WHERE actor_id = ?`, actorID)
	if err := row.Scan(&ct); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get github token for %s: %w", actorID, err)
	}
	return ct, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestGitHubUserToken -count=1 ./internal/store/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/github_tokens.go internal/store/github_tokens_test.go
git commit -m "store: upsert/get encrypted github user tokens"
```

---

## Phase 2 — `internal/githubauth` package

This package parallels `internal/oidc`: it builds the OAuth config, exchanges codes, and fetches identity + org/team membership. The GitHub API base URL and OAuth endpoint are struct fields so tests can point them at an `httptest` server.

### Task 4: Config, OAuth config, and identity fetch

**Files:**
- Create: `internal/githubauth/githubauth.go`
- Test: `internal/githubauth/githubauth_test.go`

- [ ] **Step 1: Write the failing test**

`internal/githubauth/githubauth_test.go`:

```go
package githubauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

// fakeGitHub serves the identity + membership endpoints this package calls.
func fakeGitHub(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func newTestClient(apiBase string) *Client {
	return &Client{
		ClientID:     "cid",
		ClientSecret: "secret",
		Org:          "sunstoneinstitute",
		AdminTeam:    "worklode-admins",
		APIBase:      apiBase,
		Endpoint:     oauth2.Endpoint{AuthURL: apiBase + "/login/oauth/authorize", TokenURL: apiBase + "/login/oauth/access_token"},
	}
}

func TestAuthCodeURLIncludesState(t *testing.T) {
	c := newTestClient("https://example.test")
	u := c.AuthCodeURL("https://wl/auth/github/callback", "xyz")
	if !strings.Contains(u, "state=xyz") || !strings.Contains(u, "client_id=cid") {
		t.Fatalf("bad authorize url: %s", u)
	}
}

func TestFetchIdentity(t *testing.T) {
	srv := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			if r.Header.Get("Authorization") != "Bearer tok" {
				t.Errorf("missing bearer: %q", r.Header.Get("Authorization"))
			}
			json.NewEncoder(w).Encode(map[string]any{"id": 42, "login": "octocat", "name": "The Octocat"})
			return
		}
		http.NotFound(w, r)
	})
	c := newTestClient(srv.URL)
	id, err := c.FetchIdentity(context.Background(), "tok")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if id.ID != 42 || id.Login != "octocat" || id.Name != "The Octocat" {
		t.Fatalf("bad identity: %+v", id)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/githubauth/`
Expected: FAIL — `undefined: Client`.

- [ ] **Step 3: Write the implementation**

`internal/githubauth/githubauth.go`:

```go
// Package githubauth wraps the GitHub App user-authorization (OAuth) flow for
// worklode's web login: it builds the authorize URL, exchanges the code for
// a user-to-server token, and reads the user's identity plus org/team
// membership. It parallels internal/oidc and never touches it. A Client is
// built only when the GitHub App client id and secret are configured.
package githubauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	githuboauth "golang.org/x/oauth2/github"
)

// Client holds GitHub App OAuth config. APIBase and Endpoint default to the
// public GitHub in New but are overridable in tests.
type Client struct {
	ClientID     string
	ClientSecret string
	Org          string
	AdminTeam    string
	APIBase      string          // e.g. https://api.github.com
	Endpoint     oauth2.Endpoint // authorize/token endpoints
}

// New builds a Client for the public GitHub.
func New(clientID, clientSecret, org, adminTeam string) *Client {
	return &Client{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Org:          org,
		AdminTeam:    adminTeam,
		APIBase:      "https://api.github.com",
		Endpoint:     githuboauth.Endpoint,
	}
}

// oauthConfig builds the oauth2 config for the given redirect URL. No scopes:
// a GitHub App's user-to-server access is governed by the App's permissions,
// not OAuth scopes.
func (c *Client) oauthConfig(redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		Endpoint:     c.Endpoint,
		RedirectURL:  redirectURL,
	}
}

// AuthCodeURL returns the GitHub authorize URL carrying state.
func (c *Client) AuthCodeURL(redirectURL, state string) string {
	return c.oauthConfig(redirectURL).AuthCodeURL(state)
}

// Token is the user-to-server token pair returned by Exchange.
type Token struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
}

// Exchange redeems an authorization code for a user-to-server token.
func (c *Client) Exchange(ctx context.Context, redirectURL, code string) (*Token, error) {
	tok, err := c.oauthConfig(redirectURL).Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("github code exchange: %w", err)
	}
	return &Token{AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, Expiry: tok.Expiry}, nil
}

// Identity is the subset of GET /user worklode consumes.
type Identity struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
}

// get performs an authenticated GET and decodes JSON into out. It returns the
// HTTP status so callers can distinguish 404 (not a member) from real errors.
func (c *Client) get(ctx context.Context, token, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIBase+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("github GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode github %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}

// FetchIdentity reads GET /user with the user-to-server token.
func (c *Client) FetchIdentity(ctx context.Context, token string) (*Identity, error) {
	var id Identity
	code, err := c.get(ctx, token, "/user", &id)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("github GET /user: status %d", code)
	}
	return &id, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/githubauth/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/githubauth/
git commit -m "githubauth: oauth config, exchange, identity fetch"
```

### Task 5: Membership → roles

**Files:**
- Modify: `internal/githubauth/githubauth.go`
- Test: `internal/githubauth/githubauth_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/githubauth/githubauth_test.go`:

```go
func membershipHandler(t *testing.T, orgState, teamStatus string, teamState string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/user/memberships/orgs/sunstoneinstitute":
			if orgState == "" {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"state": orgState})
		case r.URL.Path == "/orgs/sunstoneinstitute/teams/worklode-admins/memberships/octocat":
			if teamStatus == "404" {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"state": teamState})
		default:
			http.NotFound(w, r)
		}
	}
}

func TestRolesMember(t *testing.T) {
	srv := fakeGitHub(t, membershipHandler(t, "active", "200", "active"))
	c := newTestClient(srv.URL)
	roles, err := c.Roles(context.Background(), "tok", "octocat")
	if err != nil {
		t.Fatalf("roles: %v", err)
	}
	if !roles.User || !roles.Admin {
		t.Fatalf("want user+admin, got %+v", roles)
	}
}

func TestRolesMemberNotAdmin(t *testing.T) {
	srv := fakeGitHub(t, membershipHandler(t, "active", "404", ""))
	c := newTestClient(srv.URL)
	roles, _ := c.Roles(context.Background(), "tok", "octocat")
	if !roles.User || roles.Admin {
		t.Fatalf("want user, not admin, got %+v", roles)
	}
}

func TestRolesNonMember(t *testing.T) {
	srv := fakeGitHub(t, membershipHandler(t, "", "404", ""))
	c := newTestClient(srv.URL)
	roles, _ := c.Roles(context.Background(), "tok", "octocat")
	if roles.User {
		t.Fatalf("non-member must not have user role, got %+v", roles)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestRoles ./internal/githubauth/`
Expected: FAIL — `undefined: c.Roles`.

- [ ] **Step 3: Write the implementation**

Append to `internal/githubauth/githubauth.go`:

```go
// Roles is the authorization derived from GitHub membership.
type Roles struct {
	User  bool // active member of Org
	Admin bool // active member of AdminTeam
}

type membershipResp struct {
	State string `json:"state"`
}

// activeMembership returns true when the endpoint returns 200 with state
// "active". A 404 means "not a member" and yields false, nil.
func (c *Client) activeMembership(ctx context.Context, token, path string) (bool, error) {
	var m membershipResp
	code, err := c.get(ctx, token, path, &m)
	if err != nil {
		return false, err
	}
	switch code {
	case http.StatusOK:
		return m.State == "active", nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("github GET %s: status %d", path, code)
	}
}

// Roles evaluates org membership (→ User) and admin-team membership (→ Admin)
// for login, using the user-to-server token.
func (c *Client) Roles(ctx context.Context, token, login string) (Roles, error) {
	user, err := c.activeMembership(ctx, token, "/user/memberships/orgs/"+c.Org)
	if err != nil {
		return Roles{}, err
	}
	if !user {
		return Roles{}, nil
	}
	admin, err := c.activeMembership(ctx, token,
		fmt.Sprintf("/orgs/%s/teams/%s/memberships/%s", c.Org, c.AdminTeam, login))
	if err != nil {
		return Roles{}, err
	}
	return Roles{User: true, Admin: admin}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/githubauth/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/githubauth/
git commit -m "githubauth: derive user/admin roles from org+team membership"
```

---

## Phase 3 — API wiring: config, provisioning, routes

### Task 6: Config fields + server wiring

**Files:**
- Modify: `internal/api/server.go`
- Modify: `internal/cmd/serve.go`

- [ ] **Step 1: Add Config fields**

In `internal/api/server.go`, extend the `Config` struct (after the OIDC block):

```go
	// GitHub App auth. Enabled only when GitHubClientID and GitHubClientSecret
	// are both set; independent of the OIDC feature. PublicURL and
	// SessionSecret (above) are shared and required when this is enabled.
	GitHubClientID     string // WL_GITHUB_APP_CLIENT_ID
	GitHubClientSecret string // WL_GITHUB_APP_CLIENT_SECRET
	GitHubOrg          string // WL_GITHUB_ORG
	GitHubAdminTeam    string // WL_GITHUB_ADMIN_TEAM
	TokenEncKey        string // WL_TOKEN_ENC_KEY (hex-encoded 32 bytes)
```

- [ ] **Step 2: Add server fields**

Add to the `server` struct (after the `oidc` field):

```go
	// gh and tokenCipher are nil unless GitHub App auth is configured. All
	// /auth/github/* routes 404 when gh is nil.
	gh          *githubauth.Client
	tokenCipher *tokencrypt.Cipher
```

- [ ] **Step 3: Wire in NewServer**

In `internal/api/server.go`, add the imports `"encoding/hex"` (already present), `"github.com/sunstoneinstitute/worklode/internal/githubauth"`, and `"github.com/sunstoneinstitute/worklode/internal/tokencrypt"`. After the OIDC `if` block in `NewServer`, add:

```go
	if cfg.GitHubClientID != "" && cfg.GitHubClientSecret != "" {
		if cfg.SessionSecret == "" {
			return nil, fmt.Errorf("WL_SESSION_SECRET is required when GitHub auth is enabled")
		}
		if cfg.PublicURL == "" {
			return nil, fmt.Errorf("WL_PUBLIC_URL is required when GitHub auth is enabled")
		}
		if cfg.GitHubOrg == "" {
			return nil, fmt.Errorf("WL_GITHUB_ORG is required when GitHub auth is enabled")
		}
		key, err := hex.DecodeString(cfg.TokenEncKey)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("WL_TOKEN_ENC_KEY must be 64 hex chars (32 bytes)")
		}
		tc, err := tokencrypt.New(key)
		if err != nil {
			return nil, fmt.Errorf("configure token cipher: %w", err)
		}
		s.gh = githubauth.New(cfg.GitHubClientID, cfg.GitHubClientSecret, cfg.GitHubOrg, cfg.GitHubAdminTeam)
		s.tokenCipher = tc
	}
```

- [ ] **Step 4: Register routes**

In `NewServer`, next to the existing `/auth/login` + `/auth/callback` registrations, add:

```go
	mux.HandleFunc("GET /auth/github/login", s.githubLogin)
	mux.HandleFunc("GET /auth/github/callback", s.githubCallback)
```

- [ ] **Step 5: Read env in serve.go**

In `internal/cmd/serve.go`, extend the `api.Config{...}` literal:

```go
				GitHubClientID:     os.Getenv("WL_GITHUB_APP_CLIENT_ID"),
				GitHubClientSecret: os.Getenv("WL_GITHUB_APP_CLIENT_SECRET"),
				GitHubOrg:          os.Getenv("WL_GITHUB_ORG"),
				GitHubAdminTeam:    os.Getenv("WL_GITHUB_ADMIN_TEAM"),
				TokenEncKey:        os.Getenv("WL_TOKEN_ENC_KEY"),
```

- [ ] **Step 6: Verify it compiles (handlers come next; this step must fail to build until Task 7/8 add them)**

Run: `go build ./... 2>&1 | head`
Expected: FAIL — `s.githubLogin undefined`, `s.githubCallback undefined`. This is expected; do NOT commit yet. Proceed to Task 7.

### Task 7: `provisionGitHubActor` + web callback URL helper

**Files:**
- Create: `internal/api/githubweb.go`
- Test: `internal/api/githubweb_test.go`

- [ ] **Step 1: Write the failing test**

`internal/api/githubweb_test.go` (uses the same fake-GitHub approach as `githubauth_test`; the helper `newGitHubServer` builds a configured `*server` pointed at a fake GitHub — match the existing api test harness for constructing `server`/store, e.g. an existing `newTestServer`/`newOIDCEnv` helper if present):

```go
package api

import (
	"context"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
)

func TestProvisionGitHubActorNamespacesID(t *testing.T) {
	st := newAPITestStore(t) // open temp store + Migrate; use existing package helper if one exists
	s := &server{st: st, cfg: Config{}}
	id, err := s.provisionGitHubActor(context.Background(),
		&githubauth.Identity{ID: 42, Login: "octocat", Name: "The Octocat"},
		githubauth.Roles{User: true, Admin: true})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if id != "github:42" {
		t.Fatalf("want github:42, got %s", id)
	}
	a, err := st.GetActor(context.Background(), "github:42")
	if err != nil {
		t.Fatalf("get actor: %v", err)
	}
	if a.Kind != "human" || !a.Admin || a.DisplayName != "octocat" {
		t.Fatalf("bad actor: %+v", a)
	}
}

func TestProvisionGitHubActorRejectsNonMember(t *testing.T) {
	st := newAPITestStore(t)
	s := &server{st: st, cfg: Config{}}
	_, err := s.provisionGitHubActor(context.Background(),
		&githubauth.Identity{ID: 7, Login: "stranger"},
		githubauth.Roles{User: false})
	if err != errNoUserRole {
		t.Fatalf("want errNoUserRole, got %v", err)
	}
}
```

> Note: if the api package has no shared `newAPITestStore`/store-opening helper, add one mirroring `openTestStore` from Task 3 (open a temp DB, `Migrate()`, `t.Cleanup(Close)`), placed in an existing `*_test.go` helper file for the package.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestProvisionGitHubActor ./internal/api/`
Expected: FAIL — `undefined: s.provisionGitHubActor`.

- [ ] **Step 3: Write the implementation**

`internal/api/githubweb.go`:

```go
// githubweb.go adds "Sign in with GitHub" as a second web identity provider,
// alongside the Keycloak flow in oidcweb.go (which is untouched):
//   - GET /auth/github/login starts the GitHub App user-authorization flow.
//   - GET /auth/github/callback redeems the code, reads identity + org/team
//     membership, provisions a github:<id> actor, stores the encrypted
//     user-to-server token, and sets the same session cookie webAuth checks.
// All routes 404 when GitHub auth is unconfigured (s.gh == nil).
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// githubCallbackURL is the GitHub web redirect URI, distinct from Keycloak's
// /auth/callback so both providers coexist.
func (s *server) githubCallbackURL() string {
	return trimSlash(s.cfg.PublicURL) + "/auth/github/callback"
}

// provisionGitHubActor enforces the org-derived user role and upserts the
// github:<id> human actor, syncing the admin flag from team membership. It
// returns the namespaced actor id.
func (s *server) provisionGitHubActor(ctx context.Context, id *githubauth.Identity, roles githubauth.Roles) (string, error) {
	if !roles.User {
		return "", errNoUserRole
	}
	actorID := "github:" + strconv.FormatInt(id.ID, 10)
	existing, err := s.st.GetActor(ctx, actorID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	if existing != nil && existing.Kind != "human" {
		return "", errActorKindConflict
	}
	if err := s.st.UpsertHumanActor(ctx, actorID, id.Login, roles.Admin); err != nil {
		return "", err
	}
	return actorID, nil
}
```

> `trimSlash` already exists as the inline `strings.TrimRight(..., "/")` used by `callbackURL()`. If it is not a shared helper, replace `trimSlash(s.cfg.PublicURL)` with `strings.TrimRight(s.cfg.PublicURL, "/")` and add `"strings"` to the imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestProvisionGitHubActor ./internal/api/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/githubweb.go internal/api/githubweb_test.go internal/api/server.go internal/cmd/serve.go
git commit -m "api: github config wiring + provisionGitHubActor"
```

### Task 8: Login + callback handlers

**Files:**
- Modify: `internal/api/githubweb.go`
- Test: `internal/api/githubweb_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/githubweb_test.go`:

```go
import (
	"net/http/httptest"
	"net/url"
	// ...plus the imports already used above
)

func TestGitHubLoginRedirects(t *testing.T) {
	st := newAPITestStore(t)
	s := &server{st: st, cfg: Config{PublicURL: "https://wl.test", SessionSecret: "sekret"}}
	s.gh = githubauth.New("cid", "secret", "sunstoneinstitute", "worklode-admins")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/github/login", nil)
	s.githubLogin(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("code=%d", rr.Code)
	}
	loc, _ := url.Parse(rr.Header().Get("Location"))
	if !strings.Contains(loc.String(), "client_id=cid") || loc.Query().Get("state") == "" {
		t.Fatalf("bad redirect: %s", loc)
	}
	if len(rr.Result().Cookies()) == 0 {
		t.Fatal("expected oauth-state cookie")
	}
}

func TestGitHubLogin404WhenDisabled(t *testing.T) {
	s := &server{cfg: Config{}}
	rr := httptest.NewRecorder()
	s.githubLogin(rr, httptest.NewRequest("GET", "/auth/github/login", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code=%d", rr.Code)
	}
}
```

For the full callback, add a test that drives login → fake GitHub token+identity+membership → callback and asserts a `wl_session` cookie and a stored encrypted token. Because the callback calls `s.gh.Exchange`, point `s.gh.Endpoint` and `s.gh.APIBase` at an `httptest` server (as in `githubauth_test`) and set `s.tokenCipher` via `tokencrypt.New(bytes.Repeat([]byte{1},32))`. Assert:
- `rr.Code == 302` and a `wl_session` cookie is set;
- `st.GetGitHubUserToken(ctx, "github:42")` returns non-nil, and `s.tokenCipher.Open(that)` decrypts to JSON containing the access token.

```go
func TestGitHubCallbackSetsSessionAndStoresToken(t *testing.T) {
	// gh points at a fake GitHub serving:
	//   POST /login/oauth/access_token -> access_token=gho_x&token_type=bearer
	//   GET  /user -> {id:42, login:"octocat"}
	//   GET  /user/memberships/orgs/sunstoneinstitute -> {state:"active"}
	//   GET  /orgs/.../teams/worklode-admins/memberships/octocat -> 404
	// Build server, mint the oauth-state cookie via signOAuthState, then call
	// s.githubCallback with matching ?state=&code=. Assert session cookie set
	// and GetGitHubUserToken decrypts to a payload containing "gho_x".
}
```

Implement this test body concretely using the `fakeGitHub` pattern from `internal/githubauth/githubauth_test.go` (a `net/http/httptest.Server` whose handler switches on `r.URL.Path`, with the `access_token` endpoint returning `w.Write([]byte("access_token=gho_x&token_type=bearer"))` under `Content-Type: application/x-www-form-urlencoded`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestGitHub ./internal/api/`
Expected: FAIL — `undefined: s.githubLogin` / `s.githubCallback`.

- [ ] **Step 3: Write the implementation**

Append to `internal/api/githubweb.go` (imports: add `"golang.org/x/oauth2"` is NOT needed; add `"encoding/json"`):

```go
// githubLogin handles GET /auth/github/login: begin the GitHub OAuth flow.
func (s *server) githubLogin(w http.ResponseWriter, r *http.Request) {
	if s.gh == nil {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	next := safeNext(r.URL.Query().Get("next"))
	state, err := randToken()
	if err != nil {
		s.log.Error("generate login state", "err", err)
		webErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	now := s.st.Now()
	http.SetCookie(w, &http.Cookie{
		Name:     oauthCookieName,
		Value:    signOAuthState(s.cfg.SessionSecret, oauthState{State: state, Next: next, Exp: now.Add(oauthStateMaxAge).Unix()}),
		Path:     "/auth/",
		MaxAge:   int(oauthStateMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.gh.AuthCodeURL(s.githubCallbackURL(), state), http.StatusFound)
}

// githubUserToken is the JSON shape sealed into github_user_tokens.
type githubUserToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Expiry       string `json:"expiry"` // RFC3339, empty if none
}

// githubCallback handles GET /auth/github/callback.
func (s *server) githubCallback(w http.ResponseWriter, r *http.Request) {
	if s.gh == nil {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	c, err := r.Cookie(oauthCookieName)
	if err != nil {
		webErr(w, http.StatusBadRequest, "missing login state")
		return
	}
	stt, ok := verifyOAuthState(s.cfg.SessionSecret, c.Value, s.st.Now())
	if !ok {
		webErr(w, http.StatusBadRequest, "invalid or expired login state")
		return
	}
	if r.URL.Query().Get("state") != stt.State {
		webErr(w, http.StatusBadRequest, "state mismatch")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		webErr(w, http.StatusBadRequest, "missing code")
		return
	}

	tok, err := s.gh.Exchange(r.Context(), s.githubCallbackURL(), code)
	if err != nil {
		s.log.Error("github code exchange", "err", err)
		webErr(w, http.StatusBadGateway, "login failed")
		return
	}
	identity, err := s.gh.FetchIdentity(r.Context(), tok.AccessToken)
	if err != nil {
		s.log.Error("github fetch identity", "err", err)
		webErr(w, http.StatusBadGateway, "login failed")
		return
	}
	roles, err := s.gh.Roles(r.Context(), tok.AccessToken, identity.Login)
	if err != nil {
		s.log.Error("github membership", "err", err)
		webErr(w, http.StatusBadGateway, "login failed")
		return
	}

	actorID, err := s.provisionGitHubActor(r.Context(), identity, roles)
	if errors.Is(err, errNoUserRole) {
		webErr(w, http.StatusForbidden, fmt.Sprintf("must be a member of the %s org", s.gh.Org))
		return
	}
	if errors.Is(err, errActorKindConflict) {
		webErr(w, http.StatusConflict, "actor id conflicts with an existing non-human actor")
		return
	}
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	if err := s.storeGitHubToken(r.Context(), actorID, tok); err != nil {
		s.log.Error("store github token", "err", err)
		webErr(w, http.StatusInternalServerError, "internal error")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    signSession(s.cfg.SessionSecret, actorID, s.st.Now().Add(sessionLifetime)),
		Path:     "/",
		MaxAge:   int(sessionLifetime.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{Name: oauthCookieName, Path: "/auth/", MaxAge: -1})
	http.Redirect(w, r, safeNext(stt.Next), http.StatusFound)
}

// storeGitHubToken seals the token pair and upserts it for actorID.
func (s *server) storeGitHubToken(ctx context.Context, actorID string, tok *githubauth.Token) error {
	payload := githubUserToken{AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken}
	if !tok.Expiry.IsZero() {
		payload.Expiry = tok.Expiry.UTC().Format(timeRFC3339)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ct, err := s.tokenCipher.Seal(raw)
	if err != nil {
		return err
	}
	return s.st.UpsertGitHubUserToken(ctx, actorID, ct)
}
```

> `timeRFC3339`: use `time.RFC3339` and add `"time"` to imports (the literal `timeRFC3339` above is shorthand — replace with `time.RFC3339`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api/ ./internal/githubauth/ ./internal/tokencrypt/ ./internal/store/`
Expected: PASS.

- [ ] **Step 5: Full build + vet + gofmt**

Run: `gofmt -l . && go vet ./... && go build ./...`
Expected: no gofmt output, no vet errors, clean build.

- [ ] **Step 6: Commit**

```bash
git add internal/api/githubweb.go internal/api/githubweb_test.go
git commit -m "api: github web login + callback handlers"
```

### Task 9: Offer both providers on the login page

**Files:**
- Modify: `internal/api/oidcweb.go` (the `webAuth` redirect target) — see note below.

The current `webAuth` 302s straight to `/auth/login` (Keycloak). With two providers, unauthenticated users need a choice.

- [ ] **Step 1: Decide the minimal UX**

If **only one** provider is enabled, redirect straight to it. If **both** are enabled, redirect to a tiny chooser page. Implement `s.loginTarget()`:

```go
// loginTarget returns where webAuth sends unauthenticated users, given which
// providers are configured. With both, it points at the chooser page.
func (s *server) loginTarget(next string) string {
	q := "?next=" + url.QueryEscape(next)
	switch {
	case s.oidc != nil && s.gh != nil:
		return "/auth/choose" + q
	case s.gh != nil:
		return "/auth/github/login" + q
	default:
		return "/auth/login" + q
	}
}
```

- [ ] **Step 2: Point webAuth at loginTarget**

In `webAuth`, replace the redirect line with:

```go
		http.Redirect(w, r, s.loginTarget(r.URL.Path), http.StatusFound)
```

- [ ] **Step 3: Add the chooser handler + route (only reached when both enabled)**

Add to `githubweb.go`:

```go
// authChoose renders a minimal provider chooser when both Keycloak and GitHub
// are enabled. next is passed through to whichever provider the user picks.
func (s *server) authChoose(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	q := "?next=" + url.QueryEscape(next)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><title>Sign in</title>`+
		`<h1>Sign in to worklode</h1>`+
		`<p><a href="/auth/github/login%s">Sign in with GitHub</a></p>`+
		`<p><a href="/auth/login%s">Sign in with Keycloak</a></p>`, q, q)
}
```

Register in `server.go`: `mux.HandleFunc("GET /auth/choose", s.authChoose)`. Add `"net/url"` to `githubweb.go` imports if not present.

- [ ] **Step 4: Test the routing decision**

Add to `githubweb_test.go`:

```go
func TestLoginTarget(t *testing.T) {
	both := &server{oidc: &oidc.Verifier{}, gh: &githubauth.Client{}}
	if got := both.loginTarget("/x"); !strings.HasPrefix(got, "/auth/choose") {
		t.Fatalf("both: %s", got)
	}
	ghOnly := &server{gh: &githubauth.Client{}}
	if got := ghOnly.loginTarget("/x"); !strings.HasPrefix(got, "/auth/github/login") {
		t.Fatalf("gh only: %s", got)
	}
	kcOnly := &server{oidc: &oidc.Verifier{}}
	if got := kcOnly.loginTarget("/x"); !strings.HasPrefix(got, "/auth/login") {
		t.Fatalf("kc only: %s", got)
	}
}
```

> Requires importing `"github.com/sunstoneinstitute/worklode/internal/oidc"` in the test. `oidc.Verifier{}` zero value is fine here — `loginTarget` only checks for non-nil.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/api/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/oidcweb.go internal/api/githubweb.go internal/api/githubweb_test.go internal/api/server.go
git commit -m "api: provider chooser when both keycloak and github are enabled"
```

---

## Final verification

- [ ] Run the whole suite with the race detector:

Run: `go test -race -count=1 ./...`
Expected: all PASS.

- [ ] Confirm gofmt/vet clean:

Run: `gofmt -l . && go vet ./...`
Expected: no output, no errors.

---

## Follow-up plans (out of scope here)

1. **CLI device flow + OS keychain** — `POST /auth/github/device/start` + `/poll`, `wl login --github`, store the wl token via `zalando/go-keyring` with a `0600` file fallback. Spec Section C.
2. **Deploy + secrets wiring (hzdev)** — add `WL_GITHUB_APP_CLIENT_ID`, `WL_GITHUB_ORG`, `WL_GITHUB_ADMIN_TEAM` to the ConfigMap and `WL_GITHUB_APP_CLIENT_SECRET`, `WL_TOKEN_ENC_KEY` to the ExternalSecret; create the `worklode-dev` GitHub App and install it. Spec Sections A, F. (Note: on `main` the deploy/ overlays do not yet exist — they arrive with the CI/deploy branch; sequence this after that lands.)
3. **First outbound "on behalf of user" action + token refresh** — lazy refresh of the stored token before the call (Spec Section E's refresh path), plus the concrete GitHub write. Defines the exact repo permissions to request on the App (Spec Section A).

---

## Self-review notes

- **Spec coverage:** Section A (App/callback) → Task 6 config + Task 8 callback URL; Section B (web login) → Tasks 6–9; Section D (roles) → Task 5 + Task 7; Section E (identity key + encrypted storage) → Tasks 1–3 + Task 7 (`github:<id>`) + Task 8 (seal+store). CLI (Section C) and deploy (Section F) are explicitly deferred to follow-up plans.
- **No user migration / coexistence:** GitHub actors namespaced `github:<id>`; Keycloak path untouched; both providers gate independently. Matches spec.
- **Type consistency:** `githubauth.Client`, `.Identity`, `.Token`, `.Roles`, `.AuthCodeURL/.Exchange/.FetchIdentity/.Roles` used identically across Tasks 4–8; store methods `UpsertGitHubUserToken`/`GetGitHubUserToken` consistent Tasks 3 & 8; `tokencrypt.New/.Seal/.Open` consistent Tasks 2 & 8.
- **Test-harness caveat:** api tests assume a package store-opening helper. Task 7 Step 1 note instructs adding `newAPITestStore` mirroring `openTestStore` if none exists — resolve by inspecting existing `internal/api/*_test.go` before writing.
