package store

import (
	"database/sql"
	"fmt"
	"slices"
	"time"
)

// Local-merge outcomes, one per reported task. They are the label values of
// the api-side counter, so the set is closed and small.
const (
	LocalMergeAdvanced    = "advanced"     // the attribution is new; delivery re-resolved
	LocalMergeDuplicate   = "duplicate"    // this commit was already attributed to the task
	LocalMergeUnknownTask = "unknown_task" // no such task; nothing recorded
)

// LocalMergeOutcome is what recording one reported task did.
type LocalMergeOutcome struct {
	TaskID string
	Result string
}

// RecordLocalMerge records a merge a developer's own clone observed on the
// default branch: the commit itself, one attribution per task whose work it
// carries, and the delivery resolution those facts imply.
//
// It makes exactly the three calls applyMainPush makes for a webhook push —
// AppendMainCommit, InsertTaskCommit, ResolveDelivery — so two reporters
// asserting the same merge produce one fact under one rule. The dedup is
// AppendMainCommit's ON CONFLICT plus InsertTaskCommit's: whichever reporter
// arrives second changes nothing.
//
// Source is "local_merge" and not "merge_message" on purpose: a delivery
// asserted by a laptop, from a branch-ancestry probe rather than a signed
// webhook, is weaker evidence, and the log should say which reporter made the
// claim.
//
// ResolveDelivery runs even for a duplicate. It is idempotent and reads only
// facts, so the cost is a query, and it heals the case where an attribution
// landed in an earlier report whose resolution did not.
func RecordLocalMerge(tx *sql.Tx, now time.Time, repo, sha string, taskIDs []string, eventID int64) ([]LocalMergeOutcome, error) {
	if _, err := AppendMainCommit(tx, repo, sha, now); err != nil {
		return nil, err
	}
	prior, err := TaskIDsForSHA(tx, repo, sha)
	if err != nil {
		return nil, err
	}
	// Sorted and deduplicated so a caller repeating an id cannot report the
	// same task twice, and so the state_log ordering is reproducible — the
	// same reason applyMainPush sorts its affected set.
	ids := slices.Clone(taskIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)

	out := make([]LocalMergeOutcome, 0, len(ids))
	var movedIDs []string
	for _, taskID := range ids {
		exists, err := taskExists(tx, taskID)
		if err != nil {
			return nil, err
		}
		if !exists {
			out = append(out, LocalMergeOutcome{TaskID: taskID, Result: LocalMergeUnknownTask})
			continue
		}
		result := LocalMergeAdvanced
		if slices.Contains(prior, taskID) {
			result = LocalMergeDuplicate
		}
		if err := InsertTaskCommit(tx, TaskCommit{
			TaskID: taskID, Repo: repo, SHA: sha,
			Source: "local_merge", SeenAt: now,
		}); err != nil {
			return nil, err
		}
		moved, err := ResolveDelivery(tx, now, taskID, repo, eventID)
		if err != nil {
			return nil, fmt.Errorf("resolve delivery for %s: %w", taskID, err)
		}
		if moved {
			movedIDs = append(movedIDs, taskID)
		}
		out = append(out, LocalMergeOutcome{TaskID: taskID, Result: result})
	}
	// The transitions above record no event of their own, so this event is
	// the only place the moved set exists (WL-SPEC-66 §5.1).
	if len(movedIDs) > 0 {
		if err := MergeEventPayload(tx, eventID, map[string]any{"tasks": movedIDs}); err != nil {
			return nil, err
		}
	}
	return out, nil
}
