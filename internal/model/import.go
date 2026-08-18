package model

import "time"

// ImportCounts splits imported rows into ones that did not exist and ones
// that were refreshed. Truncated is this kind's own page-cap signal — issues
// and PRs page independently, so each has its own truncation state.
type ImportCounts struct {
	New       int  `json:"new"`
	Updated   int  `json:"updated"`
	Truncated bool `json:"truncated"`
}

// ImportResult is the wire form of POST /api/v1/inbox/import.
type ImportResult struct {
	Repo      string       `json:"repo"`
	Issues    ImportCounts `json:"issues"`
	PRs       ImportCounts `json:"prs"`
	Truncated bool         `json:"truncated"`
	DryRun    bool         `json:"dry_run"`
	// NewestUpdatedAt is the latest issue updated_at fetched this run, set
	// only when Issues.Truncated: it is the value that makes --since a resume
	// cursor (see listQuery in internal/githubauth/list.go) rather than just
	// a filter. It is issues-only because /pulls takes no since parameter, so
	// a PR timestamp here would be a cursor into a stream that cannot resume.
	NewestUpdatedAt *time.Time `json:"newest_updated_at,omitempty"`
}

// ImportInput is the request body for ImportInbox (POST
// /api/v1/inbox/import). An empty State means the server default, "open".
type ImportInput struct {
	Repo       string     `json:"repo"`
	State      string     `json:"state,omitempty"`
	IncludePRs bool       `json:"include_prs,omitempty"`
	Since      *time.Time `json:"since,omitempty"`
	DryRun     bool       `json:"dry_run,omitempty"`
}
