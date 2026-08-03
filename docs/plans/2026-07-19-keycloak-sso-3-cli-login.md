---
status: superseded
implements: docs/specs/001-keycloak-sso.md
requires:
  - 2026-07-19-keycloak-sso-1-server-core.md
isReplacedBy:
  ".":
    - 2026-07-20-provider-neutral-cli-login-design.md
---
# Keycloak SSO — Plan 3: CLI `wl login` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `wl login`: an auth-code + PKCE flow against Keycloak with a localhost redirect listener, exchanging the resulting ID token at the worklode server for a 30-day `wl_` token, which is written to `~/.config/worklode/config.toml`.

**Architecture:** A testable core `cli.RunLogin(ctx, LoginOptions)` does the whole flow: discover issuer/client from the server, run the PKCE auth-code flow via a localhost callback listener (ports 8000 → 18000), redeem the code directly at Keycloak, then POST the ID token to the worklode server. Browser-open and the HTTP client are injectable so tests drive the callback without a real browser or Keycloak. A thin `wl login` cobra command resolves the server URL, calls `RunLogin`, persists the token via a new `cli.SaveConfig`, and prints the actor id + expiry.

**Tech Stack:** Go 1.25 stdlib (`net`, `net/http`, `os/exec`), `golang.org/x/oauth2` (PKCE), `internal/oidc`, `github.com/spf13/cobra`.

---

## File Structure

- `internal/cli/client.go` (modify) — add `SaveConfig(Config) error` (a writer to match the existing `LoadConfig` reader).
- `internal/cli/client_test.go` (modify) — test `SaveConfig` round-trips through `parseConfig`.
- `internal/cli/login.go` (create) — `LoginOptions`, `LoginResult`, `RunLogin`, plus helpers (`fetchOIDCConfig`, `exchangeWTToken`, `listenLocal`, `callbackHandler`, `openBrowser`, `randState`).
- `internal/cli/login_test.go` (create) — full flow against a stub worklode server + `oidctest` issuer, with an injected browser-open that drives the callback.
- `internal/cmd/login.go` (create) — the `wl login` command.

---

## Task 1: `cli.SaveConfig`

**Files:**
- Modify: `internal/cli/client.go`
- Test: `internal/cli/client_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/client_test.go` (black-box `package cli_test` — symbols are `cli.`-prefixed; `strings` and the `cli` import are already present in this file):

```go
func TestSaveConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("WL_SERVER", "")
	t.Setenv("WL_TOKEN", "")

	want := cli.Config{ServerURL: "https://wl.example.com", Token: "wl_" + strings.Repeat("ab", 20)}
	if err := cli.SaveConfig(want); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// LoadConfig reads it back (env overrides cleared above so only the file matters).
	got, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.ServerURL != want.ServerURL || got.Token != want.Token {
		t.Fatalf("round-trip = %+v, want %+v", got, want)
	}
}
```

> `SaveConfig`/`LoadConfig` resolve the path via `os.UserHomeDir()`, which honors `$HOME` on macOS/Linux — `t.Setenv("HOME", dir)` isolates the test. On Windows `os.UserHomeDir()` reads `%USERPROFILE%`; this repo's CI runs on Linux (`.github/workflows/ci.yml`), so `HOME` is correct there.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/cli/ -run TestSaveConfigRoundTrip`
Expected: FAIL — `SaveConfig` undefined.

- [ ] **Step 3: Implement `SaveConfig`**

Add to `internal/cli/client.go` (after `parseConfig`):

```go
// SaveConfig writes cfg to ~/.config/worklode/config.toml in the same minimal format
// LoadConfig reads, creating the directory (0700) and file (0600) if needed. It
// rewrites the whole file with just the server and token keys — the only two
// keys the format defines — so any hand-added comments are not preserved.
func SaveConfig(cfg Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "server = %q\n", cfg.ServerURL)
	fmt.Fprintf(&b, "token = %q\n", cfg.Token)
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/cli/ -run TestSaveConfigRoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/client.go internal/cli/client_test.go
git commit -m "feat(cli): SaveConfig writes config.toml"
```

---

## Task 2: `RunLogin` and its helpers

**Files:**
- Create: `internal/cli/login.go`

- [ ] **Step 1: Write the login flow**

Create `internal/cli/login.go`:

```go
// login.go implements `wl login`: an auth-code + PKCE flow against Keycloak
// with a localhost redirect listener, exchanging the resulting ID token at the
// worklode server for a wl_ token. RunLogin is the testable core — the
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

	"github.com/sunstoneinstitute/worklode/internal/oidc"
)

// LoginOptions configures RunLogin. Only Server is required; the rest default.
type LoginOptions struct {
	Server      string             // worklode base URL
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
	srv := &http.Server{Handler: callbackHandler(state, codeCh, errCh)}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())

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

// fetchOIDCConfig asks the worklode server for the issuer and client id.
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
		return oidcDiscovery{}, errors.New("this worklode server does not have SSO enabled")
	}
	if resp.StatusCode != http.StatusOK {
		return oidcDiscovery{}, fmt.Errorf("fetch oidc config: server returned %d", resp.StatusCode)
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
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
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
func callbackHandler(state string, codeCh chan<- string, errCh chan<- error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code") == "" && q.Get("error") == "" {
			http.NotFound(w, r)
			return
		}
		if e := q.Get("error"); e != "" {
			http.Error(w, "authorization error: "+e, http.StatusBadRequest)
			errCh <- fmt.Errorf("authorization error: %s", e)
			return
		}
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			errCh <- errors.New("state mismatch on callback")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintln(w, "<!doctype html><title>wl login</title><p>Login complete. You can close this tab and return to the terminal.</p>")
		codeCh <- q.Get("code")
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
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/cli/`
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/cli/login.go
git commit -m "feat(cli): RunLogin auth-code+PKCE flow with localhost callback"
```

---

## Task 3: `RunLogin` flow test

**Files:**
- Create: `internal/cli/login_test.go`

- [ ] **Step 1: Write the test**

Create `internal/cli/login_test.go`:

```go
package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/oidc/oidctest"
)

func TestRunLogin(t *testing.T) {
	// Fake Keycloak: its /token endpoint returns an ID token for "heidi".
	iss := oidctest.NewIssuer(t)
	iss.TokenClaims = map[string]any{
		"preferred_username": "heidi", "name": "Heidi Example",
		"aud": iss.ClientID, "groups": []string{"user"},
	}

	// Stub worklode server: config discovery + token exchange.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/oidc/config", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": iss.URL(), "client_id": iss.ClientID})
	})
	mux.HandleFunc("POST /auth/oidc/token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["id_token"] == "" {
			http.Error(w, `{"error":"id_token is required"}`, http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"token":      "wl_" + strings.Repeat("ab", 20),
			"actor_id":   "heidi",
			"expires_at": "2026-08-18T00:00:00Z",
		})
	})
	wt := httptest.NewServer(mux)
	defer wt.Close()

	// Instead of opening a browser, drive the callback directly: parse the
	// redirect_uri and state out of the authorize URL and GET the callback.
	openBrowser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		redir := u.Query().Get("redirect_uri")
		state := u.Query().Get("state")
		go func() {
			resp, err := http.Get(redir + "?code=fake-code&state=" + url.QueryEscape(state))
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}

	res, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server:      wt.URL,
		OpenBrowser: openBrowser,
		Ports:       []int{0}, // ephemeral port, avoids collisions with a real 8000
	})
	if err != nil {
		t.Fatalf("RunLogin: %v", err)
	}
	if res.ActorID != "heidi" {
		t.Fatalf("actor id = %q, want heidi", res.ActorID)
	}
	if !strings.HasPrefix(res.Token, "wl_") {
		t.Fatalf("token = %q, want wl_ prefix", res.Token)
	}
	if res.ExpiresAt != "2026-08-18T00:00:00Z" {
		t.Fatalf("expires_at = %q", res.ExpiresAt)
	}
}

func TestRunLoginServerWithoutSSO(t *testing.T) {
	// A server whose /auth/oidc/config 404s (SSO disabled) yields a clear error.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/oidc/config", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"oidc not configured"}`, http.StatusNotFound)
	})
	wt := httptest.NewServer(mux)
	defer wt.Close()

	_, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server:      wt.URL,
		OpenBrowser: func(string) error { return nil },
		Ports:       []int{0},
	})
	if err == nil || !strings.Contains(err.Error(), "SSO") {
		t.Fatalf("err = %v, want an SSO-disabled error", err)
	}
}
```

- [ ] **Step 2: Run the test**

Run: `go test ./internal/cli/ -run TestRunLogin -v`
Expected: PASS for both `TestRunLogin` and `TestRunLoginServerWithoutSSO`.

- [ ] **Step 3: Commit**

```bash
git add internal/cli/login_test.go
git commit -m "test(cli): RunLogin full flow against stub server + fake issuer"
```

---

## Task 4: The `wl login` command

**Files:**
- Create: `internal/cmd/login.go`

- [ ] **Step 1: Write the command**

Create `internal/cmd/login.go`:

```go
package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func newLoginCmd() *cobra.Command {
	var server string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate via SSO and store a worklode token",
		Long: "Log in through the org Keycloak (auth-code + PKCE, browser + localhost\n" +
			"callback) and store the resulting 30-day token in ~/.config/worklode/config.toml.\n" +
			"Re-run after it expires — there are no refresh tokens.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := cli.LoadConfig()
			if err != nil {
				return err
			}
			if server != "" {
				cfg.ServerURL = server
			}
			if cfg.ServerURL == "" {
				return errors.New(`server URL not set: pass --server, set WL_SERVER, or add server = "https://..." to ~/.config/worklode/config.toml`)
			}

			res, err := cli.RunLogin(cmd.Context(), cli.LoginOptions{Server: cfg.ServerURL})
			if err != nil {
				return err
			}
			if err := cli.SaveConfig(cli.Config{ServerURL: cfg.ServerURL, Token: res.Token}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "logged in as %s (token expires %s)\n", res.ActorID, res.ExpiresAt)
			return nil
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "worklode server URL (overrides WL_SERVER / config file)")
	return cmd
}

func init() {
	rootCmd.AddCommand(newLoginCmd())
}
```

- [ ] **Step 2: Verify it builds and appears in help**

Run: `go build ./... && go run ./cmd/wl login --help`
Expected: build succeeds; help text for `wl login` prints, listing the `--server` flag.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/login.go
git commit -m "feat(cli): wl login command"
```

---

## Task 5: Full verification

- [ ] **Step 1: Whole suite + vet + build**

Run: `go test ./... && go vet ./... && go build ./...`
Expected: ok / no output, exit 0.

- [ ] **Step 2: (Optional) end-to-end sanity against a running server**

If a Keycloak realm and a server with `WL_OIDC_ISSUER`/`WL_OIDC_CLIENT_ID`/`WL_SESSION_SECRET`/`WL_PUBLIC_URL` set are available, run `wl login --server <url>` and confirm a token is written to `~/.config/worklode/config.toml` and `wl board` then works. Not required for the plan to be complete (covered by the stubbed test).

---

## Self-Review Notes

- **Spec coverage:** localhost callback listener on 8000 with 18000 fallback — Task 2 (`listenLocal`); open browser to the Keycloak authorize URL (auth-code + PKCE) — Task 2 (`RunLogin` + `openBrowser`); redeem the code directly at Keycloak's token endpoint — Task 2 (`oauthCfg.Exchange`); POST to `/auth/oidc/token` — Task 2 (`exchangeWTToken`); write the token to `~/.config/worklode/config.toml` and print actor id + expiry — Tasks 1 & 4; server URL resolution via `--server`/`WL_SERVER`/config — Task 4; CLI callback tested against a stubbed Keycloak (httptest) — Task 3.
- **Reuse from Plan 1:** the `internal/oidc` package (`oidc.New`, `OAuth2Config`), the `/auth/oidc/config` and `/auth/oidc/token` endpoints, the `oidctest` fake issuer, and the existing `ClientError` type from `client.go`.
- **Type consistency:** `LoginOptions`/`LoginResult`/`RunLogin`, the `oidcDiscovery` shape, and the token-response fields (`token`/`actor_id`/`expires_at`) match the JSON the Plan 1 endpoints emit.
- **Out of scope (per design):** no refresh tokens (re-run `wl login`), no device flow, no logout.
