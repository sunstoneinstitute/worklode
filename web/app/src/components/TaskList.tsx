import { useQueryClient } from "@tanstack/react-query"
import { useCallback } from "react"
import { useCreateTask, useDeleteTask, useUpdateTask } from "@/api/mutations"
import { queryKeys, useCrew, useTasks } from "@/api/queries"
import type { Task, TaskPatch } from "@/api/types"
import { Button } from "@/components/ui/button"
import { DataTable, type DataTableColumn } from "@/components/ui/data-table"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
import { PencilIcon, TasksIcon, TrashIcon } from "@/components/icons"
import { useTableUrlState } from "@/hooks/use-table-url-state"
import { celebrate } from "@/lib/burst"

// The projects from Home's quick links
const PROJECT_OPTIONS = [
  { value: "the-grid", label: "The Grid" },
  { value: "melting-glaciers", label: "Melting Glaciers" },
]

const STATUS_OPTIONS = [
  { value: "todo", label: "To Do" },
  { value: "in-progress", label: "In Progress", colorVar: "--color-accent" },
  // Highlight blue, not success green (dark text on it: 8.6:1)
  { value: "done", label: "Done", colorVar: "--color-highlight" },
  { value: "blocked", label: "Blocked", colorVar: "--color-accent-warn" },
]

const PRIORITY_OPTIONS = [
  { value: "low", label: "Low" },
  { value: "medium", label: "Medium", colorVar: "--color-accent" },
  { value: "high", label: "High", colorVar: "--color-accent-warn" },
]

const NAME_COLUMN: DataTableColumn<Task> = {
  key: "name",
  label: "Task Name",
  type: "text",
  width: "w-70",
  filterable: false,
}

// Only on the top-level Tasks page, where tasks from every project mix
const PROJECT_COLUMN: DataTableColumn<Task> = {
  key: "project",
  label: "Project",
  type: "select",
  width: "w-45",
  options: PROJECT_OPTIONS,
}

const OTHER_COLUMNS: DataTableColumn<Task>[] = [
  { key: "assignee", label: "Assignee", type: "user", width: "w-50" },
  { key: "due", label: "Due Date", type: "date", width: "w-40" },
  { key: "status", label: "Status", type: "select", width: "w-40", options: STATUS_OPTIONS },
  { key: "priority", label: "Priority", type: "select", width: "w-35", options: PRIORITY_OPTIONS },
  {
    key: "actions",
    label: "",
    type: "actions",
    actions: [
      {
        id: "edit",
        label: "Edit task",
        icon: <PencilIcon size={20} />,
        onSelect: () => {},
      },
      {
        id: "delete",
        label: "Delete task",
        icon: <TrashIcon size={20} />,
        variant: "destructive",
        onSelect: (_task, { removeRow }) => removeRow(),
      },
    ],
  },
]

// Module-level so DataTable gets a stable array — a fresh one each render
// would rebuild every column (and reset any open cell picker).
const COLUMNS = [NAME_COLUMN, ...OTHER_COLUMNS]
const COLUMNS_WITH_PROJECT = [NAME_COLUMN, PROJECT_COLUMN, ...OTHER_COLUMNS]

// Module-level for the same reason: DataTable rebuilds every column (and
// closes any open cell picker) whenever getRowId's identity changes, which
// an inline arrow function here would do on every render — including ones
// triggered by an unrelated mutation settling elsewhere in the table.
function getTaskId(task: Task) {
  return task.id
}

// Easter egg: marking a task done sets off a celebration from its status
// badge — one of several, picked at random (see celebrate() in
// src/lib/burst.ts).
function celebrateIfNewlyDone(task: Task | undefined, patch: TaskPatch) {
  if (!task || patch.status !== "done" || task.status === "done") return
  const cell = document.querySelector(
    `[data-row-id="${CSS.escape(task.id)}"] [data-column-id="status"]`,
  )
  const badge = cell?.querySelector("[data-slot=badge]") ?? cell
  if (!badge) return
  const rect = badge.getBoundingClientRect()
  celebrate(rect.left + rect.width / 2, rect.top + rect.height / 2)
}

/**
 * `showProject` adds a filterable Project column after Task Name — for the
 * top-level Tasks page, which lists tasks across every project. Home and
 * Project Overview leave it off. `showTitleIcon={false}` drops the icon
 * before the "Tasks" title (top-level pages show the plain title).
 * `syncWithUrl` keeps sort, filters and search in the URL — for the
 * full-page Tasks tables, not the ones embedded in Home and Project
 * Overview. `projectId` shows only that project's tasks, and new tasks
 * join that project.
 */
export function TaskList({
  showProject = false,
  showTitleIcon = true,
  syncWithUrl = false,
  projectId,
}: {
  showProject?: boolean
  showTitleIcon?: boolean
  syncWithUrl?: boolean
  projectId?: string
}) {
  const tasks = useTasks({ projectId })
  const crew = useCrew()
  const createTask = useCreateTask()
  const updateTask = useUpdateTask()
  const deleteTask = useDeleteTask()
  const columns = showProject ? COLUMNS_WITH_PROJECT : COLUMNS
  const urlState = useTableUrlState(columns, { enabled: syncWithUrl })

  const queryClient = useQueryClient()
  const updateMutate = updateTask.mutate
  const deleteMutate = deleteTask.mutate
  // Reads the cache directly (queryClient is stable, unlike tasks.data)
  // rather than closing over tasks.data, so handleUpdateRow's identity
  // survives every task-data change — otherwise DataTable rebuilds its
  // columns (remounting every cell) on every unrelated edit settling.
  const handleUpdateRow = useCallback(
    (id: string, patch: TaskPatch) => {
      const current = queryClient.getQueryData<Task[]>(queryKeys.tasks.list(projectId))
      celebrateIfNewlyDone(current?.find((task) => task.id === id), patch)
      updateMutate({ id, patch })
    },
    [queryClient, projectId, updateMutate],
  )
  const handleRemoveRow = useCallback((id: string) => deleteMutate(id), [deleteMutate])

  if (tasks.isPending || crew.isPending) return <LoadingBlock label="Loading tasks" className="h-80" />
  if (tasks.isError || crew.isError) {
    return (
      <LoadError
        message="Couldn't load tasks."
        onRetry={() => {
          if (tasks.isError) void tasks.refetch()
          if (crew.isError) void crew.refetch()
        }}
      />
    )
  }

  return (
    <DataTable
      columns={columns}
      data={tasks.data}
      getRowId={getTaskId}
      onUpdateRow={handleUpdateRow}
      onRemoveRow={handleRemoveRow}
      users={crew.data}
      title="Tasks"
      titleIcon={showTitleIcon ? <TasksIcon size={24} className="text-text-primary" /> : undefined}
      {...urlState}
      toolbarActions={
        <Button
          variant="outline"
          size="sm"
          disabled={createTask.isPending}
          onClick={() =>
            createTask.mutate({
              name: "Untitled task",
              project: projectId ?? "",
              assignee: "",
              due: "",
              status: "todo",
              priority: "medium",
            })
          }
          // Outlined buttons fill with their border color on hover; in dark
          // mode, highlight with dark text (white on it is only ~3:1)
          className="rounded-full border border-text-primary bg-transparent px-4 text-text-primary hover:bg-text-primary hover:text-background dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"
        >
          New Task
        </Button>
      }
    />
  )
}
