package model

import "time"

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
	// local refs to tasks reads it rather than rendering one (008 §3.1).
	Branch string `json:"branch"`
	// Secrets is the task's declared org-catalog secret names (spec 017).
	// Names only; nil and empty are equivalent, always [] on the wire.
	Secrets []string `json:"secrets"`
	// PlanDoc is the plan document this task was minted from (025 §9.2); 0
	// (omitted on the wire) when no plan authored it.
	PlanDoc int64 `json:"plan_doc,omitempty"`
	// AboutDoc is the document this task is about (025 §15.4): set on review
	// tasks minted at submission and design tasks minted at acceptance. 0
	// (omitted on the wire) when the task carries no such reference. Distinct
	// from PlanDoc, which names the plan whose acceptance minted the task
	// (025 §9.2), not what the task is about.
	AboutDoc int64 `json:"about_doc,omitempty"`
	// Closed reports whether the task has no work left for anyone to own, by
	// the per-repo predicate of 004 §1.3: server-derived and read-only. A
	// client cannot compute this itself (the predicate reads other repos'
	// done_state and landed-commit facts), and it is ignored on any inbound
	// body.
	Closed bool `json:"closed"`
	// Tombstone carries the delete record (044 §2) and is nil on a live task.
	// Every list and pickup path already hides deleted tasks, so a non-nil
	// value only ever reaches a caller that asked for this task by id.
	Tombstone *Tombstone `json:"tombstone,omitempty"`
}

// Tombstone is the delete record a soft-deleted task or document carries
// (044 §2): who deleted it, when, and why. Justification is "" only for a
// delete made on a dev instance, which does not require one (044 §3).
type Tombstone struct {
	DeletedAt     time.Time `json:"deleted_at"`
	DeletedBy     string    `json:"deleted_by"`
	Justification string    `json:"justification,omitempty"`
}

// DeleteInput is the request body for the delete endpoints (044 §5). The
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

// SettableTaskStates are the states that endpoint accepts (061 §2.1) — the
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
	// Secrets declares the org-catalog secret names this task needs (spec
	// 017). Names only; validated against internal/secrets.ValidName.
	Secrets []string `json:"secrets,omitempty"`
	// Decisions poses questions on the new task in the same transaction as
	// the insert (025 §10.1). Legal on any kind, and a decision-kind task
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
	// wholesale (spec 017).
	Secrets *[]string `json:"secrets"`
	// Kind, when non-nil, retags the task (WL-101): validated against the
	// same kind set creation uses, deprecated aliases normalised the same
	// way.
	Kind *string `json:"kind"`
	// Artifacts, when non-nil, declares each listed catalog address as
	// verified-by for this task (spec 029 §3.1), which is what routes a
	// /hooks/catalog delivery to it. Declarations are additive and
	// idempotent — an entity may hold several addresses, and there is no
	// undeclare surface yet.
	Artifacts *[]string `json:"artifacts"`
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
