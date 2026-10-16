import { useParams } from "@tanstack/react-router"
import { TaskList } from "@/components/TaskList"

/** A project's tasks, under the project layout (WL-REQ-1803) */
export function ProjectTasks() {
  const { projectId } = useParams({ from: "/projects/$projectId/tasks" })
  return (
    <main className="p-5 pt-8 lg:p-page">
      <TaskList projectId={projectId} syncWithUrl />
    </main>
  )
}
