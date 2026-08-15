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
}
