package model

import "time"

// TaskPriorities is the tasks.priority CHECK constraint's value set, most
// urgent first. Postgres is the authority; this is the one Go copy every
// layer reads — the API gate, the store gate, the web form's menu, the plan
// parser and the CLI's completion — so none of them can hold a stale list of
// its own. internal/store's TestPriorityCheckConstraintMatchesModel reads the
// constraint back and fails if the two drift.
var TaskPriorities = []string{"critical", "high", "medium", "low"}

// TaskStates is the tasks.state CHECK constraint's value set, in the
// lifecycle order the state machine walks. Same deal as TaskPriorities:
// internal/store's TestStateCheckConstraintMatchesModel pins it to the
// constraint and to legalTransitions, so a state added in a migration or in
// the machine cannot leave this list behind.
var TaskStates = []string{
	"draft", "ready", "in_progress", "in_review",
	"merged", "deployed_dev", "deployed_prod", "released", "abandoned",
}

// DisplayState is the state a surface shows for a task: "blocked" for a
// ready task with an open blocker or blocking plan, else the stored state.
// Blocked is derived and never stored, so it is not in TaskStates.
func DisplayState(state string, blocked bool) string {
	if state == "ready" && blocked {
		return "blocked"
	}
	return state
}

// TaskConcerns is the tasks.concern CHECK constraint's value set. Same deal
// as TaskPriorities: internal/store's TestConcernCheckConstraintMatchesModel
// pins it to the constraint, so a concern added in a migration cannot leave
// this list behind.
var TaskConcerns = []string{"completeness", "performance", "usability", "security"}

// Task is a unit of work. Concern is "" when the task has none; Assignee is
// "" when the task is unassigned; Skills is never nil (the store guarantees
// an empty slice, so the JSON reads [] rather than null).
type Task struct {
	ID                 string `json:"id"`
	Project            string `json:"project"`
	Title              string `json:"title"`
	Body               string `json:"body"`
	Priority           string `json:"priority"`
	Kind               string `json:"kind"`
	State              string `json:"state"`
	Concern            string `json:"concern"`
	NeedsDecomposition bool   `json:"needs_decomposition"`
	// HumanOnly marks a task no unattended worker may pick up: ready to work,
	// but only by a person (console-only steps like minting a cloud
	// credential). It keeps the task out of the ranked ready set that
	// `lode next` and the frontier share, while an explicit claim by id
	// still succeeds — that is the escape hatch for the person doing it.
	HumanOnly bool      `json:"human_only"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Skills    []string  `json:"skills"`
	Assignee  string    `json:"assignee"`
	// Branch is the server-authoritative task branch. It is derived from
	// LODE_BRANCH_TEMPLATE, which only the server knows, so a client matching
	// local refs to tasks reads it rather than rendering one (WL-REQ-267).
	Branch string `json:"branch"`
	// Secrets is the task's declared org-catalog secret names (WL-SPEC-74).
	// Names only; nil and empty are equivalent, always [] on the wire.
	Secrets []string `json:"secrets"`
	// PlanDoc is the plan document this task was minted from (WL-REQ-172); 0
	// (omitted on the wire) when no plan authored it.
	PlanDoc int64 `json:"plan_doc,omitempty"`
	// AboutDoc is the document this task is about (WL-RULE-179): set on review
	// tasks minted at submission and design tasks minted at acceptance. 0
	// (omitted on the wire) when the task carries no such reference. Distinct
	// from PlanDoc, which names the plan whose acceptance minted the task
	// (WL-REQ-172), not what the task is about.
	AboutDoc int64 `json:"about_doc,omitempty"`
	// AboutAnchor narrows AboutDoc to one section ("sec-3"), "" when the task
	// is about the whole document (WL-REQ-171). Only meaningful alongside
	// AboutDoc; it is what lets two escalations against different sections of
	// one spec stay two tasks.
	AboutAnchor string `json:"about_anchor,omitempty"`
	// Milestone is the milestone this task is attached to (WL-REQ-118), ""
	// when it is attached to none. Always in the task's own project — the
	// store refuses a cross-project attach.
	Milestone string `json:"milestone,omitempty"`
	// Closed reports whether the task has no work left for anyone to own, by
	// the per-repo predicate of WL-REQ-87: server-derived and read-only. A
	// client cannot compute this itself (the predicate reads other repos'
	// done_state and landed-commit facts), and it is ignored on any inbound
	// body.
	Closed bool `json:"closed"`
	// Tombstone carries the delete record (WL-REQ-117) and is nil on a live task.
	// Every list and pickup path already hides deleted tasks, so a non-nil
	// value only ever reaches a caller that asked for this task by id.
	Tombstone *Tombstone `json:"tombstone,omitempty"`
}

// Tombstone is the delete record a soft-deleted task or document carries
// (WL-REQ-117): who deleted it, when, and why. Justification is "" only for a
// delete made on a dev instance, which does not require one (WL-REQ-117).
type Tombstone struct {
	DeletedAt     time.Time `json:"deleted_at"`
	DeletedBy     string    `json:"deleted_by"`
	Justification string    `json:"justification,omitempty"`
}

// DeleteInput is the request body for the delete endpoints (WL-REQ-117). The
// justification is required on a prod instance and optional on a dev one; the
// server owns that rule, because it is the only party that knows which
// instance it is.
type DeleteInput struct {
	Justification string `json:"justification,omitempty"`
}

// SetTaskStateInput is the request body for POST /api/v1/tasks/{id}/state:
// the delivery state to move the task into.
type SetTaskStateInput struct {
	State string `json:"state"`
}

// SettableTaskStates are the states that endpoint accepts (WL-REQ-306) — the
// four an ingestion path normally supplies. Every other transition has its
// own endpoint, because it carries behaviour beyond the state write (claim
// takes a lease, reopen clears commit attribution, abandon is its own event).
// Which of these four a given task may actually reach stays the store's
// transition table's call; this list only bounds the endpoint.
var SettableTaskStates = []string{"merged", "deployed_dev", "deployed_prod", "released"}

// CreateTaskInput is the request body for CreateTask (POST /api/v1/tasks).
type CreateTaskInput struct {
	Project  string   `json:"project"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Priority string   `json:"priority"`
	Kind     string   `json:"kind"`
	Concern  string   `json:"concern,omitempty"`
	Draft    bool     `json:"draft"`
	Skills   []string `json:"skills,omitempty"`
	// Parent, when set, files the new task under this parent in the same
	// request instead of a separate edge call.
	Parent string `json:"parent,omitempty"`
	// FollowUpTo, when set, records the task this one was spun out of in the
	// same request instead of a separate edge call.
	FollowUpTo string `json:"follow_up_to,omitempty"`
	// GovernedBy names rules ("WL-RULE-12") that govern the task from
	// creation. Optional: a planless task may acquire its rule later (S4).
	GovernedBy []string `json:"governed_by,omitempty"`
	// Secrets declares the org-catalog secret names this task needs (spec
	// 017). Names only; validated against internal/secrets.ValidName.
	Secrets []string `json:"secrets,omitempty"`
	// Decisions poses questions on the new task in the same transaction as
	// the insert (WL-REQ-176). Legal on any kind, and a decision-kind task
	// with none is legal too — the list is often written after the task.
	Decisions []Decision `json:"decisions,omitempty"`
}

// EditTaskInput carries the optional fields of a task edit (PATCH
// /api/v1/tasks/{id}); nil means leave the field unchanged. Concern "" or
// "none" clears the concern. State requests one of the transitions
// patchStateFrom allows (internal/api/tasks.go) in the same request.
type EditTaskInput struct {
	Title              *string `json:"title"`
	Body               *string `json:"body"`
	Priority           *string `json:"priority"`
	Concern            *string `json:"concern"`
	NeedsDecomposition *bool   `json:"needs_decomposition"`
	// HumanOnly, when non-nil, sets or clears the no-unattended-pickup flag.
	HumanOnly *bool   `json:"human_only"`
	State     *string `json:"state"`
	// Secrets, when non-nil, replaces the task's declared secret names
	// wholesale (WL-SPEC-74).
	Secrets *[]string `json:"secrets"`
	// Kind, when non-nil, retags the task (WL-101): validated against the
	// same kind set creation uses, deprecated aliases normalised the same
	// way.
	Kind *string `json:"kind"`
	// Artifacts, when non-nil, declares each listed catalog address as
	// verified-by for this task (WL-REQ-118), which is what routes a
	// /hooks/catalog delivery to it. Declarations are additive and
	// idempotent — an entity may hold several addresses, and there is no
	// undeclare surface yet.
	Artifacts *[]string `json:"artifacts"`
	// Milestone, when non-nil, attaches or detaches the task from a milestone
	// (WL-REQ-118): "" or "none" detaches, any other value must name a
	// milestone in the task's own project.
	Milestone *string `json:"milestone,omitempty"`
	// Plan, when non-nil, links the task to the plan document it executed
	// (WL-SPEC-82 §15.6): an id or slug, resolved server-side the same way
	// `--plan` on `task list` resolves one. Refused if the task already
	// carries a different plan document, or the resolved document is not a
	// plan in the task's own project. There is no detach — once set, a
	// task's plan_doc does not change.
	Plan *string `json:"plan,omitempty"`
}

// EdgeInput is the request body for adding or removing a task edge
// (POST/DELETE /api/v1/tasks/{id}/edges). Exactly one of To or From must be
// set: To names the task {id} points to, From names the task pointing at
// {id} — the two directions the endpoint accepts.
type EdgeInput struct {
	To   *string `json:"to"`
	From *string `json:"from"`
	Type string  `json:"type"`
}

// SetSkillsInput is the request body for PUT /api/v1/tasks/{id}/skills:
// replaces the task's pinned skill names.
type SetSkillsInput struct {
	Skills []string `json:"skills"`
}

// TaskSkills is the response body of PUT /api/v1/tasks/{id}/skills: the
// stored list read back after cleaning, not the raw request echoed.
type TaskSkills struct {
	Skills []string `json:"skills"`
}

// Edge is the response body of POST /api/v1/tasks/{id}/edges: the edge as
// stored, with both endpoints resolved to task ids (EdgeInput names only one
// of them).
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

// DecomposeInput is the request body for POST /api/v1/tasks/{id}/decompose:
// one draft child is created per title.
type DecomposeInput struct {
	Into []string `json:"into"`
}

// TaskPublishInput is the body POST /projects/{id}/tasks/publish takes: the
// cockpit's Publish button, `lode task publish` (draft -> ready) from a page.
// It names one task, or a plan whose draft tasks are all published. The
// acting actor is the session's (WL-SPEC-82 §15.4 rule 6).
type TaskPublishInput struct {
	Task string `json:"task,omitempty"`
	Plan int64  `json:"plan,omitempty"`
}

// TaskPublishResponse is the reply to POST /projects/{id}/tasks/publish: the
// tasks moved to ready. The page applies none of it; it reloads.
type TaskPublishResponse struct {
	Published []string `json:"published"`
}

// TaskListParams is the query string of
// GET /api/v1/tasks?project=&state=&priority=&kind=&parent=&assignee=&has_children=&repo=&updated_since=&plan_doc=&about_doc=&deleted=&detail=&tree=&root=.
// State is repeatable and/or comma-separated; the handler splits each value
// on commas after decoding. Tree, Deleted, Detail and HasChildren are
// booleans: a bare flag is false; a value that is not a boolean is a 400
// naming the parameter.
type TaskListParams struct {
	Project  string `query:"project,omitempty"`
	Priority string `query:"priority,omitempty"`
	Kind     string `query:"kind,omitempty"`
	// States is repeatable (state=a&state=b) and/or comma-separated
	// (state=a,b); either form narrows to tasks in one of these states.
	States []string `query:"state,omitempty"`
	// Parent narrows to the direct children of this task id.
	Parent string `query:"parent,omitempty"`
	// Assignee narrows to tasks assigned to this actor id.
	Assignee string `query:"assignee,omitempty"`
	// HasChildren narrows to containers — tasks with at least one child.
	HasChildren bool `query:"has_children,omitempty"`
	// Repo narrows to the project owning this repo. Any git remote URL form
	// works as well as owner/name; the server normalizes it.
	Repo string `query:"repo,omitempty"`
	// UpdatedSince is an RFC3339 instant that narrows to the tasks touched at
	// or after it (the incremental fetch a polling mirror makes).
	UpdatedSince string `query:"updated_since,omitempty"`
	// PlanDoc narrows to the tasks minted from this plan document id (025
	// §9.2). nil (the parameter absent) does not filter; a non-positive
	// value is refused, so a caller cannot distinguish "0" from "absent" on
	// the wire.
	PlanDoc *int64 `query:"plan_doc,omitempty"`
	// AboutDoc narrows to the tasks that reference this document id — the
	// review and planning tasks the doc-lifecycle watcher mints (WL-RULE-179).
	// nil (the parameter absent) does not filter; a non-positive value is
	// refused, the same stance PlanDoc takes.
	AboutDoc *int64 `query:"about_doc,omitempty"`
	// Deleted switches the list from live tasks to tombstoned ones (WL-REQ-117)
	// instead of joining the two.
	Deleted bool `query:"deleted,omitempty"`
	// Detail adds "blocked" and "edges" to each row (see TaskListDetail) at
	// the cost of two extra bulk queries.
	Detail bool `query:"detail,omitempty"`
	// Tree answers with the hierarchy instead of a flat list (see
	// TaskTreeResponse); Project, States and Root still narrow it, every
	// other field is ignored.
	Tree bool `query:"tree,omitempty"`
	// Root, with Tree, names the single container to report instead of every
	// container in scope.
	Root string `query:"root,omitempty"`
}
