package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/oidc/oidctest"
)

func TestMCPOrigins(t *testing.T) {
	t.Parallel()
	s := &server{cfg: Config{
		PublicURL:    "https://worklode.sunstone.internal",
		MCPPublicURL: "https://mcp.example.com/worklode",
	}}
	mcpReq := httptest.NewRequest("GET", "/", nil)
	mcpReq.Host = "mcp.example.com"
	webReq := httptest.NewRequest("GET", "/", nil)
	webReq.Host = "worklode.sunstone.internal"

	if got := advertise(s.originFor(mcpReq), "/mcp"); got != "https://mcp.example.com/worklode/mcp" {
		t.Errorf("mcp origin: %s", got)
	}
	if got := advertise(s.originFor(webReq), "/oauth/authorize"); got != "https://worklode.sunstone.internal/oauth/authorize" {
		t.Errorf("browser origin: %s", got)
	}
	for in, want := range map[string]bool{
		"https://worklode.sunstone.internal":     true,
		"https://worklode.sunstone.internal/mcp": true,
		"https://mcp.example.com/worklode":       true,
		"https://mcp.example.com/worklode/mcp":   true,
		"https://mcp.example.com/worklode/mcp/":  true,
		"https://evil.example.com/mcp":           false,
		"https://mcp.example.com/mcp":            false,
	} {
		if got := s.isResource(in); got != want {
			t.Errorf("isResource(%q) = %v, want %v", in, got, want)
		}
	}
}

func metaDoc(t *testing.T, h http.HandlerFunc, host string) (map[string]any, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = host
	rr := httptest.NewRecorder()
	h(rr, req)
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m, rr
}

func TestMCPMetadata(t *testing.T) {
	t.Parallel()
	s := &server{cfg: Config{
		PublicURL:    "https://worklode.sunstone.internal",
		MCPPublicURL: "https://mcp.example.com/worklode",
	}}
	pr, _ := metaDoc(t, s.protectedResourceMetadata, "mcp.example.com")
	if pr["resource"] != "https://mcp.example.com/worklode/mcp" ||
		pr["authorization_servers"].([]any)[0] != "https://mcp.example.com/worklode" {
		t.Errorf("mcp protected resource: %v", pr)
	}
	pr, _ = metaDoc(t, s.protectedResourceMetadata, "worklode.sunstone.internal")
	if pr["resource"] != "https://worklode.sunstone.internal/mcp" {
		t.Errorf("browser protected resource: %v", pr)
	}
	as, _ := metaDoc(t, s.authorizationServerMetadata, "mcp.example.com")
	for k, want := range map[string]string{
		"issuer":                 "https://mcp.example.com/worklode",
		"authorization_endpoint": "https://worklode.sunstone.internal/oauth/authorize",
		"token_endpoint":         "https://mcp.example.com/worklode/oauth/token",
	} {
		if as[k] != want {
			t.Errorf("%s = %v, want %s", k, as[k], want)
		}
	}
	as, _ = metaDoc(t, s.authorizationServerMetadata, "worklode.sunstone.internal")
	if as["issuer"] != "https://worklode.sunstone.internal" || as["token_endpoint"] != "https://worklode.sunstone.internal/oauth/token" {
		t.Errorf("browser as: %v", as)
	}
	if got := wellKnownURL("https://mcp.example.com/worklode", "oauth-protected-resource", "/mcp"); got != "https://mcp.example.com/.well-known/oauth-protected-resource/worklode/mcp" {
		t.Errorf("wellKnownURL = %s", got)
	}
}

func newMCPOAuthServer(t *testing.T, mcpURL string) (http.Handler, http.Handler, *oidctest.Issuer) {
	t.Helper()
	iss := oidctest.NewIssuer(t)
	iss.TokenClaims = map[string]any{"preferred_username": "grace", "name": "Grace", "aud": iss.ClientID, "groups": []string{"user"}}
	h, admin, err := NewServer(newStoreT(t), Config{
		OIDCIssuer: iss.URL(), OIDCClientID: iss.ClientID, SessionSecret: "sek",
		PublicURL: "http://localhost:8080", MCPPublicURL: mcpURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, admin, iss
}

func get(h http.Handler, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func cookieOf(rr *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rr.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func authorizePath(over map[string]string) string {
	v := url.Values{
		"response_type": {"code"}, "client_id": {"worklode-mcp"},
		"redirect_uri":   {"https://claude.ai/api/mcp/auth_callback"},
		"code_challenge": {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"}, "code_challenge_method": {"S256"},
		"state": {"st8"},
	}
	for k, val := range over {
		if val == "" {
			v.Del(k)
		} else {
			v.Set(k, val)
		}
	}
	return "/oauth/authorize?" + v.Encode()
}

// callback completes the fake Keycloak login started by rr, sending intent as
// the MCP intent cookie.
func callback(h http.Handler, login *httptest.ResponseRecorder, intent *http.Cookie) *httptest.ResponseRecorder {
	loc, _ := url.Parse(login.Header().Get("Location"))
	cookies := []*http.Cookie{cookieOf(login, oauthCookieName)}
	if intent != nil {
		cookies = append(cookies, intent)
	}
	return get(h, "/auth/callback?code=c&state="+url.QueryEscape(loc.Query().Get("state")), cookies...)
}

func TestOAuthAuthorizeReachesConsent(t *testing.T) {
	t.Parallel()
	h, admin, iss := newMCPOAuthServer(t, "")
	login := get(h, authorizePath(nil))
	if login.Code != http.StatusFound || !strings.HasPrefix(login.Header().Get("Location"), iss.URL()) {
		t.Fatalf("authorize: %d %s", login.Code, login.Header().Get("Location"))
	}
	intent := cookieOf(login, mcpCookieName)
	if intent == nil || !intent.HttpOnly || !intent.Secure || intent.SameSite != http.SameSiteLaxMode || intent.Path != "/auth/" {
		t.Fatalf("intent cookie = %+v", intent)
	}
	rr := callback(h, login, intent)
	body := rr.Body.String()
	if rr.Code != http.StatusOK || !strings.Contains(body, "grace") || !strings.Contains(body, "the Worklode connector in claude.ai") {
		t.Fatalf("consent: %d %s", rr.Code, body)
	}
	if c := cookieOf(rr, sessionCookieName); c != nil && c.Value != "" {
		t.Fatal("the MCP branch set a browser session")
	}
	scrape := get(admin, "/metrics").Body.String()
	for _, want := range []string{
		`worklode_mcp_oauth_requests_total{endpoint="authorize",outcome="ok"} 1`,
		`worklode_mcp_oauth_requests_total{endpoint="callback",outcome="ok"} 1`,
	} {
		if !strings.Contains(scrape, want) {
			t.Errorf("metrics missing %s", want)
		}
	}
}

// An intent cookie from another login is ignored: the callback ends as a
// plain web login and shows no consent (WL-REQ-1835).
func TestOAuthIntentNonceMismatch(t *testing.T) {
	t.Parallel()
	h, _, _ := newMCPOAuthServer(t, "")
	stale := cookieOf(get(h, authorizePath(nil)), mcpCookieName)
	for _, login := range []*httptest.ResponseRecorder{get(h, authorizePath(nil)), get(h, "/auth/login?next=/")} {
		rr := callback(h, login, stale)
		if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/" || cookieOf(rr, sessionCookieName) == nil {
			t.Fatalf("callback: %d %s %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
		}
	}
}

func TestPlainLoginClearsIntents(t *testing.T) {
	t.Parallel()
	h, _, _ := newMCPOAuthServer(t, "")
	rr := get(h, "/auth/login?next=/")
	for _, name := range []string{mcpCookieName, cliCookieName} {
		if c := cookieOf(rr, name); c == nil || c.MaxAge >= 0 || c.Path != "/auth/" {
			t.Errorf("%s not cleared: %+v", name, c)
		}
	}
}

func TestOAuthAuthorizeErrors(t *testing.T) {
	t.Parallel()
	h, admin, _ := newMCPOAuthServer(t, "https://mcp.example.com/worklode")
	const res = "https://mcp.example.com/worklode/mcp"

	// A bad client or redirect URI never redirects.
	for _, over := range []map[string]string{
		{"client_id": "other", "resource": res},
		{"redirect_uri": "http://localhost:5555/callback", "resource": res},
		{"redirect_uri": "https://claude.ai/api/mcp/auth_callback/x", "resource": res},
	} {
		rr := get(h, authorizePath(over))
		if rr.Code != http.StatusBadRequest || rr.Header().Get("Location") != "" {
			t.Errorf("%v: %d %q; want 400 and no redirect", over, rr.Code, rr.Header().Get("Location"))
		}
	}
	for _, tc := range []struct {
		over map[string]string
		want string
	}{
		{map[string]string{}, "invalid_request"}, // two origins: resource required
		{map[string]string{"resource": "https://evil.example.com/mcp"}, "invalid_target"},
		{map[string]string{"resource": res, "code_challenge_method": "plain"}, "invalid_request"},
		{map[string]string{"resource": res, "code_challenge": ""}, "invalid_request"},
		{map[string]string{"resource": res, "response_type": "token"}, "invalid_request"},
	} {
		rr := get(h, authorizePath(tc.over))
		loc, _ := url.Parse(rr.Header().Get("Location"))
		if rr.Code != http.StatusFound || !strings.HasPrefix(loc.String(), "https://claude.ai/api/mcp/auth_callback?") ||
			loc.Query().Get("error") != tc.want || loc.Query().Get("state") != "st8" {
			t.Errorf("%v: %d %s; want %s", tc.over, rr.Code, loc, tc.want)
		}
		if cookieOf(rr, mcpCookieName) != nil {
			t.Errorf("%v: intent set on an error", tc.over)
		}
	}
	if rr := get(h, authorizePath(map[string]string{"resource": res, "redirect_uri": "http://127.0.0.1:33418/callback"})); cookieOf(rr, mcpCookieName) == nil {
		t.Errorf("loopback on 33418 with resource: %d %s", rr.Code, rr.Header().Get("Location"))
	}
	if scrape := get(admin, "/metrics").Body.String(); !strings.Contains(scrape, `worklode_mcp_oauth_requests_total{endpoint="authorize",outcome="invalid_target"} 1`) {
		t.Error("invalid_target not counted")
	}
}
