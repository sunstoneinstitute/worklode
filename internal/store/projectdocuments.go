// projectdocuments.go implements WL-REQ-1798's documents register: pointers
// to files, datasets and links a project or idea works from. Every write is
// one event (document.registered, document.updated, document.removed).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// DocOwner is the project or idea a register row belongs to; exactly one is set.
type DocOwner struct{ project, idea string }

// ProjectOwner is the DocOwner for a project's register.
func ProjectOwner(projectID string) DocOwner { return DocOwner{project: projectID} }

const (
	// Participants are read as JSON so database/sql needs no text[] codec.
	projectDocColumns = `id, COALESCE(project, ''), COALESCE(idea, ''), name, category, type, status,
		url, source, milestone, COALESCE(due::text, ''), COALESCE(date::text, ''),
		to_jsonb(participants), created_at, updated_at, COALESCE(created_by, '')`
	projectDocEventSource = "cli"
)

var (
	projectDocCategories = []string{"spec", "plan", "meeting-notes", "deliverables", "sources"}
	projectDocTypes      = []string{"document", "spreadsheet", "pdf", "slides", "dataset", "link"}
	projectDocStatuses   = []string{"draft", "in-review", "final"}
)

func scanProjectDocument(row rowScanner) (*model.ProjectDocument, error) {
	var d model.ProjectDocument
	var parts []byte
	if err := row.Scan(&d.ID, &d.Project, &d.Idea, &d.Name, &d.Category, &d.Type, &d.Status,
		&d.URL, &d.Source, &d.Milestone, &d.Due, &d.Date, &parts,
		&d.CreatedAt, &d.UpdatedAt, &d.CreatedBy); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(parts, &d.Participants); err != nil {
		return nil, fmt.Errorf("decode participants: %w", err)
	}
	d.CreatedAt = d.CreatedAt.UTC()
	d.UpdatedAt = d.UpdatedAt.UTC()
	return &d, nil
}

func validDocEnum(field, v string, allowed []string) error {
	if !slices.Contains(allowed, v) {
		return fmt.Errorf("%s %q must be one of %s: %w", field, v, strings.Join(allowed, ", "), ErrInvalidInput)
	}
	return nil
}

func validDocDate(field, s string) error {
	if s == "" {
		return nil
	}
	if d, err := time.Parse("2006-01-02", s); err != nil || d.Format("2006-01-02") != s {
		return fmt.Errorf("%s must be YYYY-MM-DD: %w", field, ErrInvalidInput)
	}
	return nil
}

// validateProjectDocument checks every field of a full row.
func validateProjectDocument(d *model.ProjectDocument) error {
	if strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("name is empty: %w", ErrInvalidInput)
	}
	for _, c := range []struct {
		field, v string
		allowed  []string
	}{
		{"category", d.Category, projectDocCategories},
		{"type", d.Type, projectDocTypes},
		{"status", d.Status, projectDocStatuses},
	} {
		if err := validDocEnum(c.field, c.v, c.allowed); err != nil {
			return err
		}
	}
	if err := validDocDate("due", d.Due); err != nil {
		return err
	}
	return validDocDate("date", d.Date)
}

// checkActors refuses an id that is not an actor. An empty id is skipped.
func checkActors(tx *sql.Tx, ids []string) error {
	for _, id := range ids {
		if id == "" {
			continue
		}
		var ok bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM actors WHERE id = $1)`, id).Scan(&ok); err != nil {
			return fmt.Errorf("look up participant %s: %w", id, err)
		}
		if !ok {
			return fmt.Errorf("unknown actor %q in participants or actor: %w", id, ErrInvalidInput)
		}
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Store) listProjectDocuments(ctx context.Context, col, id string) ([]model.ProjectDocument, error) {
	what := "list documents for " + id
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+projectDocColumns+` FROM project_documents WHERE `+col+` = $1
		 ORDER BY created_at, id`, id)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	out, err := collectRows(rows, what, byValue(scanProjectDocument))
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []model.ProjectDocument{}
	}
	return out, nil
}

// ListProjectDocuments returns a project's register rows, oldest first.
func (s *Store) ListProjectDocuments(ctx context.Context, projectID string) ([]model.ProjectDocument, error) {
	return s.listProjectDocuments(ctx, "project", projectID)
}

// ListIdeaDocuments returns an idea's register rows, oldest first.
func (s *Store) ListIdeaDocuments(ctx context.Context, ideaTaskID string) ([]model.ProjectDocument, error) {
	return s.listProjectDocuments(ctx, "idea", ideaTaskID)
}

// GetProjectDocument returns one row, ErrNotFound when absent.
func (s *Store) GetProjectDocument(ctx context.Context, id string) (*model.ProjectDocument, error) {
	d, err := scanProjectDocument(s.db.QueryRowContext(ctx,
		`SELECT `+projectDocColumns+` FROM project_documents WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("document %s: %w", id, ErrNotFound)
	}
	return d, err
}

func insertProjectDocument(tx *sql.Tx, d *model.ProjectDocument) error {
	parts, _ := json.Marshal(d.Participants)
	_, err := tx.Exec(
		`INSERT INTO project_documents (id, project, idea, name, category, type, status, url, source,
			milestone, due, date, participants, created_at, updated_at, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
			ARRAY(SELECT jsonb_array_elements_text($13::jsonb)), $14, $15, $16)`,
		d.ID, nullIfEmpty(d.Project), nullIfEmpty(d.Idea), d.Name, d.Category, d.Type, d.Status,
		d.URL, d.Source, d.Milestone, nullIfEmpty(d.Due), nullIfEmpty(d.Date), string(parts),
		d.CreatedAt, d.UpdatedAt, nullIfEmpty(d.CreatedBy))
	if err != nil {
		return fmt.Errorf("insert document %s: %w", d.ID, err)
	}
	return nil
}

// CreateProjectDocument registers a row under owner and records
// document.registered in the same transaction. Validation failures are
// ErrInvalidInput naming the field.
func (s *Store) CreateProjectDocument(ctx context.Context, owner DocOwner, in model.CreateProjectDocumentInput, actor string) (*model.ProjectDocument, error) {
	if (owner.project == "") == (owner.idea == "") {
		return nil, fmt.Errorf("document needs exactly one of project and idea: %w", ErrInvalidInput)
	}
	suffix, err := randomExternalID()
	if err != nil {
		return nil, err
	}
	now := s.Now().UTC().Truncate(time.Second)
	d := &model.ProjectDocument{
		ID: "doc-" + suffix[:12], Project: owner.project, Idea: owner.idea,
		Name: strings.TrimSpace(in.Name), Category: in.Category, Type: in.Type, Status: in.Status,
		URL: in.URL, Source: in.Source, Milestone: in.Milestone, Due: in.Due, Date: in.Date,
		Participants: in.Participants, CreatedAt: now, UpdatedAt: now, CreatedBy: actor,
	}
	if d.Participants == nil {
		d.Participants = []string{}
	}
	if err := validateProjectDocument(d); err != nil {
		return nil, err
	}
	if err := s.recordProjectDocEvent(ctx, "document.registered", actor, d, func(tx *sql.Tx) error {
		if owner.project != "" {
			var ok bool
			if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1)`, owner.project).Scan(&ok); err != nil {
				return fmt.Errorf("look up project %s: %w", owner.project, err)
			}
			if !ok {
				return fmt.Errorf("project %s: %w", owner.project, ErrNotFound)
			}
		}
		if err := checkActors(tx, append([]string{actor}, d.Participants...)); err != nil {
			return err
		}
		return insertProjectDocument(tx, d)
	}); err != nil {
		return nil, err
	}
	return d, nil
}

// PatchProjectDocument applies the non-nil fields of in and records
// document.updated. The owner never changes.
func (s *Store) PatchProjectDocument(ctx context.Context, id string, in model.PatchProjectDocumentInput, actor string) (*model.ProjectDocument, error) {
	d, err := s.GetProjectDocument(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, f := range []struct {
		dst *string
		src *string
	}{
		{&d.Name, in.Name}, {&d.Category, in.Category}, {&d.Type, in.Type}, {&d.Status, in.Status},
		{&d.URL, in.URL}, {&d.Source, in.Source}, {&d.Milestone, in.Milestone},
		{&d.Due, in.Due}, {&d.Date, in.Date},
	} {
		if f.src != nil {
			*f.dst = *f.src
		}
	}
	d.Name = strings.TrimSpace(d.Name)
	if in.Participants != nil {
		d.Participants = *in.Participants
		if d.Participants == nil {
			d.Participants = []string{}
		}
	}
	if err := validateProjectDocument(d); err != nil {
		return nil, err
	}
	d.UpdatedAt = s.Now().UTC().Truncate(time.Second)
	if err := s.recordProjectDocEvent(ctx, "document.updated", actor, d, func(tx *sql.Tx) error {
		if err := checkActors(tx, d.Participants); err != nil {
			return err
		}
		parts, _ := json.Marshal(d.Participants)
		res, err := tx.Exec(
			`UPDATE project_documents SET name = $2, category = $3, type = $4, status = $5, url = $6,
				source = $7, milestone = $8, due = $9, date = $10,
				participants = ARRAY(SELECT jsonb_array_elements_text($11::jsonb)), updated_at = $12
			 WHERE id = $1`,
			d.ID, d.Name, d.Category, d.Type, d.Status, d.URL, d.Source, d.Milestone,
			nullIfEmpty(d.Due), nullIfEmpty(d.Date), string(parts), d.UpdatedAt)
		if err != nil {
			return fmt.Errorf("update document %s: %w", d.ID, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("document %s: %w", d.ID, ErrNotFound)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return d, nil
}

// DeleteProjectDocument hard-deletes a row and records document.removed.
func (s *Store) DeleteProjectDocument(ctx context.Context, id, actor string) error {
	d, err := s.GetProjectDocument(ctx, id)
	if err != nil {
		return err
	}
	return s.recordProjectDocEvent(ctx, "document.removed", actor, d, func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM project_documents WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("delete document %s: %w", id, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("document %s: %w", id, ErrNotFound)
		}
		return nil
	})
}

// recordProjectDocEvent runs write inside a RecordEvent of type typ.
func (s *Store) recordProjectDocEvent(ctx context.Context, typ, actor string, d *model.ProjectDocument, write func(tx *sql.Tx) error) error {
	ext, err := randomExternalID()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		ID      string `json:"id"`
		Project string `json:"project,omitempty"`
		Idea    string `json:"idea,omitempty"`
		Name    string `json:"name"`
		Actor   string `json:"actor"`
	}{d.ID, d.Project, d.Idea, d.Name, actor})
	if err != nil {
		return err
	}
	_, _, err = s.RecordEvent(ctx, projectDocEventSource, ext, typ, payload,
		func(tx *sql.Tx, _ int64) error { return write(tx) })
	return err
}
