package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/work-tracker/internal/githubauth"
	"github.com/sunstoneinstitute/work-tracker/internal/store"
)

func TestCLICodeStoreMintRedeem(t *testing.T) {
	now := time.Unix(1000, 0)
	s := newCLICodeStore(func() time.Time { return now })

	code, err := s.mint("github:42", "clistate")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if len(code) < 32 {
		t.Fatalf("code too short: %q", code)
	}

	actor, ok := s.redeem(code, "clistate")
	if !ok || actor != "github:42" {
		t.Fatalf("redeem = %q,%v; want github:42,true", actor, ok)
	}
	// Single use: second redeem fails.
	if _, ok := s.redeem(code, "clistate"); ok {
		t.Fatal("second redeem should fail")
	}
}

func TestCLICodeStoreWrongStateAndExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	s := newCLICodeStore(func() time.Time { return now })
	code, _ := s.mint("a", "right")

	if _, ok := s.redeem(code, "wrong"); ok {
		t.Fatal("wrong state should not redeem")
	}
	// The wrong-state attempt must NOT have consumed the code: a correct redeem
	// still succeeds.
	if actor, ok := s.redeem(code, "right"); !ok || actor != "a" {
		t.Fatalf("code should survive a wrong-state attempt: got %q,%v", actor, ok)
	}

	// Expiry: a fresh code no longer redeems once the clock passes its TTL.
	code2, _ := s.mint("a", "right")
	now = now.Add(cliCodeTTL + time.Second)
	if _, ok := s.redeem(code2, "right"); ok {
		t.Fatal("expired code should not redeem")
	}
}

func TestFinishLoginCLIBranch(t *testing.T) {
	s := &server{cfg: Config{SessionSecret: "sek"}, cliCodes: newCLICodeStore(func() time.Time { return time.Unix(1000, 0) })}

	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=x&state=y", nil)
	req.AddCookie(&http.Cookie{
		Name:  cliCookieName,
		Value: signCLIIntent("sek", cliIntent{Redirect: "http://localhost:5555/", State: "clistate", Exp: time.Unix(1000, 0).Add(cliCodeTTL).Unix()}),
	})
	rr := httptest.NewRecorder()

	s.finishLogin(rr, req, "github:42", "/")

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d; want 302", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "http://localhost:5555/?code=") || !strings.Contains(loc, "state=clistate") {
		t.Fatalf("redirect = %q; want loopback with code+state", loc)
	}
	// No browser session cookie was set on the CLI branch.
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			t.Fatal("CLI branch must not set a session cookie")
		}
	}
	// The code embedded in the redirect redeems to the actor.
	u, _ := url.Parse(loc)
	if actor, ok := s.cliCodes.redeem(u.Query().Get("code"), "clistate"); !ok || actor != "github:42" {
		t.Fatalf("minted code did not redeem: %q,%v", actor, ok)
	}
}

func TestCLILoginValidatesLoopback(t *testing.T) {
	s := &server{cfg: Config{SessionSecret: "sek"}, gh: &githubauth.Client{}, cliCodes: newCLICodeStore(func() time.Time { return time.Unix(1000, 0) })}

	bad := []string{
		"", "https://evil.com/", "http://evil.com/", "http://localhost/", "ftp://localhost:1/",
		"http://localhost.evil.com:5555/",          // subdomain confusion
		"http://evil.com@localhost.evil.com:5555/", // userinfo-host confusion
	}
	for _, ru := range bad {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/auth/cli/login?state=x&redirect_uri="+url.QueryEscape(ru), nil)
		s.cliLogin(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("redirect_uri %q: status %d; want 400", ru, rr.Code)
		}
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/cli/login?state=x&redirect_uri="+url.QueryEscape("http://localhost:5555/"), nil)
	s.cliLogin(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("good redirect_uri: status %d; want 302", rr.Code)
	}
	// Intent cookie is set, and we are redirected into the web login (single
	// provider -> /auth/github/login).
	var hasIntent bool
	for _, c := range rr.Result().Cookies() {
		if c.Name == cliCookieName && c.Value != "" {
			hasIntent = true
		}
	}
	if !hasIntent {
		t.Fatal("intent cookie not set")
	}
	if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "/auth/github/login") {
		t.Fatalf("redirect = %q; want /auth/github/login", loc)
	}
}

// newStoreT opens a migrated store for white-box tests (package api can import
// store the same way the black-box harness does).
func newStoreT(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "wt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(store.MigrationsDirForTests()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestCLITokenRejectsUnknownCode(t *testing.T) {
	st := newStoreT(t)
	s := &server{st: st, log: slog.Default(), cliCodes: newCLICodeStore(st.Now)}
	req := httptest.NewRequest(http.MethodPost, "/auth/cli/token", strings.NewReader(`{"code":"nope","state":"s"}`))
	rr := httptest.NewRecorder()
	s.cliToken(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rr.Code)
	}
}

func TestCLITokenHappyPath(t *testing.T) {
	st := newStoreT(t)
	if err := st.CreateActor(context.Background(), "github:7", "human", "Bob", false); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	s := &server{st: st, log: slog.Default(), cliCodes: newCLICodeStore(st.Now)}
	code, err := s.cliCodes.mint("github:7", "clistate")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/auth/cli/token",
		strings.NewReader(`{"code":"`+code+`","state":"clistate"}`))
	rr := httptest.NewRecorder()
	s.cliToken(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s; want 201", rr.Code, rr.Body.String())
	}
	var m map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(m["token"]) < 10 || m["actor_id"] != "github:7" {
		t.Fatalf("bad token response: %v", m)
	}
}

func TestWellKnownLogin404WhenNoProvider(t *testing.T) {
	s := &server{} // no oidc, no gh
	req := httptest.NewRequest(http.MethodGet, "/.well-known/wt-login", nil)
	rr := httptest.NewRecorder()
	s.wellKnownLogin(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rr.Code)
	}
}

func TestWellKnownLoginReportsProviders(t *testing.T) {
	s := &server{gh: &githubauth.Client{}, cfg: Config{PublicURL: "https://wt.example.com"}}
	req := httptest.NewRequest(http.MethodGet, "/.well-known/wt-login", nil)
	rr := httptest.NewRecorder()
	s.wellKnownLogin(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rr.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m["authorize_url"] != "https://wt.example.com/auth/cli/login" || m["token_url"] != "https://wt.example.com/auth/cli/token" {
		t.Fatalf("urls wrong: %v", m)
	}
	provs, _ := m["providers"].([]any)
	if len(provs) != 1 || provs[0] != "github" {
		t.Fatalf("providers = %v; want [github]", m["providers"])
	}
}
