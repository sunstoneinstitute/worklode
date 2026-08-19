package model

import "time"

// Task is a unit of work. Concern is "" when the task has none; Assignee is
// "" when the task is unassigned; Skills is never nil (the store guarantees
// an empty slice, so the JSON reads [] rather than null).
type Task struct {
	ID                 string    `json:"id"`
	Project            string    `json:"project"`
	Title              string    `json:"title"`
	Body               string    `json:"body"`
	Priority           string    `json:"priority"`
	Kind               string    `json:"kind"`
	State              string    `json:"state"`
	Concern            string    `json:"concern"`
	NeedsDecomposition bool      `json:"needs_decomposition"`
	CreatedBy          string    `json:"created_by"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	Skills             []string  `json:"skills"`
	Assignee           string    `json:"assignee"`
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
}

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
	State              *string `json:"state"`
	// Secrets, when non-nil, replaces the task's declared secret names
	// wholesale (spec 017).
	Secrets *[]string `json:"secrets"`
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
