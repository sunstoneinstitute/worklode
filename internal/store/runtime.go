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

	res, err := tx.Exec(
		`INSERT INTO runtime_events (cluster, kind, workload, image, artifact_id, message, occurred_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		re.Cluster, re.Kind, re.Workload, re.Image, artifactIDArg, re.Message,
		re.OccurredAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("insert runtime event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("runtime event insert id: %w", err)
	}
	return id, nil
}

// runtimeEventColumns is the SELECT list scanRuntimeEvent expects, in order.
const runtimeEventColumns = `id, cluster, kind, workload, image, artifact_id, message, occurred_at`

func scanRuntimeEvent(row rowScanner) (*RuntimeEvent, error) {
	var re RuntimeEvent
	var workload, image, message sql.NullString
	var occurredAt string
	var artifactID sql.NullInt64
	if err := row.Scan(&re.ID, &re.Cluster, &re.Kind, &workload, &image,
		&artifactID, &message, &occurredAt); err != nil {
		return nil, err
	}
	re.Workload = workload.String
	re.Image = image.String
	re.Message = message.String
	if artifactID.Valid {
		re.ArtifactID = &artifactID.Int64
	}
	t, err := time.Parse(time.RFC3339, occurredAt)
	if err != nil {
		return nil, fmt.Errorf("parse runtime event %d occurred_at: %w", re.ID, err)
	}
	re.OccurredAt = t
	return &re, nil
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
		q += ` WHERE cluster = ?`
		args = append(args, cluster)
	}
	q += ` ORDER BY occurred_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list runtime events: %w", err)
	}
	defer rows.Close()

	var out []RuntimeEvent
	for rows.Next() {
		re, err := scanRuntimeEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan runtime event: %w", err)
		}
		out = append(out, *re)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list runtime events: %w", err)
	}
	return out, nil
}
