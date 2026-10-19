package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubAPI answers GET /api/v1/projects with one project when the caller's
// bearer token is "wl_test", and 401 otherwise.
var stubAPI = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer wl_test" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"projects":[{"id":"WL","name":"Worklode"}]}`))
})

func post(t *testing.T, h http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestInitializeThenToolsListIsStateless(t *testing.T) {
	h := New(Config{API: stubAPI})
	rr := post(t, h, "wl_test", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("initialize = %d: %s", rr.Code, rr.Body)
	}
	if id := rr.Header().Get("Mcp-Session-Id"); id != "" {
		t.Fatalf("initialize set Mcp-Session-Id %q on a stateless server", id)
	}
	if !strings.Contains(rr.Body.String(), `"tools"`) {
		t.Fatalf("initialize does not advertise tools: %s", rr.Body)
	}

	rr = post(t, h, "wl_test", `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("tools/list = %d: %s", rr.Code, rr.Body)
	}
	if id := rr.Header().Get("Mcp-Session-Id"); id != "" {
		t.Fatalf("tools/list set Mcp-Session-Id %q", id)
	}
	if !strings.Contains(rr.Body.String(), `"tools":[]`) {
		t.Fatalf("tools/list body = %s, want an empty tool list", rr.Body)
	}
}

func TestGetIsMethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer wl_test")
	req.Header.Set("Accept", "text/event-stream")
	rr := httptest.NewRecorder()
	New(Config{API: stubAPI}).ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d, want 405", rr.Code)
	}
}

func TestNoBearerIsUnauthorized(t *testing.T) {
	rr := post(t, New(Config{API: stubAPI}), "", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer = %d, want 401", rr.Code)
	}
}

func TestClientForCallsAPIInProcessWithCallersToken(t *testing.T) {
	s := &server{cfg: Config{API: stubAPI, PublicURL: "https://lode.example"}}

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	if _, err := s.clientFor(req); err != errNoBearer {
		t.Fatalf("clientFor without bearer: err = %v, want errNoBearer", err)
	}

	req.Header.Set("Authorization", "Bearer wl_test")
	c, err := s.clientFor(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := c.ListProjects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 1 || got.Projects[0].ID != "WL" {
		t.Fatalf("projects = %+v", got.Projects)
	}
}
