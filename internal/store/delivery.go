// Delivery-lifecycle fact tables and resolver
// (docs/specs/2026-07-25-delivery-lifecycle-design.md). Handlers record
// facts inside a RecordEvent transaction, then call ResolveDelivery, which
// advances the task to the furthest milestone the facts support.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TaskCommit attributes one commit to a task.
type TaskCommit struct {
	TaskID string
	Repo   string
	SHA    string
	Source string // branch_push | pr | merge_message | marker
	SeenAt time.Time
}

// InsertTaskCommit records a task↔commit attribution; duplicates are no-ops.
func InsertTaskCommit(tx *sql.Tx, tc TaskCommit) error {
	_, err := tx.Exec(
		`INSERT INTO task_commits (task_id, repo, sha, source, seen_at)
		 VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`,
		tc.TaskID, tc.Repo, tc.SHA, tc.Source, tc.SeenAt.UTC())
	if err != nil {
		return fmt.Errorf("insert task_commit %s %s: %w", tc.TaskID, tc.SHA, err)
	}
	return nil
}

// AppendMainCommit records one default-branch commit and returns its id
// (the per-repo ordering "seq"). Re-appending an existing sha returns the
// original id.
func AppendMainCommit(tx *sql.Tx, repo, sha string, pushedAt time.Time) (int64, error) {
	var id int64
	err := tx.QueryRow(
		`INSERT INTO main_commits (repo, sha, pushed_at) VALUES ($1, $2, $3)
		 ON CONFLICT (repo, sha) DO NOTHING RETURNING id`,
		repo, sha, pushedAt.UTC()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRow(`SELECT id FROM main_commits WHERE repo = $1 AND sha = $2`,
			repo, sha).Scan(&id)
	}
	if err != nil {
		return 0, fmt.Errorf("append main_commit %s %s: %w", repo, sha, err)
	}
	return id, nil
}

// MapDeploySHA maps a deploy-branch commit to the main commit its
// main-sha: trailer names; duplicates are no-ops.
func MapDeploySHA(tx *sql.Tx, repo, sha string, mainID int64) error {
	_, err := tx.Exec(
		`INSERT INTO deploy_shas (repo, sha, main_id) VALUES ($1, $2, $3)
		 ON CONFLICT DO NOTHING`, repo, sha, mainID)
	if err != nil {
		return fmt.Errorf("map deploy_sha %s %s: %w", repo, sha, err)
	}
	return nil
}

// MainIDForSHA resolves a sha to a main-commit id for repo, checking main
// commits first, then deploy-branch mappings. nil if unknown.
func MainIDForSHA(tx *sql.Tx, repo, sha string) (*int64, error) {
	var id int64
	err := tx.QueryRow(`SELECT id FROM main_commits WHERE repo = $1 AND sha = $2`,
		repo, sha).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRow(`SELECT main_id FROM deploy_shas WHERE repo = $1 AND sha = $2`,
			repo, sha).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("main id for %s %s: %w", repo, sha, err)
	}
	return &id, nil
}

// MainIDForSHAAnyRepo resolves a sha with no repo context (Flux events don't
// carry one). Returns the owning repo and id, or ("", nil) if unknown.
func MainIDForSHAAnyRepo(tx *sql.Tx, sha string) (string, *int64, error) {
	var repo string
	var id int64
	err := tx.QueryRow(`SELECT repo, id FROM main_commits WHERE sha = $1 LIMIT 1`,
		sha).Scan(&repo, &id)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRow(`SELECT repo, main_id FROM deploy_shas WHERE sha = $1 LIMIT 1`,
			sha).Scan(&repo, &id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("main id for sha %s: %w", sha, err)
	}
	return repo, &id, nil
}

// LandedMainID returns the id of the newest main commit attributed to
// taskID in repo, or nil if the task's work has not landed on main.
func LandedMainID(tx *sql.Tx, taskID, repo string) (*int64, error) {
	var id sql.NullInt64
	err := tx.QueryRow(
		`SELECT max(mc.id) FROM task_commits tc
		 JOIN main_commits mc ON mc.repo = tc.repo AND mc.sha = tc.sha
		 WHERE tc.task_id = $1 AND tc.repo = $2`, taskID, repo).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("landed main id for %s: %w", taskID, err)
	}
	if !id.Valid {
		return nil, nil
	}
	return &id.Int64, nil
}
