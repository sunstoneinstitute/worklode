// deliverables.go implements spec 029 §3's deliverable: a declared,
// checkable output of a project (a datapackage, a report PDF, a CMS post).
// It is a first-class entity, not a task kind — it cannot be claimed, worked,
// or closed — and it stores no state, because §3.2 makes deliverable state
// something emitters and probers report, never something a human asserts by
// closing a task. Only the three descriptive fields §3.1 gives a custom
// deliverable (name, description, optional URL) live in the row; the artifact
// address it is verified by is a declaration (see artifactevidence.go), and
// the state reported against that address is joined on read.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// DeliverableInput carries the fields for declaring a new deliverable.
type DeliverableInput struct {
	ProjectID   string
	Name        string
	Description string
	URL         string
	// Artifact is the address the deliverable is verified by (029 §3.1), or
	// "" when it declares none. It is not stored on the row: it becomes an
	// artifact_declarations entry, which is what the catalog ingest routes on.
	Artifact  string
	CreatedBy string
}

// deliverableSeqKind is the deliverable's row key in project_entity_seq and
// the type segment of its id (spec 029 §4's COW-DEL-3).
const deliverableSeqKind = "DEL"

// deliverableColumns is the deliverables table's own column list, in insert
// and scan order.
const deliverableColumns = `id, project_id, name, description, url, created_by, created_at, updated_at`

// deliverableSelect is what a read projects: the stored columns, then the
// declared artifact address and the newest state reported about it. The last
// two are joined, never stored — 029 §3.2 keeps deliverable state a reported
// fact, so the row itself has nothing to say about it.
const deliverableSelect = deliverableColumns + `,
	COALESCE((SELECT ad.artifact_uri FROM artifact_declarations ad
	           WHERE ad.entity_kind = 'deliverable' AND ad.entity_id = deliverables.id
	           ORDER BY ad.id LIMIT 1), ''),
	COALESCE(ev.state, ''), ev.occurred_at`

// deliverableFrom pairs the table with the latest evidence filed against the
// deliverable, if any. Latest is by the emitter's own clock with id as the
// tiebreak: a fact reported late about an earlier moment does not displace a
// newer one. This is the only reader of artifact_evidence.
const deliverableFrom = `FROM deliverables
	LEFT JOIN LATERAL (
	    SELECT e.state, e.occurred_at FROM artifact_evidence e
	     WHERE e.entity_kind = 'deliverable' AND e.entity_id = deliverables.id
	     ORDER BY e.occurred_at DESC, e.id DESC LIMIT 1
	) ev ON true`

// CreateDeliverable allocates the next <KEY>-DEL-<n> id from the project's
// DEL counter and inserts the deliverable inside the given transaction. Like
// CreateTask it is meant to be called from a RecordEvent apply callback with
// the store's clock as now. An unknown project is ErrNotFound and a blank
// name is ErrInvalidInput — both checked before the id is allocated, so a
// rejected input never burns an ordinal.
func CreateDeliverable(tx *sql.Tx, now time.Time, in DeliverableInput) (*model.Deliverable, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("deliverable name is empty: %w", ErrInvalidInput)
	}

	var key string
	if err := tx.QueryRow(`SELECT key FROM projects WHERE id = $1`, in.ProjectID).Scan(&key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("project %s: %w", in.ProjectID, ErrNotFound)
		}
		return nil, fmt.Errorf("look up project %s: %w", in.ProjectID, err)
	}

	// The upsert both creates the counter row on a project's first
	// deliverable (next = 2, ordinal 1) and advances it afterwards, holding
	// the row lock for the rest of the transaction so two concurrent creates
	// cannot draw the same ordinal.
	var n int64
	if err := tx.QueryRow(
		`INSERT INTO project_entity_seq (project_id, kind, next) VALUES ($1, $2, 2)
		 ON CONFLICT (project_id, kind) DO UPDATE SET next = project_entity_seq.next + 1
		 RETURNING next - 1`,
		in.ProjectID, deliverableSeqKind,
	).Scan(&n); err != nil {
		return nil, fmt.Errorf("allocate deliverable id: %w", err)
	}
	id := fmt.Sprintf("%s-%s-%d", key, deliverableSeqKind, n)

	ts := now.UTC().Truncate(time.Second)
	var createdBy sql.NullString
	if in.CreatedBy != "" {
		createdBy = sql.NullString{String: in.CreatedBy, Valid: true}
	}
	d := &model.Deliverable{
		ID:          id,
		Project:     in.ProjectID,
		Name:        name,
		Description: strings.TrimSpace(in.Description),
		URL:         strings.TrimSpace(in.URL),
		CreatedBy:   in.CreatedBy,
		CreatedAt:   ts,
		UpdatedAt:   ts,
		Artifact:    strings.TrimSpace(in.Artifact),
	}
	if _, err := tx.Exec(
		`INSERT INTO deliverables (`+deliverableColumns+`)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		d.ID, d.Project, d.Name, d.Description, d.URL, createdBy, d.CreatedAt, d.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert deliverable %s: %w", id, err)
	}
	if d.Artifact != "" {
		if err := DeclareArtifact(tx, now, "deliverable", d.ID, d.Artifact); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// scanDeliverable reads one row selected with deliverableSelect. It is the
// single scan point for both readers, so neither can drift on the projection.
func scanDeliverable(row rowScanner) (*model.Deliverable, error) {
	var d model.Deliverable
	var createdBy sql.NullString
	var reportedAt sql.NullTime
	if err := row.Scan(&d.ID, &d.Project, &d.Name, &d.Description, &d.URL,
		&createdBy, &d.CreatedAt, &d.UpdatedAt,
		&d.Artifact, &d.ReportedState, &reportedAt); err != nil {
		return nil, err
	}
	d.CreatedBy = createdBy.String
	if reportedAt.Valid {
		t := reportedAt.Time
		d.ReportedAt = &t
	}
	return &d, nil
}

// ListDeliverables returns a project's declared deliverables in declaration
// order. An unknown project yields an empty slice, not an error — callers
// that need the project to exist (every current one) load it first.
func (s *Store) ListDeliverables(ctx context.Context, projectID string) ([]model.Deliverable, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+deliverableSelect+` `+deliverableFrom+`
		 WHERE project_id = $1 ORDER BY created_at, id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list deliverables for %s: %w", projectID, err)
	}
	out, err := collectRows(rows, fmt.Sprintf("list deliverables for %s", projectID), byValue(scanDeliverable))
	if err != nil {
		return nil, err
	}
	return nonNil(out), nil
}

// GetDeliverable looks up one deliverable by id. Returns ErrNotFound if it
// does not exist.
func (s *Store) GetDeliverable(ctx context.Context, id string) (*model.Deliverable, error) {
	d, err := scanDeliverable(s.db.QueryRowContext(ctx,
		`SELECT `+deliverableSelect+` `+deliverableFrom+` WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get deliverable %s: %w", id, err)
	}
	return d, nil
}
