---
status: accepted
covers:
  - docs/specs/001-identity-and-authentication.md#sec-9.3
  - docs/specs/001-identity-and-authentication.md#sec-9.5
requires:
  - 2026-08-11-keycloak-primary-auth-identity.md
---
# Keycloak-Primary Auth 2 — GitHub link storage, refresh, and the web link flow

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement spec 023 §C and §E: rework `github_user_tokens` so the row **is** the link, add `store.UserToken` with single-use-refresh-token-safe rotation, and serve the authenticated web link flow (`/auth/github/link` → `/auth/github/callback`) that strict-matches against `expected_github_login`.

**Architecture:** A link exists iff a row exists; unlink is a delete. The row stores the sealed `{access_token, refresh_token, access_expires_at}` blob, the durable numeric GitHub user id, the renameable login as display metadata, and `status ∈ {active, broken}`. `store.UserToken` locks the row with `SELECT … FOR UPDATE` for the whole refresh, because GitHub refresh tokens are single-use: a concurrent caller must block and reuse the pair the winner persisted, never race a second refresh. Encryption and the GitHub round trip are injected as interfaces (`TokenCodec`, `TokenRefresher`) so `internal/store` keeps importing neither `tokencrypt` nor `githubauth`.

**Tech Stack:** Go 1.25+, Postgres (golang-migrate), `golang.org/x/oauth2` (refresh-token grant via `TokenSource`), AES-GCM (`internal/tokencrypt`), net/http `ServeMux`.

**Read first:** `docs/specs/001-identity-and-authentication.md` §3.3, §3.5, §5, `internal/store/github_tokens.go` (replaced here), `internal/api/oidcweb.go` (the state cookie and callback shape the link flow copies), `internal/store/store.go:142` (`Tx`), `internal/tokencrypt/` (`Seal`/`Open`).

**Prerequisite:** plan 1 is merged — `actors.expected_github_login` exists and `githubauth.Client` has no `Org`/`Roles`.

**Conventions:**
- Run `go test ./internal/...`. **Store and API tests need Postgres with pgvector**; override the DSN with `TEST_POSTGRES_DSN`.
- Commit after every task, imperative mood, no trailers.
- List new migrations in `deploy/base/kustomization.yaml`. If `./scripts/check-migrations.sh` renumbers `0011`, use the number it assigns.

---

## File structure

| File | Responsibility |
|---|---|
| `deploy/base/migrations/0011_github_links.{up,down}.sql` (new) | Rework `github_user_tokens` into the link row |
| `internal/store/github_tokens.go` | `GitHubLink`, upsert/get/delete, `UserToken` + its two interfaces |
| `internal/store/github_tokens_test.go` | Uniqueness, unlink, rotation, concurrent refresh, broken-marking |
| `internal/githubauth/githubauth.go` | `Refresh` — the refresh-token grant |
| `internal/api/githublink.go` (new) | `/auth/github/link`, `/auth/github/callback`, the `TokenRefresher` adapter |
| `internal/api/githublink_test.go` (new) | Strict-match refusal, success, state mismatch |
| `internal/api/web.go`, `internal/api/templates/profile.html` (new) | `/profile` — link state and a Connect button |
| `internal/api/server.go` | Three routes; `tmplProfile`; metrics fields |
| `internal/api/metrics.go` | `worklode_github_link_attempts_total`, `worklode_github_token_refreshes_total` |
| `docs/follow-ups.md` | Duplicate-link 409 mapping recorded as a follow-up |

---

## Tasks

### Task 1 — The link row

```yaml
kind: feature
priority: high
blockedBy: []
```

**Files:**
- Create: `deploy/base/migrations/0011_github_links.up.sql`, `…down.sql`
- Modify: `deploy/base/kustomization.yaml`, `internal/store/github_tokens.go`
- Test: `internal/store/github_tokens_test.go`

- [ ] **Step 1: Write the migration**

`deploy/base/migrations/0011_github_links.up.sql`:

```sql
-- Spec 001 §9.5: the token row IS the GitHub account link. A link exists iff a
-- row exists; unlink is a delete. The old table held only an opaque blob
-- written by the removed GitHub login and never read, so there is nothing to
-- preserve.
DROP TABLE github_user_tokens;

CREATE TABLE github_user_tokens (
    actor_id          text PRIMARY KEY REFERENCES actors (id) ON DELETE CASCADE,
    -- Durable external identity. The login is renameable; the id is not.
    github_user_id    bigint NOT NULL UNIQUE,
    github_login      text NOT NULL,
    -- AES-GCM over {access_token, refresh_token, access_expires_at}.
    token_ciphertext  bytea NOT NULL,
    status            text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'broken')),
    created_at        timestamptz NOT NULL,
    updated_at        timestamptz NOT NULL
);
```

`deploy/base/migrations/0011_github_links.down.sql`:

```sql
DROP TABLE github_user_tokens;

CREATE TABLE github_user_tokens (
    actor_id   text PRIMARY KEY REFERENCES actors (id) ON DELETE CASCADE,
    ciphertext bytea NOT NULL,
    updated_at timestamptz NOT NULL
);
```

Add both filenames to the `worklode-migrations` file list in `deploy/base/kustomization.yaml`.

- [ ] **Step 2: Write the failing test**

Replace the contents of `internal/store/github_tokens_test.go` (the two existing tests cover the dropped shape):

```go
package store

import (
	"errors"
	"testing"
)

func seedLinkActors(t *testing.T, s *Store, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := s.UpsertHumanActor(t.Context(), id, id, false, ""); err != nil {
			t.Fatalf("actor %s: %v", id, err)
		}
	}
}

func TestUpsertGitHubLinkRoundTripAndRelink(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()
	seedLinkActors(t, s, "stig")

	if err := s.UpsertGitHubLink(ctx, GitHubLink{
		ActorID: "stig", GitHubUserID: 42, GitHubLogin: "stigsb", Ciphertext: []byte("ct-a"),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.GetGitHubLink(ctx, "stig")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.GitHubUserID != 42 || got.GitHubLogin != "stigsb" || got.Status != "active" {
		t.Fatalf("link = %+v, want 42/stigsb/active", got)
	}

	// Re-linking after a rename refreshes the display login and clears broken.
	if err := s.MarkGitHubLinkBroken(ctx, "stig"); err != nil {
		t.Fatalf("mark broken: %v", err)
	}
	if err := s.UpsertGitHubLink(ctx, GitHubLink{
		ActorID: "stig", GitHubUserID: 42, GitHubLogin: "stig-b", Ciphertext: []byte("ct-b"),
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _ = s.GetGitHubLink(ctx, "stig")
	if got.GitHubLogin != "stig-b" || got.Status != "active" || string(got.Ciphertext) != "ct-b" {
		t.Fatalf("link = %+v, want stig-b/active/ct-b", got)
	}
}

func TestGitHubUserIDIsUniqueAcrossActors(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()
	seedLinkActors(t, s, "stig", "other")

	if err := s.UpsertGitHubLink(ctx, GitHubLink{
		ActorID: "stig", GitHubUserID: 42, GitHubLogin: "stigsb", Ciphertext: []byte("ct"),
	}); err != nil {
		t.Fatalf("first link: %v", err)
	}
	err := s.UpsertGitHubLink(ctx, GitHubLink{
		ActorID: "other", GitHubUserID: 42, GitHubLogin: "stigsb", Ciphertext: []byte("ct"),
	})
	if err == nil {
		t.Fatal("second actor linked the same GitHub user; want a uniqueness error")
	}
}

func TestDeleteGitHubLinkIsUnlink(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()
	seedLinkActors(t, s, "stig")

	if err := s.UpsertGitHubLink(ctx, GitHubLink{
		ActorID: "stig", GitHubUserID: 42, GitHubLogin: "stigsb", Ciphertext: []byte("ct"),
	}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := s.DeleteGitHubLink(ctx, "stig"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetGitHubLink(ctx, "stig"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'GitHub' -v`
Expected: FAIL — `undefined: GitHubLink`, `UpsertGitHubLink`, `GetGitHubLink`, `DeleteGitHubLink`, `MarkGitHubLinkBroken`.

- [ ] **Step 4: Write the implementation**

Replace `internal/store/github_tokens.go` with:

```go
// GitHub account links (spec 001 §9.5): the row IS the link. A link exists iff
// a row exists, so unlink is a delete. The store never inspects the
// ciphertext — sealing is the caller's job.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// GitHubLink is one actor's link to a GitHub account.
type GitHubLink struct {
	ActorID      string
	GitHubUserID int64
	GitHubLogin  string
	Ciphertext   []byte
	Status       string // "active" or "broken"
}

// UpsertGitHubLink creates or replaces the link for one actor. Re-linking
// refreshes the display login and clears a broken status, which is what makes
// "reconnect GitHub" a working repair rather than an unlink-first ceremony.
func (s *Store) UpsertGitHubLink(ctx context.Context, l GitHubLink) error {
	now := s.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO github_user_tokens
		   (actor_id, github_user_id, github_login, token_ciphertext, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, 'active', $5, $5)
		 ON CONFLICT (actor_id) DO UPDATE SET
		   github_user_id = excluded.github_user_id,
		   github_login = excluded.github_login,
		   token_ciphertext = excluded.token_ciphertext,
		   status = 'active',
		   updated_at = excluded.updated_at`,
		l.ActorID, l.GitHubUserID, l.GitHubLogin, l.Ciphertext, now,
	)
	if err != nil {
		return fmt.Errorf("upsert github link for %s: %w", l.ActorID, err)
	}
	return nil
}

// GetGitHubLink returns the link for actorID, or ErrNotFound when unlinked.
func (s *Store) GetGitHubLink(ctx context.Context, actorID string) (*GitHubLink, error) {
	var l GitHubLink
	row := s.db.QueryRowContext(ctx,
		`SELECT actor_id, github_user_id, github_login, token_ciphertext, status
		   FROM github_user_tokens WHERE actor_id = $1`, actorID)
	if err := row.Scan(&l.ActorID, &l.GitHubUserID, &l.GitHubLogin, &l.Ciphertext, &l.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get github link for %s: %w", actorID, err)
	}
	return &l, nil
}

// DeleteGitHubLink unlinks actorID. Deleting a link that does not exist is not
// an error: unlink is idempotent.
func (s *Store) DeleteGitHubLink(ctx context.Context, actorID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM github_user_tokens WHERE actor_id = $1`, actorID); err != nil {
		return fmt.Errorf("delete github link for %s: %w", actorID, err)
	}
	return nil
}

// MarkGitHubLinkBroken records that the stored token pair can no longer be
// refreshed. Callers translate it into "reconnect GitHub" guidance.
func (s *Store) MarkGitHubLinkBroken(ctx context.Context, actorID string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE github_user_tokens SET status = 'broken', updated_at = $2 WHERE actor_id = $1`,
		actorID, s.Now().UTC()); err != nil {
		return fmt.Errorf("mark github link broken for %s: %w", actorID, err)
	}
	return nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/store/ -run 'GitHub' -v`
Expected: PASS (3 tests). `go build ./...` will flag the removed `UpsertGitHubUserToken`/`GetGitHubUserToken` callers — plan 1 deleted `githubweb.go`, so there should be none.

- [ ] **Step 6: Commit**

```bash
git add deploy/base/migrations/0011_github_links.up.sql \
  deploy/base/migrations/0011_github_links.down.sql deploy/base/kustomization.yaml \
  internal/store/github_tokens.go internal/store/github_tokens_test.go
git commit -m "Make the GitHub token row the account link"
```

---

### Task 2 — `githubauth.Client.Refresh`

```yaml
kind: feature
priority: medium
blockedBy: []
```

**Files:**
- Modify: `internal/githubauth/githubauth.go`
- Test: `internal/githubauth/githubauth_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/githubauth/githubauth_test.go`:

```go
func TestRefreshExchangesRefreshToken(t *testing.T) {
	var gotGrant, gotRefresh string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotGrant = r.Form.Get("grant_type")
		gotRefresh = r.Form.Get("refresh_token")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"gho_new","refresh_token":"ghr_new",` +
			`"token_type":"bearer","expires_in":28800}`))
	}))
	defer srv.Close()

	c := New("cid", "csecret")
	c.Endpoint = oauth2.Endpoint{TokenURL: srv.URL}

	tok, err := c.Refresh(context.Background(), "ghr_old")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if gotGrant != "refresh_token" || gotRefresh != "ghr_old" {
		t.Fatalf("grant=%q refresh=%q, want refresh_token/ghr_old", gotGrant, gotRefresh)
	}
	if tok.AccessToken != "gho_new" || tok.RefreshToken != "ghr_new" {
		t.Fatalf("token = %+v, want gho_new/ghr_new", tok)
	}
	if tok.Expiry.IsZero() {
		t.Fatal("expiry is zero; expires_in must be carried through so UserToken can pre-empt expiry")
	}
}

func TestRefreshFailsOnRevokedGrant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad_refresh_token"}`))
	}))
	defer srv.Close()

	c := New("cid", "csecret")
	c.Endpoint = oauth2.Endpoint{TokenURL: srv.URL}
	if _, err := c.Refresh(context.Background(), "ghr_dead"); err == nil {
		t.Fatal("want an error for a revoked refresh token")
	}
}
```

Add `context`, `net/http`, `net/http/httptest`, and `golang.org/x/oauth2` to the imports if absent.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/githubauth/ -run TestRefresh -v`
Expected: FAIL — `c.Refresh undefined`.

- [ ] **Step 3: Write the implementation**

Add to `internal/githubauth/githubauth.go`, after `Exchange`:

```go
// Refresh trades a refresh token for a fresh user-to-server pair. GitHub
// refresh tokens are single-use: the returned RefreshToken replaces the one
// passed in, and losing it means the user must re-link. Callers must persist
// the whole pair before using the access token.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (*Token, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)
	// An already-expired token makes the source perform the refresh grant
	// rather than hand back what it was given.
	src := c.oauthConfig("").TokenSource(ctx, &oauth2.Token{
		RefreshToken: refreshToken,
		Expiry:       time.Now().Add(-time.Minute),
	})
	tok, err := src.Token()
	if err != nil {
		return nil, fmt.Errorf("github token refresh: %w", err)
	}
	return &Token{AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, Expiry: tok.Expiry}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/githubauth/ -v 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/githubauth/githubauth.go internal/githubauth/githubauth_test.go
git commit -m "Add the GitHub refresh-token grant"
```

---

### Task 3 — `store.UserToken`

```yaml
kind: feature
priority: high
blockedBy: [1]
```

**Files:**
- Modify: `internal/store/github_tokens.go`
- Test: `internal/store/github_tokens_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/github_tokens_test.go`:

```go
// plainCodec is a no-op TokenCodec: these tests exercise locking and rotation,
// not AES-GCM (covered in internal/tokencrypt).
type plainCodec struct{}

func (plainCodec) Seal(b []byte) ([]byte, error) { return b, nil }
func (plainCodec) Open(b []byte) ([]byte, error) { return b, nil }

type fakeRefresher struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *fakeRefresher) Refresh(_ context.Context, _ string) (GitHubToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return GitHubToken{}, f.err
	}
	return GitHubToken{
		AccessToken:  fmt.Sprintf("gho_fresh_%d", f.calls),
		RefreshToken: fmt.Sprintf("ghr_fresh_%d", f.calls),
		ExpiresAt:    time.Now().Add(8 * time.Hour),
	}, nil
}

// seedLink stores a link whose access token expires at exp.
func seedLink(t *testing.T, s *Store, actorID string, exp time.Time) {
	t.Helper()
	seedLinkActors(t, s, actorID)
	blob, err := json.Marshal(map[string]any{
		"access_token": "gho_old", "refresh_token": "ghr_old", "access_expires_at": exp,
	})
	if err != nil {
		t.Fatalf("marshal blob: %v", err)
	}
	if err := s.UpsertGitHubLink(t.Context(), GitHubLink{
		ActorID: actorID, GitHubUserID: 42, GitHubLogin: "stigsb", Ciphertext: blob,
	}); err != nil {
		t.Fatalf("seed link: %v", err)
	}
}

func TestUserTokenReturnsUnexpiredTokenWithoutRefreshing(t *testing.T) {
	s := openTestStore(t)
	seedLink(t, s, "stig", time.Now().Add(time.Hour))
	rf := &fakeRefresher{}

	got, err := s.UserToken(t.Context(), "stig", plainCodec{}, rf)
	if err != nil {
		t.Fatalf("UserToken: %v", err)
	}
	if got != "gho_old" || rf.calls != 0 {
		t.Fatalf("token=%q calls=%d, want gho_old/0", got, rf.calls)
	}
}

func TestUserTokenRefreshesAndPersistsTheNewPair(t *testing.T) {
	s := openTestStore(t)
	seedLink(t, s, "stig", time.Now().Add(-time.Hour))
	rf := &fakeRefresher{}

	got, err := s.UserToken(t.Context(), "stig", plainCodec{}, rf)
	if err != nil {
		t.Fatalf("UserToken: %v", err)
	}
	if got != "gho_fresh_1" {
		t.Fatalf("token = %q, want gho_fresh_1", got)
	}
	// The rotated refresh token must be persisted: GitHub invalidated the old
	// one, so a lost write means the link is dead on the next refresh.
	link, _ := s.GetGitHubLink(t.Context(), "stig")
	if !bytes.Contains(link.Ciphertext, []byte("ghr_fresh_1")) {
		t.Fatalf("stored blob = %s, want the rotated refresh token", link.Ciphertext)
	}
}

func TestUserTokenRefreshesOnceUnderConcurrency(t *testing.T) {
	s := openTestStore(t)
	seedLink(t, s, "stig", time.Now().Add(-time.Hour))
	rf := &fakeRefresher{}

	var wg sync.WaitGroup
	tokens := make([]string, 4)
	for i := range tokens {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, err := s.UserToken(context.Background(), "stig", plainCodec{}, rf)
			if err != nil {
				t.Errorf("caller %d: %v", i, err)
				return
			}
			tokens[i] = tok
		}(i)
	}
	wg.Wait()

	if rf.calls != 1 {
		t.Fatalf("refresh calls = %d, want 1 — GitHub refresh tokens are single-use, "+
			"so losers must block on the row lock and reuse the winner's pair", rf.calls)
	}
	for i, tok := range tokens {
		if tok != "gho_fresh_1" {
			t.Fatalf("caller %d got %q, want gho_fresh_1", i, tok)
		}
	}
}

func TestUserTokenMarksLinkBrokenOnRefreshFailure(t *testing.T) {
	s := openTestStore(t)
	seedLink(t, s, "stig", time.Now().Add(-time.Hour))
	rf := &fakeRefresher{err: errors.New("bad_refresh_token")}

	_, err := s.UserToken(t.Context(), "stig", plainCodec{}, rf)
	if !errors.Is(err, ErrLinkBroken) {
		t.Fatalf("err = %v, want ErrLinkBroken", err)
	}
	link, gerr := s.GetGitHubLink(t.Context(), "stig")
	if gerr != nil {
		t.Fatalf("get link: %v", gerr)
	}
	if link.Status != "broken" {
		t.Fatalf("status = %q, want broken — the marking must survive the failing call", link.Status)
	}
	// A broken link short-circuits without another doomed GitHub round trip.
	if _, err := s.UserToken(t.Context(), "stig", plainCodec{}, rf); !errors.Is(err, ErrLinkBroken) {
		t.Fatalf("second call err = %v, want ErrLinkBroken", err)
	}
	if rf.calls != 1 {
		t.Fatalf("refresh calls = %d, want 1", rf.calls)
	}
}

func TestUserTokenNotFoundWhenUnlinked(t *testing.T) {
	s := openTestStore(t)
	seedLinkActors(t, s, "stig")
	if _, err := s.UserToken(t.Context(), "stig", plainCodec{}, &fakeRefresher{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
```

Add `bytes`, `context`, `encoding/json`, `fmt`, `sync`, and `time` to the file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestUserToken -v`
Expected: FAIL — `undefined: GitHubToken`, `ErrLinkBroken`, `s.UserToken`.

- [ ] **Step 3: Write the implementation**

Append to `internal/store/github_tokens.go` (and add `database/sql`, `encoding/json`, `time` to its imports):

```go
// ErrLinkBroken reports a link whose refresh token no longer works — the user
// revoked the App's authorization, or let it lapse past GitHub's ~6-month
// refresh window. The repair is re-linking, not retrying.
var ErrLinkBroken = errors.New("github link broken")

// refreshSkew refreshes slightly ahead of expiry so a token handed to a caller
// does not expire mid-request.
const refreshSkew = 5 * time.Minute

// GitHubToken is a user-to-server token pair. internal/githubauth returns the
// same shape; the API layer adapts between them so the store imports neither
// githubauth nor tokencrypt.
type GitHubToken struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// TokenCodec seals and opens the stored blob. *tokencrypt.Cipher satisfies it.
type TokenCodec interface {
	Seal([]byte) ([]byte, error)
	Open([]byte) ([]byte, error)
}

// TokenRefresher performs the GitHub refresh-token grant.
type TokenRefresher interface {
	Refresh(ctx context.Context, refreshToken string) (GitHubToken, error)
}

// linkBlob is the JSON sealed into token_ciphertext.
type linkBlob struct {
	AccessToken     string    `json:"access_token"`
	RefreshToken    string    `json:"refresh_token"`
	AccessExpiresAt time.Time `json:"access_expires_at"`
}

// UserToken returns a usable GitHub access token for actorID, refreshing when
// it has expired or is within refreshSkew of doing so. The row is held under
// SELECT … FOR UPDATE for the whole refresh because GitHub refresh tokens are
// single-use: concurrent callers block, then re-read and reuse the pair the
// winner persisted, instead of racing a second grant that would invalidate the
// first. There is no background refresher — on demand suffices given the
// ~6-month refresh-token lifetime.
//
// A failed refresh marks the link broken (committed, so it survives) and
// returns ErrLinkBroken; an unlinked actor returns ErrNotFound.
func (s *Store) UserToken(ctx context.Context, actorID string, codec TokenCodec, rt TokenRefresher) (string, error) {
	var access string
	var broken bool
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var ct []byte
		var status string
		row := tx.QueryRowContext(ctx,
			`SELECT token_ciphertext, status FROM github_user_tokens
			   WHERE actor_id = $1 FOR UPDATE`, actorID)
		if err := row.Scan(&ct, &status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock github link for %s: %w", actorID, err)
		}
		if status != "active" {
			broken = true
			return nil
		}
		raw, err := codec.Open(ct)
		if err != nil {
			return fmt.Errorf("open github token blob for %s: %w", actorID, err)
		}
		var b linkBlob
		if err := json.Unmarshal(raw, &b); err != nil {
			return fmt.Errorf("decode github token blob for %s: %w", actorID, err)
		}
		if b.AccessExpiresAt.IsZero() || s.Now().Add(refreshSkew).Before(b.AccessExpiresAt) {
			access = b.AccessToken
			return nil
		}

		fresh, rerr := rt.Refresh(ctx, b.RefreshToken)
		if rerr != nil {
			// Commit the broken marking rather than returning the error, which
			// would roll the UPDATE back with it.
			if _, e := tx.ExecContext(ctx,
				`UPDATE github_user_tokens SET status = 'broken', updated_at = $2 WHERE actor_id = $1`,
				actorID, s.Now().UTC()); e != nil {
				return fmt.Errorf("mark github link broken for %s: %w", actorID, e)
			}
			broken = true
			return nil
		}

		sealed, err := sealBlob(codec, linkBlob{
			AccessToken:     fresh.AccessToken,
			RefreshToken:    fresh.RefreshToken,
			AccessExpiresAt: fresh.ExpiresAt,
		})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE github_user_tokens SET token_ciphertext = $2, updated_at = $3 WHERE actor_id = $1`,
			actorID, sealed, s.Now().UTC()); err != nil {
			return fmt.Errorf("persist refreshed github token for %s: %w", actorID, err)
		}
		access = fresh.AccessToken
		return nil
	})
	if err != nil {
		return "", err
	}
	if broken {
		return "", fmt.Errorf("github link for %s: %w", actorID, ErrLinkBroken)
	}
	return access, nil
}

// sealBlob marshals and seals a token pair. Exported as SealGitHubToken for the
// link flow, which writes the first blob before any refresh happens.
func sealBlob(codec TokenCodec, b linkBlob) ([]byte, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("encode github token blob: %w", err)
	}
	sealed, err := codec.Seal(raw)
	if err != nil {
		return nil, fmt.Errorf("seal github token blob: %w", err)
	}
	return sealed, nil
}

// SealGitHubToken seals a token pair into the blob UserToken expects. The link
// flow calls it to build the ciphertext for UpsertGitHubLink.
func SealGitHubToken(codec TokenCodec, tok GitHubToken) ([]byte, error) {
	return sealBlob(codec, linkBlob{
		AccessToken:     tok.AccessToken,
		RefreshToken:    tok.RefreshToken,
		AccessExpiresAt: tok.ExpiresAt,
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/store/ -run 'UserToken|GitHub' -v`
Expected: PASS (8 tests). `-race` matters: the concurrency test is the point.

- [ ] **Step 5: Commit**

```bash
git add internal/store/github_tokens.go internal/store/github_tokens_test.go
git commit -m "Add store.UserToken with single-use-safe refresh"
```

---

### Task 4 — The web link flow

```yaml
kind: feature
priority: medium
blockedBy: [1, 2, 3]
```

**Files:**
- Create: `internal/api/githublink.go`, `internal/api/githublink_test.go`
- Modify: `internal/api/server.go`, `docs/follow-ups.md`

- [ ] **Step 1: Write the failing test**

Create `internal/api/githublink_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/tokencrypt"
)

// linkServer builds a server whose GitHub client points at a fake that
// exchanges any code and returns identity {id: 42, login: ghLogin}.
func linkServer(t *testing.T, expectedLogin, ghLogin string) (*store.Store, *server) {
	t.Helper()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			w.Write([]byte(`{"access_token":"gho_x","refresh_token":"ghr_x",` +
				`"token_type":"bearer","expires_in":28800}`))
		case "/user":
			json.NewEncoder(w).Encode(map[string]any{"id": 42, "login": ghLogin})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fake.Close)

	st := store.OpenTestStore(t)
	if err := st.UpsertHumanActor(context.Background(), "stig", "Stig", false, expectedLogin); err != nil {
		t.Fatalf("actor: %v", err)
	}
	key := make([]byte, 32)
	tc, err := tokencrypt.New(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	gh := githubauth.New("cid", "csecret")
	gh.APIBase = fake.URL
	gh.Endpoint = oauth2.Endpoint{AuthURL: fake.URL + "/authorize", TokenURL: fake.URL + "/token"}

	return st, &server{
		st: st, log: slog.Default(), gh: gh, tokenCipher: tc,
		cfg: Config{PublicURL: "https://wl.test", SessionSecret: "sekret"},
	}
}

// callback drives GET /auth/github/callback for actor "stig" with a matching
// state cookie, as githubLinkStart would have set it. The session cookie goes
// on BOTH requests: in this plan the callback re-reads it to identify the
// actor (plan 3 moves the actor into the signed state instead), so sending
// only the oauth-state cookie from the start response would 401 the callback.
func callback(t *testing.T, s *server) *httptest.ResponseRecorder {
	t.Helper()
	sess := &http.Cookie{
		Name:  sessionCookieName,
		Value: signSession(s.cfg.SessionSecret, "stig", s.st.Now().Add(time.Hour)),
	}
	start := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/github/link", nil)
	req.AddCookie(sess)
	s.githubLinkStart(start, req)
	if start.Code != http.StatusFound {
		t.Fatalf("link start status = %d, want 302", start.Code)
	}
	loc, _ := url.Parse(start.Header().Get("Location"))
	state := loc.Query().Get("state")

	rr := httptest.NewRecorder()
	cb := httptest.NewRequest("GET", "/auth/github/callback?code=c&state="+state, nil)
	cb.AddCookie(sess)
	for _, c := range start.Result().Cookies() {
		cb.AddCookie(c)
	}
	s.githubLinkCallback(rr, cb)
	return rr
}

func TestLinkSucceedsWhenLoginMatches(t *testing.T) {
	st, s := linkServer(t, "stigsb", "stigsb")
	rr := callback(t, s)
	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	link, err := st.GetGitHubLink(context.Background(), "stig")
	if err != nil {
		t.Fatalf("get link: %v", err)
	}
	if link.GitHubUserID != 42 || link.GitHubLogin != "stigsb" || link.Status != "active" {
		t.Fatalf("link = %+v, want 42/stigsb/active", link)
	}
}

func TestLinkMatchesCaseInsensitively(t *testing.T) {
	st, s := linkServer(t, "StigSB", "stigsb")
	if rr := callback(t, s); rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 — the match is case-insensitive", rr.Code)
	}
	if _, err := st.GetGitHubLink(context.Background(), "stig"); err != nil {
		t.Fatalf("get link: %v", err)
	}
}

func TestLinkRefusedOnMismatch(t *testing.T) {
	st, s := linkServer(t, "stigsb", "someone-else")
	rr := callback(t, s)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "someone-else") || !strings.Contains(rr.Body.String(), "stigsb") {
		t.Fatalf("body = %q, want both logins named", rr.Body)
	}
	if _, err := st.GetGitHubLink(context.Background(), "stig"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a refused link must write no row")
	}
}

func TestLinkRefusedWhenNoExpectation(t *testing.T) {
	st, s := linkServer(t, "", "stigsb")
	rr := callback(t, s)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	if _, err := st.GetGitHubLink(context.Background(), "stig"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a refused link must write no row")
	}
}

func TestLinkStartRequiresSession(t *testing.T) {
	_, s := linkServer(t, "stigsb", "stigsb")
	rr := httptest.NewRecorder()
	s.githubLinkStart(rr, httptest.NewRequest("GET", "/auth/github/link", nil))
	if rr.Code != http.StatusFound || !strings.HasPrefix(rr.Header().Get("Location"), "/auth/login") {
		t.Fatalf("status=%d loc=%q, want a 302 to /auth/login", rr.Code, rr.Header().Get("Location"))
	}
}
```

Add `errors`, `net/url`, `strings`, and `time` to the imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestLink -v`
Expected: FAIL — `s.githubLinkStart undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/api/githublink.go`:

```go
// githublink.go serves the GitHub account-link flow (spec 001 §9.3). GitHub is
// not an identity provider here: the actor is already authenticated, and the
// link is refused unless the GitHub login matches the github_username Keycloak
// asserts for that actor. Routes 404 when the App is unconfigured.
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// ghRefresher adapts githubauth.Client to store.TokenRefresher, keeping the
// store free of any GitHub import.
type ghRefresher struct{ c *githubauth.Client }

func (g ghRefresher) Refresh(ctx context.Context, refreshToken string) (store.GitHubToken, error) {
	t, err := g.c.Refresh(ctx, refreshToken)
	if err != nil {
		return store.GitHubToken{}, err
	}
	return store.GitHubToken{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, ExpiresAt: t.Expiry}, nil
}

// userToken returns a usable GitHub access token for actorID. It is the single
// entry point for features acting as a user; store.ErrLinkBroken means
// "reconnect GitHub", store.ErrNotFound means "not linked".
func (s *server) userToken(ctx context.Context, actorID string) (string, error) {
	if s.gh == nil || s.tokenCipher == nil {
		return "", errors.New("github app not configured")
	}
	return s.st.UserToken(ctx, actorID, s.tokenCipher, ghRefresher{s.gh})
}

// githubLinkCallbackURL is the redirect URI registered with the App.
func (s *server) githubLinkCallbackURL() string {
	return strings.TrimRight(s.cfg.PublicURL, "/") + "/auth/github/callback"
}

// sessionActor returns the actor id carried by a valid session cookie.
func (s *server) sessionActor(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	return verifySession(s.cfg.SessionSecret, c.Value, s.st.Now())
}

// githubLinkStart handles GET /auth/github/link: begin the link flow for the
// already-authenticated actor. Unauthenticated callers go sign in first.
func (s *server) githubLinkStart(w http.ResponseWriter, r *http.Request) {
	if s.gh == nil {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	// The session cookie, re-read in the callback, is what binds this flow to an
	// actor; the start handler only has to prove one exists.
	if _, ok := s.sessionActor(r); !ok {
		http.Redirect(w, r, s.loginTarget("/auth/github/link"), http.StatusFound)
		return
	}
	state, err := randToken()
	if err != nil {
		s.log.Error("generate link state", "err", err)
		webErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	now := s.st.Now()
	http.SetCookie(w, &http.Cookie{
		Name: oauthCookieName,
		Value: signOAuthState(s.cfg.SessionSecret, oauthState{
			State: state, Next: "/profile", Exp: now.Add(oauthStateMaxAge).Unix(),
		}),
		Path:     "/auth/",
		MaxAge:   int(oauthStateMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.gh.AuthCodeURL(s.githubLinkCallbackURL(), state), http.StatusFound)
}

// githubLinkCallback handles GET /auth/github/callback: redeem the code, read
// the GitHub identity, strict-match it, and store the link.
func (s *server) githubLinkCallback(w http.ResponseWriter, r *http.Request) {
	if s.gh == nil || s.tokenCipher == nil {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	actorID, ok := s.sessionActor(r)
	if !ok {
		webErr(w, http.StatusUnauthorized, "sign in before linking GitHub")
		return
	}
	c, err := r.Cookie(oauthCookieName)
	if err != nil {
		webErr(w, http.StatusBadRequest, "missing link state")
		return
	}
	stt, ok := verifyOAuthState(s.cfg.SessionSecret, c.Value, s.st.Now())
	if !ok {
		webErr(w, http.StatusBadRequest, "invalid or expired link state")
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

	actor, err := s.st.GetActor(r.Context(), actorID)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	if actor.ExpectedGitHubLogin == "" {
		webErr(w, http.StatusForbidden,
			"your Keycloak account has no github_username attribute; get it set before linking")
		return
	}

	tok, err := s.gh.Exchange(r.Context(), s.githubLinkCallbackURL(), code)
	if err != nil {
		s.log.Error("github link code exchange", "err", err)
		webErr(w, http.StatusBadGateway, "linking failed, try again")
		return
	}
	identity, err := s.gh.FetchIdentity(r.Context(), tok.AccessToken)
	if err != nil {
		s.log.Error("github link fetch identity", "err", err)
		webErr(w, http.StatusBadGateway, "linking failed, try again")
		return
	}
	if !strings.EqualFold(identity.Login, actor.ExpectedGitHubLogin) {
		webErr(w, http.StatusForbidden, fmt.Sprintf(
			"you authorized GitHub as %s, but your Keycloak account says %s; "+
				"get the github_username attribute corrected", identity.Login, actor.ExpectedGitHubLogin))
		return
	}

	sealed, err := store.SealGitHubToken(s.tokenCipher, store.GitHubToken{
		AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, ExpiresAt: tok.Expiry,
	})
	if err != nil {
		s.log.Error("seal github token", "err", err)
		webErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := s.st.UpsertGitHubLink(r.Context(), store.GitHubLink{
		ActorID: actorID, GitHubUserID: identity.ID, GitHubLogin: identity.Login, Ciphertext: sealed,
	}); err != nil {
		s.webStoreErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oauthCookieName, Path: "/auth/", MaxAge: -1})
	http.Redirect(w, r, safeNext(stt.Next), http.StatusFound)
}
```

- [ ] **Step 4: Register the routes**

In `internal/api/server.go`, beside `/auth/login` and `/auth/callback`:

```go
	mux.HandleFunc("GET /auth/github/link", s.githubLinkStart)
	mux.HandleFunc("GET /auth/github/callback", s.githubLinkCallback)
```

Delete `TestGitHubLoginRoutesRemoved`'s `/auth/github/*` entries from `internal/api/oidcweb_test.go` (added in plan 1 Task 4), leaving only `/auth/choose` — those two routes exist again, as link routes.

- [ ] **Step 5: Record the follow-up**

Append to `docs/follow-ups.md` (the 409 mapping is deliberately out of scope
here):

```markdown
- **Duplicate GitHub link surfaces as 500.** `UpsertGitHubLink` hitting the
  `github_user_id` unique index (a second actor linking an already-linked
  GitHub account) reaches `webStoreErr` as a generic 500. Map it to a 409
  naming the conflict ("this GitHub account is already linked to another
  actor") if the case ever occurs outside admin error.
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/api/ -run 'TestLink|TestGitHubLoginRoutesRemoved' -v`
Expected: PASS (6 tests).

- [ ] **Step 7: Commit**

```bash
git add internal/api/githublink.go internal/api/githublink_test.go \
  internal/api/server.go internal/api/oidcweb_test.go docs/follow-ups.md
git commit -m "Add the GitHub account link flow"
```

---

### Task 5 — `/profile` — see and start a link

```yaml
kind: feature
priority: medium
blockedBy: [1, 4]
```

**Files:**
- Create: `internal/api/templates/profile.html`
- Modify: `internal/api/web.go`, `internal/api/server.go`, `internal/api/templates/layout.html`
- Test: `internal/api/githublink_test.go`

Spec 001 §9.3 makes linking lazy, with a settings entry for doing it proactively. This is that entry: one page, no form, no unlink button (§4 keeps unlink to the CLI/admin).

- [ ] **Step 1: Write the failing test**

Append to `internal/api/githublink_test.go`:

```go
func TestProfileShowsLinkState(t *testing.T) {
	st, s := linkServer(t, "stigsb", "stigsb")
	get := func() *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/profile", nil)
		req.AddCookie(&http.Cookie{
			Name:  sessionCookieName,
			Value: signSession(s.cfg.SessionSecret, "stig", s.st.Now().Add(time.Hour)),
		})
		s.profilePage(rr, req)
		return rr
	}

	rr := get()
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "/auth/github/link") {
		t.Fatalf("status=%d body=%q, want a Connect GitHub link", rr.Code, rr.Body)
	}

	if err := st.UpsertGitHubLink(context.Background(), store.GitHubLink{
		ActorID: "stig", GitHubUserID: 42, GitHubLogin: "stigsb", Ciphertext: []byte("ct"),
	}); err != nil {
		t.Fatalf("link: %v", err)
	}
	rr = get()
	if !strings.Contains(rr.Body.String(), "stigsb") {
		t.Fatalf("body = %q, want the linked login shown", rr.Body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestProfileShowsLinkState -v`
Expected: FAIL — `s.profilePage undefined`.

- [ ] **Step 3: Write the template**

Create `internal/api/templates/profile.html`, matching the structure of `board.html`:

```html
{{define "content"}}
<h1>{{.ActorID}}</h1>
<section>
  <h2>GitHub</h2>
  {{if eq .LinkState "linked"}}
    <p>Linked as <strong>{{.GitHubLogin}}</strong>.
       <a href="/auth/github/link">Re-link</a></p>
  {{else if eq .LinkState "broken"}}
    <p>The link to <strong>{{.GitHubLogin}}</strong> stopped working.
       <a href="/auth/github/link">Reconnect GitHub</a></p>
  {{else}}
    <p>Not linked. <a href="/auth/github/link">Connect GitHub</a></p>
  {{end}}
</section>
{{end}}
```

- [ ] **Step 4: Write the handler and the template plumbing**

There is **no generic render helper**: each page owns a pre-parsed
`*template.Template` field, built once in `NewServer` via `parseWebTemplates`
(one parse per page — see the comment on `parseWebTemplates` in
`internal/api/web.go:41`), and `layout.html` unconditionally reads `.Title`
and `.AutoRefresh`, so every view struct embeds `basePage`. Three pieces:

1. In `internal/api/server.go`, add the field beside `tmplProject` in the
   `server` struct and the initializer beside the other `tmpl*` entries in the
   `NewServer` composite literal (plan 1's deletions shifted this file's line
   numbers, so locate by name):

```go
	tmplProfile *template.Template
```
```go
		tmplProfile: parseWebTemplates("profile.html"),
```

2. In `linkServer` (`internal/api/githublink_test.go`, Task 4), add the same
   field to the `server` literal — the helper bypasses `NewServer`, so without
   it `profilePage` nil-panics in the test below:

```go
		tmplProfile: parseWebTemplates("profile.html"),
```

3. Add to `internal/api/web.go`:

```go
// profileView backs profile.html. LinkState is "unlinked", "linked", or
// "broken" — the same three states lode auth status reports.
type profileView struct {
	basePage
	ActorID     string
	LinkState   string
	GitHubLogin string
}

// profilePage handles GET /profile: the signed-in actor and its GitHub link
// state, with the link flow's entry point (spec 001 §9.3).
func (s *server) profilePage(w http.ResponseWriter, r *http.Request) {
	actorID, ok := s.sessionActor(r)
	if !ok {
		http.Redirect(w, r, s.loginTarget("/profile"), http.StatusFound)
		return
	}
	v := profileView{
		basePage:  basePage{Title: "worklode: " + actorID},
		ActorID:   actorID,
		LinkState: "unlinked",
	}
	link, err := s.st.GetGitHubLink(r.Context(), actorID)
	switch {
	case err == nil:
		v.LinkState = "linked"
		if link.Status == "broken" {
			v.LinkState = "broken"
		}
		v.GitHubLogin = link.GitHubLogin
	case errors.Is(err, store.ErrNotFound):
		// unlinked
	default:
		s.webStoreErr(w, err)
		return
	}
	if err := s.tmplProfile.ExecuteTemplate(w, "layout.html", v); err != nil {
		s.log.Error("render profile page", "err", err)
	}
}
```

Register the route in `internal/api/server.go` beside the other web pages:

```go
	mux.HandleFunc("GET /profile", s.profilePage)
```

Add a `<a href="/profile">profile</a>` entry to the `<header>` in `internal/api/templates/layout.html`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/... 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/templates/profile.html internal/api/templates/layout.html \
  internal/api/web.go internal/api/server.go internal/api/githublink_test.go
git commit -m "Add a profile page showing GitHub link state"
```

---

### Task 6 — Metrics for the link flow and token refresh

```yaml
kind: chore
priority: low
blockedBy: [2, 4]
```

**Files:**
- Modify: `internal/api/metrics.go`, `internal/api/server.go`, `internal/api/githublink.go`
- Test: `internal/api/metrics_internal_test.go`

CLAUDE.md requires `worklode_*` metrics for new outbound calls and store
operations with meaningful outcomes (spec 022 conventions: nil-safe observers
in the owning package's `metrics.go`, bounded label values). The registry is
already threaded from `serve.go` via `Config.Metrics` into `initMetrics`, so
**no `serve.go` change is needed**. Two counters, both with a bounded
`outcome` label:

- `worklode_github_link_attempts_total{outcome}` — `linked` | `refused` | `error`
- `worklode_github_token_refreshes_total{outcome}` — `ok` | `error`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/metrics_internal_test.go` (match `TestObserveSkillSync`):

```go
func TestLinkFlowMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := &server{}
	s.initMetrics(reg)

	s.observeLinkAttempt("linked")
	s.observeLinkAttempt("refused")
	s.observeLinkAttempt("refused")
	s.observeGHRefresh("error")

	for _, tc := range []struct {
		outcome string
		want    float64
	}{{"linked", 1}, {"refused", 2}, {"error", 0}} {
		if got := testutil.ToFloat64(s.linkAttempts.WithLabelValues(tc.outcome)); got != tc.want {
			t.Fatalf("linkAttempts{%s} = %v, want %v", tc.outcome, got, tc.want)
		}
	}
	if got := testutil.ToFloat64(s.ghRefreshes.WithLabelValues("error")); got != 1 {
		t.Fatalf("ghRefreshes{error} = %v, want 1", got)
	}

	// Nil-safe: the handler tests in this package build a *server without
	// initMetrics; the observers must be no-ops there, not panics.
	bare := &server{}
	bare.observeLinkAttempt("linked")
	bare.observeGHRefresh("ok")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestLinkFlowMetrics -v`
Expected: FAIL — `s.observeLinkAttempt undefined`.

- [ ] **Step 3: Write the implementation**

Add the fields to the `server` struct in `internal/api/server.go`, beside
`syncItems`:

```go
	linkAttempts *prometheus.CounterVec
	ghRefreshes  *prometheus.CounterVec
```

In `initMetrics` (`internal/api/metrics.go`), create, register, and
pre-initialise them like the skill-sync counters:

```go
	s.linkAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_github_link_attempts_total",
		Help: "GitHub account-link callback outcomes.",
	}, []string{"outcome"})
	s.ghRefreshes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_github_token_refreshes_total",
		Help: "Outbound GitHub refresh-token grants by outcome.",
	}, []string{"outcome"})
```

(add both to the `reg.MustRegister(...)` call, and pre-initialise
`linked`/`refused`/`error` and `ok`/`error` so alerts see 0, not no-data).

Add the nil-safe observers to `internal/api/metrics.go`:

```go
// observeLinkAttempt records one /auth/github/callback outcome: "linked",
// "refused" (strict-match or missing expectation), or "error" (exchange,
// identity fetch, seal, or store failure). Nil-safe: tests build a *server
// directly without initMetrics.
func (s *server) observeLinkAttempt(outcome string) {
	if s.linkAttempts == nil {
		return
	}
	s.linkAttempts.WithLabelValues(outcome).Inc()
}

// observeGHRefresh records one refresh-token grant against GitHub. Nil-safe.
func (s *server) observeGHRefresh(outcome string) {
	if s.ghRefreshes == nil {
		return
	}
	s.ghRefreshes.WithLabelValues(outcome).Inc()
}
```

- [ ] **Step 4: Wire the call sites**

In `internal/api/githublink.go`:

`ghRefresher` gains the observer, so the outbound grant is counted wherever
`UserToken` triggers it:

```go
// ghRefresher adapts githubauth.Client to store.TokenRefresher, keeping the
// store free of any GitHub import, and counts each grant.
type ghRefresher struct {
	c       *githubauth.Client
	observe func(outcome string)
}

func (g ghRefresher) Refresh(ctx context.Context, refreshToken string) (store.GitHubToken, error) {
	t, err := g.c.Refresh(ctx, refreshToken)
	if err != nil {
		g.observe("error")
		return store.GitHubToken{}, err
	}
	g.observe("ok")
	return store.GitHubToken{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, ExpiresAt: t.Expiry}, nil
}
```

and `userToken` constructs it as `ghRefresher{s.gh, s.observeGHRefresh}`.

In `githubLinkCallback`: `s.observeLinkAttempt("refused")` beside each 403
`webErr`, `s.observeLinkAttempt("error")` beside the 502/500/store-error
responses, and `s.observeLinkAttempt("linked")` right after the successful
`UpsertGitHubLink`. The 400 responses (missing/mismatched state, missing
code) are request noise, not link attempts — leave them uncounted.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/api/ -run 'TestLinkFlowMetrics|TestLink' -v`
Expected: PASS — the nil-safe observers keep Task 4's handler tests green.

- [ ] **Step 6: Commit**

```bash
git add internal/api/metrics.go internal/api/metrics_internal_test.go \
  internal/api/server.go internal/api/githublink.go
git commit -m "Count GitHub link attempts and token refreshes"
```

---

## Done when

A signed-in actor can link their GitHub account from `/profile`, mismatched or unasserted logins are refused with no row written, and `store.UserToken` hands out a valid access token — refreshing exactly once under concurrency and marking the link broken when the grant dies. Link attempts and refresh grants are counted in `worklode_*` metrics, and the duplicate-link 409 mapping is recorded in `docs/follow-ups.md`. Plan 3 (`2026-08-02-keycloak-primary-auth-3-cli-and-e2e.md`) puts the same flow behind `lode auth`.
