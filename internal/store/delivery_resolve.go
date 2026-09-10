// Delivery resolver: the single place delivery lifecycle rules live.
// Webhook handlers record facts (internal/store/delivery.go) and call
// ResolveDelivery, which advances the task to the furthest milestone those
// facts support — forward-only and independent of event arrival order.

package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RepoDoneState returns the done_state configured on the repo mapping — the
// terminal state that counts as fully delivered for that repo. Unmapped
// repos return the default.
func RepoDoneState(tx *sql.Tx, repo string) (string, error) {
	var st string
	err := tx.QueryRow(`SELECT done_state FROM project_repos WHERE repo = $1`,
		repo).Scan(&st)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultDoneState, nil
	}
	if err != nil {
		return "", fmt.Errorf("done_state for %s: %w", repo, err)
	}
	return st, nil
}

// TasksBelowFrontier returns ids of tasks whose landed main commit in repo
// is at or below frontier and whose state can still advance. Used by
// frontier-moving handlers to find affected tasks.
func TasksBelowFrontier(tx *sql.Tx, repo string, frontier int64) ([]string, error) {
	rows, err := tx.Query(
		`SELECT DISTINCT tc.task_id FROM task_commits tc
		 JOIN main_commits mc ON mc.repo = tc.repo AND mc.sha = tc.sha
		 JOIN tasks t ON t.id = tc.task_id
		 WHERE tc.repo = $1 AND mc.id <= $2
		   AND t.deleted_at IS NULL
		   AND t.state IN ('ready','in_progress','in_review','merged','deployed_dev')`,
		repo, frontier)
	if err != nil {
		return nil, fmt.Errorf("tasks below frontier %s/%d: %w", repo, frontier, err)
	}
	return scanColumn[string](rows, fmt.Sprintf("tasks below frontier %s/%d", repo, frontier))
}

// ResolveDelivery advances taskID to the furthest delivery milestone the
// recorded facts support, forward-only. All lifecycle rules live here;
// webhook handlers only record facts and call this. Safe to call repeatedly
// and in any fact-arrival order. It never advances a draft or abandoned task.
//
// Delivery never touches the lease: a lease records that a worktree is
// occupied, and landing, deploying or releasing the code says nothing about
// that (spec 004 §3). Leases end on release, abandon, reopen, or the expiry
// sweep. Mutual exclusion is unaffected — Claim requires state "ready", so a
// delivered task cannot be claimed out from under its holder anyway.
//
// The repo's done_state picks which delivery branch applies. A release-based
// repo follows merged → deployed_dev → released and ignores prod deploys:
// deployed_prod → released is not a legal transition, so advancing on a prod
// deploy would strand the task one hop short of its done_state forever.
// Every other repo follows merged → deployed_dev → deployed_prod. The
// asymmetry is deliberate: a done_state of "merged" still advances past
// merged when deploy facts exist, because "merged" is also the default for
// repos discovery has not profiled yet — real deploy signals outrank it.
//
// Reopening a task clears its commit attribution (ClearTaskCommits), so a
// reopened task has no landed commit here and is left alone until new work
// lands.
//
// moved reports whether the task actually changed state. The transitions
// here write a state_log row attributed to the incoming event and record no
// event of their own, so the caller is the only place that can name the
// tasks a delivery event moved — which it merges onto that event's payload
// for the Progress page to fan out (WL-SPEC-66 §5.1).
func ResolveDelivery(tx *sql.Tx, now time.Time, taskID, repo string, eventID int64) (bool, error) {
	var moved bool
	// A task with children has no commit of its own (004 §6.4), and an
	// unknown task id is a correlation miss that must not fail the delivery
	// (InsertTaskCommit's contract); both return nil here rather than an
	// error. A tombstoned task joins them (044 §4): its commits still land,
	// but nothing advances a row nothing can see.
	//
	// FOR UPDATE because this read is the from-state of up to two transitions
	// below (transitionKnown), and because a resolve is a read-then-write on
	// the task either way: without the lock a concurrent writer could move the
	// task between the read and the UPDATE. Every other delivery-state writer
	// locks the task row first too, so the lock order is unchanged.
	var state string
	if err := tx.QueryRow(
		`SELECT state FROM tasks WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, taskID).Scan(&state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("get task %s state: %w", taskID, err)
	}
	container, err := hasChildren(tx, taskID)
	if err != nil {
		return false, err
	}
	if container {
		return false, nil
	}

	landed, err := LandedMainID(tx, taskID, repo)
	if err != nil {
		return false, err
	}
	if landed == nil {
		return false, nil
	}

	switch state {
	case "ready", "in_progress", "in_review":
		if err := transitionKnown(tx, now, taskID, state, state, "merged", eventID); err != nil {
			return false, err
		}
		state, moved = "merged", true
	case "merged", "deployed_dev":
		// Already landed; delivery checks below may advance it further.
	default:
		return false, nil // draft, abandoned, or already at a delivered state
	}

	covered := func(frontier *int64) bool {
		return frontier != nil && *frontier >= *landed
	}

	if state == "merged" {
		dev, err := ConfirmedFrontier(tx, repo, "dev")
		if err != nil {
			return moved, err
		}
		if covered(dev) {
			if err := transitionKnown(tx, now, taskID, state, "merged", "deployed_dev", eventID); err != nil {
				return moved, err
			}
			state, moved = "deployed_dev", true
		}
	}

	doneState, err := RepoDoneState(tx, repo)
	if err != nil {
		return moved, err
	}
	if doneState == "released" {
		rel, err := ReleaseFrontier(tx, repo)
		if err != nil {
			return moved, err
		}
		if covered(rel) {
			if err := transitionKnown(tx, now, taskID, state, state, "released", eventID); err != nil {
				return moved, err
			}
			moved = true
		}
		return moved, nil
	}

	prod, err := ConfirmedFrontier(tx, repo, "prod")
	if err != nil {
		return moved, err
	}
	if covered(prod) {
		if err := transitionKnown(tx, now, taskID, state, state, "deployed_prod", eventID); err != nil {
			return moved, err
		}
		moved = true
	}
	return moved, nil
}
