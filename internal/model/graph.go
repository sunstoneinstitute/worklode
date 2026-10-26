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
	// childOf, dependsOn, followUpTo, duplicateOf.
	TaskEdges []Edge `json:"task_edges"`
	// Docs carry no Body: the graph draws and cites documents, it does not
	// render them.
	Docs     []Doc          `json:"docs"`
	DocEdges []GraphDocEdge `json:"doc_edges"`
	// Covers are the plan-to-rule covers edges (WL-REQ-1709) of the plans
	// among Docs. DocEdges draws covers at document level for the page.
	Covers []GraphCover `json:"covers"`
	Links  []GraphLink  `json:"links"`
}

// GraphDocEdge is one doc_edges relation collapsed to document level: one
// entry per (from, to, type), whatever anchors the stored rows name. Type is
// the stored type: covers, implements, requires, wasDerivedFrom, blocks,
// defers.
type GraphDocEdge struct {
	From int64  `json:"from_doc"`
	To   int64  `json:"to_doc"`
	Type string `json:"type"`
}

// GraphCover is one covers edge from plan From to rule number Rule of the
// same project.
type GraphCover struct {
	From int64 `json:"from_doc"`
	Rule int64 `json:"rule"`
}

// GraphLink joins a task to a document. Type is planned_in (tasks.plan_doc:
// the plan whose acceptance minted the task), about (tasks.about_doc), or
// generated_by (docs.generated_by_task: the task that wrote the document).
type GraphLink struct {
	Task string `json:"task"`
	Doc  int64  `json:"doc"`
	Type string `json:"type"`
}
