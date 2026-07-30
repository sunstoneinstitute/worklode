// Task hierarchy (docs/specs/018-task-hierarchy.md): epics are declared
// containers, a task has at most one parent, and a chain is at most
// maxHierarchyDepth edges deep. Progress is derived on read; closure is
// stored, one transition per event, by ResolveHierarchy.

package store

import (
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
// (0 for a task with no children).
func descendantDepth(tx *sql.Tx, id string) (int, error) {
	visited := map[string]bool{id: true}
	depth := 0
	frontier := []string{id}
	for len(frontier) > 0 {
		var next []string
		for _, cur := range frontier {
			kids, err := childIDs(tx, cur)
			if err != nil {
				return 0, err
			}
			for _, k := range kids {
				if !visited[k] {
					visited[k] = true
					next = append(next, k)
				}
			}
		}
		if len(next) > 0 {
			depth++
		}
		frontier = next
	}
	return depth, nil
}

// childIDs returns the ids of a task's direct children.
func childIDs(tx *sql.Tx, id string) ([]string, error) {
	rows, err := tx.Query(
		`SELECT from_task FROM task_edges WHERE to_task = $1 AND type = 'child_of'`, id)
	if err != nil {
		return nil, fmt.Errorf("walk children of %s: %w", id, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("scan child of %s: %w", id, err)
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("walk children of %s: %w", id, err)
	}
	return out, nil
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
