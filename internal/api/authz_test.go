package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
)

// This file exercises the authorization layer through the HTTP surface, the
// way a caller meets it. The policy's own table-level properties (default
// deny, the public allow-list, the boot-time route checks) are pinned in
// authz_internal_test.go, which can see the unexported policy.

// webLogin drives the full Keycloak round trip against the issuer's already
// configured claims and returns the session cookie, so a test exercises the
// web surface as a real logged-in person rather than by forging a cookie.
func webLogin(t *testing.T, h http.Handler, username string) string {
	t.Helper()
	login := doReq(t, h, "GET", "/auth/login?next=/", "", nil)
	oauthCookie := cookieValue(login, "wl_oauth")
	loc, _ := url.Parse(login.Header().Get("Location"))

	req := httptest.NewRequest("GET", "/auth/callback?code=fake-code&state="+
		url.QueryEscape(loc.Query().Get("state")), nil)
	req.AddCookie(&http.Cookie{Name: "wl_oauth", Value: oauthCookie})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("login callback for %s: status = %d, body %s", username, rr.Code, rr.Body.String())
	}
	session := cookieValue(rr, "wl_session")
	if session == "" {
		t.Fatalf("login callback for %s set no session cookie", username)
	}
	return session
}

// withSession performs a request carrying a session cookie.
func withSession(t *testing.T, h http.Handler, method, path, session string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	var req *http.Request
	if rd != nil {
		req = httptest.NewRequest(method, path, rd)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if session != "" {
		req.AddCookie(&http.Cookie{Name: "wl_session", Value: session})
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestAPIDeniesWithoutPermission checks the enforcement point on /api/v1: a
// perfectly valid non-admin token is refused on every admin-gated route, with
// the message the policy derives, and is allowed on the ordinary ones. This
// is the same guarantee requireAdmin used to give per route, now given by the
// permission each route declares.
func TestAPIDeniesWithoutPermission(t *testing.T) {
	st, h, adminToken := newTestServer(t)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "worker", "agent", "Worker", false); err != nil {
		t.Fatalf("create worker: %v", err)
	}
	workerToken, err := st.CreateToken(ctx, "worker", "worker token", nil)
	if err != nil {
		t.Fatalf("create worker token: %v", err)
	}
	createProject(t, st, "proj")

	// Admin-gated: the permission is granted to RoleAdmin alone.
	for _, tt := range []struct{ method, path string }{
		{"POST", "/api/v1/projects"},
		{"PATCH", "/api/v1/projects/proj"},
		{"POST", "/api/v1/projects/proj/repos"},
		{"POST", "/api/v1/actors"},
		{"POST", "/api/v1/actors/worker/tokens"},
		{"DELETE", "/api/v1/tokens"},
		{"POST", "/api/v1/skills/sync"},
		{"POST", "/api/v1/inbox/import"},
	} {
		t.Run("denied "+tt.method+" "+tt.path, func(t *testing.T) {
			rr := doReq(t, h, tt.method, tt.path, workerToken, map[string]any{})
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body %s", rr.Code, rr.Body.String())
			}
			if got := decodeMap(t, rr)["error"]; got != "admin required" {
				t.Errorf("error = %v, want admin required", got)
			}
		})
	}

	// Not admin-gated: the same non-admin token works.
	for _, tt := range []struct{ method, path string }{
		{"GET", "/api/v1/tasks"},
		{"GET", "/api/v1/projects"},
		{"GET", "/api/v1/board"},
		{"GET", "/api/v1/inbox"},
		{"GET", "/api/v1/projects/proj/deliverables"},
	} {
		t.Run("allowed "+tt.method+" "+tt.path, func(t *testing.T) {
			rr := doReq(t, h, tt.method, tt.path, workerToken, nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
			}
		})
	}

	// And the admin token still reaches the admin routes.
	rr := doReq(t, h, "POST", "/api/v1/projects", adminToken,
		map[string]any{"id": "p2", "name": "P2", "key": "P2"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("admin create project status = %d, body %s", rr.Code, rr.Body.String())
	}
}

// TestAPIUnauthenticatedIs401 checks the boundary between the two failure
// modes: no credential is 401 (authenticate and try again), a credential
// without the permission is 403 (do not bother). Conflating them is how
// clients end up retrying a login they do not need.
func TestAPIUnauthenticatedIs401(t *testing.T) {
	_, h, _ := newTestServer(t)
	for _, path := range []string{"/api/v1/tasks", "/api/v1/projects", "/api/v1/board"} {
		rr := doReq(t, h, "GET", path, "", nil)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a token = %d, want 401", path, rr.Code)
		}
	}
}

// TestWebGuardAllowsLoggedInUser checks the web enforcement point end to end
// on a session-gated deployment: a logged-in non-admin reads the cockpit and
// submits the creation form, and the write is attributed to their actor —
// which is the thing webGuard resolving the session to an actor buys.
func TestWebGuardAllowsLoggedInUser(t *testing.T) {
	st, h, iss := newOIDCServer(t)
	ctx := context.Background()
	createProject(t, st, "proj")

	iss.TokenClaims = map[string]any{
		"preferred_username": "dana", "name": "Dana",
		"aud": iss.ClientID, "groups": []string{"user"},
	}
	session := webLogin(t, h, "dana")

	if rr := withSession(t, h, "GET", "/projects/proj", session, ""); rr.Code != http.StatusOK {
		t.Fatalf("cockpit with session = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	if rr := withSession(t, h, "GET", "/projects/proj/tasks/new", session, ""); rr.Code != http.StatusOK {
		t.Fatalf("new-task form with session = %d, want 200", rr.Code)
	}

	form := url.Values{"title": {"Signed in"}, "priority": {"high"}, "kind": {"feature"}}
	rr := withSession(t, h, "POST", "/projects/proj/tasks", session, form.Encode())
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("form submit with session = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	task, err := st.GetTask(ctx, "WL-1")
	if err != nil {
		t.Fatalf("get created task: %v", err)
	}
	if task.CreatedBy != "dana" {
		t.Errorf("created_by = %q, want dana — the session's actor", task.CreatedBy)
	}
}

// TestWebGuardRedirectsAnonymous checks that a session-gated deployment sends
// an unauthenticated visitor to login rather than serving or 403ing — the
// behaviour the former webAuth had, preserved through the policy path.
func TestWebGuardRedirectsAnonymous(t *testing.T) {
	st, h, _ := newOIDCServer(t)
	createProject(t, st, "proj")

	for _, path := range []string{"/", "/projects/proj", "/projects/proj/tasks/new"} {
		rr := withSession(t, h, "GET", path, "", "")
		if rr.Code != http.StatusFound {
			t.Errorf("GET %s anonymously = %d, want 302", path, rr.Code)
		}
		if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "/auth/login?next=") {
			t.Errorf("GET %s Location = %q, want the login target", path, loc)
		}
	}
}

// TestWebGuardRejectsSessionForDeletedActor pins a case the old signature-only
// check could not see: a cookie that verifies but names an actor that no
// longer exists is anonymous, not authorized. Without the actor lookup, a
// revoked person kept a working session until the cookie expired.
func TestWebGuardRejectsSessionForDeletedActor(t *testing.T) {
	st, h, iss := newOIDCServer(t)
	createProject(t, st, "proj")

	iss.TokenClaims = map[string]any{
		"preferred_username": "ghost", "name": "Ghost",
		"aud": iss.ClientID, "groups": []string{"user"},
	}
	session := webLogin(t, h, "ghost")
	if rr := withSession(t, h, "GET", "/projects/proj", session, ""); rr.Code != http.StatusOK {
		t.Fatalf("sanity: logged-in read = %d, want 200", rr.Code)
	}

	if _, err := st.DBForTests().ExecContext(context.Background(),
		`DELETE FROM actors WHERE id = 'ghost'`); err != nil {
		t.Fatalf("delete actor: %v", err)
	}

	rr := withSession(t, h, "GET", "/projects/proj", session, "")
	if rr.Code != http.StatusFound {
		t.Fatalf("read with a session naming a deleted actor = %d, want 302 to login", rr.Code)
	}
}

// TestOpenDeploymentStaysOpen checks the deliberate bypass is intact: with no
// login provider configured the cockpit serves and accepts writes, and the
// write is attributed to nobody rather than to a fabricated actor.
func TestOpenDeploymentStaysOpen(t *testing.T) {
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	if rr := doReq(t, h, "GET", "/projects/proj", "", nil); rr.Code != http.StatusOK {
		t.Fatalf("cockpit on an open deployment = %d, want 200", rr.Code)
	}
	form := url.Values{"title": {"Anonymous"}, "priority": {"low"}, "kind": {"chore"}}
	rr := withSession(t, h, "POST", "/projects/proj/tasks", "", form.Encode())
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("form submit on an open deployment = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	task, err := st.GetTask(context.Background(), "WL-1")
	if err != nil {
		t.Fatalf("get created task: %v", err)
	}
	if task.CreatedBy != "" {
		t.Errorf("created_by = %q, want empty — an open deployment has no actor to attribute to", task.CreatedBy)
	}
}

// TestAuthzDecisionsCounted checks the decision counter is wired, so a denial
// spike is visible without reading logs.
func TestAuthzDecisionsCounted(t *testing.T) {
	st := newTestStore(t)
	main, admin, err := api.NewServer(st, api.Config{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ctx := context.Background()
	if err := st.CreateActor(ctx, "worker", "agent", "Worker", false); err != nil {
		t.Fatalf("create worker: %v", err)
	}
	workerToken, tokErr := st.CreateToken(ctx, "worker", "worker token", nil)
	if tokErr != nil {
		t.Fatalf("create worker token: %v", tokErr)
	}

	doReq(t, main, "GET", "/api/v1/tasks", workerToken, nil)                // allow
	doReq(t, main, "POST", "/api/v1/actors", workerToken, map[string]any{}) // deny

	body := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	for _, want := range []string{
		`worklode_authz_decisions_total{outcome="allow",permission="task.read"} 1`,
		`worklode_authz_decisions_total{outcome="deny",permission="actor.admin"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q", want)
		}
	}
}
