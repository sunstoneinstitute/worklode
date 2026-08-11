// oidcweb.go gates the read-only web UI behind Keycloak, worklode's sole
// interactive login provider (spec 023 §3.1):
//   - webAuth wraps each web page and, when unauthenticated, 302s to
//     loginTarget (/auth/login). It is a passthrough only when OIDC is
//     unconfigured (the UI stays open, as in v1).
//   - GET /auth/login starts an auth-code + PKCE flow: it sets a signed
//     oauth-state cookie and redirects to Keycloak's authorize URL.
//   - GET /auth/callback redeems the code, verifies the ID token, provisions
//     the actor (shared provisionActor), sets the session cookie, and 302s to
//     the originally requested page.
//
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
func randToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// safeNext returns next only if it is a safe same-origin path (single leading
// slash, not a "//" scheme-relative URL, no backslash); otherwise "/". Guards
// the ?next parameter against open redirects.
func safeNext(next string) string {
	if next == "" || next[0] != '/' || strings.HasPrefix(next, "//") || strings.Contains(next, "\\") {
		return "/"
	}
	return next
}

// callbackURL is the web redirect URI, derived from the configured public URL.
func (s *server) callbackURL() string {
	return strings.TrimRight(s.cfg.PublicURL, "/") + "/auth/callback"
}

// loginTarget returns where webAuth sends unauthenticated users. Keycloak is
// worklode's only interactive login provider (spec 023 §3.1); the dormant
// GitHub App OAuth client (s.gh, spec 023 §3.3) never affects this.
func (s *server) loginTarget(next string) string {
	return "/auth/login?next=" + url.QueryEscape(next)
}

// webAuth wraps a web page handler with session-cookie enforcement. It is a
// passthrough only when OIDC is disabled. Unauthenticated requests 302 to
// loginTarget with the current path preserved in ?next.
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
		http.Redirect(w, r, s.loginTarget(r.URL.Path), http.StatusFound)
	}
}

// authLogin handles GET /auth/login: begin the auth-code + PKCE flow.
func (s *server) authLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	next := safeNext(r.URL.Query().Get("next"))

	state, err := randToken()
	if err != nil {
		s.log.Error("generate login state", "err", err)
		webErr(w, http.StatusInternalServerError, "internal error")
		return
	}
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
		s.log.Error("oidc id token verify", "err", err)
		webErr(w, http.StatusUnauthorized, "invalid id token")
		return
	}
	if claims.PreferredUsername == "" {
		webErr(w, http.StatusUnauthorized, "id token missing preferred_username")
		return
	}

	username, err := s.provisionActor(r.Context(), claims)
	if errors.Is(err, errNoUserRole) {
		webErr(w, http.StatusForbidden, "the worklode user role is required")
		return
	}
	if errors.Is(err, errActorKindConflict) {
		webErr(w, http.StatusConflict, "actor id conflicts with an existing non-human actor")
		return
	}
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	s.finishLogin(w, r, username, st.Next)
}
