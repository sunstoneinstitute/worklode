package store

import (
	"context"
	"fmt"
	"time"
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

// ProjectionFailure is one quarantined project: the projector could not
// render or write its graph, and the global watermark has moved on past the
// state_log rows that made it dirty, so this row is the only remaining record
// that the project still owes a projection. Package-local rather than an
// internal/model type (ADR 036) because it never crosses the HTTP boundary —
// like TaskFilter and Edge, it is store↔caller plumbing.
type ProjectionFailure struct {
	ProjectID     string
	Attempts      int // consecutive failed attempts, including the latest
	FirstFailedAt time.Time
	LastFailedAt  time.Time
	NextAttemptAt time.Time // no earlier re-attempt unless the project goes dirty again
	LastError     string
}

// ProjectionFailures returns every quarantined project, oldest failure first.
// The whole table is read each run rather than only the due rows: it is
// bounded by the number of projects, and the projector needs the attempt
// count of a not-yet-due project too, in case fresh state_log activity makes
// it dirty and it fails again.
func (s *Store) ProjectionFailures(ctx context.Context) ([]ProjectionFailure, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT project_id, attempts, first_failed_at, last_failed_at, next_attempt_at, last_error
		   FROM graph_projection_failures ORDER BY first_failed_at, project_id`)
	if err != nil {
		return nil, fmt.Errorf("projection failures: %w", err)
	}
	defer rows.Close()

	var out []ProjectionFailure
	for rows.Next() {
		var f ProjectionFailure
		if err := rows.Scan(&f.ProjectID, &f.Attempts, &f.FirstFailedAt,
			&f.LastFailedAt, &f.NextAttemptAt, &f.LastError); err != nil {
			return nil, fmt.Errorf("scan projection failure: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("projection failures: %w", err)
	}
	return out, nil
}

// RecordProjectionFailure upserts one project's quarantine row. The caller
// owns the retry policy: it supplies Attempts and NextAttemptAt, so the
// backoff curve stays a pure function in Go rather than an expression in SQL.
// FirstFailedAt is preserved across updates — it is how long the project has
// been stuck, which the attempt count alone does not say.
func (s *Store) RecordProjectionFailure(ctx context.Context, f ProjectionFailure) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO graph_projection_failures
		     (project_id, attempts, first_failed_at, last_failed_at, next_attempt_at, last_error)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (project_id) DO UPDATE SET
		     attempts        = EXCLUDED.attempts,
		     last_failed_at  = EXCLUDED.last_failed_at,
		     next_attempt_at = EXCLUDED.next_attempt_at,
		     last_error      = EXCLUDED.last_error`,
		f.ProjectID, f.Attempts, f.FirstFailedAt, f.LastFailedAt, f.NextAttemptAt, f.LastError)
	if err != nil {
		return fmt.Errorf("record projection failure for %s: %w", f.ProjectID, err)
	}
	return nil
}

// ClearProjectionFailure removes a project from quarantine after a successful
// projection. Deleting a row that is not there is not an error.
func (s *Store) ClearProjectionFailure(ctx context.Context, projectID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM graph_projection_failures WHERE project_id = $1`, projectID)
	if err != nil {
		return fmt.Errorf("clear projection failure for %s: %w", projectID, err)
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
// task no longer resolves. No path *removes* a task row — delete is a
// tombstone (044 §2), which still joins — so this is a guard, not a feature.
// state_log ids are assigned at insert time, so two
// concurrent transactions within one process can commit out of order: a
// slow transaction can commit a lower id after a projector scan already read
// past it and checkpointed, and that row is never scanned. Acceptable for
// v1 because it self-heals: the project's next real event re-renders its
// whole graph from scratch, and a watermark rewind heals unconditionally.
// The tracked fix is WL-119 (read to a commit horizon, as
// EventLogHorizonID does in internal/store/events.go).
func (s *Store) DirtyProjects(ctx context.Context, after int64, limit int) (projects []string, through int64, err error) {
	// Documents dirty their project too (WL-289): a doc mutation logs
	// entity_kind 'doc' with the doc id in decimal, and the projector
	// re-renders the owning project's declared doc graphs in the same
	// cycle as its project graph.
	rows, err := s.db.QueryContext(ctx,
		`SELECT sl.id, coalesce(t.project_id, d.project)
		   FROM state_log sl
		   LEFT JOIN tasks t ON sl.entity_kind = 'task' AND t.id = sl.entity_id
		   LEFT JOIN docs d ON sl.entity_kind = 'doc' AND d.id::text = sl.entity_id
		  WHERE sl.entity_kind IN ('task', 'doc') AND sl.id > $1
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
