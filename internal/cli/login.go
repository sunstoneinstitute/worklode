// login.go implements `lode login`: a provider-neutral, server-mediated auth flow.
// The CLI discovers the server's login URLs, opens a browser to the server's
// /auth/cli/login with an ephemeral-port loopback redirect, waits for the
// server to redirect a one-time code back to the loopback, and exchanges that
// code for a wl_ token. The CLI speaks no provider protocol.
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
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type LoginOptions struct {
	Server      string
	HTTPClient  *http.Client
	OpenBrowser func(string) error
}

type LoginResult struct {
	ActorID   string
	ExpiresAt string
	Token     string
}

type wlLoginDiscovery struct {
	AuthorizeURL string   `json:"authorize_url"`
	TokenURL     string   `json:"token_url"`
	Providers    []string `json:"providers"`
}

func RunLogin(ctx context.Context, opts LoginOptions) (*LoginResult, error) {
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if opts.OpenBrowser == nil {
		opts.OpenBrowser = openBrowser
	}

	disc, err := fetchLoginConfig(ctx, opts.HTTPClient, opts.Server)
	if err != nil {
		return nil, err
	}

	ln, port, err := listenLocal()
	if err != nil {
		return nil, err
	}
	defer ln.Close()

	state := randState()
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{Handler: callbackHandler(state, codeCh, errCh), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	redirectURL := fmt.Sprintf("http://localhost:%d/", port)
	authURL, err := buildAuthURL(disc.AuthorizeURL, redirectURL, state)
	if err != nil {
		return nil, err
	}
	if len(disc.Providers) > 0 {
		fmt.Printf("Opening browser to sign in (%s)…\n", strings.Join(disc.Providers, ", "))
	}
	if err := opts.OpenBrowser(authURL); err != nil {
		return nil, fmt.Errorf("open browser: %w", err)
	}

	// Bound the wait so an unattended `lode login` whose callback never arrives
	// fails cleanly instead of blocking forever. Derived from the passed ctx so
	// Ctrl-C still cancels immediately.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var code string
	select {
	case code = <-codeCh:
	case err = <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return exchangeCLIToken(ctx, opts.HTTPClient, disc.TokenURL, code, state)
}

// fetchLoginConfig gets the discovery document. A 404 means the server has no
// interactive login configured.
func fetchLoginConfig(ctx context.Context, client *http.Client, server string) (wlLoginDiscovery, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(server, "/")+"/.well-known/lode-login", nil)
	if err != nil {
		return wlLoginDiscovery{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return wlLoginDiscovery{}, fmt.Errorf("fetch login config: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return wlLoginDiscovery{}, errors.New("this worklode server has no interactive login; ask an admin to mint you a token and set LODE_TOKEN")
	}
	if resp.StatusCode != http.StatusOK {
		return wlLoginDiscovery{}, &ClientError{Status: resp.StatusCode, Msg: "fetch login config"}
	}
	var d wlLoginDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return wlLoginDiscovery{}, fmt.Errorf("decode login config: %w", err)
	}
	if d.AuthorizeURL == "" || d.TokenURL == "" {
		return wlLoginDiscovery{}, errors.New("login config missing authorize_url or token_url")
	}
	return d, nil
}

// buildAuthURL adds the loopback redirect_uri and CSRF state to the server's
// authorize URL. It parses the base so an authorize_url that already carries a
// query is preserved rather than clobbered.
func buildAuthURL(authorizeURL, redirectURL, state string) (string, error) {
	u, err := url.Parse(authorizeURL)
	if err != nil {
		return "", fmt.Errorf("parse authorize_url: %w", err)
	}
	q := u.Query()
	q.Set("redirect_uri", redirectURL)
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// exchangeCLIToken posts the one-time code to the server's token endpoint. The
// token exchange is the most failure-prone step (expired/reused code, state
// mismatch), so a non-2xx surfaces the server's own error message.
func exchangeCLIToken(ctx context.Context, client *http.Client, tokenURL, code, state string) (*LoginResult, error) {
	body, _ := json.Marshal(map[string]string{"code": code, "state": state})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
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

// listenLocal binds an ephemeral loopback port. Because the server (not the IdP)
// redirects to this URL, no port pre-registration is needed and any free port
// works — immune to port conflicts.
func listenLocal() (net.Listener, int, error) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, 0, fmt.Errorf("bind loopback callback port: %w", err)
	}
	return ln, ln.Addr().(*net.TCPAddr).Port, nil
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
		fmt.Fprintln(w, "<!doctype html><title>lode login</title><p>Login complete. You can close this tab and return to the terminal.</p>")
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
