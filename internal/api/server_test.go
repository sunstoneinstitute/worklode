package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// newTestStore opens a fresh migrated per-test Postgres store.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	return store.OpenTestStore(t)
}

// newTestServer returns a store, a server handler, and a valid bearer token
// for admin actor "alice".
func newTestServer(t *testing.T) (*store.Store, http.Handler, string) {
	t.Helper()
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "alice", "human", "Alice", true); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	token, err := st.CreateToken(ctx, "alice", "test token", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	// Web tests drive pages anonymously; they are the deliberate open case.
	// The refusal itself is tested through newTestServerWithConfig in
	// authz_test.go, which does not go through this helper.
	h, _, err := api.NewServer(st, api.Config{WebOpen: true})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return st, h, token
}

// newTestServerAdmin returns both the public app handler and the admin handler
// (/healthz, /metrics), for tests that assert on their separation.
func newTestServerAdmin(t *testing.T) (main, admin http.Handler) {
	t.Helper()
	st := newTestStore(t)
	main, admin, err := api.NewServer(st, api.Config{WebOpen: true})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return main, admin
}

// doReq performs a request against the handler. A non-nil body is JSON-encoded.
// An empty token omits the Authorization header.
func doReq(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decodeMap(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
	return m
}

// decodeInto decodes a response body into a caller-supplied typed struct,
// for tests that want field access without map[string]any assertions.
func decodeInto(t *testing.T, rr *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), v); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
}

func TestHealthzNoAuth(t *testing.T) {
	_, admin := newTestServerAdmin(t)
	rr := doReq(t, admin, "GET", "/healthz", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rr.Code)
	}
	if got := decodeMap(t, rr)["status"]; got != "ok" {
		t.Fatalf("healthz status field = %v, want ok", got)
	}
}

// /healthz and /metrics live on the admin handler only; the public handler
// (the one behind the ingress) must not serve them.
func TestHealthzAndMetricsNotOnPublicHandler(t *testing.T) {
	main, _ := newTestServerAdmin(t)
	for _, path := range []string{"/healthz", "/metrics"} {
		rr := doReq(t, main, "GET", path, "", nil)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("GET %s on public handler = %d, want 404", path, rr.Code)
		}
	}
}

func TestAPIRequiresAuth(t *testing.T) {
	_, h, _ := newTestServer(t)
	for name, token := range map[string]string{
		"missing token": "",
		"bad token":     "wl_deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	} {
		t.Run(name, func(t *testing.T) {
			rr := doReq(t, h, "GET", "/api/v1/tasks", token, nil)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rr.Code)
			}
			if got := decodeMap(t, rr)["error"]; got != "unauthorized" {
				t.Fatalf("error = %v, want unauthorized", got)
			}
		})
	}
}

func TestAPIRejectsMalformedAuthHeader(t *testing.T) {
	_, h, token := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/v1/tasks", nil)
	req.Header.Set("Authorization", "Token "+token) // not Bearer
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestBootstrapAdmin(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	tok1 := "wl_" + strings.Repeat("ab", 20)
	if err := st.BootstrapAdmin(ctx, tok1); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	a, err := st.Authenticate(ctx, tok1)
	if err != nil {
		t.Fatalf("authenticate bootstrap token: %v", err)
	}
	if a.ID != "admin" || a.Kind != "service" || !a.Admin {
		t.Fatalf("actor = %+v, want id=admin kind=service admin=true", a)
	}

	// Second call must no-op: actors table is no longer empty.
	tok2 := "wl_" + strings.Repeat("cd", 20)
	if err := st.BootstrapAdmin(ctx, tok2); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if _, err := st.Authenticate(ctx, tok2); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second token authenticate err = %v, want ErrNotFound", err)
	}
	// First token still works.
	if _, err := st.Authenticate(ctx, tok1); err != nil {
		t.Fatalf("first token stopped working: %v", err)
	}
}

func TestBootstrapAdminRejectsMalformedToken(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	for name, tok := range map[string]string{
		"empty":         "",
		"no prefix":     strings.Repeat("ab", 20),
		"too short":     "wl_abcdef",
		"not hex":       "wl_" + strings.Repeat("zz", 20),
		"uppercase hex": "wl_" + strings.Repeat("AB", 20),
	} {
		t.Run(name, func(t *testing.T) {
			if err := st.BootstrapAdmin(ctx, tok); !errors.Is(err, store.ErrInvalidInput) {
				t.Fatalf("BootstrapAdmin(%q) err = %v, want ErrInvalidInput", tok, err)
			}
		})
	}
	// Nothing was created by the rejected attempts.
	if _, err := st.GetActor(ctx, "admin"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("admin actor after rejected bootstraps: err = %v, want ErrNotFound", err)
	}
}

func TestBootstrapAdminNoOpWithExistingActors(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "alice", "human", "Alice", false); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok := "wl_" + strings.Repeat("ef", 20)
	if err := st.BootstrapAdmin(ctx, tok); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if _, err := st.GetActor(ctx, "admin"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("admin actor err = %v, want ErrNotFound", err)
	}
}

func TestOversizedBody413(t *testing.T) {
	_, h, token := newTestServer(t)
	// A syntactically valid JSON body just over the 1 MiB cap.
	body := []byte(`{"title":"` + strings.Repeat("a", 1<<20) + `"}`)
	req := httptest.NewRequest("POST", "/api/v1/tasks", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rr.Code)
	}
	if got := decodeMap(t, rr)["error"]; got != "request body too large" {
		t.Fatalf("error = %v, want request body too large", got)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	main, admin := newTestServerAdmin(t)
	// Generate at least one recorded request on the public handler first (an
	// unauthenticated API call is fine — it still passes through the metrics
	// middleware). Metrics are then read from the admin handler.
	doReq(t, main, "GET", "/api/v1/tasks", "", nil)

	rr := doReq(t, admin, "GET", "/metrics", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "http_requests_total") {
		t.Fatalf("metrics body missing http_requests_total:\n%s", body)
	}
	if !strings.Contains(body, "http_request_duration_seconds") {
		t.Fatalf("metrics body missing http_request_duration_seconds")
	}
}

// TestMetricsEndpointDomainFamilies wires a shared registry through both the
// store and the server, the way serve.go does, and asserts the domain
// families appear on the admin /metrics alongside the HTTP ones.
func TestMetricsEndpointDomainFamilies(t *testing.T) {
	reg := prometheus.NewRegistry()
	st := store.OpenTestStore(t, store.WithMetrics(reg))
	main, admin, err := api.NewServer(st, api.Config{Metrics: reg})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	doReq(t, main, "GET", "/api/v1/tasks", "", nil)

	rr := doReq(t, admin, "GET", "/metrics", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"http_requests_total",
		"worklode_leases_active",
		"go_sql_open_connections",
		"worklode_skill_sync_runs_total",
		"worklode_skill_sync_duration_seconds",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %s", want)
		}
	}
}

// TestNewServerRejectsMalformedPublicURL and TestNewServerRejectsBadTokenEncKey
// re-home from the deleted githubweb_test.go: they cover config validation
// that survives the GitHub App OAuth client (dormant, spec 001 §9.3), not the
// login flow that was removed.
func TestNewServerRejectsMalformedPublicURL(t *testing.T) {
	st := newTestStore(t)
	_, _, err := api.NewServer(st, api.Config{
		GitHubClientID:     "cid",
		GitHubClientSecret: "secret",
		SessionSecret:      "sekret",
		PublicURL:          "not-a-url",
		TokenEncKey:        "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})
	if err == nil {
		t.Fatal("expected error for malformed PublicURL")
	}
}

func TestNewServerRejectsBadTokenEncKey(t *testing.T) {
	st := newTestStore(t)
	_, _, err := api.NewServer(st, api.Config{
		GitHubClientID:     "cid",
		GitHubClientSecret: "secret",
		SessionSecret:      "sekret",
		PublicURL:          "https://wl.test",
		TokenEncKey:        "abcd",
	})
	if err == nil {
		t.Fatal("expected error for bad-length TokenEncKey")
	}
}

type failingCollector struct{ desc *prometheus.Desc }

func (c failingCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }
func (c failingCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.NewInvalidMetric(c.desc, errors.New("collector boom"))
}

// A failing collector must not take the whole scrape down with it.
func TestMetricsEndpointSurvivesCollectorFailure(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(failingCollector{prometheus.NewDesc("worklode_probe", "test", nil, nil)})
	main, admin, err := api.NewServer(newTestStore(t), api.Config{Metrics: reg})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	doReq(t, main, "GET", "/api/v1/tasks", "", nil)

	// Two scrapes: the body is encoded from the snapshot Gather() returned, and
	// the error counter increments after it, so the failure only shows up next scrape.
	doReq(t, admin, "GET", "/metrics", "", nil)
	rr := doReq(t, admin, "GET", "/metrics", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "http_requests_total") {
		t.Fatalf("healthy family dropped:\n%s", body)
	}
	if !strings.Contains(body, `promhttp_metric_handler_errors_total{cause="gathering"} 1`) {
		t.Fatalf("collector failure not surfaced:\n%s", body)
	}
	if strings.Contains(body, "worklode_probe") {
		t.Fatalf("failing collector's family should be absent:\n%s", body)
	}
}
