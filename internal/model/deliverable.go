package model

import "time"

// Deliverable is a declared, checkable output of a project (a datapackage, a
// report PDF, a CMS post). The deliverable still stores no state of its own —
// spec 029 §3.2 makes that a reported fact — so ReportedState and ReportedAt
// are not columns: they are the latest artifact_evidence row an emitter filed
// against Artifact, carried on the read projection only. Both are empty until
// something reports, and no write path accepts them.
type Deliverable struct {
	ID          string    `json:"id"`
	Project     string    `json:"project"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// Artifact is the address this deliverable declares it is verified by
	// (029 §3.1) — a catalog identifier such as
	// "bigquery://sunstone-prod/cow/casualties", not necessarily a browser
	// link. "" when the deliverable declares none.
	Artifact string `json:"artifact"`

	// Milestone is the milestone this deliverable is attached to (spec 029
	// §2), "" when it is attached to none. Always in the deliverable's own
	// project — the store refuses a cross-project attach.
	Milestone string `json:"milestone,omitempty"`

	// ReportedState is the state of the newest evidence for Artifact
	// (published | updated | deprecated | removed | failed), "" when nothing
	// has reported; ReportedAt is when that report says it happened.
	ReportedState string     `json:"reported_state"`
	ReportedAt    *time.Time `json:"reported_at"`
}

// DeliverableListResponse is the response body of GET
// /api/v1/projects/{id}/deliverables.
type DeliverableListResponse struct {
	Deliverables []Deliverable `json:"deliverables"`
}

// CreateDeliverableInput is the request body for declaring a deliverable
// (POST /api/v1/projects/{id}/deliverables).
type CreateDeliverableInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Artifact    string `json:"artifact"`
	// Milestone attaches the deliverable to a milestone in the same project
	// at declaration time (spec 029 §2), "" for none.
	Milestone string `json:"milestone,omitempty"`
}

// EditDeliverableInput is the request body for PATCH /api/v1/deliverables/{id}.
// The three descriptive fields (name, description, url) stay immutable in
// P1 — only the milestone attachment can be edited after declaration.
type EditDeliverableInput struct {
	// Milestone, when non-nil, attaches or detaches the deliverable from a
	// milestone (spec 029 §2): "" detaches, any other value must name a
	// milestone in the deliverable's own project.
	Milestone *string `json:"milestone"`
}
