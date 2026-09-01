package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// BlockingFanOut returns, for every task that blocks at least one other task,
// the transitive count of distinct OPEN tasks it unblocks over 'blocks'
// edges. A task absent from the returned map has fan-out 0. The closure
// itself walks every live 'blocks' edge — a closed intermediate does not cut
// an open dependent off from its root — but the count keeps only open tasks
// (the taskClosed predicate, per-repo done_state and all): spec 007 §4's
// closed-task rule (WL-354). "How much work t unblocks when done" is about
// remaining work, and this map feeds both claim-next's sort key and the
// overview surfaces, so counting finished dependents would inflate pickup
// priority for no benefit.
//
// It also filters tombstoned tasks off both ends of every edge (044 §4): the
// count is a ranking input, and a deleted task neither waits on anything nor
// lends weight to whatever blocks it.
func (s *Store) BlockingFanOut(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE closure(root, task) AS (
		    SELECT e.from_task, e.to_task FROM task_edges e
		      JOIN tasks f ON f.id = e.from_task AND f.deleted_at IS NULL
		      JOIN tasks b ON b.id = e.to_task   AND b.deleted_at IS NULL
		     WHERE e.type = 'blocks'
		  UNION
		    SELECT c.root, e.to_task
		    FROM closure c JOIN task_edges e ON e.from_task = c.task AND e.type = 'blocks'
		      JOIN tasks b ON b.id = e.to_task AND b.deleted_at IS NULL
		)
		SELECT c.root, COUNT(DISTINCT c.task)
		  FROM closure c
		  JOIN tasks bt ON bt.id = c.task
		 WHERE NOT `+taskClosed("bt")+`
		 GROUP BY c.root`)
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

// readyCandidates returns every task eligible for pickup: state ready, no
// child_of children, not needs_decomposition, not human_only, unleased, not
// blocked by an open 'blocks' edge from a task not in a closed state, and not held
// by a plan-to-plan ordering edge (planBlockedCondition, 025 §9.3). An empty
// projectID matches every project; an empty kind matches every kind. A task
// with children is excluded because the worktree is the unit of Worklode work
// and a container has nothing to check out (spec 004 §6.3). A tombstoned task
// is never handed out, and a tombstoned child does not make its parent a
// container (044 §4).
//
// This is the one seam both ranked paths go through — Frontier and ClaimNext
// share it via rankedFrontier — so human_only is filtered here and nowhere
// else. Claim(id) does not consult it: an explicit claim by id is the escape
// hatch for the person the task is waiting on (WL-466).
func (s *Store) readyCandidates(ctx context.Context, projectID, kind string) ([]model.Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+taskColumnsT+` FROM tasks t
		WHERE t.state = 'ready'
		  AND t.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM task_edges c
		                  JOIN tasks ct ON ct.id = c.from_task AND ct.deleted_at IS NULL
		                  WHERE c.to_task = t.id AND c.type = 'child_of')
		  AND NOT t.needs_decomposition
		  AND NOT t.human_only
		  AND ($1 = '' OR t.project_id = $1)
		  AND ($2 = '' OR t.kind = $2)
		  AND NOT EXISTS (SELECT 1 FROM leases l
		                  WHERE l.task_id = t.id AND l.released_at IS NULL)
		  AND NOT EXISTS (SELECT 1 FROM task_edges e
		                  WHERE e.to_task = t.id AND `+blockedCondition+`)
		  AND NOT (`+planBlockedCondition+`)`, projectID, kind)
	if err != nil {
		return nil, fmt.Errorf("ready candidates: %w", err)
	}
	return collectRows(rows, "ready candidates", byValue(scanTask))
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

// rankedFrontier is the ranking pipeline both Frontier and ClaimNext run:
// the ready set for (projectID, kind), ordered by rankTasks under the given
// focus mode, plus the blocking fan-out map the ranking used. It writes
// nothing. An empty ready set short-circuits before the fan-out and focus
// queries — neither has anything to say about no tasks — and yields a nil
// slice with a non-nil empty map.
func (s *Store) rankedFrontier(ctx context.Context, projectID, kind string, strictFocus bool) ([]model.Task, map[string]int, error) {
	candidates, err := s.readyCandidates(ctx, projectID, kind)
	if err != nil {
		return nil, nil, err
	}
	if len(candidates) == 0 {
		return nil, map[string]int{}, nil
	}

	fanOut, err := s.BlockingFanOut(ctx)
	if err != nil {
		return nil, nil, err
	}

	var projectIDs []string
	seen := map[string]bool{}
	for _, t := range candidates {
		if !seen[t.Project] {
			seen[t.Project] = true
			projectIDs = append(projectIDs, t.Project)
		}
	}
	focus, err := s.projectFocusMap(ctx, projectIDs)
	if err != nil {
		return nil, nil, err
	}

	in := make([]rankInput, len(candidates))
	for i, t := range candidates {
		in[i] = rankInput{Task: t, Focus: focus[t.Project], FanOut: fanOut[t.ID]}
	}
	return rankTasks(in, strictFocus), fanOut, nil
}

// Frontier returns the ready, unblocked, unleased tasks in the exact rank
// order ClaimNext consumes, plus the blocking fan-out map — the read-only
// overview mirror of the authoritative frontier (spec 007 §3.4). It claims
// nothing.
//
// It shares rankedFrontier with ClaimNext rather than reimplementing the
// pipeline, so the two orders agree by construction;
// TestFrontierMirrorsClaimNextOrder is a cheap regression guard against that
// sharing being undone, not an independent check of the ranking itself
// (rankTasks has its own tests for that).
//
// The two arguments Frontier does not take are deliberate, not an oversight:
// no kind filter and soft focus (critical priority still outranks focus).
// The overview is a read-only view of the whole queue as it stands, so it
// must not narrow itself the way one worker's claim call does — a strict- or
// kind-filtered claim is a different question, asked through ClaimNext.
func (s *Store) Frontier(ctx context.Context, projectID string) ([]model.Task, map[string]int, error) {
	return s.rankedFrontier(ctx, projectID, "", false)
}

// rankInput carries the per-candidate inputs rankTasks needs beyond the task
// itself: the ranking concern index depends on the task's own project focus
// (a claim-next call spanning multiple projects uses each task's own
// project), and fan-out is precomputed by BlockingFanOut.
type rankInput struct {
	Task   model.Task
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

// criticalRank lifts critical-priority tasks above everything else in the
// default order. Strict focus drops the arm entirely (every task ranks the
// same), so focus alone decides the head of the queue.
func criticalRank(priority string, strictFocus bool) int {
	if !strictFocus && priority == "critical" {
		return 0
	}
	return 1
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
func rankTasks(in []rankInput, strictFocus bool) []model.Task {
	ranked := slices.Clone(in)
	slices.SortStableFunc(ranked, func(a, b rankInput) int {
		return cmp.Or(
			cmp.Compare(criticalRank(a.Task.Priority, strictFocus), criticalRank(b.Task.Priority, strictFocus)),
			cmp.Compare(concernRank(a.Task.Concern, a.Focus), concernRank(b.Task.Concern, b.Focus)),
			cmp.Compare(priorityRank(a.Task.Priority), priorityRank(b.Task.Priority)),
			cmp.Compare(b.FanOut, a.FanOut), // higher fan-out first
			a.Task.CreatedAt.Compare(b.Task.CreatedAt),
			cmp.Compare(numericTaskID(a.Task.ID), numericTaskID(b.Task.ID)),
		)
	})
	out := make([]model.Task, len(ranked))
	for i, r := range ranked {
		out[i] = r.Task
	}
	return out
}

// ClaimNextOpts configures ClaimNext.
type ClaimNextOpts struct {
	ProjectID   string
	Kind        string
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
	Task    *model.Task
	FanOut  int
	Lease   *Lease
}

// ClaimNext ranks the ready set (rankedFrontier — the same pipeline Frontier
// reads, here with the caller's kind filter and focus mode) and atomically
// claims the top candidate via Claim, falling through to the next ranked
// candidate whenever a claim loses the race (ErrLeased, ErrBlocked,
// ErrBadTransition) — Claim's own transaction is what makes each attempt
// atomic; ClaimNext just retries in rank order. An empty ready set is not an
// error: it returns Claimed=false, Task=nil. DryRun returns the top-ranked
// candidate without writing anything (no lease, no state change).
func (s *Store) ClaimNext(ctx context.Context, opts ClaimNextOpts) (*ClaimNextResult, error) {
	ranked, fanOut, err := s.rankedFrontier(ctx, opts.ProjectID, opts.Kind, opts.StrictFocus)
	if err != nil {
		return nil, err
	}
	if len(ranked) == 0 {
		// A dry run is a read, not a claim attempt.
		if !opts.DryRun {
			s.metrics.claim("claim_next", "none")
		}
		return &ClaimNextResult{Claimed: false}, nil
	}

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
