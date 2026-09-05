package model

import "time"

// Approval is one row of the approvals table (spec 029 §7.1): the human
// decision one entity revision is waiting on, or the settled record of one.
// Lane names the flow requirement the row answers (029 §7.2): one revision
// carries several independent lanes, and the row is unique on lane, so ""
// is the no-lane row a PR ingest or an ad-hoc request writes. CreatedBy is
// who put the requirement here, nil for rows that predate the column.
// internal/store aliases this type rather than declaring its own, so the
// queue reader scans into the shape internal/api serializes (ADR 036 §2).
type Approval struct {
	ID              int64      `json:"id"`
	EntityKind      string     `json:"entity_kind"`
	EntityID        string     `json:"entity_id"`
	SubjectRevision string     `json:"subject_revision"`
	Lane            string     `json:"lane"`
	RequiredRole    *string    `json:"required_role,omitempty"`
	RequiredActor   *string    `json:"required_actor,omitempty"`
	ResolvingActor  *string    `json:"resolving_actor,omitempty"`
	State           string     `json:"state"`
	CreatedAt       time.Time  `json:"created_at"`
	CreatedBy       *string    `json:"created_by,omitempty"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
}

// AwaitingApproval is one row of the awaiting queue: the approval plus what a
// person needs to act on it. The entity fields are kind-neutral — Title/URL/
// Author are the PR's for a 'pr' row and the document's for a 'doc' row — so
// the queue does not grow a parallel set of columns per kind. Every one of
// them, Task included, is "" when the row's kind does not carry it: a
// document hangs off its project directly, with no task in between.
type AwaitingApproval struct {
	Approval
	Title             string  `json:"title"`
	URL               string  `json:"url"`
	Author            string  `json:"author,omitempty"`
	Task              string  `json:"task,omitempty"`
	Project           string  `json:"project,omitempty"`
	ProjectName       string  `json:"project_name,omitempty"`
	RequiredActorName *string `json:"required_actor_name,omitempty"`
}

// ApprovalListResponse is the response body of GET /api/v1/approvals.
type ApprovalListResponse struct {
	Approvals []AwaitingApproval `json:"approvals"`
}

// SetDocReviewersInput is the body of POST /api/v1/docs/{id}/reviewers:
// replaces the document's durable reviewer set wholesale (025 §7.3, WL-359).
// There is no add/remove verb — "who reviews stays a social choice",
// decided once per change the way a PR's reviewer list is, not accumulated a
// name at a time.
type SetDocReviewersInput struct {
	Reviewers []string `json:"reviewers"`
}
