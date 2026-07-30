// Hierarchy resolver: the single place the spec-018 epic roll-up table lives.
// Progress (closed/total) is derived on read in hierarchy.go; closure is
// stored as real transitions, attributed to the triggering event, by
// ResolveHierarchy. Transition itself calls the resolver, so every state
// change rolls its parent up.

package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// epicTarget returns the state the spec-018 roll-up table implies for an epic
// whose direct children are in the given states, or "" when no roll-up
// applies. An epic with no children never moves: that is a modelling mistake,
// not a completed epic. All-abandoned rolls up to abandoned rather than
// merged — treating abandonment as delivery would report cancelled work as
// shipped.
func epicTarget(states []string) string {
	if len(states) == 0 {
		return ""
	}
	closed, abandoned, started := 0, 0, 0
	for _, st := range states {
		if closedStateSet[st] {
			closed++
			if st == "abandoned" {
				abandoned++
			}
		}
		if st == "in_progress" || st == "in_review" {
			started++
		}
	}
	switch {
	// Must precede the closed==len arm — abandoned is itself a closed state.
	case closed == len(states) && abandoned == len(states):
		return "abandoned"
	case closed == len(states):
		return "merged"
	case started > 0 || closed > 0:
		return "in_progress"
	default:
		return "ready"
	}
}

// ResolveHierarchy moves parentID to the state its children imply, per the
// spec-018 roll-up table, inside the given transaction. Non-epics and draft
// epics are left alone: draft -> ready is a manual publish, not a roll-up.
//
// A closed epic whose children reopened routes through ready, the only edge
// out of a closed state, so the reopen shows in the timeline as a reopen.
// Both transitions carry the triggering child's eventID, which is the correct
// attribution for a derived move.
func ResolveHierarchy(tx *sql.Tx, now time.Time, parentID string, eventID int64) error {
	var state, kind string
	err := tx.QueryRow(`SELECT state, kind FROM tasks WHERE id = $1`, parentID).Scan(&state, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("task %s: %w", parentID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("get parent %s: %w", parentID, err)
	}
	if kind != kindEpic || state == "draft" {
		return nil
	}

	states, err := childStates(tx, parentID)
	if err != nil {
		return err
	}
	target := epicTarget(states)
	if target == "" || target == state {
		return nil
	}

	// No legal path to target: leave the epic where it is rather than fail the
	// child's transition that triggered this.
	if !legalTransitions[[2]string{state, target}] {
		if !legalTransitions[[2]string{state, "ready"}] {
			return nil
		}
		if err := Transition(tx, now, parentID, state, "ready", eventID); err != nil {
			return err
		}
		state = "ready"
		// Unreachable today (ready reaches every target epicTarget produces);
		// kept so a new target state fails closed rather than driving an
		// illegal transition.
		if !legalTransitions[[2]string{state, target}] {
			return nil
		}
	}
	return Transition(tx, now, parentID, state, target, eventID)
}

// childStates returns the states of parentID's direct child_of children, in
// no particular order — epicTarget only counts them.
func childStates(tx *sql.Tx, parentID string) ([]string, error) {
	rows, err := tx.Query(
		`SELECT t.state FROM task_edges e JOIN tasks t ON t.id = e.from_task
		  WHERE e.to_task = $1 AND e.type = 'child_of'`, parentID)
	if err != nil {
		return nil, fmt.Errorf("children of %s: %w", parentID, err)
	}
	defer rows.Close()
	var states []string
	for rows.Next() {
		var st string
		if err := rows.Scan(&st); err != nil {
			return nil, fmt.Errorf("scan child state of %s: %w", parentID, err)
		}
		states = append(states, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("children of %s: %w", parentID, err)
	}
	return states, nil
}

// resolveParent rolls the task's parent, if it has one, up to the state its
// children imply. Transition calls this rather than its call sites doing so:
// hooking each caller would leave the invariant one forgotten call site away
// from breaking. Recursion terminates because checkHierarchy caps a child_of
// chain at maxHierarchyDepth edges — a subtask resolves its task, the task
// resolves the epic, the epic has no parent.
func resolveParent(tx *sql.Tx, now time.Time, taskID string, eventID int64) error {
	parent, ok, err := parentOf(tx, taskID)
	if err != nil || !ok {
		return err
	}
	return ResolveHierarchy(tx, now, parent, eventID)
}
