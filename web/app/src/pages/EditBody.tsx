import { useParams } from "@tanstack/react-router"
import { type BodyTarget, useProject } from "@/api/queries"
import { LoadingBlock } from "@/components/ui/load-state"
import { EditBodyPage } from "@/editor/EditBodyPage"
import { NotFound } from "@/pages/NotFound"

// What the cockpit's /projects/{proj}/{kind}/{n} resolves (internal/api/canonicalurl.go):
// a document kind, else a task kind (ns.TaskKinds) or the kind-free "task"
const DOC_KINDS = ["spec", "plan"]
const TASK_KINDS = ["bug", "chore", "decision", "design", "feature", "rally", "review", "spike", "task"]

/** Edits a document's or a task's body at /projects/$projectId/$kind/$n/edit (WL-RULE-1306) */
export function EditBody() {
  const { projectId, kind, n } = useParams({ from: "/projects/$projectId/$kind/$n/edit" })
  const project = useProject(projectId)
  const known = DOC_KINDS.includes(kind) || TASK_KINDS.includes(kind)
  if (!known || !/^[1-9]\d*$/.test(n)) return <NotFound />
  if (!project.data) return <LoadingBlock label="Loading" className="m-5 h-80 lg:m-page" />
  const key = project.data.key
  const target: BodyTarget = DOC_KINDS.includes(kind)
    ? { kind: "doc", ref: `${key}-${kind.toUpperCase()}-${n}` }
    : { kind: "task", ref: `${key}-${n}` }
  return <EditBodyPage target={target} viewHref={`/projects/${projectId}/${kind}/${n}`} />
}
