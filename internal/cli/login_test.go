package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/work-tracker/internal/cli"
	"github.com/sunstoneinstitute/work-tracker/internal/oidc/oidctest"
)

func TestRunLogin(t *testing.T) {
	// Fake Keycloak: its /token endpoint returns an ID token for "heidi".
	iss := oidctest.NewIssuer(t)
	iss.TokenClaims = map[string]any{
		"preferred_username": "heidi", "name": "Heidi Example",
		"aud": iss.ClientID, "groups": []string{"user"},
	}

	// Stub work-tracker server: config discovery + token exchange.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/oidc/config", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": iss.URL(), "client_id": iss.ClientID})
	})
	mux.HandleFunc("POST /auth/oidc/token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["id_token"] == "" {
			http.Error(w, `{"error":"id_token is required"}`, http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"token":      "wt_" + strings.Repeat("ab", 20),
			"actor_id":   "heidi",
			"expires_at": "2026-08-18T00:00:00Z",
		})
	})
	wt := httptest.NewServer(mux)
	defer wt.Close()

	// Instead of opening a browser, drive the callback directly: parse the
	// redirect_uri and state out of the authorize URL and GET the callback.
	openBrowser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		redir := u.Query().Get("redirect_uri")
		state := u.Query().Get("state")
		go func() {
			resp, err := http.Get(redir + "?code=fake-code&state=" + url.QueryEscape(state))
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}

	res, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server:      wt.URL,
		OpenBrowser: openBrowser,
		Ports:       []int{0}, // ephemeral port, avoids collisions with a real 8000
	})
	if err != nil {
		t.Fatalf("RunLogin: %v", err)
	}
	if res.ActorID != "heidi" {
		t.Fatalf("actor id = %q, want heidi", res.ActorID)
	}
	if !strings.HasPrefix(res.Token, "wt_") {
		t.Fatalf("token = %q, want wt_ prefix", res.Token)
	}
	if res.ExpiresAt != "2026-08-18T00:00:00Z" {
		t.Fatalf("expires_at = %q", res.ExpiresAt)
	}
}

func TestRunLoginServerWithoutSSO(t *testing.T) {
	// A server whose /auth/oidc/config 404s (SSO disabled) yields a clear error.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/oidc/config", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"oidc not configured"}`, http.StatusNotFound)
	})
	wt := httptest.NewServer(mux)
	defer wt.Close()

	_, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server:      wt.URL,
		OpenBrowser: func(string) error { return nil },
		Ports:       []int{0},
	})
	if err == nil || !strings.Contains(err.Error(), "SSO") {
		t.Fatalf("err = %v, want an SSO-disabled error", err)
	}
}

// stubWTServer returns a work-tracker stub whose /auth/oidc/config points at
// iss and whose POST /auth/oidc/token is handled by tokenHandler.
func stubWTServer(t *testing.T, iss *oidctest.Issuer, tokenHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/oidc/config", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": iss.URL(), "client_id": iss.ClientID})
	})
	mux.HandleFunc("POST /auth/oidc/token", tokenHandler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestRunLoginConcurrentCallbacks reproduces the callback-deadlock bug: several
// concurrent hits to the loopback callback must not wedge srv.Shutdown, so
// RunLogin still returns a valid result.
func TestRunLoginConcurrentCallbacks(t *testing.T) {
	iss := oidctest.NewIssuer(t)
	iss.TokenClaims = map[string]any{
		"preferred_username": "heidi", "aud": iss.ClientID, "groups": []string{"user"},
	}
	wt := stubWTServer(t, iss, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"token": "wt_" + strings.Repeat("ab", 20), "actor_id": "heidi", "expires_at": "2026-08-18T00:00:00Z",
		})
	})

	// Fire several concurrent GETs at the callback with the correct state.
	openBrowser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		redir := u.Query().Get("redirect_uri")
		state := u.Query().Get("state")
		target := redir + "?code=fake-code&state=" + url.QueryEscape(state)
		for i := 0; i < 5; i++ {
			go func() {
				if resp, err := http.Get(target); err == nil {
					resp.Body.Close()
				}
			}()
		}
		return nil
	}

	res, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server: wt.URL, OpenBrowser: openBrowser, Ports: []int{0},
	})
	if err != nil {
		t.Fatalf("RunLogin: %v", err)
	}
	if res.ActorID != "heidi" || !strings.HasPrefix(res.Token, "wt_") {
		t.Fatalf("result = %+v, want heidi with wt_ token", res)
	}
}

// TestRunLoginTokenEndpointError asserts a non-2xx from /auth/oidc/token
// surfaces as a *cli.ClientError carrying the status.
func TestRunLoginTokenEndpointError(t *testing.T) {
	iss := oidctest.NewIssuer(t)
	iss.TokenClaims = map[string]any{"preferred_username": "heidi", "aud": iss.ClientID}
	wt := stubWTServer(t, iss, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"the work-tracker user role is required"}`, http.StatusForbidden)
	})

	openBrowser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		redir := u.Query().Get("redirect_uri")
		state := u.Query().Get("state")
		go func() {
			if resp, err := http.Get(redir + "?code=fake-code&state=" + url.QueryEscape(state)); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}

	_, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server: wt.URL, OpenBrowser: openBrowser, Ports: []int{0},
	})
	var ce *cli.ClientError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v (%T), want *cli.ClientError", err, err)
	}
	if ce.Status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", ce.Status)
	}
}

// TestRunLoginStateMismatch asserts a callback whose state does not match the
// authorize URL's state fails with a state error rather than logging in.
func TestRunLoginStateMismatch(t *testing.T) {
	iss := oidctest.NewIssuer(t)
	iss.TokenClaims = map[string]any{"preferred_username": "heidi", "aud": iss.ClientID}
	wt := stubWTServer(t, iss, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "wt_x", "actor_id": "heidi"})
	})

	openBrowser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		redir := u.Query().Get("redirect_uri")
		go func() {
			if resp, err := http.Get(redir + "?code=fake-code&state=not-the-right-state"); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}

	_, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server: wt.URL, OpenBrowser: openBrowser, Ports: []int{0},
	})
	if err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatalf("err = %v, want a state-mismatch error", err)
	}
}
