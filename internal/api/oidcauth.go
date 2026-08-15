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

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/oidc"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// ssoTokenTTL is the lifetime of a wl_ token minted from an SSO login. No
// refresh tokens — re-run `lode login` after expiry.
const ssoTokenTTL = 30 * 24 * time.Hour

// errNoUserRole is returned by provisionActor when the ID token's groups lack
// the required "user" client role.
var errNoUserRole = errors.New("missing user role")

// errActorKindConflict is returned by provisionActor when preferred_username
// collides with an existing non-human actor (e.g. the bootstrap admin service
// actor). Provisioning would otherwise overwrite that actor via the upsert.
var errActorKindConflict = errors.New("actor id is reserved by a non-human actor")

// provisionActor enforces the "user" role and upserts the human actor from the
// verified claims, syncing the admin flag from the "admin" role and the
// expected GitHub login from the github_username claim (spec 001 §9.2, empty
// when Keycloak asserts none). It returns the provisioned actor id (the
// preferred_username). Shared by the token-exchange endpoint and the web
// callback.
func (s *server) provisionActor(ctx context.Context, c *oidc.Claims) (string, error) {
	if !c.HasRole("user") {
		return "", errNoUserRole
	}
	// Refuse to clobber a non-human actor (notably the bootstrap admin) whose id
	// collides with this preferred_username. The store is single-writer, so a
	// pre-existing conflicting row is always visible here.
	existing, err := s.st.GetActor(ctx, c.PreferredUsername)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	if existing != nil && existing.Kind != "human" {
		return "", errActorKindConflict
	}
	if err := s.st.UpsertHumanActor(ctx, c.PreferredUsername, c.Name, c.HasRole("admin"), c.GitHubUsername); err != nil {
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

// oidcTokenExchange handles POST /auth/oidc/token: verify a Keycloak ID token
// and mint a wl_ token for the corresponding human actor. 404 when OIDC is
// unconfigured; 401 for an invalid/expired/wrong-audience or malformed token;
// 403 when the "user" role is absent.
func (s *server) oidcTokenExchange(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeErr(w, http.StatusNotFound, "oidc not configured")
		return
	}
	var req model.OIDCTokenInput
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
	if errors.Is(err, errActorKindConflict) {
		writeErr(w, http.StatusConflict, "actor id conflicts with an existing non-human actor")
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
