import { useMutation, useQueryClient } from "@tanstack/react-query"
import { api } from "./client"
import { failure, projectCrewKey } from "./queries"
import type { components } from "./schema"

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
