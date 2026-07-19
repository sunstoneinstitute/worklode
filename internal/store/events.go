package store

import (
	"context"
	"database/sql"
	"encoding/json"
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
		receivedAt := s.nowFn().Format(time.RFC3339)

		res, err := tx.ExecContext(ctx,
			`INSERT INTO events (source, external_id, type, payload, received_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT (source, external_id) DO NOTHING`,
			source, externalID, typ, payload, receivedAt,
		)
		if err != nil {
			return fmt.Errorf("insert event: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected: %w", err)
		}

		if affected == 0 {
			// Already recorded: look up the existing id and skip apply.
			row := tx.QueryRowContext(ctx,
				`SELECT id FROM events WHERE source = ? AND external_id = ?`,
				source, externalID,
			)
			if err := row.Scan(&id); err != nil {
				return fmt.Errorf("look up existing event: %w", err)
			}
			inserted = false
			return nil
		}

		newID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("last insert id: %w", err)
		}
		id = newID
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
		 VALUES (?, ?, ?, ?, ?)`,
		entityKind, entityID, string(changeJSON), eventID, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert state_log: %w", err)
	}
	return nil
}
