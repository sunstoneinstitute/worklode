package model

// ApprovalFlow is one named, versioned review flow (029 §7.2). Flows live in
// instance configuration; a project stores the effective snapshot, so a later
// configuration edit cannot silently change an open review.
type ApprovalFlow struct {
	Name         string                `json:"name"`
	Rev          string                `json:"rev"`
	Match        map[string]string     `json:"match,omitempty"` // project label selector
	Requirements []ApprovalRequirement `json:"requirements"`
}

// ApprovalRequirement is one review lane a flow demands.
type ApprovalRequirement struct {
	Lane       string `json:"lane"`             // unique within the flow
	EntityKind string `json:"entity_kind"`      // document | deliverable | task
	Target     string `json:"target,omitempty"` // exact entity name, case-insensitive; empty = every entity of the kind
	Role       string `json:"role"`             // Keycloak group that may decide
}

// ApprovalFlowSnapshot is what projects.approval_flow stores: the flow the
// project was stamped with plus its reviewer template (lane -> actor id).
type ApprovalFlowSnapshot struct {
	Flow      ApprovalFlow      `json:"flow"`
	Reviewers map[string]string `json:"reviewers,omitempty"`
}
