import { type FormEvent, useCallback, useMemo, useState } from "react"
import { useCreateTask, useDeleteTask, useUpdateTask } from "@/api/mutations"
import { useCrew, useProjects, useTasks } from "@/api/queries"
import type { Project, Task } from "@/api/types"
import { TrashIcon } from "@/components/icons"
import { Button } from "@/components/ui/button"
import { DataTable, type DataTableColumn, type DataTableUser, type TableHelpers } from "@/components/ui/data-table"
import { Input } from "@/components/ui/input"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { useTableUrlState } from "@/hooks/use-table-url-state"

// Worklode's task states. The server decides which a task can move to and
// refuses the rest (WL-REQ-1803).
const STATE_OPTIONS = [
  { value: "draft", label: "Draft" },
  { value: "ready", label: "Ready" },
  { value: "in_progress", label: "In progress", colorVar: "--color-accent" },
  { value: "in_review", label: "In review", colorVar: "--color-accent" },
  { value: "merged", label: "Merged", colorVar: "--color-highlight" },
  { value: "deployed_dev", label: "Deployed dev", colorVar: "--color-highlight" },
  { value: "deployed_prod", label: "Deployed prod", colorVar: "--color-highlight" },
  { value: "released", label: "Released", colorVar: "--color-highlight" },
  { value: "abandoned", label: "Abandoned" },
]

const PRIORITY_OPTIONS = [
  { value: "critical", label: "Critical", colorVar: "--color-accent-warn" },
  { value: "high", label: "High", colorVar: "--color-accent-warn" },
  { value: "medium", label: "Medium", colorVar: "--color-accent" },
  { value: "low", label: "Low" },
]

/** A task's canonical URL, /projects/<proj>/task/<n> (WL-RULE-1306) */
function taskHref(task: Task) {
  const n = (task.id ?? "").split("-").pop()
  return `/projects/${task.project}/task/${n}`
}

const TITLE_COLUMN: DataTableColumn<Task> = {
  key: "title",
  label: "Title",
  type: "text",
  width: "w-70",
  filterable: false,
  // A plain link: the templ cockpit serves the task page until it is ported
  render: (task) => (
    <a href={taskHref(task)} className="underline-offset-2 transition-colors hover:text-highlight hover:underline">
      {task.title}
    </a>
  ),
}

// Only on /tasks, where tasks from every project mix
function projectColumn(projects: Project[] | undefined): DataTableColumn<Task> {
  return {
    key: "project",
    label: "Project",
    type: "select",
    width: "w-45",
    options: (projects ?? []).map((project) => ({ value: project.id ?? "", label: project.name ?? "" })),
    // Only once projects have loaded: until then every project is unknown
    unknownLabel: projects ? "Deleted project" : undefined,
  }
}

const OTHER_COLUMNS: DataTableColumn<Task>[] = [
  { key: "assignee", label: "Assignee", type: "user", width: "w-50" },
  { key: "due", label: "Due", type: "date", width: "w-40" },
  { key: "state", label: "State", type: "select", width: "w-40", options: STATE_OPTIONS },
  { key: "priority", label: "Priority", type: "select", width: "w-35", options: PRIORITY_OPTIONS },
  {
    key: "actions",
    label: "",
    type: "actions",
    actions: [
      {
        id: "delete",
        label: "Delete task",
        icon: <TrashIcon size={20} />,
        variant: "destructive",
        confirm: (task) => `Delete ${task.id}?`,
        onSelect: (_task, { removeRow }) => removeRow(),
      },
    ],
  },
]

// Module-level so DataTable gets a stable array and function; new ones each
// render would rebuild every column
const COLUMNS = [TITLE_COLUMN, ...OTHER_COLUMNS]

function getTaskId(task: Task) {
  return task.id ?? ""
}

/**
 * The task table (WL-REQ-1803). `projectId` shows only that project's
 * tasks and new tasks join it. `showProject` adds the Project column and a
 * project picker to the new-task form, for /tasks. `syncWithUrl` keeps
 * sort, filters and search in the URL.
 */
export function TaskList({
  projectId,
  showProject = false,
  syncWithUrl = false,
}: {
  projectId?: string
  showProject?: boolean
  syncWithUrl?: boolean
}) {
  const tasks = useTasks({ project: projectId })
  const crew = useCrew()
  const projects = useProjects()
  const createTask = useCreateTask()
  const updateTask = useUpdateTask()
  const deleteTask = useDeleteTask()
  // The Project column exists from the first render, so URL filters on it
  // are never dropped as unknown
  const columns = useMemo(
    () => (showProject ? [TITLE_COLUMN, projectColumn(projects.data), ...OTHER_COLUMNS] : COLUMNS),
    [showProject, projects.data],
  )
  const urlState = useTableUrlState(columns, { enabled: syncWithUrl })
  const users = useMemo<DataTableUser[]>(
    () => (crew.data ?? []).map((c) => ({ id: c.actor ?? "", name: c.display_name || (c.actor ?? "") })),
    [crew.data],
  )

  const updateMutate = updateTask.mutate
  const deleteMutate = deleteTask.mutate
  const createMutate = createTask.mutate
  const onUpdateRow = useCallback((id: string, patch: Partial<Task>) => updateMutate({ id, patch }), [updateMutate])
  const onRemoveRow = useCallback((id: string) => deleteMutate(id), [deleteMutate])
  const onAddRow = useCallback(
    (row: Task) =>
      createMutate({ title: row.title, project: projectId ?? row.project, kind: "feature", priority: "medium" }),
    [createMutate, projectId],
  )

  if (tasks.isPending) return <LoadingBlock label="Loading tasks" className="h-80" />
  if (tasks.isError) return <LoadError message="Couldn't load tasks." onRetry={() => void tasks.refetch()} />

  return (
    <DataTable
      columns={columns}
      data={tasks.data}
      getRowId={getTaskId}
      users={users}
      onUpdateRow={onUpdateRow}
      onRemoveRow={onRemoveRow}
      onAddRow={onAddRow}
      title="Tasks"
      {...urlState}
      toolbarActions={({ addRow }) => (
        <NewTask
          projects={showProject ? (projects.data ?? []) : undefined}
          addRow={addRow}
          pending={createTask.isPending}
        />
      )}
    />
  )
}

function NewTask({
  projects,
  addRow,
  pending,
}: {
  /** Offered as a picker when set (on /tasks); otherwise the list's own project is used */
  projects?: Project[]
  pending: boolean
} & TableHelpers<Task>) {
  const [title, setTitle] = useState("")
  const [project, setProject] = useState("")

  function submit(event: FormEvent) {
    event.preventDefault()
    addRow({ title: title.trim(), project })
    setTitle("")
  }

  return (
    <form onSubmit={submit} className="flex flex-wrap items-center gap-2.5">
      <Input
        aria-label="New task title"
        placeholder="New task"
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        className="w-56"
      />
      {projects && (
        <Select value={project} onValueChange={setProject}>
          <SelectTrigger aria-label="Project" className="w-44">
            <SelectValue placeholder="Project" />
          </SelectTrigger>
          <SelectContent>
            {projects.map((p) => (
              <SelectItem key={p.id} value={p.id ?? ""}>
                {p.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )}
      <Button type="submit" size="sm" disabled={!title.trim() || (projects && !project) || pending}>
        Add task
      </Button>
    </form>
  )
}
