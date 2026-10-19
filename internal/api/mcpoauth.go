package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// consentTTL bounds a pending consent: 5 minutes, single use (D3 of WL-PLAN-160).
const consentTTL = 5 * time.Minute

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

// mcpIntent is the payload of the signed cookie /oauth/authorize sets. Nonce
// is also in the login's OAuth state, and finishLogin acts on the intent only
// when the two match (WL-REQ-1835).
type mcpIntent struct {
	ClientID      string `json:"c"`
	RedirectURI   string `json:"r"`
	CodeChallenge string `json:"cc"`
	Resource      string `json:"res,omitempty"`
	State         string `json:"s"`
	Nonce         string `json:"nn"`
	Exp           int64  `json:"e"`
}

func signMCPIntent(secret string, mi mcpIntent) string {
	payload, _ := json.Marshal(mi)
	return signPayload(secret, payload)
}

func verifyMCPIntent(secret, value string, now time.Time) (mcpIntent, bool) {
	payload, ok := verifyPayload(secret, value)
	if !ok {
		return mcpIntent{}, false
	}
	var mi mcpIntent
	if err := json.Unmarshal(payload, &mi); err != nil || now.Unix() >= mi.Exp {
		return mcpIntent{}, false
	}
	return mi, true
}

func (s *server) mcpIntentFrom(r *http.Request) (mcpIntent, bool) {
	c, err := r.Cookie(mcpCookieName)
	if err != nil {
		return mcpIntent{}, false
	}
	return verifyMCPIntent(s.cfg.SessionSecret, c.Value, s.now())
}

// oauthAuthorize handles GET /oauth/authorize (WL-REQ-1835). A bad client_id
// or redirect_uri gets an error page and never a redirect; other errors go
// back to the redirect URI with the OAuth error and state (D7 of WL-PLAN-160).
func (s *server) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		s.mcpMetrics.OAuthRequest("authorize", "not_found")
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	q := r.URL.Query()
	clientID, redirect, state, resource := q.Get("client_id"), q.Get("redirect_uri"), q.Get("state"), q.Get("resource")
	if _, ok := mcpClients[clientID]; !ok {
		s.mcpMetrics.OAuthRequest("authorize", "invalid_client")
		webErr(w, http.StatusBadRequest, "unknown client_id")
		return
	}
	if _, ok := clientRedirect(clientID, redirect); !ok {
		s.mcpMetrics.OAuthRequest("authorize", "invalid_request")
		webErr(w, http.StatusBadRequest, "redirect_uri is not registered for this client")
		return
	}
	var oauthErr string
	switch {
	case q.Get("response_type") != "code",
		q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256",
		state == "",
		resource == "" && len(s.origins()) > 1: // WL-REQ-1836
		oauthErr = "invalid_request"
	case resource != "" && !s.isResource(resource):
		oauthErr = "invalid_target"
	}
	if oauthErr != "" {
		s.mcpMetrics.OAuthRequest("authorize", oauthErr)
		v := url.Values{"error": {oauthErr}}
		if state != "" {
			v.Set("state", state)
		}
		http.Redirect(w, r, redirect+"?"+v.Encode(), http.StatusFound)
		return
	}
	nonce, err := randToken()
	if err != nil {
		webErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	setAuthCookie(w, mcpCookieName, "/auth/", signMCPIntent(s.cfg.SessionSecret, mcpIntent{
		ClientID: clientID, RedirectURI: redirect, CodeChallenge: q.Get("code_challenge"),
		Resource: resource, State: state, Nonce: nonce, Exp: s.now().Add(oauthStateMaxAge).Unix(),
	}), oauthStateMaxAge)
	s.mcpMetrics.OAuthRequest("authorize", "ok")
	s.startLogin(w, r, "/", nonce)
}

// finishLoginMCP ends the Keycloak login of an MCP authorize: it records a
// pending consent and shows the consent page. Nothing is minted here
// (WL-RULE-1837).
func (s *server) finishLoginMCP(w http.ResponseWriter, r *http.Request, actorID string, mi mcpIntent) {
	clearAuthCookie(w, mcpCookieName)
	clearAuthCookie(w, oauthCookieName)
	p := pendingConsent{intent: mi, actor: actorID}
	id, err := s.consents.put(p)
	if err != nil {
		s.mcpMetrics.OAuthRequest("callback", "server_error")
		webErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.mcpMetrics.OAuthRequest("callback", "ok")
	s.renderConsent(w, r, id, p)
}

// renderConsent shows who is signing in and where the token goes. It is a
// placeholder for WL-1203's consent page: it offers no Allow, so no code can
// follow from it (WL-RULE-1837).
func (s *server) renderConsent(w http.ResponseWriter, _ *http.Request, _ string, p pendingConsent) {
	dest, _ := clientRedirect(p.intent.ClientID, p.intent.RedirectURI)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, "<!doctype html><title>worklode: connect</title><p>Signed in as %s. Connecting %s is not available yet.</p>",
		html.EscapeString(p.actor), html.EscapeString(dest))
}

// pendingConsent is an MCP login waiting for its user's Allow or Deny.
type pendingConsent struct {
	intent  mcpIntent
	actor   string
	expires time.Time
}

// consentStore holds pending consents in memory, keyed on a random id (D3 of
// WL-PLAN-160). A restart drops them; the user reconnects.
type consentStore struct {
	mu  sync.Mutex
	m   map[string]pendingConsent
	now func() time.Time
}

func newConsentStore(now func() time.Time) *consentStore {
	return &consentStore{m: map[string]pendingConsent{}, now: now}
}

// put stores p under a fresh id for consentTTL and drops expired entries.
func (c *consentStore) put(p pendingConsent) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for k, v := range c.m {
		if now.After(v.expires) {
			delete(c.m, k)
		}
	}
	p.expires = now.Add(consentTTL)
	c.m[id] = p
	return id, nil
}
