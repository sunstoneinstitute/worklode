package model

import "time"

// RepoBranchRules is the response body of GET /api/v1/repos/branch-rules:
// the rules last observed on a mapped repo's default branch, flattened
// across every GitHub ruleset that applies (WL-SPEC-66 §6.3, WL-SPEC-72 §3).
type RepoBranchRules struct {
	Repo       string `json:"repo"`
	Branch     string `json:"branch"`
	MergeQueue bool   `json:"merge_queue"`
	// PullRequest is whether a ruleset requires pull requests. Absent when
	// the row predates the observation and has not been refreshed since.
	PullRequest *bool     `json:"pull_request,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
}

// RepoBranchRulesParams is the query string of GET /api/v1/repos/branch-rules.
type RepoBranchRulesParams struct {
	// Remote is the git remote URL of the repo, normalized server-side.
	Remote string `query:"remote,omitempty"`
}
