import { useParams } from "@tanstack/react-router"
import { ProjectSummary } from "@/components/ProjectSummary"
import { TaskList } from "@/components/TaskList"

/**
 * A project's Overview (WL-REQ-1804): the summary card over the task list.
 * The design's upcoming-meetings sidebar is not rendered (no meetings API);
 * its column stays empty on desktop.
 */
export function ProjectOverview() {
  const { projectId } = useParams({ from: "/projects/$projectId/" })

  return (
    <div className="flex flex-col gap-10 px-5 pb-5 lg:grid lg:flex-1 lg:grid-cols-12 lg:items-stretch lg:gap-0 lg:p-0">
      <main className="flex min-w-0 flex-col gap-8 lg:col-span-8 lg:p-page">
        <ProjectSummary projectId={projectId} />
        <TaskList projectId={projectId} />
      </main>
    </div>
  )
}
