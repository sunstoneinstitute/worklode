package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDecideReturn: the decide form's return field is attacker-controlled, so
// only a same-site absolute path may be redirected to. Everything else falls
// back to the queue rather than sending a signed-in reviewer off-site.
func TestDecideReturn(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, want string }{
		{"", "/reviews"},
		{"/docs/WL-SPEC-25", "/docs/WL-SPEC-25"},
		{"/docs/WL-SPEC-25?body=source", "/docs/WL-SPEC-25?body=source"},
		{"//evil.example.com/", "/reviews"},
		{"https://evil.example.com/", "/reviews"},
		{"docs/WL-SPEC-25", "/reviews"},
		{"javascript:alert(1)", "/reviews"},
	} {
		if got := decideReturn(c.in); got != c.want {
			t.Errorf("decideReturn(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestOriginAllowedAcceptsLegacyHost checks originAllowed also accepts an
// Origin matching LegacyURL's host (WL-REQ-1308), not only PublicURL's.
func TestOriginAllowedAcceptsLegacyHost(t *testing.T) {
	t.Parallel()
	s := &server{cfg: Config{PublicURL: "https://worklode.test", LegacyURL: "https://worklold.test"}}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Host = "worklode.test"
	req.Header.Set("Origin", "https://worklold.test")
	if !s.originAllowed(req) {
		t.Error("originAllowed should accept an Origin matching LegacyURL's host")
	}
}

// TestOriginAllowedRejectsUnknownHost checks the fallback stays bounded: an
// Origin matching neither configured base is still refused.
func TestOriginAllowedRejectsUnknownHost(t *testing.T) {
	t.Parallel()
	s := &server{cfg: Config{PublicURL: "https://worklode.test", LegacyURL: "https://worklold.test"}}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Host = "worklode.test"
	req.Header.Set("Origin", "https://evil.example")
	if s.originAllowed(req) {
		t.Error("originAllowed should reject an unrelated Origin")
	}
}
