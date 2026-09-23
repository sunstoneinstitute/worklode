package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// governPlanTasks gives every task minted by the plan that carries no
// governing link the plan's arranged clauses (S5: a closed plan's tasks each
// carry at least one link; S3: the store is the second writer of links, in
// its minimal form). Tasks that already have links are left alone.
//
// Called only while the caller already holds a lock on the plan's docs row
// (settlePlan's FOR NO KEY UPDATE, WithdrawDoc's FOR UPDATE), so two closes
// of the same plan cannot race each other's inserts here.
func governPlanTasks(tx *sql.Tx, planID int64) error {
	clauses, err := planClauses(tx, planID)
	if err != nil {
		return err
	}
	if len(clauses) == 0 {
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
		for _, c := range clauses {
			if err := Govern(tx, id, c, "plan", false); err != nil {
				return fmt.Errorf("govern task %s from plan %d: %w", id, planID, err)
			}
		}
	}
	return nil
}

// settlePlan runs after every task state change: when the task came from a
// plan and that plan has at least one live minted task and no live task left
// open, the plan is spent (S5, increment 3 R4). A plan with no minted task
// never gets here with a task, so a coverage-only plan stays accepted. The
// "at least one live task" half of that check is load-bearing, not
// redundant: DeleteTask tombstones a task (deleted_at) before it calls
// transitionKnown to roll it off in_progress, so by the time settlePlan runs
// here the just-deleted task is already invisible to the live-task count.
// Without requiring a live task to exist, deleting the last in-progress task
// of a plan — or the last one if it was the only task minted — would spend
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
	var status string
	err := tx.QueryRow(`SELECT status FROM docs WHERE id = $1 AND deleted_at IS NULL FOR NO KEY UPDATE`, planID.Int64).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("status of plan %d: %w", planID.Int64, err)
	}
	if status != "accepted" && status != "stale" {
		return nil
	}
	var total, open int
	if err := tx.QueryRow(
		`SELECT count(*), count(*) FILTER (WHERE NOT `+taskClosed("t")+`)
		   FROM tasks t WHERE t.plan_doc = $1 AND t.deleted_at IS NULL`,
		planID.Int64).Scan(&total, &open); err != nil {
		return fmt.Errorf("live tasks of plan %d: %w", planID.Int64, err)
	}
	if total == 0 || open > 0 {
		return nil
	}
	if err := governPlanTasks(tx, planID.Int64); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE docs SET status = 'spent', updated_at = $2 WHERE id = $1`,
		planID.Int64, now.UTC().Truncate(time.Second)); err != nil {
		return fmt.Errorf("spend plan %d: %w", planID.Int64, err)
	}
	return logDocChange(tx, planID.Int64, eventID, map[string]string{"field": "status", "old": status, "new": "spent"})
}
