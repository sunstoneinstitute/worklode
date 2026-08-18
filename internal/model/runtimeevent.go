package model

import "time"

// RuntimeEvent is a recent runtime event (crash loop, OOM kill, ...) as
// reported by the pod watcher and shown on the board.
type RuntimeEvent struct {
	ID         int64     `json:"id"`
	Cluster    string    `json:"cluster"`
	Kind       string    `json:"kind"`
	Workload   string    `json:"workload"`
	Image      string    `json:"image"`
	Message    string    `json:"message"`
	OccurredAt time.Time `json:"occurred_at"`
}

// RuntimeEventAck is the response body of POST /api/v1/runtime-events.
// Status is "ok" for a stored event and "duplicate" when the dedupe key had
// already been seen — the duplicate answer carries no id, since no row was
// written for this call.
type RuntimeEventAck struct {
	ID     int64  `json:"id,omitempty"`
	Status string `json:"status"`
}

// RuntimeEventInput is the request body for creating a runtime event (POST
// /api/v1/runtime-events), posted by the pod watcher.
type RuntimeEventInput struct {
	Cluster    string `json:"cluster"`
	Kind       string `json:"kind"`
	Workload   string `json:"workload"`
	Image      string `json:"image"`
	Message    string `json:"message"`
	OccurredAt string `json:"occurred_at"`
	DedupeKey  string `json:"dedupe_key"`
}
