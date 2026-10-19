package api

import (
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
