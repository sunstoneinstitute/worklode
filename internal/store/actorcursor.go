package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ActorEventCursor returns the actor's Morning Brief boundary: the highest
// event id they have explicitly reviewed through. 0 when the actor has
// never reviewed — the brief then starts from the beginning of the log.
func (s *Store) ActorEventCursor(ctx context.Context, actorID string) (int64, error) {
	var last int64
	err := s.db.QueryRowContext(ctx,
		`SELECT last_event_id FROM actor_event_cursor WHERE actor_id = $1`,
		actorID,
	).Scan(&last)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("actor event cursor for %s: %w", actorID, err)
	}
	return last, nil
}

// AdvanceActorEventCursor moves the boundary forward to `to`, upserting the
// row. Forward-only: a `to` at or below the stored value is a no-op
// (advanced=false), never a rewind — GREATEST in the upsert, not a check in
// the handler.
func (s *Store) AdvanceActorEventCursor(ctx context.Context, actorID string, to int64) (bool, error) {
	if actorID == "" || to <= 0 {
		return false, fmt.Errorf("%w: advance actor event cursor needs a non-empty actorID and to > 0", ErrInvalidInput)
	}
	var advanced bool
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO actor_event_cursor (actor_id, last_event_id, updated_at)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (actor_id) DO UPDATE
		   SET last_event_id = GREATEST(actor_event_cursor.last_event_id, EXCLUDED.last_event_id),
		       updated_at = EXCLUDED.updated_at
		 RETURNING last_event_id = $2`,
		actorID, to, s.nowFn().UTC(),
	).Scan(&advanced)
	if err != nil {
		return false, fmt.Errorf("advance actor event cursor for %s: %w", actorID, err)
	}
	return advanced, nil
}
