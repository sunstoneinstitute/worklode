// oidcauth.go implements the unauthenticated SSO endpoints that mint wt_
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

	"github.com/sunstoneinstitute/work-tracker/internal/oidc"
)

// ssoTokenTTL is the lifetime of a wt_ token minted from an SSO login. No
// refresh tokens — re-run `wt login` after expiry.
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
// and mint a wt_ token for the corresponding human actor. 404 when OIDC is
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
		writeErr(w, http.StatusForbidden, "the work-tracker user role is required")
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
