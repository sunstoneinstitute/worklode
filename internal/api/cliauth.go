// cliauth.go implements the server-mediated CLI login flow: a discovery
// endpoint, a login-start endpoint that stamps a loopback redirect target into
// a signed cookie, and a token endpoint that redeems a one-time code for a wl_
// token. The one-time code is minted in finishLogin (shared by both web
// callbacks) once the actor is provisioned. See
// docs/specs/031-provider-neutral-cli-login.md.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// cliCodeTTL bounds how long a one-time code is valid between the browser
// redirect and the CLI's token exchange.
const cliCodeTTL = 60 * time.Second

type cliCode struct {
	actorID string
	state   string
	expires time.Time
}

// cliCodeStore holds pending one-time codes in memory. The server is
// single-instance, so a restart simply drops pending 60s codes.
type cliCodeStore struct {
	mu    sync.Mutex
	codes map[string]cliCode
	now   func() time.Time
}

func newCLICodeStore(now func() time.Time) *cliCodeStore {
	return &cliCodeStore{codes: map[string]cliCode{}, now: now}
}

// mint stores a fresh single-use code bound to actorID and the CLI state.
func (s *cliCodeStore) mint(actorID, state string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	code := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[code] = cliCode{actorID: actorID, state: state, expires: s.now().Add(cliCodeTTL)}
	return code, nil
}

// redeem returns the bound actor id and consumes the code. It fails if the
// code is unknown, expired, or the state does not match. A state mismatch does
// NOT consume the code.
func (s *cliCodeStore) redeem(code, state string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.codes[code]
	if !ok || s.now().After(c.expires) {
		delete(s.codes, code)
		return "", false
	}
	if c.state != state {
		return "", false
	}
	delete(s.codes, code)
	return c.actorID, true
}

// now returns the store clock, or wall-clock when there is no store (tests that
// build a bare *server to exercise a handler directly).
func (s *server) now() time.Time {
	if s.st != nil {
		return s.st.Now()
	}
	// Handler-level white-box tests build a bare *server (no store) but inject a
	// fake clock into cliCodes; honour it so intent-expiry checks stay consistent
	// with the minted codes. In production st.Now and cliCodes.now are the same
	// clock, so this is a no-op there.
	if s.cliCodes != nil {
		return s.cliCodes.now()
	}
	return time.Now()
}

// finishLogin ends a successful web login for actorID. When the CLI-intent
// cookie is present (a server-mediated `lode login`), it mints a one-time code
// and redirects to the loopback redirect_uri instead of establishing a browser
// session. Otherwise it delegates to finishLoginWeb.
func (s *server) finishLogin(w http.ResponseWriter, r *http.Request, actorID, next string) {
	if c, err := r.Cookie(cliCookieName); err == nil {
		if ci, ok := verifyCLIIntent(s.cfg.SessionSecret, c.Value, s.now()); ok {
			code, err := s.cliCodes.mint(actorID, ci.State)
			if err != nil {
				s.log.Error("mint cli code", "err", err)
				webErr(w, http.StatusInternalServerError, "internal error")
				return
			}
			// Clear both transient cookies.
			http.SetCookie(w, &http.Cookie{Name: cliCookieName, Path: "/auth/", MaxAge: -1})
			http.SetCookie(w, &http.Cookie{Name: oauthCookieName, Path: "/auth/", MaxAge: -1})
			// Build the loopback URL with net/url so an eventual path or query in
			// redirect_uri is preserved rather than corrupted by concatenation.
			u, err := url.Parse(ci.Redirect)
			if err != nil {
				s.log.Error("parse cli redirect", "err", err)
				webErr(w, http.StatusInternalServerError, "internal error")
				return
			}
			u.RawQuery = url.Values{"code": {code}, "state": {ci.State}}.Encode()
			http.Redirect(w, r, u.String(), http.StatusFound)
			return
		}
	}
	s.finishLoginWeb(w, r, actorID, next)
}

// finishLoginWeb sets the browser session cookie and redirects to next. This is
// the original tail shared by both web callbacks.
func (s *server) finishLoginWeb(w http.ResponseWriter, r *http.Request, actorID, next string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    signSession(s.cfg.SessionSecret, actorID, s.st.Now().Add(sessionLifetime)),
		Path:     "/",
		MaxAge:   int(sessionLifetime.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{Name: oauthCookieName, Path: "/auth/", MaxAge: -1})
	http.Redirect(w, r, safeNext(next), http.StatusFound)
}

// isLoopbackRedirect reports whether raw is a syntactically valid http URL whose
// host is a loopback address with an explicit non-zero port. This blocks code
// exfiltration to a remote redirect target.
func isLoopbackRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return false
	}
	if p := u.Port(); p == "" || p == "0" {
		return false
	}
	return true
}

// cliLogin handles GET /auth/cli/login: validate the loopback redirect target,
// stamp it into a signed cookie, and redirect into the Keycloak web login.
// 404 when OIDC is unconfigured (s.oidc == nil).
func (s *server) cliLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeErr(w, http.StatusNotFound, "no interactive login configured")
		return
	}
	q := r.URL.Query()
	redirect := q.Get("redirect_uri")
	state := q.Get("state")
	if state == "" || !isLoopbackRedirect(redirect) {
		writeErr(w, http.StatusBadRequest, "invalid redirect_uri or state")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cliCookieName,
		Value:    signCLIIntent(s.cfg.SessionSecret, cliIntent{Redirect: redirect, State: state, Exp: s.now().Add(oauthStateMaxAge).Unix()}),
		Path:     "/auth/",
		MaxAge:   int(oauthStateMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.loginTarget("/"), http.StatusFound)
}

type cliTokenRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

// cliToken handles POST /auth/cli/token: redeem a one-time code (proof the
// browser login completed) for a 30-day wl_ token.
func (s *server) cliToken(w http.ResponseWriter, r *http.Request) {
	var req cliTokenRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	actorID, ok := s.cliCodes.redeem(req.Code, req.State)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid or expired code")
		return
	}
	exp := s.now().Add(ssoTokenTTL)
	token, err := s.st.CreateToken(r.Context(), actorID, "lode login", &exp)
	if err != nil {
		s.log.Error("mint cli token", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"token":      token,
		"actor_id":   actorID,
		"expires_at": exp.UTC().Format(time.RFC3339),
	})
}

// wellKnownLogin handles GET /.well-known/lode-login: tells the CLI where to
// start the login and which providers are available — always ["keycloak"],
// worklode's sole interactive login provider (spec 023 §3.1). 404 when OIDC
// is unconfigured (s.oidc == nil).
func (s *server) wellKnownLogin(w http.ResponseWriter, _ *http.Request) {
	if s.oidc == nil {
		writeErr(w, http.StatusNotFound, "no interactive login configured")
		return
	}
	base := strings.TrimRight(s.cfg.PublicURL, "/")
	writeJSON(w, http.StatusOK, map[string]any{
		"authorize_url": base + "/auth/cli/login",
		"token_url":     base + "/auth/cli/token",
		"providers":     []string{"keycloak"},
	})
}
