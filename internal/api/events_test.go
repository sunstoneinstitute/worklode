package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// pollEvents polls GET /api/v1/events{query} until it returns at least want
// events, or fails the test. The commit horizon (pg_snapshot_xmin) is
// cluster-wide, not per-test-database (internal/store/events_test.go's
// pollReadEventBatch documents this at length): a concurrent transaction in
// another package's test binary sharing this Postgres instance can hold a
// single read back to fewer events than were actually committed. Once a
// query has observed an id, later queries always see it too — the horizon
// only advances — so only the first read against a freshly seeded id needs
// to poll.
func pollEvents(t *testing.T, h http.Handler, token, query string, want int) []any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rr := doReq(t, h, "GET", "/api/v1/events"+query, token, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/events%s status = %d, body %s", query, rr.Code, rr.Body.String())
		}
		events, _ := decodeMap(t, rr)["events"].([]any)
		if len(events) >= want {
			return events
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET /api/v1/events%s: got %d events after polling, want %d "+
				"(commit horizon held back by a concurrent transaction elsewhere on the instance?)",
				query, len(events), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestListEventsFilters exercises the query-string surface of GET
// /api/v1/events: type, since, after and limit, plus the newest-last
// ordering (025 §18). Any authenticated actor may read it.
func TestListEventsFilters(t *testing.T) {
	st, h, token := newTestServer(t)

	seedEvent(t, st, "le-1", func(tx *sql.Tx, _ int64) error { return nil })
	seedEvent(t, st, "le-2", func(tx *sql.Tx, _ int64) error { return nil })

	// Filtered on our own seeded type from the start: newTestServer records
	// nothing of its own today, but this test's correctness must not depend
	// on that staying true.
	events := pollEvents(t, h, token, "?type=test.seed", 2)
	first := events[0].(map[string]any)
	second := events[1].(map[string]any)
	if first["external_id"] != "le-1" || second["external_id"] != "le-2" {
		t.Fatalf("events order = %v, want le-1 then le-2 (newest last)", events)
	}
	for _, k := range []string{"id", "source", "type", "received_at"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("event missing field %q: %v", k, first)
		}
	}

	// Both seeded rows are already known visible above, so a single
	// unpolled read is safe here and below.
	rr := doReq(t, h, "GET", "/api/v1/events?type=nope.event", token, nil)
	events = decodeMap(t, rr)["events"].([]any)
	if len(events) != 0 {
		t.Fatalf("non-matching type filter events = %v, want 0", events)
	}

	// after filter: exclusive cursor on the first event's id.
	firstID := int64(first["id"].(float64))
	rr = doReq(t, h, "GET", fmt.Sprintf("/api/v1/events?after=%d", firstID), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("after filter status = %d, body %s", rr.Code, rr.Body.String())
	}
	events = decodeMap(t, rr)["events"].([]any)
	if len(events) != 1 || events[0].(map[string]any)["external_id"] != "le-2" {
		t.Fatalf("after filter events = %v, want just le-2", events)
	}

	// since filter: a future timestamp excludes everything seeded so far.
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	rr = doReq(t, h, "GET", "/api/v1/events?since="+future, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("since filter status = %d, body %s", rr.Code, rr.Body.String())
	}
	events = decodeMap(t, rr)["events"].([]any)
	if len(events) != 0 {
		t.Fatalf("future since filter events = %v, want 0", events)
	}

	// limit.
	rr = doReq(t, h, "GET", "/api/v1/events?limit=1", token, nil)
	events = decodeMap(t, rr)["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("limit=1 events = %v, want 1", events)
	}

	// invalid query values are rejected, not silently ignored — including a
	// negative or zero limit, which strconv.Atoi parses without error but
	// which ListEvents' default/cap logic would otherwise silently clamp to
	// a full page instead of signalling the mistake.
	for _, q := range []string{"since=not-a-time", "after=not-an-int", "limit=not-an-int", "limit=0", "limit=-1"} {
		rr = doReq(t, h, "GET", "/api/v1/events?"+q, token, nil)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("GET /api/v1/events?%s status = %d, want 422; body %s", q, rr.Code, rr.Body.String())
		}
	}
}

// TestListEventSubscribers covers GET /api/v1/event-subscribers: lag and
// holder_pid alongside the durable offsets, and that any authenticated actor
// may read it.
func TestListEventSubscribers(t *testing.T) {
	st, h, token := newTestServer(t)
	ctx := context.Background()
	if err := st.EnsureEventSubscriber(ctx, "les-sub"); err != nil {
		t.Fatal(err)
	}
	seedEvent(t, st, "les-1", func(tx *sql.Tx, _ int64) error { return nil })

	rr := doReq(t, h, "GET", "/api/v1/event-subscribers", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list subscribers status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	subs, ok := got["subscribers"].([]any)
	if !ok {
		t.Fatalf("subscribers not an array: %s", rr.Body.String())
	}
	var found map[string]any
	for _, raw := range subs {
		sub := raw.(map[string]any)
		if sub["name"] == "les-sub" {
			found = sub
		}
	}
	if found == nil {
		t.Fatalf("subscribers = %v, want les-sub present", subs)
	}
	for _, k := range []string{"last_read_offset", "last_acked_offset", "lag", "holder_pid", "updated_at"} {
		if _, ok := found[k]; !ok {
			t.Fatalf("subscriber missing field %q: %v", k, found)
		}
	}
	if found["holder_pid"] != float64(0) {
		t.Fatalf("holder_pid = %v, want 0 (unlocked)", found["holder_pid"])
	}
}

// TestSeekEventSubscriber covers POST
// /api/v1/event-subscribers/{name}/seek: admin-only, 404 on an unknown
// name, and 200 with the updated row on success.
func TestSeekEventSubscriber(t *testing.T) {
	st, h, adminToken := newTestServer(t)
	ctx := context.Background()
	workerToken := seedActor(t, st, "worker", "agent", "Worker", false)
	if err := st.EnsureEventSubscriber(ctx, "seek-api-sub"); err != nil {
		t.Fatal(err)
	}

	// Non-admin is refused.
	rr := doReq(t, h, "POST", "/api/v1/event-subscribers/seek-api-sub/seek", workerToken, map[string]any{"to": 0})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin seek status = %d, want 403; body %s", rr.Code, rr.Body.String())
	}

	// Unknown subscriber name 404s for an admin.
	rr = doReq(t, h, "POST", "/api/v1/event-subscribers/no-such-sub/seek", adminToken, map[string]any{"to": 0})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown subscriber seek status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}

	// Admin seek succeeds and returns the updated row.
	rr = doReq(t, h, "POST", "/api/v1/event-subscribers/seek-api-sub/seek", adminToken, map[string]any{"to": 5})
	if rr.Code != http.StatusOK {
		t.Fatalf("admin seek status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["name"] != "seek-api-sub" {
		t.Fatalf("seek response = %v, want name=seek-api-sub", got)
	}
	if got["last_read_offset"] != float64(5) || got["last_acked_offset"] != float64(5) {
		t.Fatalf("seek response offsets = %v, want both 5", got)
	}

	subs, err := st.EventSubscribers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range subs {
		if s.Name == "seek-api-sub" {
			found = true
			if s.LastRead != 5 || s.LastAcked != 5 {
				t.Fatalf("store offsets after seek = (read=%d, acked=%d), want both 5", s.LastRead, s.LastAcked)
			}
		}
	}
	if !found {
		t.Fatalf("subscriber seek-api-sub not found after seek")
	}
}
