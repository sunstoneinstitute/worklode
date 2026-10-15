//go:build e2e

// webapp_test.go drives the one-host web app over public surfaces only: a
// spaRoutes path is the SPA shell (WL-REQ-1304), every other cockpit path is
// templ (WL-REQ-1303), one login session covers both (WL-REQ-1308), /api/v1
// reads and the write checks on a non-bearer write (WL-REQ-1307,
// WL-REQ-1770), and the OpenAPI document's security schemes (WL-REQ-1309).
package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/oidc/oidctest"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestWebAppOneHost(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	iss := oidctest.NewIssuer(t)
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		OIDCIssuer:     iss.URL(),
		OIDCClientID:   iss.ClientID,
		PublicURL:      "http://localhost:8080",
		SessionSecret:  "e2e-session-secret",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "web", Name: "Web Project", Key: "WEB",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	do := func(method, path, body string, hdr map[string]string) (*http.Response, string) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, srv.URL+path, rd)
		if err != nil {
			t.Fatalf("build %s %s: %v", method, path, err)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read %s %s: %v", method, path, err)
		}
		return resp, string(b)
	}
	wantStatus := func(resp *http.Response, body string, want int) {
		t.Helper()
		if resp.StatusCode != want {
			t.Fatalf("%s %s = %d, want %d (body %s)", resp.Request.Method,
				resp.Request.URL.RequestURI(), resp.StatusCode, want, body)
		}
	}

	// 1. Signed out, the SPA path and a templ page both send the browser to
	// the one login.
	for _, path := range []string{"/projects", "/work"} {
		resp, body := do(http.MethodGet, path, "", nil)
		wantStatus(resp, body, http.StatusFound)
		if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/auth/login") {
			t.Errorf("signed-out %s Location = %q, want /auth/login", path, loc)
		}
	}

	session := webSession(t, srv.URL, iss, "alice")
	cookie := map[string]string{"Cookie": "wl_session=" + session}

	// 2. /projects is the SPA shell.
	resp, body := do(http.MethodGet, "/projects", "", cookie)
	wantStatus(resp, body, http.StatusOK)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("/projects Content-Type = %q, want text/html", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("/projects Cache-Control = %q, want no-cache", cc)
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("/projects carries no Content-Security-Policy")
	}
	if strings.Contains(body, `<nav aria-label="Primary"`) {
		t.Error("/projects rendered the templ cockpit, want the SPA shell")
	}

	// 3. /work is still the templ cockpit, on the same session.
	resp, body = do(http.MethodGet, "/work", "", cookie)
	wantStatus(resp, body, http.StatusOK)
	if !strings.Contains(body, `<nav aria-label="Primary"`) {
		t.Errorf("/work is not the templ cockpit: %.200s", body)
	}

	// 4. /api/v1: a session read, then a session write without and with the
	// write checks, then a bearer write.
	resp, body = do(http.MethodGet, "/api/v1/projects", "", cookie)
	wantStatus(resp, body, http.StatusOK)
	var list model.ProjectListResponse
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode projects: %v (body %s)", err, body)
	}
	if len(list.Projects) != 1 || list.Projects[0].ID != "web" {
		t.Errorf("projects = %+v, want the one project \"web\"", list.Projects)
	}

	task := `{"project":"web","title":"From the web app","priority":"medium","kind":"feature"}`
	resp, body = do(http.MethodPost, "/api/v1/tasks", task, cookie)
	wantStatus(resp, body, http.StatusForbidden)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("refused write Content-Type = %q, want JSON", ct)
	}
	resp, body = do(http.MethodPost, "/api/v1/tasks", task, map[string]string{
		"Cookie":           "wl_session=" + session,
		"Sec-Fetch-Site":   "same-origin",
		"X-Requested-With": "lode-web",
		"Content-Type":     "application/json",
	})
	wantStatus(resp, body, http.StatusCreated)
	resp, body = do(http.MethodPost, "/api/v1/tasks", task, map[string]string{
		"Authorization": "Bearer " + bootstrapToken,
	})
	wantStatus(resp, body, http.StatusCreated)

	// 5. /static/ belongs to the SPA, /assets/ to the templ cockpit.
	resp, body = do(http.MethodGet, "/static/nope.js", "", nil)
	wantStatus(resp, body, http.StatusNotFound)
	resp, body = do(http.MethodGet, "/assets/app.css", "", nil)
	wantStatus(resp, body, http.StatusOK)

	// 6. The OpenAPI document declares both ways in.
	resp, body = do(http.MethodGet, "/api/v1/openapi.json", "", nil)
	wantStatus(resp, body, http.StatusOK)
	var doc struct {
		Components struct {
			SecuritySchemes map[string]json.RawMessage `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode openapi.json: %v", err)
	}
	for _, name := range []string{"bearerToken", "sessionCookie"} {
		if _, ok := doc.Components.SecuritySchemes[name]; !ok {
			t.Errorf("openapi.json securitySchemes lacks %q", name)
		}
	}
}
