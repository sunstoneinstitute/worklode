package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
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

// prefixedTaskColumns returns taskColumns with each column qualified by
// alias, for queries that join tasks against other tables. Splits naively on
// ", ", so every taskColumns entry must stay comma-free.
func prefixedTaskColumns(alias string) string {
	cols := strings.Split(taskColumns, ", ")
	for i, c := range cols {
		cols[i] = alias + "." + c
	}
	return strings.Join(cols, ", ")
}

// readyCandidates returns every task eligible for pickup: state ready, not an
// epic, not needs_decomposition, unleased, and not blocked by an open 'blocks'
// edge from a task that is not in a closed state. An empty projectID matches
// every project. Epics are excluded because the worktree is the unit of
// Worklode work and a container has nothing to check out (spec 018).
func (s *Store) readyCandidates(ctx context.Context, projectID string) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+prefixedTaskColumns("t")+` FROM tasks t
		WHERE t.state = 'ready'
		  AND t.kind <> 'epic'
		  AND NOT t.needs_decomposition
		  AND ($1 = '' OR t.project_id = $1)
		  AND NOT EXISTS (SELECT 1 FROM leases l
		                  WHERE l.task_id = t.id AND l.released_at IS NULL)
		  AND NOT EXISTS (SELECT 1 FROM task_edges e
		                  WHERE e.to_task = t.id AND `+blockedCondition+`)`, projectID)
	if err != nil {
		return nil, fmt.Errorf("ready candidates: %w", err)
	}
	defer rows.Close()

	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan ready candidate: %w", err)
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ready candidates: %w", err)
	}
	return out, nil
}

// projectFocusMap returns each project's focus, keyed by project id, for the
// given set of project ids in one query.
func (s *Store) projectFocusMap(ctx context.Context, projectIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(projectIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, focus FROM projects WHERE id = ANY($1)`, projectIDs)
	if err != nil {
		return nil, fmt.Errorf("project focus map: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, fmt.Errorf("scan project focus: %w", err)
		}
		focus, err := scanProjectFocus(raw)
		if err != nil {
			return nil, fmt.Errorf("project %s focus: %w", id, err)
		}
		out[id] = focus
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("project focus map: %w", err)
	}
	return out, nil
}

// rankInput carries the per-candidate inputs rankTasks needs beyond the task
// itself: the ranking concern index depends on the task's own project focus
// (a claim-next call spanning multiple projects uses each task's own
// project), and fan-out is precomputed by BlockingFanOut.
type rankInput struct {
	Task   Task
	Focus  []string // the task's project focus
	FanOut int
}

// concernRank returns the index of concern in focus (lower is more
// in-focus). A concern not listed in focus, or an empty concern, sorts last.
func concernRank(concern string, focus []string) int {
	if concern == "" {
		return math.MaxInt
	}
	for i, c := range focus {
		if c == concern {
			return i
		}
	}
	return math.MaxInt
}

// priorityRank maps a task priority to its sort weight, lower first.
func priorityRank(p string) int {
	switch p {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return math.MaxInt
	}
}

// numericTaskID parses the numeric suffix of a <KEY>-<n> id for tiebreaking.
// SW-9 must sort before SW-10, which a plain string compare gets wrong. A
// malformed id sorts last rather than panicking.
func numericTaskID(id string) int {
	if _, n, ok := splitTaskID(id); ok {
		return n
	}
	return math.MaxInt
}

// rankTasks orders candidates by the spec-02 key:
//
//	default:      (is_critical desc, concern_rank asc, priority asc, fan_out desc)
//	strict-focus: (concern_rank asc, priority asc, fan_out desc)
//
// tiebreak: created_at asc, then numeric id asc. The sort is stable and its
// inputs are pure, so identical input always yields identical output.
func rankTasks(in []rankInput, strictFocus bool) []Task {
	ranked := make([]rankInput, len(in))
	copy(ranked, in)
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if !strictFocus {
			aCrit := a.Task.Priority == "critical"
			bCrit := b.Task.Priority == "critical"
			if aCrit != bCrit {
				return aCrit
			}
		}
		if aConcern, bConcern := concernRank(a.Task.Concern, a.Focus), concernRank(b.Task.Concern, b.Focus); aConcern != bConcern {
			return aConcern < bConcern
		}
		if aPrio, bPrio := priorityRank(a.Task.Priority), priorityRank(b.Task.Priority); aPrio != bPrio {
			return aPrio < bPrio
		}
		if a.FanOut != b.FanOut {
			return a.FanOut > b.FanOut
		}
		if !a.Task.CreatedAt.Equal(b.Task.CreatedAt) {
			return a.Task.CreatedAt.Before(b.Task.CreatedAt)
		}
		return numericTaskID(a.Task.ID) < numericTaskID(b.Task.ID)
	})
	out := make([]Task, len(ranked))
	for i, r := range ranked {
		out[i] = r.Task
	}
	return out
}

// ClaimNextOpts configures ClaimNext.
type ClaimNextOpts struct {
	ProjectID   string
	StrictFocus bool
	DryRun      bool
	Worktree    string
	ActorID     string
	TTL         time.Duration
}

// ClaimNextResult reports the outcome of a ClaimNext call. Task and FanOut
// are set whenever a candidate was found, whether or not it was actually
// claimed (DryRun, or Claimed). Lease is nil unless a real claim succeeded.
type ClaimNextResult struct {
	Claimed bool
	Task    *Task
	FanOut  int
	Lease   *Lease
}

// ClaimNext ranks the ready set (see readyCandidates and rankTasks) and
// atomically claims the top candidate via Claim, falling through to the next
// ranked candidate whenever a claim loses the race (ErrLeased, ErrBlocked,
// ErrBadTransition) — Claim's own transaction is what makes each attempt
// atomic; ClaimNext just retries in rank order. An empty ready set is not an
// error: it returns Claimed=false, Task=nil. DryRun returns the top-ranked
// candidate without writing anything (no lease, no state change).
func (s *Store) ClaimNext(ctx context.Context, opts ClaimNextOpts) (*ClaimNextResult, error) {
	candidates, err := s.readyCandidates(ctx, opts.ProjectID)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		s.metrics.claim("claim_next", "none")
		return &ClaimNextResult{Claimed: false}, nil
	}

	fanOut, err := s.BlockingFanOut(ctx)
	if err != nil {
		return nil, err
	}

	var projectIDs []string
	seen := map[string]bool{}
	for _, t := range candidates {
		if !seen[t.ProjectID] {
			seen[t.ProjectID] = true
			projectIDs = append(projectIDs, t.ProjectID)
		}
	}
	focusByProject, err := s.projectFocusMap(ctx, projectIDs)
	if err != nil {
		return nil, err
	}

	rin := make([]rankInput, len(candidates))
	for i, t := range candidates {
		rin[i] = rankInput{
			Task:   t,
			Focus:  focusByProject[t.ProjectID],
			FanOut: fanOut[t.ID],
		}
	}
	ranked := rankTasks(rin, opts.StrictFocus)

	if opts.DryRun {
		top := ranked[0]
		return &ClaimNextResult{Claimed: false, Task: &top, FanOut: fanOut[top.ID]}, nil
	}

	for _, t := range ranked {
		lease, err := s.Claim(ctx, t.ID, opts.ActorID, opts.Worktree, opts.TTL)
		if err != nil {
			if errors.Is(err, ErrLeased) || errors.Is(err, ErrBlocked) || errors.Is(err, ErrBadTransition) {
				// These lost races are already counted under op=claim by Claim itself —
				// ErrBadTransition lands in outcome="error", so that series includes
				// contention, not just faults.
				continue
			}
			s.metrics.claim("claim_next", "error")
			return nil, err
		}
		task := t
		s.metrics.claim("claim_next", "ok")
		return &ClaimNextResult{Claimed: true, Task: &task, FanOut: fanOut[t.ID], Lease: lease}, nil
	}
	// Every candidate lost its race; reported as none, same as an empty ready set.
	s.metrics.claim("claim_next", "none")
	return &ClaimNextResult{Claimed: false}, nil
}
