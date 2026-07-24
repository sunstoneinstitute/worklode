package store

import (
	"context"
	"database/sql"
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
