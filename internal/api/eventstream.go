package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// eventstream.go serves GET /api/v1/events/stream: the ordered event log
// followed live over server-sent events (spec 025 §15/§18), behind
// permEventStream.
//
// It is a poller, not a listener. Every read goes through the same
// horizon-bounded store.ListEvents every other consumer uses, so a stream
// structurally cannot show an id that a later read would order before it —
// which a LISTEN/NOTIFY push, delivered at NOTIFY time rather than at
// horizon-advance time, could not promise.

const (
	// streamPageSize is the events one poll may carry. A full page means the
	// follower is behind, so the next poll happens immediately instead of
	// after a tick — a backlog drains at query speed rather than at
	// streamPageSize per interval.
	streamPageSize = 200

	defaultStreamPollInterval      = time.Second
	defaultStreamHeartbeatInterval = 30 * time.Second
)

// streamPollInterval and streamHeartbeatInterval are the loop's two timings,
// in nanoseconds. Atomic and overridable so a test can shorten them without
// racing a stream that is still draining; zero means "use the default".
var (
	streamPollInterval      atomic.Int64
	streamHeartbeatInterval atomic.Int64
)

func streamPoll() time.Duration {
	if d := streamPollInterval.Load(); d > 0 {
		return time.Duration(d)
	}
	return defaultStreamPollInterval
}

func streamHeartbeat() time.Duration {
	if d := streamHeartbeatInterval.Load(); d > 0 {
		return time.Duration(d)
	}
	return defaultStreamHeartbeatInterval
}

// streamEvents handles GET /api/v1/events/stream?type=&after=.
//
// The start cursor is the Last-Event-ID request header when the client is
// reconnecting, else ?after=, else the current head — a bare follow shows
// what happens next, and `lode event tail --follow` supplies its own backlog
// cursor so the gap between the one-shot page and the stream is closed by the
// client that knows where it stopped.
func (s *server) streamEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	typ := q.Get("type")

	// Cursor parsing happens before any header, so a malformed one is an
	// ordinary 422 rather than an error mid-stream.
	cursor := int64(-1) // -1: no cursor given, start at the head
	if v := q.Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeErr(w, http.StatusUnprocessableEntity, "invalid after: must be a non-negative integer event id")
			return
		}
		cursor = n
	}
	// Last-Event-ID wins: it is the browser's automatic reconnect, and it
	// describes where this client actually stopped.
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeErr(w, http.StatusUnprocessableEntity, "invalid Last-Event-ID: must be a non-negative integer event id")
			return
		}
		cursor = n
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// Nginx buffers proxied responses by default, which would hold a stream
	// until its buffer filled.
	h.Set("X-Accel-Buffering", "no")

	// Flush the header before touching the database, so a client blocks
	// waiting for data rather than waiting to learn whether it has a stream.
	// A writer that cannot flush is a server bug (see statusWriter.Unwrap),
	// not a client one, and is reported as a plain JSON 500 — with the
	// stream headers taken back off, so the error is not mislabelled as an
	// event stream.
	rc := http.NewResponseController(w)
	if err := rc.Flush(); err != nil {
		for _, k := range []string{"Content-Type", "Cache-Control", "Connection", "X-Accel-Buffering"} {
			h.Del(k)
		}
		s.log.Error("event stream: response writer cannot flush", "err", err)
		writeErr(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	s.observeEventStreamOpen()
	defer s.observeEventStreamClose()

	ctx := r.Context()
	if cursor < 0 {
		var err error
		if cursor, err = s.streamHead(ctx, typ); err != nil {
			s.streamEnd(ctx, "find stream head", err)
			return
		}
	}

	ticker := time.NewTicker(streamPoll())
	defer ticker.Stop()
	lastWrite := time.Now()

	for {
		events, err := s.st.ListEvents(ctx, store.EventFilter{After: cursor, Type: typ, Limit: streamPageSize})
		if err != nil {
			s.streamEnd(ctx, "list events", err)
			return
		}
		if len(events) > 0 {
			for _, e := range events {
				if err := writeEventFrame(w, e); err != nil {
					// The client hung up mid-write. Normal, not an error.
					return
				}
				cursor = e.ID
			}
			if err := rc.Flush(); err != nil {
				return
			}
			s.observeEventStreamSent(len(events))
			lastWrite = time.Now()
		} else if time.Since(lastWrite) >= streamHeartbeat() {
			// An SSE comment: ignored by every client, but it keeps proxies
			// and idle timeouts from dropping a quiet stream silently.
			if _, err := w.Write([]byte(":\n\n")); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
			lastWrite = time.Now()
		}

		if len(events) == streamPageSize {
			continue // behind: keep draining without waiting a tick
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// streamHead returns the id of the newest event visible past the commit
// horizon, which a bare follow starts from. It pages ListEvents rather than
// asking for MAX(id): one horizon-bounded query is the only way this package
// reads the log, and the alternative is a second predicate to keep in step
// with the first.
//
// The cost is one round trip per streamPageSize rows, paid only by a client
// that supplies no cursor at all — `lode event tail --follow` always passes
// the last id it printed. docs/follow-ups.md records the head query that
// would make it O(1).
func (s *server) streamHead(ctx context.Context, typ string) (int64, error) {
	var head int64
	for {
		events, err := s.st.ListEvents(ctx, store.EventFilter{After: head, Type: typ, Limit: streamPageSize})
		if err != nil {
			return 0, err
		}
		if len(events) == 0 {
			return head, nil
		}
		head = events[len(events)-1].ID
		if len(events) < streamPageSize {
			return head, nil
		}
	}
}

// streamEnd logs the end of a stream at the level it deserves: a client that
// hung up mid-query cancels the context, which surfaces as a query error and
// is the normal way a follow ends, not something to page anyone about.
func (s *server) streamEnd(ctx context.Context, what string, err error) {
	if ctx.Err() != nil {
		s.log.Debug("event stream closed by client", "during", what)
		return
	}
	s.log.Error("event stream failed", "during", what, "err", err)
}

// writeEventFrame writes one SSE message. `id:` carries the event id so a
// reconnecting client can send it back as Last-Event-ID, and `event:` the
// event type so a browser can addEventListener on it; both are also inside
// the JSON, which is the form a non-browser client parses.
func writeEventFrame(w http.ResponseWriter, e store.Event) error {
	// eventJSON is the same projection GET /api/v1/events serves, so a
	// follower and a poller see identical objects. Marshalling compacts the
	// embedded payload, which matters here: a data: line may not contain a
	// newline.
	data, err := json.Marshal(toEventJSON(e))
	if err != nil {
		return fmt.Errorf("encode event %d: %w", e.ID, err)
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, data)
	return err
}
