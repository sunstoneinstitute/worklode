# Provider-Neutral `wl login` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `wl login` provider-neutral: it discovers the server's auth setup, drives whichever web login the server has (Keycloak / GitHub / chooser) via a server-mediated localhost loopback, and stores the resulting 30-day `wl_` token in the OS keychain instead of cleartext.

**Architecture:** All provider logic stays server-side, reusing the existing web login flows. The CLI opens a browser to a new `/auth/cli/login` endpoint with an ephemeral-port loopback `redirect_uri`; the server runs its normal web flow, mints a short-lived one-time code in `finishLogin`, and 302s it to the loopback; the CLI exchanges the code at `/auth/cli/token` for a `wl_` token. Token is stored via a keychain-backed `tokenStore`.

**Tech Stack:** Go 1.25 stdlib (`net/http`, `net`, `crypto/*`), `github.com/spf13/cobra`, `github.com/zalando/go-keyring` (new), existing `internal/store`, `internal/oidc`, `internal/api` cookie/HMAC helpers.

**Design doc:** `docs/plans/2026-07-20-provider-neutral-cli-login-design.md`

---

## File Structure

**Server (`internal/api/`):**
- `cliauth.go` (create) — one-time-code store (`cliCodeStore`), `finishLogin` seam, and the three handlers (`wellKnownLogin`, `cliLogin`, `cliToken`).
- `cliauth_test.go` (create) — tests for the store and all three handlers + `finishLogin` branches.
- `session.go` (modify) — add the signed CLI-intent cookie (`cliCookieName`, `cliIntent`, `signCLIIntent`, `verifyCLIIntent`).
- `session_test.go` (modify) — round-trip test for the CLI-intent cookie.
- `oidcweb.go` (modify) — `authCallback` tail calls `finishLogin`.
- `githubweb.go` (modify) — `githubCallback` tail calls `finishLogin`.
- `server.go` (modify) — register the three new routes; add `cliCodes` field to `server`, init in `NewServer`.

**CLI (`internal/cli/`, `internal/cmd/`):**
- `tokenstore.go` (create) — `TokenStore` interface + keychain impl.
- `tokenstore_test.go` (create) — keychain get/set/delete via `keyring.MockInit()`.
- `client.go` (modify) — `LoadConfig`/`SaveConfig` use the keychain; config.toml holds only `server`; legacy-token read fallback + strip.
- `client_test.go` (modify) — resolution order + legacy migration tests.
- `login.go` (rewrite) — server-mediated `RunLogin`; delete `fetchOIDCConfig`/`exchangeWTToken`; ephemeral-only `listenLocal`.
- `login_test.go` (rewrite) — drive the new flow with a stub server + injected browser.
- `cmd/login.go` (modify) — provider-neutral help text (behavior unchanged).
- `cmd/logout.go` (create) — `wl logout`.
- `cmd/logout_test.go` (create) — logout clears the keychain entry.

**e2e (`e2e/`):**
- `cli_login_test.go` (create, optional) — happy-path login against a GitHub-auth stub server.

---

## Task 1: One-time-code store

**Files:**
- Create: `internal/api/cliauth.go`
- Test: `internal/api/cliauth_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/cliauth_test.go` (white-box `package api` — it tests unexported types):

```go
package api

import (
	"testing"
	"time"
)

func TestCLICodeStoreMintRedeem(t *testing.T) {
	now := time.Unix(1000, 0)
	s := newCLICodeStore(func() time.Time { return now })

	code, err := s.mint("github:42", "clistate")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if len(code) < 32 {
		t.Fatalf("code too short: %q", code)
	}

	actor, ok := s.redeem(code, "clistate")
	if !ok || actor != "github:42" {
		t.Fatalf("redeem = %q,%v; want github:42,true", actor, ok)
	}
	// Single use: second redeem fails.
	if _, ok := s.redeem(code, "clistate"); ok {
		t.Fatal("second redeem should fail")
	}
}

func TestCLICodeStoreWrongStateAndExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	s := newCLICodeStore(func() time.Time { return now })
	code, _ := s.mint("a", "right")

	if _, ok := s.redeem(code, "wrong"); ok {
		t.Fatal("wrong state should not redeem")
	}
	// Still unused after a failed state check; now let it expire.
	now = now.Add(cliCodeTTL + time.Second)
	if _, ok := s.redeem(code, "right"); ok {
		t.Fatal("expired code should not redeem")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestCLICodeStore -v`
Expected: FAIL — `undefined: newCLICodeStore`.

- [ ] **Step 3: Implement the store**

Create `internal/api/cliauth.go`:

```go
// cliauth.go implements the server-mediated CLI login flow: a discovery
// endpoint, a login-start endpoint that stamps a loopback redirect target into
// a signed cookie, and a token endpoint that redeems a one-time code for a wl_
// token. The one-time code is minted in finishLogin (shared by both web
// callbacks) once the actor is provisioned. See
// docs/plans/2026-07-20-provider-neutral-cli-login-design.md.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// cliCodeTTL bounds how long a one-time code is valid between the browser
// redirect and the CLI's token exchange.
const cliCodeTTL = 60 * time.Second

type cliCode struct {
	actorID string
	state   string
	expires time.Time
}

// cliCodeStore holds pending one-time codes in memory. The server is
// single-instance, so a restart simply drops pending 60s codes.
type cliCodeStore struct {
	mu    sync.Mutex
	codes map[string]cliCode
	now   func() time.Time
}

func newCLICodeStore(now func() time.Time) *cliCodeStore {
	return &cliCodeStore{codes: map[string]cliCode{}, now: now}
}

// mint stores a fresh single-use code bound to actorID and the CLI state.
func (s *cliCodeStore) mint(actorID, state string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	code := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[code] = cliCode{actorID: actorID, state: state, expires: s.now().Add(cliCodeTTL)}
	return code, nil
}

// redeem returns the bound actor id and consumes the code. It fails if the
// code is unknown, expired, or the state does not match. A state mismatch does
// NOT consume the code.
func (s *cliCodeStore) redeem(code, state string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.codes[code]
	if !ok || s.now().After(c.expires) {
		delete(s.codes, code)
		return "", false
	}
	if c.state != state {
		return "", false
	}
	delete(s.codes, code)
	return c.actorID, true
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run TestCLICodeStore -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/cliauth.go internal/api/cliauth_test.go
git commit -m "feat(api): one-time code store for CLI login"
```

---

## Task 2: CLI-intent signed cookie

**Files:**
- Modify: `internal/api/session.go`
- Test: `internal/api/session_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/api/session_test.go` (white-box `package api`):

```go
func TestCLIIntentRoundTrip(t *testing.T) {
	now := time.Unix(2000, 0)
	secret := "s3cr3t"
	want := cliIntent{Redirect: "http://localhost:54321/", State: "abc", Exp: now.Add(cliCodeTTL).Unix()}
	val := signCLIIntent(secret, want)

	got, ok := verifyCLIIntent(secret, val, now)
	if !ok || got.Redirect != want.Redirect || got.State != want.State {
		t.Fatalf("verify = %+v,%v; want %+v", got, ok, want)
	}
	// Tampered value fails.
	if _, ok := verifyCLIIntent(secret, val+"x", now); ok {
		t.Fatal("tampered intent should not verify")
	}
	// Expired fails.
	if _, ok := verifyCLIIntent(secret, val, now.Add(2*cliCodeTTL)); ok {
		t.Fatal("expired intent should not verify")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestCLIIntentRoundTrip -v`
Expected: FAIL — `undefined: cliIntent`.

- [ ] **Step 3: Implement the cookie**

Add to `internal/api/session.go`. Add `cliCookieName` to the `const` block:

```go
	cliCookieName = "wl_cli"
```

Then append:

```go
// cliIntent is the payload of the short-lived cookie set at /auth/cli/login. It
// carries the loopback redirect target and CSRF state across the web-login
// redirect chain, so finishLogin knows to hand a one-time code back to the CLI
// rather than set a browser session.
type cliIntent struct {
	Redirect string `json:"r"`
	State    string `json:"s"`
	Exp      int64  `json:"e"`
}

func signCLIIntent(secret string, ci cliIntent) string {
	payload, _ := json.Marshal(ci)
	return signPayload(secret, payload)
}

func verifyCLIIntent(secret, value string, now time.Time) (cliIntent, bool) {
	payload, ok := verifyPayload(secret, value)
	if !ok {
		return cliIntent{}, false
	}
	var ci cliIntent
	if err := json.Unmarshal(payload, &ci); err != nil {
		return cliIntent{}, false
	}
	if now.Unix() >= ci.Exp {
		return cliIntent{}, false
	}
	return ci, true
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run 'TestCLIIntentRoundTrip|TestCLICodeStore' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/session.go internal/api/session_test.go
git commit -m "feat(api): signed CLI-intent cookie"
```

---

## Task 3: `finishLogin` seam

Refactor both web callbacks to end via one shared helper that either hands a
one-time code back to the CLI (when the intent cookie is present) or sets the
browser session as before.

**Files:**
- Modify: `internal/api/cliauth.go` (add `finishLogin`)
- Modify: `internal/api/oidcweb.go:168-181` (`authCallback` tail)
- Modify: `internal/api/githubweb.go:177-187` (`githubCallback` tail)
- Modify: `internal/api/server.go` (add `cliCodes *cliCodeStore` field; init in `NewServer`)
- Test: `internal/api/cliauth_test.go`

- [ ] **Step 1: Add the `cliCodes` field and init**

In `internal/api/server.go`, add to the `server` struct (near `tokenCipher`):

```go
	// cliCodes holds pending one-time codes for the server-mediated CLI login.
	cliCodes *cliCodeStore
```

In `NewServer`, right after `s := &server{...}` is constructed, add:

```go
	s.cliCodes = newCLICodeStore(st.Now)
```

- [ ] **Step 2: Write the failing test for `finishLogin`**

Add to `internal/api/cliauth_test.go` (white-box `package api`). Extend its existing `import` block with `net/http`, `net/http/httptest`, `net/url`, and `strings`.

The CLI branch touches no store, so `&server{}` needs only `cfg` + `cliCodes`. `finishLogin`'s signature is `finishLogin(w, r, actorID, next string)` — pass a `next` of `"/"`:

```go
func TestFinishLoginCLIBranch(t *testing.T) {
	s := &server{cfg: Config{SessionSecret: "sek"}, cliCodes: newCLICodeStore(func() time.Time { return time.Unix(1000, 0) })}

	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=x&state=y", nil)
	req.AddCookie(&http.Cookie{
		Name:  cliCookieName,
		Value: signCLIIntent("sek", cliIntent{Redirect: "http://localhost:5555/", State: "clistate", Exp: time.Unix(1000, 0).Add(cliCodeTTL).Unix()}),
	})
	rr := httptest.NewRecorder()

	s.finishLogin(rr, req, "github:42", "/")

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d; want 302", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "http://localhost:5555/?code=") || !strings.Contains(loc, "state=clistate") {
		t.Fatalf("redirect = %q; want loopback with code+state", loc)
	}
	// No browser session cookie was set on the CLI branch.
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			t.Fatal("CLI branch must not set a session cookie")
		}
	}
	// The code embedded in the redirect redeems to the actor.
	u, _ := url.Parse(loc)
	if actor, ok := s.cliCodes.redeem(u.Query().Get("code"), "clistate"); !ok || actor != "github:42" {
		t.Fatalf("minted code did not redeem: %q,%v", actor, ok)
	}
}
```

The **web branch** (`finishLoginWeb` sets a session cookie) is left covered by the existing `oidcweb_test.go` / `githubweb_test.go` callback tests, which already assert a session cookie is set — after Step 5 they exercise the refactored path unchanged. Don't duplicate that assertion here (it would need a real store, which the white-box package can't get from `newTestStore`).

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/api/ -run TestFinishLoginCLIBranch -v`
Expected: FAIL — `s.finishLogin undefined`.

- [ ] **Step 4: Implement `finishLogin` + `finishLoginWeb` + `now()`**

Add to `internal/api/cliauth.go`. The `now()` helper lets handler-level white-box tests build a `&server{}` without a store (the CLI branch and `cliLogin` don't touch the DB):

```go
// now returns the store clock, or wall-clock when there is no store (tests that
// build a bare *server to exercise a handler directly).
func (s *server) now() time.Time {
	if s.st != nil {
		return s.st.Now()
	}
	return time.Now()
}

// finishLogin ends a successful web login for actorID. When the CLI-intent
// cookie is present (a server-mediated `wl login`), it mints a one-time code
// and redirects to the loopback redirect_uri instead of establishing a browser
// session. Otherwise it delegates to finishLoginWeb.
func (s *server) finishLogin(w http.ResponseWriter, r *http.Request, actorID, next string) {
	if c, err := r.Cookie(cliCookieName); err == nil {
		if ci, ok := verifyCLIIntent(s.cfg.SessionSecret, c.Value, s.now()); ok {
			code, err := s.cliCodes.mint(actorID, ci.State)
			if err != nil {
				s.log.Error("mint cli code", "err", err)
				webErr(w, http.StatusInternalServerError, "internal error")
				return
			}
			// Clear both transient cookies.
			http.SetCookie(w, &http.Cookie{Name: cliCookieName, Path: "/auth/", MaxAge: -1})
			http.SetCookie(w, &http.Cookie{Name: oauthCookieName, Path: "/auth/", MaxAge: -1})
			u := ci.Redirect + "?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(ci.State)
			http.Redirect(w, r, u, http.StatusFound)
			return
		}
	}
	s.finishLoginWeb(w, r, actorID, next)
}

// finishLoginWeb sets the browser session cookie and redirects to next. This is
// the original tail shared by both web callbacks.
func (s *server) finishLoginWeb(w http.ResponseWriter, r *http.Request, actorID, next string) {
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
	http.Redirect(w, r, safeNext(next), http.StatusFound)
}
```

- [ ] **Step 5: Point both callbacks at `finishLogin`**

In `internal/api/oidcweb.go`, replace the tail of `authCallback` (the `http.SetCookie(session…)` + oauth-cookie clear + `http.Redirect(safeNext(st.Next))` block, currently lines ~168-181) with:

```go
	s.finishLogin(w, r, username, st.Next)
}
```

In `internal/api/githubweb.go`, replace the equivalent tail of `githubCallback` (lines ~177-187) with:

```go
	s.finishLogin(w, r, actorID, safeNext(stt.Next))
}
```

- [ ] **Step 6: Run the full api suite**

Run: `go test ./internal/api/ -v`
Expected: PASS — existing `oidcweb_test.go` / `githubweb_test.go` still assert the web branch sets a session; new `TestFinishLoginCLIBranch` passes.

- [ ] **Step 7: Commit**

```bash
git add internal/api/cliauth.go internal/api/cliauth_test.go internal/api/oidcweb.go internal/api/githubweb.go internal/api/server.go
git commit -m "feat(api): finishLogin seam shared by web callbacks and CLI login"
```

---

## Task 4: `GET /auth/cli/login` handler

**Files:**
- Modify: `internal/api/cliauth.go`
- Modify: `internal/api/server.go` (register route)
- Test: `internal/api/cliauth_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/api/cliauth_test.go`:

```go
func TestCLILoginValidatesLoopback(t *testing.T) {
	s := &server{cfg: Config{SessionSecret: "sek"}, gh: &githubauth.Client{}, cliCodes: newCLICodeStore(func() time.Time { return time.Unix(1000, 0) })}

	bad := []string{"", "https://evil.com/", "http://evil.com/", "http://localhost/", "ftp://localhost:1/"}
	for _, ru := range bad {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/auth/cli/login?state=x&redirect_uri="+url.QueryEscape(ru), nil)
		s.cliLogin(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("redirect_uri %q: status %d; want 400", ru, rr.Code)
		}
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/cli/login?state=x&redirect_uri="+url.QueryEscape("http://localhost:5555/"), nil)
	s.cliLogin(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("good redirect_uri: status %d; want 302", rr.Code)
	}
	// Intent cookie is set, and we are redirected into the web login (single
	// provider -> /auth/github/login).
	var hasIntent bool
	for _, c := range rr.Result().Cookies() {
		if c.Name == cliCookieName && c.Value != "" {
			hasIntent = true
		}
	}
	if !hasIntent {
		t.Fatal("intent cookie not set")
	}
	if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "/auth/github/login") {
		t.Fatalf("redirect = %q; want /auth/github/login", loc)
	}
}
```

(Add `"github.com/sunstoneinstitute/work-tracker/internal/githubauth"` to the test imports.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestCLILoginValidatesLoopback -v`
Expected: FAIL — `s.cliLogin undefined`.

- [ ] **Step 3: Implement `cliLogin` + loopback validation**

Add to `internal/api/cliauth.go`:

```go
// isLoopbackRedirect reports whether raw is a syntactically valid http URL whose
// host is a loopback address with an explicit non-zero port. This blocks code
// exfiltration to a remote redirect target.
func isLoopbackRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return false
	}
	if p := u.Port(); p == "" || p == "0" {
		return false
	}
	return true
}

// cliLogin handles GET /auth/cli/login: validate the loopback redirect target,
// stamp it into a signed cookie, and redirect into the normal web login.
func (s *server) cliLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil && s.gh == nil {
		writeErr(w, http.StatusNotFound, "no interactive login configured")
		return
	}
	q := r.URL.Query()
	redirect := q.Get("redirect_uri")
	state := q.Get("state")
	if state == "" || !isLoopbackRedirect(redirect) {
		writeErr(w, http.StatusBadRequest, "invalid redirect_uri or state")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cliCookieName,
		Value:    signCLIIntent(s.cfg.SessionSecret, cliIntent{Redirect: redirect, State: state, Exp: s.now().Add(oauthStateMaxAge).Unix()}),
		Path:     "/auth/",
		MaxAge:   int(oauthStateMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.loginTarget("/"), http.StatusFound)
}
```

`cliLogin` uses `s.now()` (added in Task 3), so the white-box test's bare `&server{...}` (no store) works. The Task 4 test builds `&server{cfg: Config{SessionSecret: "sek"}, gh: &githubauth.Client{}, cliCodes: newCLICodeStore(func() time.Time { return time.Unix(1000, 0) })}` — `gh` non-nil so `loginTarget` returns `/auth/github/login`.

- [ ] **Step 4: Register the route**

In `internal/api/server.go`, beside the other `/auth/*` unauthenticated routes (near `/auth/oidc/config`), add:

```go
	mux.HandleFunc("GET /auth/cli/login", s.cliLogin)
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/api/ -run 'TestCLILogin|TestFinishLogin' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/cliauth.go internal/api/cliauth_test.go internal/api/server.go
git commit -m "feat(api): GET /auth/cli/login with loopback redirect validation"
```

---

## Task 5: `POST /auth/cli/token` handler

**Files:**
- Modify: `internal/api/cliauth.go`
- Modify: `internal/api/server.go` (register route)
- Test: `internal/api/cliauth_test.go`

- [ ] **Step 1: Write the failing test**

Test `cliToken` **white-box** by calling the handler directly on a hand-built `&server{}` — the same pattern `githubweb_test.go` uses. `NewServer` returns `s.logging(s.metrics(mux))` (a wrapped handler, NOT `*server`), so there is no `*server` to cast to from a black-box test; calling the method directly sidesteps that entirely. The happy path needs a real store for `CreateToken`, so add a white-box store helper.

Add to `internal/api/cliauth_test.go`:

```go
// newStoreT opens a migrated store for white-box tests (package api can import
// store the same way the black-box harness does).
func newStoreT(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "wl.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(store.MigrationsDirForTests()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestCLITokenRejectsUnknownCode(t *testing.T) {
	st := newStoreT(t)
	s := &server{st: st, log: slog.Default(), cliCodes: newCLICodeStore(st.Now)}
	req := httptest.NewRequest(http.MethodPost, "/auth/cli/token", strings.NewReader(`{"code":"nope","state":"s"}`))
	rr := httptest.NewRecorder()
	s.cliToken(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rr.Code)
	}
}

func TestCLITokenHappyPath(t *testing.T) {
	st := newStoreT(t)
	if err := st.CreateActor(context.Background(), "github:7", "human", "Bob", false); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	s := &server{st: st, log: slog.Default(), cliCodes: newCLICodeStore(st.Now)}
	code, err := s.cliCodes.mint("github:7", "clistate")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/auth/cli/token",
		strings.NewReader(`{"code":"`+code+`","state":"clistate"}`))
	rr := httptest.NewRecorder()
	s.cliToken(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s; want 200", rr.Code, rr.Body.String())
	}
	var m map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(m["token"]) < 10 || m["actor_id"] != "github:7" {
		t.Fatalf("bad token response: %v", m)
	}
}
```

Add to the `cliauth_test.go` import block (in addition to those from earlier tasks): `context`, `encoding/json`, `log/slog`, `path/filepath`, `strings`, and `github.com/sunstoneinstitute/work-tracker/internal/store`. No `export_test.go` and no changes to `NewServer` are needed.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestCLIToken -v`
Expected: FAIL — undefined helpers / handler.

- [ ] **Step 3: Implement `cliToken`**

Add to `internal/api/cliauth.go`:

```go
type cliTokenRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

// cliToken handles POST /auth/cli/token: redeem a one-time code (proof the
// browser login completed) for a 30-day wl_ token.
func (s *server) cliToken(w http.ResponseWriter, r *http.Request) {
	var req cliTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	actorID, ok := s.cliCodes.redeem(req.Code, req.State)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid or expired code")
		return
	}
	exp := s.now().Add(ssoTokenTTL)
	token, err := s.st.CreateToken(r.Context(), actorID, "wl login", &exp)
	if err != nil {
		s.log.Error("mint cli token", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"token":      token,
		"actor_id":   actorID,
		"expires_at": exp.UTC().Format(time.RFC3339),
	})
}
```

Add `"encoding/json"` to the `cliauth.go` imports. `ssoTokenTTL` already exists in `oidcauth.go`.

- [ ] **Step 4: Register the route**

In `internal/api/server.go`, near the `/auth/cli/login` registration:

```go
	mux.HandleFunc("POST /auth/cli/token", s.cliToken)
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/api/ -run TestCLIToken -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/cliauth.go internal/api/cliauth_test.go internal/api/server.go
git commit -m "feat(api): POST /auth/cli/token redeems one-time code for wl_ token"
```

---

## Task 6: `GET /.well-known/wl-login` discovery

**Files:**
- Modify: `internal/api/cliauth.go`
- Modify: `internal/api/server.go` (register route)
- Test: `internal/api/cliauth_test.go`

- [ ] **Step 1: Write the failing test**

White-box again — call `s.wellKnownLogin` directly on a hand-built `&server{}`. This mirrors `githubweb_test.go`, which builds `&server{gh: &githubauth.Client{}}` to exercise provider-gated behavior without real provider config. Add to `internal/api/cliauth_test.go`:

```go
func TestWellKnownLogin404WhenNoProvider(t *testing.T) {
	s := &server{} // no oidc, no gh
	req := httptest.NewRequest(http.MethodGet, "/.well-known/wl-login", nil)
	rr := httptest.NewRecorder()
	s.wellKnownLogin(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rr.Code)
	}
}

func TestWellKnownLoginReportsProviders(t *testing.T) {
	s := &server{gh: &githubauth.Client{}, cfg: Config{PublicURL: "https://wl.example.com"}}
	req := httptest.NewRequest(http.MethodGet, "/.well-known/wl-login", nil)
	rr := httptest.NewRecorder()
	s.wellKnownLogin(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rr.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m["authorize_url"] != "https://wl.example.com/auth/cli/login" || m["token_url"] != "https://wl.example.com/auth/cli/token" {
		t.Fatalf("urls wrong: %v", m)
	}
	provs, _ := m["providers"].([]any)
	if len(provs) != 1 || provs[0] != "github" {
		t.Fatalf("providers = %v; want [github]", m["providers"])
	}
}
```

`githubauth` is already imported by the Task 4 test. No `export_test.go`, no `routes()` extraction.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestWellKnownLogin -v`
Expected: FAIL — `s.wellKnownLogin undefined`.

- [ ] **Step 3: Implement `wellKnownLogin`**

Add to `internal/api/cliauth.go`:

```go
// wellKnownLogin handles GET /.well-known/wl-login: tells the CLI where to start
// the login and which providers are available. 404 when the server has no
// interactive provider configured.
func (s *server) wellKnownLogin(w http.ResponseWriter, _ *http.Request) {
	if s.oidc == nil && s.gh == nil {
		writeErr(w, http.StatusNotFound, "no interactive login configured")
		return
	}
	var providers []string
	if s.gh != nil {
		providers = append(providers, "github")
	}
	if s.oidc != nil {
		providers = append(providers, "keycloak")
	}
	base := strings.TrimRight(s.cfg.PublicURL, "/")
	writeJSON(w, http.StatusOK, map[string]any{
		"authorize_url": base + "/auth/cli/login",
		"token_url":     base + "/auth/cli/token",
		"providers":     providers,
	})
}
```

- [ ] **Step 4: Register the route**

In `internal/api/server.go`:

```go
	mux.HandleFunc("GET /.well-known/wl-login", s.wellKnownLogin)
```

Note the literal `.well-known` path segment is fine with `http.ServeMux` — register it exactly as shown.

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/api/ -v`
Expected: PASS (whole api suite).

- [ ] **Step 6: Commit**

```bash
git add internal/api/cliauth.go internal/api/cliauth_test.go internal/api/server.go
git commit -m "feat(api): GET /.well-known/wl-login discovery endpoint"
```

---

## Task 7: Keychain-backed `TokenStore`

**Files:**
- Create: `internal/cli/tokenstore.go`
- Test: `internal/cli/tokenstore_test.go`
- Modify: `go.mod` / `go.sum`

- [ ] **Step 1: Add the dependency**

Run:
```bash
go get github.com/zalando/go-keyring@latest
```
Expected: `go.mod`/`go.sum` updated.

- [ ] **Step 2: Write the failing test**

Create `internal/cli/tokenstore_test.go`:

```go
package cli_test

import (
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/sunstoneinstitute/work-tracker/internal/cli"
)

func TestKeychainTokenStore(t *testing.T) {
	keyring.MockInit() // in-memory backend; no real keychain touched

	ts := cli.NewKeychainTokenStore()
	const server = "https://wl.example.com"

	if _, err := ts.Get(server); err == nil {
		t.Fatal("expected miss before set")
	}
	if err := ts.Set(server, "wl_abc"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := ts.Get(server)
	if err != nil || got != "wl_abc" {
		t.Fatalf("get = %q,%v; want wl_abc,nil", got, err)
	}
	if err := ts.Delete(server); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := ts.Get(server); err == nil {
		t.Fatal("expected miss after delete")
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/cli/ -run TestKeychainTokenStore -v`
Expected: FAIL — `cli.NewKeychainTokenStore undefined`.

- [ ] **Step 4: Implement the store**

Create `internal/cli/tokenstore.go`:

```go
// tokenstore.go stores the wl_ bearer token in the OS keychain (macOS Keychain,
// Linux Secret Service, Windows Credential Manager) instead of cleartext on
// disk. Tokens are keyed by server URL so one machine can hold tokens for
// several work-tracker servers.
package cli

import "github.com/zalando/go-keyring"

// keychainService is the keychain "service" all wl tokens live under.
const keychainService = "work-tracker"

// TokenStore reads and writes the bearer token for a given server URL.
type TokenStore interface {
	Get(server string) (string, error)
	Set(server, token string) error
	Delete(server string) error
}

// KeychainTokenStore is the production TokenStore backed by the OS keychain.
type KeychainTokenStore struct{}

func NewKeychainTokenStore() KeychainTokenStore { return KeychainTokenStore{} }

func (KeychainTokenStore) Get(server string) (string, error) {
	return keyring.Get(keychainService, server)
}

func (KeychainTokenStore) Set(server, token string) error {
	return keyring.Set(keychainService, server, token)
}

func (KeychainTokenStore) Delete(server string) error {
	return keyring.Delete(keychainService, server)
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/cli/ -run TestKeychainTokenStore -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/cli/tokenstore.go internal/cli/tokenstore_test.go
git commit -m "feat(cli): keychain-backed TokenStore"
```

---

## Task 8: `LoadConfig`/`SaveConfig` via keychain

Move the token out of `config.toml`: read order `WL_TOKEN` → keychain → legacy
file token; `SaveConfig` writes the token to the keychain and only `server` to
the file, stripping any legacy cleartext token.

**Files:**
- Modify: `internal/cli/client.go`
- Test: `internal/cli/client_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/cli/client_test.go`:

```go
func TestLoadConfigResolvesTokenFromKeychain(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("WL_TOKEN", "")
	t.Setenv("WL_SERVER", "")

	// config.toml has only server.
	if err := cli.SaveServerOnly("https://wl.example.com"); err != nil {
		t.Fatalf("save server: %v", err)
	}
	if err := cli.NewKeychainTokenStore().Set("https://wl.example.com", "wl_kc"); err != nil {
		t.Fatalf("seed keychain: %v", err)
	}
	cfg, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Token != "wl_kc" {
		t.Fatalf("token = %q; want wl_kc", cfg.Token)
	}
}

func TestEnvTokenBeatsKeychain(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("WL_SERVER", "https://wl.example.com")
	t.Setenv("WL_TOKEN", "wl_env")
	_ = cli.NewKeychainTokenStore().Set("https://wl.example.com", "wl_kc")

	cfg, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Token != "wl_env" {
		t.Fatalf("token = %q; want wl_env (env overrides keychain)", cfg.Token)
	}
}

func TestSaveConfigWritesKeychainAndStripsLegacyToken(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("WL_TOKEN", "")
	t.Setenv("WL_SERVER", "")

	// Simulate a legacy cleartext config.toml with a token line.
	if err := cli.WriteRawConfigForTest("server = \"https://wl.example.com\"\ntoken = \"wl_old\"\n"); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	if err := cli.SaveConfig(cli.Config{ServerURL: "https://wl.example.com", Token: "wl_new"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Keychain now holds the new token.
	if got, _ := cli.NewKeychainTokenStore().Get("https://wl.example.com"); got != "wl_new" {
		t.Fatalf("keychain token = %q; want wl_new", got)
	}
	// File no longer contains a token line.
	raw, _ := cli.ReadRawConfigForTest()
	if strings.Contains(raw, "token") {
		t.Fatalf("config.toml still has a token line:\n%s", raw)
	}
}
```

(Add `github.com/zalando/go-keyring` and `strings` to the test imports if not present.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/cli/ -run 'TestLoadConfigResolves|TestEnvToken|TestSaveConfigWrites' -v`
Expected: FAIL — `cli.SaveServerOnly` / `WriteRawConfigForTest` / new behavior undefined.

- [ ] **Step 3: Implement**

In `internal/cli/client.go`:

1. Update the `Config` doc comment: config.toml now recognizes only `server`; `token` is accepted on read as a deprecated legacy field.

2. Add a package var for the token store (overridable in tests is unnecessary since tests use `keyring.MockInit()`):

```go
// tokenStore is the keychain the client reads/writes tokens through.
var tokenStore TokenStore = NewKeychainTokenStore()
```

3. Rewrite `LoadConfig`'s token resolution. After parsing the file into `cfg` (which may set a legacy `cfg.Token`) and before returning, apply:

```go
	// Server + explicit env token first.
	if v := os.Getenv("WL_SERVER"); v != "" {
		cfg.ServerURL = v
	}
	if v := os.Getenv("WL_TOKEN"); v != "" {
		cfg.Token = v
		return cfg, nil
	}
	// Keychain wins over any legacy cleartext token in the file.
	if cfg.ServerURL != "" {
		if tok, err := tokenStore.Get(cfg.ServerURL); err == nil && tok != "" {
			cfg.Token = tok
		}
	}
	return cfg, nil
```

(Keep the legacy `cfg.Token` from the file as the fallback when the keychain has nothing — that already happens because we only overwrite it on a keychain hit.)

4. Rewrite `SaveConfig` to store the token in the keychain and write only `server`:

```go
// SaveConfig stores the token in the OS keychain and writes only the server URL
// to ~/.config/worklode/config.toml. Any legacy cleartext token in the file is
// dropped. Returns an error (without writing the file) if the keychain write
// fails, so the token is never silently left only in cleartext.
func SaveConfig(cfg Config) error {
	if cfg.Token != "" {
		if err := tokenStore.Set(cfg.ServerURL, cfg.Token); err != nil {
			return fmt.Errorf("store token in keychain (set WL_TOKEN to use a token without the keychain): %w", err)
		}
	}
	return SaveServerOnly(cfg.ServerURL)
}

// SaveServerOnly writes just the server key to config.toml (0600), creating the
// directory (0700) as needed. It never writes the token.
func SaveServerOnly(server string) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "server = %q\n", server)
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
```

5. Add small test hooks at the bottom of `client.go` (guarded only by being exported for `_test` use — acceptable in this codebase, mirroring existing patterns; if the team prefers, move to a `client_export_test.go`). Preferred: create `internal/cli/export_test.go`:

```go
package cli

import "os"

func WriteRawConfigForTest(data string) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepathDir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(data), 0o600)
}

func ReadRawConfigForTest() (string, error) {
	path, err := configPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	return string(b), err
}
```

Use `path/filepath` directly instead of a `filepathDir` shim — replace `filepathDir(path)` with `filepath.Dir(path)` and import `path/filepath`.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/cli/ -v`
Expected: PASS. (Existing `SaveConfig` round-trip test that asserted a `token` line in the file must be updated/removed — the file no longer holds a token. Update `TestSaveConfigRoundTrip` to assert only `server` is written and the token is retrievable from `keyring.MockInit()`.)

- [ ] **Step 5: Commit**

```bash
git add internal/cli/client.go internal/cli/client_test.go internal/cli/export_test.go
git commit -m "feat(cli): store token in keychain; config.toml keeps only server"
```

---

## Task 9: Rewrite `RunLogin` (server-mediated) + ephemeral port

**Files:**
- Rewrite: `internal/cli/login.go`
- Rewrite: `internal/cli/login_test.go`

- [ ] **Step 1: Write the failing test**

Replace `internal/cli/login_test.go` with a flow test against a stub server. The injected `OpenBrowser` simulates the browser: it fetches the authorize URL's `redirect_uri` + `state`, then calls the loopback with a `code`.

```go
package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/sunstoneinstitute/work-tracker/internal/cli"
)

func TestRunLoginServerMediated(t *testing.T) {
	// Stub work-tracker server: discovery + token exchange.
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/wl-login", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"authorize_url": "http://" + r.Host + "/auth/cli/login",
			"token_url":     "http://" + r.Host + "/auth/cli/token",
			"providers":     []string{"github"},
		})
	})
	mux.HandleFunc("/auth/cli/token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != "THECODE" || body["state"] == "" {
			http.Error(w, `{"error":"bad code"}`, http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"token": "wl_minted", "actor_id": "github:7", "expires_at": "2026-08-19T00:00:00Z",
		})
	})
	wt := httptest.NewServer(mux)
	defer wt.Close()

	// Browser stub: parse redirect_uri + state from the authorize URL and hit
	// the loopback with a code, as the server would after a web login.
	openBrowser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		cb := q.Get("redirect_uri") + "?code=THECODE&state=" + url.QueryEscape(q.Get("state"))
		go http.Get(cb)
		return nil
	}

	res, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server: wt.URL, OpenBrowser: openBrowser,
	})
	if err != nil {
		t.Fatalf("RunLogin: %v", err)
	}
	if res.Token != "wl_minted" || res.ActorID != "github:7" {
		t.Fatalf("result = %+v; want wl_minted/github:7", res)
	}
}

func TestRunLoginNoInteractiveLogin(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/wl-login", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no interactive login configured", http.StatusNotFound)
	})
	wt := httptest.NewServer(mux)
	defer wt.Close()

	_, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server: wt.URL, OpenBrowser: func(string) error { return nil },
	})
	if err == nil {
		t.Fatal("expected error when server has no interactive login")
	}
}

func TestRunLoginStateMismatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/wl-login", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"authorize_url": "http://" + r.Host + "/auth/cli/login",
			"token_url":     "http://" + r.Host + "/auth/cli/token",
		})
	})
	wt := httptest.NewServer(mux)
	defer wt.Close()

	openBrowser := func(authURL string) error {
		u, _ := url.Parse(authURL)
		cb := u.Query().Get("redirect_uri") + "?code=X&state=WRONG"
		go http.Get(cb)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := cli.RunLogin(ctx, cli.LoginOptions{Server: wt.URL, OpenBrowser: openBrowser})
	if err == nil {
		t.Fatal("expected state-mismatch error")
	}
}
```

(Add `"time"` import.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/cli/ -run TestRunLogin -v`
Expected: FAIL — old `RunLogin` signature/behavior; compile errors after rewrite.

- [ ] **Step 3: Rewrite `login.go`**

Replace `internal/cli/login.go` with the server-mediated flow. Keep `LoginOptions`/`LoginResult`, `callbackHandler`, `randState`, `openBrowser`. Delete `fetchOIDCConfig`, `exchangeWTToken`, and all `internal/oidc`/`golang.org/x/oauth2` usage. Simplify `listenLocal` to ephemeral-only.

```go
// login.go implements `wl login`: a provider-neutral, server-mediated auth flow.
// The CLI discovers the server's login URLs, opens a browser to the server's
// /auth/cli/login with an ephemeral-port loopback redirect, waits for the
// server to redirect a one-time code back to the loopback, and exchanges that
// code for a wl_ token. The CLI speaks no provider protocol.
package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type LoginOptions struct {
	Server      string
	HTTPClient  *http.Client
	OpenBrowser func(string) error
}

type LoginResult struct {
	ActorID   string
	ExpiresAt string
	Token     string
}

type wlLoginDiscovery struct {
	AuthorizeURL string   `json:"authorize_url"`
	TokenURL     string   `json:"token_url"`
	Providers    []string `json:"providers"`
}

func RunLogin(ctx context.Context, opts LoginOptions) (*LoginResult, error) {
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if opts.OpenBrowser == nil {
		opts.OpenBrowser = openBrowser
	}

	disc, err := fetchLoginConfig(ctx, opts.HTTPClient, opts.Server)
	if err != nil {
		return nil, err
	}

	ln, port, err := listenLocal()
	if err != nil {
		return nil, err
	}
	defer ln.Close()

	state := randState()
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{Handler: callbackHandler(state, codeCh, errCh), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	redirectURL := fmt.Sprintf("http://localhost:%d/", port)
	authURL := disc.AuthorizeURL + "?redirect_uri=" + url.QueryEscape(redirectURL) + "&state=" + url.QueryEscape(state)
	if len(disc.Providers) > 0 {
		fmt.Printf("Opening browser to sign in (%s)…\n", strings.Join(disc.Providers, ", "))
	}
	if err := opts.OpenBrowser(authURL); err != nil {
		return nil, fmt.Errorf("open browser: %w", err)
	}

	var code string
	select {
	case code = <-codeCh:
	case err = <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return exchangeCLIToken(ctx, opts.HTTPClient, disc.TokenURL, code, state)
}

// fetchLoginConfig gets the discovery document. A 404 means the server has no
// interactive login configured.
func fetchLoginConfig(ctx context.Context, client *http.Client, server string) (wlLoginDiscovery, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(server, "/")+"/.well-known/wl-login", nil)
	if err != nil {
		return wlLoginDiscovery{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return wlLoginDiscovery{}, fmt.Errorf("fetch login config: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return wlLoginDiscovery{}, errors.New("this work-tracker server has no interactive login; ask an admin to mint you a token and set WL_TOKEN")
	}
	if resp.StatusCode != http.StatusOK {
		return wlLoginDiscovery{}, &ClientError{Status: resp.StatusCode, Msg: "fetch login config"}
	}
	var d wlLoginDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return wlLoginDiscovery{}, fmt.Errorf("decode login config: %w", err)
	}
	if d.AuthorizeURL == "" || d.TokenURL == "" {
		return wlLoginDiscovery{}, errors.New("login config missing authorize_url or token_url")
	}
	return d, nil
}

// exchangeCLIToken posts the one-time code to the server's token endpoint.
func exchangeCLIToken(ctx context.Context, client *http.Client, tokenURL, code, state string) (*LoginResult, error) {
	body, _ := json.Marshal(map[string]string{"code": code, "state": state})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, &ClientError{Status: resp.StatusCode, Msg: "exchange code"}
	}
	var r struct {
		Token     string `json:"token"`
		ActorID   string `json:"actor_id"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	return &LoginResult{Token: r.Token, ActorID: r.ActorID, ExpiresAt: r.ExpiresAt}, nil
}

// listenLocal binds an ephemeral loopback port. Because the server (not the IdP)
// redirects to this URL, no port pre-registration is needed and any free port
// works — immune to port conflicts.
func listenLocal() (net.Listener, int, error) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, 0, fmt.Errorf("bind loopback callback port: %w", err)
	}
	return ln, ln.Addr().(*net.TCPAddr).Port, nil
}
```

Keep the existing `callbackHandler`, `randState`, and `openBrowser` functions from the old `login.go` verbatim (they are provider-neutral already). `randState` uses `crypto/rand`+`encoding/hex` (imports retained).

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/cli/ -run TestRunLogin -v`
Expected: PASS.

- [ ] **Step 5: Verify no dangling references**

Run: `go build ./... && go vet ./...`
Expected: no errors. If `internal/oidc` is now unused by the CLI, that is fine — the server still uses it.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/login.go internal/cli/login_test.go
git commit -m "feat(cli): server-mediated provider-neutral RunLogin with ephemeral port"
```

---

## Task 10: `wl logout` command

**Files:**
- Create: `internal/cmd/logout.go`
- Test: `internal/cmd/logout_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cmd/logout_test.go`:

```go
package cmd

import (
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/sunstoneinstitute/work-tracker/internal/cli"
)

func TestLogoutClearsKeychain(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("WL_TOKEN", "")
	t.Setenv("WL_SERVER", "https://wl.example.com")

	if err := cli.NewKeychainTokenStore().Set("https://wl.example.com", "wl_x"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := runLogout("https://wl.example.com"); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := cli.NewKeychainTokenStore().Get("https://wl.example.com"); err == nil {
		t.Fatal("token should be gone after logout")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/cmd/ -run TestLogout -v`
Expected: FAIL — `runLogout undefined`.

- [ ] **Step 3: Implement**

Create `internal/cmd/logout.go`:

```go
package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/work-tracker/internal/cli"
)

// runLogout deletes the stored token for server from the keychain. A missing
// entry is not an error.
func runLogout(server string) error {
	if server == "" {
		return errors.New(`server URL not set: pass --server or set WL_SERVER`)
	}
	err := cli.NewKeychainTokenStore().Delete(server)
	if err != nil && !errors.Is(err, cli.ErrTokenNotFound) {
		return err
	}
	return nil
}

func newLogoutCmd() *cobra.Command {
	var server string
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored token for a server from the OS keychain",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := cli.LoadConfig()
			if err != nil {
				return err
			}
			if server != "" {
				cfg.ServerURL = server
			}
			if err := runLogout(cfg.ServerURL); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "logged out of %s\n", cfg.ServerURL)
			return nil
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "work-tracker server URL (overrides WL_SERVER / config file)")
	return cmd
}

func init() {
	rootCmd.AddCommand(newLogoutCmd())
}
```

Add `ErrTokenNotFound` to `internal/cli/tokenstore.go` to normalize the keychain "not found" error:

```go
import (
	"errors"

	"github.com/zalando/go-keyring"
)

// ErrTokenNotFound is returned by Get/Delete when no token exists for a server.
var ErrTokenNotFound = keyring.ErrNotFound
```

(Adjust the import block in `tokenstore.go` accordingly. `keyring.ErrNotFound` is the sentinel `go-keyring` returns.)

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/cmd/ -run TestLogout -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/logout.go internal/cmd/logout_test.go internal/cli/tokenstore.go
git commit -m "feat(cli): wl logout removes the keychain token"
```

---

## Task 11: Provider-neutral `wl login` help text

**Files:**
- Modify: `internal/cmd/login.go`

- [ ] **Step 1: Update the command copy**

In `internal/cmd/login.go`, change `Short`/`Long` to be provider-neutral (behavior is unchanged — it already calls `RunLogin` then `SaveConfig`):

```go
		Short: "Authenticate to work-tracker and store a token",
		Long: "Open a browser to sign in with whatever identity provider the server\n" +
			"is configured for (Keycloak, GitHub, or a choice of both), then store the\n" +
			"resulting 30-day token in the OS keychain. Re-run after it expires.",
```

Remove the now-inaccurate `Ports`/Keycloak references if any remain in comments.

- [ ] **Step 2: Build + existing tests**

Run: `go build ./... && go test ./internal/cmd/ -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/login.go
git commit -m "docs(cli): provider-neutral wl login help text"
```

---

## Task 12: e2e happy path (optional)

**Files:**
- Create: `e2e/cli_login_test.go` (follow the existing `e2e/` harness conventions — read a sibling test first)

- [ ] **Step 1: Read an existing e2e test** to learn the harness (how it starts a server + runs the `wl` binary). Run: `ls e2e/ && sed -n '1,60p' e2e/<existing_test>.go`.

- [ ] **Step 2: Write** a test that: starts a work-tracker server with GitHub auth stubbed (or a fake provider), runs `wl login` with an injected browser driver that completes the loopback, and asserts a subsequent authenticated `wl` call (e.g. `wl board`) succeeds using the keychain token (`keyring.MockInit()` in-process). If the e2e harness runs `wl` as a separate process, the keychain mock will not apply across processes — in that case assert via `WL_TOKEN` captured from the login output instead, or skip e2e and rely on the unit coverage. Decide based on the harness shape.

- [ ] **Step 3: Run** `go test ./e2e/ -run CLILogin -v`. Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add e2e/cli_login_test.go
git commit -m "test(e2e): provider-neutral wl login happy path"
```

---

## Final verification

- [ ] `go build ./...` — clean.
- [ ] `go vet ./...` — clean.
- [ ] `go test ./...` — all pass.
- [ ] `gofmt -l internal/ cmd/ e2e/` — no files listed.
- [ ] Manual smoke (optional, needs a real server): `WL_SERVER=… wl login` opens a browser, completes, and `wl board` works; `wl logout` then makes `wl board` prompt for auth. Confirm no token appears in `~/.config/worklode/config.toml`.
