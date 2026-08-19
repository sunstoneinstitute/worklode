package model

import (
	"encoding/json"
	"time"
)

// TimelineResponse is the response body of GET /api/v1/tasks/{id}/timeline:
// the task plus its merged timeline, ascending by time.
type TimelineResponse struct {
	Task     Task            `json:"task"`
	Timeline []TimelineEntry `json:"timeline"`
}

// TimelineEntry is one row of a task's timeline: a flat union discriminated
// by Type. At and Type are the only fields every entry carries; the rest are
// omitempty and populated per type:
//
//	state       Change, EventID
//	pr          Repo, Number, Title, State, URL, MergedAt
//	ci          Repo, Workflow, Status, Conclusion, URL, CompletedAt
//	review      Repo, Number, Reviewer, State
//	artifact    Kind, Name, Version
//	deployment  Environment, TargetName, Status
//	runtime     Kind, Cluster, Workload, Message
//	landed      Repo, SHA
//	deployed    Repo, Environment
//	released    Repo, Tag
//
// One flat struct rather than a per-type struct behind a payload object
// (ADR 036 §8): the entries are flat on the wire, seven of the fields are
// shared by two or more types, and Go has no sum type that would buy a
// consumer exhaustiveness checking for the nesting it would cost. A consumer
// switches on Type either way — the difference is only whether the fields it
// then reads are declared.
//
// Change stays raw because it is a stored state_log payload passing through,
// not a shape this API declares: LogChange writes {"field","old","new"} for a
// field update, {"field","names"} for materialized secrets, and
// {"field":"edge","op","type","from","to"} for AddEdge/RemoveEdge, so there
// is no one struct to decode it into (ADR 036 §3).
type TimelineEntry struct {
	At   time.Time `json:"at"`
	Type string    `json:"type"`

	Change  json.RawMessage `json:"change,omitempty"`
	EventID int64           `json:"event_id,omitempty"`

	Repo        string     `json:"repo,omitempty"`
	Number      int64      `json:"number,omitempty"`
	Title       string     `json:"title,omitempty"`
	State       string     `json:"state,omitempty"`
	URL         string     `json:"url,omitempty"`
	MergedAt    *time.Time `json:"merged_at,omitempty"`
	Workflow    string     `json:"workflow,omitempty"`
	Status      string     `json:"status,omitempty"`
	Conclusion  *string    `json:"conclusion,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Reviewer    string     `json:"reviewer,omitempty"`
	Kind        string     `json:"kind,omitempty"`
	Name        string     `json:"name,omitempty"`
	Version     string     `json:"version,omitempty"`
	Environment string     `json:"environment,omitempty"`
	TargetName  string     `json:"target_name,omitempty"`
	Cluster     string     `json:"cluster,omitempty"`
	Workload    string     `json:"workload,omitempty"`
	Message     string     `json:"message,omitempty"`
	SHA         string     `json:"sha,omitempty"`
	Tag         string     `json:"tag,omitempty"`
}
