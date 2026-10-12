import { type QueryClient, type QueryKey, useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { apiFetch } from "./mock-client"
import { queryKeys } from "./queries"
import type { NewTask, Task, TaskPatch } from "./mock-types"

// Writes. Edits and deletes are optimistic: every cached task list changes
// at once, rolls back if the server says no, and is refetched either way.

type Snapshot = [QueryKey, Task[] | undefined][]

/** Stops in-flight task fetches, saves every cached task list, applies `change` to each */
async function changeTaskLists(queryClient: QueryClient, change: (tasks: Task[]) => Task[]) {
  await queryClient.cancelQueries({ queryKey: queryKeys.tasks.all })
  const snapshot: Snapshot = queryClient.getQueriesData<Task[]>({ queryKey: queryKeys.tasks.all })
  queryClient.setQueriesData<Task[]>({ queryKey: queryKeys.tasks.all }, (tasks) => tasks && change(tasks))
  return snapshot
}

function restore(queryClient: QueryClient, snapshot: Snapshot | undefined) {
  for (const [key, tasks] of snapshot ?? []) queryClient.setQueryData(key, tasks)
}

function refetchTasks(queryClient: QueryClient) {
  return queryClient.invalidateQueries({ queryKey: queryKeys.tasks.all })
}

// Every task mutation shares this key, so isMutating({ mutationKey: TASKS_MUTATION_KEY })
// counts them together (create, update, delete).
const TASKS_MUTATION_KEY = ["tasks"]

/**
 * Refetch only when this is the last task mutation still in flight.
 * Without this, two overlapping edits flicker: a fast one's settle refetches
 * the list from the server before a slower one has landed there, replacing
 * the slower edit's still-pending optimistic value with the stale server
 * one until it, too, settles.
 */
function refetchIfLast(queryClient: QueryClient) {
  if (queryClient.isMutating({ mutationKey: TASKS_MUTATION_KEY }) === 1) return refetchTasks(queryClient)
}

function findTask(queryClient: QueryClient, id: string): Task | undefined {
  for (const [, tasks] of queryClient.getQueriesData<Task[]>({ queryKey: queryKeys.tasks.all })) {
    const task = tasks?.find((candidate) => candidate.id === id)
    if (task) return task
  }
  return undefined
}

function patchTaskLists(queryClient: QueryClient, id: string, patch: TaskPatch) {
  queryClient.setQueriesData<Task[]>({ queryKey: queryKeys.tasks.all }, (tasks) =>
    tasks?.map((task) => (task.id === id ? { ...task, ...patch } : task)),
  )
}

/** `task`'s current values for exactly `patch`'s fields — a same-shaped TaskPatch */
function fieldsBefore(task: Task, patch: TaskPatch): TaskPatch {
  const keys = Object.keys(patch) as (keyof TaskPatch)[]
  const before: TaskPatch = {}
  for (const key of keys) before[key] = task[key]
  return before
}

export function useCreateTask() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationKey: TASKS_MUTATION_KEY,
    mutationFn: (task: NewTask) => apiFetch<Task>("/api/tasks", { method: "POST", body: task }),
    onError: () => toast.error("Couldn't create the task"),
    onSettled: () => refetchIfLast(queryClient),
  })
}

export function useUpdateTask() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationKey: TASKS_MUTATION_KEY,
    mutationFn: ({ id, patch }: { id: string; patch: TaskPatch }) =>
      apiFetch<Task>(`/api/tasks/${encodeURIComponent(id)}`, { method: "PATCH", body: patch }),
    onMutate: async ({ id, patch }) => {
      await queryClient.cancelQueries({ queryKey: queryKeys.tasks.all })
      // Only this task's previous values for the fields being patched, so a
      // rollback restores just what this mutation changed — not a
      // whole-list snapshot that could undo another task mutation still in
      // flight (see refetchIfLast above for the same overlap concern).
      const task = findTask(queryClient, id)
      const previous = task ? fieldsBefore(task, patch) : undefined
      patchTaskLists(queryClient, id, patch)
      return { id, previous }
    },
    onError: (_error, _variables, context) => {
      if (context?.previous) patchTaskLists(queryClient, context.id, context.previous)
      toast.error("Couldn't save the task")
    },
    onSettled: () => refetchIfLast(queryClient),
  })
}

export function useDeleteTask() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationKey: TASKS_MUTATION_KEY,
    mutationFn: (id: string) =>
      apiFetch<void>(`/api/tasks/${encodeURIComponent(id)}`, { method: "DELETE" }),
    onMutate: (id) => changeTaskLists(queryClient, (tasks) => tasks.filter((task) => task.id !== id)),
    onError: (_error, _id, snapshot) => {
      // Only restore the whole-list snapshot when no other task mutation is
      // still in flight — otherwise this would undo that mutation's
      // optimistic change too.
      if (queryClient.isMutating({ mutationKey: TASKS_MUTATION_KEY }) === 1) restore(queryClient, snapshot)
      toast.error("Couldn't delete the task")
    },
    onSettled: () => refetchIfLast(queryClient),
  })
}
