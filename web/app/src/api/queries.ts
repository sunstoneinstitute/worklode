import { queryOptions, useQuery, useSuspenseQuery } from "@tanstack/react-query"
import { api } from "./client"
import type { ErrorResponse } from "./types"

/** Full-page navigation, behind an object so tests can spy on it. */
export const browser = {
  assign: (url: string) => window.location.assign(url),
}

/** A non-2xx answer, carrying its status so the query client can decide on a retry. */
export class HttpError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = "HttpError"
    this.status = status
  }
}

/** Thrown after starting the login redirect, so no error page renders meanwhile. A 4xx, so never retried. */
export class LoginRedirect extends HttpError {
  constructor() {
    super(401, "login required")
  }
}

export function failure(error: ErrorResponse, response: Response) {
  return new HttpError(response.status, error.error ?? `${response.status} ${response.statusText}`.trim())
}

export const meQueryOptions = queryOptions({
  queryKey: ["whoami"],
  queryFn: async () => {
    const { data, error, response } = await api.GET("/api/v1/whoami")
    if (response.status === 401) {
      // WL-REQ-1308: no session sends the browser to log in and come back here
      browser.assign(`/auth/login?next=${encodeURIComponent(location.pathname + location.search)}`)
      throw new LoginRedirect()
    }
    if (error) throw failure(error, response)
    return data
  },
})

/** The signed-in actor. The root route's loader fetches it before any page renders. */
export function useMe() {
  return useSuspenseQuery(meQueryOptions).data
}

export function useProjects() {
  return useQuery({
    queryKey: ["projects"],
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/projects")
      if (error) throw failure(error, response)
      return data.projects ?? []
    },
  })
}

export function useProject(id: string) {
  return useQuery({
    queryKey: ["projects", "detail", id],
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/projects/{id}", { params: { path: { id } } })
      if (error) throw failure(error, response)
      return data
    },
  })
}

export function useCrew() {
  return useQuery({
    queryKey: ["crew"],
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/crew")
      if (error) throw failure(error, response)
      return data.crew ?? []
    },
  })
}

export const projectCrewKey = (id: string) => ["projects", "crew", id]

/** A project's Crew roster */
export function useProjectCrew(id: string) {
  return useQuery({
    queryKey: projectCrewKey(id),
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/projects/{id}/participants", {
        params: { path: { id } },
      })
      if (error) throw failure(error, response)
      return data.participants ?? []
    },
  })
}
