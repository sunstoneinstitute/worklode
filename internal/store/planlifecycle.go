package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// governPlanTasks gives every task minted by the plan that carries no
// governing link the rules the plan's covers edges point at (S5: a closed plan's tasks each
// carry at least one link; S3: the store is the second writer of links, in
// its minimal form). Tasks that already have links are left alone.
//
// Called only while the caller already holds a lock on the plan's docs row
// (settlePlan's FOR NO KEY UPDATE, WithdrawDoc's FOR UPDATE), so two closes
// of the same plan cannot race each other's inserts here.
func governPlanTasks(tx *sql.Tx, planID int64) error {
	rules, err := planRules(tx, planID)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return nil
	}
	rows, err := tx.Query(
		`SELECT t.id FROM tasks t
		  WHERE t.plan_doc = $1 AND t.deleted_at IS NULL
		    AND NOT EXISTS (SELECT 1 FROM task_governed_by g WHERE g.task_id = t.id)`, planID)
	if err != nil {
		return fmt.Errorf("ungoverned tasks of plan %d: %w", planID, err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		for _, c := range rules {
			if err := Govern(tx, id, c, "plan", false); err != nil {
				return fmt.Errorf("govern task %s from plan %d: %w", id, planID, err)
			}
		}
	}
	return nil
}

// settlePlan runs after every task state change: when the task came from a
// plan and that plan has at least one live minted task and no live task left
// open, the plan is executed (WL-REQ-1664). A plan with no minted task
// never gets here with a task, so a coverage-only plan stays accepted. The
// "at least one live task" half of that check is load-bearing, not
// redundant: DeleteTask tombstones a task (deleted_at) before it calls
// transitionKnown to roll it off in_progress, so by the time settlePlan runs
// here the just-deleted task is already invisible to the live-task count.
// Without requiring a live task to exist, deleting the last in-progress task
// of a plan — or the last one if it was the only task minted — would execute
// the plan with nothing delivered.
//
// Cheap for the common case of a task with no plan_doc: one indexed lookup,
// no lock, no further query. The status update below changes no key
// column, so FOR NO KEY UPDATE is enough and leaves FK checks on the row
// unblocked. Deadlock argument against WithdrawDoc's lockDoc: both lock the
// same single docs row (planID), lockDoc with FOR UPDATE and this with FOR NO
// KEY UPDATE, and those two modes conflict.
// WithdrawDoc's path to governPlanTasks does take a row lock on the
// referenced tasks rows (Govern's insert takes FOR KEY SHARE via the
// task_governed_by foreign key), but FOR KEY SHARE does not conflict with
// the FOR NO KEY UPDATE that `UPDATE tasks SET state` takes here, so the
// only lock the two paths actually contend on is docs(planID) — they
// serialize on it, never cycle. taskClosed's own subqueries bind ch, cht,
// tc, mc, pr; "t" is free.
func settlePlan(tx *sql.Tx, now time.Time, taskID string, eventID int64) error {
	var planID sql.NullInt64
	if err := tx.QueryRow(`SELECT plan_doc FROM tasks WHERE id = $1`, taskID).Scan(&planID); err != nil {
		return fmt.Errorf("plan of task %s: %w", taskID, err)
	}
	if !planID.Valid {
		return nil
	}
	status, done, err := planFinished(tx, planID.Int64)
	if err != nil || !done {
		return err
	}
	return executePlan(tx, now, planID.Int64, status, eventID)
}

// planFinished locks plan planID and reports whether it is due to be executed:
// accepted or stale, with at least one live minted task and none open. The
// status it returns is the one executePlan logs as the old value.
func planFinished(tx *sql.Tx, planID int64) (status string, done bool, err error) {
	err = tx.QueryRow(`SELECT status FROM docs WHERE id = $1 AND deleted_at IS NULL FOR NO KEY UPDATE`, planID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("status of plan %d: %w", planID, err)
	}
	if status != "accepted" && status != "stale" {
		return status, false, nil
	}
	var total, open int
	if err := tx.QueryRow(
		`SELECT count(*), count(*) FILTER (WHERE NOT `+taskClosed("t")+`)
		   FROM tasks t WHERE t.plan_doc = $1 AND t.deleted_at IS NULL`,
		planID).Scan(&total, &open); err != nil {
		return status, false, fmt.Errorf("live tasks of plan %d: %w", planID, err)
	}
	return status, total > 0 && open == 0, nil
}

// executePlan governs the plan's tasks and flips it from status to executed,
// attributed to eventID. Callers check planFinished first.
func executePlan(tx *sql.Tx, now time.Time, planID int64, status string, eventID int64) error {
	if err := governPlanTasks(tx, planID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE docs SET status = 'executed', updated_at = $2 WHERE id = $1`,
		planID, now.UTC().Truncate(time.Second)); err != nil {
		return fmt.Errorf("execute plan %d: %w", planID, err)
	}
	return logDocChange(tx, planID, eventID, map[string]string{"field": "status", "old": status, "new": "executed"})
}

// SettleFinishedPlans executes every accepted or stale plan whose minted tasks
// have all closed. settlePlan does this as each task closes, but a plan whose
// tasks closed before that hook existed, or that went stale after they did,
// is reached only here. The sweeper runs it on every tick; it returns the
// number of plans executed.
//
// Each plan is settled in its own transaction: planFinished re-checks under
// the row lock, and the doc.executed event is inserted only once that check
// passes, keyed on (slug, version) so a plan is executed by at most one event.
func (s *Store) SettleFinishedPlans(ctx context.Context) (executed int, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT d.id, d.slug, d.version FROM docs d
		  WHERE d.kind = 'plan' AND d.status IN ('accepted', 'stale') AND d.deleted_at IS NULL
		    AND EXISTS (SELECT 1 FROM tasks t WHERE t.plan_doc = d.id AND t.deleted_at IS NULL)
		    AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.plan_doc = d.id AND t.deleted_at IS NULL
		                       AND NOT `+taskClosed("t")+`)
		  ORDER BY d.id`)
	if err != nil {
		return 0, fmt.Errorf("list finished plans: %w", err)
	}
	type cand struct {
		id      int64
		slug    string
		version int
	}
	cands, err := collectRows(rows, "list finished plans", func(r rowScanner) (cand, error) {
		var c cand
		err := r.Scan(&c.id, &c.slug, &c.version)
		return c, err
	})
	if err != nil {
		return 0, err
	}
	for _, c := range cands {
		err := s.Tx(ctx, func(tx *sql.Tx) error {
			status, done, err := planFinished(tx, c.id)
			if err != nil || !done {
				return err
			}
			payload, err := EventPayload(map[string]any{"doc": c.id, "cause": "sweep"})
			if err != nil {
				return err
			}
			var eventID int64
			err = tx.QueryRowContext(ctx,
				`INSERT INTO events (source, external_id, type, payload, received_at)
				 VALUES ($1, $2, 'doc.executed', $3, $4)
				 ON CONFLICT (source, external_id) DO NOTHING
				 RETURNING id`,
				staleEventSource, "doc.executed:"+c.slug+":"+strconv.Itoa(c.version), payload, s.nowFn().UTC(),
			).Scan(&eventID)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("record doc.executed for plan %d: %w", c.id, err)
			}
			if err := executePlan(tx, s.nowFn(), c.id, status, eventID); err != nil {
				return err
			}
			executed++
			s.metrics.docOp("executed", nil)
			return nil
		})
		if err != nil {
			return executed, fmt.Errorf("settle plan %d: %w", c.id, err)
		}
	}
	return executed, nil
}
