package store

import (
	"context"
	"fmt"
	"time"
)

// ProjectionCheckpoint returns the transaction id through which the
// backbone→knowledge-graph projector has already run (spec 006 §11). It is a
// state_log.txid, not a state_log.id — see DirtyProjects for why the
// watermark counts transactions.
func (s *Store) ProjectionCheckpoint(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT last_txid FROM graph_projection WHERE id = 1`).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("projection checkpoint: %w", err)
	}
	return id, nil
}

// SetProjectionCheckpoint advances the projector's watermark to txid.
func (s *Store) SetProjectionCheckpoint(ctx context.Context, txid int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE graph_projection SET last_txid = $1 WHERE id = 1`, txid)
	if err != nil {
		return fmt.Errorf("set projection checkpoint to %d: %w", txid, err)
	}
	return nil
}

// ProjectionFailure is one quarantined project: the projector could not
// render or write its graph, and the global watermark has moved on past the
// transaction that made it dirty, so this row is the only remaining record
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

// stateLogHorizon is eventHorizon (internal/store/events.go) qualified with
// DirtyProjects' alias for state_log: one predicate, two logs, because both
// have the same problem — an id assigned at INSERT time says nothing about
// commit order. Written as a concatenation so the horizon expression itself
// still exists exactly once.
const stateLogHorizon = "sl." + eventHorizon

// DirtyProjects returns the distinct project ids whose tasks or docs have
// state_log activity after the given watermark, in first-touched order, and
// the watermark to checkpoint (== after when nothing may be checkpointed
// yet). limit bounds the number of transactions read, so one projection batch
// is bounded even after a long outage; a transaction is always read whole, so
// the watermark only ever lands on a transaction boundary.
//
// The watermark counts *transactions*, not state_log rows, because state_log
// ids come from a sequence assigned at INSERT time: two concurrent
// transactions in one process — two API request handlers are enough, no
// second replica needed — can commit out of id order, and a row-id watermark
// that advanced to the highest id it saw would checkpoint past a lower id
// that had not committed yet and never scan it (WL-119).
//
// Which is why the two bounds this returns are different bounds, and this is
// the whole design:
//
//   - Dirtiness is read from every *visible* row above the watermark, so a
//     project is re-rendered as soon as its change is committed.
//   - The watermark only advances through transactions below the commit
//     horizon (spec 025 §15's rule for the event log, same predicate). A
//     transaction still above it stays above the watermark, so the row it
//     commits late — however low its id — is scanned on a later run.
//
// Tying the watermark to the horizon but not the rendering is what keeps a
// long-running transaction anywhere on the instance (the horizon's
// characteristic hazard, visible as worklode_event_horizon_id) from stalling
// projection: while it runs, dirty projects still project, they just project
// again once the horizon passes them. Re-rendering a project costs nothing
// twice — the graph is replaced whole and deterministically, so the second
// PUT is byte-identical — which is what lets the two bounds differ at all.
//
// The LEFT JOIN keeps the watermark advancing even over a log row whose
// task no longer resolves. No path *removes* a task row — delete is a
// tombstone (044 §2), which still joins — so this is a guard, not a feature.
func (s *Store) DirtyProjects(ctx context.Context, after int64, limit int) (projects []string, through int64, err error) {
	// Documents dirty their project too (WL-289): a doc mutation logs
	// entity_kind 'doc' with the doc id in decimal, and the projector
	// re-renders the owning project's declared doc graphs in the same
	// cycle as its project graph.
	//
	// batch picks the transactions first so limit bounds transactions and
	// never cuts one in half; the outer query then reads all of their rows,
	// each carrying whether its transaction is settled — below the horizon,
	// and so safe to checkpoint. The casts are the xid8↔bigint boundary:
	// xid8 is unsigned 64-bit, and nothing here outlives a Postgres instance
	// that has issued enough transactions for that to matter.
	rows, err := s.db.QueryContext(ctx,
		`WITH batch AS (
		     SELECT DISTINCT sl.txid
		       FROM state_log sl
		      WHERE sl.entity_kind IN ('task', 'doc')
		        AND sl.txid > ($1::bigint)::text::xid8
		      ORDER BY sl.txid LIMIT $2)
		 SELECT sl.txid::text::bigint, `+stateLogHorizon+`,
		        coalesce(t.project_id, d.project_id)
		   FROM state_log sl
		   JOIN batch b ON b.txid = sl.txid
		   LEFT JOIN tasks t ON sl.entity_kind = 'task' AND t.id = sl.entity_id
		   LEFT JOIN docs d ON sl.entity_kind = 'doc' AND d.id::text = sl.entity_id
		  WHERE sl.entity_kind IN ('task', 'doc')
		  ORDER BY sl.txid, sl.id`,
		after, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("dirty projects after %d: %w", after, err)
	}
	defer rows.Close()

	through = after
	settledSoFar := true
	seen := make(map[string]bool)
	for rows.Next() {
		var txid int64
		var settled bool
		var projectID *string
		if err := rows.Scan(&txid, &settled, &projectID); err != nil {
			return nil, 0, fmt.Errorf("scan dirty project row: %w", err)
		}
		// Ascending txid order makes the settled rows a prefix — a later
		// transaction id cannot be below a horizon an earlier one is above —
		// so the watermark stops at the first unsettled transaction and the
		// scan keeps collecting projects past it.
		settledSoFar = settledSoFar && settled
		if settledSoFar {
			through = txid
		}
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
