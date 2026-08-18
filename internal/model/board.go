package model

import "time"

// Holder is the actor currently holding a lease on a board task.
type Holder struct {
	ActorID   string    `json:"actor_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// BoardTask is a Task as it appears on the board, with its lease holder when
// in progress. Parent is the task's parent when it has one, so a board can
// group a parent's children under it without a lookup per task.
type BoardTask struct {
	Task
	Parent string  `json:"parent,omitempty"`
	Holder *Holder `json:"holder,omitempty"`
}

// BoardProject is one project's four state buckets on the board.
type BoardProject struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	InProgress []BoardTask `json:"in_progress"`
	InReview   []BoardTask `json:"in_review"`
	Ready      []BoardTask `json:"ready"`
	Blocked    []BoardTask `json:"blocked"`
}

// BoardResponse is the wire form of GET /api/v1/board, and the data the web
// board pages (GET / and GET /projects/{id}) render. RecentFailures is nil
// when a project filter narrows the response to one project (it is not
// project-scoped), non-nil (possibly empty) otherwise — that is how a caller
// tells "board scoped to one project" from "board with no recent failures"
// apart.
type BoardResponse struct {
	Projects       []BoardProject `json:"projects"`
	RecentFailures []RuntimeEvent `json:"recent_failures"`
}
