// project_work.go provides a single bulk, UI-neutral read of everything the
// project cockpit and the legacy board need about a project's tasks: the
// task itself, its parent (if any), its open blockers, its active lease (if
// any), and the most recent state-change event (if any). It replaces the
// per-task ListTasks + BlockedTaskIDs + ParentMap + ActiveLease assembly
// those two callers used to do themselves, with one round trip.
//
// The types here carry only declared facts — no product language (mode,
// health, readiness). Mapping facts into the cockpit's product-facing shape
// is the next layer's job (see cockpit.go), not this one's.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// EventFact is the event behind a fact: enough provenance (source, type,
// when) to say where a value came from, without the full event payload.
type EventFact struct {
	ID     int64
	Source string
	Type   string
	At     time.Time
}

// TaskRef is a minimal reference to another task — enough to link and label
// it without pulling the full Task.
type TaskRef struct {
	ID    string
	Title string
	State string
}

// ProjectWorkFact is one task's full read-side state: the task itself, its
// parent (nil if it has none), its open blockers (never nil — an empty
// slice when there are none), its active lease (nil if unleased), and the
// newest state-change event recorded against it (nil for a task that has
// never transitioned).
type ProjectWorkFact struct {
	Task         Task
	Parent       *TaskRef
	OpenBlockers []TaskRef
	Lease        *Lease
	StateEvent   *EventFact
}

// Blocked reports whether the task has at least one open blocker.
func (f ProjectWorkFact) Blocked() bool { return len(f.OpenBlockers) > 0 }

// ListProjectWorkFacts returns a ProjectWorkFact for every task in
// projectID, ordered the same way ListTasks orders its results (priority
// rank, then the id's project-key prefix, then its numeric suffix).
// projectID == "" returns every task across every project.
//
// Parent, Lease, and StateEvent come from a single joined query (a task has
// at most one child_of parent — task_edges_single_parent — at most one
// active lease — leases_active — and the state-log lookup is bounded to its
// newest row), so that part needs no fan-out. OpenBlockers is a second,
// separate query (an edge fan-out the first query cannot express without
// duplicating task rows), merged in afterward.
func (s *Store) ListProjectWorkFacts(ctx context.Context, projectID string) (out []ProjectWorkFact, err error) {
	defer func() { s.metrics.projectWorkRead(err) }()

	rows, err := s.db.QueryContext(ctx, `
SELECT `+prefixedTaskColumns("t")+`,
       parent.id, parent.title, parent.state,
       l.id, l.task_id, l.actor_id, l.worktree, l.acquired_at, l.renewed_at,
       l.expires_at, l.released_at,
       se.id, se.source, se.type, se.at
  FROM tasks t
  LEFT JOIN task_edges pe
    ON pe.from_task = t.id AND pe.type = 'child_of'
  LEFT JOIN tasks parent ON parent.id = pe.to_task
  LEFT JOIN leases l ON l.task_id = t.id AND l.released_at IS NULL
  LEFT JOIN LATERAL (
    SELECT sl.id, e.source, e.type, sl.at
      FROM state_log sl
      JOIN events e ON e.id = sl.event_id
     WHERE sl.entity_kind = 'task'
       AND sl.entity_id = t.id
       AND sl.change->>'field' = 'state'
     ORDER BY sl.at DESC, sl.id DESC
     LIMIT 1
  ) se ON true
 WHERE ($1 = '' OR t.project_id = $1)
 ORDER BY CASE t.priority
            WHEN 'critical' THEN 0 WHEN 'high' THEN 1
            WHEN 'medium' THEN 2 ELSE 3
          END,
          split_part(t.id, '-', 1),
          CAST(split_part(t.id, '-', 2) AS INTEGER)`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project work facts: %w", err)
	}
	defer rows.Close()

	// facts is keyed by task id so the OpenBlockers pass below can attach to
	// the right fact by id; ordered preserves the query's own ordering for
	// the returned slice.
	facts := make(map[string]*ProjectWorkFact)
	var ordered []*ProjectWorkFact
	for rows.Next() {
		f, err := scanProjectWorkFact(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project work fact: %w", err)
		}
		f.OpenBlockers = []TaskRef{}
		facts[f.Task.ID] = f
		ordered = append(ordered, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list project work facts: %w", err)
	}

	if err := s.attachOpenBlockers(ctx, projectID, facts); err != nil {
		return nil, err
	}

	out = make([]ProjectWorkFact, len(ordered))
	for i, f := range ordered {
		out[i] = *f
	}
	return out, nil
}

// attachOpenBlockers fills in OpenBlockers on every fact in facts (keyed by
// task id) from open 'blocks' edges whose dependent task is in scope for
// projectID ("" meaning every project). "Open" uses the same predicate as
// blockedCondition: the blocker's state is not one of closedStates. The
// blocker itself need not be in the same project as its dependent — 'blocks'
// edges, unlike child_of, are not project-scoped (see AddEdge).
func (s *Store) attachOpenBlockers(ctx context.Context, projectID string, facts map[string]*ProjectWorkFact) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT e.to_task, b.id, b.title, b.state
  FROM task_edges e
  JOIN tasks b ON b.id = e.from_task
  JOIN tasks dep ON dep.id = e.to_task
 WHERE e.type = 'blocks'
   AND b.state NOT IN `+closedStates+`
   AND ($1 = '' OR dep.project_id = $1)`, projectID)
	if err != nil {
		return fmt.Errorf("open blockers: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var depID string
		var ref TaskRef
		if err := rows.Scan(&depID, &ref.ID, &ref.Title, &ref.State); err != nil {
			return fmt.Errorf("scan open blocker: %w", err)
		}
		if f, ok := facts[depID]; ok {
			f.OpenBlockers = append(f.OpenBlockers, ref)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("open blockers: %w", err)
	}
	return nil
}

// scanProjectWorkFact scans one row of the ListProjectWorkFacts query: the
// task columns (same shape and order as scanTask, duplicated rather than
// composed because a *sql.Rows Scan call must consume every column of the
// row in one call — scanTask's fixed 14-destination Scan cannot be reused
// against a wider, joined row), followed by the nullable parent, lease, and
// state-event columns.
func scanProjectWorkFact(row rowScanner) (*ProjectWorkFact, error) {
	var t Task
	var body, createdBy, concern, assignee sql.NullString
	var skillsJSON string

	var parentID, parentTitle, parentState sql.NullString

	var leaseID sql.NullInt64
	var leaseTaskID, leaseActorID, leaseWorktree sql.NullString
	var leaseAcquiredAt, leaseRenewedAt, leaseExpiresAt, leaseReleasedAt sql.NullTime

	var eventID sql.NullInt64
	var eventSource, eventType sql.NullString
	var eventAt sql.NullTime

	if err := row.Scan(
		&t.ID, &t.ProjectID, &t.Title, &body, &t.Priority, &t.Kind,
		&t.State, &concern, &assignee, &t.NeedsDecomposition, &createdBy,
		&t.CreatedAt, &t.UpdatedAt, &skillsJSON,
		&parentID, &parentTitle, &parentState,
		&leaseID, &leaseTaskID, &leaseActorID, &leaseWorktree,
		&leaseAcquiredAt, &leaseRenewedAt, &leaseExpiresAt, &leaseReleasedAt,
		&eventID, &eventSource, &eventType, &eventAt,
	); err != nil {
		return nil, err
	}

	t.Body = body.String
	t.Concern = concern.String
	t.Assignee = assignee.String
	t.CreatedBy = createdBy.String
	t.CreatedAt = t.CreatedAt.UTC()
	t.UpdatedAt = t.UpdatedAt.UTC()
	if err := json.Unmarshal([]byte(skillsJSON), &t.Skills); err != nil {
		return nil, fmt.Errorf("unmarshal task %s skills: %w", t.ID, err)
	}
	if t.Skills == nil {
		t.Skills = []string{}
	}

	f := &ProjectWorkFact{Task: t}

	if parentID.Valid {
		f.Parent = &TaskRef{ID: parentID.String, Title: parentTitle.String, State: parentState.String}
	}

	if leaseID.Valid {
		l := &Lease{
			ID:         leaseID.Int64,
			TaskID:     leaseTaskID.String,
			ActorID:    leaseActorID.String,
			Worktree:   leaseWorktree.String,
			AcquiredAt: leaseAcquiredAt.Time.UTC(),
			ExpiresAt:  leaseExpiresAt.Time.UTC(),
		}
		if leaseRenewedAt.Valid {
			l.RenewedAt = leaseRenewedAt.Time.UTC()
		}
		if leaseReleasedAt.Valid {
			releasedAt := leaseReleasedAt.Time.UTC()
			l.ReleasedAt = &releasedAt
		}
		f.Lease = l
	}

	if eventID.Valid {
		f.StateEvent = &EventFact{
			ID:     eventID.Int64,
			Source: eventSource.String,
			Type:   eventType.String,
			At:     eventAt.Time.UTC(),
		}
	}

	return f, nil
}
