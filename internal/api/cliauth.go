// cliauth.go implements the server-mediated CLI login flow: a discovery
// endpoint, a login-start endpoint that stamps a loopback redirect target into
// a signed cookie, and a token endpoint that redeems a one-time code for a wl_
// token. The one-time code is minted in finishLogin (shared by both web
// callbacks) once the actor is provisioned. See
// docs/specs/001-identity-and-authentication.md.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// cliCodeTTL bounds how long a one-time code is valid between the browser
// redirect and the CLI's token exchange. It is set by the slowest legitimate
// path, manual mode: a human reading a code off one machine's screen and
// pasting it into another's terminal cannot reliably beat the 60 seconds the
// loopback flow alone needed. Five minutes matches the CLI's own wait window
// and stays inside RFC 6749 §4.1.2's ceiling; single use and the state binding,
// not the TTL, are what make the code safe (spec 001 §8.3).
const cliCodeTTL = 5 * time.Minute

// The two shapes a `lode login` can take: the default loopback redirect (§8) and
// manual mode, where the code is rendered for the user to copy (§8.7).
const (
	cliModeLoopback = "loopback"
	cliModeManual   = "manual"
)

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

// cliIntentFrom returns the verified CLI-login intent carried by the request's
// cookie, if it carries one.
func (s *server) cliIntentFrom(r *http.Request) (cliIntent, bool) {
	c, err := r.Cookie(cliCookieName)
	if err != nil {
		return cliIntent{}, false
	}
	return verifyCLIIntent(s.cfg.SessionSecret, c.Value, s.now())
}

// finishLogin ends a successful web login for actorID. When the CLI-intent
// cookie is present (a server-mediated `lode login`), it mints a one-time code
// and redirects to the loopback redirect_uri instead of establishing a browser
// session. Otherwise it delegates to finishLoginWeb.
func (s *server) finishLogin(w http.ResponseWriter, r *http.Request, actorID, next string) {
	ci, ok := s.cliIntentFrom(r)
	if !ok {
		s.finishLoginWeb(w, r, actorID, next)
		return
	}
	code, err := s.cliCodes.mint(actorID, ci.State)
	if err != nil {
		s.log.Error("mint cli code", "err", err)
		webErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Clear both transient cookies.
	clearAuthCookie(w, cliCookieName)
	clearAuthCookie(w, oauthCookieName)
	// Manual mode (§8.7) has no loopback to redirect to: show the code
	// so the user can carry it to the terminal themselves.
	if ci.Mode == cliModeManual {
		s.renderCLICode(w, r, actorID, code)
		return
	}
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
}

// finishLoginWeb sets the browser session cookie and redirects to next. This is
// the original tail shared by both web callbacks.
func (s *server) finishLoginWeb(w http.ResponseWriter, r *http.Request, actorID, next string) {
	setAuthCookie(w, sessionCookieName, "/",
		signSession(s.cfg.SessionSecret, actorID, s.st.Now().Add(sessionLifetime)), sessionLifetime)
	clearAuthCookie(w, oauthCookieName)
	http.Redirect(w, r, safeNext(next), http.StatusFound)
}

// renderCLICode shows the one-time code for the user to copy back into their
// terminal, ending a manual-mode login (§8.7). Only the code reaches the page:
// the 30-day token it redeems for is minted later, at /auth/cli/token, and
// never leaves the CLI process.
func (s *server) renderCLICode(w http.ResponseWriter, r *http.Request, actorID, code string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// A page holding a live credential must not be cached, and least of all on
	// the shared or borrowed machine manual mode exists to serve.
	w.Header().Set("Cache-Control", "no-store")
	view := ui.CLICodeView{
		Title:     "worklode: finish signing in",
		ActorID:   actorID,
		Code:      code,
		ExpiresIn: humanizeDuration(cliCodeTTL),
	}
	if err := ui.CLICode(view).Render(r.Context(), w); err != nil {
		s.log.Error("render cli code page", "err", err)
	}
}

// humanizeDuration renders a whole-minute or whole-second duration for prose.
// Only ever called with cliCodeTTL, so it handles no other shape.
func humanizeDuration(d time.Duration) string {
	if m := int(d.Minutes()); m >= 1 {
		return fmt.Sprintf("%d minute%s", m, plural(m))
	}
	sec := int(d.Seconds())
	return fmt.Sprintf("%d second%s", sec, plural(sec))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
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
	mode := q.Get("mode")
	if mode == "" {
		mode = cliModeLoopback
	}
	// Manual mode binds no listener, so it carries no redirect_uri and the
	// loopback check has nothing to check. Loopback — the default, and anything
	// claiming to be it — must present one.
	switch {
	case state == "":
		writeErr(w, http.StatusBadRequest, "invalid redirect_uri or state")
		return
	case mode == cliModeManual:
		redirect = ""
	case mode == cliModeLoopback && isLoopbackRedirect(redirect):
	default:
		writeErr(w, http.StatusBadRequest, "invalid redirect_uri or state")
		return
	}
	setAuthCookie(w, cliCookieName, "/auth/",
		signCLIIntent(s.cfg.SessionSecret, cliIntent{Redirect: redirect, State: state, Mode: mode, Exp: s.now().Add(oauthStateMaxAge).Unix()}),
		oauthStateMaxAge)
	http.Redirect(w, r, s.loginTarget("/"), http.StatusFound)
}

// cliToken handles POST /auth/cli/token: redeem a one-time code (proof the
// browser login completed) for a 30-day wl_ token.
func (s *server) cliToken(w http.ResponseWriter, r *http.Request) {
	var req model.CLITokenInput
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
	writeMintedToken(w, token, actorID, exp)
}

// wellKnownLogin handles GET /.well-known/lode-login: tells the CLI where to
// start the login and which providers are available — always ["keycloak"],
// worklode's sole interactive login provider (spec 001 §3). 404 when OIDC
// is unconfigured (s.oidc == nil).
func (s *server) wellKnownLogin(w http.ResponseWriter, _ *http.Request) {
	if s.oidc == nil {
		writeErr(w, http.StatusNotFound, "no interactive login configured")
		return
	}
	base := strings.TrimRight(s.cfg.PublicURL, "/")
	writeJSON(w, http.StatusOK, model.LoginDiscovery{
		AuthorizeURL: base + "/auth/cli/login",
		TokenURL:     base + "/auth/cli/token",
		Providers:    []string{"keycloak"},
	})
}
