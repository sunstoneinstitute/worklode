// Reconciliation queries (docs/specs/013-reconciliation.md): the applied_at
// completion marker, the replay candidate set, and the ingestion-health and
// poll-candidate reads added by later tasks in the same plan.

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// MarkEventApplied records that an event's apply completed, by either the
// webhook path or the replayer. Must run in the same transaction as the
// apply so the marker commits or rolls back with the effect it describes.
func MarkEventApplied(tx *sql.Tx, eventID int64, at time.Time) error {
	if _, err := tx.Exec(`UPDATE events SET applied_at = $2 WHERE id = $1`,
		eventID, at.UTC()); err != nil {
		return fmt.Errorf("mark event %d applied: %w", eventID, err)
	}
	return nil
}

// UnappliedFilter bounds the replay candidate set. Zero values disable each
// filter. Repo matches the delivery payload's repository.full_name.
type UnappliedFilter struct {
	Repo  string
	Since *time.Time
}

// UnappliedGitHubEvents returns github-source events whose apply has not
// completed — *.ignored deliveries and anything the replayer has not reached
// yet — oldest first, so replay preserves arrival order.
func (s *Store) UnappliedGitHubEvents(ctx context.Context, f UnappliedFilter) ([]Event, error) {
	q := `SELECT id, source, external_id, type, payload, received_at
	      FROM events WHERE source = 'github' AND applied_at IS NULL`
	var args []any
	if f.Repo != "" {
		args = append(args, f.Repo)
		q += fmt.Sprintf(` AND payload->'repository'->>'full_name' = $%d`, len(args))
	}
	if f.Since != nil {
		args = append(args, f.Since.UTC())
		q += fmt.Sprintf(` AND received_at >= $%d`, len(args))
	}
	q += ` ORDER BY id`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("unapplied events: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Source, &e.ExternalID, &e.Type, &e.Payload, &e.ReceivedAt); err != nil {
			return nil, fmt.Errorf("scan unapplied event: %w", err)
		}
		e.ReceivedAt = e.ReceivedAt.UTC()
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("unapplied events: %w", err)
	}
	return out, nil
}
