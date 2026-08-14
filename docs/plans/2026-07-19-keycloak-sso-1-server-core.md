---
status: superseded
covers: docs/specs/001-identity-and-authentication.md
---
# Keycloak SSO — Plan 1: Server Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the shared OIDC verification core, human-actor auto-provisioning, feature-flag config wiring, and the two unauthenticated `/auth/oidc/*` endpoints (`config` + `token`) that back the CLI login flow.

**Architecture:** A new `internal/oidc` package wraps `github.com/coreos/go-oidc/v3` for ID-token verification and exposes the oauth2 endpoints that the web (Plan 2) and CLI (Plan 3) flows share. The store gains an `UpsertHumanActor` method. `api.Config` gains four optional OIDC fields; `NewServer` builds a `*oidc.Verifier` only when the issuer + client id are set (unset behaves exactly as today). A new `internal/oidc/oidctest` helper serves a fake issuer (discovery + JWKS + signed tokens) so every flow is testable without a real Keycloak.

**Tech Stack:** Go 1.25, `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`, `github.com/go-jose/go-jose/v4` (test signing), `modernc.org/sqlite`.

**Prerequisite (out of scope for this plan):** The Keycloak `worklode` client, client roles (`user`/`admin`), `client-roles-as-groups` mapper, and groups are configured via GitOps in the admin-cluster repo (`clusters/admin/keycloak-config/rbac.yaml`), per the design doc's "Keycloak configuration" section. This plan is testable end-to-end against the fake issuer without that config.

---

## File Structure

- `go.mod` / `go.sum` — add `go-oidc/v3`, promote `x/oauth2` to a direct dependency.
- `internal/oidc/oidc.go` (create) — `Verifier` (verify ID tokens), `Claims`, `OAuth2Config` builder. Shared by all three flows.
- `internal/oidc/oidctest/oidctest.go` (create) — fake issuer for tests: discovery, JWKS, `SignToken`, and a `/token` endpoint (used by Plans 2 & 3).
- `internal/oidc/oidc_test.go` (create) — verifier unit tests.
- `internal/store/actors.go` (modify) — add `UpsertHumanActor`.
- `internal/store/actors_test.go` (modify) — test `UpsertHumanActor`.
- `internal/api/server.go` (modify) — add OIDC config fields + `oidc` field, build the verifier in `NewServer`, register `/auth/oidc/config` and `/auth/oidc/token`.
- `internal/api/oidcauth.go` (create) — the two endpoint handlers + the shared `provisionActor` helper (reused by Plan 2's web callback).
- `internal/api/oidcauth_test.go` (create) — endpoint tests against the fake issuer.
- `internal/cmd/serve.go` (modify) — pass the four `WL_OIDC_*` / `WL_PUBLIC_URL` / `WL_SESSION_SECRET` env vars into `api.Config`.
- `README.md` (modify) — document the new env vars.

---

## Task 1: Add dependencies

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add go-oidc and promote oauth2 to a direct dependency**

Run:

```bash
go get github.com/coreos/go-oidc/v3@v3.11.0
go get golang.org/x/oauth2@v0.30.0
go mod tidy
```

Expected: `go.mod` now lists `github.com/coreos/go-oidc/v3` and `golang.org/x/oauth2` in the direct `require` block, and `github.com/go-jose/go-jose/v4` appears (indirect for now — Task 3 makes it direct).

- [ ] **Step 2: Verify the module still builds**

Run: `go build ./...`
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "build: add go-oidc and x/oauth2 for SSO"
```

---

## Task 2: `internal/oidc` verifier package

**Files:**
- Create: `internal/oidc/oidc.go`

- [ ] **Step 1: Write the verifier package**

Create `internal/oidc/oidc.go`:

```go
// Package oidc wraps go-oidc/oauth2 for worklode's SSO flows: it verifies
// Keycloak ID tokens and builds the oauth2 config the web and CLI login flows
// share. A Verifier is constructed only when WL_OIDC_ISSUER and
// WL_OIDC_CLIENT_ID are set; an unconfigured server never builds one.
package oidc

import (
	"context"
	"fmt"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Claims are the ID-token claims worklode consumes.
type Claims struct {
	PreferredUsername string   `json:"preferred_username"`
	Name              string   `json:"name"`
	Groups            []string `json:"groups"`
}

// HasRole reports whether role is present in the groups claim. Keycloak's
// client-roles-as-groups mapper delivers the worklode client roles
// (user, admin) here.
func (c *Claims) HasRole(role string) bool {
	for _, g := range c.Groups {
		if g == role {
			return true
		}
	}
	return false
}

// Verifier verifies Keycloak ID tokens and exposes the provider's oauth2
// endpoints.
type Verifier struct {
	provider *gooidc.Provider
	verifier *gooidc.IDTokenVerifier
	clientID string
	issuer   string
}

// New builds a Verifier by fetching the issuer's discovery document (and, on
// first Verify, its JWKS). It returns an error if discovery fails, so a
// misconfigured issuer fails fast at server startup.
func New(ctx context.Context, issuer, clientID string) (*Verifier, error) {
	provider, err := gooidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s: %w", issuer, err)
	}
	return &Verifier{
		provider: provider,
		verifier: provider.Verifier(&gooidc.Config{ClientID: clientID}),
		clientID: clientID,
		issuer:   issuer,
	}, nil
}

// Issuer returns the configured issuer URL.
func (v *Verifier) Issuer() string { return v.issuer }

// ClientID returns the configured OIDC client id.
func (v *Verifier) ClientID() string { return v.clientID }

// Verify checks the raw ID token's signature, issuer, audience, and expiry,
// then extracts the claims worklode uses.
func (v *Verifier) Verify(ctx context.Context, rawIDToken string) (*Claims, error) {
	tok, err := v.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("verify id token: %w", err)
	}
	var c Claims
	if err := tok.Claims(&c); err != nil {
		return nil, fmt.Errorf("decode id token claims: %w", err)
	}
	return &c, nil
}

// OAuth2Config builds the oauth2 config for an auth-code + PKCE flow with the
// given redirect URL and scopes. Shared by the web and CLI login flows. The
// client is public, so ClientSecret is left empty and PKCE is required.
func (v *Verifier) OAuth2Config(redirectURL string, scopes []string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:    v.clientID,
		Endpoint:    v.provider.Endpoint(),
		RedirectURL: redirectURL,
		Scopes:      scopes,
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/oidc/`
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/oidc/oidc.go
git commit -m "feat(oidc): ID-token verifier and oauth2 config builder"
```

---

## Task 3: Fake issuer test helper (`oidctest`)

**Files:**
- Create: `internal/oidc/oidctest/oidctest.go`

- [ ] **Step 1: Write the fake issuer**

Create `internal/oidc/oidctest/oidctest.go`:

```go
// Package oidctest provides a fake OIDC issuer for tests: an httptest server
// serving an OIDC discovery document and JWKS, a SignToken helper that mints
// ID tokens signed with the matching key, and a /token endpoint that returns a
// caller-configured signed ID token (used by the web and CLI login-flow
// tests). It is a normal (non-_test) package so tests in any package can
// import it.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

const keyID = "test-key"

// Issuer is a fake Keycloak realm for tests.
type Issuer struct {
	Server   *httptest.Server
	ClientID string // default audience for SignToken and the /token endpoint

	// TokenClaims is the claim set the /token endpoint signs and returns as its
	// id_token. Tests set this before driving an auth-code exchange.
	TokenClaims map[string]any

	key *rsa.PrivateKey
}

// NewIssuer starts a fake issuer and registers cleanup. ClientID defaults to
// "worklode".
func NewIssuer(t *testing.T) *Issuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	iss := &Issuer{Server: srv, ClientID: "worklode", key: key}

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                srv.URL,
			"authorization_endpoint":                srv.URL + "/auth",
			"token_endpoint":                        srv.URL + "/token",
			"jwks_uri":                              srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: &key.PublicKey, KeyID: keyID, Algorithm: "RS256", Use: "sig",
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		claims := iss.TokenClaims
		if claims == nil {
			claims = map[string]any{}
		}
		writeJSON(w, map[string]any{
			"access_token": "fake-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     iss.SignToken(t, claims),
		})
	})

	t.Cleanup(srv.Close)
	return iss
}

// URL is the issuer URL (pass to oidc.New).
func (i *Issuer) URL() string { return i.Server.URL }

// SignToken mints an RS256 ID token. Missing standard claims are defaulted:
// iss = issuer URL, aud = i.ClientID, sub = "test-subject", iat = now,
// exp = now + 1h. Any of these may be overridden by claims (e.g. a past exp
// for an expiry test, a different aud for a wrong-audience test).
func (i *Issuer) SignToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	now := time.Now()
	full := map[string]any{
		"iss": i.Server.URL,
		"aud": i.ClientID,
		"sub": "test-subject",
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
	for k, v := range claims {
		full[k] = v
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: i.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID),
	)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	payload, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	s, err := obj.CompactSerialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	return s
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 2: Make go-jose a direct dependency**

Run: `go mod tidy && go build ./...`
Expected: no output, exit 0. `github.com/go-jose/go-jose/v4` is now a direct require in `go.mod`.

- [ ] **Step 3: Commit**

```bash
git add internal/oidc/oidctest/oidctest.go go.mod go.sum
git commit -m "test(oidc): fake issuer helper (discovery, JWKS, signed tokens)"
```

---

## Task 4: Verifier unit tests

**Files:**
- Create: `internal/oidc/oidc_test.go`

- [ ] **Step 1: Write the tests**

Create `internal/oidc/oidc_test.go`:

```go
package oidc_test

import (
	"context"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/oidc"
	"github.com/sunstoneinstitute/worklode/internal/oidc/oidctest"
)

func newVerifier(t *testing.T, iss *oidctest.Issuer) *oidc.Verifier {
	t.Helper()
	v, err := oidc.New(context.Background(), iss.URL(), iss.ClientID)
	if err != nil {
		t.Fatalf("oidc.New: %v", err)
	}
	return v
}

func TestVerifyValidToken(t *testing.T) {
	iss := oidctest.NewIssuer(t)
	v := newVerifier(t, iss)

	raw := iss.SignToken(t, map[string]any{
		"preferred_username": "alice",
		"name":               "Alice Example",
		"groups":             []string{"user", "admin"},
	})
	claims, err := v.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.PreferredUsername != "alice" || claims.Name != "Alice Example" {
		t.Fatalf("claims = %+v", claims)
	}
	if !claims.HasRole("user") || !claims.HasRole("admin") {
		t.Fatalf("HasRole failed for %+v", claims.Groups)
	}
	if claims.HasRole("nope") {
		t.Fatalf("HasRole matched an absent role")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	iss := oidctest.NewIssuer(t)
	v := newVerifier(t, iss)

	raw := iss.SignToken(t, map[string]any{
		"preferred_username": "alice",
		"exp":                time.Now().Add(-time.Hour).Unix(),
	})
	if _, err := v.Verify(context.Background(), raw); err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

func TestVerifyRejectsWrongAudience(t *testing.T) {
	iss := oidctest.NewIssuer(t)
	v := newVerifier(t, iss)

	raw := iss.SignToken(t, map[string]any{
		"preferred_username": "alice",
		"aud":                "some-other-client",
	})
	if _, err := v.Verify(context.Background(), raw); err == nil {
		t.Fatal("expected error for wrong audience, got nil")
	}
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/oidc/ -run TestVerify -v`
Expected: PASS for all three tests.

- [ ] **Step 3: Commit**

```bash
git add internal/oidc/oidc_test.go
git commit -m "test(oidc): verifier valid/expired/wrong-audience"
```

---

## Task 5: Store `UpsertHumanActor`

**Files:**
- Modify: `internal/store/actors.go`
- Test: `internal/store/actors_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/store/actors_test.go` (white-box `package store` — use the existing `openTestStore(t)` helper and `t.Context()`, matching the other tests in this file; no new imports needed):

```go
func TestUpsertHumanActor(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	// Insert.
	if err := s.UpsertHumanActor(ctx, "alice", "Alice Example", false); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	a, err := s.GetActor(ctx, "alice")
	if err != nil {
		t.Fatalf("get actor: %v", err)
	}
	if a.Kind != "human" || a.DisplayName != "Alice Example" || a.Admin {
		t.Fatalf("after insert: %+v", a)
	}

	// Re-login promotes to admin and updates the display name.
	if err := s.UpsertHumanActor(ctx, "alice", "Alice E.", true); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	a, _ = s.GetActor(ctx, "alice")
	if !a.Admin || a.DisplayName != "Alice E." || a.Kind != "human" {
		t.Fatalf("after promote: %+v", a)
	}

	// Re-login demotes back to non-admin (demotion takes effect at next login).
	if err := s.UpsertHumanActor(ctx, "alice", "Alice E.", false); err != nil {
		t.Fatalf("third upsert: %v", err)
	}
	a, _ = s.GetActor(ctx, "alice")
	if a.Admin {
		t.Fatalf("after demote still admin: %+v", a)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run TestUpsertHumanActor`
Expected: FAIL — `st.UpsertHumanActor undefined`.

- [ ] **Step 3: Add the store method**

Add to `internal/store/actors.go` (after `CreateActor`):

```go
// UpsertHumanActor inserts a human actor, or on repeat login updates its
// display name and admin flag. Admin is re-synced on every login, so a Keycloak
// demotion takes effect the next time the user logs in. Kind is set to 'human'
// on insert and left unchanged on update.
func (s *Store) UpsertHumanActor(ctx context.Context, id, displayName string, admin bool) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO actors (id, kind, display_name, admin) VALUES (?, 'human', ?, ?)
		 ON CONFLICT (id) DO UPDATE SET display_name = excluded.display_name, admin = excluded.admin`,
		id, displayName, admin,
	)
	if err != nil {
		return fmt.Errorf("upsert human actor %s: %w", id, err)
	}
	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/store/ -run TestUpsertHumanActor -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/actors.go internal/store/actors_test.go
git commit -m "feat(store): UpsertHumanActor for SSO auto-provisioning"
```

---

## Task 6: Wire OIDC config into the server

**Files:**
- Modify: `internal/api/server.go`

- [ ] **Step 1: Add the config fields and server field**

In `internal/api/server.go`, extend `Config`:

```go
type Config struct {
	BootstrapToken      string            // WL_BOOTSTRAP_TOKEN: create the first admin actor if the store is empty
	GitHubWebhookSecret string            // WL_GITHUB_WEBHOOK_SECRET
	FluxWebhookSecret   string            // WL_FLUX_WEBHOOK_SECRET
	ClusterEnvMap       map[string]string // WL_CLUSTER_ENV_MAP: cluster name -> environment

	// OIDC/SSO. The feature is off unless OIDCIssuer and OIDCClientID are both
	// set; unset behaves exactly as before. SessionSecret is required when OIDC
	// is enabled (Plan 2's web sessions sign cookies with it). PublicURL is the
	// external base URL used to build the web callback redirect URI.
	OIDCIssuer    string // WL_OIDC_ISSUER
	OIDCClientID  string // WL_OIDC_CLIENT_ID
	PublicURL     string // WL_PUBLIC_URL
	SessionSecret string // WL_SESSION_SECRET
}
```

Add an `oidc` field to `server` (after `log *slog.Logger`):

```go
	// oidc is nil unless OIDC is configured (issuer + client id set). All SSO
	// routes 404 when it is nil.
	oidc *oidc.Verifier
```

Add the import to the block:

```go
	"github.com/sunstoneinstitute/worklode/internal/oidc"
```

- [ ] **Step 2: Build the verifier in `NewServer`**

In `NewServer`, immediately after the `s := &server{...}` literal (before the bootstrap block), add:

```go
	if cfg.OIDCIssuer != "" && cfg.OIDCClientID != "" {
		if cfg.SessionSecret == "" {
			return nil, fmt.Errorf("WL_SESSION_SECRET is required when OIDC is enabled")
		}
		v, err := oidc.New(context.Background(), cfg.OIDCIssuer, cfg.OIDCClientID)
		if err != nil {
			return nil, fmt.Errorf("configure oidc: %w", err)
		}
		s.oidc = v
	}
```

- [ ] **Step 3: Register the two SSO endpoints**

In `NewServer`, after the `/hooks/flux` registration and before the `/api/v1/tasks` routes, add:

```go
	// SSO token exchange + config discovery for the CLI login flow. Registered
	// outside the /api/v1 bearer-auth middleware, like /healthz and /hooks/*.
	// Both 404 when OIDC is unconfigured (s.oidc == nil).
	mux.HandleFunc("GET /auth/oidc/config", s.oidcConfig)
	mux.HandleFunc("POST /auth/oidc/token", s.oidcTokenExchange)
```

- [ ] **Step 4: Verify it compiles**

Run: `go build ./...`
Expected: FAIL — `s.oidcConfig` / `s.oidcTokenExchange` undefined (added in Task 7). This is expected; proceed to Task 7 before running tests.

---

## Task 7: `/auth/oidc/config` and `/auth/oidc/token` handlers

**Files:**
- Create: `internal/api/oidcauth.go`
- Test: `internal/api/oidcauth_test.go`

- [ ] **Step 1: Write the handlers and the shared `provisionActor` helper**

Create `internal/api/oidcauth.go`:

```go
// oidcauth.go implements the unauthenticated SSO endpoints that mint wl_
// tokens from a Keycloak identity: GET /auth/oidc/config (so the CLI can
// discover the issuer/client without its own config) and POST /auth/oidc/token
// (validate an ID token, auto-provision the human actor, mint a 30-day token).
// Both 404 when OIDC is unconfigured. provisionActor is shared with the web
// callback (Plan 2).
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/oidc"
)

// ssoTokenTTL is the lifetime of a wl_ token minted from an SSO login. No
// refresh tokens — re-run `wl login` after expiry.
const ssoTokenTTL = 30 * 24 * time.Hour

// errNoUserRole is returned by provisionActor when the ID token's groups lack
// the required "user" client role.
var errNoUserRole = errors.New("missing user role")

// provisionActor enforces the "user" role and upserts the human actor from the
// verified claims, syncing the admin flag from the "admin" role. It returns the
// provisioned actor id (the preferred_username). Shared by the token-exchange
// endpoint and the web callback.
func (s *server) provisionActor(ctx context.Context, c *oidc.Claims) (string, error) {
	if !c.HasRole("user") {
		return "", errNoUserRole
	}
	if err := s.st.UpsertHumanActor(ctx, c.PreferredUsername, c.Name, c.HasRole("admin")); err != nil {
		return "", err
	}
	return c.PreferredUsername, nil
}

// oidcConfig handles GET /auth/oidc/config: the issuer and client id the CLI
// needs to run the auth-code flow itself. 404 when OIDC is unconfigured.
func (s *server) oidcConfig(w http.ResponseWriter, _ *http.Request) {
	if s.oidc == nil {
		writeErr(w, http.StatusNotFound, "oidc not configured")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"issuer":    s.oidc.Issuer(),
		"client_id": s.oidc.ClientID(),
	})
}

type oidcTokenRequest struct {
	IDToken string `json:"id_token"`
}

// oidcTokenExchange handles POST /auth/oidc/token: verify a Keycloak ID token
// and mint a wl_ token for the corresponding human actor. 404 when OIDC is
// unconfigured; 401 for an invalid/expired/wrong-audience or malformed token;
// 403 when the "user" role is absent.
func (s *server) oidcTokenExchange(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeErr(w, http.StatusNotFound, "oidc not configured")
		return
	}
	var req oidcTokenRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.IDToken == "" {
		writeErr(w, http.StatusBadRequest, "id_token is required")
		return
	}

	claims, err := s.oidc.Verify(r.Context(), req.IDToken)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid id token")
		return
	}
	if claims.PreferredUsername == "" {
		writeErr(w, http.StatusUnauthorized, "id token missing preferred_username")
		return
	}

	actorID, err := s.provisionActor(r.Context(), claims)
	if errors.Is(err, errNoUserRole) {
		writeErr(w, http.StatusForbidden, "the worklode user role is required")
		return
	}
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	now := s.st.Now()
	exp := now.Add(ssoTokenTTL)
	desc := fmt.Sprintf("sso login for %s at %s", actorID, now.Format(time.RFC3339))
	token, err := s.st.CreateToken(r.Context(), actorID, desc, &exp)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"token":      token,
		"actor_id":   actorID,
		"expires_at": exp.UTC().Format(time.RFC3339),
	})
}
```

The `context` import is used by `provisionActor`'s `ctx context.Context` parameter; the `errors`, `fmt`, `net/http`, `time`, and `internal/oidc` imports are all used as shown.

- [ ] **Step 2: Write the endpoint tests**

Create `internal/api/oidcauth_test.go`:

```go
package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/oidc/oidctest"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// newOIDCServer stands up a store + server wired to a fake issuer. It returns
// the store, the handler, and the fake issuer so tests can mint ID tokens.
func newOIDCServer(t *testing.T) (*store.Store, http.Handler, *oidctest.Issuer) {
	t.Helper()
	st := newTestStore(t)
	iss := oidctest.NewIssuer(t)
	h, err := api.NewServer(st, api.Config{
		OIDCIssuer:    iss.URL(),
		OIDCClientID:  iss.ClientID,
		PublicURL:     "http://localhost:8080",
		SessionSecret: "test-session-secret",
	})
	if err != nil {
		t.Fatalf("new oidc server: %v", err)
	}
	return st, h, iss
}

func TestOIDCConfig(t *testing.T) {
	_, h, iss := newOIDCServer(t)
	rr := doReq(t, h, "GET", "/auth/oidc/config", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	m := decodeMap(t, rr)
	if m["issuer"] != iss.URL() || m["client_id"] != iss.ClientID {
		t.Fatalf("config = %v", m)
	}
}

func TestOIDCConfig404WhenUnconfigured(t *testing.T) {
	_, h, _ := newTestServer(t) // no OIDC
	rr := doReq(t, h, "GET", "/auth/oidc/config", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestOIDCTokenExchangeMintsToken(t *testing.T) {
	st, h, iss := newOIDCServer(t)
	raw := iss.SignToken(t, map[string]any{
		"preferred_username": "bob",
		"name":               "Bob Example",
		"groups":             []string{"user"},
	})
	rr := doReq(t, h, "POST", "/auth/oidc/token", "", map[string]string{"id_token": raw})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	m := decodeMap(t, rr)
	if m["actor_id"] != "bob" {
		t.Fatalf("actor_id = %v", m["actor_id"])
	}
	token, _ := m["token"].(string)
	if token == "" {
		t.Fatal("empty token")
	}
	// The minted token authenticates as the provisioned actor.
	a, err := st.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("authenticate minted token: %v", err)
	}
	if a.ID != "bob" || a.Kind != "human" || a.Admin {
		t.Fatalf("actor = %+v", a)
	}
}

func TestOIDCTokenExchangeAdminSyncsOnAndOff(t *testing.T) {
	st, h, iss := newOIDCServer(t)
	ctx := context.Background()

	// First login with admin role -> Admin true.
	raw := iss.SignToken(t, map[string]any{
		"preferred_username": "carol", "name": "Carol", "groups": []string{"user", "admin"},
	})
	rr := doReq(t, h, "POST", "/auth/oidc/token", "", map[string]string{"id_token": raw})
	if rr.Code != http.StatusCreated {
		t.Fatalf("first login status = %d, body %s", rr.Code, rr.Body.String())
	}
	a, _ := st.GetActor(ctx, "carol")
	if !a.Admin {
		t.Fatal("expected admin after first login")
	}

	// Second login without admin role -> Admin false (demotion at next login).
	raw = iss.SignToken(t, map[string]any{
		"preferred_username": "carol", "name": "Carol", "groups": []string{"user"},
	})
	rr = doReq(t, h, "POST", "/auth/oidc/token", "", map[string]string{"id_token": raw})
	if rr.Code != http.StatusCreated {
		t.Fatalf("second login status = %d, body %s", rr.Code, rr.Body.String())
	}
	a, _ = st.GetActor(ctx, "carol")
	if a.Admin {
		t.Fatal("expected non-admin after second login")
	}
}

func TestOIDCTokenExchangeRequiresUserRole(t *testing.T) {
	_, h, iss := newOIDCServer(t)
	raw := iss.SignToken(t, map[string]any{
		"preferred_username": "dan", "name": "Dan", "groups": []string{"other"},
	})
	rr := doReq(t, h, "POST", "/auth/oidc/token", "", map[string]string{"id_token": raw})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", rr.Code, rr.Body.String())
	}
}

func TestOIDCTokenExchangeRejectsExpired(t *testing.T) {
	_, h, iss := newOIDCServer(t)
	raw := iss.SignToken(t, map[string]any{
		"preferred_username": "eve", "groups": []string{"user"},
		"exp": int64(1), // 1970 — long expired
	})
	rr := doReq(t, h, "POST", "/auth/oidc/token", "", map[string]string{"id_token": raw})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %s", rr.Code, rr.Body.String())
	}
}

func TestOIDCTokenExchangeRejectsWrongAudience(t *testing.T) {
	_, h, iss := newOIDCServer(t)
	raw := iss.SignToken(t, map[string]any{
		"preferred_username": "frank", "groups": []string{"user"},
		"aud": "some-other-client",
	})
	rr := doReq(t, h, "POST", "/auth/oidc/token", "", map[string]string{"id_token": raw})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %s", rr.Code, rr.Body.String())
	}
}

func TestOIDCTokenExchange404WhenUnconfigured(t *testing.T) {
	_, h, _ := newTestServer(t) // no OIDC
	rr := doReq(t, h, "POST", "/auth/oidc/token", "", map[string]string{"id_token": "x"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}
```

- [ ] **Step 3: Run the tests**

Run: `go vet ./internal/api/ && go test ./internal/api/ -run TestOIDC -v`
Expected: PASS for all `TestOIDC*` tests, no vet complaints.

- [ ] **Step 4: Run the full API + store + oidc suites**

Run: `go test ./internal/api/... ./internal/store/... ./internal/oidc/...`
Expected: ok for all three.

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go internal/api/oidcauth.go internal/api/oidcauth_test.go
git commit -m "feat(api): /auth/oidc/config and /auth/oidc/token endpoints"
```

---

## Task 8: Serve command env wiring

**Files:**
- Modify: `internal/cmd/serve.go`

- [ ] **Step 1: Pass the new env vars into the config**

In `internal/cmd/serve.go`, extend the `api.Config` literal in `RunE`:

```go
			handler, err := api.NewServer(st, api.Config{
				BootstrapToken:      os.Getenv("WL_BOOTSTRAP_TOKEN"),
				GitHubWebhookSecret: os.Getenv("WL_GITHUB_WEBHOOK_SECRET"),
				FluxWebhookSecret:   os.Getenv("WL_FLUX_WEBHOOK_SECRET"),
				ClusterEnvMap:       parseClusterEnvMap(os.Getenv("WL_CLUSTER_ENV_MAP")),
				OIDCIssuer:          os.Getenv("WL_OIDC_ISSUER"),
				OIDCClientID:        os.Getenv("WL_OIDC_CLIENT_ID"),
				PublicURL:           os.Getenv("WL_PUBLIC_URL"),
				SessionSecret:       os.Getenv("WL_SESSION_SECRET"),
			})
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./...`
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/serve.go
git commit -m "feat(serve): wire WL_OIDC_* env into server config"
```

---

## Task 9: Document the env vars

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add an SSO env section**

Add a short subsection to `README.md` near the existing env/config documentation (after the webhook env vars). Keep it terse:

```markdown
### SSO (optional)

Human login via the org Keycloak is off unless both `WL_OIDC_ISSUER` and
`WL_OIDC_CLIENT_ID` are set; unset behaves as before (tokens minted only by an
admin or the bootstrap token). When enabled:

| Var | Meaning |
|---|---|
| `WL_OIDC_ISSUER` | e.g. `https://auth.sunstoneinstitute.ai/realms/sunstone` |
| `WL_OIDC_CLIENT_ID` | e.g. `worklode` |
| `WL_PUBLIC_URL` | external base URL, for the web login callback |
| `WL_SESSION_SECRET` | HMAC key for web session cookies (required when OIDC is enabled) |

Users then run `wl login` to obtain a 30-day `wl_` token from their SSO
identity. Agent/service tokens are unchanged.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs(readme): document SSO env vars"
```

---

## Task 10: Full verification

- [ ] **Step 1: Run the entire test suite**

Run: `go test ./...`
Expected: ok for every package (the existing `newTestServer`-based tests still pass because `api.Config{}` leaves `s.oidc` nil).

- [ ] **Step 2: Vet and build**

Run: `go vet ./... && go build ./...`
Expected: no output, exit 0.

---

## Self-Review Notes

- **Spec coverage:** ID-token verification (go-oidc, iss/aud/exp) — Task 2; `groups`-claim role checks (`user` required, `admin` synced) — Tasks 2 & 7; auto-provision human actor (id=`preferred_username`, name=`name`) — Tasks 5 & 7; token exchange endpoint (401/403/404, 30-day token, description) — Task 7; feature flag (off unless issuer+client set) — Task 6; env vars — Tasks 8 & 9; fake-issuer tests (valid/missing-role/admin-sync/expired/wrong-aud/404) — Task 7.
- **Deferred to later plans:** web sessions (Plan 2) and `wl login` CLI (Plan 3). The `GET /auth/oidc/config` endpoint added here is consumed by Plan 3; the `provisionActor` helper is reused by Plan 2.
- **Role-string coupling:** `HasRole` matches the exact strings `"user"`/`"admin"` in the `groups` claim, per the design's token-exchange pseudocode. If the deployed `client-roles-as-groups` mapper emits namespaced values (e.g. `worklode:user`), align the constants at that point — this is the single config-coupling seam.
