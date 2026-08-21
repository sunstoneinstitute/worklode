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

// TaskTreeNode is one container, its derived progress, and its direct
// children — the unit `lode task tree` renders. Children carries every live
// child whatever its state, so Progress and the listed children always count
// the same set.
type TaskTreeNode struct {
	Parent   Task         `json:"parent"`
	Progress TaskProgress `json:"progress"`
	Children []Task       `json:"children"`
}

// TaskTreeResponse is the wire form of GET /api/v1/tasks?tree=true: the whole
// hierarchy in one response, so a client never fetches children per parent.
type TaskTreeResponse struct {
	Nodes []TaskTreeNode `json:"nodes"`
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
	// Blobs are the task's images and attachments (spec 021 §3). Attached
	// blobs appear nowhere in the body markdown, so a reader that only
	// renders Body would never learn they exist.
	Blobs []TaskBlob `json:"blobs,omitempty"`
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
