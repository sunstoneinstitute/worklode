package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Project groups one or more repos under a single unit of work. Focus is the
// ordered list of concerns (see ValidConcern) the project's ranking should
// prioritize; an empty slice means no focus preference.
//
// Project is deliberately not model.Project: the curated cockpit columns
// below (migration 0013) are internal bookkeeping this package and the
// cockpit projection need that never cross the /api/v1/projects wire shape,
// so they stay outside the five fields model.Project declares (ADR 036 §3,
// "store scan plumbing"). api.toProjectJSON is the one conversion point from
// this type to model.Project.
type Project struct {
	ID    string
	Name  string
	Key   string
	Focus []string

	// Curated v0 backing for the cockpit's "Pinned focus" and "Next decision"
	// cards (migration 0013), set by a lead until spec-029 derives them. Zero
	// values mean unset: an empty FocusNote or DecisionTitle means the card is
	// absent. Written via PinProjectFocus / SetProjectNextDecision.
	FocusNote           string
	FocusPinnedBy       string
	FocusPinnedAt       time.Time
	DecisionTitle       string
	DecisionAccountable string
	DecisionReadiness   string
}

// projectExtras holds the nullable cockpit columns (migration 0013) during a
// row scan; dest wires them into a Scan call and apply copies present values
// onto a Project, leaving Go zero values where the column was NULL. Keeping
// both scan sites (GetProject, ListProjects) on this one helper stops them
// drifting apart.
type projectExtras struct {
	focusNote           sql.NullString
	focusPinnedBy       sql.NullString
	focusPinnedAt       sql.NullTime
	decisionTitle       sql.NullString
	decisionAccountable sql.NullString
	decisionReadiness   sql.NullString
}

func (e *projectExtras) dest() []any {
	return []any{
		&e.focusNote, &e.focusPinnedBy, &e.focusPinnedAt,
		&e.decisionTitle, &e.decisionAccountable, &e.decisionReadiness,
	}
}

func (e *projectExtras) apply(p *Project) {
	p.FocusNote = e.focusNote.String
	p.FocusPinnedBy = e.focusPinnedBy.String
	p.FocusPinnedAt = e.focusPinnedAt.Time
	p.DecisionTitle = e.decisionTitle.String
	p.DecisionAccountable = e.decisionAccountable.String
	p.DecisionReadiness = e.decisionReadiness.String
}

// nullIfZeroTime maps the zero time to a SQL NULL, so optional timestamptz
// columns stay NULL rather than holding the year-1 sentinel.
func nullIfZeroTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// projectColumns is the SELECT list shared by GetProject and ListProjects:
// the base columns plus the migration-0013 cockpit columns, in the order
// projectExtras.dest expects them.
const projectColumns = `id, name, key, focus,
	focus_note, focus_pinned_by, focus_pinned_at,
	decision_title, decision_accountable, decision_readiness`

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

// CreateProject registers a new project with the given immutable key.
func (s *Store) CreateProject(ctx context.Context, id, name, key string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (id, name, key) VALUES ($1, $2, $3)`, id, name, key)
	if err != nil {
		if isUniqueViolationOn(err, "projects_key_unique") {
			return ErrKeyTaken
		}
		return fmt.Errorf("insert project %s: %w", id, err)
	}
	return nil
}

// projectColumnsP is projectColumns under the `p` alias, for the queries that
// join projects against another table.
var projectColumnsP = qualifyColumns(projectColumns, "p")

// scanProject reads one row selected with projectColumns.
func scanProject(row rowScanner) (*Project, error) {
	var p Project
	var focus []byte
	var ext projectExtras
	if err := row.Scan(append([]any{&p.ID, &p.Name, &p.Key, &focus}, ext.dest()...)...); err != nil {
		return nil, err
	}
	f, err := scanProjectFocus(focus)
	if err != nil {
		return nil, fmt.Errorf("project %s focus: %w", p.ID, err)
	}
	p.Focus = f
	ext.apply(&p)
	return &p, nil
}

// projectRow runs a single-project query, mapping a missing row to
// ErrNotFound and wrapping anything else under what.
func (s *Store) projectRow(ctx context.Context, query, what string, args ...any) (*Project, error) {
	p, err := scanProject(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return p, nil
}

// GetProject looks up a project by id. Returns ErrNotFound if it does not exist.
func (s *Store) GetProject(ctx context.Context, id string) (*Project, error) {
	return s.projectRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = $1`,
		fmt.Sprintf("get project %s", id), id)
}

// ListProjects returns all projects.
func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+projectColumns+` FROM projects`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	return collectRows(rows, "list projects", byValue(scanProject))
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
			return requireOneAffected(res, "set focus",
				fmt.Errorf("project %s: %w", projectID, ErrNotFound))
		})
	return err
}

// PinProjectFocus sets (or clears) the cockpit's curated "Pinned focus" card
// for a project: a lead-set note plus who pinned it and when (migration 0013,
// superseded by spec-029). An empty note clears all three columns to NULL,
// ignoring pinnedBy and pinnedAt; a non-empty note writes them, storing NULL
// for an empty pinnedBy or a zero pinnedAt. Returns ErrNotFound if the project
// does not exist.
func (s *Store) PinProjectFocus(ctx context.Context, projectID, note, pinnedBy string, pinnedAt time.Time) error {
	// nil params clear all three; set only when the card carries a note.
	var noteVal, byVal, atVal any
	if note != "" {
		noteVal = note
		byVal = nullText(pinnedBy)
		atVal = nullIfZeroTime(pinnedAt)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE projects
		    SET focus_note = $1, focus_pinned_by = $2, focus_pinned_at = $3
		  WHERE id = $4`, noteVal, byVal, atVal, projectID)
	if err != nil {
		return fmt.Errorf("pin focus for project %s: %w", projectID, err)
	}
	return requireOneAffected(res, "pin focus",
		fmt.Errorf("project %s: %w", projectID, ErrNotFound))
}

// SetProjectNextDecision sets (or clears) the cockpit's curated "Next decision"
// card for a project: a lead-set title plus who is accountable and a readiness
// note (migration 0013, superseded by spec-029). An empty title clears all
// three columns to NULL, ignoring accountable and readiness; a non-empty title
// writes them, storing NULL for an empty accountable or readiness. Returns
// ErrNotFound if the project does not exist.
func (s *Store) SetProjectNextDecision(ctx context.Context, projectID, title, accountable, readiness string) error {
	// nil params clear all three; set only when the card carries a title.
	var titleVal, accVal, readyVal any
	if title != "" {
		titleVal = title
		accVal = nullText(accountable)
		readyVal = nullText(readiness)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE projects
		    SET decision_title = $1, decision_accountable = $2, decision_readiness = $3
		  WHERE id = $4`, titleVal, accVal, readyVal, projectID)
	if err != nil {
		return fmt.Errorf("set next decision for project %s: %w", projectID, err)
	}
	return requireOneAffected(res, "set next decision",
		fmt.Errorf("project %s: %w", projectID, ErrNotFound))
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

// RemoveRepo unmaps repo from whatever project it belongs to. A repo maps to
// at most one project (project_repos.repo is UNIQUE), so the project id is
// not needed to name the mapping. Only the mapping row goes: tasks, documents
// and ingested deliveries carry their own project_id and keep it, so
// everything already recorded for the repo stays addressable and only future
// webhook traffic stops resolving to a project. This is not a delete in the
// 044 sense — nothing is tombstoned. Returns ErrNotFound if the repo is not
// mapped to any project.
func (s *Store) RemoveRepo(ctx context.Context, repo string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM project_repos WHERE repo = $1`, repo)
	if err != nil {
		return fmt.Errorf("remove repo %s: %w", repo, err)
	}
	return requireOneAffected(res, "remove repo",
		fmt.Errorf("repo %s: %w", repo, ErrNotFound))
}

// ProjectForRepo looks up the project a repo is mapped to. Returns
// ErrNotFound if the repo is not mapped to any project.
func (s *Store) ProjectForRepo(ctx context.Context, repo string) (*Project, error) {
	// One join rather than a repo lookup followed by GetProject: this runs on
	// the GitHub webhook path, and an unmapped repo is ErrNotFound either way.
	return s.projectRow(ctx,
		`SELECT `+projectColumnsP+` FROM projects p
		   JOIN project_repos pr ON pr.project_id = p.id
		  WHERE pr.repo = $1`,
		fmt.Sprintf("look up project for repo %s", repo), repo)
}

// DefaultDoneState is the project_repos.done_state schema default, used for
// repos with no explicit terminal state (and for unmapped repos).
const DefaultDoneState = "merged"

// validDoneStates are the terminal states a repo mapping may declare as
// "fully delivered" (docs/specs/004-execution-backbone.md).
var validDoneStates = map[string]bool{"merged": true, "deployed_prod": true, "released": true}

// ValidDoneState reports whether state is an accepted repo done_state.
func ValidDoneState(state string) bool { return validDoneStates[state] }

// SetRepoDoneState sets the delivery terminal state for a mapped repo. A repo
// maps to at most one project (project_repos.repo is UNIQUE), so this updates
// exactly one row. Returns ErrInvalidInput for an unknown state and
// ErrNotFound if the repo is not mapped to any project.
func (s *Store) SetRepoDoneState(ctx context.Context, repo, state string) error {
	if !validDoneStates[state] {
		return fmt.Errorf("done_state %q: %w", state, ErrInvalidInput)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE project_repos SET done_state = $1 WHERE repo = $2`, state, repo)
	if err != nil {
		return fmt.Errorf("set done_state for %s: %w", repo, err)
	}
	return requireOneAffected(res, "set done_state",
		fmt.Errorf("repo %s: %w", repo, ErrNotFound))
}

// ListRepos returns the repos mapped to a project, each with its done_state.
func (s *Store) ListRepos(ctx context.Context, projectID string) ([]model.RepoMapping, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT repo, done_state FROM project_repos WHERE project_id = $1`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list repos for project %s: %w", projectID, err)
	}
	return collectRows(rows, fmt.Sprintf("list repos for project %s", projectID), func(r rowScanner) (model.RepoMapping, error) {
		var m model.RepoMapping
		if err := r.Scan(&m.Repo, &m.DoneState); err != nil {
			return model.RepoMapping{}, err
		}
		return m, nil
	})
}

// ListReposForProjects is the bulk form of ListRepos: the repos mapped to
// every project in one query, keyed by project id. A list endpoint reporting
// repos by calling ListRepos per row would issue one query per project.
// Projects with no repos are absent from the map; ids is empty-safe.
func (s *Store) ListReposForProjects(ctx context.Context, ids []string) (map[string][]model.RepoMapping, error) {
	if len(ids) == 0 {
		return map[string][]model.RepoMapping{}, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT project_id, repo, done_state FROM project_repos WHERE project_id = ANY($1)
		 ORDER BY project_id, repo`, ids)
	if err != nil {
		return nil, fmt.Errorf("list repos for %d projects: %w", len(ids), err)
	}
	return groupRows(rows, fmt.Sprintf("list repos for %d projects", len(ids)),
		func(r rowScanner) (string, model.RepoMapping, error) {
			var projectID string
			var m model.RepoMapping
			if err := r.Scan(&projectID, &m.Repo, &m.DoneState); err != nil {
				return "", model.RepoMapping{}, err
			}
			return projectID, m, nil
		})
}
