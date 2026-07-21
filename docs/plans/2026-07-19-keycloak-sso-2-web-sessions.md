# Keycloak SSO — Plan 2: Web UI Sessions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Depends on:** Plan 1 (server core) — this plan uses `s.oidc`, `s.cfg.SessionSecret`, `s.cfg.PublicURL`, the `provisionActor` helper, and the `oidctest` fake issuer, all introduced there.

**Goal:** Gate the read-only web UI behind an OIDC login when SSO is enabled: an auth-code + PKCE redirect flow (`/auth/login`, `/auth/callback`), HMAC-signed session cookies with no server-side state, and middleware that 302s unauthenticated web requests to login. When OIDC is unconfigured the web UI stays open exactly as today.

**Architecture:** Two small self-contained files. `session.go` holds stateless cookie signing/verification (session cookie + a short-lived oauth-transient cookie carrying `state`/PKCE-verifier/`next`). `oidcweb.go` holds `/auth/login` and `/auth/callback` plus a `webAuth` middleware. The three existing web routes (`/`, `/tasks/{id}`, `/projects/{id}`) get wrapped in `webAuth`; `/healthz` and `/metrics` stay open. All cookie state is signed under `WL_SESSION_SECRET` — no server session store.

**Tech Stack:** Go 1.25 stdlib (`crypto/hmac`, `crypto/sha256`, `encoding/base64`, `net/http`), `golang.org/x/oauth2` (PKCE helpers), `internal/oidc`.

---

## File Structure

- `internal/api/session.go` (create) — cookie signing/verification: `signSession`/`verifySession`, `signOAuthState`/`verifyOAuthState`, cookie constants.
- `internal/api/session_test.go` (create) — round-trip + tamper/expiry unit tests.
- `internal/api/oidcweb.go` (create) — `authLogin`, `authCallback`, `webAuth` middleware, small helpers (`randToken`, `idTokenFromOAuth2`).
- `internal/api/oidcweb_test.go` (create) — redirect-when-no-cookie, full callback round-trip, tampered/expired cookie.
- `internal/api/server.go` (modify) — wrap web routes in `webAuth`, register `/auth/login` and `/auth/callback`.

---

## Task 1: Session and oauth-state cookies

**Files:**
- Create: `internal/api/session.go`
- Test: `internal/api/session_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/api/session_test.go`:

```go
package api

import (
	"testing"
	"time"
)

func TestSessionRoundTrip(t *testing.T) {
	const secret = "s3cr3t"
	now := time.Unix(1_700_000_000, 0)
	cookie := signSession(secret, "alice", now.Add(time.Hour))

	user, ok := verifySession(secret, cookie, now)
	if !ok || user != "alice" {
		t.Fatalf("verifySession = (%q, %v), want (alice, true)", user, ok)
	}
}

func TestSessionRejectsTamper(t *testing.T) {
	const secret = "s3cr3t"
	now := time.Unix(1_700_000_000, 0)
	cookie := signSession(secret, "alice", now.Add(time.Hour))

	// Flip the last byte of the cookie.
	bad := cookie[:len(cookie)-1] + string([]byte{cookie[len(cookie)-1] ^ 0x01})
	if _, ok := verifySession(secret, bad, now); ok {
		t.Fatal("verifySession accepted a tampered cookie")
	}
	// A different secret must also fail.
	if _, ok := verifySession("other", cookie, now); ok {
		t.Fatal("verifySession accepted a cookie under the wrong secret")
	}
}

func TestSessionRejectsExpired(t *testing.T) {
	const secret = "s3cr3t"
	now := time.Unix(1_700_000_000, 0)
	cookie := signSession(secret, "alice", now.Add(time.Hour))

	later := now.Add(2 * time.Hour)
	if _, ok := verifySession(secret, cookie, later); ok {
		t.Fatal("verifySession accepted an expired cookie")
	}
}

func TestOAuthStateRoundTrip(t *testing.T) {
	const secret = "s3cr3t"
	now := time.Unix(1_700_000_000, 0)
	cookie := signOAuthState(secret, oauthState{
		State: "st", Verifier: "vfy", Next: "/tasks/WL-1", Exp: now.Add(10 * time.Minute).Unix(),
	})
	got, ok := verifyOAuthState(secret, cookie, now)
	if !ok {
		t.Fatal("verifyOAuthState = !ok")
	}
	if got.State != "st" || got.Verifier != "vfy" || got.Next != "/tasks/WL-1" {
		t.Fatalf("state = %+v", got)
	}
}

func TestOAuthStateRejectsExpired(t *testing.T) {
	const secret = "s3cr3t"
	now := time.Unix(1_700_000_000, 0)
	cookie := signOAuthState(secret, oauthState{
		State: "st", Verifier: "vfy", Next: "/", Exp: now.Add(-time.Minute).Unix(),
	})
	if _, ok := verifyOAuthState(secret, cookie, now); ok {
		t.Fatal("verifyOAuthState accepted an expired cookie")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run 'TestSession|TestOAuthState'`
Expected: FAIL — `signSession`/`verifySession`/`signOAuthState`/`verifyOAuthState`/`oauthState` undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/api/session.go`:

```go
// session.go implements the web UI's stateless auth cookies, all signed under
// WL_SESSION_SECRET (there is no server-side session store):
//   - the session cookie: {username, expiry}, ~12h, set after a successful
//     login and checked by webAuth on every gated web request.
//   - the oauth-state cookie: {state, PKCE verifier, next, expiry}, short-lived,
//     set at /auth/login and consumed at /auth/callback.
// Both use the same construction: base64url(payload) + "." + base64url(HMAC),
// with a constant-time MAC compare and an expiry embedded in the payload.
package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookieName = "wl_session"
	oauthCookieName   = "wl_oauth"
	sessionLifetime   = 12 * time.Hour
	oauthStateMaxAge  = 10 * time.Minute
)

// hmacSHA256 returns HMAC-SHA256(secret, msg).
func hmacSHA256(secret string, msg []byte) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(msg)
	return m.Sum(nil)
}

// signPayload returns base64url(payload) + "." + base64url(HMAC(secret,payload)).
func signPayload(secret string, payload []byte) string {
	mac := hmacSHA256(secret, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac)
}

// verifyPayload checks the MAC (constant time) and returns the raw payload.
func verifyPayload(secret, value string) ([]byte, bool) {
	pb64, mb64, ok := strings.Cut(value, ".")
	if !ok {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(pb64)
	if err != nil {
		return nil, false
	}
	mac, err := base64.RawURLEncoding.DecodeString(mb64)
	if err != nil {
		return nil, false
	}
	if !hmac.Equal(mac, hmacSHA256(secret, payload)) {
		return nil, false
	}
	return payload, true
}

// signSession signs a session cookie value for username expiring at expiry.
// Payload form: "username|unixExpiry".
func signSession(secret, username string, expiry time.Time) string {
	payload := []byte(username + "|" + strconv.FormatInt(expiry.Unix(), 10))
	return signPayload(secret, payload)
}

// verifySession returns the username from a valid, unexpired session cookie.
func verifySession(secret, value string, now time.Time) (string, bool) {
	payload, ok := verifyPayload(secret, value)
	if !ok {
		return "", false
	}
	user, expStr, ok := strings.Cut(string(payload), "|")
	if !ok {
		return "", false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", false
	}
	if now.Unix() >= exp {
		return "", false
	}
	return user, true
}

// oauthState is the payload of the short-lived cookie that carries CSRF state,
// the PKCE verifier, and the post-login redirect target across the redirect to
// Keycloak and back.
type oauthState struct {
	State    string `json:"s"`
	Verifier string `json:"v"`
	Next     string `json:"n"`
	Exp      int64  `json:"e"`
}

// signOAuthState signs an oauth-state cookie value.
func signOAuthState(secret string, st oauthState) string {
	payload, _ := json.Marshal(st)
	return signPayload(secret, payload)
}

// verifyOAuthState returns the oauthState from a valid, unexpired cookie.
func verifyOAuthState(secret, value string, now time.Time) (oauthState, bool) {
	payload, ok := verifyPayload(secret, value)
	if !ok {
		return oauthState{}, false
	}
	var st oauthState
	if err := json.Unmarshal(payload, &st); err != nil {
		return oauthState{}, false
	}
	if now.Unix() >= st.Exp {
		return oauthState{}, false
	}
	return st, true
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run 'TestSession|TestOAuthState' -v`
Expected: PASS for all five tests.

- [ ] **Step 5: Commit**

```bash
git add internal/api/session.go internal/api/session_test.go
git commit -m "feat(api): stateless signed session and oauth-state cookies"
```

---

## Task 2: Login, callback, and the webAuth middleware

**Files:**
- Create: `internal/api/oidcweb.go`

- [ ] **Step 1: Write the handlers and middleware**

Create `internal/api/oidcweb.go`:

```go
// oidcweb.go gates the read-only web UI behind Keycloak when OIDC is enabled:
//   - webAuth wraps each web page and 302s to /auth/login when the session
//     cookie is absent/invalid. It is a passthrough when OIDC is unconfigured
//     (the UI stays open, as in v1).
//   - GET /auth/login starts an auth-code + PKCE flow: it sets a signed
//     oauth-state cookie and redirects to Keycloak's authorize URL.
//   - GET /auth/callback redeems the code, verifies the ID token, provisions
//     the actor (shared provisionActor), sets the session cookie, and 302s to
//     the originally requested page.
// No server-side session state; cookies expire (no logout endpoint).
package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
)

// oidcScopes are requested on every login. The client-roles-as-groups mapper
// adds the groups claim without an extra scope.
var oidcScopes = []string{"openid", "profile"}

// randToken returns 16 random bytes as hex, for the CSRF state value.
func randToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// callbackURL is the web redirect URI, derived from the configured public URL.
func (s *server) callbackURL() string {
	return strings.TrimRight(s.cfg.PublicURL, "/") + "/auth/callback"
}

// webAuth wraps a web page handler with session-cookie enforcement. When OIDC
// is disabled it is a passthrough. Unauthenticated requests 302 to /auth/login
// with the current path preserved in ?next.
func (s *server) webAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.oidc == nil {
			next(w, r)
			return
		}
		if c, err := r.Cookie(sessionCookieName); err == nil {
			if _, ok := verifySession(s.cfg.SessionSecret, c.Value, s.st.Now()); ok {
				next(w, r)
				return
			}
		}
		http.Redirect(w, r, "/auth/login?next="+url.QueryEscape(r.URL.Path), http.StatusFound)
	}
}

// authLogin handles GET /auth/login: begin the auth-code + PKCE flow.
func (s *server) authLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	next := r.URL.Query().Get("next")
	if !strings.HasPrefix(next, "/") { // only same-origin absolute paths
		next = "/"
	}

	state := randToken()
	verifier := oauth2.GenerateVerifier()
	now := s.st.Now()

	http.SetCookie(w, &http.Cookie{
		Name:     oauthCookieName,
		Value:    signOAuthState(s.cfg.SessionSecret, oauthState{State: state, Verifier: verifier, Next: next, Exp: now.Add(oauthStateMaxAge).Unix()}),
		Path:     "/auth/",
		MaxAge:   int(oauthStateMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	cfg := s.oidc.OAuth2Config(s.callbackURL(), oidcScopes)
	http.Redirect(w, r, cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

// authCallback handles GET /auth/callback: finish the flow and set the session.
func (s *server) authCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		webErr(w, http.StatusNotFound, "not found")
		return
	}

	c, err := r.Cookie(oauthCookieName)
	if err != nil {
		webErr(w, http.StatusBadRequest, "missing login state")
		return
	}
	st, ok := verifyOAuthState(s.cfg.SessionSecret, c.Value, s.st.Now())
	if !ok {
		webErr(w, http.StatusBadRequest, "invalid or expired login state")
		return
	}
	if r.URL.Query().Get("state") != st.State {
		webErr(w, http.StatusBadRequest, "state mismatch")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		webErr(w, http.StatusBadRequest, "missing code")
		return
	}

	cfg := s.oidc.OAuth2Config(s.callbackURL(), oidcScopes)
	tok, err := cfg.Exchange(r.Context(), code, oauth2.VerifierOption(st.Verifier))
	if err != nil {
		s.log.Error("oidc code exchange", "err", err)
		webErr(w, http.StatusBadGateway, "login failed")
		return
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		webErr(w, http.StatusBadGateway, "no id token in response")
		return
	}
	claims, err := s.oidc.Verify(r.Context(), rawID)
	if err != nil {
		webErr(w, http.StatusUnauthorized, "invalid id token")
		return
	}
	if claims.PreferredUsername == "" {
		webErr(w, http.StatusUnauthorized, "id token missing preferred_username")
		return
	}

	username, err := s.provisionActor(r.Context(), claims)
	if errors.Is(err, errNoUserRole) {
		webErr(w, http.StatusForbidden, "the work-tracker user role is required")
		return
	}
	if errors.Is(err, errActorKindConflict) {
		webErr(w, http.StatusConflict, "your username conflicts with an existing non-human actor")
		return
	}
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    signSession(s.cfg.SessionSecret, username, s.st.Now().Add(sessionLifetime)),
		Path:     "/",
		MaxAge:   int(sessionLifetime.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	// Clear the transient oauth-state cookie.
	http.SetCookie(w, &http.Cookie{Name: oauthCookieName, Path: "/auth/", MaxAge: -1})

	next := st.Next
	if !strings.HasPrefix(next, "/") {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusFound)
}
```

The imports are all used: `crypto/rand`+`encoding/hex` (`randToken`), `errors` (`errors.Is` on `errNoUserRole`), `net/http`, `net/url` (`url.QueryEscape`), `strings`, and `golang.org/x/oauth2` (PKCE helpers + `*oauth2.Token`). The `oidc` package is not imported directly here — `s.oidc`'s methods are called on the field typed in `server.go`.

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/api/`
Expected: no output, exit 0. The `authLogin`/`authCallback` methods are unreferenced until Task 3, which Go permits for methods.

- [ ] **Step 3: Commit**

```bash
git add internal/api/oidcweb.go
git commit -m "feat(api): web OIDC login, callback, and session middleware"
```

---

## Task 3: Wire web routes through `webAuth` and register auth routes

**Files:**
- Modify: `internal/api/server.go`

- [ ] **Step 1: Wrap the three web routes**

In `NewServer`, replace the three read-only web UI registrations:

```go
	mux.HandleFunc("GET /{$}", s.boardPage)
	mux.HandleFunc("GET /tasks/{id}", s.taskPage)
	mux.HandleFunc("GET /projects/{id}", s.projectPage)
```

with the `webAuth`-wrapped versions plus the login/callback routes:

```go
	// Read-only web UI. When OIDC is enabled these require a valid session
	// cookie (webAuth 302s to /auth/login otherwise); when OIDC is
	// unconfigured webAuth is a passthrough and the UI stays open as in v1.
	// /healthz and /metrics (above) always stay open.
	mux.HandleFunc("GET /{$}", s.webAuth(s.boardPage))
	mux.HandleFunc("GET /tasks/{id}", s.webAuth(s.taskPage))
	mux.HandleFunc("GET /projects/{id}", s.webAuth(s.projectPage))
	mux.HandleFunc("GET /auth/login", s.authLogin)
	mux.HandleFunc("GET /auth/callback", s.authCallback)
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./...`
Expected: no output, exit 0.

- [ ] **Step 3: Run the existing web tests (open-UI path unchanged)**

Run: `go test ./internal/api/ -run 'TestBoardPage|TestTaskPage|TestProjectPage' -v`
Expected: PASS — with no OIDC configured, `webAuth` is a passthrough, so the existing tests behave exactly as before.

- [ ] **Step 4: Commit**

```bash
git add internal/api/server.go
git commit -m "feat(api): gate web UI behind webAuth when OIDC enabled"
```

---

## Task 4: Web session flow tests

**Files:**
- Create: `internal/api/oidcweb_test.go`

- [ ] **Step 1: Write the tests**

Create `internal/api/oidcweb_test.go`. These reuse `newOIDCServer` from `oidcauth_test.go` (Plan 1) and the `oidctest` issuer's `/token` endpoint.

```go
package api_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// gated GETs must redirect to /auth/login when OIDC is enabled and no session
// cookie is present.
func TestWebRedirectsWhenNoSession(t *testing.T) {
	_, h, _ := newOIDCServer(t)
	for _, path := range []string{"/", "/tasks/WL-1", "/projects/proj"} {
		rr := doReq(t, h, "GET", path, "", nil)
		if rr.Code != http.StatusFound {
			t.Fatalf("GET %s status = %d, want 302", path, rr.Code)
		}
		loc := rr.Header().Get("Location")
		if !strings.HasPrefix(loc, "/auth/login?next=") {
			t.Fatalf("GET %s Location = %q, want /auth/login?next=...", path, loc)
		}
	}
}

// /healthz stays open even with OIDC enabled.
func TestHealthzOpenWithOIDC(t *testing.T) {
	_, h, _ := newOIDCServer(t)
	rr := doReq(t, h, "GET", "/healthz", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rr.Code)
	}
}

// /auth/login sets an oauth-state cookie and redirects to the issuer's
// authorize endpoint carrying the PKCE challenge.
func TestAuthLoginRedirectsToIssuer(t *testing.T) {
	_, h, iss := newOIDCServer(t)
	rr := doReq(t, h, "GET", "/auth/login?next=/tasks/WL-1", "", nil)
	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, iss.URL()+"/auth") {
		t.Fatalf("Location = %q, want issuer authorize URL", loc)
	}
	u, _ := url.Parse(loc)
	if u.Query().Get("code_challenge") == "" || u.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("missing PKCE challenge in %q", loc)
	}
	if u.Query().Get("state") == "" {
		t.Fatalf("missing state in %q", loc)
	}
	if !hasCookie(rr, "wl_oauth") {
		t.Fatal("no wl_oauth cookie set")
	}
}

// Full round-trip: login -> (drive the issuer) -> callback sets a session
// cookie and redirects to the originally requested page.
func TestAuthCallbackRoundTrip(t *testing.T) {
	_, h, iss := newOIDCServer(t)

	// The issuer's /token endpoint will return this ID token.
	iss.TokenClaims = map[string]any{
		"preferred_username": "grace", "name": "Grace", "aud": iss.ClientID,
		"groups": []string{"user"},
	}

	// Step 1: hit /auth/login to obtain the oauth-state cookie and the state param.
	login := doReq(t, h, "GET", "/auth/login?next=/tasks/WL-1", "", nil)
	oauthCookie := cookieValue(login, "wl_oauth")
	loc, _ := url.Parse(login.Header().Get("Location"))
	state := loc.Query().Get("state")

	// Step 2: simulate Keycloak redirecting back to /auth/callback with a code,
	// carrying the oauth-state cookie. The callback exchanges the code at the
	// issuer's /token endpoint (which returns iss.TokenClaims as the id_token).
	req := httptest.NewRequest("GET", "/auth/callback?code=fake-code&state="+url.QueryEscape(state), nil)
	req.AddCookie(&http.Cookie{Name: "wl_oauth", Value: oauthCookie})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("callback status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Location"); got != "/tasks/WL-1" {
		t.Fatalf("callback Location = %q, want /tasks/WL-1", got)
	}
	if !hasCookie(rr, "wl_session") {
		t.Fatal("no wl_session cookie set after callback")
	}

	// Step 3: the session cookie now lets a gated page through.
	session := cookieValue(rr, "wl_session")
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(&http.Cookie{Name: "wl_session", Value: session})
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("board with session status = %d, want 200", rr2.Code)
	}
}

// A tampered session cookie is treated as absent: redirect to login.
func TestWebTamperedSessionRedirects(t *testing.T) {
	_, h, _ := newOIDCServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "wl_session", Value: "garbage.garbage"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 for tampered cookie", rr.Code)
	}
}

// A callback without the oauth-state cookie is a 400 (no session state).
func TestAuthCallbackMissingState(t *testing.T) {
	_, h, _ := newOIDCServer(t)
	rr := doReq(t, h, "GET", "/auth/callback?code=x&state=y", "", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

// --- cookie test helpers ---

func hasCookie(rr *httptest.ResponseRecorder, name string) bool {
	return cookieValue(rr, name) != ""
}

func cookieValue(rr *httptest.ResponseRecorder, name string) string {
	for _, c := range rr.Result().Cookies() {
		if c.Name == name && c.MaxAge >= 0 {
			return c.Value
		}
	}
	return ""
}
```

> This file references `oidctest` only through the `iss` value returned by `newOIDCServer` (defined in `oidcauth_test.go`, same `api_test` package) — all via inferred types and method calls — so it needs no `oidctest` import of its own. `iss.TokenClaims`, `iss.URL()`, and `iss.ClientID` work through the returned `*oidctest.Issuer`.

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/api/ -run 'TestWeb|TestAuth|TestHealthzOpenWithOIDC' -v`
Expected: PASS for every test.

- [ ] **Step 3: Run the full API suite**

Run: `go test ./internal/api/...`
Expected: ok.

- [ ] **Step 4: Commit**

```bash
git add internal/api/oidcweb_test.go
git commit -m "test(api): web session redirect, callback round-trip, tamper"
```

---

## Task 5: Full verification

- [ ] **Step 1: Whole suite + vet + build**

Run: `go test ./... && go vet ./... && go build ./...`
Expected: ok / no output, exit 0.

---

## Self-Review Notes

- **Spec coverage:** web routes require a session when OIDC on, else 302 to `/auth/login` (Task 3 + Task 4 `TestWebRedirectsWhenNoSession`); `/healthz`/`/metrics` stay open (Task 4 `TestHealthzOpenWithOIDC`); UI open when OIDC unconfigured (Task 3 Step 3 — existing tests pass unchanged); `GET /auth/login` → 302 to authorize with PKCE + state in a signed cookie (Task 1 + Task 4 `TestAuthLoginRedirectsToIssuer`); `GET /auth/callback` redeems code, verifies, requires `user`, provisions, sets session, 302s to `next` (Task 2 + Task 4 `TestAuthCallbackRoundTrip`); session cookie HMAC-signed `{username, expiry}` ~12h, `HttpOnly`/`Secure`/`SameSite=Lax`, no server state, no logout (Task 1 + Task 2); tampered/expired cookie → redirect (Task 1 + Task 4).
- **Reuse from Plan 1:** `provisionActor`, `errNoUserRole`, `s.oidc`, `newOIDCServer`, and the `oidctest` issuer (with its `/token` endpoint and `TokenClaims`).
- **Type consistency:** cookie names `wl_session`/`wl_oauth`, functions `signSession`/`verifySession`/`signOAuthState`/`verifyOAuthState`, and the `oauthState` fields are used identically across `session.go`, `oidcweb.go`, and both test files.
- **Note on `Secure` cookies in tests:** `httptest` requests are treated as non-TLS, but `ResponseRecorder.Result().Cookies()` still parses `Secure` cookies from the `Set-Cookie` header, so the round-trip test reads them back fine. In production behind `WL_PUBLIC_URL` (https) the `Secure` attribute is honored by browsers.
