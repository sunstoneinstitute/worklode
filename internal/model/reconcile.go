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
// when the server has no GitHub App configured — the check cannot run,
// which is different from "not installed".
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
