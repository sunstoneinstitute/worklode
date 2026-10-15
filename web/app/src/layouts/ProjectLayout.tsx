import { Outlet, useParams } from "@tanstack/react-router"
import { HttpError, useProject } from "@/api/queries"
import { ProjectToolbar } from "@/components/ProjectToolbar"
import { LoadError } from "@/components/ui/load-state"
import { NotFound } from "@/pages/NotFound"

/**
 * A project's pages share the project toolbar under the header. The
 * toolbar is sticky, so it must sit directly in this tall wrapper — a
 * wrapper sized to the toolbar alone gives it nowhere to stick (see
 * AGENTS.md). lg:flex-col lets Project Overview's grid stretch to the
 * bottom of the window, as its own wrapper used to. A project id the
 * server doesn't know gets the Not Found page instead; any other failure
 * shows an error with Retry in place of the toolbar and page, since
 * nothing below it can be trusted without the project.
 */
export function ProjectLayout() {
  const { projectId } = useParams({ from: "/projects/$projectId" })
  const project = useProject(projectId)

  if (project.error instanceof HttpError && project.error.status === 404) return <NotFound />
  if (project.isError) {
    return (
      <div className="min-h-screen bg-background pt-13 lg:pt-19">
        <main className="p-5 lg:p-page">
          <LoadError message="Couldn't load this project." onRetry={() => void project.refetch()} />
        </main>
      </div>
    )
  }

  return (
    <div className="min-h-screen bg-background pt-13 lg:flex lg:flex-col lg:pt-19">
      <ProjectToolbar projectId={projectId} projectName={project.data?.name ?? ""} />
      <Outlet />
    </div>
  )
}
