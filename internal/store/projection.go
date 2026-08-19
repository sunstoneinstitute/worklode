package store

import (
	"context"
	"fmt"
)

// ProjectionCheckpoint returns the state_log id through which the
// backbone→knowledge-graph projector has already run (spec 006 §11).
func (s *Store) ProjectionCheckpoint(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT last_state_log_id FROM graph_projection WHERE id = 1`).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("projection checkpoint: %w", err)
	}
	return id, nil
}

// SetProjectionCheckpoint advances the projector's watermark to id.
func (s *Store) SetProjectionCheckpoint(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE graph_projection SET last_state_log_id = $1 WHERE id = 1`, id)
	if err != nil {
		return fmt.Errorf("set projection checkpoint to %d: %w", id, err)
	}
	return nil
}

// DirtyProjects returns the distinct project ids whose tasks have state_log
// activity after the given watermark, in first-touched order, and the last
// state_log id the scan covered (== after when there was nothing). limit
// bounds the number of log rows read, so one projection batch is bounded
// even after a long outage.
//
// The LEFT JOIN keeps the watermark advancing even over a log row whose
// task no longer resolves (no delete path exists today; this is a guard,
// not a feature). state_log ids are assigned at insert time, so two
// concurrent transactions within one process can commit out of order: a
// slow transaction can commit a lower id after a projector scan already read
// past it and checkpointed, and that row is never scanned. Acceptable for
// v1 because it self-heals: the project's next real event re-renders its
// whole graph from scratch, and a watermark rewind heals unconditionally.
// The tracked fix is WL-119 (read to a commit horizon, as
// EventLogHorizonID does in internal/store/events.go).
func (s *Store) DirtyProjects(ctx context.Context, after int64, limit int) (projects []string, through int64, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT sl.id, t.project_id
		   FROM state_log sl LEFT JOIN tasks t ON t.id = sl.entity_id
		  WHERE sl.entity_kind = 'task' AND sl.id > $1
		  ORDER BY sl.id LIMIT $2`,
		after, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("dirty projects after %d: %w", after, err)
	}
	defer rows.Close()

	through = after
	seen := make(map[string]bool)
	for rows.Next() {
		var id int64
		var projectID *string
		if err := rows.Scan(&id, &projectID); err != nil {
			return nil, 0, fmt.Errorf("scan dirty project row: %w", err)
		}
		through = id
		if projectID == nil || seen[*projectID] {
			continue
		}
		seen[*projectID] = true
		projects = append(projects, *projectID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("dirty projects after %d: %w", after, err)
	}
	return projects, through, nil
}
