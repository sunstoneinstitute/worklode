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
