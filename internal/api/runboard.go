// runboard.go is the run board's pure fact-to-group classifier (032 §8; see
// docs/plans/2026-08-27-project-cockpit-3-run-board.md). It has no store
// calls and no HTTP: it only turns a store.ProjectWorkFact into the group it
// belongs in.
package api

import "github.com/sunstoneinstitute/worklode/internal/store"

// runGroup is 032 §8's grouping of live work, in the spec's own order.
// runGroupNone marks a task the board excludes (draft: not execution yet).
type runGroup int

const (
	runGroupNone runGroup = iota
	runGroupReady
	runGroupRunning
	runGroupWaiting
	runGroupJudgment // "Needs judgment"
	runGroupFailed
	runGroupCompleted
)

// runGroupOf classifies one task's facts per the pinned table in the plan's
// Global Constraints. Blockedness is f.Blocked() — the claim path's own
// predicate — and "running" requires the active lease ListProjectWorkFacts
// attaches, so an in_progress task whose lease expired lands in Needs
// judgment rather than lying about a worker that is gone.
func runGroupOf(f store.ProjectWorkFact) runGroup {
	switch f.Task.State {
	case "merged", "deployed_dev", "deployed_prod", "released":
		return runGroupCompleted
	case "abandoned":
		return runGroupFailed
	case "in_review":
		return runGroupJudgment
	case "in_progress":
		if f.Lease != nil {
			return runGroupRunning
		}
		return runGroupJudgment
	case "ready":
		if f.Blocked() {
			return runGroupWaiting
		}
		return runGroupReady
	default:
		return runGroupNone
	}
}
