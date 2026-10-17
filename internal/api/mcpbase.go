// mcpbase.go mounts the MCP tool server (internal/mcp) at /mcp and holds
// what it and its OAuth endpoints (mcpauth.go) share: the base URLs MCP
// clients reach this server on, the one client they log in as, the RFC 9728
// protected-resource metadata, and the 401 challenge that points at it.
package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/mcp"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// registerMCP registers /mcp and the protected-resource metadata. The MCP
// tool server's tools call back into r's mux.
func (s *server) registerMCP(r *router, reg prometheus.Registerer) error {
	h, err := mcp.New(mcp.Config{
		// The mux is the one every route is registered on, including those
		// registered after this call; observe gives a loopback call the same
		// request log and metrics a network call gets.
		API:          s.observe(r.mux),
		Authenticate: s.mcpAuthenticate,
		Unauthorized: s.mcpUnauthorized,
		PublicURL:    s.cfg.PublicURL,
		Log:          s.log,
		Registerer:   reg,
	})
	if err != nil {
		return err
	}
	for _, p := range []string{"POST /mcp", "GET /mcp", "DELETE /mcp"} {
		r.public(p, h)
	}
	r.publicFunc("GET /.well-known/oauth-protected-resource", s.serveMCPResourceMetadata)
	r.publicFunc("GET /.well-known/oauth-protected-resource/mcp", s.serveMCPResourceMetadata)
	return nil
}

// mcpAuthenticate resolves an MCP request's wl_ token to its actor, for
// mcp.Config.Authenticate. An unknown token answers the 401 challenge.
func (s *server) mcpAuthenticate(w http.ResponseWriter, r *http.Request, token string) (string, bool) {
	actor, _, err := s.st.Authenticate(r.Context(), token)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.mcpUnauthorized(w, r)
			return "", false
		}
		s.mapStoreErr(w, err)
		return "", false
	}
	return actor.ID, true
}

// mcpUnauthorized answers 401 with the RFC 9728 pointer to this origin's
// protected-resource metadata, which is how an MCP client finds the login.
// An instance with no MCP origin (OIDC unconfigured) has no login to point
// at, so the challenge is bare.
func (s *server) mcpUnauthorized(w http.ResponseWriter, r *http.Request) {
	challenge := "Bearer"
	if origin, ok := s.mcpOrigin(r); ok {
		challenge += fmt.Sprintf(` resource_metadata=%q`, origin+"/.well-known/oauth-protected-resource/mcp")
	}
	w.Header().Set("WWW-Authenticate", challenge)
	writeErr(w, http.StatusUnauthorized, "unauthorized")
}

// serveMCPResourceMetadata answers GET /.well-known/oauth-protected-resource
// and its /mcp suffixed form (RFC 9728 section 3.1) for the origin the request
// arrived on. 404 when the host is not one of mcpOrigins, which includes
// every request to an instance with OIDC unconfigured.
func (s *server) serveMCPResourceMetadata(w http.ResponseWriter, r *http.Request) {
	origin, ok := s.mcpOrigin(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "no MCP login on this host")
		return
	}
	// Public discovery data, read by clients on other origins.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	writeJSON(w, http.StatusOK, model.ProtectedResourceMetadata{
		Resource:               mcpResource(origin),
		AuthorizationServers:   []string{origin},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "Worklode",
	})
}

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
