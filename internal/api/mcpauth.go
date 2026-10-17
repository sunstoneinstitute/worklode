// mcpauth.go makes lode-server the OAuth 2.1 authorization server MCP clients
// (Claude Code, claude.ai, Cowork) log in to. Keycloak stays upstream: the
// browser leg is the ordinary web login, and finishLogin hands the result back
// here instead of setting a session.
//
//   - GET /.well-known/oauth-authorization-server (RFC 8414) describes the
//     server on whichever MCP origin the client asked on.
//   - GET /oauth/authorize checks the client, its redirect URI and its PKCE
//     challenge, stamps them into a signed cookie, and starts the web login.
//   - finishLogin (cliauth.go) sees that cookie, mints a single-use code bound
//     to the actor and the challenge, and redirects to the client.
//   - POST /oauth/token redeems the code and the PKCE verifier for a wl_ token.
//
// The one client is mcpClientID, a public client with no secret and no
// dynamic registration (mcpbase.go).
package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// mcpCookieName carries the MCP-intent cookie across the web login, on the
// same /auth/ path as the CLI one so /auth/callback sees it.
const mcpCookieName = "wl_mcp"

// mcpCodeTTL bounds the code between the redirect and the client's token
// request. The client exchanges it without a human in the loop, so the CLI's
// five minutes is generous; single use and the PKCE binding make it safe.
const mcpCodeTTL = cliCodeTTL

// maxTokenForm caps the POST /oauth/token body. The form carries five short
// fields.
const maxTokenForm = 16 << 10

// mcpIntent is the payload of the short-lived cookie set at /oauth/authorize:
// what the client asked for, carried across the Keycloak redirect so
// finishLogin can bind a code to it.
type mcpIntent struct {
	Redirect  string `json:"r"`
	State     string `json:"s,omitempty"`
	Challenge string `json:"c"`
	Resource  string `json:"res,omitempty"`
	Exp       int64  `json:"e"`
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
	if err := json.Unmarshal(payload, &mi); err != nil {
		return mcpIntent{}, false
	}
	if now.Unix() >= mi.Exp {
		return mcpIntent{}, false
	}
	return mi, true
}

// mcpIntentFrom returns the verified MCP-login intent carried by the
// request's cookie, if it carries one.
func (s *server) mcpIntentFrom(r *http.Request) (mcpIntent, bool) {
	c, err := r.Cookie(mcpCookieName)
	if err != nil {
		return mcpIntent{}, false
	}
	return verifyMCPIntent(s.cfg.SessionSecret, c.Value, s.now())
}

// mcpCode is what a pending authorization code is bound to.
type mcpCode struct {
	actorID   string
	redirect  string
	challenge string
	resource  string
	expires   time.Time
}

// mcpCodeStore holds pending authorization codes in memory, like
// cliCodeStore: the server is single-instance, so a restart drops them and
// the client logs in again.
type mcpCodeStore struct {
	mu    sync.Mutex
	codes map[string]mcpCode
	now   func() time.Time
}

func newMCPCodeStore(now func() time.Time) *mcpCodeStore {
	return &mcpCodeStore{codes: map[string]mcpCode{}, now: now}
}

// mint stores a fresh single-use code bound to c, setting its expiry.
func (s *mcpCodeStore) mint(c mcpCode) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	code := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	c.expires = s.now().Add(mcpCodeTTL)
	s.codes[code] = c
	return code, nil
}

// take consumes code and returns what it was bound to. Unlike
// cliCodeStore.redeem, every attempt consumes it: the checks that follow
// (redirect URI, PKCE verifier) happen after take, so a wrong verifier burns
// the code instead of leaving it open to guessing.
func (s *mcpCodeStore) take(code string) (mcpCode, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.codes[code]
	delete(s.codes, code)
	if !ok || s.now().After(c.expires) {
		return mcpCode{}, false
	}
	return c, true
}

// The endpoint label values of worklode_mcp_oauth_requests_total, each with
// the outcomes it can report. callback is finishLogin's MCP branch.
var mcpOAuthOutcomes = map[string][]string{
	"metadata":  {"ok", "not_found"},
	"authorize": {"ok", "not_found", "invalid_client", "invalid_redirect", "invalid_request", "unsupported_response_type", "invalid_target"},
	"callback":  {"ok", "error"},
	"token":     {"ok", "not_found", "invalid_request", "invalid_client", "unsupported_grant_type", "invalid_grant", "invalid_target", "error"},
}

// mcpOAuthMetrics counts MCP OAuth requests by endpoint and outcome. Nil-safe.
type mcpOAuthMetrics struct {
	requests *prometheus.CounterVec
}

func newMCPOAuthMetrics(reg prometheus.Registerer) *mcpOAuthMetrics {
	m := &mcpOAuthMetrics{requests: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_mcp_oauth_requests_total",
		Help: "MCP OAuth requests, by endpoint (metadata, authorize, callback, token) and outcome (ok, not_found, or the OAuth error code). " +
			"Labels are bounded: the client, the redirect URI and the actor are deliberately not among them.",
	}, []string{"endpoint", "outcome"})}
	for endpoint, outcomes := range mcpOAuthOutcomes {
		for _, o := range outcomes {
			m.requests.WithLabelValues(endpoint, o)
		}
	}
	reg.MustRegister(m.requests)
	return m
}

func (m *mcpOAuthMetrics) observe(endpoint, outcome string) {
	if m != nil {
		m.requests.WithLabelValues(endpoint, outcome).Inc()
	}
}

// registerMCPAuth registers the three MCP OAuth routes and the state they
// share.
func (s *server) registerMCPAuth(r *router, reg prometheus.Registerer) {
	s.mcpCodes = newMCPCodeStore(s.st.Now)
	s.mcpOAuth = newMCPOAuthMetrics(reg)
	r.publicFunc("GET /.well-known/oauth-authorization-server", s.mcpServerMetadata)
	r.publicFunc("GET /oauth/authorize", s.mcpAuthorize)
	r.publicFunc("POST /oauth/token", s.mcpToken)
}

// mcpServerMetadata handles GET /.well-known/oauth-authorization-server. The
// issuer is the origin the request arrived on, so a client that found /mcp on
// LODE_MCP_PUBLIC_URL sees that as the issuer and its token endpoint. 404
// when OIDC is unconfigured or the host is not an MCP origin.
func (s *server) mcpServerMetadata(w http.ResponseWriter, r *http.Request) {
	origin, ok := s.mcpOrigin(r)
	if !ok {
		s.mcpOAuth.observe("metadata", "not_found")
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	s.mcpOAuth.observe("metadata", "ok")
	writeJSON(w, http.StatusOK, model.OAuthServerMetadata{
		Issuer:                                     origin,
		AuthorizationEndpoint:                      strings.TrimRight(s.cfg.PublicURL, "/") + "/oauth/authorize",
		TokenEndpoint:                              origin + "/oauth/token",
		ResponseTypesSupported:                     []string{"code"},
		GrantTypesSupported:                        []string{"authorization_code"},
		CodeChallengeMethodsSupported:              []string{"S256"},
		TokenEndpointAuthMethodsSupported:          []string{"none"},
		AuthorizationResponseIssParameterSupported: true,
	})
}

// onPublicHost reports whether the request arrived on LODE_PUBLIC_URL's host.
func (s *server) onPublicHost(r *http.Request) bool {
	u, err := url.Parse(s.cfg.PublicURL)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// mcpIssuer is the issuer a response for resource names: the origin resource
// sits on, or LODE_PUBLIC_URL when the client named no resource.
func (s *server) mcpIssuer(resource string) string {
	origins := s.mcpOrigins()
	resource = strings.TrimRight(resource, "/")
	for _, o := range origins {
		if resource == mcpResource(o) {
			return o
		}
	}
	return strings.TrimRight(s.cfg.PublicURL, "/")
}

// validS256Challenge reports whether c has the shape of an S256 code
// challenge: base64url without padding of a 32-byte SHA-256 digest.
func validS256Challenge(c string) bool {
	b, err := base64.RawURLEncoding.DecodeString(c)
	return err == nil && len(b) == sha256.Size
}

// mcpAuthorize handles GET /oauth/authorize. It answers only on
// LODE_PUBLIC_URL's host, where the web login and its Keycloak callback live.
// A bad client_id or redirect_uri is shown to the user, never redirected to:
// until both are checked, the redirect URI is the attacker's. Every later
// error goes back to the client as RFC 6749 §4.1.2.1 describes.
func (s *server) mcpAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil || !s.onPublicHost(r) {
		s.mcpOAuth.observe("authorize", "not_found")
		webErr(w, http.StatusNotFound, "not found")
		return
	}
	q := r.URL.Query()
	if q.Get("client_id") != mcpClientID {
		s.mcpOAuth.observe("authorize", "invalid_client")
		webErr(w, http.StatusBadRequest, "unknown client_id")
		return
	}
	redirect := q.Get("redirect_uri")
	if len(q["redirect_uri"]) != 1 || !mcpRedirectAllowed(redirect) {
		s.mcpOAuth.observe("authorize", "invalid_redirect")
		webErr(w, http.StatusBadRequest, "redirect_uri is not registered for this client")
		return
	}
	state := q.Get("state")
	resource := q.Get("resource")
	fail := func(code, desc string) {
		s.mcpOAuth.observe("authorize", code)
		u, _ := url.Parse(redirect)
		v := url.Values{"error": {code}, "error_description": {desc}, "iss": {s.mcpIssuer(resource)}}
		if state != "" {
			v.Set("state", state)
		}
		u.RawQuery = v.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	}
	if q.Get("response_type") != "code" {
		fail("unsupported_response_type", "response_type must be code")
		return
	}
	challenge := q.Get("code_challenge")
	if q.Get("code_challenge_method") != "S256" || !validS256Challenge(challenge) {
		fail("invalid_request", "code_challenge with code_challenge_method S256 is required")
		return
	}
	if len(q["resource"]) > 1 || (resource != "" && !s.mcpResourceAllowed(resource)) {
		fail("invalid_target", "resource must name this server's /mcp endpoint")
		return
	}
	setAuthCookie(w, mcpCookieName, "/auth/",
		signMCPIntent(s.cfg.SessionSecret, mcpIntent{
			Redirect: redirect, State: state, Challenge: challenge, Resource: resource,
			Exp: s.now().Add(oauthStateMaxAge).Unix(),
		}),
		oauthStateMaxAge)
	// One login at a time: a CLI intent left from an abandoned `lode login`
	// would otherwise win in finishLogin.
	clearAuthCookie(w, cliCookieName)
	s.mcpOAuth.observe("authorize", "ok")
	http.Redirect(w, r, s.loginTarget("/"), http.StatusFound)
}

// finishLoginMCP ends a web login that /oauth/authorize started: it mints the
// authorization code and sends the browser back to the client with it.
func (s *server) finishLoginMCP(w http.ResponseWriter, r *http.Request, actorID string, mi mcpIntent) {
	code, err := s.mcpCodes.mint(mcpCode{
		actorID: actorID, redirect: mi.Redirect, challenge: mi.Challenge, resource: mi.Resource,
	})
	if err != nil {
		s.mcpOAuth.observe("callback", "error")
		s.log.Error("mint mcp code", "err", err)
		webErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	clearAuthCookie(w, mcpCookieName)
	clearAuthCookie(w, oauthCookieName)
	// Redirect was checked by mcpRedirectAllowed at /oauth/authorize and has
	// no query of its own.
	u, err := url.Parse(mi.Redirect)
	if err != nil {
		s.mcpOAuth.observe("callback", "error")
		s.log.Error("parse mcp redirect", "err", err)
		webErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	v := url.Values{"code": {code}, "iss": {s.mcpIssuer(mi.Resource)}}
	if mi.State != "" {
		v.Set("state", mi.State)
	}
	u.RawQuery = v.Encode()
	s.mcpOAuth.observe("callback", "ok")
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// pkceMatches reports whether verifier hashes to the S256 challenge. The
// verifier's shape is RFC 7636 §4.1's: 43 to 128 unreserved characters.
func pkceMatches(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for _, c := range verifier {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}

// mcpToken handles POST /oauth/token: redeem an authorization code and its
// PKCE verifier for a 30-day wl_ token, the lifetime `lode login` gets. 404
// when OIDC is unconfigured or the host is not an MCP origin.
func (s *server) mcpToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	fail := func(status int, code, desc string) {
		s.mcpOAuth.observe("token", code)
		writeJSON(w, status, model.OAuthError{Error: code, ErrorDescription: desc})
	}
	if _, ok := s.mcpOrigin(r); !ok {
		s.mcpOAuth.observe("token", "not_found")
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/x-www-form-urlencoded" {
		fail(http.StatusBadRequest, "invalid_request", "body must be application/x-www-form-urlencoded")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTokenForm)
	if err := r.ParseForm(); err != nil {
		fail(http.StatusBadRequest, "invalid_request", "unreadable form body")
		return
	}
	f := r.PostForm
	if f.Get("grant_type") != "authorization_code" {
		fail(http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code")
		return
	}
	if f.Get("client_id") != mcpClientID {
		fail(http.StatusUnauthorized, "invalid_client", "unknown client_id")
		return
	}
	c, ok := s.mcpCodes.take(f.Get("code"))
	if !ok {
		fail(http.StatusBadRequest, "invalid_grant", "invalid or expired code")
		return
	}
	if f.Get("redirect_uri") != c.redirect {
		fail(http.StatusBadRequest, "invalid_grant", "redirect_uri does not match the authorization request")
		return
	}
	if !pkceMatches(f.Get("code_verifier"), c.challenge) {
		fail(http.StatusBadRequest, "invalid_grant", "code_verifier does not match the code challenge")
		return
	}
	if res := f.Get("resource"); res != "" {
		allowed := s.mcpResourceAllowed(res)
		if c.resource != "" {
			allowed = strings.TrimRight(res, "/") == strings.TrimRight(c.resource, "/")
		}
		if !allowed {
			fail(http.StatusBadRequest, "invalid_target", "resource does not match the authorization request")
			return
		}
	}
	host := c.redirect
	if u, err := url.Parse(c.redirect); err == nil {
		host = u.Hostname()
	}
	exp := s.now().Add(ssoTokenTTL)
	token, err := s.st.CreateToken(r.Context(), c.actorID, "mcp login ("+host+")", &exp)
	if err != nil {
		s.log.Error("mint mcp token", "err", err)
		s.mcpOAuth.observe("token", "error")
		writeJSON(w, http.StatusInternalServerError, model.OAuthError{Error: "server_error"})
		return
	}
	s.mcpOAuth.observe("token", "ok")
	writeJSON(w, http.StatusOK, model.OAuthToken{
		AccessToken: token, TokenType: "Bearer", ExpiresIn: int64(ssoTokenTTL.Seconds()),
	})
}
