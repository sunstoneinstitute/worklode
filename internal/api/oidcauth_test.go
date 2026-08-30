package api_test

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/oidc/oidctest"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// newOIDCServer stands up a store + server wired to a fake issuer. It returns
// the store, the handler, and the fake issuer so tests can mint ID tokens.
// cfg carries any extra configuration the test needs; the OIDC fields are
// filled in over it, since a server without them is not what this helper
// builds. Pass api.Config{} when there is nothing extra to say.
func newOIDCServer(t *testing.T, cfg api.Config) (*store.Store, http.Handler, *oidctest.Issuer) {
	t.Helper()
	st := newTestStore(t)
	iss := oidctest.NewIssuer(t)
	cfg.OIDCIssuer = iss.URL()
	cfg.OIDCClientID = iss.ClientID
	cfg.PublicURL = "http://localhost:8080"
	cfg.SessionSecret = "test-session-secret"
	h, _, err := api.NewServer(st, cfg)
	if err != nil {
		t.Fatalf("new oidc server: %v", err)
	}
	return st, h, iss
}

// newOIDCServerWithAdmin is newOIDCServer plus the admin handler (/metrics),
// for tests that drive a session-gated route and then read a counter back out.
func newOIDCServerWithAdmin(t *testing.T) (*store.Store, http.Handler, http.Handler, *oidctest.Issuer) {
	t.Helper()
	st := newTestStore(t)
	iss := oidctest.NewIssuer(t)
	main, admin, err := api.NewServer(st, api.Config{
		OIDCIssuer:    iss.URL(),
		OIDCClientID:  iss.ClientID,
		PublicURL:     "http://localhost:8080",
		SessionSecret: "test-session-secret",
	})
	if err != nil {
		t.Fatalf("new oidc server: %v", err)
	}
	return st, main, admin, iss
}

// newOIDCServerAdmin is like newOIDCServer but returns the admin handler
// (/healthz, /metrics) rather than the public app handler.
func newOIDCServerAdmin(t *testing.T) http.Handler {
	t.Helper()
	st := newTestStore(t)
	iss := oidctest.NewIssuer(t)
	_, admin, err := api.NewServer(st, api.Config{
		OIDCIssuer:    iss.URL(),
		OIDCClientID:  iss.ClientID,
		PublicURL:     "http://localhost:8080",
		SessionSecret: "test-session-secret",
	})
	if err != nil {
		t.Fatalf("new oidc server: %v", err)
	}
	return admin
}

// TestNewServerRequiresPublicURLWhenOIDC verifies NewServer fails fast when
// OIDC is enabled but PublicURL is missing or not an absolute http(s) URL —
// otherwise callbackURL() silently builds a relative redirect URI that
// Keycloak rejects, making the web UI unloggable while the CLI flow keeps
// working.
func TestNewServerRequiresPublicURLWhenOIDC(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	iss := oidctest.NewIssuer(t)

	for _, publicURL := range []string{"", "/auth"} {
		_, _, err := api.NewServer(st, api.Config{
			OIDCIssuer:    iss.URL(),
			OIDCClientID:  iss.ClientID,
			PublicURL:     publicURL,
			SessionSecret: "test-session-secret",
		})
		if err == nil {
			t.Fatalf("PublicURL %q: expected error, got nil", publicURL)
		}
	}
}

// TestNewServerAcceptsGitHubWithoutOrg asserts NewServer succeeds with the
// dormant GitHub App OAuth client configured (spec 001 §9.3) and no org
// setting: the org-membership guard that used to gate the GitHub login flow
// is gone along with that flow (spec 001 §3).
func TestNewServerAcceptsGitHubWithoutOrg(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	iss := oidctest.NewIssuer(t)
	_, _, err := api.NewServer(st, api.Config{
		OIDCIssuer:         iss.URL(),
		OIDCClientID:       iss.ClientID,
		PublicURL:          "http://localhost:8080",
		SessionSecret:      "test-session-secret",
		GitHubClientID:     "cid",
		GitHubClientSecret: "secret",
		TokenEncKey:        "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
}

func TestOIDCConfig(t *testing.T) {
	t.Parallel()
	_, h, iss := newOIDCServer(t, api.Config{})
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
	t.Parallel()
	_, h, _ := newTestServer(t) // no OIDC
	rr := doReq(t, h, "GET", "/auth/oidc/config", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestOIDCTokenExchangeMintsToken(t *testing.T) {
	t.Parallel()
	st, h, iss := newOIDCServer(t, api.Config{})
	raw := iss.SignToken(t, map[string]any{
		"preferred_username": "bob",
		"name":               "Bob Example",
		"email":              "bob@example.org",
		"groups":             []string{"user", "science-lead"},
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
	a, _, err := st.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("authenticate minted token: %v", err)
	}
	if a.ID != "bob" || a.Kind != "human" || a.Admin {
		t.Fatalf("actor = %+v", a)
	}

	// provisionActor stores the full email and groups claims (spec 029 §6.2).
	got, err := st.GetActor(context.Background(), "bob")
	if err != nil {
		t.Fatalf("get actor: %v", err)
	}
	if got.Email != "bob@example.org" || !slices.Equal(got.Groups, []string{"user", "science-lead"}) {
		t.Fatalf("claims not stored: %+v", got)
	}
}

func TestOIDCTokenExchangeAdminSyncsOnAndOff(t *testing.T) {
	t.Parallel()
	st, h, iss := newOIDCServer(t, api.Config{})
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

// TestOIDCTokenExchangeSyncsGitHubUsername asserts expected_github_login is
// re-synced on every login exactly like the admin flag (spec 001 §9.2): a
// login carrying github_username sets it, and a later login without the
// claim clears it back to empty while still succeeding (201).
func TestOIDCTokenExchangeSyncsGitHubUsername(t *testing.T) {
	t.Parallel()
	st, h, iss := newOIDCServer(t, api.Config{})
	ctx := context.Background()

	raw := iss.SignToken(t, map[string]any{
		"preferred_username": "heidi", "name": "Heidi", "groups": []string{"user"},
		"github_username": "hheidi",
	})
	rr := doReq(t, h, "POST", "/auth/oidc/token", "", map[string]string{"id_token": raw})
	if rr.Code != http.StatusCreated {
		t.Fatalf("first login status = %d, body %s", rr.Code, rr.Body.String())
	}
	a, err := st.GetActor(ctx, "heidi")
	if err != nil {
		t.Fatalf("get actor: %v", err)
	}
	if a.ExpectedGitHubLogin != "hheidi" {
		t.Fatalf("ExpectedGitHubLogin = %q, want %q", a.ExpectedGitHubLogin, "hheidi")
	}

	// Second login without the claim clears it, and still returns 201.
	raw = iss.SignToken(t, map[string]any{
		"preferred_username": "heidi", "name": "Heidi", "groups": []string{"user"},
	})
	rr = doReq(t, h, "POST", "/auth/oidc/token", "", map[string]string{"id_token": raw})
	if rr.Code != http.StatusCreated {
		t.Fatalf("second login status = %d, body %s", rr.Code, rr.Body.String())
	}
	a, err = st.GetActor(ctx, "heidi")
	if err != nil {
		t.Fatalf("get actor: %v", err)
	}
	if a.ExpectedGitHubLogin != "" {
		t.Fatalf("ExpectedGitHubLogin after clear = %q, want empty", a.ExpectedGitHubLogin)
	}
}

func TestOIDCTokenExchangeRequiresUserRole(t *testing.T) {
	t.Parallel()
	_, h, iss := newOIDCServer(t, api.Config{})
	raw := iss.SignToken(t, map[string]any{
		"preferred_username": "dan", "name": "Dan", "groups": []string{"other"},
	})
	rr := doReq(t, h, "POST", "/auth/oidc/token", "", map[string]string{"id_token": raw})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", rr.Code, rr.Body.String())
	}
}

func TestOIDCTokenExchangeRejectsMissingIDToken(t *testing.T) {
	t.Parallel()
	_, h, _ := newOIDCServer(t, api.Config{})
	rr := doReq(t, h, "POST", "/auth/oidc/token", "", map[string]string{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

func TestOIDCTokenExchangeActorKindConflict(t *testing.T) {
	t.Parallel()
	st, h, iss := newOIDCServer(t, api.Config{})
	ctx := context.Background()

	// Pre-create a non-human actor whose id collides with the login username.
	if err := st.CreateActor(ctx, "admin", "service", "bootstrap admin", true); err != nil {
		t.Fatalf("create actor: %v", err)
	}

	raw := iss.SignToken(t, map[string]any{
		"preferred_username": "admin", "name": "Impostor", "groups": []string{"user"},
	})
	rr := doReq(t, h, "POST", "/auth/oidc/token", "", map[string]string{"id_token": raw})
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", rr.Code, rr.Body.String())
	}

	// The pre-existing actor must be untouched.
	a, err := st.GetActor(ctx, "admin")
	if err != nil {
		t.Fatalf("get actor: %v", err)
	}
	if a.Kind != "service" || !a.Admin || a.DisplayName != "bootstrap admin" {
		t.Fatalf("pre-existing actor was modified: %+v", a)
	}
}

func TestOIDCTokenExchangeRejectsExpired(t *testing.T) {
	t.Parallel()
	_, h, iss := newOIDCServer(t, api.Config{})
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
	t.Parallel()
	_, h, iss := newOIDCServer(t, api.Config{})
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
	t.Parallel()
	_, h, _ := newTestServer(t) // no OIDC
	rr := doReq(t, h, "POST", "/auth/oidc/token", "", map[string]string{"id_token": "x"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}
