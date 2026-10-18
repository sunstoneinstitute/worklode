import { useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { api } from "./client"
import { type ListResource, useCreateInList, useRemoveFromList, useUpdateInList } from "./list-mutations"
import { HttpError, failure, projectCrewKey, queryKeys } from "./queries"
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
    onSuccess: () => queryClient.invalidateQueries({ queryKey: IDEAS.listKey }),
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
