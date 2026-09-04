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

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// maxHierarchyDepth caps a child_of chain at two edges, now spanning task ->
// subtask only (spec 004 §6.1). The brief is a bounded payload and the walks
// that feed roll-up and breadcrumbs are unbounded without a cap.
const maxHierarchyDepth = 2

// hasChildren reports whether id has any child_of children. It is the
// container predicate every guard keys off, container-ness being inferred
// rather than declared: with decompose creating parent-hood and its children
// in one transaction, "has children" is exactly as sharp as a column
// (004 §6.1). Only live children count (044 §4) — deleting the last child
// makes its parent an ordinary task again, which is what keeps the parent
// claimable and lets it advance past merged.
// Served by the task_edges_children partial index.
func hasChildren(tx *sql.Tx, id string) (bool, error) {
	var one int
	err := tx.QueryRow(
		`SELECT 1 FROM task_edges e JOIN tasks c ON c.id = e.from_task
		  WHERE e.to_task = $1 AND e.type = 'child_of' AND c.deleted_at IS NULL LIMIT 1`,
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
// declare, and the edge itself is what makes the parent a container — except
// a decision, which never is (004 §6.3 as amended): a decision closes by its
// recorded answer in one transaction, a parent closes by roll-up, and the two
// cannot both hold. A decision as a child stays legal, it is a leaf like any
// other.
func checkHierarchy(tx *sql.Tx, child, parent string, project map[string]string) error {
	if project[child] != project[parent] {
		return fmt.Errorf("cross-project edge %s (%s) child_of %s (%s): %w",
			child, project[child], parent, project[parent], ErrInvalidInput)
	}

	var parentKind string
	if err := tx.QueryRow(`SELECT kind FROM tasks WHERE id = $1`, parent).Scan(&parentKind); err != nil {
		return fmt.Errorf("parent kind of %s: %w", parent, err)
	}
	if parentKind == "decision" {
		return fmt.Errorf("task %s is a decision and cannot take children: %w", parent, ErrInvalidInput)
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

// ChildProgress returns the closed/total counts over taskID's direct children:
// a parent's derived roll-up, computed on read and never stored — there is no
// resolver, no migration, and no event-log noise behind it. A task with no
// children reports a zero value. Tombstoned children are out of the count
// entirely — counting one as closed would report deleted work as delivered.
func (s *Store) ChildProgress(ctx context.Context, taskID string) (model.TaskProgress, error) {
	var p model.TaskProgress
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE `+taskClosed("t")+`)
		   FROM task_edges e JOIN tasks t ON t.id = e.from_task
		  WHERE e.to_task = $1 AND e.type = 'child_of'
		    AND t.deleted_at IS NULL`, taskID).Scan(&p.Total, &p.Closed)
	if err != nil {
		return model.TaskProgress{}, fmt.Errorf("child progress of %s: %w", taskID, err)
	}
	return p, nil
}

// ParentOf returns taskID's parent, or nil when it has none. Only ID, Title,
// and State are populated: one hop up is all any caller needs, and the full
// ancestry is unbounded.
func (s *Store) ParentOf(ctx context.Context, taskID string) (*model.Task, error) {
	var p model.Task
	err := s.db.QueryRowContext(ctx,
		`SELECT t.id, t.title, t.state
		   FROM task_edges e JOIN tasks t ON t.id = e.to_task
		  WHERE e.from_task = $1 AND e.type = 'child_of'
		    AND t.deleted_at IS NULL`, taskID).Scan(&p.ID, &p.Title, &p.State)
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
		   FROM task_edges e
		   JOIN tasks t ON t.id = e.from_task
		   JOIN tasks p ON p.id = e.to_task
		  WHERE e.type = 'child_of' AND ($1 = '' OR t.project_id = $1)
		    AND t.deleted_at IS NULL AND p.deleted_at IS NULL`, projectID)
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

// ChildrenOf returns the live direct children of every id in ids, keyed by
// parent id and ordered within each parent exactly as ListTasks orders its
// results. Parents with no children are absent from the map. One query for
// the rows and one for their closedness, whatever the number of parents — the
// bulk form of ListTasks{Parent: id}, so a caller rendering a whole hierarchy
// does not issue a request per container.
func (s *Store) ChildrenOf(ctx context.Context, ids []string) (map[string][]model.Task, error) {
	out := map[string][]model.Task{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskColumnsT+`, e.to_task
		   FROM task_edges e JOIN tasks t ON t.id = e.from_task
		  WHERE e.to_task = ANY($1) AND e.type = 'child_of'
		    AND t.deleted_at IS NULL`+taskListOrder("t"), ids)
	if err != nil {
		return nil, fmt.Errorf("children of tasks: %w", err)
	}
	out, err = groupRows(rows, "children of tasks", func(r rowScanner) (string, model.Task, error) {
		var parent string
		t, err := scanTask(appendScan{r, []any{&parent}})
		if err != nil {
			return "", model.Task{}, err
		}
		return parent, *t, nil
	})
	if err != nil {
		return nil, err
	}
	// Closedness is a second query for the reason ListTasks gives: taskClosed
	// binds aliases that would collide with the join above.
	var childIDs []string
	for _, kids := range out {
		for _, k := range kids {
			childIDs = append(childIDs, k.ID)
		}
	}
	closed, err := s.ClosedTaskIDs(ctx, childIDs)
	if err != nil {
		return nil, fmt.Errorf("children of tasks: %w", err)
	}
	for parent, kids := range out {
		for i := range kids {
			kids[i].Closed = closed[kids[i].ID]
		}
		out[parent] = kids
	}
	return out, nil
}

// TaskTreeFilter selects which containers a TaskTree covers. Root names a
// single task to report (with its children) whatever its own parentage;
// Project and States narrow the whole-project form, where the roots are the
// containers that are not themselves someone's child.
type TaskTreeFilter struct {
	Project string
	States  []string
	Root    string
}

// TaskTree returns one node per container — the container, its derived
// progress, and its direct children — in ListTasks order. It is the whole
// `lode task tree` view in one read: the client used to fetch the containers
// and then one child list per container, an N+1 against the API (WL-169).
//
// Children are every live child whatever its state, so the counts in Progress
// and the rows under a parent describe the same set; States narrows which
// containers appear, never which children they report. A Root that does not
// exist is ErrNotFound.
func (s *Store) TaskTree(ctx context.Context, f TaskTreeFilter) ([]model.TaskTreeNode, error) {
	var parents []model.Task
	if f.Root != "" {
		t, err := s.GetTask(ctx, f.Root)
		if err != nil {
			return nil, err
		}
		parents = []model.Task{*t}
	} else {
		listed, err := s.ListTasks(ctx, TaskFilter{
			Project: f.Project, States: f.States, HasChildren: true,
		})
		if err != nil {
			return nil, err
		}
		// Only the roots: a container that is itself a subtask already shows
		// up under its own parent, and listing it twice would double-count
		// the tree.
		parentOf, err := s.ParentMap(ctx, f.Project)
		if err != nil {
			return nil, err
		}
		for _, t := range listed {
			if parentOf[t.ID] == "" {
				parents = append(parents, t)
			}
		}
	}

	ids := make([]string, 0, len(parents))
	for _, p := range parents {
		ids = append(ids, p.ID)
	}
	children, err := s.ChildrenOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	nodes := make([]model.TaskTreeNode, 0, len(parents))
	for _, p := range parents {
		kids := children[p.ID]
		// The same roll-up ChildProgress computes, off rows already read:
		// both count live children and both call a child closed by the
		// per-repo predicate the Closed flag carries.
		progress := model.TaskProgress{Total: len(kids)}
		for _, k := range kids {
			if k.Closed {
				progress.Closed++
			}
		}
		nodes = append(nodes, model.TaskTreeNode{Parent: p, Progress: progress, Children: kids})
	}
	return nodes, nil
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
// that its children would exceed maxHierarchyDepth, when the parent is a
// decision (004 §6.3 as amended — it closes by its recorded answer, not by
// roll-up), and from the delivery states a task with children can never
// occupy.
func Decompose(tx *sql.Tx, now time.Time, parentID string, titles []string, createdBy string, eventID int64) ([]model.Task, error) {
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
	// A decision closes by its recorded answer in one transaction; a parent
	// closes by roll-up, and the two cannot both hold (004 §6.3 as amended).
	if kind == "decision" {
		return nil, fmt.Errorf("task %s is a decision and cannot take children: %w",
			parentID, ErrInvalidInput)
	}
	already, err := hasChildren(tx, parentID)
	if err != nil {
		return nil, err
	}
	if already {
		return nil, fmt.Errorf("task %s already has children; add more to it instead: %w",
			parentID, ErrInvalidInput)
	}

	// activeLeaseTx answers both "is it leased" and "by whom" in one query;
	// no lease is ErrNotFound, which is the pass case here.
	lease, err := activeLeaseTx(tx, parentID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if err == nil {
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

	children := make([]model.Task, 0, len(trimmed))
	for _, title := range trimmed {
		child, err := CreateTask(tx, now, TaskInput{
			ProjectID: projectID,
			Title:     title,
			Priority:  priority,
			Kind:      kind,
			Concern:   concern.String,
			CreatedBy: createdBy,
			Draft:     true,
		}, eventID)
		if err != nil {
			return nil, err
		}
		if err := AddEdge(tx, now, child.ID, parentID, "child_of", eventID); err != nil {
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
