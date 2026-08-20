package model

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
