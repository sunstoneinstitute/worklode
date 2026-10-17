package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/oauth2"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/oidc/oidctest"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

const (
	mcpPublic      = "http://localhost:8080"
	mcpPublicHost  = "localhost:8080"
	mcpSecond      = "https://mcp.example.com"
	mcpSecondHost  = "mcp.example.com"
	mcpClaudeAI    = "https://claude.ai/api/mcp/auth_callback"
	mcpClaudeCode  = "http://localhost:33418/callback"
	mcpTestClient  = "worklode-mcp"
	mcpTestSubject = "grace"
)

// mcpServer is an OIDC-enabled server answering MCP on LODE_PUBLIC_URL and,
// when second is set, on LODE_MCP_PUBLIC_URL too.
type mcpServer struct {
	st  *store.Store
	h   http.Handler
	iss *oidctest.Issuer
	reg *prometheus.Registry
}

func newMCPServer(t *testing.T, second string) *mcpServer {
	t.Helper()
	st := newTestStore(t)
	iss := oidctest.NewIssuer(t)
	iss.TokenClaims = map[string]any{
		"preferred_username": mcpTestSubject, "name": "Grace", "aud": iss.ClientID, "groups": []string{"user"},
	}
	reg := prometheus.NewRegistry()
	h, _, err := api.NewServer(st, api.Config{
		OIDCIssuer:    iss.URL(),
		OIDCClientID:  iss.ClientID,
		PublicURL:     mcpPublic,
		MCPPublicURL:  second,
		SessionSecret: "test-session-secret",
		Metrics:       reg,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return &mcpServer{st: st, h: h, iss: iss, reg: reg}
}

// get serves a GET on host carrying cookies.
func (m *mcpServer) get(host, pathQuery string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://"+host+pathQuery, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	m.h.ServeHTTP(rr, req)
	return rr
}

// token POSTs form to /oauth/token on host.
func (m *mcpServer) token(host string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://"+host+"/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	m.h.ServeHTTP(rr, req)
	return rr
}

// authorizeQuery is a valid /oauth/authorize query for redirect and the
// challenge of verifier; extra entries replace or add parameters.
func authorizeQuery(redirect, verifier string, extra url.Values) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {mcpTestClient},
		"redirect_uri":          {redirect},
		"code_challenge":        {oauth2.S256ChallengeFromVerifier(verifier)},
		"code_challenge_method": {"S256"},
		"state":                 {"client-state"},
	}
	for k, v := range extra {
		q[k] = v
	}
	return "/oauth/authorize?" + q.Encode()
}

// login drives /oauth/authorize, the web login and the fake Keycloak's
// callback, and returns the callback's response: the redirect to the client.
func (m *mcpServer) login(t *testing.T, query string) *httptest.ResponseRecorder {
	t.Helper()
	auth := m.get(mcpPublicHost, query)
	if auth.Code != http.StatusFound || auth.Header().Get("Location") != "/auth/login?next=%2F" {
		t.Fatalf("authorize = %d %q; want 302 to /auth/login", auth.Code, auth.Header().Get("Location"))
	}
	intent := cookieValue(auth, "wl_mcp")
	if intent == "" {
		t.Fatal("authorize set no wl_mcp cookie")
	}
	login := m.get(mcpPublicHost, "/auth/login?next=%2F")
	loc, _ := url.Parse(login.Header().Get("Location"))
	cb := m.get(mcpPublicHost, "/auth/callback?code=fake-code&state="+url.QueryEscape(loc.Query().Get("state")),
		&http.Cookie{Name: "wl_oauth", Value: cookieValue(login, "wl_oauth")},
		&http.Cookie{Name: "wl_mcp", Value: intent})
	if cb.Code != http.StatusFound {
		t.Fatalf("callback = %d body %s; want 302", cb.Code, cb.Body.String())
	}
	return cb
}

// codeFrom returns the code a callback redirect hands the client.
func codeFrom(t *testing.T, cb *httptest.ResponseRecorder) string {
	t.Helper()
	u, err := url.Parse(cb.Header().Get("Location"))
	if err != nil || u.Query().Get("code") == "" {
		t.Fatalf("callback Location %q carries no code", cb.Header().Get("Location"))
	}
	return u.Query().Get("code")
}

func tokenForm(code, redirect, verifier string) url.Values {
	return url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {mcpTestClient},
		"code":          {code},
		"redirect_uri":  {redirect},
		"code_verifier": {verifier},
	}
}

// oauthErr decodes an RFC 6749 §5.2 body and checks its status and code.
func oauthErr(t *testing.T, rr *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var e model.OAuthError
	if err := json.Unmarshal(rr.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error body %q: %v", rr.Body.String(), err)
	}
	if rr.Code != status || e.Error != code {
		t.Fatalf("token = %d %q; want %d %q (body %s)", rr.Code, e.Error, status, code, rr.Body.String())
	}
}

// counter reads worklode_mcp_oauth_requests_total{endpoint,outcome}.
func (m *mcpServer) counter(t *testing.T, endpoint, outcome string) float64 {
	t.Helper()
	mfs, err := m.reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "worklode_mcp_oauth_requests_total" {
			continue
		}
		for _, mt := range mf.GetMetric() {
			labels := map[string]string{}
			for _, l := range mt.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["endpoint"] == endpoint && labels["outcome"] == outcome {
				return mt.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("no series worklode_mcp_oauth_requests_total{endpoint=%q,outcome=%q}", endpoint, outcome)
	return 0
}

// The metadata names the origin it was asked on as issuer and token
// endpoint, and always sends the browser to LODE_PUBLIC_URL to log in.
func TestMCPServerMetadataPerOrigin(t *testing.T) {
	t.Parallel()
	m := newMCPServer(t, mcpSecond)
	for _, tc := range []struct{ host, origin string }{
		{mcpPublicHost, mcpPublic},
		{mcpSecondHost, mcpSecond},
	} {
		rr := m.get(tc.host, "/.well-known/oauth-authorization-server")
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status %d", tc.host, rr.Code)
		}
		var raw map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if _, ok := raw["registration_endpoint"]; ok {
			t.Fatalf("%s: metadata advertises dynamic registration", tc.host)
		}
		var md model.OAuthServerMetadata
		_ = json.Unmarshal(rr.Body.Bytes(), &md)
		want := model.OAuthServerMetadata{
			Issuer:                                     tc.origin,
			AuthorizationEndpoint:                      mcpPublic + "/oauth/authorize",
			TokenEndpoint:                              tc.origin + "/oauth/token",
			ResponseTypesSupported:                     []string{"code"},
			GrantTypesSupported:                        []string{"authorization_code"},
			CodeChallengeMethodsSupported:              []string{"S256"},
			TokenEndpointAuthMethodsSupported:          []string{"none"},
			AuthorizationResponseIssParameterSupported: true,
		}
		got, _ := json.Marshal(md)
		exp, _ := json.Marshal(want)
		if string(got) != string(exp) {
			t.Fatalf("%s: metadata\n got %s\nwant %s", tc.host, got, exp)
		}
	}
	// A host that is not a configured origin is not echoed back.
	if rr := m.get("evil.example", "/.well-known/oauth-authorization-server"); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown host: status %d; want 404", rr.Code)
	}
	if got := m.counter(t, "metadata", "ok"); got != 2 {
		t.Fatalf("metadata ok = %v; want 2", got)
	}
	if got := m.counter(t, "metadata", "not_found"); got != 1 {
		t.Fatalf("metadata not_found = %v; want 1", got)
	}
}

// Without LODE_MCP_PUBLIC_URL, only LODE_PUBLIC_URL is an origin.
func TestMCPServerMetadataSingleOrigin(t *testing.T) {
	t.Parallel()
	m := newMCPServer(t, "")
	if rr := m.get(mcpPublicHost, "/.well-known/oauth-authorization-server"); rr.Code != http.StatusOK {
		t.Fatalf("public host: status %d", rr.Code)
	}
	if rr := m.get(mcpSecondHost, "/.well-known/oauth-authorization-server"); rr.Code != http.StatusNotFound {
		t.Fatalf("unconfigured second host: status %d; want 404", rr.Code)
	}
}

// With OIDC unconfigured there is no login to offer, so all three routes 404.
func TestMCPAuthWithoutOIDC(t *testing.T) {
	t.Parallel()
	_, h, _ := newTestServerWithConfig(t, api.Config{PublicURL: mcpPublic})
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/.well-known/oauth-authorization-server"},
		{http.MethodGet, authorizeQuery(mcpClaudeCode, oauth2.GenerateVerifier(), nil)},
		{http.MethodPost, "/oauth/token"},
	} {
		req := httptest.NewRequest(tc.method, "http://"+mcpPublicHost+tc.path, strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s %s: status %d; want 404", tc.method, tc.path, rr.Code)
		}
	}
}

// Until client_id and redirect_uri are both checked, the redirect URI is
// untrusted: errors are shown to the user, never redirected to.
func TestMCPAuthorizeRendersClientAndRedirectErrors(t *testing.T) {
	t.Parallel()
	m := newMCPServer(t, "")
	v := oauth2.GenerateVerifier()
	cases := map[string]url.Values{
		"wrong client":        {"client_id": {"someone-else"}},
		"no client":           {"client_id": {""}},
		"remote redirect":     {"redirect_uri": {"https://evil.example/callback"}},
		"loopback wrong path": {"redirect_uri": {"http://localhost:33418/elsewhere"}},
		"loopback no port":    {"redirect_uri": {"http://localhost/callback"}},
		"loopback query":      {"redirect_uri": {"http://localhost:33418/callback?x=1"}},
		"claude.ai lookalike": {"redirect_uri": {"https://claude.ai/api/mcp/auth_callback/"}},
		"two redirects":       {"redirect_uri": {mcpClaudeCode, "https://evil.example/callback"}},
		"no redirect":         {"redirect_uri": {""}},
	}
	for name, extra := range cases {
		rr := m.get(mcpPublicHost, authorizeQuery(mcpClaudeCode, v, extra))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d; want 400", name, rr.Code)
		}
		if loc := rr.Header().Get("Location"); loc != "" {
			t.Fatalf("%s: redirected to %q", name, loc)
		}
		if !strings.HasPrefix(rr.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s: content-type %q; want an error page", name, rr.Header().Get("Content-Type"))
		}
		if hasCookie(rr, "wl_mcp") {
			t.Fatalf("%s: set an intent cookie", name)
		}
	}
	if got := m.counter(t, "authorize", "invalid_client"); got != 2 {
		t.Fatalf("authorize invalid_client = %v; want 2", got)
	}
	if got := m.counter(t, "authorize", "invalid_redirect"); got != 7 {
		t.Fatalf("authorize invalid_redirect = %v; want 7", got)
	}
}

// Once the redirect URI is trusted, the remaining errors go back to the
// client (RFC 6749 §4.1.2.1) carrying its state and the issuer.
func TestMCPAuthorizeRedirectsRequestErrors(t *testing.T) {
	t.Parallel()
	m := newMCPServer(t, mcpSecond)
	v := oauth2.GenerateVerifier()
	cases := []struct {
		name  string
		extra url.Values
		code  string
	}{
		{"implicit grant", url.Values{"response_type": {"token"}}, "unsupported_response_type"},
		{"no challenge", url.Values{"code_challenge": {""}}, "invalid_request"},
		{"plain method", url.Values{"code_challenge_method": {"plain"}}, "invalid_request"},
		{"no method", url.Values{"code_challenge_method": {""}}, "invalid_request"},
		{"malformed challenge", url.Values{"code_challenge": {"not-a-sha256"}}, "invalid_request"},
		{"foreign resource", url.Values{"resource": {"https://evil.example/mcp"}}, "invalid_target"},
		{"not the mcp path", url.Values{"resource": {mcpPublic + "/api/v1"}}, "invalid_target"},
		{"two resources", url.Values{"resource": {mcpPublic + "/mcp", mcpSecond + "/mcp"}}, "invalid_target"},
	}
	for _, tc := range cases {
		rr := m.get(mcpPublicHost, authorizeQuery(mcpClaudeAI, v, tc.extra))
		if rr.Code != http.StatusFound {
			t.Fatalf("%s: status %d; want 302", tc.name, rr.Code)
		}
		u, _ := url.Parse(rr.Header().Get("Location"))
		if got := u.Scheme + "://" + u.Host + u.Path; got != mcpClaudeAI {
			t.Fatalf("%s: redirected to %q; want the client", tc.name, got)
		}
		q := u.Query()
		if q.Get("error") != tc.code || q.Get("state") != "client-state" || q.Get("iss") == "" {
			t.Fatalf("%s: redirect query %v; want error=%s with state and iss", tc.name, q, tc.code)
		}
		if hasCookie(rr, "wl_mcp") {
			t.Fatalf("%s: set an intent cookie", tc.name)
		}
	}
}

// The browser leg lives on LODE_PUBLIC_URL, so the public MCP host does not
// serve /oauth/authorize.
func TestMCPAuthorizeOnlyOnPublicURL(t *testing.T) {
	t.Parallel()
	m := newMCPServer(t, mcpSecond)
	rr := m.get(mcpSecondHost, authorizeQuery(mcpClaudeAI, oauth2.GenerateVerifier(), nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d; want 404", rr.Code)
	}
}

// claude.ai's path: discovered on the public MCP host, logged in over
// LODE_PUBLIC_URL, token fetched from the public host, and the token works on
// /api/v1.
func TestMCPLoginHappyPath(t *testing.T) {
	t.Parallel()
	m := newMCPServer(t, mcpSecond)
	v := oauth2.GenerateVerifier()
	cb := m.login(t, authorizeQuery(mcpClaudeAI, v, url.Values{"resource": {mcpSecond + "/mcp"}}))

	u, _ := url.Parse(cb.Header().Get("Location"))
	if got := u.Scheme + "://" + u.Host + u.Path; got != mcpClaudeAI {
		t.Fatalf("callback redirected to %q; want the client", got)
	}
	if q := u.Query(); q.Get("state") != "client-state" || q.Get("iss") != mcpSecond {
		t.Fatalf("callback query %v; want state client-state and iss %s", q, mcpSecond)
	}
	if hasCookie(cb, "wl_session") {
		t.Fatal("the MCP branch must not set a browser session")
	}

	form := tokenForm(codeFrom(t, cb), mcpClaudeAI, v)
	form.Set("resource", mcpSecond+"/mcp")
	rr := m.token(mcpSecondHost, form)
	if rr.Code != http.StatusOK {
		t.Fatalf("token = %d body %s", rr.Code, rr.Body.String())
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q; want no-store", cc)
	}
	var tok model.OAuthToken
	if err := json.Unmarshal(rr.Body.Bytes(), &tok); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if !strings.HasPrefix(tok.AccessToken, "wl_") || tok.TokenType != "Bearer" || tok.ExpiresIn != int64((30*24*time.Hour).Seconds()) {
		t.Fatalf("token = %+v", tok)
	}

	who := doReq(t, m.h, http.MethodGet, "/api/v1/whoami", tok.AccessToken, nil)
	if who.Code != http.StatusOK {
		t.Fatalf("whoami = %d body %s", who.Code, who.Body.String())
	}
	var w model.WhoAmI
	_ = json.Unmarshal(who.Body.Bytes(), &w)
	if w.ID != mcpTestSubject {
		t.Fatalf("whoami id = %q; want %q", w.ID, mcpTestSubject)
	}

	var desc string
	if err := m.st.DBForTests().QueryRowContext(context.Background(),
		`SELECT description FROM tokens WHERE actor_id = $1`, mcpTestSubject).Scan(&desc); err != nil {
		t.Fatalf("read token row: %v", err)
	}
	if desc != "mcp login (claude.ai)" {
		t.Fatalf("token description = %q", desc)
	}

	for _, ep := range []string{"authorize", "callback", "token"} {
		if got := m.counter(t, ep, "ok"); got != 1 {
			t.Fatalf("%s ok = %v; want 1", ep, got)
		}
	}
}

// Claude Code's path: a loopback redirect and no resource. The issuer is then
// LODE_PUBLIC_URL, and a resource named only at the token request must still
// be one of this server's.
func TestMCPLoginLoopbackWithoutResource(t *testing.T) {
	t.Parallel()
	m := newMCPServer(t, mcpSecond)
	v := oauth2.GenerateVerifier()
	cb := m.login(t, authorizeQuery(mcpClaudeCode, v, url.Values{"state": {""}}))
	u, _ := url.Parse(cb.Header().Get("Location"))
	if u.Host != "localhost:33418" || u.Query().Get("iss") != mcpPublic {
		t.Fatalf("callback Location = %q", cb.Header().Get("Location"))
	}
	if _, ok := u.Query()["state"]; ok {
		t.Fatal("a request without state got one back")
	}

	form := tokenForm(codeFrom(t, cb), mcpClaudeCode, v)
	form.Set("resource", mcpPublic+"/mcp/")
	if rr := m.token(mcpPublicHost, form); rr.Code != http.StatusOK {
		t.Fatalf("token = %d body %s", rr.Code, rr.Body.String())
	}

	cb = m.login(t, authorizeQuery(mcpClaudeCode, v, nil))
	form = tokenForm(codeFrom(t, cb), mcpClaudeCode, v)
	form.Set("resource", "https://evil.example/mcp")
	oauthErr(t, m.token(mcpPublicHost, form), http.StatusBadRequest, "invalid_target")
}

func TestMCPTokenCodeIsSingleUse(t *testing.T) {
	t.Parallel()
	m := newMCPServer(t, "")
	v := oauth2.GenerateVerifier()
	code := codeFrom(t, m.login(t, authorizeQuery(mcpClaudeCode, v, nil)))
	if rr := m.token(mcpPublicHost, tokenForm(code, mcpClaudeCode, v)); rr.Code != http.StatusOK {
		t.Fatalf("first redeem = %d body %s", rr.Code, rr.Body.String())
	}
	oauthErr(t, m.token(mcpPublicHost, tokenForm(code, mcpClaudeCode, v)), http.StatusBadRequest, "invalid_grant")
}

// A wrong verifier burns the code, so PKCE cannot be guessed at.
func TestMCPTokenWrongVerifierConsumesCode(t *testing.T) {
	t.Parallel()
	m := newMCPServer(t, "")
	v := oauth2.GenerateVerifier()
	code := codeFrom(t, m.login(t, authorizeQuery(mcpClaudeCode, v, nil)))
	oauthErr(t, m.token(mcpPublicHost, tokenForm(code, mcpClaudeCode, oauth2.GenerateVerifier())), http.StatusBadRequest, "invalid_grant")
	oauthErr(t, m.token(mcpPublicHost, tokenForm(code, mcpClaudeCode, v)), http.StatusBadRequest, "invalid_grant")
	if got := m.counter(t, "token", "invalid_grant"); got != 2 {
		t.Fatalf("token invalid_grant = %v; want 2", got)
	}
}

func TestMCPTokenWrongRedirectURI(t *testing.T) {
	t.Parallel()
	m := newMCPServer(t, "")
	v := oauth2.GenerateVerifier()
	code := codeFrom(t, m.login(t, authorizeQuery(mcpClaudeCode, v, nil)))
	oauthErr(t, m.token(mcpPublicHost, tokenForm(code, "http://127.0.0.1:33418/callback", v)), http.StatusBadRequest, "invalid_grant")
}

func TestMCPTokenExpiredCode(t *testing.T) {
	t.Parallel()
	m := newMCPServer(t, "")
	base := time.Unix(1_900_000_000, 0)
	m.st.SetNowFunc(func() time.Time { return base })
	v := oauth2.GenerateVerifier()
	code := codeFrom(t, m.login(t, authorizeQuery(mcpClaudeCode, v, nil)))
	m.st.SetNowFunc(func() time.Time { return base.Add(5*time.Minute + time.Second) })
	oauthErr(t, m.token(mcpPublicHost, tokenForm(code, mcpClaudeCode, v)), http.StatusBadRequest, "invalid_grant")
}

func TestMCPTokenRequestErrors(t *testing.T) {
	t.Parallel()
	m := newMCPServer(t, mcpSecond)
	v := oauth2.GenerateVerifier()

	form := tokenForm("x", mcpClaudeCode, v)
	form.Set("grant_type", "refresh_token")
	oauthErr(t, m.token(mcpPublicHost, form), http.StatusBadRequest, "unsupported_grant_type")

	form = tokenForm("x", mcpClaudeCode, v)
	form.Set("client_id", "someone-else")
	oauthErr(t, m.token(mcpPublicHost, form), http.StatusUnauthorized, "invalid_client")

	req := httptest.NewRequest(http.MethodPost, "http://"+mcpPublicHost+"/oauth/token",
		strings.NewReader(`{"grant_type":"authorization_code"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	m.h.ServeHTTP(rr, req)
	oauthErr(t, rr, http.StatusBadRequest, "invalid_request")

	// A resource bound at authorize must be the one asked for at the token
	// endpoint, even when both are this server's.
	cb := m.login(t, authorizeQuery(mcpClaudeAI, v, url.Values{"resource": {mcpSecond + "/mcp"}}))
	form = tokenForm(codeFrom(t, cb), mcpClaudeAI, v)
	form.Set("resource", mcpPublic+"/mcp")
	oauthErr(t, m.token(mcpSecondHost, form), http.StatusBadRequest, "invalid_target")

	if rr := m.token("evil.example", tokenForm("x", mcpClaudeCode, v)); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown host: status %d; want 404", rr.Code)
	}
}

// The server-mediated `lode login` still completes end to end next to the MCP
// branch, and starting it drops a pending MCP intent.
func TestCLILoginFlowStillWorks(t *testing.T) {
	t.Parallel()
	m := newMCPServer(t, "")
	start := m.get(mcpPublicHost, "/auth/cli/login?state=clistate&redirect_uri="+url.QueryEscape("http://localhost:5555/"))
	if start.Code != http.StatusFound {
		t.Fatalf("cli login = %d", start.Code)
	}
	var clearsMCP bool
	for _, c := range start.Result().Cookies() {
		if c.Name == "wl_mcp" && c.MaxAge < 0 {
			clearsMCP = true
		}
	}
	if !clearsMCP {
		t.Fatal("cli login does not clear a pending MCP intent")
	}
	login := m.get(mcpPublicHost, "/auth/login?next=%2F")
	loc, _ := url.Parse(login.Header().Get("Location"))
	cb := m.get(mcpPublicHost, "/auth/callback?code=fake-code&state="+url.QueryEscape(loc.Query().Get("state")),
		&http.Cookie{Name: "wl_oauth", Value: cookieValue(login, "wl_oauth")},
		&http.Cookie{Name: "wl_cli", Value: cookieValue(start, "wl_cli")})
	u, _ := url.Parse(cb.Header().Get("Location"))
	if cb.Code != http.StatusFound || u.Host != "localhost:5555" || u.Query().Get("state") != "clistate" {
		t.Fatalf("callback = %d %q; want the CLI loopback", cb.Code, cb.Header().Get("Location"))
	}
	rr := doReq(t, m.h, http.MethodPost, "/auth/cli/token", "",
		model.CLITokenInput{Code: u.Query().Get("code"), State: "clistate"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("cli token = %d body %s", rr.Code, rr.Body.String())
	}
}
