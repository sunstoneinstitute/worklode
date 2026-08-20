// project_work.go provides a single bulk, UI-neutral read of everything the
// project cockpit and the legacy board need about a project's tasks: the
// task itself, its parent (if any), what holds it (open blocker tasks and
// unfinished blocking plans), its active lease (if any), and the most recent
// state-change event (if any). It replaces the per-task ListTasks +
// BlockedTaskIDs + ParentMap + ActiveLease assembly those two callers used to
// do themselves, with one round trip.
//
// The types here carry only declared facts — no product language (mode,
// health, readiness). Mapping facts into the cockpit's product-facing shape
// is the next layer's job (see cockpit.go), not this one's.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
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
// parent (nil if it has none), its open blockers (never nil — an empty slice
// when there are none), the plans holding it, its active lease (nil if
// unleased), and the newest state-change event recorded against it (nil for a
// task that has never transitioned).
type ProjectWorkFact struct {
	Task         model.Task
	Parent       *TaskRef
	OpenBlockers []TaskRef
	// BlockingPlans are the unfinished plan documents ordered before this
	// task's plan (025 §9.3). A blocking plan still draft has minted no task,
	// so it appears here with nothing in OpenBlockers.
	BlockingPlans []model.DocRef
	Lease         *Lease
	StateEvent    *EventFact
}

// Blocked reports whether anything holds the task: an open blocker task, or a
// plan ordered before its plan. It answers the question IsBlocked answers on
// the claim path, so the board and Claim cannot disagree about what is
// pickable.
func (f ProjectWorkFact) Blocked() bool {
	return len(f.OpenBlockers) > 0 || len(f.BlockingPlans) > 0
}

// ListProjectWorkFacts returns a ProjectWorkFact for every task in
// projectID, ordered the same way ListTasks orders its results (priority
// rank, then the id's project-key prefix, then its numeric suffix).
// projectID == "" returns every task across every project. Tombstoned tasks
// are out, and a tombstoned parent is not named (044 §4).
//
// Parent, Lease, and StateEvent come from a single joined query (a task has
// at most one child_of parent — task_edges_single_parent — at most one
// active lease — leases_active — and the state-log lookup is bounded to its
// newest row), so that part needs no fan-out. OpenBlockers and BlockingPlans
// are two further queries (fan-outs the first query cannot express without
// duplicating task rows), merged in afterward.
//
// The state-log lookup requires an "old" key so CreateTask's own state_log
// row (edit style, no "old" — see CreateTask) never counts as a transition:
// StateEvent stays nil until the task's first real move.
func (s *Store) ListProjectWorkFacts(ctx context.Context, projectID string) (out []ProjectWorkFact, err error) {
	defer func() { s.metrics.projectWorkRead(err) }()

	rows, err := s.db.QueryContext(ctx, `
SELECT `+taskColumnsT+`,
       parent.id, parent.title, parent.state,
       l.id, l.task_id, l.actor_id, l.worktree, l.acquired_at, l.renewed_at,
       l.expires_at, l.released_at,
       se.id, se.source, se.type, se.at
  FROM tasks t
  LEFT JOIN task_edges pe
    ON pe.from_task = t.id AND pe.type = 'child_of'
  LEFT JOIN tasks parent ON parent.id = pe.to_task AND parent.deleted_at IS NULL
  LEFT JOIN leases l ON l.task_id = t.id AND l.released_at IS NULL
  LEFT JOIN LATERAL (
    SELECT sl.id, e.source, e.type, sl.at
      FROM state_log sl
      JOIN events e ON e.id = sl.event_id
     WHERE sl.entity_kind = 'task'
       AND sl.entity_id = t.id
       AND sl.change->>'field' = 'state'
       AND sl.change ? 'old'
     ORDER BY sl.at DESC, sl.id DESC
     LIMIT 1
  ) se ON true
 WHERE ($1 = '' OR t.project_id = $1)
   AND t.deleted_at IS NULL
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

	// facts is keyed by task id so the two passes below can attach to the
	// right fact by id; ordered preserves the query's own ordering for
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
	if err := s.attachBlockingPlans(ctx, projectID, facts); err != nil {
		return nil, err
	}

	out = make([]ProjectWorkFact, len(ordered))
	for i, f := range ordered {
		out[i] = *f
	}
	return out, nil
}

// attachOpenBlockers fills in OpenBlockers on every fact in facts (keyed by
// task id) with the open tasks holding a dependent in scope for projectID (""
// meaning every project): the from_task of a 'blocks' edge, and the open tasks
// of any plan ordered before the dependent's plan (025 §9.3). "Open" uses the
// same predicate as blockedCondition and planBlockedCondition: the blocker is
// live and has not reached its repo's done_state (taskClosed). The blocker
// itself need not be in the same project as its dependent — 'blocks' edges,
// unlike child_of, are not project-scoped (see AddEdge).
//
// A blocking plan still draft has minted no task and so names none here; it
// reaches the fact through attachBlockingPlans instead.
func (s *Store) attachOpenBlockers(ctx context.Context, projectID string, facts map[string]*ProjectWorkFact) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT e.to_task, b.id, b.title, b.state
  FROM task_edges e
  JOIN tasks b ON b.id = e.from_task
  JOIN tasks dep ON dep.id = e.to_task
 WHERE e.type = 'blocks'
   AND b.deleted_at IS NULL
   AND NOT `+taskClosed("b")+`
   AND dep.deleted_at IS NULL
   AND ($1 = '' OR dep.project_id = $1)
UNION
SELECT dep.id, b.id, b.title, b.state
  FROM tasks dep
  JOIN doc_edges de ON de.type = 'blocks' AND de.to_doc = dep.plan_doc
  JOIN tasks b ON b.plan_doc = de.from_doc
 WHERE dep.plan_doc IS NOT NULL
   AND b.deleted_at IS NULL
   AND NOT `+taskClosed("b")+`
   AND dep.deleted_at IS NULL
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

// attachBlockingPlans fills in BlockingPlans on every fact in facts: the
// unfinished plans ordered before the fact's own plan (planUnfinished, the
// predicate planBlockedCondition gates the ready set on). It is what makes
// Blocked() answer for a plan whose task set is unminted — a draft blocker has
// no task for attachOpenBlockers to name, and a task shown as pickable that
// Claim then refuses is worse than no badge at all.
func (s *Store) attachBlockingPlans(ctx context.Context, projectID string, facts map[string]*ProjectWorkFact) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT dep.id, bd.id, bd.slug, bd.title, bd.status
  FROM tasks dep
  JOIN doc_edges de ON de.type = 'blocks' AND de.to_doc = dep.plan_doc
  JOIN docs bd ON bd.id = de.from_doc
 WHERE dep.plan_doc IS NOT NULL
   AND dep.deleted_at IS NULL
   AND ($1 = '' OR dep.project_id = $1)
   AND `+planUnfinished("bd")+`
 ORDER BY 2`, projectID)
	if err != nil {
		return fmt.Errorf("blocking plans: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var depID string
		var ref model.DocRef
		if err := rows.Scan(&depID, &ref.ID, &ref.Slug, &ref.Title, &ref.Status); err != nil {
			return fmt.Errorf("scan blocking plan: %w", err)
		}
		if f, ok := facts[depID]; ok {
			f.BlockingPlans = append(f.BlockingPlans, ref)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("blocking plans: %w", err)
	}
	return nil
}

// scanProjectWorkFact scans one row of the ListProjectWorkFacts query: the
// task columns, read by scanTask through appendScan so the joined row's extra
// destinations ride along in the one Scan call *sql.Rows requires, followed by
// the nullable parent, lease, and state-event columns.
func scanProjectWorkFact(row rowScanner) (*ProjectWorkFact, error) {
	var parentID, parentTitle, parentState sql.NullString

	var leaseID sql.NullInt64
	var leaseTaskID, leaseActorID, leaseWorktree sql.NullString
	var leaseAcquiredAt, leaseRenewedAt, leaseExpiresAt, leaseReleasedAt sql.NullTime

	var eventID sql.NullInt64
	var eventSource, eventType sql.NullString
	var eventAt sql.NullTime

	t, err := scanTask(appendScan{row, []any{
		&parentID, &parentTitle, &parentState,
		&leaseID, &leaseTaskID, &leaseActorID, &leaseWorktree,
		&leaseAcquiredAt, &leaseRenewedAt, &leaseExpiresAt, &leaseReleasedAt,
		&eventID, &eventSource, &eventType, &eventAt,
	}})
	if err != nil {
		return nil, err
	}
	// scanTask derives Branch; this projection has always shipped it empty
	// and the board's wire shape is not this change's business. WL-183 picks
	// which side is right.
	t.Branch = ""

	f := &ProjectWorkFact{Task: *t}

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
