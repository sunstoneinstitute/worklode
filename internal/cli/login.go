// login.go implements `wt login`: an auth-code + PKCE flow against Keycloak
// with a localhost redirect listener, exchanging the resulting ID token at the
// work-tracker server for a wt_ token. RunLogin is the testable core — the
// browser-open step and the HTTP client are injectable so tests can drive the
// callback without a real browser or Keycloak.
package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/sunstoneinstitute/work-tracker/internal/oidc"
)

// LoginOptions configures RunLogin. Only Server is required; the rest default.
type LoginOptions struct {
	Server      string             // work-tracker base URL
	HTTPClient  *http.Client       // defaults to a 30s-timeout client
	OpenBrowser func(string) error // defaults to openBrowser; tests inject a driver
	Ports       []int              // localhost callback ports; defaults to {8000, 18000}
}

// LoginResult is the outcome of a successful login.
type LoginResult struct {
	ActorID   string
	ExpiresAt string
	Token     string
}

// loginScopes are requested from Keycloak. The client-roles-as-groups mapper
// adds the groups claim without an extra scope.
var loginScopes = []string{"openid", "profile"}

// RunLogin performs the whole login flow and returns the minted token.
func RunLogin(ctx context.Context, opts LoginOptions) (*LoginResult, error) {
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if opts.OpenBrowser == nil {
		opts.OpenBrowser = openBrowser
	}
	if len(opts.Ports) == 0 {
		opts.Ports = []int{8000, 18000}
	}

	disc, err := fetchOIDCConfig(ctx, opts.HTTPClient, opts.Server)
	if err != nil {
		return nil, err
	}
	verifier, err := oidc.New(ctx, disc.Issuer, disc.ClientID)
	if err != nil {
		return nil, fmt.Errorf("connect to issuer %s: %w", disc.Issuer, err)
	}

	ln, port, err := listenLocal(opts.Ports)
	if err != nil {
		return nil, err
	}
	defer ln.Close()

	redirectURL := fmt.Sprintf("http://localhost:%d/", port)
	oauthCfg := verifier.OAuth2Config(redirectURL, loginScopes)

	state := randState()
	pkce := oauth2.GenerateVerifier()
	authURL := oauthCfg.AuthCodeURL(state, oauth2.S256ChallengeOption(pkce))

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{
		Handler:           callbackHandler(state, codeCh, errCh),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go srv.Serve(ln)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := opts.OpenBrowser(authURL); err != nil {
		return nil, fmt.Errorf("open browser: %w", err)
	}

	var code string
	select {
	case code = <-codeCh:
	case err = <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	tok, err := oauthCfg.Exchange(ctx, code, oauth2.VerifierOption(pkce))
	if err != nil {
		return nil, fmt.Errorf("exchange code at issuer: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return nil, errors.New("issuer response contained no id_token")
	}

	return exchangeWTToken(ctx, opts.HTTPClient, opts.Server, rawID)
}

// oidcDiscovery is the shape of GET /auth/oidc/config.
type oidcDiscovery struct {
	Issuer   string `json:"issuer"`
	ClientID string `json:"client_id"`
}

// fetchOIDCConfig asks the work-tracker server for the issuer and client id.
func fetchOIDCConfig(ctx context.Context, client *http.Client, server string) (oidcDiscovery, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(server, "/")+"/auth/oidc/config", nil)
	if err != nil {
		return oidcDiscovery{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return oidcDiscovery{}, fmt.Errorf("fetch oidc config: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return oidcDiscovery{}, errors.New("this work-tracker server does not have SSO enabled")
	}
	if resp.StatusCode != http.StatusOK {
		return oidcDiscovery{}, &ClientError{Status: resp.StatusCode, Msg: "fetch oidc config"}
	}
	var d oidcDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return oidcDiscovery{}, fmt.Errorf("decode oidc config: %w", err)
	}
	if d.Issuer == "" || d.ClientID == "" {
		return oidcDiscovery{}, errors.New("oidc config missing issuer or client_id")
	}
	return d, nil
}

// exchangeWTToken POSTs the ID token to /auth/oidc/token and returns the result.
func exchangeWTToken(ctx context.Context, client *http.Client, server, idToken string) (*LoginResult, error) {
	body, _ := json.Marshal(map[string]string{"id_token": idToken})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(server, "/")+"/auth/oidc/token", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchange token: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(data))
		var e map[string]string
		if json.Unmarshal(data, &e) == nil && e["error"] != "" {
			msg = e["error"]
		}
		return nil, &ClientError{Status: resp.StatusCode, Msg: msg}
	}
	var r struct {
		Token     string `json:"token"`
		ActorID   string `json:"actor_id"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	return &LoginResult{Token: r.Token, ActorID: r.ActorID, ExpiresAt: r.ExpiresAt}, nil
}

// listenLocal binds the first available port from ports on 127.0.0.1. A port of
// 0 binds an ephemeral port (used by tests). It returns the listener and the
// actual bound port.
func listenLocal(ports []int) (net.Listener, int, error) {
	var lastErr error
	for _, p := range ports {
		// Bind to localhost (not the 127.0.0.1 literal) so the listener answers
		// on whatever localhost resolves to for the browser — including ::1 on
		// IPv6-first hosts — and to match Keycloak's registered
		// http://localhost:<port> redirect URIs.
		ln, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", p))
		if err == nil {
			return ln, ln.Addr().(*net.TCPAddr).Port, nil
		}
		lastErr = err
	}
	return nil, 0, fmt.Errorf("no local callback port available (tried %v): %w", ports, lastErr)
}

// callbackHandler serves the localhost redirect. It validates state, forwards
// the code on codeCh (or an error on errCh), and shows a close-the-tab page.
// Requests without a code or error (e.g. /favicon.ico) are 404'd silently.
//
// Both channel sends are non-blocking: the buffer depth of 1 guarantees the
// first legitimate hit lands, and extra concurrent hits (browser prefetch, a
// port scanner, a double-click) are dropped rather than blocking a handler
// goroutine on a full channel — which would otherwise wedge srv.Shutdown.
func callbackHandler(state string, codeCh chan<- string, errCh chan<- error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code") == "" && q.Get("error") == "" {
			http.NotFound(w, r)
			return
		}
		if e := q.Get("error"); e != "" {
			http.Error(w, "authorization error: "+e, http.StatusBadRequest)
			select {
			case errCh <- fmt.Errorf("authorization error: %s", e):
			default:
			}
			return
		}
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			select {
			case errCh <- errors.New("state mismatch on callback"):
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintln(w, "<!doctype html><title>wt login</title><p>Login complete. You can close this tab and return to the terminal.</p>")
		select {
		case codeCh <- q.Get("code"):
		default:
		}
	})
}

// randState returns 16 random bytes as hex, for the CSRF state value.
func randState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// openBrowser opens url in the platform default browser.
func openBrowser(url string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{url}
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		name, args = "xdg-open", []string{url}
	}
	return exec.Command(name, args...).Start()
}
