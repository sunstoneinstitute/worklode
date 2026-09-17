package model

// ProjectGraph is one project's work graph: every live task, the task edges
// between them, and the live documents reachable from a task — directly
// through a link, or through a chain of document edges. A document with no
// path to a task is left out. Nothing here is stored; a reader recomputes it
// per request (store.ProjectGraph).
type ProjectGraph struct {
	Project string `json:"project"`
	Tasks   []Task `json:"tasks"`
	// TaskEdges are task_edges rows with both ends among Tasks:
	// blocks, child_of, follow_up_to, duplicate_of.
	TaskEdges []Edge `json:"task_edges"`
	// Docs carry no Body: the graph draws and cites documents, it does not
	// render them.
	Docs     []Doc          `json:"docs"`
	DocEdges []GraphDocEdge `json:"doc_edges"`
	Links    []GraphLink    `json:"links"`
}

// GraphDocEdge is one doc_edges relation collapsed to document level: one
// entry per (from, to, type), whatever anchors the stored rows name. Type is
// the stored type: covers, implements, amends, replaces, requires,
// wasDerivedFrom, blocks, defers.
type GraphDocEdge struct {
	From int64  `json:"from_doc"`
	To   int64  `json:"to_doc"`
	Type string `json:"type"`
}

// GraphLink joins a task to a document. Type is planned_in (tasks.plan_doc:
// the plan whose acceptance minted the task), about (tasks.about_doc), or
// generated_by (docs.generated_by_task: the task that wrote the document).
type GraphLink struct {
	Task string `json:"task"`
	Doc  int64  `json:"doc"`
	Type string `json:"type"`
}
