package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
)

// TestMCPRoute checks the /mcp row (WL-REQ-1822): a bearer token reaches the
// MCP handler, and a request without one answers 401 even on a WebOpen
// instance, where apiGuard alone would hand it the open subject.
func TestMCPRoute(t *testing.T) {
	t.Parallel()
	_, h, token := newTestServerWithConfig(t, api.Config{WebOpen: true, PublicURL: "https://lode.example.com"})
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
	want := `Bearer resource_metadata="https://lode.example.com/.well-known/oauth-protected-resource/mcp"`
	for name, tok := range map[string]string{"no": "", "unknown": "wl_deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"} {
		rr := call(http.MethodPost, tok)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s bearer = %d, want 401", name, rr.Code)
		}
		if got := rr.Header().Get("WWW-Authenticate"); got != want {
			t.Fatalf("%s bearer WWW-Authenticate = %q", name, got)
		}
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"S256"`) {
		t.Fatalf("as metadata = %d: %s", rr.Code, rr.Body)
	}
}
