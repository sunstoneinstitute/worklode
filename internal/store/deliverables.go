// deliverables.go implements spec 029 §3's deliverable: a declared,
// checkable output of a project (a datapackage, a report PDF, a CMS post).
// It is a first-class entity, not a task kind — it cannot be claimed, worked,
// or closed — and it stores no state, because §3.2 makes deliverable state
// something emitters and probers report, never something a human asserts by
// closing a task. Only the three descriptive fields §3.1 gives a custom
// deliverable (name, description, optional URL) live here.
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
	CreatedBy   string
}

// deliverableSeqKind is the deliverable's row key in project_entity_seq and
// the type segment of its id (spec 029 §4's COW-DEL-3).
const deliverableSeqKind = "DEL"

// deliverableColumns is the SELECT list shared by every deliverable read.
const deliverableColumns = `id, project_id, name, description, url, created_by, created_at, updated_at`

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
	}
	if _, err := tx.Exec(
		`INSERT INTO deliverables (`+deliverableColumns+`)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		d.ID, d.Project, d.Name, d.Description, d.URL, createdBy, d.CreatedAt, d.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert deliverable %s: %w", id, err)
	}
	return d, nil
}

// scanDeliverable reads one row selected with deliverableColumns.
func scanDeliverable(row rowScanner) (*model.Deliverable, error) {
	var d model.Deliverable
	var createdBy sql.NullString
	if err := row.Scan(&d.ID, &d.Project, &d.Name, &d.Description, &d.URL,
		&createdBy, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	d.CreatedBy = createdBy.String
	return &d, nil
}

// ListDeliverables returns a project's declared deliverables in declaration
// order. An unknown project yields an empty slice, not an error — callers
// that need the project to exist (every current one) load it first.
func (s *Store) ListDeliverables(ctx context.Context, projectID string) ([]model.Deliverable, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+deliverableColumns+` FROM deliverables
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
		`SELECT `+deliverableColumns+` FROM deliverables WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get deliverable %s: %w", id, err)
	}
	return d, nil
}
