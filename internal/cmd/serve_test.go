package cmd

import (
	"context"
	"maps"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// TestParseClusterEnvMap: only dev and prod are accepted. env_deploys holds
// no other stage, so accepting one would give a server that records
// deployments but never advances a task.
// startTestServer starts one http.Server on a loopback port with handler,
// wired to baseCtx exactly as serve.go wires the real ones.
func startTestServer(t *testing.T, baseCtx context.Context, handler http.Handler) (*http.Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{
		Handler:     handler,
		BaseContext: func(net.Listener) context.Context { return baseCtx },
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return srv, "http://" + ln.Addr().String()
}

// TestShutdownServersWithOpenStream is the SIGTERM case that broke when the
// event log grew a live SSE stream: http.Server.Shutdown waits for handlers
// and never cancels them, so a handler that returns only on request-context
// cancellation would hold shutdown to its deadline and make the command exit
// non-zero. Shutdown must instead complete promptly and return nil.
func TestShutdownServersWithOpenStream(t *testing.T) {
	reqCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()

	streaming := make(chan struct{}) // closed once the handler is in flight
	cancelled := make(chan struct{}) // closed once the handler saw cancellation
	srv, url := startTestServer(t, reqCtx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		http.NewResponseController(w).Flush()
		close(streaming)
		<-r.Context().Done()
		close(cancelled)
	}))
	adminSrv, _ := startTestServer(t, reqCtx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	<-streaming

	const grace = 50 * time.Millisecond
	start := time.Now()
	if err := shutdownServers(cancelRequests, grace, shutdownTimeout, srv, adminSrv); err != nil {
		t.Fatalf("shutdownServers with a stream open: %v, want nil", err)
	}
	if d := time.Since(start); d >= shutdownTimeout {
		t.Fatalf("shutdownServers took %v, want well under the %v budget", d, shutdownTimeout)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("stream handler was never cancelled, so it did not return deliberately")
	}
}

// The admin server's shutdown must not inherit an already-spent budget from
// the public one, and a shutdown with nothing in flight must not wait out the
// grace window.
func TestShutdownServersIdleReturnsImmediately(t *testing.T) {
	reqCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()

	srv, url := startTestServer(t, reqCtx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	adminSrv, adminURL := startTestServer(t, reqCtx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for _, u := range []string{url, adminURL} {
		resp, err := http.Get(u)
		if err != nil {
			t.Fatalf("GET %s: %v", u, err)
		}
		resp.Body.Close()
	}

	const grace = 5 * time.Second
	start := time.Now()
	if err := shutdownServers(cancelRequests, grace, shutdownTimeout, srv, adminSrv); err != nil {
		t.Fatalf("shutdownServers: %v, want nil", err)
	}
	if d := time.Since(start); d >= grace {
		t.Fatalf("idle shutdown took %v, want it not to wait out the %v grace window", d, grace)
	}
}

func TestParseClusterEnvMap(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		want    map[string]string
		wantErr string
	}{
		{name: "empty", in: "", want: nil},
		{
			name: "dev and prod",
			in:   "hzdev=dev, hzprod=prod,admin=prod",
			want: map[string]string{"hzdev": "dev", "hzprod": "prod", "admin": "prod"},
		},
		{name: "entry without =", in: "hzdev", want: map[string]string{}},
		{name: "unknown value", in: "staging-1=staging", wantErr: `cluster "staging-1" maps to "staging"`},
		{name: "empty value", in: "hzdev=", wantErr: `cluster "hzdev" maps to ""`},
		{name: "one bad among good", in: "hzdev=dev,qa=qa", wantErr: `cluster "qa" maps to "qa"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseClusterEnvMap(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("parseClusterEnvMap(%q) = %v, want error containing %q", tc.in, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				if !strings.Contains(err.Error(), "LODE_CLUSTER_ENV_MAP") {
					t.Fatalf("error = %q, want it to name LODE_CLUSTER_ENV_MAP", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseClusterEnvMap(%q): %v", tc.in, err)
			}
			if !maps.Equal(got, tc.want) {
				t.Fatalf("parseClusterEnvMap(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestGraphProjector: unset LODE_GRAPHSERVER_URL disables projection;
// URL-only configures an unauthenticated client; a half-configured auth
// triple (graphserver.FromEnv's contract) fails the boot rather than
// silently disabling. Each case sets all four LODE_GRAPHSERVER_* variables
// explicitly, since t.Setenv restores after the test but a var left set from
// an earlier case would otherwise leak into the next one's meaning.
func TestGraphProjector(t *testing.T) {
	for _, tc := range []struct {
		name     string
		url      string
		tokenURL string
		clientID string
		secret   string
		wantNil  bool
		wantErr  bool
	}{
		{name: "unset disables projection", url: "", tokenURL: "", clientID: "", secret: "", wantNil: true},
		{name: "url only", url: "http://localhost:9999", tokenURL: "", clientID: "", secret: "", wantNil: false},
		{name: "half-configured auth fails boot", url: "http://localhost:9999", tokenURL: "http://localhost:9999/token", clientID: "", secret: "", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LODE_GRAPHSERVER_URL", tc.url)
			t.Setenv("LODE_GRAPHSERVER_TOKEN_URL", tc.tokenURL)
			t.Setenv("LODE_GRAPHSERVER_CLIENT_ID", tc.clientID)
			t.Setenv("LODE_GRAPHSERVER_CLIENT_SECRET", tc.secret)

			p, err := graphProjector(prometheus.NewRegistry(), nil)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("graphProjector() = %v, nil, want an error", p)
				}
				return
			}
			if err != nil {
				t.Fatalf("graphProjector(): %v", err)
			}
			if tc.wantNil && p != nil {
				t.Fatalf("graphProjector() = %v, want nil (projection disabled)", p)
			}
			if !tc.wantNil && p == nil {
				t.Fatal("graphProjector() = nil, want a non-nil projector")
			}
		})
	}
}
