// Hierarchy resolver: the single place the spec-004 §6.5 roll-up table lives.
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

// childState is one direct child as the roll-up sees it: its state, and
// whether it counts as closed. Closedness is per repo (taskClosed, 004 §1.3)
// and so cannot be read off the state alone — a merged child in a repo that
// gates on released has not finished delivering.
type childState struct {
	State  string
	Closed bool
}

// containerTarget returns the state the spec-004 §6.5 roll-up table implies
// for a task with the given direct children, or "" when no roll-up applies. A
// task with no children never moves: it is an ordinary task and stays where it
// is. All-abandoned rolls up to abandoned rather than merged — treating
// abandonment as delivery would report cancelled work as shipped.
//
// "Started" is every child past ready, not only the two in-flight states: a
// child that has landed but is still delivering has plainly started, and
// counting it as un-started would send the parent back to ready.
func containerTarget(children []childState) string {
	if len(children) == 0 {
		return ""
	}
	closed, abandoned, started := 0, 0, 0
	for _, c := range children {
		if c.Closed {
			closed++
			if c.State == "abandoned" {
				abandoned++
			}
		}
		if c.State != "draft" && c.State != "ready" {
			started++
		}
	}
	switch {
	// Must precede the closed==len arm — abandoned is itself a closed state.
	case closed == len(children) && abandoned == len(children):
		return "abandoned"
	case closed == len(children):
		return "merged"
	case started > 0 || closed > 0:
		return "in_progress"
	default:
		return "ready"
	}
}

// ResolveHierarchy moves parentID to the state its children imply, per the
// spec-004 §6.5 roll-up table, inside the given transaction. A draft parent is
// left alone: draft -> ready is a manual publish, not a roll-up. A task with
// no children is left alone by containerTarget, which is what keeps this a
// no-op for an ordinary task now that container-ness is inferred (029 §2)
// rather than declared.
//
// A closed parent whose children reopened routes through ready, the only edge
// out of a closed state, so the reopen shows in the timeline as a reopen.
// Both transitions carry the triggering child's eventID, which is the correct
// attribution for a derived move.
//
// A tombstoned parent is left alone (044 §4): its edges survive the delete, so
// a live child transitioning would otherwise move — and emit events against —
// a row nothing can see.
func ResolveHierarchy(tx *sql.Tx, now time.Time, parentID string, eventID int64) error {
	var state string
	var deleted bool
	err := tx.QueryRow(`SELECT state, deleted_at IS NOT NULL FROM tasks WHERE id = $1`,
		parentID).Scan(&state, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("task %s: %w", parentID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("get parent %s: %w", parentID, err)
	}
	if deleted || state == "draft" {
		return nil
	}

	children, err := childStates(tx, parentID)
	if err != nil {
		return err
	}
	target := containerTarget(children)
	if target == "" || target == state {
		return nil
	}

	// No legal path to target: leave the parent where it is rather than fail
	// the child's transition that triggered this.
	if !legalTransitions[[2]string{state, target}] {
		if !legalTransitions[[2]string{state, "ready"}] {
			return nil
		}
		if err := Transition(tx, now, parentID, state, "ready", eventID); err != nil {
			return err
		}
		state = "ready"
		// Unreachable today (ready reaches every target containerTarget produces);
		// kept so a new target state fails closed rather than driving an
		// illegal transition.
		if !legalTransitions[[2]string{state, target}] {
			return nil
		}
	}
	return Transition(tx, now, parentID, state, target, eventID)
}

// childStates returns parentID's direct child_of children — state plus the
// per-repo closed verdict — in no particular order, since containerTarget only
// counts them. Tombstoned children are not children for this purpose (044 §4),
// so deleting the last unfinished one closes the parent. Closedness comes from
// the same taskClosed predicate the blocking queries and ChildProgress use, so
// a roll-up and the progress counts read at the same moment agree. They can still drift afterwards: the roll-up
// stores a state and only Transition re-runs it, while taskClosed also depends
// on project_repos.done_state and the landed commit set, either of which can
// change with no task transition to trigger a re-resolve.
func childStates(tx *sql.Tx, parentID string) ([]childState, error) {
	rows, err := tx.Query(
		`SELECT t.state, `+taskClosed("t")+`
		   FROM task_edges e JOIN tasks t ON t.id = e.from_task
		  WHERE e.to_task = $1 AND e.type = 'child_of'
		    AND t.deleted_at IS NULL`, parentID)
	if err != nil {
		return nil, fmt.Errorf("children of %s: %w", parentID, err)
	}
	return collectRows(rows, fmt.Sprintf("children of %s", parentID), func(r rowScanner) (childState, error) {
		var c childState
		if err := r.Scan(&c.State, &c.Closed); err != nil {
			return childState{}, err
		}
		return c, nil
	})
}

// resolveParent rolls the task's parent, if it has one, up to the state its
// children imply. Transition calls this rather than its call sites doing so:
// hooking each caller would leave the invariant one forgotten call site away
// from breaking. Recursion terminates because checkHierarchy caps a child_of
// chain at maxHierarchyDepth edges — a subtask resolves its task, and that
// task has no parent.
func resolveParent(tx *sql.Tx, now time.Time, taskID string, eventID int64) error {
	parent, ok, err := parentOf(tx, taskID)
	if err != nil || !ok {
		return err
	}
	return ResolveHierarchy(tx, now, parent, eventID)
}
