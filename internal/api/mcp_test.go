package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMCPRoute checks the /mcp row (WL-REQ-1822): a bearer token reaches the
// MCP handler, and a request without one answers 401 even on a WebOpen
// instance, where apiGuard alone would hand it the open subject.
func TestMCPRoute(t *testing.T) {
	t.Parallel()
	_, h, token := newTestServer(t)
	call := func(method, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/mcp",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	if rr := call(http.MethodPost, token); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"tools"`) {
		t.Fatalf("tools/list with bearer = %d: %s", rr.Code, rr.Body)
	}
	if rr := call(http.MethodGet, token); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d, want 405", rr.Code)
	}
	if rr := call(http.MethodPost, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer = %d, want 401", rr.Code)
	}
	if rr := call(http.MethodPost, "wl_deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unknown bearer = %d, want 401", rr.Code)
	}
}
