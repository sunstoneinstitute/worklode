package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RuntimeEvent is one observed pod-level or Flux reconciliation event:
// a crash loop, an OOM kill, or a Flux reconciliation failure/recovery.
//
// RuntimeEvent is deliberately not model.RuntimeEvent: ArtifactID is
// database bookkeeping this package needs internally (linking a Flux event
// to the deployment artifact that caused it) that never crosses the wire, so
// it stays outside the seven fields model.RuntimeEvent declares (ADR 036 §3,
// "store scan plumbing"). api.toRuntimeEventJSON is the one conversion point
// from this type to model.RuntimeEvent.
type RuntimeEvent struct {
	ID         int64
	Cluster    string
	Kind       string
	Workload   string
	Image      string
	ArtifactID *int64
	Message    string
	OccurredAt time.Time
}

// defaultRuntimeEventLimit is used by ListRuntimeEvents when limit <= 0.
const defaultRuntimeEventLimit = 50

// InsertRuntimeEvent inserts a runtime event and returns its id. If
// re.ArtifactID is nil, it is resolved from re.Image using the same match
// rule as FindArtifactByImage (kind=docker_image, name:tag), scoped to this
// transaction; no match leaves artifact_id NULL. A caller-supplied
// ArtifactID is never overridden by image resolution.
func InsertRuntimeEvent(tx *sql.Tx, re RuntimeEvent) (int64, error) {
	artifactID := re.ArtifactID
	if artifactID == nil && re.Image != "" {
		a, err := findArtifactByImageTx(tx, re.Image)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return 0, err
		}
		if err == nil {
			artifactID = &a.ID
		}
	}

	var artifactIDArg sql.NullInt64
	if artifactID != nil {
		artifactIDArg = sql.NullInt64{Int64: *artifactID, Valid: true}
	}

	var id int64
	err := tx.QueryRow(
		`INSERT INTO runtime_events (cluster, kind, workload, image, artifact_id, message, occurred_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id`,
		re.Cluster, re.Kind, re.Workload, re.Image, artifactIDArg, re.Message,
		re.OccurredAt.UTC(),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert runtime event: %w", err)
	}
	return id, nil
}

// runtimeEventColumns is the SELECT list scanRuntimeEvent expects, in order.
const runtimeEventColumns = `id, cluster, kind, workload, image, artifact_id, message, occurred_at`

func scanRuntimeEvent(row rowScanner) (*RuntimeEvent, error) {
	var re RuntimeEvent
	var workload, image, message sql.NullString
	var artifactID sql.NullInt64
	if err := row.Scan(&re.ID, &re.Cluster, &re.Kind, &workload, &image,
		&artifactID, &message, &re.OccurredAt); err != nil {
		return nil, err
	}
	re.Workload = workload.String
	re.Image = image.String
	re.Message = message.String
	if artifactID.Valid {
		re.ArtifactID = &artifactID.Int64
	}
	re.OccurredAt = re.OccurredAt.UTC()
	return &re, nil
}

// RuntimeEventsForArtifact returns the runtime events referencing
// artifactID, oldest first.
func (s *Store) RuntimeEventsForArtifact(ctx context.Context, artifactID int64) ([]RuntimeEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+runtimeEventColumns+` FROM runtime_events WHERE artifact_id = $1 ORDER BY occurred_at, id`,
		artifactID)
	if err != nil {
		return nil, fmt.Errorf("runtime events for artifact %d: %w", artifactID, err)
	}
	return collectRows(rows, fmt.Sprintf("runtime events for artifact %d", artifactID), byValue(scanRuntimeEvent))
}

// ListRuntimeEvents returns runtime events, newest first, optionally
// filtered by cluster ("" means all clusters). limit <= 0 means
// defaultRuntimeEventLimit.
func (s *Store) ListRuntimeEvents(ctx context.Context, cluster string, limit int) ([]RuntimeEvent, error) {
	if limit <= 0 {
		limit = defaultRuntimeEventLimit
	}
	q := `SELECT ` + runtimeEventColumns + ` FROM runtime_events`
	var args []any
	if cluster != "" {
		args = append(args, cluster)
		q += fmt.Sprintf(` WHERE cluster = $%d`, len(args))
	}
	args = append(args, limit)
	q += fmt.Sprintf(` ORDER BY occurred_at DESC, id DESC LIMIT $%d`, len(args))

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list runtime events: %w", err)
	}
	return collectRows(rows, "list runtime events", byValue(scanRuntimeEvent))
}
