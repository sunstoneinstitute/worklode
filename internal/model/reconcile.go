package model

import "time"

// ReplayResult is one reconcile replay run's report (spec 013 §2.1). It is
// the "replay" section of the POST /api/v1/reconcile response body, so ADR
// 036 puts it here rather than in internal/hooks.
type ReplayResult struct {
	DryRun        bool     `json:"dry_run"`
	Candidates    int      `json:"candidates"`
	Replayed      int      `json:"replayed"`
	StillUnmapped int      `json:"still_unmapped"`
	Errors        []string `json:"errors,omitempty"`
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
	// predates the mapping — the signal to run lode reconcile.
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
// the CLI (`lode reconcile`) sends. Repo and Task are mutually exclusive
// bounds; Since accepts RFC 3339 or a Go duration, resolved against the
// server clock.
type ReconcileInput struct {
	Repo   string `json:"repo,omitempty"`
	Task   string `json:"task,omitempty"`
	Since  string `json:"since,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// ReconcileResponse is one reconcile run's report, one section per engine.
// Poll is null when polling did not run; PollSkipped says why.
type ReconcileResponse struct {
	RunID       string        `json:"run_id"`
	DryRun      bool          `json:"dry_run"`
	Replay      *ReplayResult `json:"replay"`
	Poll        any           `json:"poll"` // *PollResult once a later plan adds engine 2 to this endpoint
	PollSkipped string        `json:"poll_skipped,omitempty"`
}
