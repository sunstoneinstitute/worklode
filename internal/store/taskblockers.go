// taskblockers.go walks the blocker relation transitively: what is holding a
// task, what is holding those, all the way down. The one-hop answer lives in
// brief.go (openBlockers, blockingPlans); this is the same relation with the
// same open predicate, followed to the bottom.
package store

import (
	"context"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// blockerRelation is "(blocked, blocker)" over open blockers: the from_task of
// a 'blocks' edge, and the open tasks of a plan ordered before the blocked
// task's plan (025 §9.3). It is openBlockers' body with the blocked task left
// free instead of bound to one id, so the two surfaces cannot disagree about
// what blocks what — including on what "open" means, which is taskClosed in
// both.
//
// It binds `e`, `b`, `dep` and `de` on top of the aliases taskClosed binds.
var blockerRelation = `
	SELECT e.to_task AS blocked, b.id, b.title, b.state, b.project_id
	  FROM task_edges e
	  JOIN tasks b ON b.id = e.from_task
	 WHERE e.type = 'blocks'
	   AND b.deleted_at IS NULL
	   AND NOT ` + taskClosed("b") + `
	UNION
	SELECT dep.id AS blocked, b.id, b.title, b.state, b.project_id
	  FROM tasks dep
	  JOIN doc_edges de ON de.type = 'blocks' AND de.to_doc = dep.plan_doc
	  JOIN tasks b ON b.plan_doc = de.from_doc
	 WHERE dep.deleted_at IS NULL
	   AND b.deleted_at IS NULL
	   AND NOT ` + taskClosed("b")

// BlockerTree returns every open task transitively blocking rootID, plus the
// unfinished plans ordered before rootID's own plan. ErrNotFound when rootID
// names no live task.
//
// Each row carries the task it blocks (Via) and its hop count from the root,
// so a caller renders the tree without a second pass. A blocker reachable by
// two routes appears once per route — that is the tree, not a duplicate — but
// only once per (blocker, blocked) pair, which is why the rows are grouped:
// the same edge reached by two paths would otherwise print its whole subtree
// twice. A cycle stops at its repeat, marked Cycle: 'blocks' edges are not
// validated against cycles on write, so this query must survive one, and a
// renderer must not expand a node it marks.
//
// The blocker relation is computed whole (Postgres materialises a CTE that is
// referenced twice), not narrowed to the tree. That is the same shape and
// cost class as AllBlockEdges + ClosedTaskIDs, which /frontier pays on every
// call; narrow it only if the edge set ever grows enough to measure.
func (s *Store) BlockerTree(ctx context.Context, rootID string) (model.BlockerTree, error) {
	out := model.BlockerTree{
		Root:     rootID,
		Blockers: []model.BlockerNode{},
	}
	if _, err := s.GetTask(ctx, rootID); err != nil {
		return model.BlockerTree{}, err
	}

	rows, err := s.db.QueryContext(ctx, `
WITH RECURSIVE blockers(blocked, id, title, state, project_id) AS (`+blockerRelation+`),
tree(id, title, state, project_id, via, depth) AS (
    SELECT id, title, state, project_id, blocked, 1
      FROM blockers WHERE blocked = $1
  UNION ALL
    SELECT b.id, b.title, b.state, b.project_id, b.blocked, t.depth + 1
      FROM tree t JOIN blockers b ON b.blocked = t.id
) CYCLE id SET is_cycle USING path
SELECT id, title, state, project_id, via, MIN(depth), bool_or(is_cycle)
  FROM tree
 GROUP BY id, title, state, project_id, via
 ORDER BY MIN(depth), via, CAST(split_part(id, '-', 2) AS INTEGER), id`, rootID)
	if err != nil {
		return model.BlockerTree{}, fmt.Errorf("blocker tree of %s: %w", rootID, err)
	}
	nodes, err := collectRows(rows, fmt.Sprintf("blocker tree of %s", rootID),
		func(r rowScanner) (model.BlockerNode, error) {
			var n model.BlockerNode
			err := r.Scan(&n.ID, &n.Title, &n.State, &n.Project, &n.Via, &n.Depth, &n.Cycle)
			return n, err
		})
	if err != nil {
		return model.BlockerTree{}, err
	}
	if len(nodes) > 0 {
		out.Blockers = nodes
	}

	plans, err := s.blockingPlans(ctx, rootID)
	if err != nil {
		return model.BlockerTree{}, err
	}
	out.BlockingPlans = plans
	if out.BlockingPlans == nil {
		out.BlockingPlans = []model.DocRef{}
	}
	return out, nil
}

// BlockerForest returns one blocker tree per blocked task in projectID
// ("" for every project), for the id-less form of `lode task blockers`.
//
// The seed set is blockedTaskIDsIn — the same predicate BlockedTaskIDs and
// the claim path use — and every seed is walked in one query rather than one
// per task. A seed that turns up inside another seed's tree is dropped, so a
// chain prints once from its top instead of once per task on it. When every
// seed is covered but seeds exist, the blocker graph is one or more cycles
// among the seeds themselves; all of them are kept rather than reporting
// nothing, and `lode task critical-path` is what names the cycle.
func (s *Store) BlockerForest(ctx context.Context, projectID string) ([]model.BlockerTree, error) {
	seeds, err := s.blockedTaskIDsIn(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if len(seeds) == 0 {
		return []model.BlockerTree{}, nil
	}

	rows, err := s.db.QueryContext(ctx, `
WITH RECURSIVE blockers(blocked, id, title, state, project_id) AS (`+blockerRelation+`),
tree(root, id, title, state, project_id, via, depth) AS (
    SELECT s.id, b.id, b.title, b.state, b.project_id, s.id, 1
      FROM unnest($1::text[]) AS s(id) JOIN blockers b ON b.blocked = s.id
  UNION ALL
    SELECT t.root, b.id, b.title, b.state, b.project_id, b.blocked, t.depth + 1
      FROM tree t JOIN blockers b ON b.blocked = t.id
) CYCLE id SET is_cycle USING path
SELECT root, id, title, state, project_id, via, MIN(depth), bool_or(is_cycle)
  FROM tree
 GROUP BY root, id, title, state, project_id, via
 ORDER BY root, MIN(depth), via, CAST(split_part(id, '-', 2) AS INTEGER), id`, seeds)
	if err != nil {
		return nil, fmt.Errorf("blocker forest of %q: %w", projectID, err)
	}
	type rooted struct {
		root string
		node model.BlockerNode
	}
	all, err := collectRows(rows, fmt.Sprintf("blocker forest of %q", projectID),
		func(r rowScanner) (rooted, error) {
			var x rooted
			err := r.Scan(&x.root, &x.node.ID, &x.node.Title, &x.node.State,
				&x.node.Project, &x.node.Via, &x.node.Depth, &x.node.Cycle)
			return x, err
		})
	if err != nil {
		return nil, err
	}

	byRoot := map[string][]model.BlockerNode{}
	covered := map[string]bool{}
	for _, x := range all {
		byRoot[x.root] = append(byRoot[x.root], x.node)
		if x.node.ID != x.root {
			covered[x.node.ID] = true
		}
	}
	roots := make([]string, 0, len(seeds))
	for _, id := range seeds {
		if !covered[id] {
			roots = append(roots, id)
		}
	}
	if len(roots) == 0 {
		roots = seeds
	}

	plans, err := s.blockingPlansFor(ctx, roots)
	if err != nil {
		return nil, err
	}
	out := make([]model.BlockerTree, 0, len(roots))
	for _, id := range roots {
		t := model.BlockerTree{Root: id, Blockers: byRoot[id], BlockingPlans: plans[id]}
		if t.Blockers == nil {
			t.Blockers = []model.BlockerNode{}
		}
		if t.BlockingPlans == nil {
			t.BlockingPlans = []model.DocRef{}
		}
		out = append(out, t)
	}
	return out, nil
}

// blockedTaskIDsIn is BlockedTaskIDs narrowed to one project and returned in
// the ranked order every other task list uses (priority, then id), so the
// forest's trees come out in the order the board and `lode task list` would
// put their roots in. projectID == "" spans every project.
func (s *Store) blockedTaskIDsIn(ctx context.Context, projectID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id FROM tasks t
		  WHERE t.deleted_at IS NULL
		    AND ($1 = '' OR t.project_id = $1)
		    AND (EXISTS (SELECT 1 FROM task_edges e
		                  WHERE e.to_task = t.id AND `+blockedCondition+`)
		         OR `+planBlockedCondition+`)`+
			taskListOrder("t"), projectID)
	if err != nil {
		return nil, fmt.Errorf("blocked tasks in %q: %w", projectID, err)
	}
	return collectRows(rows, fmt.Sprintf("blocked tasks in %q", projectID),
		func(r rowScanner) (string, error) {
			var id string
			return id, r.Scan(&id)
		})
}
