// Delivery-lifecycle fact tables
// (docs/specs/004-execution-backbone.md). Handlers record
// facts inside a RecordEvent transaction, then call ResolveDelivery
// (delivery_resolve.go), which advances the task to the furthest milestone
// the facts support.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
// An unknown task id is also a no-op, checked explicitly rather than left to
// the task_id foreign key: a correlation failure must never fail the
// delivery, but an FK violation aborts the whole enclosing transaction
// (Postgres does not let ON CONFLICT DO NOTHING suppress it), which would
// discard every other fact recorded alongside it and the event row itself.
func InsertTaskCommit(tx *sql.Tx, tc TaskCommit) error {
	exists, err := taskExists(tx, tc.TaskID)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err = tx.Exec(
		`INSERT INTO task_commits (task_id, repo, sha, source, seen_at)
		 VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`,
		tc.TaskID, tc.Repo, tc.SHA, tc.Source, tc.SeenAt.UTC())
	if err != nil {
		return fmt.Errorf("insert task_commit %s %s: %w", tc.TaskID, tc.SHA, err)
	}
	return nil
}

// TaskIDsForSHA returns the ids of tasks already attributed to sha in repo,
// from any source (branch push, PR correlation, merge message, marker),
// ordered for deterministic iteration.
func TaskIDsForSHA(tx *sql.Tx, repo, sha string) ([]string, error) {
	rows, err := tx.Query(
		`SELECT DISTINCT task_id FROM task_commits WHERE repo = $1 AND sha = $2
		 ORDER BY task_id`, repo, sha)
	if err != nil {
		return nil, fmt.Errorf("tasks for sha %s %s: %w", repo, sha, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan task for sha %s %s: %w", repo, sha, err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tasks for sha %s %s: %w", repo, sha, err)
	}
	return out, nil
}

// ClearTaskCommits drops every commit attributed to taskID. Reopening a task
// voids its previous delivery: the old commits are still on main, so without
// this the next webhook to touch the repo would find the task below the
// deployed frontier and snap it straight back to its former delivered state
// — closing the fresh lease of whoever re-claimed it. New work must re-land.
func ClearTaskCommits(tx *sql.Tx, taskID string) error {
	if _, err := tx.Exec(`DELETE FROM task_commits WHERE task_id = $1`, taskID); err != nil {
		return fmt.Errorf("clear task_commits for %s: %w", taskID, err)
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

// LatestMainID returns the id of the newest default-branch commit recorded
// for repo, or nil if none has been seen. A release tags main's head, so
// this is what a published release covers.
func LatestMainID(tx *sql.Tx, repo string) (*int64, error) {
	var id sql.NullInt64
	if err := tx.QueryRow(`SELECT max(id) FROM main_commits WHERE repo = $1`,
		repo).Scan(&id); err != nil {
		return nil, fmt.Errorf("latest main commit for %s: %w", repo, err)
	}
	if !id.Valid {
		return nil, nil
	}
	return &id.Int64, nil
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
// carry one). Returns the owning repo and id, or ("", nil) if unknown. If the
// sha exists in more than one repo (forks, repo migration), picks the most
// recently appended match deterministically rather than an arbitrary row.
func MainIDForSHAAnyRepo(tx *sql.Tx, sha string) (string, *int64, error) {
	var repo string
	var id int64
	err := tx.QueryRow(`SELECT repo, id FROM main_commits WHERE sha = $1 ORDER BY id DESC LIMIT 1`,
		sha).Scan(&repo, &id)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRow(`SELECT repo, main_id FROM deploy_shas WHERE sha = $1 ORDER BY main_id DESC LIMIT 1`,
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

// NormalizeEnvironment maps a GitHub environment name to the delivery stage
// it represents: "dev", "prod", or "" for environments the lifecycle
// ignores (copilot, github-pages, pypi, *-apply, ...).
func NormalizeEnvironment(name string) string {
	switch strings.ToLower(name) {
	case "dev", "test", "development", "staging":
		return "dev"
	case "prod", "production":
		return "prod"
	default:
		return ""
	}
}

func bumpEnvDeploy(tx *sql.Tx, now time.Time, repo, env, column string, mainID int64, fluxSeen bool) error {
	// column is one of two compile-time constants below — never user input.
	q := fmt.Sprintf(
		`INSERT INTO env_deploys (repo, environment, %[1]s, flux_seen, updated_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (repo, environment) DO UPDATE SET
		   %[1]s = greatest(coalesce(env_deploys.%[1]s, 0), excluded.%[1]s),
		   flux_seen = env_deploys.flux_seen OR excluded.flux_seen,
		   updated_at = excluded.updated_at`, column)
	if _, err := tx.Exec(q, repo, env, mainID, fluxSeen, now.UTC()); err != nil {
		return fmt.Errorf("bump env_deploy %s/%s %s: %w", repo, env, column, err)
	}
	return nil
}

// BumpEnvDeployGH advances the GitHub deployment watermark for repo/env.
func BumpEnvDeployGH(tx *sql.Tx, now time.Time, repo, env string, mainID int64) error {
	return bumpEnvDeploy(tx, now, repo, env, "gh_main_id", mainID, false)
}

// BumpEnvDeployFlux advances the Flux confirmation watermark and latches
// flux_seen, switching the repo/env to dual-signal gating permanently. It
// reports whether this call is the one that latched, so the caller can log
// the switch once: a Flux revision correlated to the wrong repo latches a
// repo/env onto a signal that will never arrive, stranding its tasks, and
// that is otherwise invisible.
func BumpEnvDeployFlux(tx *sql.Tx, now time.Time, repo, env string, mainID int64) (latched bool, err error) {
	var seen bool
	err = tx.QueryRow(`SELECT flux_seen FROM env_deploys WHERE repo = $1 AND environment = $2`,
		repo, env).Scan(&seen)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("flux_seen %s/%s: %w", repo, env, err)
	}
	if err := bumpEnvDeploy(tx, now, repo, env, "flux_main_id", mainID, true); err != nil {
		return false, err
	}
	return !seen, nil
}

// ConfirmedFrontier returns the newest main-commit id confirmed deployed to
// repo/env: min(gh, flux) once a Flux signal has ever been correlated for
// the pair, the GitHub watermark alone before that (bootstrap fallback).
// nil if nothing is confirmed.
func ConfirmedFrontier(tx *sql.Tx, repo, env string) (*int64, error) {
	var gh, flux sql.NullInt64
	var fluxSeen bool
	err := tx.QueryRow(
		`SELECT gh_main_id, flux_main_id, flux_seen FROM env_deploys
		 WHERE repo = $1 AND environment = $2`, repo, env).Scan(&gh, &flux, &fluxSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("frontier %s/%s: %w", repo, env, err)
	}
	if !fluxSeen {
		if !gh.Valid {
			return nil, nil
		}
		return &gh.Int64, nil
	}
	if !gh.Valid || !flux.Valid {
		return nil, nil
	}
	confirmed := min(gh.Int64, flux.Int64)
	return &confirmed, nil
}

// SetReleaseFrontier records the newest main commit covered by a published
// release. Forward-only per tag, like every other watermark here: re-cutting
// a tag onto a newer commit advances it, a stale re-publish (or a plain
// redelivery) never moves it back. published_at moves with main_id so the
// row always describes one cut — a stale re-publish must not backdate the
// newer frontier.
func SetReleaseFrontier(tx *sql.Tx, repo, tag string, mainID int64, publishedAt time.Time) error {
	_, err := tx.Exec(
		`INSERT INTO release_frontiers (repo, tag, main_id, published_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (repo, tag) DO UPDATE SET
		   main_id = greatest(release_frontiers.main_id, excluded.main_id),
		   published_at = CASE WHEN excluded.main_id > release_frontiers.main_id
		                       THEN excluded.published_at
		                       ELSE release_frontiers.published_at END`,
		repo, tag, mainID, publishedAt.UTC())
	if err != nil {
		return fmt.Errorf("set release frontier %s %s: %w", repo, tag, err)
	}
	return nil
}

// DeliveryFacts summarizes one repo's delivery progress for a task.
type DeliveryFacts struct {
	Repo       string
	LandedSHA  string
	LandedAt   time.Time
	Deployed   []DeployFact // confirmed envs covering the landed commit
	ReleaseTag string       // "" if not released
	ReleasedAt time.Time
}

// DeployFact is one environment that has confirmably received the work.
type DeployFact struct {
	Environment string
	At          time.Time
}

// DeliveryFactsForTask returns the task's delivery facts, one entry per repo
// its work landed in (newest landed commit per repo); repos where nothing has
// landed are absent. Read-only, but runs in a transaction so it can reuse
// ConfirmedFrontier's dual-signal rule instead of restating it in SQL.
func (s *Store) DeliveryFactsForTask(ctx context.Context, taskID string) ([]DeliveryFacts, error) {
	var out []DeliveryFacts
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = deliveryFactsForTask(tx, taskID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func deliveryFactsForTask(tx *sql.Tx, taskID string) ([]DeliveryFacts, error) {
	// DISTINCT ON collapses several landed commits in one repo to the newest,
	// matching LandedMainID.
	rows, err := tx.Query(
		`SELECT DISTINCT ON (tc.repo) tc.repo, mc.id, mc.sha, mc.pushed_at
		 FROM task_commits tc
		 JOIN main_commits mc ON mc.repo = tc.repo AND mc.sha = tc.sha
		 WHERE tc.task_id = $1
		 ORDER BY tc.repo, mc.id DESC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("delivery facts for %s: %w", taskID, err)
	}
	defer rows.Close()
	var out []DeliveryFacts
	var landedIDs []int64
	for rows.Next() {
		var f DeliveryFacts
		var landedID int64
		var pushedAt time.Time
		if err := rows.Scan(&f.Repo, &landedID, &f.LandedSHA, &pushedAt); err != nil {
			return nil, fmt.Errorf("scan delivery facts for %s: %w", taskID, err)
		}
		f.LandedAt = pushedAt.UTC()
		out = append(out, f)
		landedIDs = append(landedIDs, landedID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("delivery facts for %s: %w", taskID, err)
	}

	for i := range out {
		if err := attachDeployFacts(tx, &out[i], landedIDs[i]); err != nil {
			return nil, err
		}
		if err := attachReleaseFact(tx, &out[i], landedIDs[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// attachDeployFacts records the environments whose confirmed frontier covers
// landedID. The confirmation rule is ConfirmedFrontier's, not this query's:
// env_deploys rows exist as soon as one signal arrives, long before they
// confirm anything. The rows are drained before the frontier lookups: a
// transaction holds one connection, so the queries cannot overlap.
func attachDeployFacts(tx *sql.Tx, f *DeliveryFacts, landedID int64) error {
	rows, err := tx.Query(
		`SELECT environment, updated_at FROM env_deploys WHERE repo = $1
		 ORDER BY environment`, f.Repo)
	if err != nil {
		return fmt.Errorf("env deploys for %s: %w", f.Repo, err)
	}
	defer rows.Close()
	var envs []DeployFact
	for rows.Next() {
		var d DeployFact
		var updatedAt time.Time
		if err := rows.Scan(&d.Environment, &updatedAt); err != nil {
			return fmt.Errorf("scan env deploy for %s: %w", f.Repo, err)
		}
		d.At = updatedAt.UTC()
		envs = append(envs, d)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("env deploys for %s: %w", f.Repo, err)
	}
	for _, d := range envs {
		frontier, err := ConfirmedFrontier(tx, f.Repo, d.Environment)
		if err != nil {
			return err
		}
		if frontier != nil && *frontier >= landedID {
			f.Deployed = append(f.Deployed, d)
		}
	}
	return nil
}

// attachReleaseFact records the earliest release covering landedID — the cut
// that shipped the work, not the newest one that happens to include it.
func attachReleaseFact(tx *sql.Tx, f *DeliveryFacts, landedID int64) error {
	var tag string
	var publishedAt time.Time
	err := tx.QueryRow(
		`SELECT tag, published_at FROM release_frontiers
		 WHERE repo = $1 AND main_id >= $2 ORDER BY main_id LIMIT 1`,
		f.Repo, landedID).Scan(&tag, &publishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("release covering %s/%d: %w", f.Repo, landedID, err)
	}
	f.ReleaseTag = tag
	f.ReleasedAt = publishedAt.UTC()
	return nil
}

// ReleaseFrontier returns the newest released main-commit id for repo, or
// nil if the repo has no releases recorded.
func ReleaseFrontier(tx *sql.Tx, repo string) (*int64, error) {
	var id sql.NullInt64
	err := tx.QueryRow(`SELECT max(main_id) FROM release_frontiers WHERE repo = $1`,
		repo).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("release frontier %s: %w", repo, err)
	}
	if !id.Valid {
		return nil, nil
	}
	return &id.Int64, nil
}
