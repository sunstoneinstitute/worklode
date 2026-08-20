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
	where := "source = 'github' AND applied_at IS NULL"
	var args sqlArgs
	if f.Repo != "" {
		where += " AND payload->'repository'->>'full_name' = " + args.next(f.Repo)
	}
	if f.Since != nil {
		where += " AND received_at >= " + args.next(f.Since.UTC())
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+eventColumns+`
		   FROM events
		  WHERE `+where+`
		  ORDER BY id`, args.vals...)
	if err != nil {
		return nil, fmt.Errorf("unapplied events: %w", err)
	}
	return collectRows(rows, "unapplied events", scanEvent)
}
