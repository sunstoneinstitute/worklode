package model

import "time"

// Instruction is a steering instruction queued against a task: an
// operator-authored message delivered to whichever actor next claims that
// task's lease (migration 0055).
type Instruction struct {
	ID        int64     `json:"id"`
	Task      string    `json:"task"`
	Body      string    `json:"body"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// InstructionInput is the request body for POST
// /api/v1/tasks/{id}/instructions: queue a steering instruction against the
// task.
type InstructionInput struct {
	Body string `json:"body"`
}

// InstructionsResponse is the pending-instructions envelope on a claim
// response: never null, an empty slice when there are none.
type InstructionsResponse struct {
	Instructions []Instruction `json:"instructions"`
}
