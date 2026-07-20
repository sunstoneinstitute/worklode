// githubweb.go adds "Sign in with GitHub" as a second web identity provider,
// alongside the Keycloak flow in oidcweb.go (which is untouched):
//   - GET /auth/github/login starts the GitHub App user-authorization flow.
//   - GET /auth/github/callback redeems the code, reads identity + org/team
//     membership, provisions a github:<id> actor, stores the encrypted
//     user-to-server token, and sets the same session cookie webAuth checks.
//
// All routes 404 when GitHub auth is unconfigured (s.gh == nil).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/work-tracker/internal/githubauth"
	"github.com/sunstoneinstitute/work-tracker/internal/store"
)

// githubCallbackURL is the GitHub web redirect URI, distinct from Keycloak's
// /auth/callback so both providers coexist.
func (s *server) githubCallbackURL() string {
	return strings.TrimRight(s.cfg.PublicURL, "/") + "/auth/github/callback"
}

// loginTarget returns where webAuth sends unauthenticated users, given which
// providers are configured. With both, it points at the chooser page.
func (s *server) loginTarget(next string) string {
	q := "?next=" + url.QueryEscape(next)
	switch {
	case s.oidc != nil && s.gh != nil:
		return "/auth/choose" + q
	case s.gh != nil:
		return "/auth/github/login" + q
	default:
		return "/auth/login" + q
	}
}

// authChoose renders a minimal provider chooser when both Keycloak and GitHub
// are enabled. next is passed through to whichever provider the user picks.
func (s *server) authChoose(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	q := "?next=" + url.QueryEscape(next)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><title>Sign in</title>`+
		`<h1>Sign in to work-tracker</h1>`+
		`<p><a href="/auth/github/login%s">Sign in with GitHub</a></p>`+
		`<p><a href="/auth/login%s">Sign in with Keycloak</a></p>`, q, q)
}

// provisionGitHubActor enforces the org-derived user role and upserts the
// github:<id> human actor, syncing the admin flag from team membership. It
// returns the namespaced actor id.
func (s *server) provisionGitHubActor(ctx context.Context, id *githubauth.Identity, roles githubauth.Roles) (string, error) {
	if !roles.User {
		return "", errNoUserRole
	}
	actorID := "github:" + strconv.FormatInt(id.ID, 10)
	existing, err := s.st.GetActor(ctx, actorID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	if existing != nil && existing.Kind != "human" {
		return "", errActorKindConflict
	}
	if err := s.st.UpsertHumanActor(ctx, actorID, id.Login, roles.Admin); err != nil {
		return "", err
	}
	return actorID, nil
}

// githubLogin handles GET /auth/github/login: begin the GitHub OAuth flow.
func (s *server) githubLogin(w http.ResponseWriter, r *http.Request) {
	if s.gh == nil {
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
	now := s.st.Now()
	http.SetCookie(w, &http.Cookie{
		Name:     oauthCookieName,
		Value:    signOAuthState(s.cfg.SessionSecret, oauthState{State: state, Next: next, Exp: now.Add(oauthStateMaxAge).Unix()}),
		Path:     "/auth/",
		MaxAge:   int(oauthStateMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.gh.AuthCodeURL(s.githubCallbackURL(), state), http.StatusFound)
}

// githubUserToken is the JSON shape sealed into github_user_tokens.
type githubUserToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Expiry       string `json:"expiry"` // RFC3339, empty if none
}

// githubCallback handles GET /auth/github/callback.
func (s *server) githubCallback(w http.ResponseWriter, r *http.Request) {
	if s.gh == nil {
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	c, err := r.Cookie(oauthCookieName)
	if err != nil {
		webErr(w, http.StatusBadRequest, "missing login state")
		return
	}
	stt, ok := verifyOAuthState(s.cfg.SessionSecret, c.Value, s.st.Now())
	if !ok {
		webErr(w, http.StatusBadRequest, "invalid or expired login state")
		return
	}
	if r.URL.Query().Get("state") != stt.State {
		webErr(w, http.StatusBadRequest, "state mismatch")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		webErr(w, http.StatusBadRequest, "missing code")
		return
	}

	tok, err := s.gh.Exchange(r.Context(), s.githubCallbackURL(), code)
	if err != nil {
		s.log.Error("github code exchange", "err", err)
		webErr(w, http.StatusBadGateway, "login failed")
		return
	}
	identity, err := s.gh.FetchIdentity(r.Context(), tok.AccessToken)
	if err != nil {
		s.log.Error("github fetch identity", "err", err)
		webErr(w, http.StatusBadGateway, "login failed")
		return
	}
	roles, err := s.gh.Roles(r.Context(), tok.AccessToken, identity.Login)
	if err != nil {
		s.log.Error("github membership", "err", err)
		webErr(w, http.StatusBadGateway, "login failed")
		return
	}

	actorID, err := s.provisionGitHubActor(r.Context(), identity, roles)
	if errors.Is(err, errNoUserRole) {
		webErr(w, http.StatusForbidden, fmt.Sprintf("must be a member of the %s org", s.gh.Org))
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

	if err := s.storeGitHubToken(r.Context(), actorID, tok); err != nil {
		s.log.Error("store github token", "err", err)
		webErr(w, http.StatusInternalServerError, "internal error")
		return
	}

	s.finishLogin(w, r, actorID, safeNext(stt.Next))
}

// storeGitHubToken seals the token pair and upserts it for actorID.
func (s *server) storeGitHubToken(ctx context.Context, actorID string, tok *githubauth.Token) error {
	payload := githubUserToken{AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken}
	if !tok.Expiry.IsZero() {
		payload.Expiry = tok.Expiry.UTC().Format(time.RFC3339)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ct, err := s.tokenCipher.Seal(raw)
	if err != nil {
		return err
	}
	return s.st.UpsertGitHubUserToken(ctx, actorID, ct)
}
