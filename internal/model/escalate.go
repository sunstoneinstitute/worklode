package model

// EscalateTaskInput is the request body for POST /api/v1/tasks/{id}/escalate
// (025 §8.1): the executor reports that the design it is working from does not
// cover the case in front of it.
//
// To is "plan" or "spec" and says which tier owes the fix. Doc is a document
// reference the server resolves; leave it empty and the server picks the
// target from the task itself — the task's plan for "plan", the single spec
// that plan covers for "spec". Anchor narrows the escalation to one section
// ("sec-3"); "" escalates the whole document.
type EscalateTaskInput struct {
	To     string `json:"to"`
	Doc    string `json:"doc,omitempty"`
	Anchor string `json:"anchor,omitempty"`
	Reason string `json:"reason"`
}

// EscalateTaskResult is the response body of that endpoint. Exactly one field
// is set: Minted carries the design task the escalation created, Joined names
// the open design task it joined instead because one already covers the same
// document and section.
type EscalateTaskResult struct {
	Minted *Task  `json:"minted,omitempty"`
	Joined string `json:"joined,omitempty"`
}
