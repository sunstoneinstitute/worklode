import { useParams } from "@tanstack/react-router"
import { useProject } from "@/api/queries"
import { LoadingBlock } from "@/components/ui/load-state"
import { EditBodyPage } from "@/editor/EditBodyPage"
import { NotFound } from "@/pages/NotFound"

/** Edits a rule's body at /projects/$projectId/rule/$n/edit (WL-RULE-1306) */
export function EditRule() {
  const { projectId, n } = useParams({ from: "/projects/$projectId/rule/$n/edit" })
  const project = useProject(projectId)
  if (!/^[1-9]\d*$/.test(n)) return <NotFound />
  // ProjectLayout answers a project that fails to load
  if (!project.data) return <LoadingBlock label="Loading" className="m-5 h-80 lg:m-page" />
  // The number names the rule; RULE is one spelling of its ref's kind
  return <EditBodyPage target={{ kind: "rule", ref: `${project.data.key}-RULE-${n}` }} viewHref={`/projects/${projectId}/rule/${n}`} />
}
