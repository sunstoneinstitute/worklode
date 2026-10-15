package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestSPAShellAndAssets(t *testing.T) {
	h := newTwoHostServer(t, withWebApp(fstest.MapFS{
		"index.html":           {Data: []byte("<div id=root></div>")},
		"static/app-abc123.js": {Data: []byte("console.log(1)")},
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
	rec = get("/static/app-abc123.js")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if rec := get("/static/missing.js"); rec.Code != 404 {
		t.Fatalf("missing asset = %d, want 404", rec.Code)
	}
}

// TestSPARedirectsToLegacy checks a path spaRoutes doesn't claim 302s to
// LODE_LEGACY_URL, query kept, while a path it does claim still answers 200.
func TestSPARedirectsToLegacy(t *testing.T) {
	h := newTwoHostServer(t, withWebApp(fstest.MapFS{
		"index.html": {Data: []byte("<div id=root></div>")},
	}))
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "worklode.test"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	for path, want := range map[string]string{
		"/work?x=1":    "https://worklold.test/work?x=1",
		"/work":        "https://worklold.test/work",
		"/a%2Fb?q=a+b": "https://worklold.test/a%2Fb?q=a+b",
	} {
		rec := get(path)
		if rec.Code != http.StatusFound {
			t.Fatalf("%s: got %d, want 302", path, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != want {
			t.Fatalf("%s: Location = %q, want %q", path, loc, want)
		}
	}

	if rec := get("/projects"); rec.Code != http.StatusOK {
		t.Fatalf("spa-handled path: got %d, want 200", rec.Code)
	}
}

// TestSPAShellNotFoundWithoutLegacyHost checks spaShell's no-legacy-host
// branch directly: registerSPA never registers it (registerRoutes only
// calls it when LODE_LEGACY_URL splits the hosts), so it is unreachable
// through the mux today.
func TestSPAShellNotFoundWithoutLegacyHost(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := &server{cfg: Config{WebAppForTest: fstest.MapFS{
		"index.html": {Data: []byte("<div id=root></div>")},
	}}}
	s.initMetrics(reg)

	req := httptest.NewRequest(http.MethodGet, "/work", nil)
	rec := httptest.NewRecorder()
	s.spaShell(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("spaShell without legacy host: got %d, want 200", rec.Code)
	}
	if got := testutil.ToFloat64(s.spaRequests.WithLabelValues(spaNotFound)); got != 1 {
		t.Fatalf("spaRequests{outcome=not_found} = %v, want 1", got)
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
