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

// RecordEvent records one event and, on first sight of it, applies its
// effect via apply — all inside a single transaction. (source, externalID)
// identifies the event: if it has already been recorded, RecordEvent
// returns the existing event's id, inserted=false, and does not call apply.
// This makes redelivered webhooks and re-run watcher ticks safe to retry.
//
// If apply returns an error, the whole transaction (including the event
// insert) rolls back and that error is returned.
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
			row := tx.QueryRowContext(ctx,
				`SELECT id FROM events WHERE source = $1 AND external_id = $2`,
				source, externalID,
			)
			if err := row.Scan(&id); err != nil {
				return fmt.Errorf("look up existing event: %w", err)
			}
			inserted = false
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
			row := tx.QueryRowContext(ctx,
				`SELECT id FROM events WHERE source = $1 AND external_id = $2`,
				source, externalID,
			)
			if err := row.Scan(&id); err != nil {
				return fmt.Errorf("look up existing event: %w", err)
			}
			inserted = false
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

// GetEvent looks up one event by id. Returns ErrNotFound if it does not
// exist. Unlike ReadEventBatch, this is not horizon-bounded — callers that
// already have an id (e.g. from RecordEvent/RecordEventWithID/Emit) don't
// need to wait out the commit horizon to read their own write back.
func (s *Store) GetEvent(ctx context.Context, id int64) (Event, error) {
	var e Event
	row := s.db.QueryRowContext(ctx,
		`SELECT id, source, external_id, type, payload, received_at FROM events WHERE id = $1`, id)
	if err := row.Scan(&e.ID, &e.Source, &e.ExternalID, &e.Type, &e.Payload, &e.ReceivedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Event{}, ErrNotFound
		}
		return Event{}, fmt.Errorf("get event %d: %w", id, err)
	}
	e.ReceivedAt = e.ReceivedAt.UTC()
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
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reset event read for %s: %w", name, err)
	}
	if n == 0 {
		return fmt.Errorf("event subscriber %s: %w", name, ErrNotFound)
	}
	return nil
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
			`SELECT id, source, external_id, type, payload, received_at
			   FROM events
			  WHERE id > $1
			    AND txid < pg_snapshot_xmin(pg_current_snapshot())
			  ORDER BY id
			  LIMIT $2`,
			lastRead, limit,
		)
		if err != nil {
			return fmt.Errorf("read events for %s: %w", name, err)
		}
		defer rows.Close()

		for rows.Next() {
			var e Event
			if err := rows.Scan(&e.ID, &e.Source, &e.ExternalID, &e.Type, &e.Payload, &e.ReceivedAt); err != nil {
				return fmt.Errorf("scan event: %w", err)
			}
			e.ReceivedAt = e.ReceivedAt.UTC()
			events = append(events, e)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read events for %s: %w", name, err)
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
// error (the CHECK backstops it).
func (s *Store) AckEvents(ctx context.Context, name string, upTo int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE event_subscribers SET last_acked_offset = $2, updated_at = $3
		 WHERE name = $1 AND $2 > last_acked_offset`,
		name, upTo, s.nowFn().UTC(),
	)
	if err != nil {
		if isCheckViolationOn(err, "event_subscribers_acked_le_read") {
			return fmt.Errorf("ack %s past last_read_offset: %w", name, err)
		}
		return fmt.Errorf("ack events for %s: %w", name, err)
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
	defer rows.Close()

	var out []EventSubscriber
	for rows.Next() {
		var sub EventSubscriber
		if err := rows.Scan(&sub.Name, &sub.LastRead, &sub.LastAcked, &sub.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan event subscriber: %w", err)
		}
		sub.UpdatedAt = sub.UpdatedAt.UTC()
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list event subscribers: %w", err)
	}
	return out, nil
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
	defer rows.Close()

	var out []StateLogEntry
	for rows.Next() {
		var e StateLogEntry
		if err := rows.Scan(&e.ID, &e.Change, &e.EventID, &e.At); err != nil {
			return nil, fmt.Errorf("scan state log entry: %w", err)
		}
		e.At = e.At.UTC()
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state log for %s %s: %w", entityKind, entityID, err)
	}
	return out, nil
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
