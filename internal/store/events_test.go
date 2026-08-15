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

// The §9 ordering trap, directly. A last_seen_id cursor sees B (id 2),
// advances, and never delivers A (id 1). The horizon delivers neither
// until A's transaction is finished, then both, in id order.
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
