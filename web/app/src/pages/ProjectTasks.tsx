import { useParams } from "@tanstack/react-router"
import { TaskList } from "@/components/TaskList"

// A project's Tasks page. Under the project layout (toolbar and all): the
// task table full width. No Project column (everything here belongs to
// this project) and, like the other full-page tables, no title icon.
export function ProjectTasks() {
  const { projectId } = useParams({ from: "/projects/$projectId" })

  return (
    <main className="p-5 pt-8 lg:p-page">
      <TaskList showTitleIcon={false} syncWithUrl projectId={projectId} />
    </main>
  )
}
