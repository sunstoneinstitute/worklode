package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	return OpenTestStore(t)
}

// pollReadEventBatch retries ReadEventBatch(name, limit), accumulating
// results across calls (each resumes from the offset the previous one
// advanced to), until it has at least want events or the timeout elapses.
// The commit horizon (pg_snapshot_xmin) is cluster-wide, not per-test
// database: a concurrent transaction in another test binary sharing this
// Postgres instance can hold a single read back to zero events even after
// the events under test have committed. Polling absorbs that operational
// hazard without weakening the assertion — a genuinely broken horizon
// predicate (e.g. a bare id > last_seen) delivers the wrong events or the
// wrong order, not merely late ones, so it still fails here.
func pollReadEventBatch(t *testing.T, ctx context.Context, s *Store, name string, limit, want int) []Event {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var all []Event
	for {
		got, err := s.ReadEventBatch(ctx, name, limit)
		if err != nil {
			t.Fatalf("ReadEventBatch(%s): %v", name, err)
		}
		all = append(all, got...)
		if len(all) >= want {
			return all
		}
		if time.Now().After(deadline) {
			t.Fatalf("ReadEventBatch(%s): got %d events after polling, want %d "+
				"(commit horizon held back by a concurrent transaction elsewhere on the instance?)",
				name, len(all), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRecordEventIdempotent(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	applyCalls := 0
	apply := func(tx *sql.Tx, eventID int64) error {
		applyCalls++
		return nil
	}

	id1, inserted1, err := s.RecordEvent(ctx, "github", "d1", "push", nil, apply)
	if err != nil {
		t.Fatalf("first RecordEvent: %v", err)
	}
	if !inserted1 {
		t.Fatalf("first RecordEvent: want inserted=true, got false")
	}
	if applyCalls != 1 {
		t.Fatalf("first RecordEvent: want apply called once, got %d", applyCalls)
	}

	id2, inserted2, err := s.RecordEvent(ctx, "github", "d1", "push", nil, apply)
	if err != nil {
		t.Fatalf("second RecordEvent: %v", err)
	}
	if inserted2 {
		t.Fatalf("second RecordEvent: want inserted=false, got true")
	}
	if id2 != id1 {
		t.Fatalf("second RecordEvent: want same id %d, got %d", id1, id2)
	}
	if applyCalls != 1 {
		t.Fatalf("second RecordEvent: apply must not be called on replay, but was called %d times total", applyCalls)
	}
}

func TestRecordEventAppliesStateInSameTx(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	boom := errors.New("apply failed")
	apply := func(tx *sql.Tx, eventID int64) error {
		return boom
	}

	_, _, err := s.RecordEvent(ctx, "github", "d2", "push", nil, apply)
	if err == nil {
		t.Fatalf("RecordEvent: want error from apply, got nil")
	}

	var count int
	row := s.db.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE source = $1 AND external_id = $2`, "github", "d2")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("query events: %v", err)
	}
	if count != 0 {
		t.Fatalf("want event row rolled back, but found %d rows", count)
	}
}

func TestLogChange(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	change := map[string]any{"field": "state", "old": "ready", "new": "in_progress"}
	apply := func(tx *sql.Tx, eventID int64) error {
		return LogChange(tx, "task", "WL-1", eventID, change)
	}

	eventID, inserted, err := s.RecordEvent(ctx, "cli", "c1", "transition", nil, apply)
	if err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}
	if !inserted {
		t.Fatalf("RecordEvent: want inserted=true")
	}

	var entityKind, entityID, changeJSON string
	var gotEventID int64
	row := s.db.QueryRowContext(ctx, `SELECT entity_kind, entity_id, change, event_id FROM state_log`)
	if err := row.Scan(&entityKind, &entityID, &changeJSON, &gotEventID); err != nil {
		t.Fatalf("query state_log: %v", err)
	}

	if entityKind != "task" || entityID != "WL-1" {
		t.Fatalf("state_log entity: got (%q, %q)", entityKind, entityID)
	}
	if gotEventID != eventID {
		t.Fatalf("state_log event_id: want %d, got %d", eventID, gotEventID)
	}

	var gotChange map[string]any
	if err := json.Unmarshal([]byte(changeJSON), &gotChange); err != nil {
		t.Fatalf("unmarshal change: %v", err)
	}
	if gotChange["field"] != "state" || gotChange["old"] != "ready" || gotChange["new"] != "in_progress" {
		t.Fatalf("state_log change: got %v", gotChange)
	}
}

func TestRecordEventWithID(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	payloadFor := func(eventID int64) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"@id":"wlid:event/%d"}`, eventID)), nil
	}

	id1, inserted1, err := s.RecordEventWithID(ctx, "cli", "wl:Test:wlid:doc/x:1", "wl:Test", payloadFor, nil)
	if err != nil {
		t.Fatalf("first RecordEventWithID: %v", err)
	}
	if !inserted1 {
		t.Fatalf("first RecordEventWithID: want inserted=true, got false")
	}

	var payload1 []byte
	if err := s.db.QueryRowContext(ctx, `SELECT payload FROM events WHERE id = $1`, id1).Scan(&payload1); err != nil {
		t.Fatalf("query payload: %v", err)
	}
	var decoded1 map[string]any
	if err := json.Unmarshal(payload1, &decoded1); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	wantID := fmt.Sprintf("wlid:event/%d", id1)
	if decoded1["@id"] != wantID {
		t.Fatalf("payload @id: got %v, want %v", decoded1["@id"], wantID)
	}

	id2, inserted2, err := s.RecordEventWithID(ctx, "cli", "wl:Test:wlid:doc/x:1", "wl:Test", payloadFor, nil)
	if err != nil {
		t.Fatalf("second RecordEventWithID: %v", err)
	}
	if inserted2 {
		t.Fatalf("second RecordEventWithID: want inserted=false, got true")
	}
	if id2 != id1 {
		t.Fatalf("second RecordEventWithID: want same id %d, got %d", id1, id2)
	}

	var payload2 []byte
	if err := s.db.QueryRowContext(ctx, `SELECT payload FROM events WHERE id = $1`, id2).Scan(&payload2); err != nil {
		t.Fatalf("query payload: %v", err)
	}
	if string(payload2) != string(payload1) {
		t.Fatalf("payload changed on replay: got %s, want %s", payload2, payload1)
	}

	if _, _, err := s.RecordEventWithID(ctx, "cli", "wl:Test:wlid:doc/x:2", "wl:Test", nil, nil); err == nil {
		t.Fatalf("RecordEventWithID with nil payloadFor: want error, got nil")
	}
}

func TestGetEvent(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	id, _, err := s.RecordEvent(ctx, "cli", "e1", "test.event", []byte(`{"a":1}`), nil)
	if err != nil {
		t.Fatal(err)
	}

	e, err := s.GetEvent(ctx, id)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if e.ID != id || e.Source != "cli" || e.ExternalID != "e1" || e.Type != "test.event" {
		t.Fatalf("GetEvent: got %+v", e)
	}

	if _, err := s.GetEvent(ctx, id+1_000_000); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetEvent unknown id: want ErrNotFound, got %v", err)
	}
}

// The 025 §15 ordering trap, directly. A last_seen_id cursor sees B (id 2),
// advances, and never delivers A (id 1). The horizon delivers neither
// until A's transaction is finished, then both, in id order.
func TestReadEventBatchHonoursCommitHorizon(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.EnsureEventSubscriber(ctx, "sub"); err != nil {
		t.Fatal(err)
	}

	insert := func(tx *sql.Tx, ext string) {
		t.Helper()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO events (source, external_id, type, payload, received_at)
			 VALUES ('system', $1, 'test.event', '{}', now())`, ext); err != nil {
			t.Fatal(err)
		}
	}

	txA, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer txA.Rollback()
	insert(txA, "slow") // id 1, uncommitted

	txB, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	insert(txB, "fast") // id 2
	if err := txB.Commit(); err != nil {
		t.Fatal(err)
	}

	got, err := s.ReadEventBatch(ctx, "sub", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("delivered %d events while txA in flight, want 0", len(got))
	}

	if err := txA.Commit(); err != nil {
		t.Fatal(err)
	}
	got = pollReadEventBatch(t, ctx, s, "sub", 10, 2)
	if len(got) != 2 || got[0].ExternalID != "slow" || got[1].ExternalID != "fast" {
		t.Fatalf("got %+v, want slow then fast", got)
	}
}

// An aborted transaction leaves a permanent hole in the id sequence (the
// IDENTITY column does not roll back). ReadEventBatch must step past it
// rather than stalling forever waiting for an id that will never commit.
func TestReadEventBatchSkipsAbortedHole(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.EnsureEventSubscriber(ctx, "sub"); err != nil {
		t.Fatal(err)
	}

	txA, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := txA.ExecContext(ctx,
		`INSERT INTO events (source, external_id, type, payload, received_at)
		 VALUES ('system', 'aborted', 'test.event', '{}', now())`); err != nil {
		t.Fatal(err)
	}
	if err := txA.Rollback(); err != nil {
		t.Fatal(err)
	}

	id, _, err := s.RecordEvent(ctx, "system", "committed", "test.event", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := pollReadEventBatch(t, ctx, s, "sub", 10, 1)
	if len(got) != 1 || got[0].ID != id || got[0].ExternalID != "committed" {
		t.Fatalf("got %+v, want just the committed event", got)
	}

	got, err = s.ReadEventBatch(ctx, "sub", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("second read delivered %d events, want 0 (offset already past the hole)", len(got))
	}
}

// An ack against a subscriber that does not exist is ErrNotFound, not a
// silent success: a consumer whose row was deleted underneath it would
// otherwise keep believing its acks were landing. The no-op lower ack in
// TestAckEventsMonotonic affects zero rows too, and must stay a nil error —
// the two cases are only distinguishable inside the statement.
func TestAckEventsUnknownSubscriber(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.AckEvents(ctx, "no-such-subscriber", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AckEvents on an unknown subscriber = %v, want ErrNotFound", err)
	}

	// Deleted mid-flight: the same case a running consumer would hit.
	if err := s.EnsureEventSubscriber(ctx, "gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM event_subscribers WHERE name = $1`, "gone"); err != nil {
		t.Fatal(err)
	}
	if err := s.AckEvents(ctx, "gone", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AckEvents after the row was deleted = %v, want ErrNotFound", err)
	}
}

func TestAckEventsMonotonic(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.EnsureEventSubscriber(ctx, "sub"); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if _, _, err := s.RecordEvent(ctx, "system", "e"+string(rune('1'+i)), "test.event", nil, nil); err != nil {
			t.Fatal(err)
		}
	}

	got := pollReadEventBatch(t, ctx, s, "sub", 3, 3)
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}

	if err := s.AckEvents(ctx, "sub", 3); err != nil {
		t.Fatalf("ack 3: %v", err)
	}
	if err := s.AckEvents(ctx, "sub", 2); err != nil {
		t.Fatalf("ack 2 (lower, replayed): want nil error, got %v", err)
	}

	subs, err := s.EventSubscribers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sub EventSubscriber
	for _, es := range subs {
		if es.Name == "sub" {
			sub = es
		}
	}
	if sub.LastAcked != 3 {
		t.Fatalf("last_acked_offset: want 3 (replayed lower ack must not regress it), got %d", sub.LastAcked)
	}

	if err := s.AckEvents(ctx, "sub", 99); err == nil {
		t.Fatalf("ack 99 (beyond last_read_offset): want error, got nil")
	}

	subs, err = s.EventSubscribers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, es := range subs {
		if es.Name == "sub" {
			sub = es
		}
	}
	if sub.LastAcked != 3 || sub.LastRead != 3 {
		t.Fatalf("offsets after rejected ack: want (read=3, acked=3), got (read=%d, acked=%d)", sub.LastRead, sub.LastAcked)
	}
}

func TestSubscriberLockExclusive(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	l1, ok, err := s.TryLockSubscriber(ctx, "doc-lifecycle")
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	_, ok, err = s.TryLockSubscriber(ctx, "doc-lifecycle")
	if err != nil || ok {
		t.Fatalf("second lock while held: ok=%v err=%v, want false", ok, err)
	}
	// A different subscriber name is a different lock.
	l2, ok, err := s.TryLockSubscriber(ctx, "other")
	if err != nil || !ok {
		t.Fatalf("other name: ok=%v err=%v", ok, err)
	}
	l2.Release(ctx)

	if err := l1.Release(ctx); err != nil {
		t.Fatal(err)
	}
	l3, ok, err := s.TryLockSubscriber(ctx, "doc-lifecycle")
	if err != nil || !ok {
		t.Fatalf("after release: ok=%v err=%v, want acquired", ok, err)
	}
	l3.Release(ctx)
}

// advisoryLockHolderPID returns the backend pid holding the wl:subscriber
// advisory lock for name in the current database, or 0 if unlocked. Splits
// the 64-bit key the way Postgres itself does for pg_locks (classid = high
// 32 bits, objid = low 32 bits, objsubid = 1 for the single-bigint lock
// form) — Task 7's subscriber-status view joins pg_locks the same way, so
// this exercises that exact join.
func advisoryLockHolderPID(t *testing.T, ctx context.Context, s *Store, name string) int {
	t.Helper()
	var pid int
	err := s.db.QueryRowContext(ctx, `
		SELECT pid FROM pg_locks
		 WHERE locktype = 'advisory'
		   AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
		   AND classid = ((hashtext('wl:subscriber:' || $1)::bigint >> 32) & 4294967295)::oid
		   AND objid = (hashtext('wl:subscriber:' || $1)::bigint & 4294967295)::oid
		   AND objsubid = 1
		   AND granted`, name).Scan(&pid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("query advisory lock holder for %s: %v", name, err)
	}
	return pid
}

// The lock's connection must be pinned out of the pool for the consumer's
// lifetime, surviving genuine pool churn: concurrent use that forces
// database/sql to open new connections and evict idle ones past
// MaxIdleConns(4) (store.go), not a run of sequential queries that all
// reuse one idle connection. The assertion also has to be direct — a
// second TryLockSubscriber failing does not prove the lock stayed pinned,
// since any other session's pg_try_advisory_lock fails regardless of who
// holds it — so this checks the actual holder in pg_locks is the same
// backend pid before and after the churn.
func TestSubscriberLockSurvivesPoolChurn(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	l1, ok, err := s.TryLockSubscriber(ctx, "doc-lifecycle")
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	defer l1.Release(ctx)

	holder := advisoryLockHolderPID(t, ctx, s, "doc-lifecycle")
	if holder == 0 {
		t.Fatalf("advisory lock not visible in pg_locks after acquiring")
	}

	// 12 concurrent holders, within MaxOpenConns(16) minus the one pinned
	// to the lock, well past MaxIdleConns(4): forces the pool to open new
	// connections and, once they return, evict the excess idle ones.
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := s.db.Conn(ctx)
			if err != nil {
				t.Errorf("churn conn: %v", err)
				return
			}
			defer conn.Close()
			if _, err := conn.ExecContext(ctx, `SELECT pg_sleep(0.05)`); err != nil {
				t.Errorf("churn query: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := advisoryLockHolderPID(t, ctx, s, "doc-lifecycle"); got != holder {
		t.Fatalf("advisory lock holder pid changed across pool churn: was %d, now %d (lock connection was recycled)", holder, got)
	}

	_, ok, err = s.TryLockSubscriber(ctx, "doc-lifecycle")
	if err != nil || ok {
		t.Fatalf("lock after pool churn: ok=%v err=%v, want false (lock connection must stay pinned)", ok, err)
	}
}

// Release must discard the underlying session, not return it to the pool:
// a session-scoped advisory lock survives (*sql.Conn).Close() on a pooled
// connection, so an idle pooled connection would hold the lock forever.
// TestSubscriberLockExclusive alone cannot catch a missing
// Raw(driver.ErrBadConn) — re-acquiring on a recycled session is just a
// re-entrant grant to the same session, so it still reports ok=true either
// way. This checks the backend actually disconnects: its pg_stat_activity
// row must disappear, not merely go idle.
func TestSubscriberLockReleaseDiscardsSession(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	backendCount := func() int {
		t.Helper()
		var n int
		if err := s.db.QueryRowContext(ctx,
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid()`,
		).Scan(&n); err != nil {
			t.Fatalf("count pg_stat_activity: %v", err)
		}
		return n
	}

	before := backendCount()

	l, ok, err := s.TryLockSubscriber(ctx, "doc-lifecycle")
	if err != nil || !ok {
		t.Fatalf("lock: ok=%v err=%v", ok, err)
	}
	if got := backendCount(); got != before+1 {
		t.Fatalf("backend count after lock: got %d, want %d (one dedicated session)", got, before+1)
	}

	if err := l.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		got := backendCount()
		if got == before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("backend count after release: got %d, want back to %d — session was pooled, not discarded", got, before)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestResetEventReadRedeliversUnacked(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.EnsureEventSubscriber(ctx, "sub"); err != nil {
		t.Fatal(err)
	}

	var ids []int64
	for i := 0; i < 3; i++ {
		id, _, err := s.RecordEvent(ctx, "system", "e"+string(rune('1'+i)), "test.event", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	got := pollReadEventBatch(t, ctx, s, "sub", 3, 3)
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}

	if err := s.AckEvents(ctx, "sub", ids[0]); err != nil {
		t.Fatalf("ack first id: %v", err)
	}
	if err := s.ResetEventRead(ctx, "sub"); err != nil {
		t.Fatalf("reset event read: %v", err)
	}

	got = pollReadEventBatch(t, ctx, s, "sub", 10, 2)
	if len(got) != 2 || got[0].ID != ids[1] || got[1].ID != ids[2] {
		t.Fatalf("got %+v, want events 2 and 3 in order", got)
	}
}

// EventSubscriberLags reports, per subscriber, how far the horizon tail runs
// ahead of what that subscriber has acked (025 §15.7). The horizon term is
// shared, so an unconsumed subscriber lags by the whole log while a caught-up
// one lags by nothing.
func TestEventSubscriberLags(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for _, name := range []string{"docs", "watcher"} {
		if err := s.EnsureEventSubscriber(ctx, name); err != nil {
			t.Fatal(err)
		}
	}

	var ids []int64
	for i := 0; i < 3; i++ {
		id, _, err := s.RecordEvent(ctx, "system", fmt.Sprintf("lag-%d", i), "test.event", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	// Poll: the horizon is cluster-wide, so the tail reaches the last id only
	// once every concurrent transaction on the instance has drained.
	lagsFor := func() map[string]int64 {
		lags, err := s.EventSubscriberLags(ctx)
		if err != nil {
			t.Fatalf("EventSubscriberLags: %v", err)
		}
		out := make(map[string]int64, len(lags))
		for _, l := range lags {
			out[l.Name] = l.Lag
		}
		return out
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := lagsFor()
		if got["docs"] == 3 && got["watcher"] == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lags = %v, want 3 for both (horizon held back by a concurrent transaction?)", got)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Reading without acking does not reduce the lag; acking does.
	if _, err := s.ReadEventBatch(ctx, "docs", 10); err != nil {
		t.Fatalf("ReadEventBatch: %v", err)
	}
	if got := lagsFor(); got["docs"] != 3 {
		t.Fatalf("lag after read without ack = %d, want 3", got["docs"])
	}
	if err := s.AckEvents(ctx, "docs", ids[2]); err != nil {
		t.Fatalf("AckEvents: %v", err)
	}
	got := lagsFor()
	if got["docs"] != 0 {
		t.Fatalf("lag after acking everything = %d, want 0", got["docs"])
	}
	if got["watcher"] != 3 {
		t.Fatalf("other subscriber's lag = %d, want 3 (unaffected)", got["watcher"])
	}
}

// pollListEvents retries ListEvents(f) until it has at least want events or
// the timeout elapses, for the same cluster-wide-horizon reason as
// pollReadEventBatch above.
func pollListEvents(t *testing.T, ctx context.Context, s *Store, f EventFilter, want int) []Event {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := s.ListEvents(ctx, f)
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(got) >= want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("ListEvents: got %d events after polling, want %d "+
				"(commit horizon held back by a concurrent transaction elsewhere on the instance?)",
				len(got), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestListEventsHonoursCommitHorizon is the 025 §15 ordering trap again, this
// time against ListEvents directly rather than through a subscriber's
// last_read_offset: an in-flight transaction's row must not appear even
// though a later-committed row already has.
func TestListEventsHonoursCommitHorizon(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	insert := func(tx *sql.Tx, ext string) {
		t.Helper()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO events (source, external_id, type, payload, received_at)
			 VALUES ('system', $1, 'test.event', '{}', now())`, ext); err != nil {
			t.Fatal(err)
		}
	}

	txA, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer txA.Rollback()
	insert(txA, "le-slow") // uncommitted

	txB, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	insert(txB, "le-fast")
	if err := txB.Commit(); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListEvents(ctx, EventFilter{Type: "test.event"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got {
		if e.ExternalID == "le-slow" {
			t.Fatalf("ListEvents surfaced an uncommitted row: %+v", e)
		}
	}

	if err := txA.Commit(); err != nil {
		t.Fatal(err)
	}
	got = pollListEvents(t, ctx, s, EventFilter{Type: "test.event"}, 2)
	var extIDs []string
	for _, e := range got {
		extIDs = append(extIDs, e.ExternalID)
	}
	if len(got) != 2 || extIDs[0] != "le-slow" || extIDs[1] != "le-fast" {
		t.Fatalf("ListEvents = %v, want [le-slow le-fast] in id order", extIDs)
	}
}

// TestListEventsFilters covers type, since, after and the default/cap-200
// limit in one pass: each filter narrows a shared seeded set independently.
func TestListEventsFilters(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	base := s.Now()
	var ids []int64
	for i, typ := range []string{"alpha.event", "beta.event", "alpha.event"} {
		id, _, err := s.RecordEvent(ctx, "system", fmt.Sprintf("lef-%d", i), typ, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	all := pollListEvents(t, ctx, s, EventFilter{After: ids[0] - 1}, 3)
	var allExt []string
	for _, e := range all {
		allExt = append(allExt, e.ExternalID)
	}
	if len(all) < 3 {
		t.Fatalf("seed events not all visible: got %v", allExt)
	}

	// Type filter.
	got, err := s.ListEvents(ctx, EventFilter{Type: "beta.event", After: ids[0] - 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != ids[1] {
		t.Fatalf("Type filter = %+v, want just event %d", got, ids[1])
	}

	// After filter: exclusive cursor, so ids[0] itself is excluded.
	got, err = s.ListEvents(ctx, EventFilter{Type: "alpha.event", After: ids[0]})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != ids[2] {
		t.Fatalf("After filter = %+v, want just event %d", got, ids[2])
	}

	// Since filter: a timestamp before every seeded row includes them all;
	// one after excludes them all.
	got = pollListEvents(t, ctx, s, EventFilter{Since: base, After: ids[0] - 1}, 3)
	if len(got) < 3 {
		t.Fatalf("Since(before all) = %+v, want at least 3", got)
	}
	got, err = s.ListEvents(ctx, EventFilter{Since: base.Add(time.Hour), After: ids[0] - 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Since(after all) = %+v, want none", got)
	}

	// Limit: 0 defaults to 200 (no truncation of our 3-row set); an explicit
	// limit truncates.
	got, err = s.ListEvents(ctx, EventFilter{Type: "alpha.event", After: ids[0] - 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("Limit=1 = %+v, want exactly 1", got)
	}
}

// TestEventSubscriberStatusesReportsHolder exercises the subscriber-status
// join against the same advisory-lock key TestSubscriberLockExclusive uses,
// confirming holder_pid tracks the live lock and goes back to 0 once it is
// released. See TestEventSubscriberStatusesReportsLag for the lag column.
func TestEventSubscriberStatusesReportsHolder(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.EnsureEventSubscriber(ctx, "status-sub"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RecordEvent(ctx, "system", "ess-1", "test.event", nil, nil); err != nil {
		t.Fatal(err)
	}

	statusFor := func(name string) (EventSubscriberStatus, bool) {
		statuses, err := s.EventSubscriberStatuses(ctx)
		if err != nil {
			t.Fatalf("EventSubscriberStatuses: %v", err)
		}
		for _, st := range statuses {
			if st.Name == name {
				return st, true
			}
		}
		return EventSubscriberStatus{}, false
	}

	st, ok := statusFor("status-sub")
	if !ok || st.HolderPID != 0 {
		t.Fatalf("status before locking = %+v, ok=%v, want holder_pid=0", st, ok)
	}

	l, ok, err := s.TryLockSubscriber(ctx, "status-sub")
	if err != nil || !ok {
		t.Fatalf("lock: ok=%v err=%v", ok, err)
	}
	wantPID := int64(advisoryLockHolderPID(t, ctx, s, "status-sub"))
	if wantPID == 0 {
		t.Fatal("advisory lock not visible in pg_locks after acquiring")
	}

	st, ok = statusFor("status-sub")
	if !ok || st.HolderPID != wantPID {
		t.Fatalf("status while locked: holder_pid = %d, want %d (ok=%v)", st.HolderPID, wantPID, ok)
	}

	if err := l.Release(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, ok = statusFor("status-sub")
		if ok && st.HolderPID == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status after release: holder_pid = %d, want 0", st.HolderPID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestEventSubscriberStatusesReportsLag asserts the exact value of the
// status join's lag column — GREATEST(horizon_max_id -
// last_acked_offset, 0) — through none-acked, partially-acked, and
// fully-acked, so a wrong GREATEST bound or a sign error in the subtraction
// would fail here rather than pass silently behind an unchecked field.
func TestEventSubscriberStatusesReportsLag(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.EnsureEventSubscriber(ctx, "lag-sub"); err != nil {
		t.Fatal(err)
	}

	var ids []int64
	for i := 0; i < 3; i++ {
		id, _, err := s.RecordEvent(ctx, "system", fmt.Sprintf("essl-%d", i), "test.event", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	lagFor := func(name string) (int64, bool) {
		statuses, err := s.EventSubscriberStatuses(ctx)
		if err != nil {
			t.Fatalf("EventSubscriberStatuses: %v", err)
		}
		for _, st := range statuses {
			if st.Name == name {
				return st.Lag, true
			}
		}
		return 0, false
	}

	// AckEvents requires last_acked_offset <= last_read_offset (the CHECK
	// constraint), so last_read_offset must be advanced past ids[1] and
	// ids[2] before acking them — pollReadEventBatch does that and, being
	// itself horizon-bounded, also waits out the same cluster-wide commit
	// horizon hazard lag's own max-id subquery is subject to (a concurrent
	// transaction elsewhere on this Postgres instance can hold
	// pg_snapshot_xmin back regardless of what this test committed).
	if got := pollReadEventBatch(t, ctx, s, "lag-sub", 3, 3); len(got) != 3 {
		t.Fatalf("read got %d events, want 3", len(got))
	}
	if lag, ok := lagFor("lag-sub"); !ok || lag != 3 {
		t.Fatalf("lag before any ack = %d (ok=%v), want 3", lag, ok)
	}

	if err := s.AckEvents(ctx, "lag-sub", ids[1]); err != nil {
		t.Fatalf("ack first two of three: %v", err)
	}
	if lag, ok := lagFor("lag-sub"); !ok || lag != 1 {
		t.Fatalf("lag after acking 2 of 3 = %d (ok=%v), want 1", lag, ok)
	}

	if err := s.AckEvents(ctx, "lag-sub", ids[2]); err != nil {
		t.Fatalf("ack all three: %v", err)
	}
	if lag, ok := lagFor("lag-sub"); !ok || lag != 0 {
		t.Fatalf("lag fully acked = %d (ok=%v), want 0", lag, ok)
	}
}

// TestSeekEventSubscriberRedeliversAndRejectsUnknown covers both branches of
// SeekEventSubscriber: moving offsets down onto an existing subscriber makes
// a following ReadEventBatch redeliver from there, and seeking an unknown
// name reports ErrNotFound.
func TestSeekEventSubscriberRedeliversAndRejectsUnknown(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.EnsureEventSubscriber(ctx, "seek-sub"); err != nil {
		t.Fatal(err)
	}

	var ids []int64
	for i := 0; i < 3; i++ {
		id, _, err := s.RecordEvent(ctx, "system", fmt.Sprintf("seek-%d", i), "test.event", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	got := pollReadEventBatch(t, ctx, s, "seek-sub", 3, 3)
	if len(got) != 3 {
		t.Fatalf("initial read got %d events, want 3", len(got))
	}
	if err := s.AckEvents(ctx, "seek-sub", ids[2]); err != nil {
		t.Fatalf("ack all: %v", err)
	}

	if err := s.SeekEventSubscriber(ctx, "seek-sub", ids[0]); err != nil {
		t.Fatalf("seek: %v", err)
	}

	subs, err := s.EventSubscribers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sub EventSubscriber
	for _, es := range subs {
		if es.Name == "seek-sub" {
			sub = es
		}
	}
	if sub.LastRead != ids[0] || sub.LastAcked != ids[0] {
		t.Fatalf("offsets after seek = (read=%d, acked=%d), want both %d", sub.LastRead, sub.LastAcked, ids[0])
	}

	redelivered := pollReadEventBatch(t, ctx, s, "seek-sub", 10, 2)
	if len(redelivered) != 2 || redelivered[0].ID != ids[1] || redelivered[1].ID != ids[2] {
		t.Fatalf("redelivered = %+v, want events 2 and 3", redelivered)
	}

	if err := s.SeekEventSubscriber(ctx, "no-such-subscriber", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("seek unknown subscriber: err = %v, want ErrNotFound", err)
	}
}

// A session-scoped advisory lock dies with its session, and the pinned
// connection sits idle between polls — so Healthy is a consumer's only way
// to learn its lock is gone (an idle_session_timeout, a pooler reap, a
// pg_terminate_backend). It must report the loss, not a healthy pool.
func TestSubscriberLockHealthyDetectsLostSession(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	l, ok, err := s.TryLockSubscriber(ctx, "doc-lifecycle")
	if err != nil || !ok {
		t.Fatalf("lock: ok=%v err=%v", ok, err)
	}
	defer l.Release(ctx)

	if err := l.Healthy(ctx); err != nil {
		t.Fatalf("Healthy on a freshly acquired lock: %v", err)
	}
	pid := advisoryLockHolderPID(t, ctx, s, "doc-lifecycle")
	if pid == 0 {
		t.Fatalf("advisory lock not visible in pg_locks after acquiring")
	}

	if _, err := s.db.ExecContext(ctx, `SELECT pg_terminate_backend($1)`, pid); err != nil {
		t.Fatalf("terminate backend %d: %v", pid, err)
	}

	// The pool is fine — only the lock session died.
	if err := s.db.PingContext(ctx); err != nil {
		t.Fatalf("pool ping after terminating the lock session: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := l.Healthy(ctx); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Healthy still reports the lock held 5s after its session was terminated")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := advisoryLockHolderPID(t, ctx, s, "doc-lifecycle"); got != 0 {
		t.Fatalf("advisory lock still held by pid %d after its session died", got)
	}
}
