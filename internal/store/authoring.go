package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// typeDocumentSubmitted is eventbus.TypeDocumentSubmitted, which store cannot
// import (eventbus imports store).
const typeDocumentSubmitted = "wl:DocumentSubmitted"

// checkAuthoringReviewable refuses in_review for a design task with no pull
// request and no submitted document it wrote. Nothing would move such a task
// on: in_review ends when a PR merges, and a design task's document closes it
// only once submitted (WL-REQ-1629).
func checkAuthoringReviewable(tx *sql.Tx, taskID string) error {
	var stuck bool
	err := tx.QueryRow(
		`SELECT t.kind = 'design'
		    AND NOT EXISTS (SELECT 1 FROM github_pull_requests p
		                    WHERE p.task_id = t.id AND p.state IN ('open', 'merged'))
		    AND NOT EXISTS (SELECT 1 FROM events e
		                    WHERE e.type = $2
		                      AND e.payload->>'wl:subject' IN
		                          (SELECT `+docIRIExpr+` FROM docs
		                           WHERE generated_by_task = t.id AND deleted_at IS NULL))
		   FROM tasks t WHERE t.id = $1`,
		taskID, typeDocumentSubmitted).Scan(&stuck)
	if err != nil {
		return fmt.Errorf("check task %s reviewable: %w", taskID, err)
	}
	if stuck {
		return fmt.Errorf("design task %s has no pull request and no submitted document: "+
			"submit the document it wrote with `lode doc submit <ref>`, which closes the task (WL-REQ-1629), "+
			"or open its pull request: %w", taskID, ErrBadTransition)
	}
	return nil
}

// CloseAuthoringTask closes the open design task that wrote docID, inside the
// transaction that records the document's submission (WL-REQ-1629). A
// document with no authoring task, or one whose task is not an open design
// task, is left alone.
func CloseAuthoringTask(tx *sql.Tx, now time.Time, docID, eventID int64) error {
	var taskID, state string
	err := tx.QueryRow(
		`SELECT t.id, t.state FROM docs d JOIN tasks t ON t.id = d.generated_by_task
		  WHERE d.id = $1 AND t.kind = 'design' AND t.deleted_at IS NULL
		    AND t.state IN ('ready', 'in_progress', 'in_review')
		  FOR UPDATE OF t`, docID).Scan(&taskID, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("authoring task of doc %d: %w", docID, err)
	}
	return transitionKnown(tx, now, taskID, state, state, "merged", eventID)
}
