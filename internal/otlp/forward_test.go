package otlp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type gotRequest struct {
	path          string
	contentType   string
	authorization string
	body          []byte
}

func newTestServer(t *testing.T, status int) (*httptest.Server, chan gotRequest) {
	t.Helper()
	received := make(chan gotRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- gotRequest{
			path:          r.URL.Path,
			contentType:   r.Header.Get("Content-Type"),
			authorization: r.Header.Get("Authorization"),
			body:          body,
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, received
}

func TestForwarderRun_PostsExactBody(t *testing.T) {
	srv, received := newTestServer(t, http.StatusOK)
	m := NewMetrics(prometheus.NewRegistry())
	f := NewForwarder(srv.URL, "sekret-token", m)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.Run(ctx)

	if !f.Enqueue("application/json", []byte(`{"resourceLogs":[]}`)) {
		t.Fatal("Enqueue returned false")
	}

	select {
	case got := <-received:
		if got.path != "/v1/logs" {
			t.Errorf("path = %q, want /v1/logs", got.path)
		}
		if got.contentType != "application/json" {
			t.Errorf("content-type = %q, want application/json", got.contentType)
		}
		if got.authorization != "Bearer sekret-token" {
			t.Errorf("authorization = %q, want Bearer sekret-token", got.authorization)
		}
		if string(got.body) != `{"resourceLogs":[]}` {
			t.Errorf("body = %q, want the exact payload", got.body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream never received the request")
	}

	waitForCounter(t, m.forward.WithLabelValues("ok"), 1)
}

func TestForwarderRun_ClientErrorDropped(t *testing.T) {
	srv, received := newTestServer(t, http.StatusBadRequest)
	m := NewMetrics(prometheus.NewRegistry())
	f := NewForwarder(srv.URL, "tok", m)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.Run(ctx)

	f.Enqueue("application/json", []byte(`{}`))
	<-received

	waitForCounter(t, m.forward.WithLabelValues("client_error"), 1)
}

func TestForwarderRun_ServerErrorDropped(t *testing.T) {
	srv, received := newTestServer(t, http.StatusInternalServerError)
	m := NewMetrics(prometheus.NewRegistry())
	f := NewForwarder(srv.URL, "tok", m)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.Run(ctx)

	f.Enqueue("application/json", []byte(`{}`))
	<-received

	waitForCounter(t, m.forward.WithLabelValues("server_error"), 1)
}

func TestForwarderEnqueue_FullQueueDrops(t *testing.T) {
	m := NewMetrics(prometheus.NewRegistry())
	f := NewForwarder("http://upstream.invalid", "tok", m)
	// Nothing drains the queue: Run is never started.

	for i := 0; i < 256; i++ {
		if !f.Enqueue("application/json", []byte("x")) {
			t.Fatalf("Enqueue %d: want true, queue should not be full yet", i)
		}
	}
	if f.Enqueue("application/json", []byte("x")) {
		t.Fatal("Enqueue on a full queue: want false")
	}

	waitForCounter(t, m.queueDropped, 1)
}

func TestForwarderNil(t *testing.T) {
	var f *Forwarder
	if f.Enqueue("application/json", []byte("x")) {
		t.Fatal("a nil *Forwarder's Enqueue must return false")
	}
	// Run must return at once rather than block or panic.
	done := make(chan struct{})
	go func() {
		f.Run(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a nil *Forwarder's Run did not return")
	}
}

// waitForCounter polls a prometheus counter until it reaches want or the
// test times out — Run's post happens on its own goroutine, off the
// caller's stack.
func waitForCounter(t *testing.T, c prometheus.Counter, want float64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := testutil.ToFloat64(c); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("counter = %v, want %v (timed out)", testutil.ToFloat64(c), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestForwarderEnqueue_ByteCapDrops(t *testing.T) {
	m := NewMetrics(prometheus.NewRegistry())
	f := NewForwarder("http://upstream.invalid", "tok", m)
	// Nothing drains the queue: Run is never started.

	chunk := make([]byte, 4<<20)
	for i := 0; i < maxQueuedBytes/len(chunk); i++ {
		if !f.Enqueue("application/json", chunk) {
			t.Fatalf("Enqueue %d: want true, the byte cap is not reached yet", i)
		}
	}
	if f.Enqueue("application/json", chunk) {
		t.Fatal("Enqueue past the byte cap: want false")
	}
	if got := f.queuedBytes.Load(); got != int64(maxQueuedBytes) {
		t.Fatalf("queued bytes = %d, want %d", got, maxQueuedBytes)
	}

	waitForCounter(t, m.queueDropped, 1)
}

func TestForwarderRun_DrainFreesQueuedBytes(t *testing.T) {
	srv, received := newTestServer(t, http.StatusOK)
	m := NewMetrics(prometheus.NewRegistry())
	f := NewForwarder(srv.URL, "tok", m)

	body := make([]byte, 1<<20)
	if !f.Enqueue("application/json", body) {
		t.Fatal("Enqueue returned false")
	}
	if got := f.queuedBytes.Load(); got != int64(len(body)) {
		t.Fatalf("queued bytes = %d, want %d", got, len(body))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.Run(ctx)
	<-received

	deadline := time.Now().Add(2 * time.Second)
	for f.queuedBytes.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("queued bytes = %d after a drain, want 0", f.queuedBytes.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
