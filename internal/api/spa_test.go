package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestSPAShellAndAssets checks a spaRoutes path answers index.html, /static/
// serves the build output immutable with a 404 for a missing file
// (WL-REQ-1304), and /assets/ stays the templ cockpit's.
func TestSPAShellAndAssets(t *testing.T) {
	h, _, err := NewServer(store.OpenTestStore(t), Config{WebOpen: true, WebAppForTest: fstest.MapFS{
		"index.html":           {Data: []byte("<div id=root></div>")},
		"static/app-abc123.js": {Data: []byte("console.log(1)")},
	}})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	rec := get("/projects")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" ||
		!strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("shell: %d %v", rec.Code, rec.Header())
	}
	rec = get("/static/app-abc123.js")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if rec := get("/static/missing.js"); rec.Code != 404 {
		t.Fatalf("missing asset = %d, want 404", rec.Code)
	}
	rec = get("/assets/app.css")
	if ct := rec.Header().Get("Content-Type"); rec.Code != 200 || !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("cockpit asset: %d %q", rec.Code, ct)
	}
}
