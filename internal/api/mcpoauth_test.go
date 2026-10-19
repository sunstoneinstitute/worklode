package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
