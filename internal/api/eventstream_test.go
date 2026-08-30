package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// --- fixture ----------------------------------------------------------------

// streamFixture is a booted server reachable three ways: as an http.Handler
// (for the recorder-based helpers the rest of this package uses), as a live
// URL (streaming needs a real connection, not a recorder that only reveals
// its buffer after the handler returns), and as the admin handler that serves
// /metrics.
type streamFixture struct {
	st          *store.Store
	h           http.Handler
	admin       http.Handler
	url         string
	adminToken  string
	workerToken string
}

// newStreamTestServer boots a server behind httptest.NewServer. The live
// listener is the point: the stream must be proven to reach a client through
// the real logging and metrics middleware, and an httptest.ResponseRecorder
// cannot tell a flushed response apart from a buffered one.
func newStreamTestServer(t *testing.T) streamFixture {
	t.Helper()
	return newStreamTestServerWithBackground(t, nil)
}

// newStreamTestServerWithBackground is newStreamTestServer with an explicit
// background context — the one serve.go cancels on SIGTERM.
func newStreamTestServerWithBackground(t *testing.T, bg context.Context) streamFixture {
	t.Helper()
	st := newTestStore(t)

	adminToken := seedActor(t, st, "alice", "human", "Alice", true)
	workerToken := seedActor(t, st, "worker", "agent", "Worker", false)

	h, admin, err := api.NewServer(st, api.Config{BackgroundCtx: bg})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	return streamFixture{st: st, h: h, admin: admin, url: ts.URL,
		adminToken: adminToken, workerToken: workerToken}
}

// metric returns the current value of a no-label metric from the admin
// listener's /metrics, or -1 when the sample is absent.
func (f streamFixture) metric(t *testing.T, name string) float64 {
	t.Helper()
	rr := httptest.NewRecorder()
	f.admin.ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d", rr.Code)
	}
	for line := range strings.SplitSeq(rr.Body.String(), "\n") {
		rest, ok := strings.CutPrefix(line, name+" ")
		if !ok {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
		if err != nil {
			t.Fatalf("parse metric %s from %q: %v", name, line, err)
		}
		return v
	}
	return -1
}

// waitMetric polls /metrics until name reaches want, or fails.
func (f streamFixture) waitMetric(t *testing.T, name string, want float64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var got float64
	for time.Now().Before(deadline) {
		got = f.metric(t, name)
		if got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s = %v after polling, want %v", name, got, want)
}

// --- SSE client -------------------------------------------------------------

// sseMessage is one server-sent-event message, or a comment line (the
// heartbeat) when Comment is set.
type sseMessage struct {
	ID      string
	Event   string
	Data    string
	Comment bool
}

// sseStream is an open stream plus a goroutine draining it into a channel.
type sseStream struct {
	resp *http.Response
	msgs chan sseMessage
}

// streamRequest issues one GET /api/v1/events/stream and returns the raw
// response without consuming the body. It never asserts on the status, so
// the refusal cases can use it too.
func streamRequest(t *testing.T, ctx context.Context, f streamFixture, query, token, lastEventID string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url+"/api/v1/events/stream"+query, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	// A client with no timeout: a stream is meant to stay open, and the
	// request context is what ends it.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/events/stream%s: %v", query, err)
	}
	return resp
}

// openEventStream opens a stream that must be live, and drains it in the
// background. That the client gets a response header at all is already the
// first half of the flush proof: net/http returns from Do only once the
// server has flushed the header, so a handler whose writer never reached the
// real net/http flusher would either 500 or block here.
func openEventStream(t *testing.T, ctx context.Context, f streamFixture, query, token, lastEventID string) *sseStream {
	t.Helper()
	resp := streamRequest(t, ctx, f, query, token, lastEventID)
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}
	s := &sseStream{resp: resp, msgs: make(chan sseMessage, 256)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(s.msgs)
		sc := bufio.NewScanner(resp.Body)
		var cur sseMessage
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if cur != (sseMessage{}) {
					s.msgs <- cur
					cur = sseMessage{}
				}
			case strings.HasPrefix(line, ":"):
				s.msgs <- sseMessage{Comment: true}
			case strings.HasPrefix(line, "id:"):
				cur.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			case strings.HasPrefix(line, "event:"):
				cur.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				cur.Data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
	}()
	t.Cleanup(func() {
		resp.Body.Close()
		for range s.msgs { //nolint:revive // drain so the reader can finish
		}
		<-done
	})
	return s
}

// next returns the next message, failing on timeout or on a closed stream.
func (s *sseStream) next(t *testing.T, timeout time.Duration) sseMessage {
	t.Helper()
	select {
	case m, ok := <-s.msgs:
		if !ok {
			t.Fatalf("stream closed before the next message arrived")
		}
		return m
	case <-time.After(timeout):
		t.Fatalf("no stream message within %s", timeout)
	}
	return sseMessage{}
}

// nextEvent returns the next non-comment message.
func (s *sseStream) nextEvent(t *testing.T, timeout time.Duration) sseMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		m := s.next(t, time.Until(deadline))
		if !m.Comment {
			return m
		}
		if time.Now().After(deadline) {
			t.Fatalf("only heartbeats within %s, want an event", timeout)
		}
	}
}

// awaitLive waits for the first heartbeat, which the handler emits only after
// a poll that returned nothing. Reaching it proves the handler has finished
// establishing its start cursor, so an event recorded afterwards is
// unambiguously "after the stream opened" — without this the test would race
// the handler's own initial read.
func (s *sseStream) awaitLive(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		m := s.next(t, time.Until(deadline))
		if m.Comment {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no heartbeat within 10s")
		}
	}
}

// dataField decodes the JSON of a message's data: line and returns one field.
func dataField(t *testing.T, m sseMessage, key string) any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(m.Data), &got); err != nil {
		t.Fatalf("decode stream data %q: %v", m.Data, err)
	}
	return got[key]
}

// recordStreamEvent records one event outside any transaction the test holds
// open, returning its id.
func recordStreamEvent(t *testing.T, st *store.Store, extID, typ string, payload []byte) int64 {
	t.Helper()
	id, _, err := st.RecordEvent(context.Background(), "system", extID, typ, payload, nil)
	if err != nil {
		t.Fatalf("record event %s: %v", extID, err)
	}
	return id
}

// --- tests ------------------------------------------------------------------

// TestEventStreamGuard covers the route's guard and its headers: the stream
// is admin-only (permEventStream) while the bounded GET /api/v1/events beside
// it is not, and a refusal is a plain JSON error, never a stream.
func TestEventStreamGuard(t *testing.T) {
	t.Parallel()
	f := newStreamTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A plain user token is refused.
	resp := streamRequest(t, ctx, f, "", f.workerToken, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("worker stream status = %d, want 403", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("refused stream Content-Type = %q, want application/json", ct)
	}

	// The same token still reads the bounded log, so the split is
	// operational, not a wider data grant.
	if rr := doReq(t, f.h, "GET", "/api/v1/events", f.workerToken, nil); rr.Code != http.StatusOK {
		t.Fatalf("worker GET /api/v1/events status = %d, want 200", rr.Code)
	}

	// No credential at all is 401, not 403.
	anon := streamRequest(t, ctx, f, "", "", "")
	defer anon.Body.Close()
	if anon.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous stream status = %d, want 401", anon.StatusCode)
	}

	// An admin gets the stream, with the headers a proxy needs to leave it
	// alone.
	s := openEventStream(t, ctx, f, "", f.adminToken, "")
	if ct := s.resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	if got := s.resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
	if got := s.resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want no", got)
	}
}

// TestEventStreamRejectsBadCursor covers the query-string surface: a
// malformed after is refused before any stream byte is written, matching
// GET /api/v1/events.
func TestEventStreamRejectsBadCursor(t *testing.T) {
	t.Parallel()
	f := newStreamTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, q := range []string{"?after=not-an-int", "?after=-1"} {
		resp := streamRequest(t, ctx, f, q, f.adminToken, "")
		if resp.StatusCode != http.StatusUnprocessableEntity {
			resp.Body.Close()
			t.Fatalf("GET /api/v1/events/stream%s status = %d, want 422", q, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// Last-Event-ID gets the same treatment: it is the cursor a browser
	// resends on its own, so it is the one a client is least able to inspect
	// when it goes wrong.
	for _, h := range []string{"not-an-int", "-1", "4 OR 1=1"} {
		resp := streamRequest(t, ctx, f, "", f.adminToken, h)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			resp.Body.Close()
			t.Fatalf("Last-Event-ID %q status = %d, want 422", h, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// TestEventStreamTypeCannotInjectFrames covers the one field of an event that
// reaches the wire outside JSON. An event type is not validated on the way in
// — internal/hooks/flux.go builds one as "flux.<kind>.<reason>" from a signed
// webhook's JSON body, where a newline in Reason is perfectly legal — so a
// type carrying CR or LF could otherwise close the event: line early and
// write a whole forged message into every admin follower's stream. Being
// HMAC-signed buys the right to record an event, not the right to author
// frames in someone else's connection.
func TestEventStreamTypeCannotInjectFrames(t *testing.T) {
	t.Parallel()
	api.SetStreamPollInterval(t, 20*time.Millisecond)
	api.SetStreamHeartbeatInterval(t, 50*time.Millisecond)
	f := newStreamTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const evil = "flux.Kustomization.ok\ndata: {\"id\":999,\"type\":\"forged\"}\n\nevent: forged"
	s := openEventStream(t, ctx, f, "", f.adminToken, "")
	s.awaitLive(t)
	id := recordStreamEvent(t, f.st, "inject-1", evil, nil)

	m := s.nextEvent(t, 15*time.Second)
	if m.ID != strconv.FormatInt(id, 10) {
		t.Fatalf("message id = %q, want %d", m.ID, id)
	}
	if strings.ContainsAny(m.Event, "\r\n") {
		t.Fatalf("event field carries a line break: %q", m.Event)
	}
	if m.Event != strings.ReplaceAll(strings.ReplaceAll(evil, "\r", ""), "\n", "") {
		t.Fatalf("event field = %q, want the type with its line breaks stripped", m.Event)
	}
	// The forged frame's data must have arrived as part of *this* message's
	// event: line, never as a message of its own.
	if got := dataField(t, m, "id"); got != float64(id) {
		t.Fatalf("data id = %v, want %d — a second frame was injected", got, id)
	}

	// And nothing else is queued behind it: the next thing on the wire is a
	// heartbeat, not the forged message.
	for {
		next := s.next(t, 15*time.Second)
		if next.Comment {
			break
		}
		t.Fatalf("unexpected second message after the injecting event: %+v", next)
	}
}

// TestEventStreamDeliversLive is the heart of it: an event recorded after the
// stream opened reaches the client while the handler is still running. It is
// also the buffering proof — every response passes through the logging and
// metrics middleware, and if the stream's writes stopped at their
// statusWriter instead of reaching the real net/http flusher, nothing would
// arrive here until the handler returned, which it never does.
func TestEventStreamDeliversLive(t *testing.T) {
	t.Parallel()
	api.SetStreamPollInterval(t, 20*time.Millisecond)
	api.SetStreamHeartbeatInterval(t, 50*time.Millisecond)
	f := newStreamTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := openEventStream(t, ctx, f, "?type=stream.live", f.adminToken, "")
	s.awaitLive(t)
	f.waitMetric(t, "worklode_event_streams_active", 1)

	id := recordStreamEvent(t, f.st, "sl-1", "stream.live", []byte(`{"hello":"world"}`))

	m := s.nextEvent(t, 15*time.Second)
	if m.ID != strconv.FormatInt(id, 10) {
		t.Fatalf("message id = %q, want %d", m.ID, id)
	}
	if m.Event != "stream.live" {
		t.Fatalf("message event = %q, want stream.live", m.Event)
	}
	if got := dataField(t, m, "external_id"); got != "sl-1" {
		t.Fatalf("data external_id = %v, want sl-1", got)
	}
	if got := dataField(t, m, "id"); got != float64(id) {
		t.Fatalf("data id = %v, want %d", got, id)
	}
	payload, _ := dataField(t, m, "payload").(map[string]any)
	if payload["hello"] != "world" {
		t.Fatalf("data payload = %v, want {hello: world}", payload)
	}

	f.waitMetric(t, "worklode_event_stream_events_sent_total", 1)

	// A second event keeps arriving on the same connection: the cursor
	// advanced rather than replaying or stalling.
	id2 := recordStreamEvent(t, f.st, "sl-2", "stream.live", nil)
	m2 := s.nextEvent(t, 15*time.Second)
	if m2.ID != strconv.FormatInt(id2, 10) {
		t.Fatalf("second message id = %q, want %d", m2.ID, id2)
	}
}

// TestEventStreamResumesExactly covers the two backlog cursors — the
// Last-Event-ID reconnect header and the ?after= query parameter. Both are
// exclusive: resuming at 3 starts at 4, not at 3 and not at the head.
func TestEventStreamResumesExactly(t *testing.T) {
	t.Parallel()
	api.SetStreamPollInterval(t, 20*time.Millisecond)
	api.SetStreamHeartbeatInterval(t, 50*time.Millisecond)
	f := newStreamTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var ids []int64
	for i := 1; i <= 3; i++ {
		ids = append(ids, recordStreamEvent(t, f.st, fmt.Sprintf("sr-%d", i), "stream.resume", nil))
	}
	// The commit horizon is cluster-wide, so wait until all three are
	// readable before asserting on what a resume shows.
	pollEvents(t, f.h, f.adminToken, "?type=stream.resume", 3)

	for _, tc := range []struct {
		name, query, lastEventID string
	}{
		{"last-event-id", "?type=stream.resume", strconv.FormatInt(ids[1], 10)},
		{"after", fmt.Sprintf("?type=stream.resume&after=%d", ids[1]), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openEventStream(t, ctx, f, tc.query, f.adminToken, tc.lastEventID)
			m := s.nextEvent(t, 15*time.Second)
			if m.ID != strconv.FormatInt(ids[2], 10) {
				t.Fatalf("first replayed id = %q, want %d (resume is exclusive)", m.ID, ids[2])
			}
		})
	}

	// A bare stream carries no backlog: it starts at the head, so the three
	// events above never appear and only what happens next does.
	s := openEventStream(t, ctx, f, "?type=stream.resume", f.adminToken, "")
	s.awaitLive(t)
	id4 := recordStreamEvent(t, f.st, "sr-4", "stream.resume", nil)
	if m := s.nextEvent(t, 15*time.Second); m.ID != strconv.FormatInt(id4, 10) {
		t.Fatalf("bare stream first id = %q, want %d (head, not backlog)", m.ID, id4)
	}
}

// TestEventStreamTypeFilter covers ?type=: an event of another type recorded
// first must not appear before the matching one recorded after it.
func TestEventStreamTypeFilter(t *testing.T) {
	t.Parallel()
	api.SetStreamPollInterval(t, 20*time.Millisecond)
	api.SetStreamHeartbeatInterval(t, 50*time.Millisecond)
	f := newStreamTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := openEventStream(t, ctx, f, "?type=stream.wanted", f.adminToken, "")
	s.awaitLive(t)

	recordStreamEvent(t, f.st, "sf-other", "stream.unwanted", nil)
	wantID := recordStreamEvent(t, f.st, "sf-wanted", "stream.wanted", nil)

	m := s.nextEvent(t, 15*time.Second)
	if m.Event != "stream.wanted" || m.ID != strconv.FormatInt(wantID, 10) {
		t.Fatalf("filtered stream first message = %+v, want stream.wanted id %d", m, wantID)
	}
}

// TestEventStreamClientDisconnect covers the lifetime contract: cancelling
// the request ends the handler promptly and nothing outlives it, which the
// active-streams gauge returning to zero is the observable proof of.
func TestEventStreamClientDisconnect(t *testing.T) {
	t.Parallel()
	api.SetStreamPollInterval(t, 20*time.Millisecond)
	api.SetStreamHeartbeatInterval(t, 50*time.Millisecond)
	f := newStreamTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())

	s := openEventStream(t, ctx, f, "?type=stream.bye", f.adminToken, "")
	s.awaitLive(t)
	f.waitMetric(t, "worklode_event_streams_active", 1)

	cancel()

	deadline := time.After(10 * time.Second)
	for {
		select {
		case _, ok := <-s.msgs:
			if !ok {
				f.waitMetric(t, "worklode_event_streams_active", 0)
				return
			}
		case <-deadline:
			t.Fatal("stream did not end within 10s of the client hanging up")
		}
	}
}

// TestEventStreamEndsOnShutdown is WL-246: SIGTERM cancels the background
// context, and the stream must end on it. Without that, the only way to stop
// a live `lode event tail --follow` at shutdown is to cancel every in-flight
// request context — which rolls back the transactions of ordinary requests
// caught in the same window, losing a webhook delivery that GitHub will never
// retry.
func TestEventStreamEndsOnShutdown(t *testing.T) {
	t.Parallel()
	api.SetStreamPollInterval(t, 20*time.Millisecond)
	api.SetStreamHeartbeatInterval(t, 50*time.Millisecond)
	bg, sigterm := context.WithCancel(context.Background())
	defer sigterm()
	f := newStreamTestServerWithBackground(t, bg)

	s := openEventStream(t, context.Background(), f, "?type=stream.bye", f.adminToken, "")
	s.awaitLive(t)
	f.waitMetric(t, "worklode_event_streams_active", 1)

	sigterm()

	deadline := time.After(10 * time.Second)
	for {
		select {
		case _, ok := <-s.msgs:
			if !ok {
				f.waitMetric(t, "worklode_event_streams_active", 0)
				return
			}
		case <-deadline:
			t.Fatal("stream did not end within 10s of shutdown being signalled")
		}
	}
}
