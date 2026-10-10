package model

import (
	"encoding/json"
	"time"
)

// Event is the wire form of one event log row (WL-RULE-179, WL-REQ-182).
type Event struct {
	ID         int64           `json:"id"`
	Source     string          `json:"source"`
	ExternalID string          `json:"external_id"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
	ReceivedAt time.Time       `json:"received_at"`
}

// EventListResponse is the response body of GET /api/v1/events.
type EventListResponse struct {
	Events []Event `json:"events"`
}

// EventListParams is the query string of GET /api/v1/events. Zero-valued
// string/After fields do not filter. Limit is a pointer so an absent limit
// (server default/cap applies) can be told apart from an explicit zero,
// which the handler rejects.
type EventListParams struct {
	// Type narrows to one event type; empty matches every type.
	Type string `query:"type,omitempty"`
	// Since narrows to events received at or after this RFC3339 timestamp.
	Since string `query:"since,omitempty"`
	// After is an exclusive id cursor.
	After int64 `query:"after,omitempty"`
	// Limit caps the page returned; nil means the server default/cap. A
	// present value must be positive.
	Limit *int `query:"limit,omitempty"`
}

// EventStreamParams is the query string of GET /api/v1/events/stream. The
// client's omitempty sends no after for a zero field, so a bare follow
// starts at the head and shows only what happens next.
type EventStreamParams struct {
	// Type narrows the stream to one event type; empty matches every type.
	Type string `query:"type,omitempty"`
	// After is the exclusive resume cursor: absent means the current head; 0
	// replays from the first event.
	After int64 `query:"after,omitempty"`
}

// EventSubscriberStatus is the wire form of one event_subscribers row plus
// its derived lag and lock holder (WL-REQ-182).
type EventSubscriberStatus struct {
	Name            string    `json:"name"`
	LastReadOffset  int64     `json:"last_read_offset"`
	LastAckedOffset int64     `json:"last_acked_offset"`
	Lag             int64     `json:"lag"`
	HolderPID       int64     `json:"holder_pid"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// EventSubscriberListResponse is the response body of GET
// /api/v1/event-subscribers.
type EventSubscriberListResponse struct {
	Subscribers []EventSubscriberStatus `json:"subscribers"`
}

// EventSubscriberSeekRequest is the body of POST
// /api/v1/event-subscribers/{name}/seek.
type EventSubscriberSeekRequest struct {
	To int64 `json:"to"`
}
