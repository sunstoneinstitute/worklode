package model

import "time"

// ClaimResponse is the response body of POST /api/v1/tasks/{id}/claim.
type ClaimResponse struct {
	Lease  Lease  `json:"lease"`
	Branch string `json:"branch"`
}

// ClaimHolder names the lease that made a claim conflict, when it was still
// there to be read: the claim path looks it up best-effort, so a conflict can
// legitimately answer without one.
type ClaimHolder struct {
	ActorID   string    `json:"actor_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ClaimConflictResponse is the 409 body of POST /api/v1/tasks/{id}/claim. It
// is an ErrorResponse plus the holder, so a client that only reads "error"
// sees the same shape it sees everywhere else.
type ClaimConflictResponse struct {
	Error  string       `json:"error"`
	Holder *ClaimHolder `json:"holder,omitempty"`
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

// ClaimInput is the request body for ClaimTask (POST
// /api/v1/tasks/{id}/claim). TTLSeconds <= 0 (the zero value) means the
// server default.
type ClaimInput struct {
	Worktree   string `json:"worktree"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// ClaimNextInput is the request body for ClaimNext (POST
// /api/v1/tasks/claim-next). Worktree is required unless DryRun is set;
// TTLSeconds <= 0 means the server default.
type ClaimNextInput struct {
	Project     string `json:"project"`
	StrictFocus bool   `json:"strict_focus"`
	DryRun      bool   `json:"dry_run"`
	Worktree    string `json:"worktree"`
	TTLSeconds  int    `json:"ttl_seconds"`
}

// RenewInput is the request body for RenewLease (POST
// /api/v1/tasks/{id}/renew). TTLSeconds <= 0 means the server default.
type RenewInput struct {
	TTLSeconds int `json:"ttl_seconds"`
}

// RebindWorktreeInput is the request body for RebindWorktree (POST
// /api/v1/tasks/{id}/lease/worktree): move the caller's active lease to a
// new worktree.
type RebindWorktreeInput struct {
	Worktree string `json:"worktree"`
}

// AssignInput is the optional request body of POST
// /api/v1/tasks/{id}/assign: an empty or missing assignee defaults to the
// caller.
type AssignInput struct {
	Assignee string `json:"assignee"`
}
