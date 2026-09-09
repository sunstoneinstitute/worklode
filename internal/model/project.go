package model

// RepoMapping is a repo mapped to a project, with the terminal delivery state
// that counts as fully delivered for it (merged, deployed_prod, or released).
type RepoMapping struct {
	Repo      string `json:"repo"`
	DoneState string `json:"done_state"`
}

// Project is the wire form of a project, including its mapped repos and
// ranking focus (the ordered list of concerns claim-next should prioritize).
type Project struct {
	ID    string        `json:"id"`
	Name  string        `json:"name"`
	Key   string        `json:"key"`
	Repos []RepoMapping `json:"repos"`
	Focus []string      `json:"focus"`

	// The approval flow governing the project (029 §7.2), so a reader can
	// name it without unmarshalling the stored snapshot. Absent until a
	// flow is applied.
	ApprovalFlowName string `json:"approval_flow_name,omitempty"`
	ApprovalFlowRev  string `json:"approval_flow_rev,omitempty"`

	// Labels are free-form classification stamped at promotion (029 §1),
	// e.g. kind=sunstone-story. Horizon is "bounded" or "standing"
	// (migration 0074).
	Labels  map[string]string `json:"labels"`
	Horizon string            `json:"horizon"`
}

// ProjectListResponse is the response body of GET /api/v1/projects.
type ProjectListResponse struct {
	Projects []Project `json:"projects"`
}

// CreateProjectInput is the request body for CreateProject (POST
// /api/v1/projects).
type CreateProjectInput struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`

	// Labels and Horizon are optional (029 §1); the schema default applies
	// when omitted (labels {}, horizon "standing"). Horizon lets the
	// standing intake project be created with horizon: "standing" over
	// the API.
	Labels  map[string]string `json:"labels,omitempty"`
	Horizon string            `json:"horizon,omitempty"`
}

// PatchProjectInput is the settable-field set of PATCH
// /api/v1/projects/{id}. Every field is a pointer so an absent field (nil)
// is distinguished from one present-but-empty (a clear): sending
// focus_note:"" clears the pinned-focus card, decision_title:"" clears the
// next-decision card. FocusPinnedBy, DecisionAccountable, and
// DecisionReadiness are the companion fields of their trigger (FocusNote /
// DecisionTitle) and are ignored without it.
type PatchProjectInput struct {
	Focus               *[]string `json:"focus"`
	FocusNote           *string   `json:"focus_note"`
	FocusPinnedBy       *string   `json:"focus_pinned_by"`
	DecisionTitle       *string   `json:"decision_title"`
	DecisionAccountable *string   `json:"decision_accountable"`
	DecisionReadiness   *string   `json:"decision_readiness"`
}

// AddRepoInput is the request body for AddRepo (POST
// /api/v1/projects/{id}/repos). DoneState is optional; empty leaves the
// mapping at the schema default.
type AddRepoInput struct {
	Repo      string `json:"repo"`
	DoneState string `json:"done_state"`
}

// SetRepoDoneStateInput is the request body of PATCH
// /api/v1/repos/{owner}/{name}. done_state is the only settable field on an
// already-mapped repo, and it is required.
type SetRepoDoneStateInput struct {
	DoneState string `json:"done_state"`
}

// AddRepoResult is the response from AddRepo. Warnings are non-fatal setup
// problems — the mapping was created regardless.
type AddRepoResult struct {
	ProjectID string   `json:"project_id"`
	Repo      string   `json:"repo"`
	DoneState string   `json:"done_state"`
	Warnings  []string `json:"warnings,omitempty"`
}
