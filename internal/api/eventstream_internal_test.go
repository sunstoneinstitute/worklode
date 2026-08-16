package api

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/store"
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

// TestStreamPageSizeWithinStoreCap pins the dependency streamHead's
// termination condition rests on. streamHead stops when a page comes back
// short; if streamPageSize ever exceeded the store's own cap, every page
// would look short and a bare follow would take the last id of the first page
// as the head — then replay an arbitrary backlog, silently. The compile-time
// assertion in eventstream.go is the real guard; this test is what names the
// failure if someone deletes it.
func TestStreamPageSizeWithinStoreCap(t *testing.T) {
	if streamPageSize > store.MaxEventListLimit {
		t.Fatalf("streamPageSize = %d exceeds store.MaxEventListLimit = %d: "+
			"streamHead would read every page as short and stop at the first",
			streamPageSize, store.MaxEventListLimit)
	}
}

// TestWriteEventFrameStripsLineBreaks is the unit-level half of
// TestEventStreamTypeCannotInjectFrames: whatever an event type contains, the
// frame it produces has exactly the four lines a frame has.
func TestWriteEventFrameStripsLineBreaks(t *testing.T) {
	var buf bytes.Buffer
	err := writeEventFrame(&buf, store.Event{
		ID:      7,
		Type:    "a\r\nevent: forged\r\ndata: {}\r\n\r\nb",
		Payload: []byte(`{"k":"v"}`),
	})
	if err != nil {
		t.Fatalf("writeEventFrame: %v", err)
	}
	got := buf.String()
	if !strings.HasSuffix(got, "\n\n") {
		t.Fatalf("frame does not end in a blank line: %q", got)
	}
	lines := strings.Split(strings.TrimSuffix(got, "\n\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("frame has %d lines, want 3 (id, event, data): %q", len(lines), got)
	}
	if lines[1] != "event: aevent: forgeddata: {}b" {
		t.Fatalf("event line = %q, want the type with every line break removed", lines[1])
	}
}

// TestWriteEventFrameReportsEncodeFailure pins that an unencodable payload is
// reported as an encoding fault and not as the client hanging up — the
// handler branches on it to decide whether anything gets logged.
func TestWriteEventFrameReportsEncodeFailure(t *testing.T) {
	err := writeEventFrame(io.Discard, store.Event{ID: 9, Type: "bad", Payload: []byte(`{"unterminated`)})
	if !errors.Is(err, errEncodeEvent) {
		t.Fatalf("writeEventFrame with an invalid payload = %v, want errEncodeEvent", err)
	}
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
