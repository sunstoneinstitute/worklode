package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/oidc"
)

// TestServedBaseTwoHost checks servedBase matches the request's host against
// either configured base (WL-REQ-1308), stripping a port, and refuses a host
// that matches neither.
func TestServedBaseTwoHost(t *testing.T) {
	t.Parallel()
	s := &server{cfg: Config{PublicURL: "https://worklode.test", LegacyURL: "https://worklold.test"}}
	for _, tc := range []struct{ host, want string }{
		{"worklode.test", "https://worklode.test"},
		{"worklold.test", "https://worklold.test"},
		{"worklode.test:8443", "https://worklode.test"},
		{"evil.example", ""},
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = tc.host
		if got := s.servedBase(req); got != tc.want {
			t.Errorf("servedBase(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}

// TestServedBaseSingleHost checks the single-host carve-out: with no
// LegacyURL, servedBase returns PublicURL regardless of the request's Host
// (an httptest address or a bare IP behind an ingress never equals
// hostOf(PublicURL) literally) — today's behaviour exactly.
func TestServedBaseSingleHost(t *testing.T) {
	t.Parallel()
	s := &server{cfg: Config{PublicURL: "http://localhost:8080"}}
	for _, host := range []string{"localhost:8080", "example.com", "10.0.0.5"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		if got, want := s.servedBase(req), "http://localhost:8080"; got != want {
			t.Errorf("servedBase(%q) = %q, want %q", host, got, want)
		}
	}
}

// TestCallbackURLPerHost checks callbackURL builds the redirect URI from the
// host the request arrived on, not always PublicURL.
func TestCallbackURLPerHost(t *testing.T) {
	t.Parallel()
	s := &server{cfg: Config{PublicURL: "https://worklode.test", LegacyURL: "https://worklold.test"}}
	req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	req.Host = "worklold.test"
	if got, want := s.callbackURL(req), "https://worklold.test/auth/callback"; got != want {
		t.Errorf("callbackURL = %q, want %q", got, want)
	}
}

// TestAuthLoginUnknownHost404 checks authLogin itself 404s when the request's
// host matches neither configured base, before touching any session state
// (the host check runs with only cfg and oidc set on s).
func TestAuthLoginUnknownHost404(t *testing.T) {
	t.Parallel()
	s := &server{cfg: Config{PublicURL: "https://worklode.test", LegacyURL: "https://worklold.test"}, oidc: &oidc.Verifier{}}
	req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	req.Host = "evil.example"
	rec := httptest.NewRecorder()
	s.authLogin(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("authLogin on unknown host: status = %d, want 404", rec.Code)
	}
}

// TestAuthCallbackUnknownHost404 is authLogin's counterpart for the callback
// endpoint.
func TestAuthCallbackUnknownHost404(t *testing.T) {
	t.Parallel()
	s := &server{cfg: Config{PublicURL: "https://worklode.test", LegacyURL: "https://worklold.test"}, oidc: &oidc.Verifier{}}
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=x&state=y", nil)
	req.Host = "evil.example"
	rec := httptest.NewRecorder()
	s.authCallback(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("authCallback on unknown host: status = %d, want 404", rec.Code)
	}
}
