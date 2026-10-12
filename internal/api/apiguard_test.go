package api_test

import (
	"maps"
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

// TestSessionWriteChecks checks a session-authorized /api/v1 write is
// refused unless it is same-origin, carries the page header, and — when it
// has a body — names a JSON content type (WL-REQ-1770).
func TestSessionWriteChecks(t *testing.T) {
	_, h, iss := newOIDCServer(t, api.Config{})
	cookie := loginSession(t, h, iss, "alice")
	post := func(hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{}`))
		req.AddCookie(cookie)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	ok := map[string]string{"Sec-Fetch-Site": "same-origin", "X-Requested-With": "lode-web", "Content-Type": "application/json"}
	if rec := post(ok); rec.Code == 403 || rec.Code == 415 {
		t.Fatalf("all checks present: %d %s", rec.Code, rec.Body) // 422 from the empty body is fine here
	}
	for name, drop := range map[string]string{"origin": "Sec-Fetch-Site", "header": "X-Requested-With", "content_type": "Content-Type"} {
		hdr := maps.Clone(ok)
		delete(hdr, drop)
		if drop == "Sec-Fetch-Site" {
			hdr["Sec-Fetch-Site"] = "cross-site"
		}
		rec := post(hdr)
		if rec.Code != 403 && rec.Code != 415 {
			t.Errorf("%s dropped: %d, want a refusal", name, rec.Code)
		}
		if rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%s: refusal is not JSON", name)
		}
	}
}

// TestSessionWriteChecksSkipBearer checks the checks (WL-REQ-1770) apply
// only to a non-bearer subject: a bearer-token write carrying none of the
// three headers is not refused by them.
func TestSessionWriteChecksSkipBearer(t *testing.T) {
	t.Parallel()
	_, h, token := newTestServer(t)
	rec := doReq(t, h, http.MethodPost, "/api/v1/tasks", token, map[string]any{})
	if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnsupportedMediaType {
		t.Fatalf("bearer write without headers: %d %s", rec.Code, rec.Body)
	}
}

// TestSessionWriteChecksBodilessDelete checks a bodiless DELETE passes the
// content-type check (WL-REQ-1770): an HTML form cannot send one, and the
// page-header check already forces a preflight.
func TestSessionWriteChecksBodilessDelete(t *testing.T) {
	t.Parallel()
	_, h, iss := newOIDCServer(t, api.Config{})
	cookie := loginSession(t, h, iss, "alice")
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/does-not-exist", nil)
	req.AddCookie(cookie)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-Requested-With", "lode-web")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnsupportedMediaType {
		t.Fatalf("bodiless DELETE with session: %d %s", rec.Code, rec.Body)
	}
}
