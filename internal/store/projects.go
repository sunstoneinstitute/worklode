package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Project groups one or more repos under a single unit of work. Focus is the
// ordered list of concerns (see ValidConcern) the project's ranking should
// prioritize; an empty slice means no focus preference.
type Project struct {
	ID          string
	Name        string
	DeployGated bool
	Focus       []string
}

// scanProjectFocus unmarshals a jsonb focus column (read as raw bytes) into
// a []string. An empty or null column yields a nil slice.
func scanProjectFocus(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var focus []string
	if err := json.Unmarshal(raw, &focus); err != nil {
		return nil, fmt.Errorf("unmarshal focus: %w", err)
	}
	return focus, nil
}

// CreateProject registers a new project.
func (s *Store) CreateProject(ctx context.Context, id, name string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (id, name) VALUES ($1, $2)`, id, name)
	if err != nil {
		return fmt.Errorf("insert project %s: %w", id, err)
	}
	return nil
}

// GetProject looks up a project by id. Returns ErrNotFound if it does not exist.
func (s *Store) GetProject(ctx context.Context, id string) (*Project, error) {
	var p Project
	var focus []byte
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, deploy_gated, focus FROM projects WHERE id = $1`, id)
	if err := row.Scan(&p.ID, &p.Name, &p.DeployGated, &focus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get project %s: %w", id, err)
	}
	f, err := scanProjectFocus(focus)
	if err != nil {
		return nil, fmt.Errorf("get project %s: %w", id, err)
	}
	p.Focus = f
	return &p, nil
}

// ListProjects returns all projects.
func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, deploy_gated, focus FROM projects`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var out []Project
	for rows.Next() {
		var p Project
		var focus []byte
		if err := rows.Scan(&p.ID, &p.Name, &p.DeployGated, &focus); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		f, err := scanProjectFocus(focus)
		if err != nil {
			return nil, fmt.Errorf("scan project %s: %w", p.ID, err)
		}
		p.Focus = f
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	return out, nil
}

// SetDeployGated sets whether a project's tasks require a verified
// deployment (rather than just a merged PR) to move to done.
func (s *Store) SetDeployGated(ctx context.Context, id string, gated bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE projects SET deploy_gated = $1 WHERE id = $2`, gated, id)
	if err != nil {
		return fmt.Errorf("set deploy_gated for project %s: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set deploy_gated rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// SetProjectFocus records the ordered list of concerns a project's ranking
// should prioritize, as a "cli" event of type project.focus_set. Every entry
// must be a valid concern (see ValidConcern); an invalid entry returns
// ErrInvalidInput without writing anything. A nil or empty focus is valid and
// is written as an empty JSON array (not null). Returns ErrNotFound if the
// project does not exist.
func (s *Store) SetProjectFocus(ctx context.Context, projectID string, focus []string) error {
	for _, c := range focus {
		if !ValidConcern(c) {
			return fmt.Errorf("unknown concern %q: %w", c, ErrInvalidInput)
		}
	}
	if focus == nil {
		focus = []string{}
	}
	focusJSON, err := json.Marshal(focus)
	if err != nil {
		return fmt.Errorf("marshal focus: %w", err)
	}

	extID, err := randomExternalID()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"project": projectID, "focus": focus})
	if err != nil {
		return fmt.Errorf("marshal focus event payload: %w", err)
	}
	_, _, err = s.RecordEvent(ctx, "cli", extID, "project.focus_set", payload,
		func(tx *sql.Tx, eventID int64) error {
			res, err := tx.Exec(
				`UPDATE projects SET focus = $1 WHERE id = $2`, focusJSON, projectID)
			if err != nil {
				return fmt.Errorf("set focus for project %s: %w", projectID, err)
			}
			affected, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("set focus rows affected: %w", err)
			}
			if affected == 0 {
				return fmt.Errorf("project %s: %w", projectID, ErrNotFound)
			}
			return nil
		})
	return err
}

// AddRepo maps repo ("owner/name") to projectID. A repo may belong to at
// most one project; adding a repo already mapped anywhere (including to the
// same project) returns ErrRepoTaken.
func (s *Store) AddRepo(ctx context.Context, projectID, repo string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO project_repos (project_id, repo) VALUES ($1, $2)`, projectID, repo)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrRepoTaken
		}
		return fmt.Errorf("add repo %s to project %s: %w", repo, projectID, err)
	}
	return nil
}

// ProjectForRepo looks up the project a repo is mapped to. Returns
// ErrNotFound if the repo is not mapped to any project.
func (s *Store) ProjectForRepo(ctx context.Context, repo string) (*Project, error) {
	var projectID string
	row := s.db.QueryRowContext(ctx,
		`SELECT project_id FROM project_repos WHERE repo = $1`, repo)
	if err := row.Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("look up project for repo %s: %w", repo, err)
	}
	return s.GetProject(ctx, projectID)
}

// ListRepos returns the repos mapped to a project.
func (s *Store) ListRepos(ctx context.Context, projectID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT repo FROM project_repos WHERE project_id = $1`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list repos for project %s: %w", projectID, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var repo string
		if err := rows.Scan(&repo); err != nil {
			return nil, fmt.Errorf("scan repo: %w", err)
		}
		out = append(out, repo)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list repos for project %s: %w", projectID, err)
	}
	return out, nil
}
