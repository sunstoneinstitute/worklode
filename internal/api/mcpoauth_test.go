package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

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
	// The MCP branch signs the browser in: POST /oauth/consent requires the
	// session (WL-REQ-1838). The consent cookie carries the form's id.
	if c := cookieOf(rr, sessionCookieName); c == nil || c.Value == "" {
		t.Fatal("the MCP branch set no browser session")
	}
	if c := cookieOf(rr, consentCookieName); c == nil || c.Path != "/oauth/consent" || !c.HttpOnly || !c.Secure ||
		!strings.Contains(body, `name="consent_id" value="`+c.Value+`"`) {
		t.Fatalf("consent cookie = %+v", c)
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

// consentPost posts decision for the consent page consent shows, with its
// cookies except those named in drop. A non-empty id replaces the posted id.
func consentPost(h http.Handler, consent *httptest.ResponseRecorder, decision, id string, hdr map[string]string, drop ...string) *httptest.ResponseRecorder {
	if id == "" {
		id = cookieOf(consent, consentCookieName).Value
	}
	req := httptest.NewRequest("POST", "/oauth/consent",
		strings.NewReader(url.Values{"consent_id": {id}, "decision": {decision}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	for _, name := range []string{sessionCookieName, consentCookieName} {
		if c := cookieOf(consent, name); c != nil && !slices.Contains(drop, name) {
			req.AddCookie(c)
		}
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// No code exists until Allow is posted (WL-RULE-1837).
func TestNoCodeBeforeConsent(t *testing.T) {
	t.Parallel()
	s := &server{cfg: Config{PublicURL: "http://localhost:8080", SessionSecret: "sek"}, log: slog.Default()}
	s.consents = newConsentStore(time.Now)
	s.mcpCodes = newTTLStore[mcpCode](time.Now)
	rr := httptest.NewRecorder()
	s.finishLoginMCP(rr, httptest.NewRequest("GET", "/auth/callback", nil), "grace", mcpIntent{
		ClientID: "worklode-mcp", RedirectURI: "https://claude.ai/api/mcp/auth_callback", CodeChallenge: "cc", State: "st8",
	})
	if len(s.mcpCodes.m) != 0 || rr.Header().Get("Location") != "" {
		t.Fatal("a code exists before the consent post")
	}
	post := consentPost(http.HandlerFunc(s.oauthConsent), rr, "allow", "", nil)
	if post.Code != http.StatusFound || len(s.mcpCodes.m) != 1 {
		t.Fatalf("allow: %d, %d codes", post.Code, len(s.mcpCodes.m))
	}
	for _, c := range s.mcpCodes.m {
		if c.v != (mcpCode{actor: "grace", clientID: "worklode-mcp", redirectURI: "https://claude.ai/api/mcp/auth_callback", codeChallenge: "cc"}) {
			t.Errorf("code binding = %+v", c.v)
		}
	}
}

func reachConsent(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	login := get(h, path)
	rr := callback(h, login, cookieOf(login, mcpCookieName))
	if rr.Code != http.StatusOK || cookieOf(rr, consentCookieName) == nil {
		t.Fatalf("consent: %d %s", rr.Code, rr.Body.String())
	}
	return rr
}

func TestOAuthConsent(t *testing.T) {
	t.Parallel()
	h, admin, _ := newMCPOAuthServer(t, "https://mcp.example.com/worklode")
	const res = "https://mcp.example.com/worklode/mcp"
	path := authorizePath(map[string]string{"resource": res})
	sameOrigin := map[string]string{"Sec-Fetch-Site": "same-origin"}

	consent := reachConsent(t, h, path)
	for name, rr := range map[string]*httptest.ResponseRecorder{
		"cross-site":     consentPost(h, consent, "allow", "", map[string]string{"Sec-Fetch-Site": "cross-site"}),
		"foreign origin": consentPost(h, consent, "allow", "", map[string]string{"Origin": "https://evil.example.com"}),
		"cookie differs": consentPost(h, consent, "allow", strings.Repeat("0", 64), sameOrigin),
		"no cookie":      consentPost(h, consent, "allow", "", sameOrigin, consentCookieName),
		"no session":     consentPost(h, consent, "allow", "", sameOrigin, sessionCookieName),
	} {
		if rr.Code != http.StatusForbidden || rr.Header().Get("Location") != "" {
			t.Errorf("%s: %d %q; want 403", name, rr.Code, rr.Header().Get("Location"))
		}
	}
	// None of the refusals spent the consent.
	allow := consentPost(h, consent, "allow", "", sameOrigin)
	loc, _ := url.Parse(allow.Header().Get("Location"))
	q := loc.Query()
	if allow.Code != http.StatusFound || !strings.HasPrefix(loc.String(), "https://claude.ai/api/mcp/auth_callback?") ||
		q.Get("code") == "" || q.Get("state") != "st8" || q.Get("iss") != "https://mcp.example.com/worklode" {
		t.Fatalf("allow: %d %s", allow.Code, loc)
	}
	if again := consentPost(h, consent, "allow", "", sameOrigin); again.Code != http.StatusBadRequest || again.Header().Get("Location") != "" {
		t.Errorf("second post: %d %q; want 400", again.Code, again.Header().Get("Location"))
	}

	deny := consentPost(h, reachConsent(t, h, path), "deny", "", sameOrigin)
	loc, _ = url.Parse(deny.Header().Get("Location"))
	if deny.Code != http.StatusFound || loc.Query().Get("error") != "access_denied" || loc.Query().Get("state") != "st8" || loc.Query().Has("code") {
		t.Errorf("deny: %d %s", deny.Code, loc)
	}

	// The browser origin's resource gets the browser origin as iss.
	allow = consentPost(h, reachConsent(t, h, authorizePath(map[string]string{"resource": "http://localhost:8080/mcp"})), "allow", "", sameOrigin)
	if loc, _ = url.Parse(allow.Header().Get("Location")); loc.Query().Get("iss") != "http://localhost:8080" {
		t.Errorf("browser iss: %s", loc)
	}

	scrape := get(admin, "/metrics").Body.String()
	for _, want := range []string{
		`worklode_mcp_oauth_requests_total{endpoint="consent",outcome="ok"} 2`,
		`worklode_mcp_oauth_requests_total{endpoint="consent",outcome="access_denied"} 1`,
		`worklode_mcp_oauth_requests_total{endpoint="consent",outcome="invalid_request"} 6`,
	} {
		if !strings.Contains(scrape, want) {
			t.Errorf("metrics missing %s", want)
		}
	}
}

const (
	rfcVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk" // RFC 7636 appendix B
	testRedir   = "https://claude.ai/api/mcp/auth_callback"
)

// authCode runs authorize, login and an Allow, and returns the minted code.
func authCode(t *testing.T, h http.Handler, over map[string]string) string {
	t.Helper()
	consent := reachConsent(t, h, authorizePath(over))
	allow := consentPost(h, consent, "allow", "", map[string]string{"Sec-Fetch-Site": "same-origin"})
	loc, _ := url.Parse(allow.Header().Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code: %d %s", allow.Code, loc)
	}
	return code
}

func tokenPost(h http.Handler, over map[string]string) *httptest.ResponseRecorder {
	v := url.Values{
		"grant_type": {"authorization_code"}, "client_id": {"worklode-mcp"},
		"redirect_uri": {testRedir}, "code_verifier": {rfcVerifier},
	}
	for k, val := range over {
		v.Set(k, val)
	}
	req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestOAuthToken(t *testing.T) {
	t.Parallel()
	h, admin, _ := newMCPOAuthServer(t, "https://mcp.example.com/worklode")
	const res = "https://mcp.example.com/worklode/mcp"
	withRes := map[string]string{"resource": res}

	wantErr := func(name string, rr *httptest.ResponseRecorder, want string) {
		t.Helper()
		var e struct{ Error string }
		_ = json.Unmarshal(rr.Body.Bytes(), &e)
		if rr.Code != http.StatusBadRequest || e.Error != want || rr.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %s; want %s", name, rr.Code, rr.Body.String(), want)
		}
	}

	wantErr("grant_type", tokenPost(h, map[string]string{"grant_type": "password", "code": "x"}), "unsupported_grant_type")
	wantErr("client", tokenPost(h, map[string]string{"client_id": "nope", "code": "x"}), "invalid_client")
	wantErr("unknown code", tokenPost(h, map[string]string{"code": "nope"}), "invalid_grant")
	wantErr("resource", tokenPost(h, map[string]string{"code": "x", "resource": "https://evil.example.com/mcp"}), "invalid_target")

	for name, over := range map[string]map[string]string{
		"verifier":     {"code_verifier": "wrong"},
		"no verifier":  {"code_verifier": ""},
		"redirect_uri": {"redirect_uri": "https://claude.ai/other"},
	} {
		over["code"] = authCode(t, h, withRes)
		wantErr(name, tokenPost(h, over), "invalid_grant")
		// The failed attempt spent the code.
		wantErr(name+" retry", tokenPost(h, map[string]string{"code": over["code"]}), "invalid_grant")
	}

	wantErr("resource mismatch", tokenPost(h, map[string]string{
		"code": authCode(t, h, withRes), "resource": "http://localhost:8080/mcp"}), "invalid_target")

	code := authCode(t, h, withRes)
	rr := tokenPost(h, map[string]string{"code": code, "resource": res})
	var tok map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &tok)
	access, _ := tok["access_token"].(string)
	if rr.Code != http.StatusOK || rr.Header().Get("Cache-Control") != "no-store" ||
		!strings.HasPrefix(access, "wl_") || tok["token_type"] != "Bearer" ||
		tok["expires_in"] != float64(ssoTokenTTL.Seconds()) || tok["refresh_token"] != nil {
		t.Fatalf("token: %d %s", rr.Code, rr.Body.String())
	}
	wantErr("replay", tokenPost(h, map[string]string{"code": code, "resource": res}), "invalid_grant")

	// The token works on /mcp.
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	} {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("/mcp %s: %d %s", body[:40], rec.Code, rec.Body.String())
		}
	}

	scrape := get(admin, "/metrics").Body.String()
	for _, want := range []string{
		`worklode_mcp_oauth_requests_total{endpoint="token",outcome="ok"} 1`,
		`worklode_mcp_oauth_requests_total{endpoint="token",outcome="invalid_grant"}`,
		`worklode_mcp_oauth_requests_total{endpoint="token",outcome="invalid_target"}`,
		`worklode_mcp_oauth_requests_total{endpoint="token",outcome="invalid_client"} 1`,
		`worklode_mcp_oauth_requests_total{endpoint="token",outcome="unsupported_grant_type"} 1`,
	} {
		if !strings.Contains(scrape, want) {
			t.Errorf("metrics missing %s", want)
		}
	}
}
