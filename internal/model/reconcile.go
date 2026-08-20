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

// WhoAmI is the response of GET /api/v1/whoami (spec 013): the calling
// actor's identity, as internal/api resolves it from the request's Subject
// and internal/cli decodes it back — one declaration per ADR 036, not a
// same-shaped struct in each package.
type WhoAmI struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Admin bool   `json:"admin"`
}
