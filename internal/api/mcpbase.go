// mcpbase.go holds what the MCP server (mcp.go) and its OAuth endpoints
// (mcpauth.go) share: the base URLs MCP clients reach this server on, and the
// one client they log in as.
package api

import (
	"net/http"
	"net/url"
	"strings"
)

// mcpClientID is the one OAuth client MCP clients use. It is registered here,
// not in Keycloak, and there is no dynamic client registration: Claude Code
// names it in .mcp.json and claude.ai or Cowork in the connector's advanced
// settings.
const mcpClientID = "worklode-mcp"

// mcpRemoteRedirects are the non-loopback redirect URIs mcpClientID may use.
// claude.ai and Cowork both finish a connector login here.
var mcpRemoteRedirects = []string{"https://claude.ai/api/mcp/auth_callback"}

// mcpLoopbackPath is the path a loopback redirect URI must carry. Any port is
// accepted (RFC 8252 section 7.3); Claude Code uses 33418 when told to.
const mcpLoopbackPath = "/callback"

// mcpRedirectAllowed reports whether raw is a redirect URI mcpClientID may
// use: one of mcpRemoteRedirects exactly, or an http loopback URI with an
// explicit port and path mcpLoopbackPath.
func mcpRedirectAllowed(raw string) bool {
	for _, r := range mcpRemoteRedirects {
		if raw == r {
			return true
		}
	}
	if !isLoopbackRedirect(raw) {
		return false
	}
	u, _ := url.Parse(raw)
	return u.Path == mcpLoopbackPath && u.RawQuery == "" && u.Fragment == ""
}

// mcpOrigins returns the base URLs MCP clients may reach this server on:
// LODE_PUBLIC_URL, then LODE_MCP_PUBLIC_URL when set. Each has no trailing
// slash. Empty when OIDC is unconfigured, since MCP needs its login.
func (s *server) mcpOrigins() []string {
	if s.oidc == nil {
		return nil
	}
	out := []string{strings.TrimRight(s.cfg.PublicURL, "/")}
	if s.cfg.MCPPublicURL != "" {
		out = append(out, strings.TrimRight(s.cfg.MCPPublicURL, "/"))
	}
	return out
}

// mcpOrigin returns the configured origin whose host the request arrived on.
// The Host header only selects among configured values; it is never echoed,
// so a forged Host cannot make the server name a URL it was not given.
func (s *server) mcpOrigin(r *http.Request) (string, bool) {
	for _, o := range s.mcpOrigins() {
		u, err := url.Parse(o)
		if err == nil && strings.EqualFold(u.Host, r.Host) {
			return o, true
		}
	}
	return "", false
}

// mcpResource is the MCP endpoint URL on origin: the resource identifier
// (RFC 8707) a token is issued for and the URL a client is configured with.
func mcpResource(origin string) string { return origin + "/mcp" }

// mcpResourceAllowed reports whether resource names /mcp on one of
// mcpOrigins. A trailing slash is tolerated, since clients differ on it.
func (s *server) mcpResourceAllowed(resource string) bool {
	resource = strings.TrimRight(resource, "/")
	for _, o := range s.mcpOrigins() {
		if resource == mcpResource(o) {
			return true
		}
	}
	return false
}
