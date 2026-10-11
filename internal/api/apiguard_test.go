package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
)

func TestAPIAcceptsSessionCookie(t *testing.T) {
	_, h, iss := newOIDCServer(t, api.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
	req.AddCookie(loginSession(t, h, iss, "alice"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"id":"alice"`) {
		t.Fatalf("whoami with session: %d %s", rec.Code, rec.Body)
	}
	// No credential at all: JSON 401, no Location header.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil))
	if rec.Code != 401 || rec.Header().Get("Location") != "" ||
		rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("anonymous: %d %v", rec.Code, rec.Header())
	}
}

// TestAPIOpenSubjectMatchesWebPosture checks D3: on a WebOpen instance with no
// OIDC, a non-bearer /api/v1 request gets exactly the permissions the open
// subject gets on the web — no more. With OIDC configured, WebOpen is
// ignored and the same anonymous request gets 401.
func TestAPIOpenSubjectMatchesWebPosture(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	h, _, err := api.NewServer(st, api.Config{WebOpen: true})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	// permProjectRead is granted to RoleUser — the open subject's role.
	if rr := doReq(t, h, "GET", "/api/v1/projects", "", nil); rr.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/projects anonymous on WebOpen = %d, want 200: %s", rr.Code, rr.Body)
	}
	// permReconcile is admin-only — the open subject never carries RoleAdmin.
	if rr := doReq(t, h, "GET", "/api/v1/repos/doctor", "", nil); rr.Code != http.StatusForbidden {
		t.Fatalf("GET /api/v1/repos/doctor anonymous on WebOpen = %d, want 403: %s", rr.Code, rr.Body)
	}

	// WebOpen is ignored once OIDC is configured: same anonymous request, 401.
	_, h2, _ := newOIDCServer(t, api.Config{WebOpen: true})
	if rr := doReq(t, h2, "GET", "/api/v1/projects", "", nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/v1/projects anonymous on WebOpen+OIDC = %d, want 401: %s", rr.Code, rr.Body)
	}
}
