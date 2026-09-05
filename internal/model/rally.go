package model

// Rally is the wire form of GET /api/v1/projects/{id}/rally: a project's
// active rally task plus the transitive tree of open tasks it is waiting on.
// A rally carries no work of its own — Blockers is its whole content, the
// tasks a human named as the thing to finish now (WL-667).
type Rally struct {
	Task     Task        `json:"task"`
	Blockers BlockerTree `json:"blockers"`
}
