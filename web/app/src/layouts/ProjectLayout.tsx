import { Outlet, useParams } from "@tanstack/react-router"
import { ProjectToolbar } from "@/components/ProjectToolbar"

/**
 * A project's pages share the project toolbar under the header. The
 * toolbar is sticky, so it must sit directly in this tall wrapper — a
 * wrapper sized to the toolbar alone gives it nowhere to stick (see
 * AGENTS.md). lg:flex-col lets Project Overview's grid stretch to the
 * bottom of the window, as its own wrapper used to.
 */
export function ProjectLayout() {
  const { projectId } = useParams({ from: "/projects/$projectId" })

  return (
    <div className="min-h-screen bg-background pt-13 lg:flex lg:flex-col lg:pt-19">
      <ProjectToolbar projectId={projectId} />
      <Outlet />
    </div>
  )
}
