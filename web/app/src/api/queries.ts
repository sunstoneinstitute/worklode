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
  /** A 422's per-field messages, keyed by wire name (WL-REQ-1811) */
  fields?: Record<string, string>

  constructor(status: number, message: string, fields?: Record<string, string>) {
    super(message)
    this.name = "HttpError"
    this.status = status
    this.fields = fields
  }
}

/** Thrown after starting the login redirect, so no error page renders meanwhile. A 4xx, so never retried. */
export class LoginRedirect extends HttpError {
  constructor() {
    super(401, "login required")
  }
}

export function failure(error: ErrorResponse, response: Response) {
  return new HttpError(response.status, error.error ?? `${response.status} ${response.statusText}`.trim(), error.fields)
}

/**
 * Query keys follow the URL path, so everything under /api/v1/tasks is
 * `["tasks", ...]` and one invalidateQueries({ queryKey: ["tasks"] })
 * refreshes every task list, whatever its filter.
 */
export const queryKeys = {
  me: ["whoami"] as const,
  crew: ["crew"] as const,
  projects: {
    all: ["projects"] as const,
    list: ["projects"] as const,
    detail: (id: string) => ["projects", "detail", id] as const,
    crew: (id: string) => ["projects", "crew", id] as const,
    summary: (id: string) => ["projects", "summary", id] as const,
  },
  tasks: {
    all: ["tasks"] as const,
    list: (project?: string) => ["tasks", { project: project ?? null }] as const,
  },
  documents: {
    all: ["documents"] as const,
    list: (project: string) => ["documents", { project }] as const,
  },
  ideas: {
    all: ["ideas"] as const,
    list: ["ideas", "list"] as const,
    detail: (id: string) => ["ideas", "detail", id] as const,
  },
  docs: (project: string, kind: string) => ["docs", { project, kind }] as const,
  rules: (project: string) => ["rules", { project }] as const,
}

export const meQueryOptions = queryOptions({
  queryKey: queryKeys.me,
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
    queryKey: queryKeys.projects.list,
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/projects")
      if (error) throw failure(error, response)
      return data.projects ?? []
    },
  })
}

export function useProject(id: string) {
  return useQuery({
    queryKey: queryKeys.projects.detail(id),
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/projects/{id}", { params: { path: { id } } })
      if (error) throw failure(error, response)
      return data
    },
  })
}

export function useCrew() {
  return useQuery({
    queryKey: queryKeys.crew,
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/crew")
      if (error) throw failure(error, response)
      return data.crew ?? []
    },
  })
}

export const projectCrewKey = queryKeys.projects.crew

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

/** Tasks, optionally those of one project (`GET /api/v1/tasks?project=`) */
export function useTasks({ project }: { project?: string } = {}) {
  return useQuery({
    queryKey: queryKeys.tasks.list(project),
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/tasks", { params: { query: { project } } })
      if (error) throw failure(error, response)
      return data.tasks ?? []
    },
  })
}

/** What is late, what awaits review and each Crew member's load (WL-REQ-1797) */
export function useProjectSummary(id: string) {
  return useQuery({
    queryKey: queryKeys.projects.summary(id),
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/projects/{id}/summary", { params: { path: { id } } })
      if (error) throw failure(error, response)
      return data
    },
  })
}

/** A project's documents register: its files, datasets and links (WL-REQ-1798) */
export function useProjectDocuments(id: string) {
  return useQuery({
    queryKey: queryKeys.documents.list(id),
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/projects/{id}/documents", { params: { path: { id } } })
      if (error) throw failure(error, response)
      return data.documents ?? []
    },
  })
}

/** A project's backbone documents of one kind, "spec" or "plan" (WL-REQ-1805) */
export function useProjectDocs(id: string, kind: "spec" | "plan") {
  return useQuery({
    queryKey: queryKeys.docs(id, kind),
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/docs", { params: { query: { project: id, kind } } })
      if (error) throw failure(error, response)
      return data.docs ?? []
    },
  })
}

/** A project's rules (WL-REQ-1805) */
export function useProjectRules(id: string) {
  return useQuery({
    queryKey: queryKeys.rules(id),
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/rules", { params: { query: { project: id } } })
      if (error) throw failure(error, response)
      return data ?? []
    },
  })
}

/** The caller's private ideas and every shared idea (WL-REQ-1799) */
export function useIdeas() {
  return useQuery({
    queryKey: queryKeys.ideas.list,
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/ideas")
      if (error) throw failure(error, response)
      return data.ideas ?? []
    },
  })
}

/**
 * One idea, for its edit form. Never cached past the form, so the form opens
 * with a fresh read each time (WL-REQ-1810).
 */
export function useIdea(id: string) {
  return useQuery({
    queryKey: queryKeys.ideas.detail(id),
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/ideas/{id}", { params: { path: { id } } })
      if (error) throw failure(error, response)
      return data
    },
    gcTime: 0,
    refetchOnWindowFocus: false,
  })
}
