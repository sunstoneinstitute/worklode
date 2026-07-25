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
// repos return the default, "merged".
func RepoDoneState(tx *sql.Tx, repo string) (string, error) {
	var st string
	err := tx.QueryRow(`SELECT done_state FROM project_repos WHERE repo = $1`,
		repo).Scan(&st)
	if errors.Is(err, sql.ErrNoRows) {
		return "merged", nil
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
		   AND t.state IN ('ready','in_progress','in_review','merged','deployed_dev')`,
		repo, frontier)
	if err != nil {
		return nil, fmt.Errorf("tasks below frontier %s/%d: %w", repo, frontier, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ResolveDelivery advances taskID to the furthest delivery milestone the
// recorded facts support, forward-only, closing any active lease when the
// work first lands. All lifecycle rules live here; webhook handlers only
// record facts and call this. Safe to call repeatedly and in any
// fact-arrival order. It never advances a draft or abandoned task.
//
// The repo's done_state picks which delivery branch applies. A release-based
// repo follows merged → deployed_dev → released and ignores prod deploys:
// deployed_prod → released is not a legal transition, so advancing on a prod
// deploy would strand the task one hop short of its done_state forever.
// Every other repo follows merged → deployed_dev → deployed_prod.
func ResolveDelivery(tx *sql.Tx, now time.Time, taskID, repo string, eventID int64) error {
	landed, err := LandedMainID(tx, taskID, repo)
	if err != nil {
		return err
	}
	if landed == nil {
		return nil
	}

	state, err := TaskState(tx, taskID)
	if err != nil {
		return err
	}

	switch state {
	case "ready", "in_progress", "in_review":
		if err := Transition(tx, now, taskID, state, "merged", eventID); err != nil {
			return err
		}
		if err := CloseActiveLease(tx, now, taskID); err != nil {
			return err
		}
		state = "merged"
	case "merged", "deployed_dev":
		// Already landed; delivery checks below may advance it further.
	default:
		return nil // draft, abandoned, or already at a delivered state
	}

	covered := func(frontier *int64) bool {
		return frontier != nil && *frontier >= *landed
	}

	if state == "merged" {
		dev, err := ConfirmedFrontier(tx, repo, "dev")
		if err != nil {
			return err
		}
		if covered(dev) {
			if err := Transition(tx, now, taskID, "merged", "deployed_dev", eventID); err != nil {
				return err
			}
			state = "deployed_dev"
		}
	}

	doneState, err := RepoDoneState(tx, repo)
	if err != nil {
		return err
	}
	if doneState == "released" {
		rel, err := ReleaseFrontier(tx, repo)
		if err != nil {
			return err
		}
		if covered(rel) {
			return Transition(tx, now, taskID, state, "released", eventID)
		}
		return nil
	}

	prod, err := ConfirmedFrontier(tx, repo, "prod")
	if err != nil {
		return err
	}
	if covered(prod) {
		return Transition(tx, now, taskID, state, "deployed_prod", eventID)
	}
	return nil
}
