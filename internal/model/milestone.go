package model

import "time"

// Milestone is one ordered container in a project (spec 029 §2). It stores
// identity, title, and ordering only; Progress is derived on read from its
// tasks and its deliverables' reported state, never stored.
type Milestone struct {
	ID        string            `json:"id"` // <KEY>-MILE-<n>
	Project   string            `json:"project"`
	Title     string            `json:"title"`
	Position  int               `json:"position"`
	CreatedBy string            `json:"created_by"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Progress  MilestoneProgress `json:"progress"`
}

// MilestoneProgress is the derived query 029 §2 makes of a milestone's
// children. Closed and live follow the pinned buckets in the plan's global
// constraints; ComputeMilestoneProgress is the only producer.
type MilestoneProgress struct {
	TasksTotal        int `json:"tasks_total"`
	TasksClosed       int `json:"tasks_closed"`
	DeliverablesTotal int `json:"deliverables_total"`
	DeliverablesLive  int `json:"deliverables_live"`
}

// MilestoneListResponse is GET /api/v1/projects/{id}/milestones.
type MilestoneListResponse struct {
	Milestones []Milestone `json:"milestones"`
}

// MilestoneDetail is GET /api/v1/milestones/{id}: the milestone plus the
// children the progress was derived from.
type MilestoneDetail struct {
	Milestone
	Tasks        []Task        `json:"tasks"`
	Deliverables []Deliverable `json:"deliverables"`
}

// CreateMilestoneInput is POST /api/v1/projects/{id}/milestones. Position 0
// means append after the project's last milestone.
type CreateMilestoneInput struct {
	Title    string `json:"title"`
	Position int    `json:"position,omitempty"`
}
