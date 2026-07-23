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
