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

// TestNewServerRequiresPublicURLWhenOIDC verifies NewServer fails fast when
// OIDC is enabled but PublicURL is missing or not an absolute http(s) URL —
// otherwise callbackURL() silently builds a relative redirect URI that
// Keycloak rejects, making the web UI unloggable while the CLI flow keeps
// working.
func TestNewServerRequiresPublicURLWhenOIDC(t *testing.T) {
	st := newTestStore(t)
	iss := oidctest.NewIssuer(t)

	for _, publicURL := range []string{"", "/auth"} {
		_, err := api.NewServer(st, api.Config{
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

func TestOIDCTokenExchangeRejectsMissingIDToken(t *testing.T) {
	_, h, _ := newOIDCServer(t)
	rr := doReq(t, h, "POST", "/auth/oidc/token", "", map[string]string{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

func TestOIDCTokenExchangeActorKindConflict(t *testing.T) {
	st, h, iss := newOIDCServer(t)
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
