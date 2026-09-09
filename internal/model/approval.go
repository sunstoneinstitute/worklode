package model

import (
	"strconv"
	"strings"
	"time"
)

// ApprovalEntityKinds are the entity_kind values an approvals row takes (029
// §7.1). Declared here because three layers need the same list: the API
// validates an ad-hoc requirement against it, the CLI spells it in help and
// completion, and the store reads it back. A document is 'doc' — the table's
// own spelling, and what DocEntityID's ids agree with.
var ApprovalEntityKinds = []string{"doc", "deliverable", "task", "pr"}

// DocEntityID renders the approvals entity_id for a document: "doc:" plus the
// docs id. Writer and reader share this one spelling, and it stays parseable
// back to the id, which is what lets the queue query correlate a row to its
// document in SQL.
func DocEntityID(docID int64) string {
	return "doc:" + strconv.FormatInt(docID, 10)
}

// DocIDFromEntityID is DocEntityID's inverse: the document id an approvals
// entity_id names, and false for an id under any other entity kind.
func DocIDFromEntityID(entityID string) (int64, bool) {
	rest, ok := strings.CutPrefix(entityID, "doc:")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// Approval is one row of the approvals table (spec 029 §7.1): the human
// decision one entity revision is waiting on, or the settled record of one.
// Lane names the flow requirement the row answers (029 §7.2): one revision
// carries several independent lanes, and the row is unique on lane, so ""
// is the no-lane row a PR ingest or an ad-hoc request writes. CreatedBy is
// who put the requirement here, nil for rows that predate the column.
// ReviewKind separates an ordinary review row ("review", the zero value)
// from a dependent-object impact review ("impact", 029 §7.1) minted when an
// upstream decision the dependent relied on gets reopened; its DB column
// lands with the exact "review"/"impact" spelling. internal/store aliases
// this type rather than declaring its own, so the queue reader scans into
// the shape internal/api serializes (ADR 036 §2).
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
	ReviewKind      string     `json:"review_kind,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	CreatedBy       *string    `json:"created_by,omitempty"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
}

// AwaitingApproval is one row of the awaiting queue: the approval plus what a
// person needs to act on it. The entity fields are kind-neutral — Title is
// the PR title, document title, deliverable name or task title, whichever the
// row governs — so the queue does not grow a parallel set of columns per
// kind. URL is the one address that opens the row's entity: GitHub for a PR,
// its declared address for a deliverable that has one, a cockpit page
// otherwise. Only a 'pr' row names a Task — a document, deliverable or task
// hangs off its project directly, so Task is "" there. Author is the PR's
// author login where there is one, otherwise the actor that filed the
// requirement.
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

// RequireApprovalInput is the body of POST /api/v1/approvals: 029 §7.2's
// ad-hoc requirement, filed by hand on a governed target rather than minted
// by a project's review flow. EntityKind is the approvals table's own
// spelling — 'doc', 'deliverable', 'task' or 'pr' — and EntityID is the id
// spelled the way that kind's writer spells it ("doc:<id>" for a document,
// "repo#number" for a PR).
//
// Role and Actor are optional and mutually exclusive: a lane demands a group
// or a person, never both. Lane defaults to "", the no-lane row. Revision
// defaults to the document's current version for a 'doc' target and "" for
// every other kind.
type RequireApprovalInput struct {
	EntityKind string `json:"entity_kind"`
	EntityID   string `json:"entity_id"`
	Revision   string `json:"revision,omitempty"`
	Lane       string `json:"lane,omitempty"`
	Role       string `json:"role,omitempty"`
	Actor      string `json:"actor,omitempty"`
}

// SetDocReviewersInput is the body of POST /api/v1/docs/{id}/reviewers:
// replaces the document's durable reviewer set wholesale (025 §7.3, WL-359).
// There is no add/remove verb — "who reviews stays a social choice",
// decided once per change the way a PR's reviewer list is, not accumulated a
// name at a time.
type SetDocReviewersInput struct {
	Reviewers []string `json:"reviewers"`
}
