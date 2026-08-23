package serverapp

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
// and never cancels them, so a handler that never returned on its own would
// hold shutdown to its deadline and make the command exit non-zero. The
// stream ends on the background context that SIGTERM cancels (streamEvents
// does this), so shutdown completes promptly and returns nil without having
// to cancel anything else that is in flight.
func TestShutdownServersWithOpenStream(t *testing.T) {
	reqCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()

	// bgCtx stands in for the SIGTERM-notified context serve.go passes to
	// api.NewServer; it is already cancelled by the time shutdown runs.
	bgCtx, sigterm := context.WithCancel(context.Background())

	streaming := make(chan struct{}) // closed once the handler is in flight
	ended := make(chan struct{})     // closed once the handler returned
	srv, url := startTestServer(t, reqCtx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		http.NewResponseController(w).Flush()
		close(streaming)
		select {
		case <-bgCtx.Done():
		case <-r.Context().Done():
		}
		close(ended)
	}))
	adminSrv, _ := startTestServer(t, reqCtx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	<-streaming

	sigterm()
	start := time.Now()
	if err := shutdownServers(cancelRequests, shutdownTimeout, srv, adminSrv); err != nil {
		t.Fatalf("shutdownServers with a stream open: %v, want nil", err)
	}
	if d := time.Since(start); d >= shutdownTimeout {
		t.Fatalf("shutdownServers took %v, want well under the %v budget", d, shutdownTimeout)
	}
	select {
	case <-ended:
	default:
		t.Fatal("stream handler never returned, so it did not observe shutdown")
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

	start := time.Now()
	if err := shutdownServers(cancelRequests, shutdownTimeout, srv, adminSrv); err != nil {
		t.Fatalf("shutdownServers: %v, want nil", err)
	}
	if d := time.Since(start); d >= shutdownTimeout {
		t.Fatalf("idle shutdown took %v, want it not to wait out the %v budget", d, shutdownTimeout)
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

// TestGraphClientFromEnv: unset LODE_GRAPHSERVER_URL disables the graph
// (projection and the graph-backed overview reads alike); URL-only configures
// an unauthenticated client; a half-configured auth triple
// (graphserver.FromEnv's contract) fails the boot rather than silently
// disabling. Each case sets all four LODE_GRAPHSERVER_* variables explicitly,
// since t.Setenv restores after the test but a var left set from an earlier
// case would otherwise leak into the next one's meaning.
//
// graphProjector follows the client, so the projector assertion rides along
// here: one env read per process is the point of the split.
func TestGraphClientFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name     string
		url      string
		tokenURL string
		clientID string
		secret   string
		wantNil  bool
		wantErr  bool
	}{
		{name: "unset disables the graph", url: "", tokenURL: "", clientID: "", secret: "", wantNil: true},
		{name: "url only", url: "http://localhost:9999", tokenURL: "", clientID: "", secret: "", wantNil: false},
		{name: "half-configured auth fails boot", url: "http://localhost:9999", tokenURL: "http://localhost:9999/token", clientID: "", secret: "", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LODE_GRAPHSERVER_URL", tc.url)
			t.Setenv("LODE_GRAPHSERVER_TOKEN_URL", tc.tokenURL)
			t.Setenv("LODE_GRAPHSERVER_CLIENT_ID", tc.clientID)
			t.Setenv("LODE_GRAPHSERVER_CLIENT_SECRET", tc.secret)

			gc, err := graphClientFromEnv()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("graphClientFromEnv() = %v, nil, want an error", gc)
				}
				return
			}
			if err != nil {
				t.Fatalf("graphClientFromEnv(): %v", err)
			}
			if tc.wantNil && gc != nil {
				t.Fatalf("graphClientFromEnv() = %v, want nil (graph disabled)", gc)
			}
			if !tc.wantNil && gc == nil {
				t.Fatal("graphClientFromEnv() = nil, want a client")
			}
			p := graphProjector(prometheus.NewRegistry(), nil, gc)
			if tc.wantNil && p != nil {
				t.Fatalf("graphProjector() = %v, want nil (projection disabled)", p)
			}
			if !tc.wantNil && p == nil {
				t.Fatal("graphProjector() = nil, want a non-nil projector")
			}
		})
	}
}

// TestRunRejectsBadInstanceEnv: LODE_INSTANCE_ENV is validated before the
// store is opened (039 §3), like LODE_WEB_OPEN and LODE_CLUSTER_ENV_MAP, so an
// operator sees the typo in the setting rather than a connection error. The
// DSN below is deliberately unreachable: if the check ever moved after
// store.Open, this test would fail with a connection error instead.
func TestRunRejectsBadInstanceEnv(t *testing.T) {
	t.Setenv("LODE_INSTANCE_ENV", "staging")
	err := Run(context.Background(), Options{
		DSN:         "postgres://nobody@127.0.0.1:1/nowhere?sslmode=disable",
		Listen:      "127.0.0.1:0",
		AdminListen: "127.0.0.1:0",
	})
	if err == nil {
		t.Fatal("runServe accepted LODE_INSTANCE_ENV=staging")
	}
	if !strings.Contains(err.Error(), "LODE_INSTANCE_ENV") {
		t.Fatalf("error = %q, want it to name LODE_INSTANCE_ENV", err)
	}
}

// TestShutdownServersLetsInFlightWriteFinish is WL-246: a rolling deploy sends
// SIGTERM while ordinary requests are still in flight. Cutting those requests'
// contexts short rolls their transaction back — the caller gets a 500, and a
// GitHub webhook delivery, which is never retried, is lost for good. An
// in-flight request must therefore keep an uncancelled context and complete,
// however long shutdown has been running; only the whole-budget backstop may
// cancel it.
func TestShutdownServersLetsInFlightWriteFinish(t *testing.T) {
	reqCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()

	inFlight := make(chan struct{}) // closed once the handler is running
	release := make(chan struct{})  // closed to let the handler finish
	gotErr := make(chan error, 1)   // the handler's view of its own context
	srv, url := startTestServer(t, reqCtx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(inFlight)
		<-release
		gotErr <- r.Context().Err()
		w.WriteHeader(http.StatusOK)
	}))
	adminSrv, _ := startTestServer(t, reqCtx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	respCh := make(chan *http.Response, 1)
	go func() {
		resp, err := http.Get(url)
		if err != nil {
			respCh <- nil
			return
		}
		respCh <- resp
	}()
	<-inFlight

	done := make(chan error, 1)
	go func() { done <- shutdownServers(cancelRequests, shutdownTimeout, srv, adminSrv) }()

	// Well past the old two-second grace window, so a shutdown that cancels
	// ordinary requests has already done so by the time the handler looks.
	time.Sleep(2*time.Second + 200*time.Millisecond)
	close(release)

	if err := <-gotErr; err != nil {
		t.Fatalf("in-flight request context was cancelled during shutdown: %v, want it left alone", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("shutdownServers: %v, want nil", err)
	}
	resp := <-respCh
	if resp == nil {
		t.Fatal("in-flight request failed, want it to complete")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("in-flight request status = %d, want 200", resp.StatusCode)
	}
}
