package model

import "time"

// Decision is one question posed on a task (025 §10.1). A task carries one
// or more; Key is stable within the task and (Task, Key) is the address
// people use, e.g. "WL-643/x-distribution".
type Decision struct {
	ID           int64            `json:"id"`
	Task         string           `json:"task"`
	Key          string           `json:"key"`
	Position     int              `json:"position"`
	Group        string           `json:"group,omitempty"`
	Question     string           `json:"question"`
	Context      string           `json:"context,omitempty"`
	ResponseType string           `json:"response_type"`
	Options      []DecisionOption `json:"options,omitempty"`
	MinPicks     *int             `json:"min_picks,omitempty"`
	MaxPicks     *int             `json:"max_picks,omitempty"`
	Answer       *DecisionAnswer  `json:"answer,omitempty"`
	DecidedBy    string           `json:"decided_by,omitempty"`
	DecidedAt    *time.Time       `json:"decided_at,omitempty"`
}

type DecisionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// DecisionAnswer is §10.1's answer JSON; Value is the yes_no third field
// ("yes" | "no" | "unsure").
type DecisionAnswer struct {
	Picked   []string `json:"picked,omitempty"`
	Notes    string   `json:"notes,omitempty"`
	Freetext string   `json:"freetext,omitempty"`
	Value    string   `json:"value,omitempty"`
}
