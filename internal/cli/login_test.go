package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// discoveryHandler serves the discovery document, pointing both URLs back at
// the stub server itself.
func discoveryHandler(providers ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		doc := map[string]any{
			"authorize_url": "http://" + r.Host + "/auth/cli/login",
			"token_url":     "http://" + r.Host + "/auth/cli/token",
		}
		if len(providers) > 0 {
			doc["providers"] = providers
		}
		json.NewEncoder(w).Encode(doc)
	}
}

// callbackBrowser stands in for the browser: it parses redirect_uri and state
// out of the authorize URL and hits the loopback with code, as the server
// would after a web login. A non-empty state overrides the one the CLI sent,
// for the mismatch case.
func callbackBrowser(code, state string) func(string) error {
	return func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		if state == "" {
			state = q.Get("state")
		}
		go http.Get(q.Get("redirect_uri") + "?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(state))
		return nil
	}
}

func TestRunLoginServerMediated(t *testing.T) {
	// Stub worklode server: discovery + token exchange.
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/lode-login", discoveryHandler("github"))
	mux.HandleFunc("/auth/cli/token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != "THECODE" || body["state"] == "" {
			http.Error(w, `{"error":"bad code"}`, http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"token": "wl_minted", "actor_id": "github:7", "expires_at": "2026-08-19T00:00:00Z",
		})
	})
	wt := httptest.NewServer(mux)
	defer wt.Close()

	res, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server: wt.URL, OpenBrowser: callbackBrowser("THECODE", ""),
	})
	if err != nil {
		t.Fatalf("RunLogin: %v", err)
	}
	if res.Token != "wl_minted" || res.ActorID != "github:7" {
		t.Fatalf("result = %+v; want wl_minted/github:7", res)
	}
}

func TestRunLoginTokenExchangeSurfacesServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/lode-login", discoveryHandler())
	mux.HandleFunc("/auth/cli/token", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid or expired code"}`, http.StatusBadRequest)
	})
	wt := httptest.NewServer(mux)
	defer wt.Close()

	_, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server: wt.URL, OpenBrowser: callbackBrowser("STALE", ""),
	})
	if err == nil || !strings.Contains(err.Error(), "invalid or expired code") {
		t.Fatalf("err = %v; want it to carry the server's error message", err)
	}
	var ce *cli.ClientError
	if !errors.As(err, &ce) || ce.Status != http.StatusBadRequest {
		t.Fatalf("err = %v (%T); want *cli.ClientError with status 400", err, err)
	}
}

// A discovery failure that isn't a 404 (misconfigured OIDC, issuer
// unreachable) is exactly where the server has the most useful thing to say,
// so fetchLoginConfig must surface it the same way exchangeCLIToken does.
func TestRunLoginDiscoverySurfacesServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/lode-login", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"oidc not configured"}`, http.StatusInternalServerError)
	})
	wt := httptest.NewServer(mux)
	defer wt.Close()

	_, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server: wt.URL, OpenBrowser: func(string) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "oidc not configured") {
		t.Fatalf("err = %v; want it to carry the server's error message", err)
	}
	var ce *cli.ClientError
	if !errors.As(err, &ce) || ce.Status != http.StatusInternalServerError {
		t.Fatalf("err = %v (%T); want *cli.ClientError with status 500", err, err)
	}
}

func TestRunLoginNoInteractiveLogin(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/lode-login", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no interactive login configured", http.StatusNotFound)
	})
	wt := httptest.NewServer(mux)
	defer wt.Close()

	_, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server: wt.URL, OpenBrowser: func(string) error { return nil },
	})
	if err == nil {
		t.Fatal("expected error when server has no interactive login")
	}
}

// manualStub serves the discovery document and a token endpoint that redeems
// wantCode, the pair a manual-mode login needs (spec 001 §8.7).
func manualStub(t *testing.T, wantCode string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/lode-login", discoveryHandler("keycloak"))
	mux.HandleFunc("/auth/cli/token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != wantCode || body["state"] == "" {
			http.Error(w, `{"error":"invalid or expired code"}`, http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"token": "wl_manual", "actor_id": "stig", "expires_at": "2026-09-13T00:00:00Z",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// In manual mode the CLI prints an authorize URL carrying mode=manual and its
// own state, then exchanges the code the user pastes back. No listener is bound
// and the browser is never launched.
func TestRunLoginManualPastedCode(t *testing.T) {
	wt := manualStub(t, "THECODE")
	var out strings.Builder

	res, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server:      wt.URL,
		NoBrowser:   true,
		Stdin:       strings.NewReader("THECODE\n"),
		Stdout:      &out,
		OpenBrowser: func(string) error { t.Error("browser launched in manual mode"); return nil },
	})
	if err != nil {
		t.Fatalf("RunLogin: %v", err)
	}
	if res.Token != "wl_manual" || res.ActorID != "stig" {
		t.Fatalf("result = %+v; want wl_manual/stig", res)
	}

	printed := out.String()
	i := strings.Index(printed, wt.URL)
	if i < 0 {
		t.Fatalf("printed output does not contain the authorize URL:\n%s", printed)
	}
	u, err := url.Parse(strings.Fields(printed[i:])[0])
	if err != nil {
		t.Fatalf("parse printed URL: %v", err)
	}
	if got := u.Query().Get("mode"); got != "manual" {
		t.Errorf("printed URL mode = %q; want manual", got)
	}
	if u.Query().Get("state") == "" {
		t.Error("printed URL carries no state")
	}
	if u.Query().Get("redirect_uri") != "" {
		t.Error("manual mode must not advertise a loopback redirect_uri")
	}
}

// A pasted code is trimmed: a copy button plus a terminal paste routinely
// delivers trailing whitespace or a newline.
func TestRunLoginManualTrimsPastedCode(t *testing.T) {
	wt := manualStub(t, "THECODE")
	_, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server: wt.URL, NoBrowser: true,
		Stdin:  strings.NewReader("  THECODE  \n"),
		Stdout: io.Discard,
	})
	if err != nil {
		t.Fatalf("RunLogin with padded paste: %v", err)
	}
}

// An OpenBrowser reporting ErrNoBrowser drops into manual mode rather than
// failing the login: this is the xdg-open-is-missing / no-DISPLAY case.
func TestRunLoginFallsBackToManualWhenNoBrowser(t *testing.T) {
	wt := manualStub(t, "THECODE")
	var out strings.Builder

	res, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server:      wt.URL,
		Stdin:       strings.NewReader("THECODE\n"),
		Stdout:      &out,
		OpenBrowser: func(string) error { return cli.ErrNoBrowser },
	})
	if err != nil {
		t.Fatalf("RunLogin: %v", err)
	}
	if res.Token != "wl_manual" {
		t.Fatalf("token = %q; want wl_manual", res.Token)
	}
	if !strings.Contains(out.String(), "mode=manual") {
		t.Errorf("fallback did not print a manual authorize URL:\n%s", out.String())
	}
}

// Any other browser-launch failure stays fatal: a browser may well have opened,
// and a silent fallback would leave two logins racing.
func TestRunLoginBrowserErrorStaysFatal(t *testing.T) {
	wt := manualStub(t, "THECODE")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := cli.RunLogin(ctx, cli.LoginOptions{
		Server:      wt.URL,
		Stdin:       strings.NewReader("THECODE\n"),
		Stdout:      io.Discard,
		OpenBrowser: func(string) error { return errors.New("permission denied") },
	})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v; want the browser launch failure", err)
	}
}

// Nobody can paste in CI. A closed stdin must fail with guidance instead of
// blocking until the login times out.
func TestRunLoginManualClosedStdin(t *testing.T) {
	wt := manualStub(t, "THECODE")
	_, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server: wt.URL, NoBrowser: true,
		Stdin:  strings.NewReader(""),
		Stdout: io.Discard,
	})
	if err == nil {
		t.Fatal("expected an error when stdin has no code to read")
	}
	if !strings.Contains(err.Error(), "no code entered") {
		t.Fatalf("err = %v; want it to say no code was entered", err)
	}
}

func TestRunLoginStateMismatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/lode-login", discoveryHandler())
	wt := httptest.NewServer(mux)
	defer wt.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := cli.RunLogin(ctx, cli.LoginOptions{
		Server: wt.URL, OpenBrowser: callbackBrowser("X", "WRONG"),
	})
	if err == nil {
		t.Fatal("expected state-mismatch error")
	}
}
