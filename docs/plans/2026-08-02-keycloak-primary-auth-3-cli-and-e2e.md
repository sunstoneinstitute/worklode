---
status: accepted
covers: docs/specs/023-keycloak-primary-auth.md
requires:
  - 2026-08-02-keycloak-primary-auth-2-link-and-tokens.md
---
# Keycloak-Primary Auth 3 — the `lode auth` command group

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement spec 023 §D and §6: `lode auth login` (with `lode login` kept as a hidden alias), `lode auth link github`, and `lode auth status`, plus the end-to-end test that drives OIDC login → link → status through public surfaces only.

**Architecture:** The CLI has no web session cookie, so a bearer-authed endpoint mints a short-lived, single-use **link nonce** bound to the calling actor and returns a link URL. `GET /auth/github/link` accepts that nonce as an alternative to the session cookie, and stamps the resolved actor plus the nonce into the already-signed oauth-state cookie — so the callback reads the actor from the state rather than from a session, and both entry points converge on one code path. The callback records the outcome on the nonce, which the CLI polls until linked, refused, or expired.

**Tech Stack:** Go 1.25+, cobra, net/http, in-memory nonce store (single-instance server, same as `cliCodeStore`), `e2e/` build tag.

**Read first:** `docs/specs/023-keycloak-primary-auth.md` §3.4, §5, §6, `internal/api/cliauth.go` (`cliCodeStore` — the pattern the nonce store copies), `internal/api/githublink.go` (plan 2), `internal/api/session.go:94-105` (`oauthState`), `internal/cli/login.go` (the loopback flow, unchanged), `e2e/smoke_test.go:120-165`.

**Prerequisite:** plans 1 and 2 are merged.

**Conventions:**
- Run `go test ./internal/...`; e2e needs `go test -race -count=1 -tags e2e ./e2e/`. Store and API tests need Postgres with pgvector (`TEST_POSTGRES_DSN` overrides the DSN).
- Commit after every task, imperative mood, no trailers.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/api/linknonce.go` (new) | `linkNonceStore` — mint, resolve, read |
| `internal/api/githublink.go` | Nonce-authenticated start, actor-from-state callback, the three API handlers |
| `internal/api/session.go` | `oauthState` carries `Actor` and `Nonce` |
| `internal/store/actors.go` | `TokenExpiry` for `lode auth status` |
| `internal/cli/client.go` | `StartGitHubLink`, `GitHubLinkStatus`, `AuthStatus` |
| `internal/cli/link.go` (new) | `RunGitHubLink` — open browser, poll to a terminal state |
| `internal/cmd/auth.go` (new) | `lode auth login|link github|status` |
| `internal/cmd/login.go` | `lode login` becomes a hidden alias |
| `e2e/authlink_test.go` (new) | login → link → status |

---

## Task 1: The link nonce

**Files:**
- Create: `internal/api/linknonce.go`, `internal/api/linknonce_test.go`
- Modify: `internal/api/server.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/linknonce_test.go`:

```go
package api

import (
	"testing"
	"time"
)

func TestLinkNonceMintResolveRead(t *testing.T) {
	now := time.Now()
	s := newLinkNonceStore(func() time.Time { return now })

	n, err := s.mint("stig")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if got, ok := s.actor(n); !ok || got != "stig" {
		t.Fatalf("actor = %q/%v, want stig/true", got, ok)
	}
	st, ok := s.get(n)
	if !ok || st.State != "pending" {
		t.Fatalf("state = %+v/%v, want pending", st, ok)
	}

	s.resolve(n, "linked", "stigsb")
	st, _ = s.get(n)
	if st.State != "linked" || st.Detail != "stigsb" {
		t.Fatalf("state = %+v, want linked/stigsb", st)
	}
	// A resolved nonce can no longer start a second browser flow.
	if _, ok := s.actor(n); ok {
		t.Fatal("actor() accepted an already-resolved nonce")
	}
}

func TestLinkNonceExpires(t *testing.T) {
	now := time.Now()
	s := newLinkNonceStore(func() time.Time { return now })
	n, _ := s.mint("stig")

	now = now.Add(linkNonceTTL + time.Second)
	if _, ok := s.actor(n); ok {
		t.Fatal("actor() accepted an expired nonce")
	}
	if _, ok := s.get(n); ok {
		t.Fatal("get() returned an expired nonce; the CLI must see it as gone")
	}

	// mint reaps expired entries, so the in-memory map cannot grow without
	// bound (resolved nonces age out on the same TTL).
	if _, err := s.mint("stig"); err != nil {
		t.Fatalf("second mint: %v", err)
	}
	s.mu.Lock()
	_, still := s.nonces[n]
	s.mu.Unlock()
	if still {
		t.Fatal("expired nonce not reaped on mint")
	}
}

func TestLinkNonceResolveIgnoresUnknown(t *testing.T) {
	s := newLinkNonceStore(time.Now)
	s.resolve("", "linked", "x")        // web flow with no nonce
	s.resolve("deadbeef", "linked", "x") // already reaped
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestLinkNonce -v`
Expected: FAIL — `undefined: newLinkNonceStore`.

- [ ] **Step 3: Write the implementation**

Create `internal/api/linknonce.go`:

```go
// linknonce.go binds a CLI-initiated GitHub link to the calling actor without
// a browser session (spec 023 §3.4). It mirrors cliCodeStore: in-memory,
// single-use, short-lived — the server is single-instance, so a restart simply
// drops pending nonces and the user re-runs the command.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// linkNonceTTL bounds the window between `lode auth link github` and the
// browser finishing the flow.
const linkNonceTTL = 5 * time.Minute

// linkNonce is one pending or resolved CLI link attempt. State is "pending",
// "linked", or "refused"; Detail carries the GitHub login on success and the
// refusal reason otherwise.
type linkNonce struct {
	ActorID string
	State   string
	Detail  string
	expires time.Time
}

type linkNonceStore struct {
	mu     sync.Mutex
	nonces map[string]linkNonce
	now    func() time.Time
}

func newLinkNonceStore(now func() time.Time) *linkNonceStore {
	return &linkNonceStore{nonces: map[string]linkNonce{}, now: now}
}

// mint issues a pending nonce bound to actorID. It reaps expired entries
// first — resolved nonces age out on the same TTL — so the map stays bounded
// by the number of links attempted in any 5-minute window.
func (s *linkNonceStore) mint(actorID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	n := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, v := range s.nonces {
		if now.After(v.expires) {
			delete(s.nonces, k)
		}
	}
	s.nonces[n] = linkNonce{ActorID: actorID, State: "pending", expires: now.Add(linkNonceTTL)}
	return n, nil
}

// actor returns the bound actor for a nonce that is still pending and unexpired
// — the check the browser entry point makes. A resolved nonce is refused, so
// one nonce drives at most one link.
func (s *linkNonceStore) actor(nonce string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nonces[nonce]
	if !ok || n.State != "pending" || s.now().After(n.expires) {
		return "", false
	}
	return n.ActorID, true
}

// resolve records a terminal outcome. Unknown or empty nonces are ignored: the
// same callback serves the web flow, which carries none.
func (s *linkNonceStore) resolve(nonce, state, detail string) {
	if nonce == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nonces[nonce]
	if !ok {
		return
	}
	n.State, n.Detail = state, detail
	s.nonces[nonce] = n
}

// get returns the nonce's current state, or false once it has expired — which
// the CLI reports as "expired, re-run to retry".
func (s *linkNonceStore) get(nonce string) (linkNonce, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nonces[nonce]
	if !ok || s.now().After(n.expires) {
		return linkNonce{}, false
	}
	return n, true
}
```

In `internal/api/server.go`, add the field beside `cliCodes` and initialise it the same way:

```go
	linkNonces *linkNonceStore
```
```go
	s.linkNonces = newLinkNonceStore(s.now)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/api/ -run TestLinkNonce -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/api/linknonce.go internal/api/linknonce_test.go internal/api/server.go
git commit -m "Add single-use nonces for CLI-initiated GitHub linking"
```

---

## Task 2: Nonce-authenticated link start and the API endpoints

**Files:**
- Modify: `internal/api/session.go`, `internal/api/githublink.go`, `internal/api/server.go`, `internal/store/actors.go`
- Test: `internal/api/githublink_test.go`

- [ ] **Step 1: Write the failing test**

First extend `linkServer` (plan 2, `internal/api/githublink_test.go`): the
nonce store did not exist when plan 2 ran, so add it to the `server` literal —

```go
		linkNonces: newLinkNonceStore(st.Now),
```

— without it, every nonce test below nil-panics in `mint`. Then append to the
same file:

```go
// bearerReq builds a bearer-authed request for actor "stig".
func bearerReq(t *testing.T, st *store.Store, method, path string) *http.Request {
	t.Helper()
	tok, err := st.CreateToken(context.Background(), "stig", "test", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	return req
}

func TestCLILinkStartMintsNonceURL(t *testing.T) {
	st, s := linkServer(t, "stigsb", "stigsb")
	rr := httptest.NewRecorder()
	s.auth(s.startCLILink).ServeHTTP(rr, bearerReq(t, st, "POST", "/api/v1/auth/github/link"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	var got struct {
		LinkURL string `json:"link_url"`
		Nonce   string `json:"nonce"`
	}
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Nonce == "" || !strings.HasPrefix(got.LinkURL, "https://wl.test/auth/github/link?nonce=") {
		t.Fatalf("response = %+v, want a nonce and an absolute link URL", got)
	}
	if actor, ok := s.linkNonces.actor(got.Nonce); !ok || actor != "stig" {
		t.Fatalf("nonce bound to %q/%v, want stig/true", actor, ok)
	}
}

// The whole CLI path: mint a nonce, drive the browser flow with it instead of a
// session cookie, then observe the resolved nonce.
func TestNonceLinkFlowResolvesNonce(t *testing.T) {
	_, s := linkServer(t, "stigsb", "stigsb")
	nonce, err := s.linkNonces.mint("stig")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	start := httptest.NewRecorder()
	s.githubLinkStart(start, httptest.NewRequest("GET", "/auth/github/link?nonce="+nonce, nil))
	if start.Code != http.StatusFound {
		t.Fatalf("start status = %d, want 302", start.Code)
	}
	loc, _ := url.Parse(start.Header().Get("Location"))

	cb := httptest.NewRequest("GET", "/auth/github/callback?code=c&state="+loc.Query().Get("state"), nil)
	for _, c := range start.Result().Cookies() {
		cb.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	s.githubLinkCallback(rr, cb)
	if rr.Code != http.StatusFound {
		t.Fatalf("callback status = %d, body = %s", rr.Code, rr.Body)
	}

	n, ok := s.linkNonces.get(nonce)
	if !ok || n.State != "linked" || n.Detail != "stigsb" {
		t.Fatalf("nonce = %+v/%v, want linked/stigsb", n, ok)
	}
}

func TestNonceLinkFlowRecordsRefusal(t *testing.T) {
	_, s := linkServer(t, "stigsb", "someone-else")
	nonce, _ := s.linkNonces.mint("stig")

	start := httptest.NewRecorder()
	s.githubLinkStart(start, httptest.NewRequest("GET", "/auth/github/link?nonce="+nonce, nil))
	loc, _ := url.Parse(start.Header().Get("Location"))
	cb := httptest.NewRequest("GET", "/auth/github/callback?code=c&state="+loc.Query().Get("state"), nil)
	for _, c := range start.Result().Cookies() {
		cb.AddCookie(c)
	}
	s.githubLinkCallback(httptest.NewRecorder(), cb)

	n, _ := s.linkNonces.get(nonce)
	if n.State != "refused" || !strings.Contains(n.Detail, "someone-else") {
		t.Fatalf("nonce = %+v, want refused naming the wrong login", n)
	}
}

func TestAuthStatusReportsLinkState(t *testing.T) {
	st, s := linkServer(t, "stigsb", "stigsb")
	call := func() map[string]any {
		rr := httptest.NewRecorder()
		s.auth(s.authStatus).ServeHTTP(rr, bearerReq(t, st, "GET", "/api/v1/auth/status"))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
		}
		var out map[string]any
		json.Unmarshal(rr.Body.Bytes(), &out)
		return out
	}

	got := call()
	if got["actor_id"] != "stig" || got["github_link_state"] != "unlinked" {
		t.Fatalf("status = %+v, want stig/unlinked", got)
	}

	if err := st.UpsertGitHubLink(context.Background(), store.GitHubLink{
		ActorID: "stig", GitHubUserID: 42, GitHubLogin: "stigsb", Ciphertext: []byte("ct"),
	}); err != nil {
		t.Fatalf("link: %v", err)
	}
	got = call()
	if got["github_link_state"] != "linked" || got["github_login"] != "stigsb" {
		t.Fatalf("status = %+v, want linked/stigsb", got)
	}

	if err := st.MarkGitHubLinkBroken(context.Background(), "stig"); err != nil {
		t.Fatalf("mark broken: %v", err)
	}
	if got = call(); got["github_link_state"] != "broken" {
		t.Fatalf("status = %+v, want broken", got)
	}
}

func TestUnlinkGitHubDeletesTheRow(t *testing.T) {
	st, s := linkServer(t, "stigsb", "stigsb")
	if err := st.UpsertGitHubLink(context.Background(), store.GitHubLink{
		ActorID: "stig", GitHubUserID: 42, GitHubLogin: "stigsb", Ciphertext: []byte("ct"),
	}); err != nil {
		t.Fatalf("link: %v", err)
	}

	del := func() int {
		rr := httptest.NewRecorder()
		s.auth(s.unlinkGitHub).ServeHTTP(rr, bearerReq(t, st, "DELETE", "/api/v1/auth/github/link"))
		return rr.Code
	}
	if code := del(); code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", code)
	}
	if _, err := st.GetGitHubLink(context.Background(), "stig"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get after unlink: err = %v, want ErrNotFound", err)
	}
	// Idempotent: unlinking twice is not an error.
	if code := del(); code != http.StatusNoContent {
		t.Fatalf("second unlink status = %d, want 204", code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run 'TestCLILink|TestNonceLink|TestAuthStatus' -v`
Expected: FAIL — `s.startCLILink undefined`.

- [ ] **Step 3: Carry the actor in the signed state**

In `internal/api/session.go`, extend `oauthState`:

```go
type oauthState struct {
	State    string `json:"s"`
	Verifier string `json:"v"`
	Next     string `json:"n"`
	Exp      int64  `json:"e"`
	// Actor and Nonce are set by the GitHub link flow only: the browser may
	// carry no worklode session (CLI-initiated), so the signed state is what
	// binds the callback to an actor and to the nonce awaiting its outcome.
	Actor string `json:"a,omitempty"`
	Nonce string `json:"ln,omitempty"`
}
```

- [ ] **Step 4: Accept a nonce and read the actor from the state**

In `internal/api/githublink.go`, replace the session lookup in `githubLinkStart`:

```go
	// Either entry point identifies the actor: a browser session (web) or a
	// single-use nonce minted for the CLI.
	actorID, ok := s.sessionActor(r)
	nonce := r.URL.Query().Get("nonce")
	if nonce != "" {
		actorID, ok = s.linkNonces.actor(nonce)
		if !ok {
			webErr(w, http.StatusBadRequest, "link request expired or already used; re-run lode auth link github")
			return
		}
	}
	if !ok {
		http.Redirect(w, r, s.loginTarget("/auth/github/link"), http.StatusFound)
		return
	}
```

and put both into the state cookie:

```go
		Value: signOAuthState(s.cfg.SessionSecret, oauthState{
			State: state, Next: "/profile", Actor: actorID, Nonce: nonce,
			Exp: now.Add(oauthStateMaxAge).Unix(),
		}),
```

In `githubLinkCallback`, take the actor from the verified state instead of the session — delete the `sessionActor` block and, right after the state/`code` checks, use:

```go
	actorID := stt.Actor
	if actorID == "" {
		webErr(w, http.StatusBadRequest, "link state carries no actor")
		return
	}
```

Record the outcome on every terminal path. Replace each refusal `webErr` in the callback with a `refuse` call — it absorbs the `s.observeLinkAttempt("refused")` metric call plan 2 Task 6 put beside those `webErr`s, so the counter keeps counting:

```go
	// refuse records the reason on the CLI's nonce (a no-op for the web flow),
	// counts the refusal, and shows the reason in the browser.
	refuse := func(msg string) {
		s.observeLinkAttempt("refused")
		s.linkNonces.resolve(stt.Nonce, "refused", msg)
		webErr(w, http.StatusForbidden, msg)
	}
```

- missing expectation → `refuse("your Keycloak account has no github_username attribute; get it set before linking")`
- login mismatch → `refuse(fmt.Sprintf("you authorized GitHub as %s, but your Keycloak account says %s; get the github_username attribute corrected", identity.Login, actor.ExpectedGitHubLogin))`
- after `UpsertGitHubLink` succeeds → `s.linkNonces.resolve(stt.Nonce, "linked", identity.Login)` (keep the existing `s.observeLinkAttempt("linked")` beside it)

- [ ] **Step 5: Add the three API handlers**

Append to `internal/api/githublink.go`:

```go
// startCLILink handles POST /api/v1/auth/github/link: mint a nonce bound to
// the calling actor and hand back the URL to open in a browser.
func (s *server) startCLILink(w http.ResponseWriter, r *http.Request) {
	if s.gh == nil {
		writeErr(w, http.StatusServiceUnavailable, "github app not configured")
		return
	}
	actor := actorFrom(r)
	nonce, err := s.linkNonces.mint(actor.ID)
	if err != nil {
		s.log.Error("mint link nonce", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nonce":      nonce,
		"link_url":   strings.TrimRight(s.cfg.PublicURL, "/") + "/auth/github/link?nonce=" + nonce,
		"expires_in": int(linkNonceTTL.Seconds()),
	})
}

// cliLinkStatus handles GET /api/v1/auth/github/link/{nonce}: the CLI's poll.
// A nonce belonging to another actor is reported as unknown rather than
// denied, so it leaks nothing about other people's pending links.
func (s *server) cliLinkStatus(w http.ResponseWriter, r *http.Request) {
	actor := actorFrom(r)
	n, ok := s.linkNonces.get(r.PathValue("nonce"))
	if !ok || n.ActorID != actor.ID {
		writeJSON(w, http.StatusOK, map[string]any{"state": "expired"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": n.State, "detail": n.Detail})
}

// unlinkGitHub handles DELETE /api/v1/auth/github/link: forget the calling
// actor's link and its stored token. Unlink is a row delete (spec 023 §3.5)
// and is idempotent, so an already-unlinked actor still gets 204.
func (s *server) unlinkGitHub(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteGitHubLink(r.Context(), actorFrom(r).ID); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// authStatus handles GET /api/v1/auth/status: who the caller is, when the
// token expires, and the GitHub link state (spec 023 §3.4).
func (s *server) authStatus(w http.ResponseWriter, r *http.Request) {
	actor := actorFrom(r)
	out := map[string]any{
		"actor_id":          actor.ID,
		"display_name":      actor.DisplayName,
		"admin":             actor.Admin,
		"github_link_state": "unlinked",
	}
	if exp, err := s.st.TokenExpiry(r.Context(), bearerToken(r)); err == nil && exp != nil {
		out["token_expires_at"] = exp.UTC()
	}
	link, err := s.st.GetGitHubLink(r.Context(), actor.ID)
	switch {
	case err == nil:
		out["github_link_state"] = "linked"
		if link.Status == "broken" {
			out["github_link_state"] = "broken"
		}
		out["github_login"] = link.GitHubLogin
	case errors.Is(err, store.ErrNotFound):
		// unlinked
	default:
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
```

`bearerToken(r)` extracts the credential from the `Authorization` header. Today that logic is inline in `s.auth` (`internal/api/server.go`, the `strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")` line — line numbers have shifted since plan 1, so locate by content): factor it out into `func bearerToken(r *http.Request) string` and call it from both places.

Register the routes in `internal/api/server.go`, beside the other `/api/v1` routes:

```go
	mux.Handle("POST /api/v1/auth/github/link", s.auth(s.startCLILink))
	mux.Handle("GET /api/v1/auth/github/link/{nonce}", s.auth(s.cliLinkStatus))
	mux.Handle("DELETE /api/v1/auth/github/link", s.auth(s.unlinkGitHub))
	mux.Handle("GET /api/v1/auth/status", s.auth(s.authStatus))
```

- [ ] **Step 6: Add `store.TokenExpiry`**

In `internal/store/actors.go`, after `Authenticate`:

```go
// TokenExpiry returns when a bearer token expires, or nil for a token with no
// expiry. Unknown or revoked tokens return ErrNotFound. `lode auth status`
// uses it to show how long the current login lasts.
func (s *Store) TokenExpiry(ctx context.Context, plaintextOrHash string) (*time.Time, error) {
	var revokedAt, expiresAt sql.NullTime
	row := s.db.QueryRowContext(ctx,
		`SELECT revoked_at, expires_at FROM tokens WHERE token_hash = $1`,
		tokenHashOf(plaintextOrHash))
	if err := row.Scan(&revokedAt, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("look up token expiry: %w", err)
	}
	if revokedAt.Valid {
		return nil, ErrNotFound
	}
	if !expiresAt.Valid {
		return nil, nil
	}
	t := expiresAt.Time
	return &t, nil
}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/api/ ./internal/store/ -run 'Link|Unlink|AuthStatus|TokenExpiry' -v 2>&1 | tail -20`
Expected: PASS. Plan 2's `TestLinkSucceedsWhenLoginMatches` and friends still pass: the `callback` helper's session cookie now only satisfies `githubLinkStart`, and the actor travels in the state.

- [ ] **Step 8: Commit**

```bash
git add internal/api/githublink.go internal/api/githublink_test.go internal/api/session.go \
  internal/api/server.go internal/store/actors.go
git commit -m "Let the CLI start a GitHub link with a bound nonce"
```

---

## Task 3: CLI client and polling

**Files:**
- Modify: `internal/cli/client.go`
- Create: `internal/cli/link.go`, `internal/cli/link_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cli/link_test.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// linkAPI serves the two link endpoints, returning "pending" until the
// browser callback has been "opened" polls times.
func linkAPI(t *testing.T, states []string) (*Client, *string) {
	t.Helper()
	var opened string
	i := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/auth/github/link":
			json.NewEncoder(w).Encode(map[string]any{
				"nonce": "n1", "link_url": "https://wl.test/auth/github/link?nonce=n1", "expires_in": 300,
			})
		case strings.HasPrefix(r.URL.Path, "/api/v1/auth/github/link/"):
			s := states[i]
			if i < len(states)-1 {
				i++
			}
			detail := ""
			if s == "linked" {
				detail = "stigsb"
			} else if s == "refused" {
				detail = "you authorized GitHub as someone-else"
			}
			json.NewEncoder(w).Encode(map[string]any{"state": s, "detail": detail})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := NewClient(Config{ServerURL: srv.URL, Token: "wl_" + strings.Repeat("a", 40)})
	return c, &opened
}

func TestRunGitHubLinkPollsUntilLinked(t *testing.T) {
	c, opened := linkAPI(t, []string{"pending", "pending", "linked"})
	login, err := RunGitHubLink(context.Background(), c, LinkOptions{
		PollInterval: time.Millisecond,
		OpenBrowser:  func(u string) error { *opened = u; return nil },
	})
	if err != nil {
		t.Fatalf("RunGitHubLink: %v", err)
	}
	if login != "stigsb" {
		t.Fatalf("login = %q, want stigsb", login)
	}
	if *opened != "https://wl.test/auth/github/link?nonce=n1" {
		t.Fatalf("opened %q, want the minted link URL", *opened)
	}
}

func TestRunGitHubLinkReportsRefusal(t *testing.T) {
	c, _ := linkAPI(t, []string{"refused"})
	_, err := RunGitHubLink(context.Background(), c, LinkOptions{
		PollInterval: time.Millisecond,
		OpenBrowser:  func(string) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "someone-else") {
		t.Fatalf("err = %v, want the server's refusal reason", err)
	}
}

func TestRunGitHubLinkReportsExpiry(t *testing.T) {
	c, _ := linkAPI(t, []string{"expired"})
	_, err := RunGitHubLink(context.Background(), c, LinkOptions{
		PollInterval: time.Millisecond,
		OpenBrowser:  func(string) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err = %v, want an expiry error naming the retry", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestRunGitHubLink -v`
Expected: FAIL — `undefined: RunGitHubLink`.

- [ ] **Step 3: Add the client methods**

In `internal/cli/client.go`, after the other auth-related methods:

```go
// LinkStart is the response from StartGitHubLink.
type LinkStart struct {
	Nonce     string `json:"nonce"`
	LinkURL   string `json:"link_url"`
	ExpiresIn int    `json:"expires_in"`
}

// LinkStatus is one poll of a pending link. State is "pending", "linked",
// "refused", or "expired"; Detail carries the GitHub login or the reason.
type LinkStatus struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// AuthStatus is the response from GET /api/v1/auth/status.
type AuthStatus struct {
	ActorID         string     `json:"actor_id"`
	DisplayName     string     `json:"display_name"`
	Admin           bool       `json:"admin"`
	TokenExpiresAt  *time.Time `json:"token_expires_at"`
	GitHubLinkState string     `json:"github_link_state"`
	GitHubLogin     string     `json:"github_login"`
}

// StartGitHubLink calls POST /api/v1/auth/github/link.
func (c *Client) StartGitHubLink(ctx context.Context) (LinkStart, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/auth/github/link", nil)
	if err != nil {
		return LinkStart{}, err
	}
	var out LinkStart
	if err := json.Unmarshal(raw, &out); err != nil {
		return LinkStart{}, fmt.Errorf("decode link start: %w", err)
	}
	return out, nil
}

// GitHubLinkStatus polls one pending link.
func (c *Client) GitHubLinkStatus(ctx context.Context, nonce string) (LinkStatus, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/auth/github/link/"+nonce, nil)
	if err != nil {
		return LinkStatus{}, err
	}
	var out LinkStatus
	if err := json.Unmarshal(raw, &out); err != nil {
		return LinkStatus{}, fmt.Errorf("decode link status: %w", err)
	}
	return out, nil
}

// UnlinkGitHub calls DELETE /api/v1/auth/github/link. It is idempotent.
func (c *Client) UnlinkGitHub(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodDelete, "/api/v1/auth/github/link", nil)
	return err
}

// AuthStatus calls GET /api/v1/auth/status.
func (c *Client) AuthStatus(ctx context.Context) (AuthStatus, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/auth/status", nil)
	if err != nil {
		return AuthStatus{}, nil, err
	}
	var out AuthStatus
	if err := json.Unmarshal(raw, &out); err != nil {
		return AuthStatus{}, nil, fmt.Errorf("decode auth status: %w", err)
	}
	return out, raw, nil
}
```

Add `"time"` to the file's imports if absent.

- [ ] **Step 4: Write the polling loop**

Create `internal/cli/link.go`:

```go
// link.go drives `lode auth link github`: start a nonce-bound link, open the
// browser, and poll until the server reports a terminal outcome (spec 023
// §3.4). The CLI speaks no GitHub protocol — the server does the whole flow.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// LinkOptions configures RunGitHubLink. Zero values use the defaults.
type LinkOptions struct {
	PollInterval time.Duration
	OpenBrowser  func(string) error
	// Out receives the fallback "open this URL" message when the browser
	// cannot be opened. Defaults to os.Stdout; commands pass their own writer.
	Out io.Writer
}

// RunGitHubLink returns the linked GitHub login, or an error carrying the
// server's own refusal text so the user learns which attribute to fix.
func RunGitHubLink(ctx context.Context, c *Client, opts LinkOptions) (string, error) {
	if opts.PollInterval == 0 {
		opts.PollInterval = time.Second
	}
	if opts.OpenBrowser == nil {
		opts.OpenBrowser = openBrowser
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}

	start, err := c.StartGitHubLink(ctx)
	if err != nil {
		return "", err
	}
	if err := opts.OpenBrowser(start.LinkURL); err != nil {
		fmt.Fprintf(opts.Out, "open this URL to link GitHub:\n\n  %s\n\n", start.LinkURL)
	}

	tick := time.NewTicker(opts.PollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-tick.C:
			st, err := c.GitHubLinkStatus(ctx, start.Nonce)
			if err != nil {
				return "", err
			}
			switch st.State {
			case "pending":
				continue
			case "linked":
				return st.Detail, nil
			case "refused":
				return "", errors.New(st.Detail)
			default:
				return "", errors.New("link request expired; re-run lode auth link github")
			}
		}
	}
}
```

`openBrowser` already exists in `internal/cli/login.go`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run TestRunGitHubLink -v`
Expected: PASS (3 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/cli/client.go internal/cli/link.go internal/cli/link_test.go
git commit -m "Add the CLI GitHub link and auth status calls"
```

---

## Task 4: `lode auth`

**Files:**
- Create: `internal/cmd/auth.go`, `internal/cmd/auth_test.go`
- Modify: `internal/cmd/login.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cmd/auth_test.go`, matching how the neighbouring command tests execute a cobra command:

```go
package cmd

import (
	"strings"
	"testing"
)

func TestAuthCommandTree(t *testing.T) {
	auth := newAuthCmd()
	want := map[string]bool{"login": false, "link": false, "unlink": false, "status": false}
	for _, c := range auth.Commands() {
		want[c.Name()] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("lode auth has no %q subcommand", name)
		}
	}
	for _, c := range auth.Commands() {
		if c.Name() != "link" {
			continue
		}
		if len(c.Commands()) != 1 || c.Commands()[0].Name() != "github" {
			t.Errorf("lode auth link should have exactly one subcommand, github")
		}
	}
}

// `lode login` keeps working for muscle memory and scripts, but is hidden so
// help output shows a single spelling.
func TestLoginAliasIsHidden(t *testing.T) {
	c := newLoginCmd("login", true)
	if !c.Hidden {
		t.Error("lode login should be hidden")
	}
	if !strings.Contains(c.Long, "lode auth login") {
		t.Error("the alias's help should point at lode auth login")
	}
}

// Spec 023 §6: `auth status` renders all three link states. The fake server
// stands in for the API; runLode (lifecycle_test.go) drives the real command.
func TestAuthStatusRendersLinkStates(t *testing.T) {
	for _, tc := range []struct {
		state, login, want string
	}{
		{"unlinked", "", "not linked"},
		{"linked", "stigsb", "linked as stigsb"},
		{"broken", "stigsb", "broken (stigsb)"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/auth/status" {
				http.NotFound(w, r)
				return
			}
			resp := map[string]any{"actor_id": "stig", "github_link_state": tc.state}
			if tc.login != "" {
				resp["github_login"] = tc.login
			}
			json.NewEncoder(w).Encode(resp)
		}))
		t.Setenv("LODE_SERVER", srv.URL)
		t.Setenv("LODE_TOKEN", "wl_"+strings.Repeat("a", 40))
		out, err := runLode(t, "auth", "status")
		srv.Close()
		if err != nil {
			t.Fatalf("%s: lode auth status: %v\noutput: %s", tc.state, err, out)
		}
		if !strings.Contains(out, tc.want) {
			t.Fatalf("%s: output %q, want it to contain %q", tc.state, out, tc.want)
		}
	}
}
```

Add `encoding/json`, `net/http`, and `net/http/httptest` to the test file's
imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/ -run 'TestAuthCommandTree|TestLoginAliasIsHidden' -v`
Expected: FAIL — `undefined: newAuthCmd`; `newLoginCmd` takes no arguments.

- [ ] **Step 3: Make `newLoginCmd` reusable**

In `internal/cmd/login.go`, take the spelling as parameters and drop the direct root registration:

```go
// newLoginCmd builds the browser login command. It is registered twice: as
// `lode auth login`, and as a hidden `lode login` alias for muscle memory.
func newLoginCmd(use string, hidden bool) *cobra.Command {
	var server string
	long := "Open a browser to sign in with Keycloak, then store the resulting\n" +
		"30-day token in the OS keychain. Re-run after it expires."
	if hidden {
		long += "\n\nAlias for `lode auth login`."
	}
	cmd := &cobra.Command{
		Use:    use,
		Short:  "Authenticate to worklode and store a token",
		Long:   long,
		Hidden: hidden,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// unchanged body
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "worklode server URL (overrides LODE_SERVER / config file)")
	return cmd
}

func init() {
	rootCmd.AddCommand(newLoginCmd("login", true))
}
```

Keep the existing `RunE` body verbatim.

- [ ] **Step 4: Write the command group**

Create `internal/cmd/auth.go`:

```go
// auth.go is the `lode auth` command group (spec 023 §3.4): sign in, link a
// GitHub account, and report both states.
package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authentication and account linking",
	}
	cmd.AddCommand(newLoginCmd("login", false), newAuthLinkCmd(), newAuthUnlinkCmd(), newAuthStatusCmd())
	return cmd
}

func newAuthUnlinkCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "unlink", Short: "Remove an external account link"}
	cmd.AddCommand(&cobra.Command{
		Use:   "github",
		Short: "Forget your GitHub link and its stored token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			if err := c.UnlinkGitHub(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "GitHub link removed")
			return nil
		},
	})
	return cmd
}

func newAuthLinkCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "link", Short: "Link an external account"}
	cmd.AddCommand(newAuthLinkGitHubCmd())
	return cmd
}

func newAuthLinkGitHubCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "github",
		Short: "Link your GitHub account to this worklode identity",
		Long: "Opens a browser to authorize the worklode GitHub App. The link is\n" +
			"refused unless the GitHub account matches the github_username your\n" +
			"Keycloak account asserts.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			login, err := cli.RunGitHubLink(ctx, c, cli.LinkOptions{Out: cmd.OutOrStdout()})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "linked GitHub account %s\n", login)
			return nil
		},
	}
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the current identity, token expiry, and GitHub link state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			st, raw, err := c.AuthStatus(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "actor:  %s\n", st.ActorID)
			if st.TokenExpiresAt != nil {
				fmt.Fprintf(out, "token:  expires %s\n", st.TokenExpiresAt.Format("2006-01-02 15:04 MST"))
			} else {
				fmt.Fprintf(out, "token:  no expiry\n")
			}
			switch st.GitHubLinkState {
			case "linked":
				fmt.Fprintf(out, "github: linked as %s\n", st.GitHubLogin)
			case "broken":
				fmt.Fprintf(out, "github: broken (%s) — run: lode auth link github\n", st.GitHubLogin)
			default:
				fmt.Fprintf(out, "github: not linked — run: lode auth link github\n")
			}
			return nil
		},
	}
}

func init() {
	rootCmd.AddCommand(newAuthCmd())
}
```

`newAPIClient`, `jsonOut`, and `printRaw` already exist in
`internal/cmd/root.go` — use them as-is.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/cmd/ -v 2>&1 | tail -20`
Expected: PASS. Then check the tree by hand: `go run ./cmd/lode auth --help` lists `login`, `link`, `unlink`, `status`; `go run ./cmd/lode --help` does not list `login`.

- [ ] **Step 6: Commit**

```bash
git add internal/cmd/auth.go internal/cmd/auth_test.go internal/cmd/login.go
git commit -m "Add the lode auth command group"
```

---

## Task 5: End-to-end

**Files:**
- Create: `e2e/authlink_test.go`

- [ ] **Step 1: Write the test**

Create `e2e/authlink_test.go`:

```go
//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/oidc/oidctest"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestAuthLinkFlow: Keycloak login mints a wl_ token, the CLI starts a GitHub
// link, the browser completes it against a fake GitHub, and auth status
// reports the link. Public surfaces only — no direct store writes.
func TestAuthLinkFlow(t *testing.T) {
	ctx := context.Background()
	st := store.OpenTestStore(t)
	iss := oidctest.NewIssuer(t)

	// A fake GitHub whose authorize endpoint bounces straight back to the
	// worklode callback, standing in for the user clicking "Authorize".
	var callbackBase string
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			http.Redirect(w, r, callbackBase+"?code=e2e-code&state="+
				url.QueryEscape(r.URL.Query().Get("state")), http.StatusFound)
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"gho_e2e","refresh_token":"ghr_e2e",` +
				`"token_type":"bearer","expires_in":28800}`))
		case "/user":
			json.NewEncoder(w).Encode(map[string]any{"id": 4242, "login": "stigsb"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer gh.Close()

	// PublicURL must be known before NewServer, so bind the listener first.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	public := "http://" + ln.Addr().String()
	callbackBase = public + "/auth/github/callback"

	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken:     bootstrapToken,
		OIDCIssuer:         iss.URL(),
		OIDCClientID:       iss.ClientID,
		PublicURL:          public,
		SessionSecret:      "e2e-session-secret",
		GitHubClientID:     "cid",
		GitHubClientSecret: "csecret",
		TokenEncKey:        strings.Repeat("2a", 32),
		GitHubAPIBase:      gh.URL,
		GitHubOAuthAuthURL: gh.URL + "/authorize",
		GitHubOAuthTokenURL: gh.URL + "/token",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := &httptest.Server{Listener: ln, Config: &http.Server{Handler: handler}}
	srv.Start()
	defer srv.Close()

	// 1. Sign in with Keycloak, asserting the GitHub username.
	idToken := iss.SignToken(t, map[string]any{
		"preferred_username": "stig",
		"name":               "Stig",
		"groups":             []string{"user"},
		"github_username":    "stigsb",
	})
	body := strings.NewReader(`{"id_token":"` + idToken + `"}`)
	resp, err := http.Post(srv.URL+"/auth/oidc/token", "application/json", body)
	if err != nil {
		t.Fatalf("oidc token exchange: %v", err)
	}
	defer resp.Body.Close()
	var minted struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&minted); err != nil || minted.Token == "" {
		t.Fatalf("no token minted: %v (status %d)", err, resp.StatusCode)
	}

	// 2. The CLI starts a link and "opens" the URL in an HTTP client that
	//    follows the redirect chain through the fake GitHub.
	c := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: minted.Token})
	start, err := c.StartGitHubLink(ctx)
	if err != nil {
		t.Fatalf("start link: %v", err)
	}
	browser := &http.Client{Jar: newJar(t)}
	linkURL := strings.Replace(start.LinkURL, public, srv.URL, 1)
	if _, err := browser.Get(linkURL); err != nil {
		t.Fatalf("browser flow: %v", err)
	}

	// 3. The nonce is resolved and auth status reports the link.
	got, err := c.GitHubLinkStatus(ctx, start.Nonce)
	if err != nil {
		t.Fatalf("link status: %v", err)
	}
	if got.State != "linked" || got.Detail != "stigsb" {
		t.Fatalf("link status = %+v, want linked/stigsb", got)
	}
	status, _, err := c.AuthStatus(ctx)
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if status.ActorID != "stig" || status.GitHubLinkState != "linked" || status.GitHubLogin != "stigsb" {
		t.Fatalf("auth status = %+v, want stig linked as stigsb", status)
	}
}
```

`newJar(t)` returns a `*cookiejar.Jar` (`net/http/cookiejar`, `cookiejar.New(nil)`) so the oauth-state cookie survives the redirect hop; add it as a small helper in this file, with this comment — the behavior it names is load-bearing:

```go
// newJar returns the fake browser's cookie jar. The oauth-state cookie is set
// with Secure over this plain-http test server; that works because Go's
// cookiejar (go.mod pins 1.26) treats localhost as a secure origin. On older
// toolchains this test fails with "missing link state".
```

Remove the unused `githubauth`/`oauth2` imports if the config-driven wiring in Step 2 makes them unnecessary.

- [ ] **Step 2: Make the GitHub endpoints configurable**

The test needs the server's GitHub client pointed at the fake. Add three optional fields to `api.Config` in `internal/api/server.go`, documented as test/staging seams beside the existing GitHub config:

```go
	// GitHubAPIBase, GitHubOAuthAuthURL, and GitHubOAuthTokenURL override the
	// public GitHub endpoints. Empty in production; set by e2e tests to point
	// the link flow at a fake.
	GitHubAPIBase       string
	GitHubOAuthAuthURL  string
	GitHubOAuthTokenURL string
```

and apply them where `s.gh` is built:

```go
		s.gh = githubauth.New(cfg.GitHubClientID, cfg.GitHubClientSecret)
		if cfg.GitHubAPIBase != "" {
			s.gh.APIBase = cfg.GitHubAPIBase
		}
		if cfg.GitHubOAuthAuthURL != "" && cfg.GitHubOAuthTokenURL != "" {
			s.gh.Endpoint = oauth2.Endpoint{
				AuthURL: cfg.GitHubOAuthAuthURL, TokenURL: cfg.GitHubOAuthTokenURL,
			}
		}
```

Add `"golang.org/x/oauth2"` to `server.go`'s imports. Do **not** wire these to environment variables in `internal/cmd/serve.go` — they exist for tests.

- [ ] **Step 3: Run the e2e suite**

Run: `go test -race -count=1 -tags e2e ./e2e/ -run TestAuthLinkFlow -v`
Expected: PASS.

- [ ] **Step 4: Run everything**

Run: `go test ./internal/... && go test -race -count=1 -tags e2e ./e2e/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add e2e/authlink_test.go internal/api/server.go
git commit -m "Cover Keycloak login through GitHub linking end to end"
```

---

## Task 6: Close out the docs

**Files:**
- Modify: `docs/specs/001-keycloak-sso.md` (one stale note), `docs/follow-ups.md`, `README.md` (if it documents `lode login` or the removed env vars)

- [ ] **Step 1: Update user-facing docs**

`grep -rn "lode login\|LODE_GITHUB_ORG\|LODE_GITHUB_ADMIN_TEAM\|Sign in with GitHub" README.md docs/ --include=*.md | grep -v docs/specs/ | grep -v docs/plans/` and fix each hit: `lode login` → `lode auth login`, and delete the two removed env vars from any config table.

- [ ] **Step 2: Bring 001 §3.2's amendment note up to date**

The inline note on `docs/specs/001-keycloak-sso.md` §3.2 still describes the
`/auth/choose` chooser that 002 added and this work removed. Append one
sentence to that existing blockquote (a clarifying note edit — 023's
frontmatter already supersedes 002 §3.2, so no new `amends`/`amendedBy` edge
and no frontmatter change):

```markdown
> **Amended by 002 §B.** When both providers are configured the session flow starts at `/auth/choose`; `/auth/login` and `/auth/callback` remain the Keycloak path. Spec 023 §3.1 has since removed the chooser and the GitHub provider: `/auth/login` is once more the sole entry.
```

- [ ] **Step 3: Record the deferred spec-§6 item**

Append to `docs/follow-ups.md` — no admin diagnostics surface exists yet, so
implementing one here would be a feature of its own:

```markdown
- **Admin diagnostics for GitHub links (spec 023 §6).** The testing section's
  "admin diagnostics report link and token validity" e2e item is deferred: no
  admin diagnostics surface exists. When one appears, it should report each
  actor's link state and whether the stored pair still refreshes
  (`store.UserToken` without side effects, or a dry-run variant).
```

- [ ] **Step 4: Verify**

Run: `./scripts/secfmt.py -l && go test ./internal/...`
Expected: no numbering complaints, tests PASS.

- [ ] **Step 5: Commit**

```bash
git add docs/specs/001-keycloak-sso.md docs/follow-ups.md README.md docs/
git commit -m "Update docs for Keycloak-primary auth"
```

- [ ] **Step 6: Stop — spec acceptance is the human's call**

Do **not** flip `docs/specs/023-keycloak-primary-auth.md` to
`status: accepted` yourself. Acceptance is an explicit human act (and
"implemented?" is a coverage query, not a doc status). Report that spec 023's
code obligations are complete — §3.6 remains ops-side, recorded in
`docs/follow-ups.md` by plan 1 — and ask the operator to review and flip the
status, which freezes the section anchors.

---

## Done when

`lode auth login`, `lode auth link github`, and `lode auth status` all work against a real server; `auth status` renders all three link states under test; `lode login` still works but is hidden; the e2e test proves Keycloak login → GitHub link → status through public surfaces. Spec 023's remaining obligations are ops-side (§3.6, recorded in `docs/follow-ups.md` by plan 1) plus the deferred admin diagnostics — and flipping the spec to `accepted`, which stays with the human.
