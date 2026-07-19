package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Task is one unit of work, identified by a global WT-<n> id.
type Task struct {
	ID        string
	ProjectID string
	Title     string
	Body      string
	Priority  string
	Kind      string
	State     string
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TaskInput carries the fields for creating a new task. Draft creates the
// task in state "draft" (not claimable) instead of the default "ready".
type TaskInput struct {
	ProjectID string
	Title     string
	Body      string
	Priority  string
	Kind      string
	CreatedBy string
	Draft     bool
}

// TaskFilter narrows ListTasks. Zero-valued fields do not filter.
type TaskFilter struct {
	Project  string
	States   []string
	Priority string
}

// Edge is a typed, directed link between two tasks. "A blocks B" means B is
// blocked until A is done or abandoned; "A child_of B" makes B an epic.
type Edge struct {
	FromTask string
	ToTask   string
	Type     string
}

// legalTransitions is the complete task state machine: draft → ready →
// in_progress → in_review → done, with backward moves in_progress → ready and
// in_review → in_progress, and abandoned reachable from every non-terminal
// state. done and abandoned are terminal.
var legalTransitions = map[[2]string]bool{
	{"draft", "ready"}:           true,
	{"ready", "in_progress"}:     true,
	{"in_progress", "in_review"}: true,
	{"in_progress", "ready"}:     true,
	{"in_review", "done"}:        true,
	{"in_review", "in_progress"}: true,
	{"draft", "abandoned"}:       true,
	{"ready", "abandoned"}:       true,
	{"in_progress", "abandoned"}: true,
	{"in_review", "abandoned"}:   true,
}

// CreateTask allocates the next WT-<n> id from task_seq and inserts the task
// inside the given transaction. It is meant to be called from a RecordEvent
// apply callback with the store's clock as now.
func CreateTask(tx *sql.Tx, now time.Time, in TaskInput) (*Task, error) {
	var n int64
	if err := tx.QueryRow(
		`UPDATE task_seq SET next = next + 1 WHERE id = 1 RETURNING next - 1`,
	).Scan(&n); err != nil {
		return nil, fmt.Errorf("allocate task id: %w", err)
	}
	id := fmt.Sprintf("WT-%d", n)

	state := "ready"
	if in.Draft {
		state = "draft"
	}
	ts := now.UTC().Truncate(time.Second)
	tsStr := ts.Format(time.RFC3339)

	var createdBy sql.NullString
	if in.CreatedBy != "" {
		createdBy = sql.NullString{String: in.CreatedBy, Valid: true}
	}
	_, err := tx.Exec(
		`INSERT INTO tasks (id, project_id, title, body, priority, kind, state, created_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, in.ProjectID, in.Title, in.Body, in.Priority, in.Kind, state, createdBy, tsStr, tsStr,
	)
	if err != nil {
		return nil, fmt.Errorf("insert task %s: %w", id, err)
	}
	return &Task{
		ID:        id,
		ProjectID: in.ProjectID,
		Title:     in.Title,
		Body:      in.Body,
		Priority:  in.Priority,
		Kind:      in.Kind,
		State:     state,
		CreatedBy: in.CreatedBy,
		CreatedAt: ts,
		UpdatedAt: ts,
	}, nil
}

// Transition moves a task from one state to another inside the given
// transaction. The move must be in legalTransitions and the task's current
// state must equal from (otherwise ErrBadTransition; unknown task is
// ErrNotFound). It bumps updated_at and appends a state_log row attributed
// to eventID.
func Transition(tx *sql.Tx, now time.Time, taskID, from, to string, eventID int64) error {
	if !legalTransitions[[2]string{from, to}] {
		return fmt.Errorf("task %s: %s -> %s: %w", taskID, from, to, ErrBadTransition)
	}

	var current string
	err := tx.QueryRow(`SELECT state FROM tasks WHERE id = ?`, taskID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("task %s: %w", taskID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("get task %s state: %w", taskID, err)
	}
	if current != from {
		return fmt.Errorf("task %s is in state %s, not %s: %w", taskID, current, from, ErrBadTransition)
	}

	_, err = tx.Exec(
		`UPDATE tasks SET state = ?, updated_at = ? WHERE id = ?`,
		to, now.UTC().Format(time.RFC3339), taskID,
	)
	if err != nil {
		return fmt.Errorf("update task %s state: %w", taskID, err)
	}
	return LogChange(tx, "task", taskID, eventID,
		map[string]string{"field": "state", "old": from, "new": to})
}

// taskColumns is the SELECT list scanTask expects, in order.
const taskColumns = `id, project_id, title, body, priority, kind, state, created_by, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner) (*Task, error) {
	var t Task
	var body, createdBy sql.NullString
	var createdAt, updatedAt string
	if err := row.Scan(&t.ID, &t.ProjectID, &t.Title, &body, &t.Priority, &t.Kind,
		&t.State, &createdBy, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	t.Body = body.String
	t.CreatedBy = createdBy.String
	var err error
	if t.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
		return nil, fmt.Errorf("parse task %s created_at: %w", t.ID, err)
	}
	if t.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt); err != nil {
		return nil, fmt.Errorf("parse task %s updated_at: %w", t.ID, err)
	}
	return &t, nil
}

// GetTask looks up a task by id. Returns ErrNotFound if it does not exist.
func (s *Store) GetTask(ctx context.Context, id string) (*Task, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get task %s: %w", id, err)
	}
	return t, nil
}

// ListTasks returns tasks matching the filter, ordered by priority (critical
// first) and then by numeric id.
func (s *Store) ListTasks(ctx context.Context, f TaskFilter) ([]Task, error) {
	q := `SELECT ` + taskColumns + ` FROM tasks`
	var conds []string
	var args []any
	if f.Project != "" {
		conds = append(conds, `project_id = ?`)
		args = append(args, f.Project)
	}
	if len(f.States) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(f.States)), ", ")
		conds = append(conds, `state IN (`+placeholders+`)`)
		for _, st := range f.States {
			args = append(args, st)
		}
	}
	if f.Priority != "" {
		conds = append(conds, `priority = ?`)
		args = append(args, f.Priority)
	}
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	q += ` ORDER BY CASE priority
	         WHEN 'critical' THEN 0
	         WHEN 'high' THEN 1
	         WHEN 'medium' THEN 2
	         ELSE 3
	       END, CAST(substr(id, 4) AS INTEGER)`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return out, nil
}

// AddEdge inserts a typed edge between two existing tasks inside the given
// transaction. Self-edges are rejected for both types; a child_of edge that
// would make the child hierarchy cyclic is rejected with a "cycle" error.
// A missing endpoint returns ErrNotFound.
func AddEdge(tx *sql.Tx, now time.Time, fromTask, toTask, typ string) error {
	if typ != "child_of" && typ != "blocks" {
		return fmt.Errorf("unknown edge type %q", typ)
	}
	if fromTask == toTask {
		return fmt.Errorf("self-edge %s %s %s not allowed", fromTask, typ, toTask)
	}
	for _, id := range []string{fromTask, toTask} {
		var one int
		err := tx.QueryRow(`SELECT 1 FROM tasks WHERE id = ?`, id).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("task %s: %w", id, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("check task %s: %w", id, err)
		}
	}
	if typ == "child_of" {
		reaches, err := reachesViaChildOf(tx, toTask, fromTask)
		if err != nil {
			return err
		}
		if reaches {
			return fmt.Errorf("edge %s child_of %s would create a cycle", fromTask, toTask)
		}
	}
	_, err := tx.Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES (?, ?, ?, ?)`,
		fromTask, toTask, typ, now.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert edge %s %s %s: %w", fromTask, typ, toTask, err)
	}
	return nil
}

// reachesViaChildOf reports whether target is reachable from start by
// walking child_of edges upward (child -> parent). The visited set keeps the
// walk terminating even if the stored graph already contains a cycle.
func reachesViaChildOf(tx *sql.Tx, start, target string) (bool, error) {
	visited := map[string]bool{start: true}
	frontier := []string{start}
	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]

		rows, err := tx.Query(
			`SELECT to_task FROM task_edges WHERE from_task = ? AND type = 'child_of'`, cur)
		if err != nil {
			return false, fmt.Errorf("walk child_of parents of %s: %w", cur, err)
		}
		var parents []string
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				return false, fmt.Errorf("scan parent of %s: %w", cur, err)
			}
			parents = append(parents, p)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, fmt.Errorf("walk child_of parents of %s: %w", cur, err)
		}
		rows.Close()

		for _, p := range parents {
			if p == target {
				return true, nil
			}
			if !visited[p] {
				visited[p] = true
				frontier = append(frontier, p)
			}
		}
	}
	return false, nil
}

// RemoveEdge deletes an edge inside the given transaction. Returns
// ErrNotFound if the edge does not exist.
func RemoveEdge(tx *sql.Tx, fromTask, toTask, typ string) error {
	res, err := tx.Exec(
		`DELETE FROM task_edges WHERE from_task = ? AND to_task = ? AND type = ?`,
		fromTask, toTask, typ,
	)
	if err != nil {
		return fmt.Errorf("delete edge %s %s %s: %w", fromTask, typ, toTask, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete edge rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListEdges returns the edges leaving taskID (out) and pointing at it (in).
func (s *Store) ListEdges(ctx context.Context, taskID string) (out, in []Edge, err error) {
	list := func(where string) ([]Edge, error) {
		rows, err := s.db.QueryContext(ctx,
			`SELECT from_task, to_task, type FROM task_edges WHERE `+where+` ORDER BY from_task, to_task, type`,
			taskID)
		if err != nil {
			return nil, fmt.Errorf("list edges for %s: %w", taskID, err)
		}
		defer rows.Close()
		var edges []Edge
		for rows.Next() {
			var e Edge
			if err := rows.Scan(&e.FromTask, &e.ToTask, &e.Type); err != nil {
				return nil, fmt.Errorf("scan edge: %w", err)
			}
			edges = append(edges, e)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("list edges for %s: %w", taskID, err)
		}
		return edges, nil
	}
	if out, err = list(`from_task = ?`); err != nil {
		return nil, nil, err
	}
	if in, err = list(`to_task = ?`); err != nil {
		return nil, nil, err
	}
	return out, in, nil
}

// blockedCondition matches 'blocks' edges whose blocker (from_task) is still
// open, i.e. the edge currently blocks its to_task.
const blockedCondition = `e.type = 'blocks'
	 AND EXISTS (SELECT 1 FROM tasks b
	             WHERE b.id = e.from_task
	               AND b.state NOT IN ('done', 'abandoned'))`

// BlockedTaskIDs returns the ids of tasks that have at least one open
// 'blocks' edge pointing at them (the blocker is not done or abandoned).
func (s *Store) BlockedTaskIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT e.to_task FROM task_edges e WHERE `+blockedCondition)
	if err != nil {
		return nil, fmt.Errorf("blocked task ids: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan blocked task id: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("blocked task ids: %w", err)
	}
	return out, nil
}

// IsBlocked reports whether taskID has an open 'blocks' edge pointing at it.
// It runs inside the given transaction so lease claims can check it
// atomically.
func IsBlocked(tx *sql.Tx, taskID string) (bool, error) {
	var blocked bool
	err := tx.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM task_edges e WHERE e.to_task = ? AND `+blockedCondition+`)`,
		taskID,
	).Scan(&blocked)
	if err != nil {
		return false, fmt.Errorf("is blocked %s: %w", taskID, err)
	}
	return blocked, nil
}
