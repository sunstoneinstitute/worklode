package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSPAShellAndAssets(t *testing.T) {
	h := newTwoHostServer(t, withWebApp(fstest.MapFS{
		"index.html":           {Data: []byte("<div id=root></div>")},
		"assets/app-abc123.js": {Data: []byte("console.log(1)")},
	}))
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "worklode.test"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := get("/projects")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" ||
		!strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("shell: %d %v", rec.Code, rec.Header())
	}
	rec = get("/assets/app-abc123.js")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if rec := get("/assets/missing.js"); rec.Code != 404 {
		t.Fatalf("missing asset = %d, want 404", rec.Code)
	}
}

// TestLegacyAssetsUnaffected checks the legacy host's own /assets/ (the
// templ cockpit's stylesheet) still answers through the cockpit's
// assetHandler, unchanged by the SPA's asset route on the public host.
func TestLegacyAssetsUnaffected(t *testing.T) {
	h := newTwoHostServer(t, withWebApp(fstest.MapFS{
		"index.html": {Data: []byte("<div id=root></div>")},
	}))
	req := httptest.NewRequest(http.MethodGet, "/assets/app.css", nil)
	req.Host = "worklold.test"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("legacy asset: got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("legacy asset content-type = %q, want text/css", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=3600" {
		t.Fatalf("legacy asset Cache-Control = %q, want public, max-age=3600", cc)
	}
}
