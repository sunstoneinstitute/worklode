//go:build e2e

// webapp_test.go drives the two-host web app over public surfaces only: the
// SPA shell on the public host (WL-REQ-1304), the redirect of legacy paths
// to the legacy host (WL-REQ-1305), the templ cockpit kept there
// (WL-REQ-1303), cookie-less /api/v1 reads and the write checks on a
// non-bearer write (WL-REQ-1307, WL-REQ-1770), and the OpenAPI document's
// security schemes (WL-REQ-1309).
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
	"github.com/sunstoneinstitute/worklode/internal/store"
)

const (
	webPublicHost = "worklode.e2e.test"
	webLegacyHost = "worklold.e2e.test"
)

func TestWebAppTwoHosts(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		PublicURL:      "https://" + webPublicHost,
		LegacyURL:      "https://" + webLegacyHost,
		WebOpen:        true,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// The CLI client sends the listener's host, which neither named host
	// matches, and it has no way to set Host itself. A second listener on the
	// same handler stands in for the public host's DNS name.
	cliSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = webPublicHost
		handler.ServeHTTP(w, r)
	}))
	defer cliSrv.Close()
	admin := cli.NewClient(cli.Config{ServerURL: cliSrv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "web", Name: "Web Project", Key: "WEB",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	do := func(method, host, path, body string, hdr map[string]string) (*http.Response, string) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, srv.URL+path, rd)
		if err != nil {
			t.Fatalf("build %s %s: %v", method, path, err)
		}
		req.Host = host
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s%s: %v", method, host, path, err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read %s %s%s: %v", method, host, path, err)
		}
		return resp, string(b)
	}
	wantStatus := func(resp *http.Response, body string, want int) {
		t.Helper()
		if resp.StatusCode != want {
			t.Fatalf("%s %s%s = %d, want %d (body %s)", resp.Request.Method,
				resp.Request.Host, resp.Request.URL.RequestURI(), resp.StatusCode, want, body)
		}
	}

	// 1. The SPA shell on the public host.
	resp, body := do(http.MethodGet, webPublicHost, "/projects", "", nil)
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

	// 2. A legacy path on the public host redirects, query intact.
	resp, body = do(http.MethodGet, webPublicHost, "/work?q=1", "", nil)
	wantStatus(resp, body, http.StatusFound)
	if loc := resp.Header.Get("Location"); loc != "https://"+webLegacyHost+"/work?q=1" {
		t.Errorf("/work?q=1 Location = %q", loc)
	}

	// 3. The legacy host serves the templ cockpit and no /api/v1.
	resp, body = do(http.MethodGet, webLegacyHost, "/work", "", nil)
	wantStatus(resp, body, http.StatusOK)
	if !strings.Contains(body, "<html") {
		t.Errorf("legacy /work is not an HTML page: %.200s", body)
	}
	resp, body = do(http.MethodGet, webLegacyHost, "/api/v1/projects", "", nil)
	wantStatus(resp, body, http.StatusNotFound)

	// 4. /api/v1 on the public host: an open read, then a write without and
	// with the session write checks, then with a bearer.
	resp, body = do(http.MethodGet, webPublicHost, "/api/v1/projects", "", nil)
	wantStatus(resp, body, http.StatusOK)
	var list model.ProjectListResponse
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode projects: %v (body %s)", err, body)
	}
	if len(list.Projects) != 1 || list.Projects[0].ID != "web" {
		t.Errorf("projects = %+v, want the one project \"web\"", list.Projects)
	}

	task := `{"project":"web","title":"From the web app","priority":"medium","kind":"feature"}`
	resp, body = do(http.MethodPost, webPublicHost, "/api/v1/tasks", task, nil)
	wantStatus(resp, body, http.StatusForbidden)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("refused write Content-Type = %q, want JSON", ct)
	}
	resp, body = do(http.MethodPost, webPublicHost, "/api/v1/tasks", task, map[string]string{
		"Sec-Fetch-Site":   "same-origin",
		"X-Requested-With": "lode-web",
		"Content-Type":     "application/json",
	})
	wantStatus(resp, body, http.StatusCreated)
	resp, body = do(http.MethodPost, webPublicHost, "/api/v1/tasks", task, map[string]string{
		"Authorization": "Bearer " + bootstrapToken,
	})
	wantStatus(resp, body, http.StatusCreated)

	// 5. /assets/ belongs to the SPA on the public host and to the templ
	// cockpit on the legacy host.
	resp, body = do(http.MethodGet, webPublicHost, "/assets/nope.js", "", nil)
	wantStatus(resp, body, http.StatusNotFound)
	resp, body = do(http.MethodGet, webLegacyHost, "/assets/app.css", "", nil)
	wantStatus(resp, body, http.StatusOK)

	// 6. The OpenAPI document declares both ways in.
	resp, body = do(http.MethodGet, webPublicHost, "/api/v1/openapi.json", "", nil)
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
