// Task hierarchy (docs/specs/004-execution-backbone.md): a container is
// inferred — a task that has child_of children — a task has at most one
// parent, and a chain is at most maxHierarchyDepth edges deep. Progress is
// derived on read; closure is stored as real transitions, attributed to the
// triggering event, by ResolveHierarchy.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// maxHierarchyDepth caps a child_of chain at two edges, now spanning task ->
// subtask only (spec 004 §6.1). The brief is a bounded payload and the walks
// that feed roll-up and breadcrumbs are unbounded without a cap.
const maxHierarchyDepth = 2

// hasChildren reports whether id has any child_of children. It is the
// container predicate every guard keys off, container-ness being inferred
// rather than declared: with decompose creating parent-hood and its children
// in one transaction, "has children" is exactly as sharp as a column
// (004 §6.1).
// Served by the task_edges_children partial index.
func hasChildren(tx *sql.Tx, id string) (bool, error) {
	var one int
	err := tx.QueryRow(
		`SELECT 1 FROM task_edges WHERE to_task = $1 AND type = 'child_of' LIMIT 1`,
		id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("children of %s: %w", id, err)
	}
	return true, nil
}

// parentOf returns id's parent via its child_of edge. The single-parent index
// makes that at most one row, so ok reports whether id has a parent at all —
// every walk over the hierarchy goes through here.
func parentOf(tx *sql.Tx, id string) (parent string, ok bool, err error) {
	err = tx.QueryRow(
		`SELECT to_task FROM task_edges WHERE from_task = $1 AND type = 'child_of'`,
		id).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("parent of %s: %w", id, err)
	}
	return parent, true, nil
}

// ancestorHops returns the number of child_of edges between id and the root of
// its hierarchy (0 for a task with no parent). The visited set keeps the walk
// terminating even if the stored graph already contains a cycle.
func ancestorHops(tx *sql.Tx, id string) (int, error) {
	visited := map[string]bool{id: true}
	hops, cur := 0, id
	for {
		parent, ok, err := parentOf(tx, cur)
		if err != nil {
			return 0, err
		}
		if !ok {
			return hops, nil
		}
		if visited[parent] {
			return hops, nil
		}
		visited[parent] = true
		hops++
		cur = parent
	}
}

// descendantDepth returns the length of the longest child_of chain below id
// (0 for a task with no children). It queries one level at a time (all of a
// level's children in a single ANY($1) round trip) and stops as soon as the
// chain is known to exceed maxHierarchyDepth, since checkHierarchy only needs
// to know the cap is blown, not by how much. The single-parent index makes
// child_of a forest, which is what lets a shared-visited BFS return the
// longest chain rather than just the level of first reachability.
func descendantDepth(tx *sql.Tx, id string) (int, error) {
	visited := map[string]bool{id: true}
	depth := 0
	frontier := []string{id}
	for len(frontier) > 0 {
		rows, err := tx.Query(
			`SELECT from_task FROM task_edges WHERE to_task = ANY($1) AND type = 'child_of'`,
			frontier)
		if err != nil {
			return 0, fmt.Errorf("walk children below %s: %w", id, err)
		}
		var next []string
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return 0, fmt.Errorf("scan child below %s: %w", id, err)
			}
			if !visited[k] {
				visited[k] = true
				next = append(next, k)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return 0, fmt.Errorf("walk children below %s: %w", id, err)
		}
		rows.Close()
		if len(next) == 0 {
			break
		}
		depth++
		if depth > maxHierarchyDepth {
			return depth, nil
		}
		frontier = next
	}
	return depth, nil
}

// checkHierarchy validates a proposed "child child_of parent" edge against the
// spec-004 invariants. project carries both endpoints' project_id, already
// read by AddEdge. Any ordinary task may be a parent — 029 §2 left no kind to
// declare, and the edge itself is what makes the parent a container.
func checkHierarchy(tx *sql.Tx, child, parent string, project map[string]string) error {
	if project[child] != project[parent] {
		return fmt.Errorf("cross-project edge %s (%s) child_of %s (%s): %w",
			child, project[child], parent, project[parent], ErrInvalidInput)
	}

	existing, hasParent, err := parentOf(tx, child)
	if err != nil {
		return err
	}
	if hasParent {
		return fmt.Errorf("task %s already has parent %s: %w", child, existing, ErrEdgeExists)
	}

	reaches, err := reachesViaChildOf(tx, parent, child)
	if err != nil {
		return err
	}
	if reaches {
		return fmt.Errorf("edge %s child_of %s: %w", child, parent, ErrCycle)
	}

	above, err := ancestorHops(tx, parent)
	if err != nil {
		return err
	}
	below, err := descendantDepth(tx, child)
	if err != nil {
		return err
	}
	if depth := above + 1 + below; depth > maxHierarchyDepth {
		return fmt.Errorf("edge %s child_of %s would make a %d-edge chain (max %d): %w",
			child, parent, depth, maxHierarchyDepth, ErrInvalidInput)
	}
	return nil
}

// HierarchyProgress is a parent's derived roll-up: how many of its direct
// children are closed, out of how many. It is computed on read and never
// stored — there is no resolver, no migration, and no event-log noise behind
// it.
type HierarchyProgress struct {
	Closed int
	Total  int
}

// ChildProgress returns the closed/total counts over taskID's direct children.
// A task with no children reports a zero value.
func (s *Store) ChildProgress(ctx context.Context, taskID string) (HierarchyProgress, error) {
	var p HierarchyProgress
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE t.state IN `+closedStates+`)
		   FROM task_edges e JOIN tasks t ON t.id = e.from_task
		  WHERE e.to_task = $1 AND e.type = 'child_of'`, taskID).Scan(&p.Total, &p.Closed)
	if err != nil {
		return HierarchyProgress{}, fmt.Errorf("child progress of %s: %w", taskID, err)
	}
	return p, nil
}

// ParentOf returns taskID's parent, or nil when it has none. Only ID, Title,
// and State are populated: one hop up is all any caller needs, and the full
// ancestry is unbounded.
func (s *Store) ParentOf(ctx context.Context, taskID string) (*Task, error) {
	var p Task
	err := s.db.QueryRowContext(ctx,
		`SELECT t.id, t.title, t.state
		   FROM task_edges e JOIN tasks t ON t.id = e.to_task
		  WHERE e.from_task = $1 AND e.type = 'child_of'`, taskID).Scan(&p.ID, &p.Title, &p.State)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("parent of %s: %w", taskID, err)
	}
	return &p, nil
}

// ParentMap returns child id -> parent id for every child_of edge in a project
// (every project when projectID is ""). One query, so a board can group a
// parent's children under it without a lookup per task.
func (s *Store) ParentMap(ctx context.Context, projectID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.from_task, e.to_task
		   FROM task_edges e JOIN tasks t ON t.id = e.from_task
		  WHERE e.type = 'child_of' AND ($1 = '' OR t.project_id = $1)`, projectID)
	if err != nil {
		return nil, fmt.Errorf("parent map: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var child, parent string
		if err := rows.Scan(&child, &parent); err != nil {
			return nil, fmt.Errorf("scan parent map row: %w", err)
		}
		out[child] = parent
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("parent map: %w", err)
	}
	return out, nil
}

// Decompose creates parentID's children in one transaction: needs_decomposition
// clears and each title becomes a draft child inheriting the parent's project,
// priority, concern, and kind. The parent's kind is not touched (004 §6.10) —
// it is the child_of edges that make it a container. This is what makes the
// spec-005 needs_decomposition gate actionable — an oversized task becomes its
// own tracking task plus the pieces, in place, keeping its id and every
// reference to it.
//
// Rejected when the parent already has children (spec 004's decompose is for
// splitting an oversized task, not for re-splitting a container — add further
// children with AddEdge instead), when it holds an active lease (decomposing
// work someone is holding is a coordination bug), when it sits deep enough
// that its children would exceed maxHierarchyDepth, and from the delivery
// states a task with children can never occupy.
func Decompose(tx *sql.Tx, now time.Time, parentID string, titles []string, createdBy string, eventID int64) ([]Task, error) {
	if len(titles) == 0 {
		return nil, fmt.Errorf("decompose %s: at least one child title is required: %w",
			parentID, ErrInvalidInput)
	}
	trimmed := make([]string, len(titles))
	for i, title := range titles {
		tt := strings.TrimSpace(title)
		if tt == "" {
			return nil, fmt.Errorf("decompose %s: child titles must not be blank: %w",
				parentID, ErrInvalidInput)
		}
		trimmed[i] = tt
	}

	var projectID, priority, state, kind string
	var concern sql.NullString
	var wasFlagged bool
	err := tx.QueryRow(
		`SELECT project_id, priority, state, kind, concern, needs_decomposition
		   FROM tasks WHERE id = $1 FOR UPDATE`,
		parentID).Scan(&projectID, &priority, &state, &kind, &concern, &wasFlagged)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task %s: %w", parentID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("lock task %s: %w", parentID, err)
	}
	if containerForbiddenStates[state] {
		return nil, fmt.Errorf("task %s is in state %s and cannot take children: %w",
			parentID, state, ErrBadTransition)
	}
	already, err := hasChildren(tx, parentID)
	if err != nil {
		return nil, err
	}
	if already {
		return nil, fmt.Errorf("task %s already has children; add more to it instead: %w",
			parentID, ErrInvalidInput)
	}

	leased, err := hasActiveLease(tx, parentID)
	if err != nil {
		return nil, err
	}
	if leased {
		lease, err := activeLeaseTx(tx, parentID)
		if err != nil {
			return nil, fmt.Errorf("get active lease on %s: %w", parentID, err)
		}
		return nil, fmt.Errorf("task %s is held by %s in %s; release it before decomposing: %w",
			parentID, lease.ActorID, lease.Worktree, ErrLeased)
	}

	above, err := ancestorHops(tx, parentID)
	if err != nil {
		return nil, err
	}
	if above+1 > maxHierarchyDepth {
		return nil, fmt.Errorf("task %s already sits at the deepest allowed level (max %d edges); decompose one of its ancestors instead: %w",
			parentID, maxHierarchyDepth, ErrInvalidInput)
	}

	if _, err := tx.Exec(
		`UPDATE tasks SET needs_decomposition = false, updated_at = $1 WHERE id = $2`,
		now.UTC(), parentID); err != nil {
		return nil, fmt.Errorf("clear needs_decomposition on %s: %w", parentID, err)
	}
	// Only when the flag was actually set: decompose is legal on an unflagged
	// task, and a change row claiming true -> false there would be a lie. The
	// child_of edges are the record that the split happened.
	if wasFlagged {
		if err := LogChange(tx, "task", parentID, eventID,
			map[string]string{"field": "needs_decomposition", "old": "true", "new": "false"}); err != nil {
			return nil, err
		}
	}

	children := make([]Task, 0, len(trimmed))
	for _, title := range trimmed {
		child, err := CreateTask(tx, now, TaskInput{
			ProjectID: projectID,
			Title:     title,
			Priority:  priority,
			Kind:      kind,
			Concern:   concern.String,
			CreatedBy: createdBy,
			Draft:     true,
		})
		if err != nil {
			return nil, err
		}
		if err := AddEdge(tx, now, child.ID, parentID, "child_of"); err != nil {
			return nil, err
		}
		children = append(children, *child)
	}

	// The fresh children are all draft, so this only pulls a parent that was
	// mid-flight back to where its children put it.
	if err := ResolveHierarchy(tx, now, parentID, eventID); err != nil {
		return nil, err
	}
	return children, nil
}
