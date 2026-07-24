package store

import (
	"context"
	"fmt"
)

// BlockingFanOut returns, for every task that blocks at least one other task,
// the transitive count of distinct tasks it unblocks over 'blocks' edges. A
// task absent from the returned map has fan-out 0. The count is unit-weight
// over all 'blocks' edges regardless of the blocked task's state (matches
// spec D12; this deliberately does not filter by blocked-task state).
func (s *Store) BlockingFanOut(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE closure(root, task) AS (
		    SELECT from_task, to_task FROM task_edges WHERE type = 'blocks'
		  UNION
		    SELECT c.root, e.to_task
		    FROM closure c JOIN task_edges e ON e.from_task = c.task AND e.type = 'blocks'
		)
		SELECT root, COUNT(DISTINCT task) FROM closure GROUP BY root`)
	if err != nil {
		return nil, fmt.Errorf("blocking fan-out: %w", err)
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var root string
		var count int
		if err := rows.Scan(&root, &count); err != nil {
			return nil, fmt.Errorf("scan blocking fan-out row: %w", err)
		}
		out[root] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("blocking fan-out: %w", err)
	}
	return out, nil
}
