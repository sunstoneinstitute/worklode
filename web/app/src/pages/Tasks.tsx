import { TaskList } from "@/components/TaskList"

/** Every task across every project, with a Project column (WL-REQ-1803) */
export function Tasks() {
  return (
    <div className="min-h-screen bg-background pt-13 lg:pt-19">
      <main className="p-5 pt-8 lg:p-page">
        <TaskList showProject syncWithUrl />
      </main>
    </div>
  )
}
