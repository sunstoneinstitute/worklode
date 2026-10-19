package api

import (
	"net/http"
	"strings"
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
