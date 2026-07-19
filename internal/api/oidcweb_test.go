package api_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// gated GETs must redirect to /auth/login when OIDC is enabled and no session
// cookie is present.
func TestWebRedirectsWhenNoSession(t *testing.T) {
	_, h, _ := newOIDCServer(t)
	for _, path := range []string{"/", "/tasks/WT-1", "/projects/proj"} {
		rr := doReq(t, h, "GET", path, "", nil)
		if rr.Code != http.StatusFound {
			t.Fatalf("GET %s status = %d, want 302", path, rr.Code)
		}
		loc := rr.Header().Get("Location")
		if !strings.HasPrefix(loc, "/auth/login?next=") {
			t.Fatalf("GET %s Location = %q, want /auth/login?next=...", path, loc)
		}
	}
}

// /healthz stays open even with OIDC enabled.
func TestHealthzOpenWithOIDC(t *testing.T) {
	_, h, _ := newOIDCServer(t)
	rr := doReq(t, h, "GET", "/healthz", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rr.Code)
	}
}

// /auth/login sets an oauth-state cookie and redirects to the issuer's
// authorize endpoint carrying the PKCE challenge.
func TestAuthLoginRedirectsToIssuer(t *testing.T) {
	_, h, iss := newOIDCServer(t)
	rr := doReq(t, h, "GET", "/auth/login?next=/tasks/WT-1", "", nil)
	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, iss.URL()+"/auth") {
		t.Fatalf("Location = %q, want issuer authorize URL", loc)
	}
	u, _ := url.Parse(loc)
	if u.Query().Get("code_challenge") == "" || u.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("missing PKCE challenge in %q", loc)
	}
	if u.Query().Get("state") == "" {
		t.Fatalf("missing state in %q", loc)
	}
	if !hasCookie(rr, "wt_oauth") {
		t.Fatal("no wt_oauth cookie set")
	}
}

// Full round-trip: login -> (drive the issuer) -> callback sets a session
// cookie and redirects to the originally requested page.
func TestAuthCallbackRoundTrip(t *testing.T) {
	_, h, iss := newOIDCServer(t)

	// The issuer's /token endpoint will return this ID token.
	iss.TokenClaims = map[string]any{
		"preferred_username": "grace", "name": "Grace", "aud": iss.ClientID,
		"groups": []string{"user"},
	}

	// Step 1: hit /auth/login to obtain the oauth-state cookie and the state param.
	login := doReq(t, h, "GET", "/auth/login?next=/tasks/WT-1", "", nil)
	oauthCookie := cookieValue(login, "wt_oauth")
	loc, _ := url.Parse(login.Header().Get("Location"))
	state := loc.Query().Get("state")

	// Step 2: simulate Keycloak redirecting back to /auth/callback with a code,
	// carrying the oauth-state cookie. The callback exchanges the code at the
	// issuer's /token endpoint (which returns iss.TokenClaims as the id_token).
	req := httptest.NewRequest("GET", "/auth/callback?code=fake-code&state="+url.QueryEscape(state), nil)
	req.AddCookie(&http.Cookie{Name: "wt_oauth", Value: oauthCookie})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("callback status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Location"); got != "/tasks/WT-1" {
		t.Fatalf("callback Location = %q, want /tasks/WT-1", got)
	}
	if !hasCookie(rr, "wt_session") {
		t.Fatal("no wt_session cookie set after callback")
	}

	// Step 3: the session cookie now lets a gated page through.
	session := cookieValue(rr, "wt_session")
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(&http.Cookie{Name: "wt_session", Value: session})
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("board with session status = %d, want 200", rr2.Code)
	}
}

// A tampered session cookie is treated as absent: redirect to login.
func TestWebTamperedSessionRedirects(t *testing.T) {
	_, h, _ := newOIDCServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "wt_session", Value: "garbage.garbage"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 for tampered cookie", rr.Code)
	}
}

// A callback without the oauth-state cookie is a 400 (no session state).
func TestAuthCallbackMissingState(t *testing.T) {
	_, h, _ := newOIDCServer(t)
	rr := doReq(t, h, "GET", "/auth/callback?code=x&state=y", "", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

// --- cookie test helpers ---

func hasCookie(rr *httptest.ResponseRecorder, name string) bool {
	return cookieValue(rr, name) != ""
}

func cookieValue(rr *httptest.ResponseRecorder, name string) string {
	for _, c := range rr.Result().Cookies() {
		if c.Name == name && c.MaxAge >= 0 {
			return c.Value
		}
	}
	return ""
}
