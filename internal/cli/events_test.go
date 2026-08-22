package cli_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// pollClientEvents polls ListEvents(f) until it returns at least want
// events, or fails the test — the same cluster-wide-commit-horizon
// accommodation as pollEvents in internal/api/events_test.go and
// pollListEvents in internal/store/events_test.go: a concurrent transaction
// in another package's test binary sharing this Postgres instance (e.g.
// internal/store's TestListEventsHonoursCommitHorizon, which deliberately
// holds one open) can hold pg_snapshot_xmin back regardless of what this
// test itself committed. Once a query has observed an id the horizon can
// only advance, so callers only need this for the first read against a
// freshly recorded id.
func pollClientEvents(t *testing.T, ctx context.Context, c *cli.Client, f cli.EventListFilter, want int) []model.Event {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, _, err := c.ListEvents(ctx, f)
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(resp.Events) >= want {
			return resp.Events
		}
		if time.Now().After(deadline) {
			t.Fatalf("ListEvents: got %d events after polling, want %d "+
				"(commit horizon held back by a concurrent transaction elsewhere on the instance?)",
				len(resp.Events), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestClientEvents covers ListEvents, EventSubscribers and
// SeekEventSubscriber end to end against a real server, including the
// filter query string and the seek round-trip.
func TestClientEvents(t *testing.T) {
	st, c, _ := newTestServer(t)
	ctx := context.Background()

	if err := st.EnsureEventSubscriber(ctx, "cli-sub"); err != nil {
		t.Fatalf("EnsureEventSubscriber: %v", err)
	}
	var firstID int64
	for i, extID := range []string{"ce-1", "ce-2"} {
		id, _, err := st.RecordEvent(ctx, "system", extID, "test.event", nil, nil)
		if err != nil {
			t.Fatalf("RecordEvent %s: %v", extID, err)
		}
		if i == 0 {
			firstID = id
		}
	}

	if events := pollClientEvents(t, ctx, c, cli.EventListFilter{Type: "test.event"}, 2); len(events) != 2 ||
		events[0].ExternalID != "ce-1" || events[1].ExternalID != "ce-2" {
		t.Fatalf("ListEvents = %+v, want [ce-1 ce-2] in id order", events)
	}

	// Both ids are now known visible past the commit horizon, so a single
	// unpolled read is safe here and for the raw-body check below.
	resp, raw, err := c.ListEvents(ctx, cli.EventListFilter{Type: "test.event"})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("ListEvents: raw body empty")
	}
	if len(resp.Events) != 2 || resp.Events[0].ExternalID != "ce-1" || resp.Events[1].ExternalID != "ce-2" {
		t.Fatalf("ListEvents = %+v, want [ce-1 ce-2] in id order", resp.Events)
	}

	resp, _, err = c.ListEvents(ctx, cli.EventListFilter{Type: "test.event", After: firstID})
	if err != nil {
		t.Fatalf("ListEvents after: %v", err)
	}
	if len(resp.Events) != 1 || resp.Events[0].ExternalID != "ce-2" {
		t.Fatalf("ListEvents after = %+v, want just ce-2", resp.Events)
	}

	subResp, raw, err := c.EventSubscribers(ctx)
	if err != nil {
		t.Fatalf("EventSubscribers: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("EventSubscribers: raw body empty")
	}
	var found bool
	for _, s := range subResp.Subscribers {
		if s.Name == "cli-sub" {
			found = true
			if s.HolderPID != 0 {
				t.Fatalf("cli-sub holder_pid = %d, want 0 (unlocked)", s.HolderPID)
			}
		}
	}
	if !found {
		t.Fatalf("EventSubscribers = %+v, want cli-sub present", subResp.Subscribers)
	}

	status, raw, err := c.SeekEventSubscriber(ctx, "cli-sub", firstID)
	if err != nil {
		t.Fatalf("SeekEventSubscriber: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("SeekEventSubscriber: raw body empty")
	}
	if status.Name != "cli-sub" || status.LastReadOffset != firstID || status.LastAckedOffset != firstID {
		t.Fatalf("SeekEventSubscriber = %+v, want offsets = %d", status, firstID)
	}

	if _, _, err := c.SeekEventSubscriber(ctx, "no-such-sub", 0); err == nil {
		t.Fatal("SeekEventSubscriber(unknown name): want error, got nil")
	}
}

// TestClientStreamEvents covers StreamEvents against a real server: the
// resume cursor is exclusive, the JSON body of each message decodes into the
// same Event the bounded list returns, a callback error stops the stream and
// is returned unchanged, and a non-admin token is refused as a ClientError
// rather than as an empty stream.
func TestClientStreamEvents(t *testing.T) {
	st, c, serverURL := newTestServer(t)
	ctx := context.Background()

	var ids []int64
	for i := 1; i <= 3; i++ {
		id, _, err := st.RecordEvent(ctx, "system", fmt.Sprintf("cs-%d", i), "cli.stream",
			[]byte(`{"n":`+fmt.Sprint(i)+`}`), nil)
		if err != nil {
			t.Fatalf("RecordEvent cs-%d: %v", i, err)
		}
		ids = append(ids, id)
	}
	// The commit horizon is cluster-wide; wait until all three are readable
	// before asserting on which of them a resume replays.
	pollClientEvents(t, ctx, c, cli.EventListFilter{Type: "cli.stream"}, 3)

	streamCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	errEnough := errors.New("enough")
	var got []model.Event
	err := c.StreamEvents(streamCtx, cli.EventStreamFilter{Type: "cli.stream", After: ids[0]},
		func(e model.Event) error {
			got = append(got, e)
			if len(got) == 2 {
				return errEnough
			}
			return nil
		})
	if !errors.Is(err, errEnough) {
		t.Fatalf("StreamEvents = %v, want the callback's own error back", err)
	}
	if len(got) != 2 || got[0].ID != ids[1] || got[1].ID != ids[2] {
		t.Fatalf("streamed events = %+v, want ids %d then %d (resume is exclusive)", got, ids[1], ids[2])
	}
	if got[0].ExternalID != "cs-2" || got[0].Type != "cli.stream" {
		t.Fatalf("first streamed event = %+v, want cs-2 of type cli.stream", got[0])
	}
	if string(got[1].Payload) != `{"n":3}` {
		t.Fatalf("streamed payload = %s, want {\"n\":3}", got[1].Payload)
	}

	// The stream is admin-only even though the bounded list beside it is not.
	if err := st.CreateActor(ctx, "streamworker", "agent", "Worker", false); err != nil {
		t.Fatalf("create worker: %v", err)
	}
	workerToken, err := st.CreateToken(ctx, "streamworker", "worker token", nil)
	if err != nil {
		t.Fatalf("create worker token: %v", err)
	}
	wc := cli.NewClient(cli.Config{ServerURL: serverURL, Token: workerToken})
	err = wc.StreamEvents(streamCtx, cli.EventStreamFilter{Type: "cli.stream"},
		func(model.Event) error { return errors.New("callback must not run") })
	var ce *cli.ClientError
	if !errors.As(err, &ce) || ce.Status != http.StatusForbidden {
		t.Fatalf("non-admin StreamEvents = %v, want a 403 ClientError", err)
	}
}

// TestClientStreamEventsFraming covers the two parts of SSE framing the real
// server does not currently exercise: a message split across several data:
// lines (the spec joins them with a newline, and a payload that ever grows a
// literal newline would otherwise silently lose it), and a stream the server
// ends on its own. The latter must be distinguishable from a clean stop —
// with reconnect deferred, a server restart that looked like success would
// leave `lode event tail --follow` exiting 0 in silence.
func TestClientStreamEventsFraming(t *testing.T) {
	body := "id: 1\nevent: t.one\n" +
		"data: {\"id\":1,\"type\":\"t.one\",\n" +
		"data: \"external_id\":\"multi\"}\n\n" +
		":\n\n" +
		"id: 2\nevent: t.two\ndata: {\"id\":2,\"type\":\"t.two\"}\n\n"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, body)
	}))
	t.Cleanup(ts.Close)

	c := cli.NewClient(cli.Config{ServerURL: ts.URL, Token: "wl_" + strings.Repeat("a", 40)})
	var got []model.Event
	err := c.StreamEvents(context.Background(), cli.EventStreamFilter{}, func(e model.Event) error {
		got = append(got, e)
		return nil
	})
	if !errors.Is(err, cli.ErrStreamEnded) {
		t.Fatalf("StreamEvents on a server-closed stream = %v, want ErrStreamEnded", err)
	}
	if len(got) != 2 {
		t.Fatalf("events = %+v, want 2", got)
	}
	if got[0].ID != 1 || got[0].ExternalID != "multi" {
		t.Fatalf("first event = %+v, want id 1 with external_id multi (data: lines joined by a newline)", got[0])
	}
	if got[1].ID != 2 || got[1].Type != "t.two" {
		t.Fatalf("second event = %+v, want id 2 of type t.two", got[1])
	}
}
