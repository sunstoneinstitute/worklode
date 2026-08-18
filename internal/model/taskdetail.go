package model

// TaskEdgeOut and TaskEdgeIn are the two halves of a TaskDetail's edge list.
type TaskEdgeOut struct {
	To   string `json:"to"`
	Type string `json:"type"`
}

type TaskEdgeIn struct {
	From string `json:"from"`
	Type string `json:"type"`
}

// TaskParent is the one-hop-up projection of a task's parent: enough to
// render a breadcrumb without a second request.
type TaskParent struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
}

// TaskProgress is the derived child roll-up, closed of total direct
// children. Computed on read, never stored.
type TaskProgress struct {
	Closed int `json:"closed"`
	Total  int `json:"total"`
}

// TaskHierarchy is the spec-004 hierarchy block on a task detail. Parent is
// null for a root task; Progress is zeroed for a task with no children.
type TaskHierarchy struct {
	Parent   *TaskParent  `json:"parent"`
	Progress TaskProgress `json:"progress"`
}

// TaskDetail is the wire form of GET /api/v1/tasks/{id}: a Task plus its
// blocked status, edges, hierarchy, and (when active) lease. AgentSessions is
// populated only alongside Lease — the sessions recorded against it — so a
// `lode task show` never needs a second request to explain who is holding a
// task and what they're running.
type TaskDetail struct {
	Task
	Blocked bool `json:"blocked"`
	Edges   struct {
		Out []TaskEdgeOut `json:"out"`
		In  []TaskEdgeIn  `json:"in"`
	} `json:"edges"`
	Lease         *Lease         `json:"lease,omitempty"`
	AgentSessions []AgentSession `json:"agent_sessions,omitempty"`
	Hierarchy     TaskHierarchy  `json:"hierarchy"`
}

// TaskListDetail is one row of GET /api/v1/tasks?detail=true: the base task
// plus the two field groups a list consumer cannot cheaply reconstruct. It is
// not TaskDetail: hierarchy is derivable from the child_of edges below, and
// lease is per-task ephemeral state that a cached list would misreport. Both
// stay on GET /api/v1/tasks/{id}.
type TaskListDetail struct {
	Task
	Blocked bool `json:"blocked"`
	Edges   struct {
		Out []TaskEdgeOut `json:"out"`
		In  []TaskEdgeIn  `json:"in"`
	} `json:"edges"`
}

// TaskListDetailResponse is the response body of GET
// /api/v1/tasks?detail=true.
type TaskListDetailResponse struct {
	Tasks []TaskListDetail `json:"tasks"`
}

// DecomposeResponse is the wire form of POST /api/v1/tasks/{id}/decompose:
// the parent, keeping its id and kind, and the children it now tracks.
type DecomposeResponse struct {
	Parent   Task   `json:"parent"`
	Children []Task `json:"children"`
}

// TimelineResponse is the response body of GET
// /api/v1/tasks/{id}/timeline. Each entry always has "at" (RFC3339 string)
// and "type" fields; the remaining fields vary by type — see
// internal/api/timeline.go for the full set per type.
type TimelineResponse struct {
	Task     Task             `json:"task"`
	Timeline []map[string]any `json:"timeline"`
}
