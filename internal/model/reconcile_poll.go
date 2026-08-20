package model

// PollResult is one reconcile poll run's report (spec 013 engine 2). Like
// ReplayResult it is a section of the POST /api/v1/reconcile response body,
// so ADR 036 puts it here rather than in internal/reconcile.
type PollResult struct {
	RunID      string       `json:"run_id"`
	DryRun     bool         `json:"dry_run"`
	Candidates int          `json:"candidates"`
	Repaired   []TaskRepair `json:"repaired"`
	Errors     []string     `json:"errors,omitempty"`
}

// TaskRepair is what the run did (or would do) for one task.
type TaskRepair struct {
	TaskID        string   `json:"task_id"`
	Repo          string   `json:"repo"`
	State         string   `json:"state"` // state before the run
	PRsUpdated    []int64  `json:"prs_updated,omitempty"`
	CommitsLanded []string `json:"commits_landed,omitempty"`
}
