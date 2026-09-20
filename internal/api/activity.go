package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/otlp"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// maxOTLPBody caps an OTLP log batch at 4 MiB (spec 071 §1).
const maxOTLPBody = 4 << 20

// ingestOTLPLogs handles POST /otlp/v1/logs: an OTLP/JSON
// ExportLogsServiceRequest from a coding agent's log exporter (spec 071 §1).
// Attributed records are stored as task activity, the raw body is queued for
// the otel-gateway (§3), and the answer is an empty
// ExportLogsServiceResponse.
//
// The status codes are the exporter's retry contract: 400 on an undecodable
// body means "do not resend", 500 on a store failure means "resend".
func (s *server) ingestOTLPLogs(w http.ResponseWriter, r *http.Request) {
	sub := subjectFrom(r)
	// Per-actor budget, checked before the body is read so a throttled
	// request costs nothing but the lookup (WL-863). The gateway's own
	// throttle (§3) protects the gateway, not this table.
	if !s.otlpLimit.Allow(otlpLimitKey(sub)) {
		s.otlpMetrics.Ingest("throttled")
		w.Header().Set("Retry-After", "1")
		writeErr(w, http.StatusTooManyRequests, "too many OTLP log batches; retry in a second")
		return
	}

	contentType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "application/json") {
		s.otlpMetrics.Ingest("unsupported_media")
		writeErr(w, http.StatusUnsupportedMediaType,
			"OTLP logs must be application/json (OTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/json)")
		return
	}

	// Read the whole body rather than decode from the stream: the forwarder
	// needs the original bytes. io.ReadAll returns a buffer nothing else
	// holds, so it can be handed to Enqueue without copying.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxOTLPBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			s.otlpMetrics.Ingest("too_large")
			writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		s.otlpMetrics.Ingest("bad_request")
		writeErr(w, http.StatusBadRequest, "read request body: "+err.Error())
		return
	}

	records, err := otlp.DecodeLogs(body)
	if err != nil {
		s.otlpMetrics.Ingest("bad_request")
		writeErr(w, http.StatusBadRequest, "invalid OTLP/JSON body: "+err.Error())
		return
	}

	rows := make([]model.TaskActivity, 0, len(records))
	unattributed := 0
	for _, rec := range records {
		if rec.Task == "" {
			unattributed++
			continue
		}
		// A task-scoped token speaks for its own task only (001 §2.1). The
		// whole batch is refused, since a mixed batch means the exporter is
		// stamping records it has no claim on.
		if sub.TaskID != "" && rec.Task != sub.TaskID {
			s.otlpMetrics.Ingest("forbidden")
			writeErr(w, http.StatusForbidden, "token is scoped to task "+sub.TaskID)
			return
		}
		rows = append(rows, model.TaskActivity{
			Task:    rec.Task,
			Actor:   sub.ActorID,
			Agent:   activityAgent(rec.Service),
			Session: rec.Session,
			At:      rec.At,
			Event:   rec.Event,
			Attrs:   rec.Attrs,
		})
	}

	stored, err := s.st.AppendTaskActivity(r.Context(), rows)
	if err != nil {
		s.otlpMetrics.Ingest("store_error")
		s.mapStoreErr(w, err)
		return
	}
	s.otlpMetrics.Records("stored", stored)
	s.otlpMetrics.Records("unattributed", unattributed)
	// The store drops a row whose task id it has never seen. Counting the
	// difference is how a mis-stamped exporter becomes visible.
	s.otlpMetrics.Records("unknown_task", len(rows)-stored)

	s.otlpForward.Enqueue(contentType, body)
	s.otlpMetrics.Ingest("ok")
	writeJSON(w, http.StatusOK, struct{}{})
}

// otlpLimitKey names the bucket a request counts against: the token's actor,
// the same identity every stored row is attributed to. An open deployment
// has no actor, so its callers share one bucket.
func otlpLimitKey(sub Subject) string {
	if sub.ActorID == "" {
		return "anon"
	}
	return sub.ActorID
}

// activityAgent maps an OTLP resource service.name onto spec 012's agent
// vocabulary (spec 071 §1). An absent name is the route's own default,
// claude-code; a name worklode has no id for is recorded as other, so an
// unfamiliar harness keeps its activity instead of losing the batch.
func activityAgent(service string) string {
	switch service {
	case "", "claude", "claude-code":
		return "claude-code"
	default:
		return model.NormalizeAgent(service)
	}
}

// activityPageSize is the rows the task page loads and the rows one stream
// poll may carry (spec 071 §4). Both call sites pass this constant, so no
// request-supplied limit ever reaches the store.
const activityPageSize = 200

// taskActivityEvents handles GET /tasks/{id}/activity/events: one task's
// activity log followed live, so the Activity card grows as the agent works
// instead of going stale until a reload. Same loop as progressEvents —
// cursor from ?after or Last-Event-ID, a poll, heartbeat comments while idle,
// and an end when either the client or the server's background context goes
// away — over task_activity instead of the event log.
//
// The data of a frame is the server-rendered row, so the page inserts markup
// it never built (§4). SSE has no multi-line data field, so the markup is
// split across data: lines and the browser rejoins them.
//
// permWebRead, like the page itself: a frame carries nothing a reader could
// not already see on the task page.
func (s *server) taskActivityEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	// The task is read first so an unknown id answers 404 the way every
	// other task route does, rather than opening an endless empty stream
	// that would tell a caller nothing but still let them probe for ids.
	if _, err := s.st.GetTask(ctx, id); err != nil {
		s.webStoreErr(w, err)
		return
	}

	cursor := int64(-1) // -1: no cursor given, start at the newest row
	if v := r.URL.Query().Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeErr(w, http.StatusUnprocessableEntity, "invalid after: must be a non-negative integer activity id")
			return
		}
		cursor = n
	}
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeErr(w, http.StatusUnprocessableEntity, "invalid Last-Event-ID: must be a non-negative integer activity id")
			return
		}
		cursor = n
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	if err := rc.Flush(); err != nil {
		for _, k := range []string{"Content-Type", "Cache-Control", "X-Accel-Buffering"} {
			h.Del(k)
		}
		s.log.Error("activity event stream: response writer cannot flush", "err", err)
		writeErr(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	s.observeActivityStreamOpen()
	defer s.observeActivityStreamClose()

	ctx, endStream := context.WithCancel(ctx)
	defer endStream()
	defer context.AfterFunc(s.bgCtx, endStream)()

	if cursor < 0 {
		newest, err := s.st.TaskActivity(ctx, id, 0, 1)
		if err != nil {
			s.streamEnd(ctx, "find activity stream head", err)
			return
		}
		cursor = 0
		if len(newest) > 0 {
			cursor = newest[0].ID
		}
	}

	ticker := time.NewTicker(streamPoll())
	defer ticker.Stop()
	lastWrite := time.Now()

	for {
		rows, err := s.activityAfter(ctx, id, cursor)
		if err != nil {
			s.streamEnd(ctx, "list task activity", err)
			return
		}
		switch {
		case len(rows) > 0:
			for _, a := range rows {
				if err := writeActivityFrame(ctx, w, a); err != nil {
					if errors.Is(err, errEncodeEvent) {
						s.log.Error("activity event stream: rendering a row failed", "activity", a.ID, "err", err)
					}
					return
				}
				cursor = a.ID
			}
			if err := rc.Flush(); err != nil {
				return
			}
			s.observeActivityStreamFrames(len(rows))
			lastWrite = time.Now()
		case time.Since(lastWrite) >= streamHeartbeat():
			// An SSE comment, same as the other two streams': ignored by
			// every client, and it keeps an idle but live follow from being
			// dropped by a proxy.
			if _, err := w.Write([]byte(":\n\n")); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
			lastWrite = time.Now()
		}

		if len(rows) == activityPageSize {
			continue // behind: drain at query speed rather than one page a tick
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// activityAfter is store.TaskActivity's forward page in the one shape the
// stream can consume: oldest of the new rows first, so the cursor ends on the
// highest id sent. A cursor of 0 (an empty table when the follow opened, or
// ?after=0 asking for a replay) reads as "newest page, newest first" in the
// store, so that case is reversed here.
func (s *server) activityAfter(ctx context.Context, taskID string, after int64) ([]model.TaskActivity, error) {
	rows, err := s.st.TaskActivity(ctx, taskID, after, activityPageSize)
	if err != nil || after > 0 {
		return rows, err
	}
	slices.Reverse(rows)
	return rows, nil
}

// activityFrameLines normalises the three characters SSE reads as a line
// terminator down to one, so the split below sees every break the browser's
// parser would. An attribute the exporter chose (an error message, say) can
// carry any of them, and an unsplit \r would end a data: line mid-value and
// let the rest be read as a field of its own.
var activityFrameLines = strings.NewReplacer("\r\n", "\n", "\r", "\n")

// writeActivityFrame writes one SSE message whose data is the rendered row.
// SSE defines no multi-line data field, so each line of the markup becomes
// its own data: line — every line, blank ones included, since an unprefixed
// blank line would end the frame early — and the browser rejoins them with
// newlines before the page inserts the row as HTML.
func writeActivityFrame(ctx context.Context, w io.Writer, a model.TaskActivity) error {
	var buf bytes.Buffer
	if err := ui.ActivityRowFragment(activityRow(a)).Render(ctx, &buf); err != nil {
		return fmt.Errorf("%w %d: %w", errEncodeEvent, a.ID, err)
	}
	if _, err := fmt.Fprintf(w, "id: %d\nevent: activity\n", a.ID); err != nil {
		return err
	}
	body := strings.TrimRight(activityFrameLines.Replace(buf.String()), "\n")
	for line := range strings.SplitSeq(body, "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}
