import { type QueryClient, type QueryKey, useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { HttpError } from "./queries"

/**
 * One kind of record the API lists and edits (tasks, projects, ...). The
 * hooks below keep every cached copy of it in step with a create, edit or
 * delete: edits and deletes show at once (optimistic), undo themselves if
 * the server says no, and refetch once the last overlapping one settles.
 */
export interface ListResource {
  /** Prefix of every query key for this resource. Also the mutation key, so overlapping writes are counted together. */
  rootKey: QueryKey
  /** Prefix of every cached list (array) of this resource */
  listKey: QueryKey
  /** Key of one cached record, for resources that also fetch one at a time */
  detailKey?: (id: string) => QueryKey
  /** Used in the error toasts: "Couldn't save the <noun>" */
  noun: string
  /** Other query keys to invalidate alongside rootKey, e.g. a project summary a task edit can change */
  alsoInvalidate?: QueryKey[]
}

type WithId = { id?: string }
type Snapshot = [QueryKey, unknown][]

/** True when the mutation calling this is the only one of this resource still running */
function isLastMutation(queryClient: QueryClient, resource: ListResource) {
  return queryClient.isMutating({ mutationKey: resource.rootKey }) === 1
}

/**
 * Refetch only after the last overlapping mutation. Without this, two
 * quick edits flicker: the fast one's refetch brings back the server's
 * old value for the slow one, until the slow one lands too.
 */
function refetchIfLast(queryClient: QueryClient, resource: ListResource) {
  if (isLastMutation(queryClient, resource)) {
    return Promise.all(
      [resource.rootKey, ...(resource.alsoInvalidate ?? [])].map((queryKey) =>
        queryClient.invalidateQueries({ queryKey }),
      ),
    )
  }
}

function findCached<T extends WithId>(queryClient: QueryClient, resource: ListResource, id: string) {
  const one = resource.detailKey ? queryClient.getQueryData<T>(resource.detailKey(id)) : undefined
  if (one) return one
  for (const [, items] of queryClient.getQueriesData<T[]>({ queryKey: resource.listKey })) {
    const item = Array.isArray(items) ? items.find((candidate) => candidate.id === id) : undefined
    if (item) return item
  }
  return undefined
}

function patchCached<T extends WithId>(queryClient: QueryClient, resource: ListResource, id: string, patch: Partial<T>) {
  queryClient.setQueriesData<T[]>({ queryKey: resource.listKey }, (items) =>
    Array.isArray(items) ? items.map((item) => (item.id === id ? { ...item, ...patch } : item)) : items,
  )
  if (resource.detailKey) {
    queryClient.setQueryData<T>(resource.detailKey(id), (item) => item && { ...item, ...patch })
  }
}

/** `item`'s current values for exactly `patch`'s fields: what a rollback puts back */
function fieldsBefore<T>(item: T, patch: Partial<T>): Partial<T> {
  const before: Partial<T> = {}
  for (const key of Object.keys(patch) as (keyof T)[]) before[key] = item[key]
  return before
}

/** Creates a record. Not optimistic: the row appears after the refetch, with the server's id. */
export function useCreateInList<T, V>(resource: ListResource, create: (variables: V) => Promise<T>) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationKey: resource.rootKey,
    mutationFn: create,
    onError: () => toast.error(`Couldn't create the ${resource.noun}`),
    onSettled: () => refetchIfLast(queryClient, resource),
  })
}

/** The server's own message when it refused the write (a 400), else a generic one */
function saveMessage(error: Error, noun: string) {
  return error instanceof HttpError && error.status === 400 && error.message
    ? error.message
    : `Couldn't save the ${noun}`
}

/**
 * Changes some fields of a record. `send` makes the request(s) for the
 * patch, so a field the server saves on its own route (a task's assignee)
 * can go there.
 */
export function useUpdateInList<T extends WithId>(
  resource: ListResource,
  send: (id: string, patch: Partial<T>) => Promise<unknown>,
) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationKey: resource.rootKey,
    mutationFn: ({ id, patch }: { id: string; patch: Partial<T> }) => send(id, patch),
    onMutate: async ({ id, patch }) => {
      await queryClient.cancelQueries({ queryKey: resource.rootKey })
      // Only this record's previous values for the patched fields, so a
      // rollback can't undo another edit still in flight
      const item = findCached<T>(queryClient, resource, id)
      const previous = item ? fieldsBefore(item, patch) : undefined
      patchCached(queryClient, resource, id, patch)
      return { id, previous }
    },
    onError: (error, _variables, context) => {
      if (context?.previous) patchCached(queryClient, resource, context.id, context.previous)
      toast.error(saveMessage(error, resource.noun))
    },
    onSettled: () => refetchIfLast(queryClient, resource),
  })
}

/** Deletes a record: it leaves every cached list at once */
export function useRemoveFromList<T extends WithId>(resource: ListResource, remove: (id: string) => Promise<unknown>) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationKey: resource.rootKey,
    mutationFn: remove,
    onMutate: async (id): Promise<Snapshot> => {
      await queryClient.cancelQueries({ queryKey: resource.rootKey })
      const snapshot: Snapshot = queryClient.getQueriesData({ queryKey: resource.listKey })
      queryClient.setQueriesData<T[]>({ queryKey: resource.listKey }, (items) =>
        Array.isArray(items) ? items.filter((item) => item.id !== id) : items,
      )
      return snapshot
    },
    onSuccess: (_data, id) => {
      if (resource.detailKey) queryClient.removeQueries({ queryKey: resource.detailKey(id) })
    },
    onError: (_error, _id, snapshot) => {
      // Restoring the whole snapshot while another write is in flight would
      // undo that write too; the final refetch brings the row back instead
      if (isLastMutation(queryClient, resource)) {
        for (const [key, items] of snapshot ?? []) queryClient.setQueryData(key, items)
      }
      toast.error(`Couldn't delete the ${resource.noun}`)
    },
    onSettled: () => refetchIfLast(queryClient, resource),
  })
}
