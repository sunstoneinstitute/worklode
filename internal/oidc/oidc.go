// Package oidc wraps go-oidc/oauth2 for work-tracker's SSO flows: it verifies
// Keycloak ID tokens and builds the oauth2 config the web and CLI login flows
// share. A Verifier is constructed only when WT_OIDC_ISSUER and
// WT_OIDC_CLIENT_ID are set; an unconfigured server never builds one.
package oidc

import (
	"context"
	"fmt"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Claims are the ID-token claims work-tracker consumes.
type Claims struct {
	PreferredUsername string   `json:"preferred_username"`
	Name              string   `json:"name"`
	Groups            []string `json:"groups"`
}

// HasRole reports whether role is present in the groups claim. Keycloak's
// client-roles-as-groups mapper delivers the work-tracker client roles
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
// then extracts the claims work-tracker uses.
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
