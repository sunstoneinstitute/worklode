package model

import "time"

// ClaimResponse is the response body of POST /api/v1/tasks/{id}/claim.
type ClaimResponse struct {
	Lease  Lease  `json:"lease"`
	Branch string `json:"branch"`
}

// TaskListResponse is the response body of GET /api/v1/tasks.
type TaskListResponse struct {
	Tasks []Task `json:"tasks"`
}

// ClaimNextPickLease is the lease shard of a ClaimNextPick, present only when
// the pick was actually claimed (not a dry run or a no-ready-task response).
type ClaimNextPickLease struct {
	Worktree  string    `json:"worktree"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ClaimNextPick is the wire form of a claim-next candidate/claimed task: a
// slimmer projection than Task, matching the ranking-relevant fields (spec
// 005) rather than the full task record.
type ClaimNextPick struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	// Branch is the server-authoritative task branch (<prefix><id>-<slug>).
	Branch   string              `json:"branch"`
	Concern  string              `json:"concern"`
	Priority string              `json:"priority"`
	FanOut   int                 `json:"fan_out"`
	Project  string              `json:"project"`
	Lease    *ClaimNextPickLease `json:"lease,omitempty"`
}

// ClaimNextResponse is the response body of POST /api/v1/tasks/claim-next.
// Task is nil only when no ready task exists (Claimed is false and Reason is
// "no-ready-task"). A dry-run hit sets DryRun and Task but leaves Claimed
// false and Task.Lease nil.
type ClaimNextResponse struct {
	Claimed bool           `json:"claimed"`
	Reason  string         `json:"reason,omitempty"`
	DryRun  bool           `json:"dry_run,omitempty"`
	Task    *ClaimNextPick `json:"task,omitempty"`
}
