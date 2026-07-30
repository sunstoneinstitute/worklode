package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Task is one unit of work, identified by a per-project <KEY>-<n> id.
type Task struct {
	ID                 string
	ProjectID          string
	Title              string
	Body               string
	Priority           string
	Kind               string
	State              string
	Concern            string
	NeedsDecomposition bool
	CreatedBy          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// TaskInput carries the fields for creating a new task. Draft creates the
// task in state "draft" (not claimable) instead of the default "ready".
// Concern is optional ("" means no concern); needs_decomposition is not a
// creation input — new tasks always start with the column default (false).
type TaskInput struct {
	ProjectID string
	Title     string
	Body      string
	Priority  string
	Kind      string
	Concern   string
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
// blocked until A reaches a closed state (see closedStates); "A child_of B"
// makes B an epic.
type Edge struct {
	FromTask string
	ToTask   string
	Type     string
}

// legalTransitions is the complete task state machine: draft → ready →
// in_progress → in_review → merged → deployed_dev → deployed_prod, with
// released as the terminal for release-based repos, backward moves
// in_progress → ready and in_review → in_progress, direct-to-main jumps
// ready|in_progress → merged, and abandoned reachable from every
// pre-merged state. Terminal-ish states are not strictly terminal: reopen
// returns to ready (a fresh claim is then required).
var legalTransitions = map[[2]string]bool{
	{"draft", "ready"}:                true,
	{"ready", "in_progress"}:          true,
	{"in_progress", "in_review"}:      true,
	{"in_progress", "ready"}:          true,
	{"in_review", "in_progress"}:      true,
	{"ready", "merged"}:               true,
	{"in_progress", "merged"}:         true,
	{"in_review", "merged"}:           true,
	{"merged", "deployed_dev"}:        true,
	{"merged", "deployed_prod"}:       true,
	{"merged", "released"}:            true,
	{"deployed_dev", "deployed_prod"}: true,
	{"deployed_dev", "released"}:      true,
	{"draft", "abandoned"}:            true,
	{"ready", "abandoned"}:            true,
	{"in_progress", "abandoned"}:      true,
	{"in_review", "abandoned"}:        true,
	{"merged", "ready"}:               true,
	{"deployed_dev", "ready"}:         true,
	{"deployed_prod", "ready"}:        true,
	{"released", "ready"}:             true,
	{"abandoned", "ready"}:            true,
}

// CreateTask allocates the next <KEY>-<n> id from the project's counter and
// inserts the task inside the given transaction. It is meant to be called
// from a RecordEvent apply callback with the store's clock as now.
func CreateTask(tx *sql.Tx, now time.Time, in TaskInput) (*Task, error) {
	if in.Concern != "" && !ValidConcern(in.Concern) {
		return nil, fmt.Errorf("unknown concern %q: %w", in.Concern, ErrInvalidInput)
	}

	var n int64
	var key string
	if err := tx.QueryRow(
		`UPDATE projects SET next_task_num = next_task_num + 1
		 WHERE id = $1 RETURNING key, next_task_num - 1`, in.ProjectID,
	).Scan(&key, &n); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("project %s: %w", in.ProjectID, ErrNotFound)
		}
		return nil, fmt.Errorf("allocate task id: %w", err)
	}
	id := fmt.Sprintf("%s-%d", key, n)

	state := "ready"
	if in.Draft {
		state = "draft"
	}
	ts := now.UTC().Truncate(time.Second)

	var createdBy sql.NullString
	if in.CreatedBy != "" {
		createdBy = sql.NullString{String: in.CreatedBy, Valid: true}
	}
	var concern sql.NullString
	if in.Concern != "" {
		concern = sql.NullString{String: in.Concern, Valid: true}
	}
	_, err := tx.Exec(
		`INSERT INTO tasks (id, project_id, title, body, priority, kind, state, concern, created_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		id, in.ProjectID, in.Title, in.Body, in.Priority, in.Kind, state, concern, createdBy, ts, ts,
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
		Concern:   in.Concern,
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
	err := tx.QueryRow(`SELECT state FROM tasks WHERE id = $1`, taskID).Scan(&current)
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
		`UPDATE tasks SET state = $1, updated_at = $2 WHERE id = $3`,
		to, now.UTC(), taskID,
	)
	if err != nil {
		return fmt.Errorf("update task %s state: %w", taskID, err)
	}
	return LogChange(tx, "task", taskID, eventID,
		map[string]string{"field": "state", "old": from, "new": to})
}

// TaskState returns the current state of a task inside the given transaction
// (ErrNotFound if the task does not exist). Use it when the from-state of a
// Transition must be read atomically with the transition itself.
func TaskState(tx *sql.Tx, taskID string) (string, error) {
	var state string
	err := tx.QueryRow(`SELECT state FROM tasks WHERE id = $1`, taskID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("task %s: %w", taskID, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("get task %s state: %w", taskID, err)
	}
	return state, nil
}

// validPriorities is the tasks.priority CHECK constraint, mirrored in Go so
// callers get a clean error instead of a raw constraint violation.
var validPriorities = map[string]bool{
	"critical": true, "high": true, "medium": true, "low": true,
}

// validConcerns is the tasks.concern CHECK constraint, mirrored in Go so
// callers get a clean error instead of a raw constraint violation.
var validConcerns = map[string]bool{
	"completeness": true, "performance": true, "usability": true, "security": true,
}

// ValidConcern reports whether s is one of the recognized task concerns.
func ValidConcern(s string) bool {
	return validConcerns[s]
}

// UpdateTaskFields updates the non-nil fields of a task inside the given
// transaction and bumps updated_at. Returns ErrNotFound if the task does not
// exist. A nil field is left unchanged; all-nil is a no-op (existence is
// still checked). concern follows special clearing rules: "" or "none" clears
// it to NULL; any other value must be a valid concern. A blank title is
// rejected, mirroring CreateTask: every task keeps a title for its whole life.
func UpdateTaskFields(tx *sql.Tx, now time.Time, id string, title, body, priority, concern *string, needsDecomposition *bool) error {
	if title != nil && strings.TrimSpace(*title) == "" {
		return fmt.Errorf("title must not be blank: %w", ErrInvalidInput)
	}
	if priority != nil && !validPriorities[*priority] {
		return fmt.Errorf("unknown priority %q: %w", *priority, ErrInvalidInput)
	}
	if concern != nil && *concern != "" && *concern != "none" && !ValidConcern(*concern) {
		return fmt.Errorf("unknown concern %q: %w", *concern, ErrInvalidInput)
	}
	var sets []string
	var args []any
	set := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if title != nil {
		set("title", *title)
	}
	if body != nil {
		set("body", *body)
	}
	if priority != nil {
		set("priority", *priority)
	}
	if concern != nil {
		if *concern == "" || *concern == "none" {
			set("concern", sql.NullString{})
		} else {
			set("concern", sql.NullString{String: *concern, Valid: true})
		}
	}
	if needsDecomposition != nil {
		set("needs_decomposition", *needsDecomposition)
	}
	set("updated_at", now.UTC())
	args = append(args, id)

	res, err := tx.Exec(
		fmt.Sprintf(`UPDATE tasks SET %s WHERE id = $%d`, strings.Join(sets, `, `), len(args)),
		args...)
	if err != nil {
		return fmt.Errorf("update task %s: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update task %s rows affected: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("task %s: %w", id, ErrNotFound)
	}
	return nil
}

// taskColumns is the SELECT list scanTask expects, in order.
const taskColumns = `id, project_id, title, body, priority, kind, state, concern, needs_decomposition, created_by, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner) (*Task, error) {
	var t Task
	var body, createdBy, concern sql.NullString
	if err := row.Scan(&t.ID, &t.ProjectID, &t.Title, &body, &t.Priority, &t.Kind,
		&t.State, &concern, &t.NeedsDecomposition, &createdBy, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.Body = body.String
	t.Concern = concern.String
	t.CreatedBy = createdBy.String
	t.CreatedAt = t.CreatedAt.UTC()
	t.UpdatedAt = t.UpdatedAt.UTC()
	return &t, nil
}

// SlugifyTitle turns a task title into a branch-name slug: lowercase, every
// run of non-alphanumeric (ASCII) characters becomes a single '-', leading and
// trailing '-' are trimmed, at most 40 characters, and "task" if nothing
// remains. Non-ASCII letters are treated as separators so slugs are always safe
// git branch components. It is the single source of truth for the branch
// naming convention (see BranchFor); the api package re-exports it.
func SlugifyTitle(title string) string {
	var b strings.Builder
	pendingDash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if pendingDash && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingDash = false
			b.WriteRune(r)
		} else {
			pendingDash = true
		}
	}
	s := b.String()
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	if s == "" {
		return "task"
	}
	return s
}

// BranchFor returns the conventional git branch for a task:
// <prefix><id>-<slug>, with the prefix from SetBranchPrefix.
func BranchFor(t *Task) string {
	return BranchPrefix() + t.ID + "-" + SlugifyTitle(t.Title)
}

// GetTask looks up a task by id. Returns ErrNotFound if it does not exist.
func (s *Store) GetTask(ctx context.Context, id string) (*Task, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE id = $1`, id)
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
// first), then by task id in CompareTaskIDs order (key lexically, suffix
// numerically, so WL-9 precedes WL-10).
func (s *Store) ListTasks(ctx context.Context, f TaskFilter) ([]Task, error) {
	q := `SELECT ` + taskColumns + ` FROM tasks`
	var conds []string
	var args []any
	if f.Project != "" {
		args = append(args, f.Project)
		conds = append(conds, fmt.Sprintf(`project_id = $%d`, len(args)))
	}
	if len(f.States) > 0 {
		var placeholders []string
		for _, st := range f.States {
			args = append(args, st)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		conds = append(conds, `state IN (`+strings.Join(placeholders, ", ")+`)`)
	}
	if f.Priority != "" {
		args = append(args, f.Priority)
		conds = append(conds, fmt.Sprintf(`priority = $%d`, len(args)))
	}
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	// Same order as CompareTaskIDs — key lexically, then the numeric suffix,
	// so WL-9 precedes WL-10 — but done in the database.
	q += ` ORDER BY CASE priority
	         WHEN 'critical' THEN 0
	         WHEN 'high' THEN 1
	         WHEN 'medium' THEN 2
	         ELSE 3
	       END, split_part(id, '-', 1), CAST(split_part(id, '-', 2) AS INTEGER)`

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
// transaction. Self-edges are rejected for both types. A child_of edge must
// also satisfy the spec-018 hierarchy invariants (see checkHierarchy): an epic
// parent, one project, one parent per task, no cycle, and at most
// maxHierarchyDepth edges. A missing endpoint returns ErrNotFound.
func AddEdge(tx *sql.Tx, now time.Time, fromTask, toTask, typ string) error {
	if typ != "child_of" && typ != "blocks" {
		return fmt.Errorf("unknown edge type %q: %w", typ, ErrInvalidInput)
	}
	if fromTask == toTask {
		return fmt.Errorf("self-edge %s %s %s not allowed: %w", fromTask, typ, toTask, ErrInvalidInput)
	}
	project := map[string]string{}
	kind := map[string]string{}
	for _, id := range []string{fromTask, toTask} {
		var p, k string
		err := tx.QueryRow(`SELECT project_id, kind FROM tasks WHERE id = $1`, id).Scan(&p, &k)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("task %s: %w", id, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("check task %s: %w", id, err)
		}
		project[id], kind[id] = p, k
	}
	if typ == "child_of" {
		if err := checkHierarchy(tx, fromTask, toTask, project, kind); err != nil {
			return err
		}
	}
	_, err := tx.Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, $3, $4)`,
		fromTask, toTask, typ, now.UTC(),
	)
	if err != nil {
		// The partial unique index is the backstop for a second parent racing
		// checkHierarchy's read; both report the same shape.
		if isUniqueViolationOn(err, "task_edges_single_parent") {
			return fmt.Errorf("task %s already has a parent: %w", fromTask, ErrEdgeExists)
		}
		if isUniqueViolation(err) {
			return fmt.Errorf("edge %s %s %s: %w", fromTask, typ, toTask, ErrEdgeExists)
		}
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
			`SELECT to_task FROM task_edges WHERE from_task = $1 AND type = 'child_of'`, cur)
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
		`DELETE FROM task_edges WHERE from_task = $1 AND to_task = $2 AND type = $3`,
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
	if out, err = list(`from_task = $1`); err != nil {
		return nil, nil, err
	}
	if in, err = list(`to_task = $1`); err != nil {
		return nil, nil, err
	}
	return out, in, nil
}

// closedStates is the SQL tuple of task states that no longer block
// dependents: everything from merged onward, plus abandoned.
const closedStates = `('merged', 'deployed_dev', 'deployed_prod', 'released', 'abandoned')`

// blockedCondition matches 'blocks' edges whose blocker (from_task) is still
// open, i.e. the edge currently blocks its to_task.
const blockedCondition = `e.type = 'blocks'
	 AND EXISTS (SELECT 1 FROM tasks b
	             WHERE b.id = e.from_task
	               AND b.state NOT IN ` + closedStates + `)`

// BlockedTaskIDs returns the ids of tasks that have at least one open
// 'blocks' edge pointing at them (the blocker is not in a closed state).
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
		`SELECT EXISTS (SELECT 1 FROM task_edges e WHERE e.to_task = $1 AND `+blockedCondition+`)`,
		taskID,
	).Scan(&blocked)
	if err != nil {
		return false, fmt.Errorf("is blocked %s: %w", taskID, err)
	}
	return blocked, nil
}
