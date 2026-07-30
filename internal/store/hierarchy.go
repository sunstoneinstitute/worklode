// Task hierarchy (docs/specs/018-task-hierarchy.md): epics are declared
// containers, a task has at most one parent, and a chain is at most
// maxHierarchyDepth edges deep. Progress is derived on read; closure is
// stored, one transition per event, by ResolveHierarchy.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// maxHierarchyDepth caps a child_of chain at two edges (epic -> task ->
// subtask). The brief is a bounded payload and the walks that feed roll-up and
// breadcrumbs are unbounded without a cap.
const maxHierarchyDepth = 2

// ancestorHops returns the number of child_of edges between id and the root of
// its hierarchy (0 for a task with no parent). The visited set keeps the walk
// terminating even if the stored graph already contains a cycle.
func ancestorHops(tx *sql.Tx, id string) (int, error) {
	visited := map[string]bool{id: true}
	hops, cur := 0, id
	for {
		var parent string
		err := tx.QueryRow(
			`SELECT to_task FROM task_edges WHERE from_task = $1 AND type = 'child_of'`,
			cur).Scan(&parent)
		if errors.Is(err, sql.ErrNoRows) {
			return hops, nil
		}
		if err != nil {
			return 0, fmt.Errorf("walk parents of %s: %w", cur, err)
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
// spec-018 invariants. project and kind carry both endpoints' columns, already
// read by AddEdge.
func checkHierarchy(tx *sql.Tx, child, parent string, project, kind map[string]string) error {
	if kind[parent] != "epic" {
		return fmt.Errorf("parent %s is a %s, not an epic: %w", parent, kind[parent], ErrInvalidInput)
	}
	if project[child] != project[parent] {
		return fmt.Errorf("cross-project edge %s (%s) child_of %s (%s): %w",
			child, project[child], parent, project[parent], ErrInvalidInput)
	}

	var existing string
	err := tx.QueryRow(
		`SELECT to_task FROM task_edges WHERE from_task = $1 AND type = 'child_of'`,
		child).Scan(&existing)
	if err == nil {
		return fmt.Errorf("task %s already has parent %s: %w", child, existing, ErrEdgeExists)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check parent of %s: %w", child, err)
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

// closedStateSet mirrors the closedStates SQL tuple for in-Go checks. Both
// must list the same states.
var closedStateSet = map[string]bool{
	"merged": true, "deployed_dev": true, "deployed_prod": true,
	"released": true, "abandoned": true,
}

// HierarchyProgress is an epic's derived roll-up: how many of its direct
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
// (every project when projectID is ""). One query, so a board can group an
// epic's children under it without a lookup per task.
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
