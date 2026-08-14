// session.go implements the web UI's stateless auth cookies, all signed under
// LODE_SESSION_SECRET (there is no server-side session store):
//   - the session cookie: {username, expiry}, ~12h, set after a successful
//     login and checked by webAuth on every gated web request.
//   - the oauth-state cookie: {state, PKCE verifier, next, expiry}, short-lived,
//     set at /auth/login and consumed at /auth/callback.
//
// Both use the same construction: base64url(payload) + "." + base64url(HMAC),
// with a constant-time MAC compare and an expiry embedded in the payload.
package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookieName = "wl_session"
	oauthCookieName   = "wl_oauth"
	cliCookieName     = "wl_cli"
	sessionLifetime   = 12 * time.Hour
	oauthStateMaxAge  = 10 * time.Minute
)

// hmacSHA256 returns HMAC-SHA256(secret, msg).
func hmacSHA256(secret string, msg []byte) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(msg)
	return m.Sum(nil)
}

// signPayload returns base64url(payload) + "." + base64url(HMAC(secret,payload)).
func signPayload(secret string, payload []byte) string {
	mac := hmacSHA256(secret, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac)
}

// verifyPayload checks the MAC (constant time) and returns the raw payload.
func verifyPayload(secret, value string) ([]byte, bool) {
	pb64, mb64, ok := strings.Cut(value, ".")
	if !ok {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(pb64)
	if err != nil {
		return nil, false
	}
	mac, err := base64.RawURLEncoding.DecodeString(mb64)
	if err != nil {
		return nil, false
	}
	if !hmac.Equal(mac, hmacSHA256(secret, payload)) {
		return nil, false
	}
	return payload, true
}

// signSession signs a session cookie value for username expiring at expiry.
// Payload form: "username|unixExpiry".
func signSession(secret, username string, expiry time.Time) string {
	payload := []byte(username + "|" + strconv.FormatInt(expiry.Unix(), 10))
	return signPayload(secret, payload)
}

// verifySession returns the username from a valid, unexpired session cookie.
func verifySession(secret, value string, now time.Time) (string, bool) {
	payload, ok := verifyPayload(secret, value)
	if !ok {
		return "", false
	}
	user, expStr, ok := strings.Cut(string(payload), "|")
	if !ok {
		return "", false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", false
	}
	if now.Unix() >= exp {
		return "", false
	}
	return user, true
}

// oauthState is the payload of the short-lived cookie that carries CSRF state,
// the PKCE verifier, and the post-login redirect target across the redirect to
// Keycloak and back.
type oauthState struct {
	State    string `json:"s"`
	Verifier string `json:"v"`
	Next     string `json:"n"`
	Exp      int64  `json:"e"`
}

// signOAuthState signs an oauth-state cookie value.
func signOAuthState(secret string, st oauthState) string {
	payload, _ := json.Marshal(st)
	return signPayload(secret, payload)
}

// verifyOAuthState returns the oauthState from a valid, unexpired cookie.
func verifyOAuthState(secret, value string, now time.Time) (oauthState, bool) {
	payload, ok := verifyPayload(secret, value)
	if !ok {
		return oauthState{}, false
	}
	var st oauthState
	if err := json.Unmarshal(payload, &st); err != nil {
		return oauthState{}, false
	}
	if now.Unix() >= st.Exp {
		return oauthState{}, false
	}
	return st, true
}

// cliIntent is the payload of the short-lived cookie set at /auth/cli/login. It
// carries the loopback redirect target and CSRF state across the web-login
// redirect chain, so finishLogin knows to hand a one-time code back to the CLI
// rather than set a browser session.
type cliIntent struct {
	Redirect string `json:"r"`
	State    string `json:"s"`
	Exp      int64  `json:"e"`
	// Mode is cliModeLoopback or cliModeManual. Empty means loopback, so a
	// cookie signed before manual mode existed still completes.
	Mode string `json:"m,omitempty"`
}

func signCLIIntent(secret string, ci cliIntent) string {
	payload, _ := json.Marshal(ci)
	return signPayload(secret, payload)
}

func verifyCLIIntent(secret, value string, now time.Time) (cliIntent, bool) {
	payload, ok := verifyPayload(secret, value)
	if !ok {
		return cliIntent{}, false
	}
	var ci cliIntent
	if err := json.Unmarshal(payload, &ci); err != nil {
		return cliIntent{}, false
	}
	if now.Unix() >= ci.Exp {
		return cliIntent{}, false
	}
	return ci, true
}
