package model

// Issue is the wire form of an inbox issue. TaskID is "" until the issue is
// promoted or linked to a task.
type Issue struct {
	Repo              string   `json:"repo"`
	Number            int64    `json:"number"`
	Title             string   `json:"title"`
	State             string   `json:"state"`
	TriageState       string   `json:"triage_state"`
	TaskID            string   `json:"task_id,omitempty"`
	AppliesToVersions []string `json:"applies_to_versions,omitempty"`
	URL               string   `json:"url"`
}

// PromoteInput is the request body for PromoteIssue (POST
// /api/v1/inbox/promote). Title is optional — the server defaults it to the
// issue's own title.
type PromoteInput struct {
	Repo              string   `json:"repo"`
	Number            int64    `json:"number"`
	Title             string   `json:"title,omitempty"`
	Body              string   `json:"body,omitempty"`
	Priority          string   `json:"priority"`
	Kind              string   `json:"kind"`
	AppliesToVersions []string `json:"applies_to_versions,omitempty"`
	Draft             bool     `json:"draft,omitempty"`
	Parent            string   `json:"parent,omitempty"`
}

// DismissInput is the request body for DismissIssue (POST
// /api/v1/inbox/dismiss).
type DismissInput struct {
	Repo   string `json:"repo"`
	Number int64  `json:"number"`
}

// LinkInput is the request body for LinkIssue (POST /api/v1/inbox/link):
// mark an inbox issue as covered by a task that already exists.
type LinkInput struct {
	Repo   string `json:"repo"`
	Number int64  `json:"number"`
	TaskID string `json:"task_id"`
}
