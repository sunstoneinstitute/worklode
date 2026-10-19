package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// origins returns the base URLs MCP clients may discover: the browser origin
// and, when configured, the MCP origin (WL-REQ-1841).
func (s *server) origins() []string {
	out := []string{strings.TrimRight(s.cfg.PublicURL, "/")}
	if s.cfg.MCPPublicURL != "" {
		out = append(out, strings.TrimRight(s.cfg.MCPPublicURL, "/"))
	}
	return out
}

// originFor picks the base URL whose host matches the request's Host header,
// falling back to the browser origin.
func (s *server) originFor(r *http.Request) string {
	all := s.origins()
	for _, o := range all {
		if hostOf(o) != "" && strings.EqualFold(hostOf(o), r.Host) {
			return o
		}
	}
	return all[0]
}

// advertise builds an advertised URL: origin is a base URL from origins(), so
// the MCP origin's path prefix (/worklode on prod) is written back.
func advertise(origin, path string) string {
	return strings.TrimRight(origin, "/") + path
}

// isResource reports whether s names an MCP resource: either base URL, or
// either base URL plus /mcp (WL-REQ-1836).
func (s *server) isResource(v string) bool {
	v = strings.TrimRight(v, "/")
	for _, o := range s.origins() {
		if v == o || v == o+"/mcp" {
			return true
		}
	}
	return false
}

func hostOf(base string) string {
	b := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	host, _, _ := strings.Cut(b, "/")
	return host
}

// wellKnownURL is the RFC 8615 location of a metadata document for the
// resource or issuer at origin+suffix: the well-known segment goes between the
// host and the path, so the MCP origin's /worklode prefix follows it.
func wellKnownURL(origin, name, suffix string) string {
	u, err := url.Parse(strings.TrimRight(origin, "/"))
	if err != nil {
		return advertise(origin, "/.well-known/"+name+suffix)
	}
	return u.Scheme + "://" + u.Host + "/.well-known/" + name + u.Path + suffix
}

// protectedResourceMetadata serves RFC 9728 metadata for the requesting
// origin's /mcp resource (WL-REQ-1834).
func (s *server) protectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	o := s.originFor(r)
	s.mcpMetrics.OAuthRequest("metadata", "ok")
	writeJSON(w, http.StatusOK, model.OAuthProtectedResource{
		Resource:               advertise(o, "/mcp"),
		AuthorizationServers:   []string{o},
		BearerMethodsSupported: []string{"header"},
	})
}

// authorizationServerMetadata serves RFC 8414 metadata. The issuer and token
// endpoint sit on the requesting origin, the authorize endpoint on the browser
// origin where the Keycloak login runs (WL-REQ-1834).
func (s *server) authorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	o := s.originFor(r)
	s.mcpMetrics.OAuthRequest("metadata", "ok")
	writeJSON(w, http.StatusOK, model.OAuthServerMetadata{
		Issuer:                            o,
		AuthorizationEndpoint:             advertise(strings.TrimRight(s.cfg.PublicURL, "/"), "/oauth/authorize"),
		TokenEndpoint:                     advertise(o, "/oauth/token"),
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none"},
	})
}

// challengeWriter adds the RFC 9728 WWW-Authenticate header to a 401, whoever
// writes it.
type challengeWriter struct {
	http.ResponseWriter
	challenge string
}

func (c challengeWriter) WriteHeader(code int) {
	if code == http.StatusUnauthorized {
		c.Header().Set("WWW-Authenticate", c.challenge)
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c challengeWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// withChallenge makes every 401 from h point at the requesting origin's
// protected-resource metadata (WL-REQ-1834).
func (s *server) withChallenge(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		o := s.originFor(r)
		h(challengeWriter{w, `Bearer resource_metadata="` + wellKnownURL(o, "oauth-protected-resource", "/mcp") + `"`}, r)
	}
}
