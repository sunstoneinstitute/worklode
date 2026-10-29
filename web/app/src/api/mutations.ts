import { useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { api } from "./client"
import { type ListResource, useCreateInList, useRemoveFromList, useUpdateInList } from "./list-mutations"
import { type BodySubject, type BodyTarget, HttpError, failure, projectCrewKey, queryKeys } from "./queries"
import type { components } from "./schema"
import type { Idea, ProjectDocument, Task } from "./types"

type AddCrewMemberInput = components["schemas"]["AddCrewMemberInput"]

/** Adds a member to a project's Crew, then refetches the roster */
export function useAddCrewMember(projectId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (input: AddCrewMemberInput) => {
      const { data, error, response } = await api.POST("/api/v1/projects/{id}/participants", {
        params: { path: { id: projectId } },
        body: input,
      })
      if (error) throw failure(error, response)
      return data
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: projectCrewKey(projectId) }),
  })
}

/**
 * Removes a member from a project's Crew, then refetches the roster. A
 * refused removal throws an HttpError carrying the server's message, which
 * names any open work the member still owns (WL-REQ-1802).
 */
export function useRemoveCrewMember(projectId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (actor: string) => {
      const { error, response } = await api.DELETE("/api/v1/projects/{id}/participants/{actor}", {
        params: { path: { id: projectId, actor } },
      })
      if (error) throw failure(error, response)
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: projectCrewKey(projectId) }),
  })
}

/**
 * Requests a handoff of the project's lead (WL-REQ-1605), then refetches the
 * roster. Accepting and authorizing have no API route: they are web-session acts.
 */
export function useRequestLeadHandoff(projectId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (to: string) => {
      const { data, error, response } = await api.POST("/api/v1/projects/{id}/lead-handoff", {
        params: { path: { id: projectId } },
        body: { to },
      })
      if (error) throw failure(error, response)
      return data
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: projectCrewKey(projectId) }),
  })
}

/** Cancels the project's pending lead handoff, then refetches the roster */
export function useCancelLeadHandoff(projectId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      const { error, response } = await api.DELETE("/api/v1/projects/{id}/lead-handoff", {
        params: { path: { id: projectId } },
      })
      if (error) throw failure(error, response)
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: projectCrewKey(projectId) }),
  })
}

// Task and project writes. The optimistic behavior lives in
// list-mutations.ts; each resource only says where its data lives.

const TASKS: ListResource = {
  rootKey: queryKeys.tasks.all,
  listKey: queryKeys.tasks.all,
  noun: "task",
  // A task's state and due date feed the project summary
  alsoInvalidate: [["projects", "summary"]],
}

export function useCreateTask() {
  return useCreateInList(TASKS, async (input: components["schemas"]["CreateTaskInput"]) => {
    const { data, error, response } = await api.POST("/api/v1/tasks", { body: input })
    if (error) throw failure(error, response)
    return data
  })
}

/**
 * Edits a task. The assignee does not go through EditTaskInput: it calls
 * /assign or /unassign, which record the assignment event (an empty
 * assignee clears it).
 */
export function useUpdateTask() {
  return useUpdateInList<Task>(TASKS, async (id, patch) => {
    const { assignee, ...fields } = patch
    const path = { id }
    if (Object.keys(fields).length > 0) {
      const { error, response } = await api.PATCH("/api/v1/tasks/{id}", { params: { path }, body: fields })
      if (error) throw failure(error, response)
    }
    if (assignee !== undefined) {
      const { error, response } = assignee
        ? await api.POST("/api/v1/tasks/{id}/assign", { params: { path }, body: { assignee } })
        : await api.POST("/api/v1/tasks/{id}/unassign", { params: { path } })
      if (error) throw failure(error, response)
    }
  })
}

export function useDeleteTask() {
  return useRemoveFromList<Task>(TASKS, async (id) => {
    const { error, response } = await api.DELETE("/api/v1/tasks/{id}", {
      params: { path: { id } },
      body: { justification: "deleted from the task list" },
    })
    if (error) throw failure(error, response)
  })
}

// The documents register (WL-REQ-1798)
const DOCUMENTS: ListResource = {
  rootKey: queryKeys.documents.all,
  listKey: queryKeys.documents.all,
  noun: "document",
  alsoInvalidate: [["projects", "summary"]],
}

export function useCreateProjectDocument(projectId: string) {
  return useCreateInList(DOCUMENTS, async (input: components["schemas"]["CreateProjectDocumentInput"]) => {
    const { data, error, response } = await api.POST("/api/v1/projects/{id}/documents", {
      params: { path: { id: projectId } },
      body: input,
    })
    if (error) throw failure(error, response)
    return data
  })
}

/** Adds a source to a shared idea; the server sets its category to sources */
export function useCreateIdeaSource(ideaId: string) {
  return useCreateInList(DOCUMENTS, async (input: components["schemas"]["CreateProjectDocumentInput"]) => {
    const { data, error, response } = await api.POST("/api/v1/ideas/{id}/sources", {
      params: { path: { id: ideaId } },
      body: input,
    })
    if (error) throw failure(error, response)
    return data
  })
}

export function useUpdateDocument() {
  return useUpdateInList<ProjectDocument>(DOCUMENTS, async (id, patch) => {
    const { error, response } = await api.PATCH("/api/v1/documents/{id}", { params: { path: { id } }, body: patch })
    if (error) throw failure(error, response)
  })
}

export function useDeleteDocument() {
  return useRemoveFromList<ProjectDocument>(DOCUMENTS, async (id) => {
    const { error, response } = await api.DELETE("/api/v1/documents/{id}", { params: { path: { id } } })
    if (error) throw failure(error, response)
  })
}

// The API has no project delete yet. A delete mutation built on this
// resource refetches tasks and documents too, whatever the server did
// with them.
export const PROJECTS: ListResource = {
  rootKey: queryKeys.projects.all,
  listKey: queryKeys.projects.list,
  detailKey: queryKeys.projects.detail,
  noun: "project",
  alsoInvalidate: [["tasks"], ["documents"]],
}

// Ideas (WL-REQ-1799). The form reports its own errors (WL-REQ-1811), so
// create and update are plain mutations that refetch the ideas after.
const IDEAS: ListResource = {
  rootKey: queryKeys.ideas.all,
  listKey: queryKeys.ideas.list,
  detailKey: queryKeys.ideas.detail,
  noun: "idea",
}

export function useCreateIdea() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationKey: IDEAS.rootKey,
    mutationFn: async (input: components["schemas"]["CreateIdeaInput"]) => {
      const { data, error, response } = await api.POST("/api/v1/ideas", { body: input })
      if (error) throw failure(error, response)
      return data
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: IDEAS.listKey }),
  })
}

export function useUpdateIdea() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationKey: IDEAS.rootKey,
    mutationFn: async ({ id, patch }: { id: string; patch: components["schemas"]["PatchIdeaInput"] }) => {
      const { data, error, response } = await api.PATCH("/api/v1/ideas/{id}", { params: { path: { id } }, body: patch })
      if (error) throw failure(error, response)
      return data
    },
    // The Intake page shows the idea it edits, so its detail refetches too
    onSuccess: () => queryClient.invalidateQueries({ queryKey: IDEAS.rootKey }),
  })
}

export function useDeleteIdea() {
  return useRemoveFromList<Idea>(IDEAS, async (id) => {
    const { error, response } = await api.DELETE("/api/v1/ideas/{id}", { params: { path: { id } } })
    if (error) throw failure(error, response)
  })
}

/** Shares an idea into intake. A 409 (not configured) shows the server's message. */
export function useShareIdea() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationKey: IDEAS.rootKey,
    mutationFn: async (id: string) => {
      const { data, error, response } = await api.POST("/api/v1/ideas/{id}/share", { params: { path: { id } } })
      if (error) throw failure(error, response)
      return data
    },
    onError: (error) =>
      toast.error(error instanceof HttpError && error.status === 409 ? error.message : "Couldn't share the idea"),
    onSettled: () => queryClient.invalidateQueries({ queryKey: IDEAS.rootKey }),
  })
}

/**
 * Records a shared idea's intake fields, shown at once and rolled back if
 * the server refuses. The route replaces both fields, so `intake` is whole.
 */
export function useSaveIntake() {
  return useUpdateInList<Idea>(IDEAS, async (id, { intake }) => {
    const { error, response } = await api.PATCH("/api/v1/ideas/{id}/intake", {
      params: { path: { id } },
      body: { pre_research_authorized: intake?.pre_research_authorized ?? false, decision: intake?.decision ?? "" },
    })
    if (error) throw failure(error, response)
  })
}

// Body locks (WL-REQ-1868). `id` is the doc id, the numeric rule id or the task id.

export type LockKind = "doc" | "rule" | "task"
export type BodyLock = components["schemas"]["BodyLock"]
export type LockLost = components["schemas"]["LockLost"]
type ErrorResponse = components["schemas"]["ErrorResponse"]

/** Takes a body lock: a new session, or (409) the lock another holder has */
export async function takeBodyLock(kind: LockKind, id: string): Promise<{ ok: true; lock: BodyLock } | { ok: false; lock: BodyLock }> {
  const { data, error, response } = await api.POST("/api/v1/locks/{kind}/{id}", { params: { path: { kind, id } } })
  if (data) return { ok: true, lock: data }
  if (response.status === 409) return { ok: false, lock: error as BodyLock }
  throw failure(error as ErrorResponse, response)
}

/** Checks a session in; a 409 means the session has ended */
export async function checkInBodyLock(
  kind: LockKind,
  id: string,
  body: components["schemas"]["LockCheckIn"],
): Promise<{ ok: true } | { ok: false; lost: LockLost }> {
  const { data, error, response } = await api.PUT("/api/v1/locks/{kind}/{id}", { params: { path: { kind, id } }, body })
  if (data) return { ok: true }
  if (response.status === 409) return { ok: false, lost: error as LockLost }
  throw failure(error as ErrorResponse, response)
}

/**
 * Releases one session. `keepalive` lets the request outlive the page on
 * pagehide; it goes through `api`, so it keeps the WL-REQ-1770 headers,
 * which `navigator.sendBeacon` cannot send.
 */
export async function releaseBodyLock(kind: LockKind, id: string, session: string, { keepalive = false } = {}) {
  const { error, response } = await api.DELETE("/api/v1/locks/{kind}/{id}", {
    params: { path: { kind, id }, query: { session } },
    keepalive,
  })
  if (error) throw failure(error, response)
}

/** Ends every session on a lock; the subject's owner or an admin only */
export async function unlockBody(kind: LockKind, id: string) {
  const { error, response } = await api.POST("/api/v1/locks/{kind}/{id}/unlock", { params: { path: { kind, id } } })
  if (error) throw failure(error, response)
}

// Body saves (WL-REQ-1868): blocks, the body_sha256 they were loaded
// against, and the lock session

export type BodyConflict = components["schemas"]["BodyConflict"]
export type BodySave = { blocks: components["schemas"]["EditorBlock"][]; if_body_sha256: string; lock_session: string }
export type BodySaveResult = { ok: true; body: string } | { ok: false; conflict: BodyConflict } | { ok: false; lock: BodyLock }

/** Saves a body; a 409 is the newer stored text (BodyConflict) or the lock (BodyLock) */
export async function saveBody(target: BodyTarget, subject: BodySubject, input: BodySave): Promise<BodySaveResult> {
  const answer =
    target.kind === "rule"
      ? await api.PUT("/api/v1/rules/{id}", { params: { path: { id: target.ref } }, body: { ...input, heading: subject.heading } })
      : target.kind === "task"
        ? await api.PATCH("/api/v1/tasks/{id}", { params: { path: { id: target.ref } }, body: input })
        : await api.PUT("/api/v1/docs/{id}/body", { params: { path: { id: subject.lockId } }, body: input })
  const { data, error, response } = answer
  if (data) return { ok: true, body: data.body ?? "" }
  if (response.status === 409) {
    if (error && "body_sha256" in error) return { ok: false, conflict: error as BodyConflict }
    return { ok: false, lock: error as BodyLock }
  }
  throw failure(error as ErrorResponse, response)
}
