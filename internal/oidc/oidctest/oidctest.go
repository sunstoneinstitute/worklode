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
// "work-tracker".
func NewIssuer(t *testing.T) *Issuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	iss := &Issuer{Server: srv, ClientID: "work-tracker", key: key}

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
