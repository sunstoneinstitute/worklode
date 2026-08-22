// Reconciliation queries (docs/specs/013-reconciliation.md): the applied_at
// completion marker, the replay candidate set, and the ingestion-health and
// poll-candidate reads added by later tasks in the same plan.

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// MarkEventApplied records that an event's apply completed, by either the
// webhook path or the replayer. Must run in the same transaction as the
// apply so the marker commits or rolls back with the effect it describes.
func MarkEventApplied(tx *sql.Tx, eventID int64, at time.Time) error {
	if _, err := tx.Exec(`UPDATE events SET applied_at = $2 WHERE id = $1`,
		eventID, at.UTC()); err != nil {
		return fmt.Errorf("mark event %d applied: %w", eventID, err)
	}
	return nil
}

// replaySources are the event sources engine 1 can re-apply: a github
// delivery recorded before its repo was mapped (or whose apply failed), and a
// catalog delivery that matched no declaration when it arrived (029 §3.2,
// WL-256). Both leave applied_at NULL exactly when there is still an apply to
// run, so the candidate set stays finite. Flux is deliberately absent: its
// handler never sets applied_at at all, so every flux row would be a
// permanent candidate with nothing to replay.
var replaySources = []string{"github", "catalog"}

// UnappliedFilter bounds the replay candidate set. Zero values disable each
// filter. Repo matches the delivery payload's repository.full_name — which
// only github deliveries carry, so a repo-scoped run is github-only by
// construction.
type UnappliedFilter struct {
	Repo  string
	Since *time.Time

	// Limit caps how many rows come back; <= 0 is unbounded. Every row
	// carries its whole delivery payload (up to hooks.maxGitHubBody each),
	// so a caller that materialises the result — hooks.Replay does — must
	// set it. Order is by id, so a bounded read is the oldest batch and the
	// next run picks up where this one stopped.
	Limit int
}

// UnappliedEvents returns replay-source events whose apply has not completed
// — github *.ignored deliveries, unrouted catalog deliveries, and anything
// the replayer has not reached yet — oldest first, so replay preserves
// arrival order.
func (s *Store) UnappliedEvents(ctx context.Context, f UnappliedFilter) ([]Event, error) {
	var args sqlArgs
	where := "source = ANY(" + args.next(replaySources) + ") AND applied_at IS NULL"
	if f.Repo != "" {
		where += " AND payload->'repository'->>'full_name' = " + args.next(f.Repo)
	}
	if f.Since != nil {
		where += " AND received_at >= " + args.next(f.Since.UTC())
	}

	limit := ""
	if f.Limit > 0 {
		limit = " LIMIT " + args.next(f.Limit)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+eventColumns+`
		   FROM events
		  WHERE `+where+`
		  ORDER BY id`+limit, args.vals...)
	if err != nil {
		return nil, fmt.Errorf("unapplied events: %w", err)
	}
	return collectRows(rows, "unapplied events", scanEvent)
}

// PollCandidate is one (task, repo) pair the poll engine should ask GitHub
// about.
type PollCandidate struct {
	TaskID string
	State  string
	Repo   string
}

// PollCandidates returns tasks whose delivery state can still advance
// (the same advanceable set TasksBelowFrontier uses) paired with each repo
// they have recorded activity in — a PR or a task commit; a task with
// neither has nothing to poll. repo/task/since bound the set (spec 013);
// since compares tasks.updated_at against the server clock. A tombstoned task
// is never polled (044 §4) — its delivery state has nowhere to go.
//
// Spec 013 open question 1: this set may be too large for an unscoped
// org-wide run; --since/--repo are the intended controls.
func (s *Store) PollCandidates(ctx context.Context, repo, task string, since *time.Time) ([]PollCandidate, error) {
	q := `SELECT DISTINCT t.id, t.state, x.repo
	      FROM tasks t
	      JOIN (SELECT task_id, repo FROM pull_requests WHERE task_id IS NOT NULL
	            UNION
	            SELECT task_id, repo FROM task_commits) x ON x.task_id = t.id
	      WHERE t.deleted_at IS NULL
	        AND t.state IN ('ready','in_progress','in_review','merged','deployed_dev')`
	var args []any
	if repo != "" {
		args = append(args, repo)
		q += fmt.Sprintf(` AND x.repo = $%d`, len(args))
	}
	if task != "" {
		args = append(args, task)
		q += fmt.Sprintf(` AND t.id = $%d`, len(args))
	}
	if since != nil {
		args = append(args, since.UTC())
		q += fmt.Sprintf(` AND t.updated_at >= $%d`, len(args))
	}
	q += ` ORDER BY t.id, x.repo`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("poll candidates: %w", err)
	}
	defer rows.Close()

	var out []PollCandidate
	for rows.Next() {
		var c PollCandidate
		if err := rows.Scan(&c.TaskID, &c.State, &c.Repo); err != nil {
			return nil, fmt.Errorf("scan poll candidate: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("poll candidates: %w", err)
	}
	return out, nil
}

// RepoIngestion is one mapped repo's ingestion health: what project doctor
// reports (spec 013 §lode project doctor).
type RepoIngestion struct {
	Repo        string
	ProjectID   string
	MappedAt    time.Time
	LastEventAt *time.Time // nil: this repo has never sent a webhook
	EventTypes  []string   // distinct event types seen, sorted
	Unapplied   int        // events still awaiting replay
}

// RepoIngestionHealth returns per-repo ingestion health for every mapped
// repo (or just one, when repo is non-empty), ordered by repo. Events
// correlate to repos by the delivery payload's repository.full_name; this
// scans the events table and is an operator-frequency query, not a hot path.
func (s *Store) RepoIngestionHealth(ctx context.Context, repo string) ([]RepoIngestion, error) {
	q := `SELECT pr.repo, pr.project_id, pr.mapped_at,
	             e.last_event_at, COALESCE(e.event_types, '[]'::jsonb), COALESCE(e.unapplied, 0)
	      FROM project_repos pr
	      LEFT JOIN LATERAL (
	          SELECT max(received_at) AS last_event_at,
	                 jsonb_agg(DISTINCT type) AS event_types,
	                 count(*) FILTER (WHERE applied_at IS NULL) AS unapplied
	          FROM events
	          WHERE source = 'github'
	            AND payload->'repository'->>'full_name' = pr.repo
	      ) e ON true`
	var args []any
	if repo != "" {
		args = append(args, repo)
		q += ` WHERE pr.repo = $1`
	}
	q += ` ORDER BY pr.repo`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("repo ingestion health: %w", err)
	}
	defer rows.Close()

	var out []RepoIngestion
	for rows.Next() {
		var ri RepoIngestion
		var types []byte
		if err := rows.Scan(&ri.Repo, &ri.ProjectID, &ri.MappedAt, &ri.LastEventAt, &types, &ri.Unapplied); err != nil {
			return nil, fmt.Errorf("scan repo ingestion health: %w", err)
		}
		if err := json.Unmarshal(types, &ri.EventTypes); err != nil {
			return nil, fmt.Errorf("decode event types for %s: %w", ri.Repo, err)
		}
		ri.MappedAt = ri.MappedAt.UTC()
		if ri.LastEventAt != nil {
			u := ri.LastEventAt.UTC()
			ri.LastEventAt = &u
		}
		out = append(out, ri)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo ingestion health: %w", err)
	}
	return out, nil
}

// UnlandedTaskCommits returns a task's recorded commit shas in repo that are
// not yet known to be on the default branch (absent from main_commits),
// sorted. These are what the poll engine checks against GitHub.
func (s *Store) UnlandedTaskCommits(ctx context.Context, taskID, repo string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT tc.sha FROM task_commits tc
		 WHERE tc.task_id = $1 AND tc.repo = $2
		   AND NOT EXISTS (SELECT 1 FROM main_commits mc
		                   WHERE mc.repo = tc.repo AND mc.sha = tc.sha)
		 ORDER BY tc.sha`, taskID, repo)
	if err != nil {
		return nil, fmt.Errorf("unlanded commits for %s in %s: %w", taskID, repo, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, fmt.Errorf("scan unlanded commit: %w", err)
		}
		out = append(out, sha)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("unlanded commits for %s in %s: %w", taskID, repo, err)
	}
	return out, nil
}

// KnownMainSHAs returns the subset of shas main_commits already records for
// repo. The poll engine asks GitHub about a commit only to learn whether it
// landed, so a sha already in main_commits has nothing left to learn and the
// request is pure waste — and a recurring one, because the scheduled run is
// org-wide and rate limits are the binding constraint (spec 013 §2.2).
func (s *Store) KnownMainSHAs(ctx context.Context, repo string, shas []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(shas) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT sha FROM main_commits WHERE repo = $1 AND sha = ANY($2)`, repo, shas)
	if err != nil {
		return nil, fmt.Errorf("known main commits in %s: %w", repo, err)
	}
	defer rows.Close()

	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, fmt.Errorf("scan known main commit: %w", err)
		}
		out[sha] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("known main commits in %s: %w", repo, err)
	}
	return out, nil
}

// UnmappedSender is a repo that has sent webhooks but maps to no project.
type UnmappedSender struct {
	Repo        string
	Events      int
	LastEventAt time.Time
}

// UnmappedSenders returns repos seen in github deliveries that have no
// project mapping, ordered by repo.
func (s *Store) UnmappedSenders(ctx context.Context) ([]UnmappedSender, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.payload->'repository'->>'full_name', count(*), max(e.received_at)
		 FROM events e
		 WHERE e.source = 'github'
		   AND e.payload->'repository'->>'full_name' IS NOT NULL
		   AND NOT EXISTS (SELECT 1 FROM project_repos pr
		                   WHERE pr.repo = e.payload->'repository'->>'full_name')
		 GROUP BY 1 ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("unmapped senders: %w", err)
	}
	defer rows.Close()

	var out []UnmappedSender
	for rows.Next() {
		var u UnmappedSender
		if err := rows.Scan(&u.Repo, &u.Events, &u.LastEventAt); err != nil {
			return nil, fmt.Errorf("scan unmapped sender: %w", err)
		}
		u.LastEventAt = u.LastEventAt.UTC()
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("unmapped senders: %w", err)
	}
	return out, nil
}
