package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// scanInstruction expects columns in the order id, task_id, body,
// COALESCE(created_by, empty string), created_at — the RETURNING list
// ClaimPendingInstructionsForActor's query below uses.
func scanInstruction(row rowScanner) (model.Instruction, error) {
	var in model.Instruction
	if err := row.Scan(&in.ID, &in.Task, &in.Body, &in.CreatedBy, &in.CreatedAt); err != nil {
		return model.Instruction{}, err
	}
	in.CreatedAt = in.CreatedAt.UTC()
	return in, nil
}

// EnqueueInstruction queues a steering instruction against taskID: an
// operator-authored message delivered to whichever actor next claims the
// task's lease (migration 0052). Recorded as a "task.instructed" cli event.
//
// Errors: ErrNotFound if taskID does not exist or is soft-deleted (same
// tombstone rule as Claim, 044 §4), or if actorID does not name an actor.
func (s *Store) EnqueueInstruction(ctx context.Context, taskID, actorID, body string) (*model.Instruction, error) {
	extID, err := randomExternalID()
	if err != nil {
		return nil, err
	}
	payload, err := EventPayload(map[string]any{"task": taskID, "actor": actorID})
	if err != nil {
		return nil, err
	}

	var instr *model.Instruction
	_, _, err = s.RecordEvent(ctx, "cli", extID, "task.instructed", payload,
		func(tx *sql.Tx, eventID int64) error {
			var one int
			if err := tx.QueryRow(
				`SELECT 1 FROM tasks WHERE id = $1 AND deleted_at IS NULL`, taskID,
			).Scan(&one); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("task %s: %w", taskID, ErrNotFound)
				}
				return fmt.Errorf("check task %s: %w", taskID, err)
			}
			if err := requireActor(tx, actorID); err != nil {
				return err
			}

			now := s.nowFn().UTC().Truncate(time.Second)
			row := tx.QueryRow(
				`INSERT INTO task_instructions (task_id, body, created_by, created_at)
				 VALUES ($1, $2, $3, $4)
				 RETURNING id, created_at`,
				taskID, body, actorID, now,
			)
			var id int64
			var createdAt time.Time
			if err := row.Scan(&id, &createdAt); err != nil {
				return fmt.Errorf("insert instruction on %s: %w", taskID, err)
			}
			instr = &model.Instruction{
				ID:        id,
				Task:      taskID,
				Body:      body,
				CreatedBy: actorID,
				CreatedAt: createdAt.UTC(),
			}
			return nil
		})
	s.metrics.instruction("enqueue", outcome(err))
	if err != nil {
		return nil, err
	}
	return instr, nil
}

// ClaimPendingInstructionsForActor delivers every undelivered instruction
// queued against a task actorID currently leases: delivered_at is stamped
// with now, and the delivered rows are returned in id order.
//
// No event is recorded — delivered_at is itself the durable record, and an
// event per poll (a 3-second cadence) would be pure log noise. The join
// scoping "tasks this actor currently leases" mirrors the ownership check
// Claim/heldLease use elsewhere in this package: an active lease is
// released_at IS NULL, additionally scoped to actor_id.
func (s *Store) ClaimPendingInstructionsForActor(ctx context.Context, actorID string) ([]model.Instruction, error) {
	var out []model.Instruction
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		now := s.nowFn().UTC().Truncate(time.Second)
		rows, err := tx.Query(
			// ponytail: plain UPDATE serializes concurrent claims by
			// blocking on contended rows; switch to FOR UPDATE SKIP LOCKED
			// if concurrent relays per actor become normal.
			`UPDATE task_instructions ti
			    SET delivered_at = $1
			   FROM leases l
			  WHERE l.task_id = ti.task_id
			    AND l.actor_id = $2
			    AND l.released_at IS NULL
			    AND ti.delivered_at IS NULL
			  RETURNING ti.id, ti.task_id, ti.body, COALESCE(ti.created_by, ''), ti.created_at`,
			now, actorID,
		)
		if err != nil {
			return fmt.Errorf("claim pending instructions for %s: %w", actorID, err)
		}
		out, err = collectRows(rows, fmt.Sprintf("claim pending instructions for %s", actorID), scanInstruction)
		return err
	})
	s.metrics.instruction("claim", outcome(err))
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	s.metrics.deliverInstructions(len(out))
	return out, nil
}
