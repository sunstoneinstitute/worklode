package model

import "time"

// TaskActivity is one row of a task's activity log: a decoded OTLP log
// record attributed to a task (spec 071 §2). It is distinct from the events
// table's append-only provenance — activity is high-volume, per-tool-call
// telemetry with a retention window, not a durable audit trail. Attrs holds
// only the allowlisted scalar keys internal/otlp keeps; the record body and
// every other attribute are dropped before a row reaches here.
type TaskActivity struct {
	ID      int64          `json:"id"`
	Task    string         `json:"task"`
	Actor   string         `json:"actor"`
	Agent   string         `json:"agent"`
	Session string         `json:"session"`
	At      time.Time      `json:"at"`
	Event   string         `json:"event"`
	Attrs   map[string]any `json:"attrs"`
}
