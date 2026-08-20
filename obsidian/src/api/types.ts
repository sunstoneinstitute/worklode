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

/** One addressable section of a spec or ADR (025 §3), matching
 *  internal/model.DocSection. A plan has none (025 §9). Anchor is the
 *  section's identity and is frozen once Published; Number is "4.1a" or
 *  similar, "" for an unnumbered heading; LastRevisedIn is the document
 *  version whose accept last changed the section (025 §4.4). */
export interface DocSection {
  anchor: string;
  number: string; // "4.1a", "" for an unnumbered heading
  heading: string;
  depth: number;
  position: number; // 0-based document order
  last_revised_in: number;
  published: boolean;
}

/**
 * One typed link between documents (025 §14), matching
 * internal/model.DocEdge. An edge is always stated from the point of view
 * of the document being read: from_anchor is the near end, to_doc and
 * to_anchor the far end -- in DocDetail.edges and DocDetail.edges_in alike.
 *
 * One stored row carries both directions, so an edge in edges_in is that
 * row read backward: the ends swap and type is already the inverse
 * spelling -- covers/isCoveredBy, implements/isImplementedBy,
 * amends/amendedBy, replaces/isReplacedBy, requires/isRequiredBy,
 * wasDerivedFrom/hadDerivation, blocks/blockedBy.
 *
 * to_external is outbound-only: in edges exactly one of to_doc and
 * to_external is set; in edges_in to_doc always is and to_external is
 * always "".
 */
export interface DocEdge {
  type: string;
  from_anchor: string;
  to_doc: number;
  to_anchor: string;
  to_external: string;
}

/** A document's open candidate revision (025 §7.2), matching
 *  internal/model.DocRevision: a copy of the accepted body being edited
 *  against a stable document identity. At most one exists per document. */
export interface DocRevision {
  doc: number;
  body: string;
  created_by: string;
  created_at: string;
}

/**
 * A backbone design document (025 §5) -- a spec, an ADR, or a plan --
 * matching internal/model.Doc. GET /api/v1/docs blanks body on every row
 * (see withoutDocBodies in internal/api/docs.go); only GET
 * /api/v1/docs/{id} serves the text. A plan has no sections (025 §9) and no
 * corpus number (025 §14.3, where Number is 0).
 */
export interface Doc {
  id: number;
  project: string;
  kind: string; // spec | adr | plan
  number: number; // 0 for plans, which carry no corpus number (025 §14.3)
  slug: string;
  title: string;
  body: string; // the full markdown, frontmatter included
  status: string;
  version: number;
  issued: string; // YYYY-MM-DD, "" when unset
  assignee: string;
  created_by: string;
  created_at: string;
  updated_at: string;
}

/** The wire form of GET /api/v1/docs/{id}: a Doc plus the rows derived from
 *  its body, matching internal/model.DocDetail. */
export interface DocDetail extends Doc {
  sections: DocSection[];
  edges: DocEdge[];
  edges_in: DocEdge[];
  revision: DocRevision | null;
}
