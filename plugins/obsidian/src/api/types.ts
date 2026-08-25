// Wire shapes for the worklode HTTP API's read surface. The source of truth
// is internal/model — ADR 036 makes it the one declaration of every shape
// crossing the HTTP boundary — specifically its project.go, task.go,
// taskdetail.go and doc.go, one Go type per interface below. There is no
// codegen yet (WL-76), so this file is kept by hand and drifts silently when
// the Go side moves; when the two disagree, change the Go side first and
// mirror it here, never the reverse.

/** The delete record a soft-deleted task or document carries (044 §2),
 *  matching internal/model.Tombstone. Absent on a live row, and absent from
 *  every ordinary list: only a fetch by id, or the `?deleted=true` listing
 *  that switches to the tombstoned rows, serves one. The mirror reads it as
 *  proof that a row really is deleted -- see deletedTaskPaths, which prunes no
 *  note without it. */
export interface Tombstone {
  deleted_at: string;
  deleted_by: string;
  justification?: string;
}

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

/** A task, matching internal/model.Task. The mirror declares only the fields
 *  a note renders, so the Go type may carry more. */
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
  tombstone?: Tombstone;
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
 * A GET /api/v1/tasks?detail=true row, matching internal/model.TaskListDetail:
 * a Task plus its blocked status and edges. edges.out/edges.in are always
 * present arrays, never null — the server guarantees this so callers can treat
 * them as required. Deliberately has no hierarchy or lease field: hierarchy is
 * derivable from the child_of edges here, and lease is per-task ephemeral state
 * a list response would misreport.
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
 * An anchor is "" when that end of the edge is the whole document rather
 * than one of its sections.
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
 *
 * to_project, to_slug, to_kind and to_number name the far end for a reader:
 * an edge is stored by id, and "document 42" tells a human nothing. A read
 * resolves them alongside the id; an unresolved to_external edge leaves them
 * empty.
 *
 * to_project is what makes the far end linkable rather than merely named: a
 * slug is unique only within a project, and an edge can leave one (the 025
 * §14.3 shorthand resolves on a project key), so a link built from to_slug
 * alone would quietly point at the near end's project.
 *
 * completed_with carries the doc_coverage_completed_with side-table (026
 * §5, §5.3) that only a covers or defers edge ever populates: a partial
 * covers entry's fullCoverageWith closure, in authored order, or a defers
 * entry's single-element owner. Each element is a slug when the reference
 * resolved to a live document, or the reference verbatim when it did not.
 * Absent for every other edge, and for a full/none covers entry.
 */
export interface DocEdge {
  type: string;
  from_anchor: string;
  to_doc: number;
  to_anchor: string;
  to_external: string;
  to_project: string;
  to_slug: string;
  to_kind: string;
  to_number: number;
  completed_with?: string[];
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
  /** The project's key ("WL"), the first segment of the 025 §14.3 shorthand
   *  ("WL-SPEC-29"); project carries the project id, which the shorthand is not
   *  built from. Stamped by the API from the document's project, so it is
   *  absent on a response served before WL-336. */
  project_key?: string;
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
  /** The task that authored the document (025 §12, projected as
   *  prov:wasGeneratedBy); "" when no task did -- a cockpit author, an agent
   *  outside a claimed worktree, a corpus import. Distinct from created_by,
   *  which names the actor rather than the unit of work. */
  generated_by_task: string;
  created_at: string;
  updated_at: string;
  tombstone?: Tombstone;
}

/** The wire form of GET /api/v1/docs/{id}: a Doc plus the rows derived from
 *  its body, matching internal/model.DocDetail. */
export interface DocDetail extends Doc {
  sections: DocSection[];
  edges: DocEdge[];
  edges_in: DocEdge[];
  revision: DocRevision | null;
}
