package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// eventHorizon is the commit-horizon predicate every cursor read of events
// carries (spec 025 §15): a row is only readable once no transaction older
// than its writer can still commit, so an id can never appear behind a
// position a subscriber has already passed. One const, not one literal per
// query — the whole ordered-log guarantee is this line.
const eventHorizon = "txid < pg_snapshot_xmin(pg_current_snapshot())"

// Event is one ingested fact: a webhook delivery, a watcher observation, a
// CLI action, or an internal system event. Events are the append-only log
// that everything else in the store is derived from.
type Event struct {
	ID         int64
	Source     string
	ExternalID string
	Type       string
	Payload    []byte
	ReceivedAt time.Time
}

// eventColumns is the SELECT list scanEvent expects, in order.
const eventColumns = `id, source, external_id, type, payload, received_at`

// scanEvent reads one row selected with eventColumns, normalising the
// timestamp to UTC the way every event reader wants it.
func scanEvent(row rowScanner) (Event, error) {
	var e Event
	if err := row.Scan(&e.ID, &e.Source, &e.ExternalID, &e.Type, &e.Payload, &e.ReceivedAt); err != nil {
		return Event{}, err
	}
	e.ReceivedAt = e.ReceivedAt.UTC()
	return e, nil
}

// RecordEvent records one event and, on first sight of it, applies its
// effect via apply — all inside a single transaction. (source, externalID)
// identifies the event: if it has already been recorded, RecordEvent
// returns the existing event's id, inserted=false, and does not call apply.
// This makes redelivered webhooks and re-run watcher ticks safe to retry.
//
// If apply returns an error, the whole transaction (including the event
// insert) rolls back and that error is returned. A caller whose applies are
// replay-safe and must not lose the delivery on a failed apply uses
// RecordEventThenApply instead.
//
// apply may be nil to record an event with no side effect.
func (s *Store) RecordEvent(
	ctx context.Context,
	source, externalID, typ string,
	payload []byte,
	apply func(tx *sql.Tx, eventID int64) error,
) (id int64, inserted bool, err error) {
	txErr := s.Tx(ctx, func(tx *sql.Tx) error {
		receivedAt := s.nowFn().UTC()

		// With DO NOTHING, RETURNING yields no row on conflict, so
		// sql.ErrNoRows means the event was already recorded.
		scanErr := tx.QueryRowContext(ctx,
			`INSERT INTO events (source, external_id, type, payload, received_at)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (source, external_id) DO NOTHING
			 RETURNING id`,
			source, externalID, typ, payload, receivedAt,
		).Scan(&id)
		if errors.Is(scanErr, sql.ErrNoRows) {
			// Already recorded: look up the existing id and skip apply.
			existing, lookupErr := recordedEventID(ctx, tx, source, externalID)
			if lookupErr != nil {
				return lookupErr
			}
			id, inserted = existing, false
			return nil
		}
		if scanErr != nil {
			return fmt.Errorf("insert event: %w", scanErr)
		}
		inserted = true

		if apply != nil {
			if err := apply(tx, id); err != nil {
				return err
			}
		}
		return nil
	})
	if txErr != nil {
		return 0, false, txErr
	}
	return id, inserted, nil
}

// ApplyFailedError reports that RecordEventThenApply durably recorded the
// event but its apply failed. The event row is committed with a NULL
// applied_at, so the replay engine (spec 013 engine 1) — or a manual
// redelivery — can re-run the apply; nothing was lost.
type ApplyFailedError struct {
	EventID int64
	Err     error
}

func (e *ApplyFailedError) Error() string {
	return fmt.Sprintf("event %d recorded, apply failed: %v", e.EventID, e.Err)
}

func (e *ApplyFailedError) Unwrap() error { return e.Err }

// RecordEventThenApply is RecordEvent with the delivery's durability split
// from its effect: the event row commits in its own transaction first, and
// apply runs in a second one. A failed apply therefore returns
// *ApplyFailedError and leaves the row behind with applied_at NULL — the
// state the replay engine (spec 013 engine 1) exists to repair — instead of
// rolling the delivery back and answering an error nothing will redeliver
// (WL-247).
//
// The split changes the dedup contract too: a redelivered event whose row
// exists but was never marked applied gets its apply run (inserted=false),
// so redelivering a failed delivery repairs it. Only an event already
// marked applied skips apply. That makes this method specific to sources
// whose applies set the applied_at marker and are order-safe under replay —
// today the GitHub webhook path. Everything else keeps RecordEvent's
// one-transaction atomicity.
//
// apply may be nil to record an event with no side effect; such a row is
// left awaiting replay exactly as under RecordEvent.
func (s *Store) RecordEventThenApply(
	ctx context.Context,
	source, externalID, typ string,
	payload []byte,
	apply func(tx *sql.Tx, eventID int64) error,
) (id int64, inserted bool, err error) {
	needApply := apply != nil
	txErr := s.Tx(ctx, func(tx *sql.Tx) error {
		receivedAt := s.nowFn().UTC()
		scanErr := tx.QueryRowContext(ctx,
			`INSERT INTO events (source, external_id, type, payload, received_at)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (source, external_id) DO NOTHING
			 RETURNING id`,
			source, externalID, typ, payload, receivedAt,
		).Scan(&id)
		if errors.Is(scanErr, sql.ErrNoRows) {
			// Already recorded. Re-run apply only if no apply ever
			// completed, so a redelivery repairs a failed delivery and a
			// completed one stays a no-op.
			var appliedAt sql.NullTime
			if err := tx.QueryRowContext(ctx,
				`SELECT id, applied_at FROM events WHERE source = $1 AND external_id = $2`,
				source, externalID,
			).Scan(&id, &appliedAt); err != nil {
				return fmt.Errorf("look up existing event: %w", err)
			}
			inserted = false
			needApply = needApply && !appliedAt.Valid
			return nil
		}
		if scanErr != nil {
			return fmt.Errorf("insert event: %w", scanErr)
		}
		inserted = true
		return nil
	})
	if txErr != nil {
		return 0, false, txErr
	}

	if !needApply {
		return id, inserted, nil
	}
	if applyErr := s.Tx(ctx, func(tx *sql.Tx) error {
		return apply(tx, id)
	}); applyErr != nil {
		return id, inserted, &ApplyFailedError{EventID: id, Err: applyErr}
	}
	return id, inserted, nil
}

// RecordEventWithID is RecordEvent for payloads that must embed their own
// event id (spec 025 §15.2: the JSON-LD `@id` is `wlid:event/<id>`). It
// reserves the id from the events.id sequence first, then calls payloadFor
// to build the payload before inserting — the reverse of RecordEvent, which
// lets the INSERT assign the id. (source, externalID) still governs
// idempotency, but only for apply: payloadFor runs on every call, including
// replays, because the reserved id is needed to build the payload before
// the INSERT can detect the conflict. On conflict the existing id is
// returned, inserted=false, and apply is not called; the payload just
// built is discarded and its id is burned (see the INSERT comment below).
//
// payloadFor must not be nil.
func (s *Store) RecordEventWithID(
	ctx context.Context,
	source, externalID, typ string,
	payloadFor func(eventID int64) ([]byte, error),
	apply func(tx *sql.Tx, eventID int64) error,
) (id int64, inserted bool, err error) {
	if payloadFor == nil {
		return 0, false, fmt.Errorf("record event with id: payloadFor must not be nil")
	}
	txErr := s.Tx(ctx, func(tx *sql.Tx) error {
		receivedAt := s.nowFn().UTC()

		var reservedID int64
		if err := tx.QueryRowContext(ctx,
			`SELECT nextval(pg_get_serial_sequence('events', 'id'))`,
		).Scan(&reservedID); err != nil {
			return fmt.Errorf("reserve event id: %w", err)
		}

		payload, err := payloadFor(reservedID)
		if err != nil {
			return fmt.Errorf("build payload for event %d: %w", reservedID, err)
		}

		// With DO NOTHING, RETURNING yields no row on conflict, so
		// sql.ErrNoRows means the event was already recorded. The
		// reserved sequence value is burned in that case — fine,
		// offsets are positions, not counts (025 §15).
		scanErr := tx.QueryRowContext(ctx,
			`INSERT INTO events (id, source, external_id, type, payload, received_at)
			 OVERRIDING SYSTEM VALUE
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (source, external_id) DO NOTHING
			 RETURNING id`,
			reservedID, source, externalID, typ, payload, receivedAt,
		).Scan(&id)
		if errors.Is(scanErr, sql.ErrNoRows) {
			existing, lookupErr := recordedEventID(ctx, tx, source, externalID)
			if lookupErr != nil {
				return lookupErr
			}
			id, inserted = existing, false
			return nil
		}
		if scanErr != nil {
			return fmt.Errorf("insert event: %w", scanErr)
		}
		inserted = true

		if apply != nil {
			if err := apply(tx, id); err != nil {
				return err
			}
		}
		return nil
	})
	if txErr != nil {
		return 0, false, txErr
	}
	return id, inserted, nil
}

// EventPayload marshals v as an event payload. Every payload is a JSON
// object naming what the event is about; an event about one task names it
// under the "task" key, so GET /api/v1/events attributes the event on its
// own without a second read of state_log (025 §15.2).
func EventPayload(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal event payload: %w", err)
	}
	return b, nil
}

// AttributeEventToTask sets "task" on the payload of the event being
// recorded, for the events whose task id does not exist until apply runs:
// task.created and issue.promoted mint the id from the project counter
// inside the transaction, after RecordEvent has already marshalled the
// payload (025 §15.2).
//
// It must be called from inside that same transaction — the apply callback —
// so the event row becomes visible to any reader already carrying its task
// id. No committed row is ever rewritten: the INSERT and this UPDATE are one
// transaction, and events are read below the commit horizon, so the
// intermediate payload is unobservable. That is what keeps the log
// append-only in the sense that matters (§15.3's objection is to patching a
// row that has already committed).
func AttributeEventToTask(tx *sql.Tx, eventID int64, taskID string) error {
	what := fmt.Sprintf("attribute event %d to task %s", eventID, taskID)
	return mergeEventPayload(tx, eventID, map[string]string{"task": taskID}, what)
}

// MergeEventPayload folds fields into an already-inserted event's payload,
// for the values that do not exist until the transaction runs: a minted id, a
// position resolved against the rows already there. Same rule and the same
// justification as AttributeEventToTask — call it only from the apply
// callback of the event it names.
func MergeEventPayload(tx *sql.Tx, eventID int64, fields map[string]string) error {
	return mergeEventPayload(tx, eventID, fields, fmt.Sprintf("merge into event %d payload", eventID))
}

// mergeEventPayload is the single writer both forms share; what names the
// operation in every error it returns.
func mergeEventPayload(tx *sql.Tx, eventID int64, fields map[string]string, what string) error {
	extra, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	res, err := tx.Exec(
		`UPDATE events
		    SET payload = CASE WHEN jsonb_typeof(payload) = 'object' THEN payload ELSE '{}'::jsonb END
		                  || $2::jsonb
		  WHERE id = $1`,
		eventID, extra)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return requireOneAffected(res, what, fmt.Errorf("event %d: %w", eventID, ErrNotFound))
}

// recordedEventID reads back the id of the event (source, externalID) already
// names, for the ON CONFLICT DO NOTHING path both RecordEvent forms take when
// the insert finds the event was recorded earlier.
func recordedEventID(ctx context.Context, tx *sql.Tx, source, externalID string) (int64, error) {
	var id int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM events WHERE source = $1 AND external_id = $2`,
		source, externalID,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("look up existing event: %w", err)
	}
	return id, nil
}

// GetEvent looks up one event by id. Returns ErrNotFound if it does not
// exist.
//
// It reads by primary key and is deliberately not eventHorizon-bounded — it
// is not a cursor read, and a caller that already has an id (from
// RecordEvent/RecordEventWithID/Emit) would otherwise have to wait out the
// commit horizon to read its own write back. Do not add the predicate here.
func (s *Store) GetEvent(ctx context.Context, id int64) (Event, error) {
	e, err := scanEvent(s.db.QueryRowContext(ctx,
		`SELECT `+eventColumns+` FROM events WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, fmt.Errorf("get event %d: %w", id, err)
	}
	return e, nil
}

// EventSubscriber mirrors one event_subscribers row (spec 025 §15.1).
type EventSubscriber struct {
	Name      string
	LastRead  int64
	LastAcked int64
	UpdatedAt time.Time
}

// EnsureEventSubscriber creates the subscriber row if absent. Offset 0
// means a new subscriber replays the whole log — the right default for a
// rule set that should have been running all along (025 §15.1).
func (s *Store) EnsureEventSubscriber(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO event_subscribers (name, updated_at) VALUES ($1, $2)
		 ON CONFLICT (name) DO NOTHING`, name, s.nowFn().UTC())
	if err != nil {
		return fmt.Errorf("ensure event subscriber %s: %w", name, err)
	}
	return nil
}

// ResetEventRead rewinds last_read_offset to last_acked_offset. Called
// once when a consumer acquires the subscriber lock: everything read but
// unacked by the previous holder is redelivered (at-least-once).
func (s *Store) ResetEventRead(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE event_subscribers SET last_read_offset = last_acked_offset, updated_at = $2
		 WHERE name = $1`, name, s.nowFn().UTC())
	if err != nil {
		return fmt.Errorf("reset event read for %s: %w", name, err)
	}
	return requireOneAffected(res, "reset event read for "+name,
		fmt.Errorf("event subscriber %s: %w", name, ErrNotFound))
}

// ReadEventBatch returns up to limit events after the subscriber's
// last_read_offset that are below the commit horizon, in id order, and
// advances last_read_offset to the last id returned — one transaction.
func (s *Store) ReadEventBatch(ctx context.Context, name string, limit int) ([]Event, error) {
	var events []Event
	txErr := s.Tx(ctx, func(tx *sql.Tx) error {
		var lastRead int64
		if err := tx.QueryRowContext(ctx,
			`SELECT last_read_offset FROM event_subscribers WHERE name = $1 FOR UPDATE`,
			name,
		).Scan(&lastRead); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("event subscriber %s: %w", name, ErrNotFound)
			}
			return fmt.Errorf("lock event subscriber %s: %w", name, err)
		}

		rows, err := tx.QueryContext(ctx,
			`SELECT `+eventColumns+`
			   FROM events
			  WHERE id > $1
			    AND `+eventHorizon+`
			  ORDER BY id
			  LIMIT $2`,
			lastRead, limit,
		)
		if err != nil {
			return fmt.Errorf("read events for %s: %w", name, err)
		}
		events, err = collectRows(rows, fmt.Sprintf("read events for %s", name), scanEvent)
		if err != nil {
			return err
		}

		if len(events) == 0 {
			return nil
		}
		lastID := events[len(events)-1].ID
		if _, err := tx.ExecContext(ctx,
			`UPDATE event_subscribers SET last_read_offset = $2, updated_at = $3
			 WHERE name = $1 AND last_read_offset < $2`,
			name, lastID, s.nowFn().UTC(),
		); err != nil {
			return fmt.Errorf("advance read offset for %s: %w", name, err)
		}
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}
	return events, nil
}

// AckEvents advances last_acked_offset to upTo, forward only: a late or
// replayed lower ack is a silent no-op. Acking past last_read_offset is an
// error (the CHECK backstops it), and an unknown subscriber is ErrNotFound —
// like ResetEventRead and SeekEventSubscriber, because a consumer whose row
// was deleted underneath it must not believe its acks are landing.
//
// The existence check rides in the same statement as the update precisely
// because both a missing row and a non-advancing ack update zero rows, so
// RowsAffected alone cannot tell them apart. The data-modifying CTE runs
// whether or not the outer SELECT reads it.
func (s *Store) AckEvents(ctx context.Context, name string, upTo int64) error {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`WITH acked AS (
		   UPDATE event_subscribers SET last_acked_offset = $2, updated_at = $3
		    WHERE name = $1 AND $2 > last_acked_offset
		   RETURNING 1
		 )
		 SELECT EXISTS (SELECT 1 FROM event_subscribers WHERE name = $1)`,
		name, upTo, s.nowFn().UTC(),
	).Scan(&exists)
	if err != nil {
		if isCheckViolationOn(err, "event_subscribers_acked_le_read") {
			return fmt.Errorf("ack %s past last_read_offset: %w", name, err)
		}
		return fmt.Errorf("ack events for %s: %w", name, err)
	}
	if !exists {
		return fmt.Errorf("event subscriber %s: %w", name, ErrNotFound)
	}
	return nil
}

// EventSubscribers lists all subscriber rows (offsets only; Task 7 adds
// the lag/holder view).
func (s *Store) EventSubscribers(ctx context.Context) ([]EventSubscriber, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, last_read_offset, last_acked_offset, updated_at
		   FROM event_subscribers ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list event subscribers: %w", err)
	}
	return collectRows(rows, "list event subscribers", func(r rowScanner) (EventSubscriber, error) {
		var sub EventSubscriber
		err := r.Scan(&sub.Name, &sub.LastRead, &sub.LastAcked, &sub.UpdatedAt)
		sub.UpdatedAt = sub.UpdatedAt.UTC()
		return sub, err
	})
}

// EventFilter narrows ListEvents. Zero values do not filter.
type EventFilter struct {
	Type  string
	Since time.Time
	After int64 // exclusive id cursor, for cursor-based paging
	Limit int   // default/cap 200
}

// MaxEventListLimit is both the default and the cap for ListEvents: a
// caller that omits Limit gets it, and a caller that asks for more than it
// is silently truncated to it rather than erroring.
//
// Exported because a caller that pages ListEvents has to know it. Paging
// stops on a short page, so a pager whose own page size exceeds this cap
// would read every page as short and stop after the first —
// internal/api/eventstream.go's streamHead is exactly that pager, and it
// pins the relationship at compile time.
const MaxEventListLimit = 200

// ListEvents returns matching events in id order (newest last, 025 §18),
// horizon-bounded like every subscriber read so the tail never shows an id
// that later reads would order before.
func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]Event, error) {
	where := eventHorizon
	var args sqlArgs
	if f.Type != "" {
		where += " AND type = " + args.next(f.Type)
	}
	if !f.Since.IsZero() {
		where += " AND received_at >= " + args.next(f.Since.UTC())
	}
	if f.After != 0 {
		where += " AND id > " + args.next(f.After)
	}
	limit := f.Limit
	if limit <= 0 || limit > MaxEventListLimit {
		limit = MaxEventListLimit
	}

	// Bound before the call: args.next mutates args.vals, and the order of
	// evaluation between a call operand and a plain one is unspecified.
	limitArg := args.next(limit)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+eventColumns+`
		   FROM events
		  WHERE `+where+`
		  ORDER BY id
		  LIMIT `+limitArg, args.vals...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return collectRows(rows, "list events", scanEvent)
}

// EventSubscriberStatus is the lode event subscribers row (025 §18): the
// durable offsets plus two derived, point-in-time facts — how far the
// subscriber trails the commit horizon, and which Postgres backend (if any)
// currently holds its advisory lock.
type EventSubscriberStatus struct {
	EventSubscriber
	Lag       int64
	HolderPID int64 // Postgres backend pid holding the lock; 0 = none
}

// EventSubscriberStatuses lists every subscriber with its lag and lock
// holder. The pg_locks join splits the 64-bit advisory key into classid
// (high 32 bits) / objid (low 32 bits) — the same split
// pg_try_advisory_lock's single-bigint form uses internally, so it must
// match exactly for the join to find the right lock. It is also scoped to
// the current database: advisory lock pids are visible cluster-wide in
// pg_locks, and a same-named subscriber on a different database must not be
// reported as this one's holder.
func (s *Store) EventSubscriberStatuses(ctx context.Context) ([]EventSubscriberStatus, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.name, s.last_read_offset, s.last_acked_offset, s.updated_at,
		       GREATEST(h.max_id - s.last_acked_offset, 0) AS lag,
		       COALESCE(l.pid, 0) AS holder_pid
		  FROM event_subscribers s
		 CROSS JOIN (SELECT COALESCE(MAX(id), 0) AS max_id
		               FROM events
		              WHERE `+eventHorizon+`) h
		  LEFT JOIN LATERAL (
		       SELECT pid FROM pg_locks
		        WHERE locktype = 'advisory' AND granted AND objsubid = 1
		          AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
		          AND classid = ((hashtext('wl:subscriber:' || s.name)::bigint >> 32) & 4294967295)::oid
		          AND objid   = (hashtext('wl:subscriber:' || s.name)::bigint & 4294967295)::oid
		        LIMIT 1) l ON true
		 ORDER BY s.name`)
	if err != nil {
		return nil, fmt.Errorf("event subscriber statuses: %w", err)
	}
	return collectRows(rows, "event subscriber statuses", func(r rowScanner) (EventSubscriberStatus, error) {
		var st EventSubscriberStatus
		err := r.Scan(&st.Name, &st.LastRead, &st.LastAcked, &st.UpdatedAt, &st.Lag, &st.HolderPID)
		st.UpdatedAt = st.UpdatedAt.UTC()
		return st, err
	})
}

// SeekEventSubscriber moves both offsets to the given position — the only
// path that moves an offset backwards (admin replay/skip, 025 §18). Setting
// last_read_offset and last_acked_offset to the same value keeps the
// event_subscribers_acked_le_read CHECK satisfied by construction. Safe
// precisely because handlers are idempotent.
func (s *Store) SeekEventSubscriber(ctx context.Context, name string, to int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE event_subscribers SET last_read_offset = $2, last_acked_offset = $2, updated_at = $3
		 WHERE name = $1`, name, to, s.nowFn().UTC())
	if err != nil {
		return fmt.Errorf("seek event subscriber %s: %w", name, err)
	}
	return requireOneAffected(res, "seek event subscriber "+name,
		fmt.Errorf("event subscriber %s: %w", name, ErrNotFound))
}

// SubscriberLock pins one pool connection holding the pg_try_advisory_lock
// for a subscriber (spec 025 §15.1: one active consumer). The connection is
// held for the consumer's lifetime; a crashed process drops it and
// Postgres releases the lock — failover with no lease table.
type SubscriberLock struct {
	conn *sql.Conn
	name string
}

// TryLockSubscriber takes a dedicated connection from the pool and
// attempts the advisory lock. ok=false means another consumer holds it;
// the connection is returned to the pool (safe: no lock was granted).
func (s *Store) TryLockSubscriber(ctx context.Context, name string) (l *SubscriberLock, ok bool, err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	var got bool
	err = conn.QueryRowContext(ctx,
		`SELECT pg_try_advisory_lock(hashtext('wl:subscriber:' || $1)::bigint)`,
		name).Scan(&got)
	if err != nil || !got {
		conn.Close()
		return nil, false, err
	}
	return &SubscriberLock{conn: conn, name: name}, true, nil
}

// Healthy reports whether the lock is still held, by pinging the session
// that holds it. A session-scoped advisory lock dies exactly with its
// session and nothing but Release unlocks it, so a live connection is proof
// of the lock and a dead one is proof it is gone.
//
// A consumer needs this because the lock connection sits idle between
// polls, which is precisely when an idle_session_timeout, a pooler reap or
// a pg_terminate_backend takes it — and no query through the pool would
// notice: the pool stays healthy while the advisory lock is already
// released and a standby is free to take the stream.
func (l *SubscriberLock) Healthy(ctx context.Context) error {
	return l.conn.PingContext(ctx)
}

// Release unlocks and then discards the underlying session instead of
// pooling it. driver.ErrBadConn from Raw marks the connection broken, so
// database/sql closes the real TCP session — Postgres then guarantees the
// lock is gone even if the unlock round-trip failed.
func (l *SubscriberLock) Release(ctx context.Context) error {
	_, unlockErr := l.conn.ExecContext(ctx,
		`SELECT pg_advisory_unlock(hashtext('wl:subscriber:' || $1)::bigint)`, l.name)
	l.conn.Raw(func(any) error { return driver.ErrBadConn })
	l.conn.Close()
	return unlockErr
}

// StateLogEntry is one recorded field-level change to an entity, as written
// by LogChange. Change is the raw JSON of the change object.
type StateLogEntry struct {
	ID      int64
	Change  string
	EventID int64
	At      time.Time
}

// StateLogForEntity returns the state_log entries for one entity, oldest
// first (ties broken by insertion order).
func (s *Store) StateLogForEntity(ctx context.Context, entityKind, entityID string) ([]StateLogEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, change, event_id, at FROM state_log
		 WHERE entity_kind = $1 AND entity_id = $2 ORDER BY at, id`,
		entityKind, entityID)
	if err != nil {
		return nil, fmt.Errorf("state log for %s %s: %w", entityKind, entityID, err)
	}
	return collectRows(rows, fmt.Sprintf("state log for %s %s", entityKind, entityID), func(r rowScanner) (StateLogEntry, error) {
		var e StateLogEntry
		err := r.Scan(&e.ID, &e.Change, &e.EventID, &e.At)
		e.At = e.At.UTC()
		return e, err
	})
}

// LogChange appends a row to state_log recording one field-level change to
// an entity, attributed to eventID. It must be called from inside an
// ingest transaction (e.g. from a RecordEvent apply callback) so the log
// entry commits or rolls back atomically with the change it describes.
func LogChange(tx *sql.Tx, entityKind, entityID string, eventID int64, change any) error {
	changeJSON, err := json.Marshal(change)
	if err != nil {
		return fmt.Errorf("marshal change: %w", err)
	}
	_, err = tx.Exec(
		`INSERT INTO state_log (entity_kind, entity_id, change, event_id, at)
		 VALUES ($1, $2, $3, $4, $5)`,
		entityKind, entityID, changeJSON, eventID, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("insert state_log: %w", err)
	}
	return nil
}

// EventLogHorizonID returns the highest event id below the commit horizon —
// the position every cursor read of the log can currently reach. It stops
// advancing while any long-running transaction anywhere on the instance holds
// pg_snapshot_xmin back, which is this design's characteristic failure and is
// otherwise indistinguishable from a quiet log.
//
// It is the max-id half of EventSubscriberLags' query, without subtracting an
// offset: a backward index scan that stops at the first row below the horizon.
func (s *Store) EventLogHorizonID(ctx context.Context) (int64, error) {
	var id int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(id), 0) FROM events WHERE `+eventHorizon).Scan(&id); err != nil {
		return 0, fmt.Errorf("event log horizon id: %w", err)
	}
	return id, nil
}

// SubscriberLag is how far one subscriber trails the log: the highest event
// id below the commit horizon minus what it has acked (025 §15.7).
type SubscriberLag struct {
	Name string
	Lag  int64
}

// EventSubscriberLags returns the lag of every subscriber, by name. The
// horizon tail is shared by all of them, which is what makes the gauge read
// two pathologies at once: a stuck subscriber lags alone, a long transaction
// holding the horizon back (025 §15) lags every subscriber together.
func (s *Store) EventSubscriberLags(ctx context.Context) ([]SubscriberLag, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.name, GREATEST(h.max_id - s.last_acked_offset, 0)
		   FROM event_subscribers s,
		        (SELECT COALESCE(MAX(id), 0) AS max_id
		           FROM events
		          WHERE `+eventHorizon+`) h
		  ORDER BY s.name`)
	if err != nil {
		return nil, fmt.Errorf("event subscriber lags: %w", err)
	}
	return collectRows(rows, "event subscriber lags", func(r rowScanner) (SubscriberLag, error) {
		var l SubscriberLag
		if err := r.Scan(&l.Name, &l.Lag); err != nil {
			return SubscriberLag{}, err
		}
		return l, nil
	})
}
