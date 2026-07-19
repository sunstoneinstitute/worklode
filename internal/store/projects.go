package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Project groups one or more repos under a single unit of work.
type Project struct {
	ID          string
	Name        string
	DeployGated bool
}

// CreateProject registers a new project.
func (s *Store) CreateProject(ctx context.Context, id, name string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (id, name) VALUES (?, ?)`, id, name)
	if err != nil {
		return fmt.Errorf("insert project %s: %w", id, err)
	}
	return nil
}

// GetProject looks up a project by id. Returns ErrNotFound if it does not exist.
func (s *Store) GetProject(ctx context.Context, id string) (*Project, error) {
	var p Project
	var deployGated int
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, deploy_gated FROM projects WHERE id = ?`, id)
	if err := row.Scan(&p.ID, &p.Name, &deployGated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get project %s: %w", id, err)
	}
	p.DeployGated = deployGated != 0
	return &p, nil
}

// ListProjects returns all projects.
func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, deploy_gated FROM projects`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var out []Project
	for rows.Next() {
		var p Project
		var deployGated int
		if err := rows.Scan(&p.ID, &p.Name, &deployGated); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		p.DeployGated = deployGated != 0
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
	gatedInt := 0
	if gated {
		gatedInt = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE projects SET deploy_gated = ? WHERE id = ?`, gatedInt, id)
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

// AddRepo maps repo ("owner/name") to projectID. A repo may belong to at
// most one project; adding a repo already mapped anywhere (including to the
// same project) returns ErrRepoTaken.
func (s *Store) AddRepo(ctx context.Context, projectID, repo string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO project_repos (project_id, repo) VALUES (?, ?)`, projectID, repo)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
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
		`SELECT project_id FROM project_repos WHERE repo = ?`, repo)
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
		`SELECT repo FROM project_repos WHERE project_id = ?`, projectID)
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
