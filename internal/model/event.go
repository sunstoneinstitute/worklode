package model

import (
	"encoding/json"
	"time"
)

// Event is the wire form of one event log row (spec 025 §15/§18).
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

// EventSubscriberStatus is the wire form of one event_subscribers row plus
// its derived lag and lock holder (spec 025 §18).
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
