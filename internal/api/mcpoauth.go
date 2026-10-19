package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// consentTTL bounds a pending consent and an authorization code: 5 minutes,
// single use (D3 of WL-PLAN-160).
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
	_, ok := s.matchResource(v)
	return ok
}

// resourceOrigin is the origin an authorize was discovered from: the one its
// resource names, else the browser origin. The authorize endpoint always sits
// on the browser origin, so the request's own Host cannot tell.
func (s *server) resourceOrigin(resource string) string {
	if o, ok := s.matchResource(resource); ok {
		return o
	}
	return s.origins()[0]
}

func (s *server) matchResource(v string) (string, bool) {
	v = strings.TrimRight(v, "/")
	for _, o := range s.origins() {
		if v == o || v == o+"/mcp" {
			return o, true
		}
	}
	return "", false
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

// finishLoginMCP ends the Keycloak login of an MCP authorize: it sets the
// browser session that POST /oauth/consent requires, records a pending
// consent and shows the consent page. No code is minted here (WL-RULE-1837).
func (s *server) finishLoginMCP(w http.ResponseWriter, r *http.Request, actorID string, mi mcpIntent) {
	clearAuthCookie(w, mcpCookieName)
	clearAuthCookie(w, oauthCookieName)
	id, err := s.consents.put(pendingConsent{intent: mi, actor: actorID})
	if err != nil {
		s.mcpMetrics.OAuthRequest("callback", "server_error")
		webErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	setAuthCookie(w, sessionCookieName, "/",
		signSession(s.cfg.SessionSecret, actorID, s.now().Add(sessionLifetime)), sessionLifetime)
	setAuthCookie(w, consentCookieName, consentPath, id, consentTTL)
	s.mcpMetrics.OAuthRequest("callback", "ok")
	dest, _ := clientRedirect(mi.ClientID, mi.RedirectURI)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	view := ui.ConsentView{Title: "worklode: connect", ActorID: actorID, Destination: dest, ConsentID: id, Action: consentPath}
	if err := ui.Consent(view).Render(r.Context(), w); err != nil {
		s.log.Error("render consent page", "err", err)
	}
}

const (
	consentCookieName = "wl_consent"
	consentPath       = "/oauth/consent"
)

// oauthConsent handles POST /oauth/consent (WL-REQ-1838): a same-origin form
// post from the signed-in user, carrying the consent id its cookie also
// carries. Allow is the only place an MCP authorization code is minted
// (WL-RULE-1837).
func (s *server) oauthConsent(w http.ResponseWriter, r *http.Request) {
	refuse := func(code int, msg string) {
		s.mcpMetrics.OAuthRequest("consent", "invalid_request")
		webErr(w, code, msg)
	}
	if !s.sameOriginForm(r) {
		refuse(http.StatusForbidden, "cross-site form submission refused")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWebForm)
	if err := r.ParseForm(); err != nil {
		refuse(http.StatusBadRequest, "bad form")
		return
	}
	sc, err := r.Cookie(sessionCookieName)
	if err != nil {
		refuse(http.StatusForbidden, "connecting requires a signed-in browser session")
		return
	}
	actor, ok := verifySession(s.cfg.SessionSecret, sc.Value, s.now())
	if !ok {
		refuse(http.StatusForbidden, "connecting requires a signed-in browser session")
		return
	}
	id := r.PostFormValue("consent_id")
	cc, err := r.Cookie(consentCookieName)
	if err != nil || id == "" || subtle.ConstantTimeCompare([]byte(cc.Value), []byte(id)) != 1 {
		refuse(http.StatusForbidden, "consent does not match this browser")
		return
	}
	p, ok := s.consents.take(id)
	if !ok || p.actor != actor {
		refuse(http.StatusBadRequest, "this consent has expired or was already used; reconnect from the app")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: consentCookieName, Path: consentPath, MaxAge: -1})
	mi := p.intent
	v := url.Values{"state": {mi.State}}
	if r.PostFormValue("decision") != "allow" {
		s.mcpMetrics.OAuthRequest("consent", "access_denied")
		v.Set("error", "access_denied")
		http.Redirect(w, r, mi.RedirectURI+"?"+v.Encode(), http.StatusFound)
		return
	}
	code, err := s.mcpCodes.put(mcpCode{
		actor: p.actor, clientID: mi.ClientID, redirectURI: mi.RedirectURI,
		codeChallenge: mi.CodeChallenge, resource: mi.Resource,
	})
	if err != nil {
		s.mcpMetrics.OAuthRequest("consent", "server_error")
		webErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.mcpMetrics.OAuthRequest("consent", "ok")
	v.Set("code", code)
	v.Set("iss", s.resourceOrigin(mi.Resource)) // RFC 9207
	http.Redirect(w, r, mi.RedirectURI+"?"+v.Encode(), http.StatusFound)
}

// pendingConsent is an MCP login waiting for its user's Allow or Deny.
type pendingConsent struct {
	intent mcpIntent
	actor  string
}

// mcpCode is an authorization code's binding (D6 of WL-PLAN-160); its expiry
// is the store's. /oauth/token redeems it with take.
type mcpCode struct {
	actor, clientID, redirectURI, codeChallenge, resource string
}

// consentStore is the pending-consent store; mcpCodes uses the same shape.
type consentStore = ttlStore[pendingConsent]

func newConsentStore(now func() time.Time) *consentStore { return newTTLStore[pendingConsent](now) }

// ttlStore holds single-use values in memory for consentTTL, keyed on a random
// id (D3 of WL-PLAN-160). A restart drops them; the user reconnects.
type ttlStore[T any] struct {
	mu  sync.Mutex
	m   map[string]ttlEntry[T]
	now func() time.Time
}

type ttlEntry[T any] struct {
	v       T
	expires time.Time
}

func newTTLStore[T any](now func() time.Time) *ttlStore[T] {
	return &ttlStore[T]{m: map[string]ttlEntry[T]{}, now: now}
}

// put stores v under a fresh id for consentTTL and drops expired entries.
func (c *ttlStore[T]) put(v T) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for k, e := range c.m {
		if now.After(e.expires) {
			delete(c.m, k)
		}
	}
	c.m[id] = ttlEntry[T]{v: v, expires: now.Add(consentTTL)}
	return id, nil
}

// take returns the value under id and spends it. Unknown, spent and expired
// ids all fail.
func (c *ttlStore[T]) take(id string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[id]
	delete(c.m, id)
	if !ok || c.now().After(e.expires) {
		var zero T
		return zero, false
	}
	return e.v, true
}
