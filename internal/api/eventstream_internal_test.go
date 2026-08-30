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

// SetStreamPollInterval shortens the stream's poll interval for the rest of
// this test binary's run. Exported from a package-internal test file so the
// external api_test package can reach an unexported knob without the knob
// becoming part of the server's configuration surface.
//
// It does not restore the previous value on Cleanup: every caller in
// eventstream_test.go asks for the same fast interval, and under
// t.Parallel() several of them are set concurrently, so a save-and-restore
// pattern raced — whichever test finished first put the default (or another
// test's snapshot) back while its siblings were still mid-stream, starving
// their poll loop and producing a spurious "no stream message" timeout
// (WL-438). No test depends on the production default being restored, so the
// simplest fix is to only ever move the interval down and leave it there.
func SetStreamPollInterval(t *testing.T, d time.Duration) {
	t.Helper()
	streamPollInterval.Store(int64(d))
}

// SetStreamHeartbeatInterval is SetStreamPollInterval for the heartbeat.
func SetStreamHeartbeatInterval(t *testing.T, d time.Duration) {
	t.Helper()
	streamHeartbeatInterval.Store(int64(d))
}

// TestStreamPageSizeWithinStoreCap pins the dependency streamHead's
// termination condition rests on. streamHead stops when a page comes back
// short; if streamPageSize ever exceeded the store's own cap, every page
// would look short and a bare follow would take the last id of the first page
// as the head — then replay an arbitrary backlog, silently. The compile-time
// assertion in eventstream.go is the real guard; this test is what names the
// failure if someone deletes it.
func TestStreamPageSizeWithinStoreCap(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	err := writeEventFrame(io.Discard, store.Event{ID: 9, Type: "bad", Payload: []byte(`{"unterminated`)})
	if !errors.Is(err, errEncodeEvent) {
		t.Fatalf("writeEventFrame with an invalid payload = %v, want errEncodeEvent", err)
	}
}

func TestObserveEventStream(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	s := &server{}
	s.observeEventStreamOpen()
	s.observeEventStreamClose()
	s.observeEventStreamSent(1)
}
