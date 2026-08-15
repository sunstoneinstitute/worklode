package api

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// SetStreamPollInterval shortens the stream's poll interval for one test and
// restores it afterwards. Exported from a package-internal test file so the
// external api_test package can reach an unexported knob without the knob
// becoming part of the server's configuration surface.
func SetStreamPollInterval(t *testing.T, d time.Duration) {
	t.Helper()
	old := streamPollInterval.Swap(int64(d))
	t.Cleanup(func() { streamPollInterval.Store(old) })
}

// SetStreamHeartbeatInterval is SetStreamPollInterval for the heartbeat.
func SetStreamHeartbeatInterval(t *testing.T, d time.Duration) {
	t.Helper()
	old := streamHeartbeatInterval.Swap(int64(d))
	t.Cleanup(func() { streamHeartbeatInterval.Store(old) })
}

func TestObserveEventStream(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := &server{}
	s.initMetrics(reg)

	if got := testutil.ToFloat64(s.eventStreamsActive); got != 0 {
		t.Fatalf("streams active before any stream = %v, want 0 (pre-initialised, not no-data)", got)
	}

	s.observeEventStreamOpen()
	s.observeEventStreamOpen()
	if got := testutil.ToFloat64(s.eventStreamsActive); got != 2 {
		t.Fatalf("streams active = %v, want 2", got)
	}
	s.observeEventStreamClose()
	if got := testutil.ToFloat64(s.eventStreamsActive); got != 1 {
		t.Fatalf("streams active after one close = %v, want 1", got)
	}

	s.observeEventStreamSent(3)
	s.observeEventStreamSent(2)
	if got := testutil.ToFloat64(s.eventStreamEventsSent); got != 5 {
		t.Fatalf("events sent = %v, want 5", got)
	}
}

// TestObserveEventStreamNilSafe pins the convention every observer in this
// package follows: a *server built directly by a test, without initMetrics,
// must not panic.
func TestObserveEventStreamNilSafe(t *testing.T) {
	s := &server{}
	s.observeEventStreamOpen()
	s.observeEventStreamClose()
	s.observeEventStreamSent(1)
}
