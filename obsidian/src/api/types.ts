// Wire shapes for the worklode HTTP API's read surface. The source of truth
// is internal/cli/client.go (the Go client's own struct + json tags for the
// same endpoints) plus, for the two expanded shapes no Go client consumes,
// the serializers in internal/api/tasks.go (taskJSON, taskListDetailJSON,
// edgeOut, edgeIn) and internal/api/docs.go (docJSON). When the two disagree,
// change the Go side first: ADR 036 makes internal/model the one declaration
// of every shape crossing the HTTP boundary, and this file is meant to
// shrink to a thin mirror of that, not grow its own opinions.

/** A repo mapped to a project, and its terminal delivery state. */
export interface RepoMapping {
  repo: string;
  done_state: string;
}

/** A project: id, display name, project key, mapped repos, ranking focus. */
export interface Project {
  id: string;
  name: string;
  key: string;
  repos: RepoMapping[];
  focus: string[];
}

/** A task, matching internal/api's taskJSON (every store.Task field). */
export interface Task {
  id: string;
  project: string;
  title: string;
  body: string;
  priority: string;
  kind: string;
  state: string;
  concern: string;
  needs_decomposition: boolean;
  created_by: string;
  created_at: string;
  updated_at: string;
  skills: string[];
  assignee: string;
  branch: string;
}

/** The "to"-side of one of a task's outgoing edges. */
export interface TaskEdgeOut {
  to: string;
  type: string;
}

/** The "from"-side of one of a task's incoming edges. */
export interface TaskEdgeIn {
  from: string;
  type: string;
}

/**
 * A GET /api/v1/tasks?detail=true row: a Task plus its blocked status and
 * edges. edges.out/edges.in are always present arrays, never null — the
 * server guarantees this so callers can treat them as required. Deliberately
 * has no hierarchy or lease field: hierarchy is derivable from the child_of
 * edges here, and lease is per-task ephemeral state a list response would
 * misreport (see internal/api/tasks.go's taskListDetailJSON).
 */
export interface TaskListDetail extends Task {
  blocked: boolean;
  edges: {
    out: TaskEdgeOut[];
    in: TaskEdgeIn[];
  };
}

/** One heading extracted from a synced document's body. */
export interface DocSection {
  anchor: string;
  heading: string;
  depth: number;
  position: number;
}

/** One cross-reference extracted from a synced document's body. */
export interface DocEdge {
  src_anchor: string;
  rel: string;
  target: string;
  target_anchor: string;
}

/**
 * A stored document, matching internal/api's docJSON. body and frontmatter
 * are present only when the list request carried body=true; sections and
 * edges are populated by GET /api/v1/docs/{id}, never by the list endpoint
 * this client's listDocs calls.
 */
export interface Doc {
  id: string;
  project: string;
  kind: string;
  ordinal: string;
  status: string;
  title: string;
  version: number;
  source_branch: string;
  source_dirty: boolean;
  synced_at: string;
  body?: string;
  frontmatter?: unknown;
  sections?: DocSection[];
  edges?: DocEdge[];
}
