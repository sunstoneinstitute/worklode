// login.go implements `lode login`: a provider-neutral, server-mediated auth flow.
// The CLI discovers the server's login URLs, opens a browser to the server's
// /auth/cli/login with an ephemeral-port loopback redirect, waits for the
// server to redirect a one-time code back to the loopback, and exchanges that
// code for a wl_ token. The CLI speaks no provider protocol.
//
// Where no browser can be launched — no opener binary, or no display, as over
// SSH — the same flow runs in manual mode (spec 001 §8.7): no listener is bound,
// the URL is printed for the user to open on any machine, and the one-time code
// the server renders there is pasted back here. Only how the code travels
// differs; discovery and the token exchange are identical.
package cli

import (
	"bufio"
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
	"os"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

type LoginOptions struct {
	Server      string
	HTTPClient  *http.Client
	OpenBrowser func(string) error
	// NoBrowser forces manual mode (§8.7) even where a browser could be
	// launched — for a terminal whose browser is on another machine.
	NoBrowser bool
	// Stdin is where manual mode reads the pasted code from; Stdout is where
	// both modes report progress. They default to the process's own.
	Stdin  io.Reader
	Stdout io.Writer
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
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}

	disc, err := fetchLoginConfig(ctx, opts.HTTPClient, opts.Server)
	if err != nil {
		return nil, err
	}

	if opts.NoBrowser {
		return runManualLogin(ctx, opts, disc)
	}
	res, err := runLoopbackLogin(ctx, opts, disc)
	if errors.Is(err, ErrNoBrowser) {
		fmt.Fprintln(opts.Stdout, "No browser can be opened on this machine — signing in by hand instead.")
		return runManualLogin(ctx, opts, disc)
	}
	return res, err
}

// runLoopbackLogin is the flow of §8: bind an ephemeral loopback port, send the
// browser through the server's web login, and wait for the one-time code to come
// back as a redirect. It returns ErrNoBrowser, unwrapped, when there is no
// browser to send.
func runLoopbackLogin(ctx context.Context, opts LoginOptions, disc wlLoginDiscovery) (*LoginResult, error) {
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
	// Announced only once the launch has actually been accepted: the fallback to
	// manual mode is decided right here, and "Opening browser…" followed by
	// "there is no browser" would describe something that never happened.
	if err := opts.OpenBrowser(authURL); err != nil {
		if errors.Is(err, ErrNoBrowser) {
			return nil, err
		}
		return nil, fmt.Errorf("open browser: %w", err)
	}
	if len(disc.Providers) > 0 {
		fmt.Fprintf(opts.Stdout, "Opening browser to sign in (%s)…\n", strings.Join(disc.Providers, ", "))
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

// runManualLogin is the flow of §8.7, for a machine that cannot launch a
// browser or reach its own loopback port from one. It binds no listener: the
// server renders the one-time code on a page and the user pastes it back here.
func runManualLogin(ctx context.Context, opts LoginOptions, disc wlLoginDiscovery) (*LoginResult, error) {
	state := randState()
	authURL, err := buildManualAuthURL(disc.AuthorizeURL, state)
	if err != nil {
		return nil, err
	}

	provider := "sign in"
	if len(disc.Providers) > 0 {
		provider = "sign in (" + strings.Join(disc.Providers, ", ") + ")"
	}
	fmt.Fprintf(opts.Stdout, "\nOpen this URL in a browser — on any machine — to %s:\n\n  %s\n\n", provider, authURL)
	fmt.Fprint(opts.Stdout, "The page will show a one-time code. Paste it here: ")

	code, err := readLine(ctx, opts.Stdin)
	if err != nil {
		return nil, err
	}
	return exchangeCLIToken(ctx, opts.HTTPClient, disc.TokenURL, code, state)
}

// buildManualAuthURL asks the server for manual mode. It passes no
// redirect_uri: there is no listener to redirect to, which is the whole point.
func buildManualAuthURL(authorizeURL, state string) (string, error) {
	u, err := url.Parse(authorizeURL)
	if err != nil {
		return "", fmt.Errorf("parse authorize_url: %w", err)
	}
	q := u.Query()
	q.Set("mode", "manual")
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// readLine reads one trimmed, non-empty line. The read runs on its own
// goroutine so a Ctrl-C at the prompt cancels the login: the caller's context
// carries SIGINT, and a blocking Read would otherwise swallow it. The goroutine
// outlives a cancelled read, which is harmless — the process is on its way out.
func readLine(ctx context.Context, in io.Reader) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		sc := bufio.NewScanner(in)
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); line != "" {
				ch <- result{line: line}
				return
			}
		}
		// Scanner reports a clean EOF as no error at all, which is exactly the
		// non-interactive case: nobody is there to type.
		err := sc.Err()
		if err == nil {
			err = errors.New("no code entered: stdin ended without one")
		}
		ch <- result{err: err}
	}()

	select {
	case r := <-ch:
		return r.line, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
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
	body, _ := json.Marshal(model.CLITokenInput{Code: code, State: state})
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
