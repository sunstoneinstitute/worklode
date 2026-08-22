package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// EventListFilter narrows ListEvents. Zero-valued fields do not filter.
type EventListFilter struct {
	Type  string
	Since time.Time
	After int64 // exclusive id cursor
	Limit int
}

// ListEvents calls GET /api/v1/events.
func (c *Client) ListEvents(ctx context.Context, f EventListFilter) (model.EventListResponse, []byte, error) {
	q := url.Values{}
	if f.Type != "" {
		q.Set("type", f.Type)
	}
	if !f.Since.IsZero() {
		q.Set("since", f.Since.UTC().Format(time.RFC3339))
	}
	if f.After != 0 {
		q.Set("after", strconv.FormatInt(f.After, 10))
	}
	if f.Limit != 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	return doJSON[model.EventListResponse](ctx, c, http.MethodGet, withQuery("/api/v1/events", q), nil, "event list")
}

// EventStreamFilter narrows StreamEvents. After is where the stream resumes
// (exclusive); zero means the server picks the current head, so a bare follow
// shows only what happens next.
type EventStreamFilter struct {
	Type  string
	After int64
}

// maxSSELine caps one line of the stream. An event's payload is a webhook
// body, which can legitimately be large, so this is generous — but it is a
// bound, because a server that never sends a newline must not grow the
// client's buffer without limit.
const maxSSELine = 4 << 20

// maxAPIErrBody caps how much of a refused stream's body is read for its
// error message. do reads whole bodies because they are bounded responses;
// this one is a stream, and a refusal is a small JSON object.
const maxAPIErrBody = 64 << 10

// ErrStreamEnded reports that the server closed the stream. A follow is meant
// to last, so this is never success — and because reconnecting is not
// implemented yet, it is the only thing that tells a caller its view of the
// log has stopped advancing. Without it a server restart is indistinguishable
// from a clean stop.
var ErrStreamEnded = errors.New("event stream closed by the server")

// StreamEvents follows GET /api/v1/events/stream, calling fn once per event
// until the context is cancelled (returning the context's error), the server
// closes the stream (ErrStreamEnded), or fn returns an error (returned
// unchanged, so a caller can stop cleanly).
//
// A dropped connection is returned, not retried: reconnecting means deciding
// what to do about the gap, and the server already has the mechanism for that
// (Last-Event-ID). docs/follow-ups.md records it.
func (c *Client) StreamEvents(ctx context.Context, f EventStreamFilter, fn func(model.Event) error) error {
	q := url.Values{}
	if f.Type != "" {
		q.Set("type", f.Type)
	}
	if f.After != 0 {
		q.Set("after", strconv.FormatInt(f.After, 10))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+withQuery("/api/v1/events/stream", q), nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	// c.http caps every request at 30s, which is a correct default for a
	// request/response call and fatal for one meant to stay open. The context
	// is what ends this one.
	resp, err := (&http.Client{Transport: c.http.Transport}).Do(req)
	if err != nil {
		return fmt.Errorf("GET /api/v1/events/stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxAPIErrBody))
		return apiError(resp.StatusCode, body)
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), maxSSELine)
	var data []byte
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			// End of one message. id: and event: are deliberately not kept:
			// both are also fields of the JSON, and one parse is better than
			// two that can disagree.
			if len(data) == 0 {
				continue
			}
			var e model.Event
			if err := json.Unmarshal(data, &e); err != nil {
				return fmt.Errorf("decode streamed event %q: %w", data, err)
			}
			data = data[:0]
			if err := fn(e); err != nil {
				return err
			}
		case strings.HasPrefix(line, ":"):
			// Comment line: the server's heartbeat.
		case strings.HasPrefix(line, "data:"):
			// The spec joins repeated data: lines with a newline, which is
			// how a value containing one is transmitted at all. The server
			// emits a single line per message today, so this is latent —
			// but a parser that concatenates instead would corrupt the first
			// message that isn't, silently.
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:"))...)
		}
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("read event stream: %w", err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrStreamEnded
}

// EventSubscribers calls GET /api/v1/event-subscribers.
func (c *Client) EventSubscribers(ctx context.Context) (model.EventSubscriberListResponse, []byte, error) {
	return doJSON[model.EventSubscriberListResponse](ctx, c, http.MethodGet, "/api/v1/event-subscribers", nil, "event subscriber list")
}

// SeekEventSubscriber calls POST /api/v1/event-subscribers/{name}/seek,
// moving both of the subscriber's offsets to to — an admin correction of
// consumer state (025 §18), safe only because handlers are idempotent.
func (c *Client) SeekEventSubscriber(ctx context.Context, name string, to int64) (model.EventSubscriberStatus, []byte, error) {
	return doJSON[model.EventSubscriberStatus](ctx, c, http.MethodPost, "/api/v1/event-subscribers/"+url.PathEscape(name)+"/seek", model.EventSubscriberSeekRequest{To: to}, "event subscriber status")
}

// EventTable prints one row per event, newest last (025 §18): id, received
// time, source, type, external id. The `lode event tail` view.
func EventTable(w io.Writer, events []model.Event) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "ID\tRECEIVED\tSOURCE\tTYPE\tEXTERNAL_ID")
	for _, e := range events {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", e.ID, LocalTime(e.ReceivedAt), e.Source, e.Type, e.ExternalID)
	}
	tw.Flush()
}

// eventStreamRowFmt lays out one `lode event tail --follow` row. Fixed
// widths rather than a tabwriter: a stream has no complete row set to
// measure, and re-measuring per row would make the columns jitter as events
// arrive.
const eventStreamRowFmt = "%-8v  %-20v  %-10v  %-28v  %v\n"

// EventStreamHeader prints the follow view's column header, once.
func EventStreamHeader(w io.Writer) {
	fmt.Fprintf(w, eventStreamRowFmt, "ID", "RECEIVED", "SOURCE", "TYPE", "EXTERNAL_ID")
}

// EventStreamRow prints one streamed event in EventTable's column order.
func EventStreamRow(w io.Writer, e model.Event) {
	fmt.Fprintf(w, eventStreamRowFmt, e.ID, LocalTime(e.ReceivedAt), e.Source, e.Type, e.ExternalID)
}

// EventSubscriberTable prints one row per subscriber: name, offsets, lag,
// lock holder pid (- when unheld), last updated. The `lode event
// subscribers` view.
func EventSubscriberTable(w io.Writer, subs []model.EventSubscriberStatus) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "NAME\tREAD\tACKED\tLAG\tHOLDER\tUPDATED")
	for _, s := range subs {
		holder := "-"
		if s.HolderPID != 0 {
			holder = strconv.FormatInt(s.HolderPID, 10)
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\t%s\n",
			s.Name, s.LastReadOffset, s.LastAckedOffset, s.Lag, holder, LocalTime(s.UpdatedAt))
	}
	tw.Flush()
}
