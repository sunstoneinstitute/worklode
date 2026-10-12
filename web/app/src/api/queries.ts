import { queryOptions, useQuery, useSuspenseQuery } from "@tanstack/react-query"
import { apiFetch } from "./mock-client"
import type { CrewMember, Meeting, MockProject, Task } from "./mock-types"

/**
 * Query keys follow the URL path, so everything under /api/tasks is
 * `["tasks", …]` and one invalidateQueries({ queryKey: ["tasks"] })
 * refreshes every task list, whatever its filter.
 */
export const queryKeys = {
  me: ["me"] as const,
  crew: ["crew"] as const,
  projects: ["projects"] as const,
  tasks: {
    all: ["tasks"] as const,
    list: (projectId?: string) => ["tasks", { projectId: projectId ?? null }] as const,
  },
  meetings: ["meetings"] as const,
}

export const meQueryOptions = queryOptions({
  queryKey: queryKeys.me,
  queryFn: () => apiFetch<CrewMember>("/api/me"),
})

/** The signed-in user. The root route's loader fetches it before any page renders. */
export function useMe() {
  return useSuspenseQuery(meQueryOptions).data
}

export function useCrew() {
  return useQuery({ queryKey: queryKeys.crew, queryFn: () => apiFetch<CrewMember[]>("/api/crew") })
}

export function useProjects() {
  return useQuery({ queryKey: queryKeys.projects, queryFn: () => apiFetch<MockProject[]>("/api/projects") })
}

/** All tasks, or one project's */
export function useTasks({ projectId }: { projectId?: string } = {}) {
  return useQuery({
    queryKey: queryKeys.tasks.list(projectId),
    queryFn: () =>
      apiFetch<Task[]>(
        projectId ? `/api/tasks?projectId=${encodeURIComponent(projectId)}` : "/api/tasks",
      ),
  })
}

export function useMeetings() {
  return useQuery({ queryKey: queryKeys.meetings, queryFn: () => apiFetch<Meeting[]>("/api/meetings") })
}
