package model

import "time"

// ProjectionFailure is one project quarantined by the knowledge-graph
// projector (spec 006 §11): graph-server would not take its graph, and the
// global watermark has moved on past the transaction that made it dirty, so
// this row is the only remaining record that the project still owes a
// projection.
//
// It crosses the HTTP boundary because
// worklode_graph_projection_quarantined_projects can only say how many
// projects are stuck — 022 §8 keeps the project set out of a label, since it
// is not closed — and the log line naming the one that just failed has
// scrolled away by the time anyone asks.
type ProjectionFailure struct {
	Project  string `json:"project"`
	Attempts int    `json:"attempts"` // consecutive failed attempts, including the latest
	// NextAttemptAt is a floor, not a schedule: fresh activity in the project
	// makes it dirty and re-attempts it immediately, whatever this says.
	FirstFailedAt time.Time `json:"first_failed_at"`
	LastFailedAt  time.Time `json:"last_failed_at"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
	LastError     string    `json:"last_error"`
}

// ProjectionFailureListResponse is the response body of GET
// /api/v1/graph/projection/failures.
type ProjectionFailureListResponse struct {
	Failures []ProjectionFailure `json:"failures"`
}
