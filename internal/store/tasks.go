package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/secrets"
)

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
	Secrets   []string
	CreatedBy string
	Draft     bool
	Skills    []string
	// PlanDoc is the plan document this task was minted from (0 = none).
	// Written only by AcceptDoc's plan branch (025 §9.2) — no other caller
	// sets it.
	PlanDoc int64
	// AboutDoc is the document this task is about (0 = none) — the review or
	// design task's reference to the document that triggered its minting
	// (025 §15.4). Distinct from PlanDoc.
	AboutDoc int64
}

// TaskFilter narrows ListTasks. Zero-valued fields do not filter. Parent
// selects the direct children of one task.
type TaskFilter struct {
	Project  string
	States   []string
	Priority string
	Kind     string
	Parent   string
	Assignee string
	// HasChildren narrows to containers — tasks with at least one child_of
	// child. Container-ness is inferred from the edges, so this is the only
	// selector for it; no kind declares one (004 §6.1).
	HasChildren bool
	// Repo narrows to the tasks of the project that owns this "owner/name"
	// repo. A repo maps to at most one project (project_repos.repo is
	// UNIQUE), so this is Project by another key — the one a client running
	// inside a checkout actually has.
	Repo string
	// UpdatedSince narrows to the tasks touched at or after this instant —
	// the incremental fetch a polling mirror makes with the highest
	// updated_at it has already seen. The zero value does not filter.
	UpdatedSince time.Time
	// PlanDoc narrows to the tasks minted from this plan document (0 = none)
	// — the query that is the plan's task set (025 §9.2, §1).
	PlanDoc int64
	// AboutDoc narrows to the tasks that reference this document (0 = none)
	// — the review/design task set a document's lifecycle minted (025 §15.4).
	AboutDoc int64
	// Deleted switches the list from live tasks to tombstoned ones (044 §5).
	// It is a switch, not an addition: a list mixing the two invites acting on
	// a row that is not there. The zero value lists live tasks, which is what
	// every caller that predates the tombstone meant.
	Deleted bool
}

// Edge is a typed, directed link between two tasks. "A blocks B" means B is
// blocked until A reaches a closed state (see taskClosed); "A child_of B"
// makes B a container.
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

// allStates returns every state the task machine can occupy, sorted. It is
// derived from legalTransitions rather than listed, so it cannot fall behind
// the machine; TestTaskStateShapeMatchesStateMachine pins ns/shapes.ttl's
// wl:taskState sh:in list against it.
func allStates() []string {
	seen := make(map[string]bool, 16)
	for pair := range legalTransitions {
		seen[pair[0]] = true
		seen[pair[1]] = true
	}
	return slices.Sorted(maps.Keys(seen))
}

// containerForbiddenStates are the delivery states a task with children can
// never occupy. They are earned by observed deploy facts about a specific
// commit (spec 004 §5.2) and a container has no commit of its own. Checked on
// both ends of a transition so `lode task done` on a parent reports the
// roll-up rule instead of a from-state mismatch.
var containerForbiddenStates = map[string]bool{
	"in_review": true, "deployed_dev": true, "deployed_prod": true, "released": true,
}

// CreateTask allocates the next <KEY>-<n> id from the project's counter and
// inserts the task inside the given transaction. It is meant to be called
// from a RecordEvent apply callback with the store's clock as now, and
// appends a state_log row attributed to eventID.
func CreateTask(tx *sql.Tx, now time.Time, in TaskInput, eventID int64) (*model.Task, error) {
	if in.Concern != "" && !ValidConcern(in.Concern) {
		return nil, fmt.Errorf("unknown concern %q: %w", in.Concern, ErrInvalidInput)
	}
	// Before the id is allocated: a rejected input must not burn a task number.
	skills, err := normalizePins(in.Skills)
	if err != nil {
		return nil, fmt.Errorf("create task: %w", err)
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
	skillsJSON, err := json.Marshal(skills)
	if err != nil {
		return nil, fmt.Errorf("marshal task %s skills: %w", id, err)
	}
	secretsVal, err := secretsJSON(in.Secrets)
	if err != nil {
		return nil, err
	}
	secretNames := in.Secrets
	if secretNames == nil {
		secretNames = []string{}
	}
	_, err = tx.Exec(
		`INSERT INTO tasks (id, project_id, title, body, priority, kind, state, concern, created_by, created_at, updated_at, skills, secrets, plan_doc, about_doc)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::jsonb, $13::jsonb, $14, $15)`,
		id, in.ProjectID, in.Title, in.Body, in.Priority, in.Kind, state, concern, createdBy, ts, ts,
		string(skillsJSON), string(secretsVal), nullID(in.PlanDoc), nullID(in.AboutDoc),
	)
	if err != nil {
		return nil, fmt.Errorf("insert task %s: %w", id, err)
	}
	if err := LogChange(tx, "task", id, eventID,
		map[string]string{"field": "state", "new": state}); err != nil {
		return nil, err
	}
	created := &model.Task{
		ID:        id,
		Project:   in.ProjectID,
		Title:     in.Title,
		Body:      in.Body,
		Priority:  in.Priority,
		Kind:      in.Kind,
		State:     state,
		Concern:   in.Concern,
		CreatedBy: in.CreatedBy,
		CreatedAt: ts,
		UpdatedAt: ts,
		Skills:    skills,
		Secrets:   secretNames,
		PlanDoc:   in.PlanDoc,
		AboutDoc:  in.AboutDoc,
	}
	created.Branch = BranchFor(created)
	return created, nil
}

// Transition moves a task from one state to another inside the given
// transaction. The move must be in legalTransitions and the task's current
// state must equal from (otherwise ErrBadTransition; unknown task is
// ErrNotFound). A task with children is additionally barred from every
// delivery state (see containerForbiddenStates), since its state is driven
// entirely by its children. It bumps updated_at and appends a state_log row attributed to
// eventID. A task with a parent rolls that parent up in the same transaction
// (see resolveParent).
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
	// Only a move touching a forbidden state pays for the children lookup.
	if containerForbiddenStates[from] || containerForbiddenStates[to] {
		container, err := hasChildren(tx, taskID)
		if err != nil {
			return err
		}
		if container {
			return fmt.Errorf("task %s has children: its state follows them, so it cannot move %s -> %s (close its children instead): %w",
				taskID, from, to, ErrBadTransition)
		}
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
	if err := LogChange(tx, "task", taskID, eventID,
		map[string]string{"field": "state", "old": from, "new": to}); err != nil {
		return err
	}
	return resolveParent(tx, now, taskID, eventID)
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

// secretsJSON marshals a secret-name list for the tasks.secrets jsonb column,
// validating every name. nil marshals as [].
func secretsJSON(names []string) ([]byte, error) {
	for _, n := range names {
		if !secrets.ValidName(n) {
			return nil, fmt.Errorf("invalid secret name %q: %w", n, ErrInvalidInput)
		}
	}
	if names == nil {
		names = []string{}
	}
	return json.Marshal(names)
}

// UpdateTaskFields updates the non-nil fields of a task inside the given
// transaction and bumps updated_at. Returns ErrNotFound if the task does not
// exist. A nil field is left unchanged; all-nil is a no-op (existence is
// still checked). concern follows special clearing rules: "" or "none" clears
// it to NULL; any other value must be a valid concern. A blank title is
// rejected, mirroring CreateTask: every task keeps a title for its whole life.
// secretNames, when non-nil, replaces the whole tasks.secrets list.
func UpdateTaskFields(tx *sql.Tx, now time.Time, id string, title, body, priority, concern *string, secretNames *[]string, needsDecomposition *bool) error {
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
	if secretNames != nil {
		val, err := secretsJSON(*secretNames)
		if err != nil {
			return err
		}
		set("secrets", val)
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
	return requireOneAffected(res, "update task "+id,
		fmt.Errorf("task %s: %w", id, ErrNotFound))
}

// taskColumns is the SELECT list scanTask expects, in order. skills,
// secrets, plan_doc and about_doc are last so positional scans elsewhere are
// unaffected by their addition. skills and secrets are "jsonb NOT NULL
// DEFAULT '[]'" (see migrations 0007 and 0024), so a bare cast is enough — no
// coalesce needed; plan_doc and about_doc are nullable bigints (migrations
// 0027 and 0028), scanned into sql.NullInt64. The three tombstone columns
// (migration 0033) are last for the same reason, and are all-null or all-set
// together. prefixedTaskColumns below requires each entry to be comma-free.
const taskColumns = `id, project_id, title, body, priority, kind, state, concern, assignee, needs_decomposition, created_by, created_at, updated_at, skills::text, secrets::text, plan_doc, about_doc, deleted_at, deleted_by, delete_justification`

// taskColumnsT is taskColumns under the `t` alias, for the queries that join
// tasks against another table.
var taskColumnsT = qualifyColumns(taskColumns, "t")

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner) (*model.Task, error) {
	var t model.Task
	var body, createdBy, concern, assignee sql.NullString
	var skillsJSON, secretsCol string
	var planDoc, aboutDoc sql.NullInt64
	var deletedAt sql.NullTime
	var deletedBy, justification sql.NullString
	if err := row.Scan(&t.ID, &t.Project, &t.Title, &body, &t.Priority, &t.Kind,
		&t.State, &concern, &assignee, &t.NeedsDecomposition, &createdBy, &t.CreatedAt, &t.UpdatedAt, &skillsJSON, &secretsCol, &planDoc, &aboutDoc,
		&deletedAt, &deletedBy, &justification); err != nil {
		return nil, err
	}
	t.Tombstone = tombstoneFrom(deletedAt, deletedBy, justification)
	t.PlanDoc = planDoc.Int64
	t.AboutDoc = aboutDoc.Int64
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
	if err := json.Unmarshal([]byte(secretsCol), &t.Secrets); err != nil {
		return nil, fmt.Errorf("unmarshal task %s secrets: %w", t.ID, err)
	}
	if t.Secrets == nil {
		t.Secrets = []string{}
	}
	// Branch is derived, not stored: the server owns LODE_BRANCH_TEMPLATE and
	// is the authority on branch names (008 §3.1). Filling it here means every
	// path that reads a task serves the same name.
	t.Branch = BranchFor(&t)
	return &t, nil
}

// maxTaskPins caps a task's pinned skill list. Every pin is inlined whole
// into every brief for that task, so an unbounded list crowds out the task
// itself; 20 is far above any real pin set.
const maxTaskPins = 20

// normalizePins cleans a pinned-skill list: trim, drop blanks, dedupe keeping
// first-occurrence order. Both write paths use it — storing pins verbatim on
// create produced "pinned skill not found" warnings for whitespace and for
// the empty string in every brief the task ever served. Over the cap is an
// error rather than a silent truncation: a caller must not be told its pins
// were stored when some were dropped.
func normalizePins(skills []string) ([]string, error) {
	clean := make([]string, 0, len(skills))
	seen := map[string]bool{}
	for _, sk := range skills {
		sk = strings.TrimSpace(sk)
		if sk == "" || seen[sk] {
			continue
		}
		seen[sk] = true
		clean = append(clean, sk)
	}
	if len(clean) > maxTaskPins {
		return nil, fmt.Errorf("%d pinned skills exceeds the maximum of %d: %w",
			len(clean), maxTaskPins, ErrInvalidInput)
	}
	return clean, nil
}

// SetTaskSkills replaces the task's pinned skill names inside the given
// transaction and bumps updated_at, matching UpdateTaskFields and Transition.
// nil normalizes to an empty pin list rather than a SQL NULL — SkillsByNames
// reads the column with jsonb_array_elements_text, which errors on a JSON
// null. See normalizePins for the cleaning rules. Returns ErrNotFound if the
// task does not exist.
func SetTaskSkills(tx *sql.Tx, now time.Time, id string, skills []string) error {
	clean, err := normalizePins(skills)
	if err != nil {
		return fmt.Errorf("set task skills %s: %w", id, err)
	}
	b, err := json.Marshal(clean)
	if err != nil {
		return fmt.Errorf("set task skills %s: %w", id, err)
	}
	res, err := tx.Exec(`UPDATE tasks SET skills = $2::jsonb, updated_at = $3 WHERE id = $1`,
		id, string(b), now.UTC())
	if err != nil {
		return fmt.Errorf("set task skills %s: %w", id, err)
	}
	return requireOneAffected(res, "set task skills "+id,
		fmt.Errorf("task %s: %w", id, ErrNotFound))
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

// GetTask looks up a task by id. Returns ErrNotFound if it does not exist.
func (s *Store) GetTask(ctx context.Context, id string) (*model.Task, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE id = $1`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get task %s: %w", id, err)
	}
	closed, err := s.ClosedTaskIDs(ctx, []string{id})
	if err != nil {
		return nil, fmt.Errorf("closed for task %s: %w", id, err)
	}
	t.Closed = closed[id]
	return t, nil
}

// ListTasks returns tasks matching the filter, ordered by priority (critical
// first), then by task id in CompareTaskIDs order (key lexically, suffix
// numerically, so WL-9 precedes WL-10).
func (s *Store) ListTasks(ctx context.Context, f TaskFilter) ([]model.Task, error) {
	q := `SELECT ` + taskColumns + ` FROM tasks`
	var conds []string
	var args sqlArgs
	// 044 §4: a tombstoned task is out of every list by default.
	if f.Deleted {
		conds = append(conds, `deleted_at IS NOT NULL`)
	} else {
		conds = append(conds, `deleted_at IS NULL`)
	}
	if f.Project != "" {
		conds = append(conds, `project_id = `+args.next(f.Project))
	}
	if len(f.States) > 0 {
		conds = append(conds, `state = ANY(`+args.next(f.States)+`)`)
	}
	if f.Priority != "" {
		conds = append(conds, `priority = `+args.next(f.Priority))
	}
	if f.Kind != "" {
		conds = append(conds, `kind = `+args.next(f.Kind))
	}
	if f.PlanDoc != 0 {
		conds = append(conds, `plan_doc = `+args.next(f.PlanDoc))
	}
	if f.AboutDoc != 0 {
		conds = append(conds, `about_doc = `+args.next(f.AboutDoc))
	}
	if f.Assignee != "" {
		conds = append(conds, `assignee = `+args.next(f.Assignee))
	}
	if f.Parent != "" {
		conds = append(conds, `EXISTS (SELECT 1 FROM task_edges e
		          WHERE e.from_task = tasks.id AND e.to_task = `+args.next(f.Parent)+` AND e.type = 'child_of')`)
	}
	if f.HasChildren {
		// Only live children make a container, exactly as hasChildren reads
		// it (044 §4) — otherwise a task whose only children are tombstoned
		// would list as a container the claim path treats as an ordinary task.
		conds = append(conds, `EXISTS (SELECT 1 FROM task_edges c
		                              JOIN tasks ct ON ct.id = c.from_task AND ct.deleted_at IS NULL
		                              WHERE c.to_task = tasks.id AND c.type = 'child_of')`)
	}
	if f.Repo != "" {
		conds = append(conds, `EXISTS (SELECT 1 FROM project_repos pr
		          WHERE pr.repo = `+args.next(f.Repo)+` AND pr.project_id = tasks.project_id)`)
	}
	if !f.UpdatedSince.IsZero() {
		// >=, never >: the caller sends the highest updated_at it has seen and
		// re-receives that boundary row, which is cheap and idempotent. With >
		// a second write landing in the same clock tick as the watermark would
		// never be handed out again — a silently lost update.
		conds = append(conds, `updated_at >= `+args.next(f.UpdatedSince.UTC()))
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

	rows, err := s.db.QueryContext(ctx, q, args.vals...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	out, err := collectRows(rows, "list tasks", byValue(scanTask))
	if err != nil {
		return nil, err
	}
	// A second query, not folded into taskColumns: taskClosed's rendered
	// subqueries bind ch/tc/mc/pr, which would collide with every other query
	// sharing that column list. collectRows has already closed the cursor, so
	// this does not hold that connection open while it runs.
	ids := make([]string, len(out))
	for i, t := range out {
		ids[i] = t.ID
	}
	closed, err := s.ClosedTaskIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	for i := range out {
		out[i].Closed = closed[out[i].ID]
	}
	return out, nil
}

// AddEdge inserts a typed edge between two existing tasks inside the given
// transaction. Self-edges are rejected for all three types. A child_of edge
// must also satisfy the spec-004 hierarchy invariants (see checkHierarchy):
// one project, one parent per task, no cycle, and at most maxHierarchyDepth
// edges. follow_up_to is unchecked beyond the single-origin index: it is
// cross-project by design and nothing walks it transitively. A missing
// endpoint returns ErrNotFound. Appends a state_log row for both endpoints,
// attributed to eventID, so a cross-project edge dirties both projects.
func AddEdge(tx *sql.Tx, now time.Time, fromTask, toTask, typ string, eventID int64) error {
	if typ != "child_of" && typ != "blocks" && typ != "follow_up_to" {
		return fmt.Errorf("unknown edge type %q: %w", typ, ErrInvalidInput)
	}
	if fromTask == toTask {
		return fmt.Errorf("self-edge %s %s %s not allowed: %w", fromTask, typ, toTask, ErrInvalidInput)
	}
	project := map[string]string{}
	for _, id := range []string{fromTask, toTask} {
		var p string
		err := tx.QueryRow(`SELECT project_id FROM tasks WHERE id = $1`, id).Scan(&p)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("task %s: %w", id, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("check task %s: %w", id, err)
		}
		project[id] = p
	}
	if typ == "child_of" {
		if err := checkHierarchy(tx, fromTask, toTask, project); err != nil {
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
		if isUniqueViolationOn(err, "task_edges_single_origin") {
			return fmt.Errorf("task %s is already a follow-up to another task: %w",
				fromTask, ErrEdgeExists)
		}
		if isUniqueViolation(err) {
			return fmt.Errorf("edge %s %s %s: %w", fromTask, typ, toTask, ErrEdgeExists)
		}
		return fmt.Errorf("insert edge %s %s %s: %w", fromTask, typ, toTask, err)
	}
	change := map[string]string{"field": "edge", "op": "add", "type": typ,
		"from": fromTask, "to": toTask}
	if err := LogChange(tx, "task", fromTask, eventID, change); err != nil {
		return err
	}
	if err := LogChange(tx, "task", toTask, eventID, change); err != nil {
		return err
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
// ErrNotFound if the edge does not exist. Appends a state_log row for both
// endpoints, attributed to eventID, matching AddEdge.
func RemoveEdge(tx *sql.Tx, fromTask, toTask, typ string, eventID int64) error {
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
	change := map[string]string{"field": "edge", "op": "remove", "type": typ,
		"from": fromTask, "to": toTask}
	if err := LogChange(tx, "task", fromTask, eventID, change); err != nil {
		return err
	}
	if err := LogChange(tx, "task", toTask, eventID, change); err != nil {
		return err
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

// TaskEdges is one task's edges, in the same split ListEdges returns.
type TaskEdges struct {
	Out []Edge
	In  []Edge
}

// ListEdgesForTasks returns the edges touching each of ids, keyed by task id,
// in one query. The bulk form of ListEdges: a list endpoint that reported
// edges by calling ListEdges per row would issue one query per task. Tasks
// with no edges are absent from the map; ids is empty-safe.
//
// Ordering within each slice matches ListEdges (from_task, to_task, type) so
// callers see the same sequence whichever reader they used.
func (s *Store) ListEdgesForTasks(ctx context.Context, ids []string) (map[string]TaskEdges, error) {
	m := map[string]TaskEdges{}
	if len(ids) == 0 {
		return m, nil
	}

	inSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		inSet[id] = true
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT from_task, to_task, type FROM task_edges WHERE from_task = ANY($1) OR to_task = ANY($1) ORDER BY from_task, to_task, type`,
		ids)
	if err != nil {
		return nil, fmt.Errorf("list edges for tasks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.FromTask, &e.ToTask, &e.Type); err != nil {
			return nil, fmt.Errorf("scan edge: %w", err)
		}
		if inSet[e.FromTask] {
			te := m[e.FromTask]
			te.Out = append(te.Out, e)
			m[e.FromTask] = te
		}
		if inSet[e.ToTask] {
			te := m[e.ToTask]
			te.In = append(te.In, e)
			m[e.ToTask] = te
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list edges for tasks: %w", err)
	}
	return m, nil
}

// deliveryRanks places the delivery states on one "how far delivered" axis, so
// "at or past a repo's done_state" is a single integer comparison.
//
// The two terminals share a rank deliberately. §5.1's branches never meet: a
// prod repo walks merged → deployed_dev → deployed_prod, a release repo walks
// merged → deployed_dev → released, and `deployed_prod → released` is not a
// legal transition. Ordering one terminal above the other would wedge a task
// that reached the *other* branch's terminal — after `lode project set-repo
// --done-state` (§5.4), or when a multi-repo task's prod deploy lands before
// its release — permanently open with no state left to advance to. Calling
// them peers under-blocks in that corner instead, which is where the old
// fixed tuple already sat.
var deliveryRanks = map[string]int{
	"merged": 1, "deployed_dev": 2, "deployed_prod": 3, "released": 3,
}

// deliveredStateSet is every ranked state plus abandoned: the states in which
// a task has no work left for anyone to own, whatever repo it belongs to. It
// answers the state-only question assign.go asks. It is *not* the blocking
// predicate — that is per repo, see taskClosed.
var deliveredStateSet = func() map[string]bool {
	m := map[string]bool{"abandoned": true}
	for st := range deliveryRanks {
		m[st] = true
	}
	return m
}()

// deliveryRankArms is deliveryRank's WHEN list. Only the scrutinee varies
// between call sites, so the arms are rendered once.
var deliveryRankArms = func() string {
	var b strings.Builder
	for _, st := range slices.Sorted(maps.Keys(deliveryRanks)) {
		fmt.Fprintf(&b, " WHEN '%s' THEN %d", st, deliveryRanks[st])
	}
	return b.String()
}()

// deliveryRank renders the SQL rank of a state expression. A task's state and
// a repo's done_state both map through the same CASE, so the two are directly
// comparable. Anything before merged ranks 0, below every done_state.
func deliveryRank(expr string) string {
	return "(CASE " + expr + deliveryRankArms + " ELSE 0 END)"
}

// taskClosed renders the "no longer blocks its dependents" predicate for the
// tasks row aliased as alias. Per spec 004 §1.3 the closed set is per repo,
// not one fixed tuple of states: a task is closed when it is abandoned, or
// when its state is at or past the done_state of every repo its work *landed*
// in. The same merged task is closed where done_state = 'merged' and still
// open where the repo gates on 'released'.
//
// Landed, not merely attributed: a task branch's pushes each write a
// task_commits row whether or not that work ever reaches the default branch
// (internal/hooks/push.go), so the repo set is the one LandedMainID reads —
// task_commits joined to main_commits. Without the join, an abandoned approach
// pushed to a branch in some other repo would gate the task on that repo's
// done_state forever, and ResolveDelivery — which walks the same join — could
// never advance it.
//
// A repo the task landed in that no project maps takes DefaultDoneState,
// matching RepoDoneState: an unconfigured repo must not block forever.
//
// A task with children is the one state-fixed case (§6.4): it has no commit of
// its own, cannot advance past merged, and so is closed at merged in every
// repo. It is checked explicitly rather than left to "a container has no
// commits", since AddEdge can give children to a task that already landed some.
// A task with no landed commit at all — marked merged by hand — has no repo to
// gate on and closes at merged too. Only *live* children make a container: a
// tombstoned child does not count.
//
// Deleted is deliberately *not* folded in here. The tombstone is orthogonal to
// state (044 §1), and this predicate is what ClosedTaskIDs answers `Closed`
// with on the wire: a tombstoned draft is a hidden draft, not a closed one.
// Each caller that means "live and open" adds `deleted_at IS NULL` itself —
// blockedCondition, planUnfinished, the open-blocker queries, OpenTaskForDoc —
// and the roll-up queries filter their children before they get here.
//
// The rendered subqueries bind `ch`, `cht`, `tc`, `mc` and `pr`; an enclosing
// query must not reuse those aliases.
func taskClosed(alias string) string {
	state := alias + ".state"
	return `(` + state + ` = 'abandoned' OR (` + deliveryRank(state) + ` > 0
	     AND (EXISTS (SELECT 1 FROM task_edges ch
	                  JOIN tasks cht ON cht.id = ch.from_task AND cht.deleted_at IS NULL
	                  WHERE ch.to_task = ` + alias + `.id AND ch.type = 'child_of')
	          OR NOT EXISTS (SELECT 1 FROM task_commits tc
	                         JOIN main_commits mc ON mc.repo = tc.repo AND mc.sha = tc.sha
	                         LEFT JOIN project_repos pr ON pr.repo = tc.repo
	                         WHERE tc.task_id = ` + alias + `.id
	                           AND ` + deliveryRank(state) + ` <
	                               ` + deliveryRank(`COALESCE(pr.done_state, '`+DefaultDoneState+`')`) + `))))`
}

// blockedCondition matches 'blocks' edges whose blocker (from_task) is still
// open, i.e. the edge currently blocks its to_task. The blocker must also be
// live: a deleted blocker stops blocking without its edge being retracted
// (044 §4), which is why the filter sits here and not in taskClosed.
var blockedCondition = `e.type = 'blocks'
	 AND EXISTS (SELECT 1 FROM tasks b
	             WHERE b.id = e.from_task
	               AND b.deleted_at IS NULL
	               AND NOT ` + taskClosed("b") + `)`

// planUnfinished renders "the plan document aliased as alias still has work
// outstanding" (025 §9.3): any open task in its set, or a set not yet minted
// because the document is still draft (§7's literal sentence would read that
// empty set as finished; §10 calls an unminted set unfinished).
//
// "Open" is taskClosed's complement, so this and blockedCondition cannot drift
// on what closed means. It binds `bt` on top of the aliases taskClosed binds.
// Every surface that reports plan-to-plan blocking renders this one predicate
// — planBlockedCondition for the gate, the blocking-plan queries for the
// cockpit and the brief — so no surface can disagree with Claim.
//
// A tombstoned plan holds nothing: it is out of the picture entirely. Its
// tasks are filtered the same way — a deleted task is not outstanding work,
// even though taskClosed alone would still call it open.
func planUnfinished(alias string) string {
	return `(` + alias + `.deleted_at IS NULL
	     AND (` + alias + `.status = 'draft'
	          OR EXISTS (SELECT 1 FROM tasks bt
	                     WHERE bt.plan_doc = ` + alias + `.id
	                       AND bt.deleted_at IS NULL
	                       AND NOT ` + taskClosed("bt") + `)))`
}

// planBlockedCondition holds a task while its plan is ordered after another
// plan whose work is unfinished (025 §9.3): a document-level blocks edge
// between the two plan documents, evaluated over the blocking plan through
// planUnfinished.
//
// It binds `de` and `bd` on top of the aliases planUnfinished binds; the
// enclosing query must alias the task row as `t`.
var planBlockedCondition = `t.plan_doc IS NOT NULL AND EXISTS (
	 SELECT 1 FROM doc_edges de
	  JOIN docs bd ON bd.id = de.from_doc
	  WHERE de.type = 'blocks' AND de.to_doc = t.plan_doc
	    AND ` + planUnfinished("bd") + `)`

// BlockedTaskIDs returns the ids of tasks that have at least one open
// 'blocks' edge pointing at them (the blocker is not in a closed state), plus
// the tasks a plan-to-plan ordering edge holds. It answers the same question
// IsBlocked answers per task, so the two must name the same set: a task shown
// as pickable that Claim then refuses is worse than no badge at all.
func (s *Store) BlockedTaskIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT e.to_task FROM task_edges e
		   JOIN tasks d ON d.id = e.to_task AND d.deleted_at IS NULL
		  WHERE `+blockedCondition+`
		 UNION
		 SELECT t.id FROM tasks t WHERE t.deleted_at IS NULL AND `+planBlockedCondition)
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

// ClosedTaskIDs reports, for the given task ids, which are closed by the
// per-repo predicate in taskClosed (004 §1.3; see also 026 §2.5, which
// requires closure be the server's answer since a client cannot evaluate a
// predicate over other repos' done_state and landed-commit facts). An empty
// ids returns an empty map without touching the database. A tombstoned id is
// answered on its state like any other: the tombstone is orthogonal to state
// (044 §1), so a deleted draft answers Closed false and a deleted merged task
// answers what its repos say. What stops a deleted task blocking or holding a
// plan open is the live filter at those queries, not this verdict.
func (s *Store) ClosedTaskIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id FROM tasks t WHERE t.id = ANY($1) AND `+taskClosed("t"), ids)
	if err != nil {
		return nil, fmt.Errorf("closed task ids: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan closed task id: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("closed task ids: %w", err)
	}
	return out, nil
}

// IsBlocked reports whether taskID has an open 'blocks' edge pointing at it,
// or sits in a plan another plan is ordered before (planBlockedCondition). It
// runs inside the given transaction so lease claims can check it atomically.
//
// The plan arm reads the task row through a one-row subquery because the
// condition is written against a `t`-aliased row — the same text
// readyCandidates renders, so the claim path and the ready set cannot
// disagree about what is pickable.
func IsBlocked(tx *sql.Tx, taskID string) (bool, error) {
	var blocked bool
	err := tx.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM task_edges e WHERE e.to_task = $1 AND `+blockedCondition+`)
		     OR EXISTS (SELECT 1 FROM tasks t WHERE t.id = $1 AND `+planBlockedCondition+`)`,
		taskID,
	).Scan(&blocked)
	if err != nil {
		return false, fmt.Errorf("is blocked %s: %w", taskID, err)
	}
	return blocked, nil
}

// OpenTaskForDoc returns the id of an open task of the given kind that
// references doc, or "" — the §5 suppression guard of spec 025 §15.4,
// computed rather than stored (025 §1). Open means live and taskClosed's
// complement, the same notion the ready set and the blocks predicate share, so
// this guard cannot disagree with either about what "still open" means. A
// tombstoned task suppresses nothing (044 §4): the lifecycle should mint the
// replacement the operator deleted the old one to make room for.
func (s *Store) OpenTaskForDoc(ctx context.Context, docID int64, kind string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM tasks
		  WHERE about_doc = $1 AND kind = $2 AND deleted_at IS NULL
		    AND NOT `+taskClosed("tasks")+`
		  ORDER BY created_at, id LIMIT 1`, docID, kind,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open task for doc %d kind %s: %w", docID, kind, err)
	}
	return id, nil
}
