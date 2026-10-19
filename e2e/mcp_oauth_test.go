//go:build e2e

package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/oidc/oidctest"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestMCPOAuth drives the whole connector flow over HTTP: metadata, authorize
// with PKCE, the fake login, the consent page, Allow, the code exchange, a
// tool call with the token, and a replayed code (WL-REQ-1834, WL-RULE-1837).
func TestMCPOAuth(t *testing.T) {
	ctx := context.Background()
	st := store.OpenTestStore(t)
	iss := oidctest.NewIssuer(t)
	iss.TokenClaims = map[string]any{
		"preferred_username": "grace", "name": "Grace", "aud": iss.ClientID, "groups": []string{"user"},
	}
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		OIDCIssuer:     iss.URL(),
		OIDCClientID:   iss.ClientID,
		PublicURL:      "http://localhost:8080",
		SessionSecret:  "e2e-mcp-oauth-secret",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	do := func(req *http.Request) (*http.Response, string) {
		t.Helper()
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	getJSON := func(path string) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		resp, body := do(req)
		var m map[string]any
		if resp.StatusCode != http.StatusOK || json.Unmarshal([]byte(body), &m) != nil {
			t.Fatalf("GET %s: %d %s", path, resp.StatusCode, body)
		}
		return m
	}

	// 1. Metadata.
	pr := getJSON("/.well-known/oauth-protected-resource/mcp")
	as := getJSON("/.well-known/oauth-authorization-server")
	if pr["resource"] != "http://localhost:8080/mcp" {
		t.Fatalf("protected resource = %v", pr)
	}
	authzEndpoint, _ := as["authorization_endpoint"].(string)
	tokenEndpoint, _ := as["token_endpoint"].(string)
	if !strings.HasSuffix(authzEndpoint, "/oauth/authorize") || !strings.HasSuffix(tokenEndpoint, "/oauth/token") {
		t.Fatalf("authorization server = %v", as)
	}

	// 2. Authorize with a PKCE pair and the loopback redirect.
	const verifier = "e2e-pkce-verifier-0123456789-0123456789-0123456789"
	sum := sha256.Sum256([]byte(verifier))
	const redirect = "http://127.0.0.1:33418/callback"
	q := url.Values{
		"response_type": {"code"}, "client_id": {"worklode-mcp"}, "redirect_uri": {redirect},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"state": {"st8"},
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/oauth/authorize?"+q.Encode(), nil)
	resp, body := do(req)
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(resp.Header.Get("Location"), iss.URL()) {
		t.Fatalf("authorize: %d %s", resp.StatusCode, body)
	}
	oauthCookie := cookieNamed(t, resp, "wl_oauth", "authorize")
	intentCookie := cookieNamed(t, resp, "wl_mcp", "authorize")
	idp, _ := url.Parse(resp.Header.Get("Location"))

	// 3. Fake login: the callback shows the consent page and signs the browser in.
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/auth/callback?code=fake-code&state="+url.QueryEscape(idp.Query().Get("state")), nil)
	req.AddCookie(&http.Cookie{Name: "wl_oauth", Value: oauthCookie})
	req.AddCookie(&http.Cookie{Name: "wl_mcp", Value: intentCookie})
	resp, body = do(req)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "grace") {
		t.Fatalf("consent page: %d %s", resp.StatusCode, body)
	}
	session := cookieNamed(t, resp, "wl_session", "consent")
	consentCookie := cookieNamed(t, resp, "wl_consent", "consent")
	m := regexp.MustCompile(`name="consent_id" value="([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("consent page has no consent_id: %s", body)
	}

	// 4. Allow.
	post := func(path string, form url.Values, cookies ...*http.Cookie) (*http.Response, string) {
		t.Helper()
		r, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", srv.URL)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		return do(r)
	}
	resp, body = post("/oauth/consent", url.Values{"consent_id": {m[1]}, "decision": {"allow"}},
		&http.Cookie{Name: "wl_session", Value: session}, &http.Cookie{Name: "wl_consent", Value: consentCookie})
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := loc.Query().Get("code")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(loc.String(), redirect+"?") ||
		code == "" || loc.Query().Get("state") != "st8" || loc.Query().Get("iss") == "" {
		t.Fatalf("allow: %d %s %s", resp.StatusCode, loc, body)
	}

	// 5. Exchange the code.
	exchange := func() (*http.Response, string) {
		return post("/oauth/token", url.Values{
			"grant_type": {"authorization_code"}, "client_id": {"worklode-mcp"}, "code": {code},
			"redirect_uri": {redirect}, "code_verifier": {verifier},
		})
	}
	resp, body = exchange()
	var tok struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal([]byte(body), &tok) != nil ||
		!strings.HasPrefix(tok.AccessToken, "wl_") || tok.TokenType != "Bearer" {
		t.Fatalf("token: %d %s", resp.StatusCode, body)
	}

	// 6. The token works on /mcp.
	c := sdk.NewClient(&sdk.Implementation{Name: "e2e", Version: "0"}, nil)
	cs, err := c.Connect(ctx, &sdk.StreamableClientTransport{
		Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: mcpBearerRT(tok.AccessToken)}, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "project_list"})
	if err != nil || res.IsError {
		t.Fatalf("project_list: %v %+v", err, res)
	}

	// 7. The code is single use.
	resp, body = exchange()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, `"invalid_grant"`) {
		t.Fatalf("replay: %d %s", resp.StatusCode, body)
	}
}
