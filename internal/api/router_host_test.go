package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// newTwoHostServer boots a server with LODE_LEGACY_URL set, so the route
// table splits across the public and legacy hosts (WL-REQ-1303).
func newTwoHostServer(t *testing.T) http.Handler {
	t.Helper()
	st := store.OpenTestStore(t)
	h, _, err := NewServer(st, Config{
		PublicURL: "https://worklode.test",
		LegacyURL: "https://worklold.test",
		WebOpen:   true,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return h
}

// TestHostRouting checks the split itself: the templ cockpit answers only on
// the legacy host, the JSON API only on the public host, and a route
// registered on both (login) answers on either.
//
// /api/v1/whoami on the public host returns 401, not 200: an unauthenticated
// /api/v1 request being handed the open subject is WL-1115's change (a
// session cookie accepted on /api/v1), not this task's.
func TestHostRouting(t *testing.T) {
	h := newTwoHostServer(t)
	for _, tc := range []struct {
		host, path string
		want       int
	}{
		{"worklold.test", "/projects", http.StatusOK},                // templ page on the legacy host
		{"worklode.test", "/api/v1/whoami", http.StatusUnauthorized}, // API on the public host; no bearer token
		{"worklold.test", "/api/v1/whoami", http.StatusNotFound},     // the API route does not exist on the legacy host
		{"worklode.test", "/auth/login", http.StatusNotFound},        // no OIDC configured: handler 404s, but the route exists on both
		{"worklold.test", "/auth/login", http.StatusNotFound},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Host = tc.host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s%s = %d, want %d", tc.host, tc.path, rec.Code, tc.want)
		}
	}
}

// TestHostRoutingSingleHost checks that without LODE_LEGACY_URL, every route
// still registers host-less and answers regardless of the Host header.
func TestHostRoutingSingleHost(t *testing.T) {
	st := store.OpenTestStore(t)
	h, _, err := NewServer(st, Config{WebOpen: true})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	for _, host := range []string{"worklode.test", "anything.example"} {
		for _, tc := range []struct {
			path string
			want int
		}{
			{"/projects", http.StatusOK},
			{"/api/v1/whoami", http.StatusUnauthorized},
		} {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Host = host
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("host %q %s = %d, want %d", host, tc.path, rec.Code, tc.want)
			}
		}
	}
}

// TestHostRoutingNoUnusedGuards registers the table across the two
// host-qualified routers registerRoutes builds and checks every entry is
// claimed: one guard per route, used once (public- or legacy-only) or twice
// (the shared routes), never left unused (WL-REQ-1303).
func TestHostRoutingNoUnusedGuards(t *testing.T) {
	t.Parallel()
	s := &server{cfg: Config{PublicURL: "https://worklode.test", LegacyURL: "https://worklold.test"}}
	mux := http.NewServeMux()
	r := newRouter(s, mux)
	pub := r.on(hostOf(s.cfg.PublicURL))
	legacy := r.on(hostOf(s.cfg.LegacyURL))

	s.registerCockpit(legacy)
	s.registerAPI(pub, prometheus.NewRegistry())
	s.registerShared(pub, legacy)

	if unused := r.unusedGuards(); len(unused) != 0 {
		t.Errorf("unusedGuards = %v, want none", unused)
	}
}
