package store

import (
	"context"
	"errors"
	"fmt"
)

// Brief is the bounded payload an agent needs to start work on a task: the
// task row, its conventional branch, the open blockers still pointing at it,
// and the active lease (nil when the task is unleased). It is deliberately
// bounded — no file contents, no unbounded lists — so a brief is one cheap,
// predictable read.
//
// GoverningDesign, AffectedComponents, and DefinitionOfDone are reserved for
// spec 006 (Deliverable/design links) and stay nil in v1; the shape is fixed
// now so the wire contract does not change when they are populated.
//
// Parent is exactly one hop up — an agent should know its task belongs to
// "Delivery lifecycle" without spelunking, while the full ancestry and the
// sibling list are both unbounded and stay out.
type Brief struct {
	Task               Task     // the task row
	Body               string   // task body (mirrors Task.Body for the wire contract)
	Branch             string   // <prefix><id>-<slug>
	OpenBlockers       []Task   // open 'blocks' edges pointing at this task; only ID/Title/State are populated
	Parent             *Task    // the task's epic, or nil; only ID/Title/State are populated
	Lease              *Lease   // active lease, or nil
	GoverningDesign    *string  // reserved: spec 006 (nil in v1)
	AffectedComponents []string // reserved: spec 006 (nil in v1)
	DefinitionOfDone   *string  // reserved: spec 006 Deliverable (nil in v1)
}

// Brief assembles the brief for taskID: the task row, its branch, its open
// blockers, and any active lease. Returns ErrNotFound if the task does not
// exist. It runs a bounded, fixed number of queries and never returns file
// contents or unbounded lists.
func (s *Store) Brief(ctx context.Context, taskID string) (*Brief, error) {
	t, err := s.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}

	blockers, err := s.openBlockers(ctx, taskID)
	if err != nil {
		return nil, err
	}

	lease, err := s.ActiveLease(ctx, taskID)
	if errors.Is(err, ErrNotFound) {
		lease = nil
	} else if err != nil {
		return nil, err
	}

	parent, err := s.ParentOf(ctx, taskID)
	if err != nil {
		return nil, err
	}

	return &Brief{
		Task:         *t,
		Body:         t.Body,
		Branch:       BranchFor(t),
		OpenBlockers: blockers,
		Parent:       parent,
		Lease:        lease,
	}, nil
}

// openBlockers returns the tasks that are the from_task of an open 'blocks'
// edge whose to_task is taskID — i.e. the blockers still blocking it. "Open"
// uses the same predicate as blockedCondition: the blocker's state is not one
// of closedStates. Only ID, Title, and State are populated (the brief surfaces no
// more than that). Ordered by numeric id for a stable payload.
func (s *Store) openBlockers(ctx context.Context, taskID string) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.title, t.state
		   FROM task_edges e
		   JOIN tasks t ON t.id = e.from_task
		  WHERE e.to_task = $1
		    AND e.type = 'blocks'
		    AND t.state NOT IN `+closedStates+`
		  ORDER BY CAST(split_part(t.id, '-', 2) AS INTEGER)`, taskID)
	if err != nil {
		return nil, fmt.Errorf("open blockers of %s: %w", taskID, err)
	}
	defer rows.Close()

	var out []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Title, &t.State); err != nil {
			return nil, fmt.Errorf("scan open blocker: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("open blockers of %s: %w", taskID, err)
	}
	return out, nil
}
