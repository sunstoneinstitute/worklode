package model

import "time"

// TaskActivity is one row of a task's activity log: a decoded OTLP log
// record attributed to a task (spec 071 §2). It is distinct from the events
// table's append-only provenance — activity is high-volume, per-tool-call
// telemetry with a retention window, not a durable audit trail.
type TaskActivity struct {
	ID      int64         `json:"id"`
	Task    string        `json:"task"`
	Actor   string        `json:"actor"`
	Agent   string        `json:"agent"`
	Session string        `json:"session"`
	At      time.Time     `json:"at"`
	Event   string        `json:"event"`
	Attrs   ActivityAttrs `json:"attrs"`
}

// ActivityAttrs is the allowlisted set of OTLP log-record attributes a
// TaskActivity row may carry (spec 071 §2, 063 §3). internal/otlp.DecodeLogs
// is the only writer; the record body, prompt/response text, tool content,
// and every other attribute are dropped before reaching here. Every field
// is omitempty so an activity row with none of a kind of attribute doesn't
// carry a field of zeroes on the wire.
type ActivityAttrs struct {
	ToolName            string `json:"tool_name,omitempty"`
	Success             *bool  `json:"success,omitempty"`
	DurationMS          int64  `json:"duration_ms,omitempty"`
	ErrorType           string `json:"error_type,omitempty"`
	DecisionType        string `json:"decision_type,omitempty"`
	DecisionSource      string `json:"decision_source,omitempty"`
	Model               string `json:"model,omitempty"`
	RequestID           string `json:"request_id,omitempty"`
	Attempt             int64  `json:"attempt,omitempty"`
	StatusCode          int64  `json:"status_code,omitempty"`
	Error               string `json:"error,omitempty"`
	PromptLength        int64  `json:"prompt_length,omitempty"`
	ResponseLength      int64  `json:"response_length,omitempty"`
	CommandName         string `json:"command_name,omitempty"`
	ToolUseID           string `json:"tool_use_id,omitempty"`
	ToolInputSizeBytes  int64  `json:"tool_input_size_bytes,omitempty"`
	ToolResultSizeBytes int64  `json:"tool_result_size_bytes,omitempty"`
}
