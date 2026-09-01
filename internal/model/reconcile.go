package model

import "time"

// ReplayResult is one reconcile replay run's report (spec 013 §2.1). It is
// the "replay" section of the POST /api/v1/reconcile response body, so ADR
// 036 puts it here rather than in internal/hooks.
// Truncated and ErrorsOmitted report the two caps the run works under: the
// candidate batch is bounded so a large backlog is not read into memory at
// once, and the error list is bounded so a bad backlog cannot produce an
// unboundedly large response body. Both are re-run signals, not failures —
// replay is re-runnable, and applied events leave the candidate set.
type ReplayResult struct {
	DryRun        bool     `json:"dry_run"`
	Candidates    int      `json:"candidates"`
	Replayed      int      `json:"replayed"`
	StillUnmapped int      `json:"still_unmapped"`
	Truncated     bool     `json:"truncated,omitempty"`
	Errors        []string `json:"errors,omitempty"`
	ErrorsOmitted int      `json:"errors_omitted,omitempty"`
}

// WhoAmI is the response of GET /api/v1/whoami (spec 013): the calling
// actor's identity, as internal/api resolves it from the request's Subject
// and internal/cli decodes it back — one declaration per ADR 036, not a
// same-shaped struct in each package.
type WhoAmI struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Admin bool   `json:"admin"`
}

// RepoDoctor is one mapped repo's ingestion health, from GET
// /api/v1/repos/doctor (spec 013 §lode project doctor). AppInstalled is nil
// when the check could not run — no GitHub App configured, GitHub unreachable
// or erroring, or the report's time budget spent before this repo's turn —
// which is different from "not installed". AppError says which, and is set
// alongside a false AppInstalled too (GitHub's own "not installed" answer).
type RepoDoctor struct {
	Repo            string     `json:"repo"`
	Project         string     `json:"project"`
	AppInstalled    *bool      `json:"app_installed"`
	AppError        string     `json:"app_error,omitempty"`
	MappedAt        time.Time  `json:"mapped_at"`
	LastEventAt     *time.Time `json:"last_event_at"`
	EventTypes      []string   `json:"event_types"`
	UnappliedEvents int        `json:"unapplied_events"`
	// Stale: this repo has never delivered a webhook, or its last delivery
	// predates the mapping — the signal to run lode task reconcile.
	Stale bool `json:"stale"`
}

// UnmappedSender is a repo that has sent webhooks but maps to no project, on
// the wire (GET /api/v1/repos/doctor's unmapped_senders section).
type UnmappedSender struct {
	Repo        string    `json:"repo"`
	Events      int       `json:"events"`
	LastEventAt time.Time `json:"last_event_at"`
}

// ReposDoctorResponse is the response body of GET /api/v1/repos/doctor.
type ReposDoctorResponse struct {
	Repos           []RepoDoctor     `json:"repos"`
	UnmappedSenders []UnmappedSender `json:"unmapped_senders"`
}

// ReconcileInput is the request body of POST /api/v1/reconcile, and what
// the CLI (`lode task reconcile`) sends. Repo and Task are mutually exclusive
// bounds; Since accepts RFC 3339 or a Go duration, resolved against the
// server clock.
type ReconcileInput struct {
	Repo   string `json:"repo,omitempty"`
	Task   string `json:"task,omitempty"`
	Since  string `json:"since,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// ReconcileResponse is one reconcile run's report, one section per engine.
// Replay is null when --task bounded the run (replay cannot be task-scoped).
// Poll is null when polling did not run: PollSkipped says the App was not
// configured, PollError says the poll ran and failed. Either way the replay
// section stands on its own — engine 1 has already written by then.
type ReconcileResponse struct {
	RunID       string        `json:"run_id"`
	DryRun      bool          `json:"dry_run"`
	Replay      *ReplayResult `json:"replay"`
	Poll        *PollResult   `json:"poll"`
	PollSkipped string        `json:"poll_skipped,omitempty"`
	PollError   string        `json:"poll_error,omitempty"`
}

// PollResult is one reconcile poll run's report (spec 013 engine 2). Like
// ReplayResult it is a section of the POST /api/v1/reconcile response body,
// so ADR 036 puts it here rather than in internal/reconcile. Repaired is what
// the run observed, not what it changed — see `lode task reconcile --help`.
type PollResult struct {
	RunID      string       `json:"run_id"`
	DryRun     bool         `json:"dry_run"`
	Candidates int          `json:"candidates"`
	Repaired   []TaskRepair `json:"repaired,omitempty"`
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
