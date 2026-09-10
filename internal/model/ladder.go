package model

// GapTaskInput is the request body for POST /api/v1/tasks/{id}/gap (025
// §15.5): the executor found that the plan or spec does not cover the case
// in front of it, but kept going instead of escalating. Doc is a document
// reference the server resolves; Anchor narrows it to one section
// ("sec-3"), "" for the whole document.
type GapTaskInput struct {
	Doc    string `json:"doc"`
	Anchor string `json:"anchor,omitempty"`
	Reason string `json:"reason"`
}

// FixTaskInput is the request body for POST /api/v1/tasks/{id}/fix (025
// §15.5): the fixer's report of one phase of closing a gap or escalation.
// Phase is "started" or "finished". A "started" call names Tier ("plan" or
// "spec") and Doc, the fix's target; a "finished" call names Outcome
// ("resolved", "substantive", or "escalated"). Attempt is a client-supplied
// ordinal pairing a "finished" call with the "started" call it closes
// (default 1 when omitted) — a second fix pass on the same task needs a
// fresh value, or its "started" replays the first's rather than logging a
// new one.
type FixTaskInput struct {
	Phase   string `json:"phase"`
	Tier    string `json:"tier,omitempty"`
	Doc     string `json:"doc,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
}

// LadderEventResult is the response body of the gap and fix endpoints:
// Recorded is true when the call logged a new event, false when it matched
// an external id already on the log and changed nothing — the replay case
// those ids exist to make safe.
type LadderEventResult struct {
	Recorded bool `json:"recorded"`
}
