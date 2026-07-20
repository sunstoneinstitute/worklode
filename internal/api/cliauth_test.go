package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/work-tracker/internal/githubauth"
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
	// Still unused after a failed state check; now let it expire.
	now = now.Add(cliCodeTTL + time.Second)
	if _, ok := s.redeem(code, "right"); ok {
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

	bad := []string{"", "https://evil.com/", "http://evil.com/", "http://localhost/", "ftp://localhost:1/"}
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
