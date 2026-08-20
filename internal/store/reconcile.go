// Reconciliation queries (docs/specs/013-reconciliation.md): the applied_at
// completion marker, the replay candidate set, and the ingestion-health and
// poll-candidate reads added by later tasks in the same plan.

package store

import (
	"context"
	"database/sql"
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

// UnappliedFilter bounds the replay candidate set. Zero values disable each
// filter. Repo matches the delivery payload's repository.full_name.
type UnappliedFilter struct {
	Repo  string
	Since *time.Time
}

// UnappliedGitHubEvents returns github-source events whose apply has not
// completed — *.ignored deliveries and anything the replayer has not reached
// yet — oldest first, so replay preserves arrival order.
func (s *Store) UnappliedGitHubEvents(ctx context.Context, f UnappliedFilter) ([]Event, error) {
	where := "source = 'github' AND applied_at IS NULL"
	var args sqlArgs
	if f.Repo != "" {
		where += " AND payload->'repository'->>'full_name' = " + args.next(f.Repo)
	}
	if f.Since != nil {
		where += " AND received_at >= " + args.next(f.Since.UTC())
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+eventColumns+`
		   FROM events
		  WHERE `+where+`
		  ORDER BY id`, args.vals...)
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
// since compares tasks.updated_at against the server clock.
//
// Spec 013 open question 1: this set may be too large for an unscoped
// org-wide run; --since/--repo are the intended controls.
func (s *Store) PollCandidates(ctx context.Context, repo, task string, since *time.Time) ([]PollCandidate, error) {
	q := `SELECT DISTINCT t.id, t.state, x.repo
	      FROM tasks t
	      JOIN (SELECT task_id, repo FROM pull_requests WHERE task_id IS NOT NULL
	            UNION
	            SELECT task_id, repo FROM task_commits) x ON x.task_id = t.id
	      WHERE t.state IN ('ready','in_progress','in_review','merged','deployed_dev')`
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
