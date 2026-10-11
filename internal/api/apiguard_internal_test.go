package api

import (
	"context"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/oidc"
	"github.com/sunstoneinstitute/worklode/internal/oidc/oidctest"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// sessionOnlyTestPattern is the one route this file injects into a copy of
// routeGuards to exercise sessionOnly (WL-REQ-1771): no production route
// uses the flag yet.
const sessionOnlyTestPattern = "GET /api/v1/__test/sessiononly"

// newAPIGuardTestServer builds a *server plus a mux carrying every /api/v1
// route, /auth/login, /auth/callback, and sessionOnlyTestPattern (guarded by
// sessionOnly(permWhoAmI)), without the templ cockpit — this test only needs
// the API surface and the login walk. iss nil means no OIDC configured (the
// WebOpen cases); non-nil wires it up as NewServer would.
func newAPIGuardTestServer(t *testing.T, webOpen bool, iss *oidctest.Issuer) (*server, http.Handler) {
	t.Helper()
	st := store.OpenTestStore(t)
	s := &server{
		st: st, log: slog.Default(),
		cfg: Config{PublicURL: "http://localhost:8080", SessionSecret: "test-session-secret", WebOpen: webOpen},
	}
	if iss != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		v, err := oidc.New(ctx, iss.URL(), iss.ClientID)
		if err != nil {
			t.Fatalf("configure oidc: %v", err)
		}
		s.oidc = v
		s.cfg.OIDCIssuer = iss.URL()
		s.cfg.OIDCClientID = iss.ClientID
	}

	guards := maps.Clone(routeGuards)
	guards[sessionOnlyTestPattern] = sessionOnly(permWhoAmI)

	mux := http.NewServeMux()
	r := newRouterWithGuards(s, mux, guards)
	s.registerAPI(r, prometheus.NewRegistry())
	s.registerShared(r)
	r.api(sessionOnlyTestPattern, func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "yes"})
	})
	return s, mux
}

// seedBearerToken creates an actor and a bearer token for it, the
// package-internal equivalent of server_test.go's seedActor (that one lives
// in api_test).
func seedBearerToken(t *testing.T, st *store.Store, id string, admin bool) string {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateActor(ctx, id, "human", id, admin); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	token, err := st.CreateToken(ctx, id, id+" token", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	return token
}

// internalLoginSession is oidcweb_test.go's loginSession, duplicated here
// because that one lives in package api_test and this file needs the
// unexported router. Keep the two in sync if the login walk ever changes.
func internalLoginSession(t *testing.T, h http.Handler, iss *oidctest.Issuer, username string) *http.Cookie {
	t.Helper()
	iss.TokenClaims = map[string]any{
		"preferred_username": username, "name": username, "aud": iss.ClientID,
		"groups": []string{"user"},
	}
	login := httptest.NewRecorder()
	h.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/auth/login?next=/", nil))
	var oauthCookie string
	for _, c := range login.Result().Cookies() {
		if c.Name == "wl_oauth" {
			oauthCookie = c.Value
		}
	}
	loc, _ := url.Parse(login.Header().Get("Location"))
	state := loc.Query().Get("state")

	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=fake-code&state="+url.QueryEscape(state), nil)
	req.AddCookie(&http.Cookie{Name: "wl_oauth", Value: oauthCookie})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("login callback for %q: %d %s", username, rr.Code, rr.Body.String())
	}
	var session string
	for _, c := range rr.Result().Cookies() {
		if c.Name == "wl_session" {
			session = c.Value
		}
	}
	if session == "" {
		t.Fatalf("login callback for %q set no wl_session cookie", username)
	}
	return &http.Cookie{Name: "wl_session", Value: session}
}

// TestSessionOnlyRefusesBearerAcceptsSession is Task 9's check: a
// sessionOnly route (WL-REQ-1771) refuses a bearer token and accepts the
// session cookie; an ordinary route still takes a bearer token; an invalid
// bearer token is 401 and never falls through to the cookie.
func TestSessionOnlyRefusesBearerAcceptsSession(t *testing.T) {
	t.Parallel()
	iss := oidctest.NewIssuer(t)
	s, h := newAPIGuardTestServer(t, false, iss)

	token := seedBearerToken(t, s.st, "bob", false)

	// A bearer token on the sessionOnly route is refused.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/__test/sessiononly", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "signed-in browser session") {
		t.Fatalf("bearer on sessionOnly: %d %s", rec.Code, rec.Body)
	}

	// The same bearer token still works on an ordinary route.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("bearer on ordinary route: %d %s", rec2.Code, rec2.Body)
	}

	// The session cookie is accepted on the sessionOnly route.
	cookie := internalLoginSession(t, h, iss, "alice")
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/__test/sessiononly", nil)
	req3.AddCookie(cookie)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("session on sessionOnly: %d %s", rec3.Code, rec3.Body)
	}

	// An invalid bearer token is 401 and never falls through to a session
	// cookie carried on the very same request.
	req4 := httptest.NewRequest(http.MethodGet, "/api/v1/__test/sessiononly", nil)
	req4.Header.Set("Authorization", "Bearer garbage-token")
	req4.AddCookie(cookie)
	rec4 := httptest.NewRecorder()
	h.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusUnauthorized {
		t.Fatalf("invalid bearer with cookie present: %d %s, want 401 (no fallthrough)", rec4.Code, rec4.Body)
	}
}

// TestSessionOnlyRefusesOpenSubject checks D3's last case: on a WebOpen
// instance with no OIDC, the open subject is refused on a sessionOnly route
// exactly like requireSession refuses it on the web — 403, not the 200 it
// gets on an ordinary route granted to RoleUser.
func TestSessionOnlyRefusesOpenSubject(t *testing.T) {
	t.Parallel()
	_, h := newAPIGuardTestServer(t, true, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/__test/sessiononly", nil))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "signed-in browser session") {
		t.Fatalf("open subject on sessionOnly: %d %s", rec.Code, rec.Body)
	}
}
