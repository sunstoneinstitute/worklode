import { HttpError, browser, useTaskRecord } from "@/api/queries"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { taskForm } from "@/forms/configs"
import { SchemaForm } from "@/forms/SchemaForm"
import { NotFound } from "@/pages/NotFound"

/** Edits a task, its body under the lock (WL-REQ-1892). `viewHref` is its cockpit page, where leaving goes. */
export function EditTask({ taskRef, viewHref }: { taskRef: string; viewHref: string }) {
  const task = useTaskRecord(taskRef)
  const leave = () => browser.assign(viewHref)
  if (task.error instanceof HttpError && task.error.status === 404) return <NotFound />
  return (
    <main className="p-5 pt-8 lg:p-page">
      {task.isPending ? (
        <LoadingBlock label="Loading" className="h-80" />
      ) : task.isError ? (
        <LoadError message="Couldn't load the task." onRetry={() => void task.refetch()} />
      ) : (
        <SchemaForm key={task.data.id} config={taskForm} record={task.data} lockKind="task" title={`${task.data.id}: ${task.data.title}`} noun="the task" onDone={leave} onClose={leave} />
      )}
    </main>
  )
}
