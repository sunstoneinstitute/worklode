import { QueryClientProvider } from "@tanstack/react-query"
import { act, renderHook, waitFor } from "@testing-library/react"
import { http, HttpResponse } from "msw"
import type { ReactNode } from "react"
import { describe, expect, it } from "vitest"
import { db } from "@/mocks/db"
import { server } from "@/mocks/node"
import { useDeleteTask, useUpdateTask } from "./mutations"
import { queryKeys, useTasks } from "./queries"
import { createQueryClient } from "./query-client"
import type { Task } from "./types"

const T1: Task = { id: "WL-1", project: "p1", title: "Draft", state: "in_progress", assignee: "stig", due: "" }
const T2: Task = { id: "WL-2", project: "p1", title: "Audit", state: "todo", assignee: "", due: "" }

// Seeds the cache directly (no component mounted), so the refetch after a
// mutation settles has no active query and the cache shows what it left.
function setup() {
  const queryClient = createQueryClient({ test: true })
  const all = queryKeys.tasks.list()
  const p1 = queryKeys.tasks.list("p1")
  queryClient.setQueryData(all, [T1, T2])
  queryClient.setQueryData(p1, [T1, T2])
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
  return { queryClient, wrapper, all, p1 }
}

const states = (data: Task[] | undefined) => data?.map((t) => t.state)

describe("task mutations", () => {
  it("an edit shows at once in every cached list, before the server answers", async () => {
    const { queryClient, wrapper, all, p1 } = setup()
    server.use(http.patch("/api/v1/tasks/:id", () => new Promise(() => {})))
    const { result } = renderHook(useUpdateTask, { wrapper })

    act(() => result.current.mutate({ id: "WL-1", patch: { state: "done" } }))

    await waitFor(() => expect(queryClient.getQueryData<Task[]>(all)?.[0].state).toBe("done"))
    expect(queryClient.getQueryData<Task[]>(p1)?.[0].state).toBe("done")
  })

  it("an edit the server refuses rolls back", async () => {
    const { queryClient, wrapper, all } = setup()
    const { result } = renderHook(useUpdateTask, { wrapper })

    act(() => result.current.mutate({ id: "WL-1", patch: { state: "bogus" } }))

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(states(queryClient.getQueryData<Task[]>(all))).toEqual(["in_progress", "todo"])
  })

  it("an assignee patch calls assign, and an empty one unassign", async () => {
    const { wrapper } = setup()
    const { result } = renderHook(useUpdateTask, { wrapper })

    act(() => result.current.mutate({ id: "WL-2", patch: { assignee: "ada" } }))
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(db.tasks.find((t) => t.id === "WL-2")?.assignee).toBe("ada")

    act(() => result.current.mutate({ id: "WL-2", patch: { assignee: "" } }))
    await waitFor(() => expect(db.tasks.find((t) => t.id === "WL-2")?.assignee).toBe(""))
  })

  it("refetches once, after the last overlapping write", async () => {
    const { queryClient, wrapper, all } = setup()
    let release: () => void = () => {}
    const gate = new Promise<void>((resolve) => (release = resolve))
    server.use(
      http.patch("/api/v1/tasks/WL-1", async () => {
        await gate
        return HttpResponse.json(T1)
      }),
    )
    const a = renderHook(useUpdateTask, { wrapper })
    const b = renderHook(useUpdateTask, { wrapper })
    act(() => a.result.current.mutate({ id: "WL-1", patch: { title: "slow" } }))
    act(() => b.result.current.mutate({ id: "WL-2", patch: { title: "fast" } }))

    await waitFor(() => expect(b.result.current.isSuccess).toBe(true))
    expect(queryClient.getQueryState(all)?.isInvalidated).toBe(false)

    release()
    await waitFor(() => expect(a.result.current.isSuccess).toBe(true))
    expect(queryClient.getQueryState(all)?.isInvalidated).toBe(true)
  })

  it("a delete removes the row at once and sends the justification", async () => {
    const { queryClient, wrapper, all } = setup()
    let body: unknown
    server.use(
      http.delete("/api/v1/tasks/:id", async ({ request }) => {
        body = await request.json()
        return new Promise(() => {})
      }),
    )
    const { result } = renderHook(useDeleteTask, { wrapper })

    act(() => result.current.mutate("WL-1"))

    await waitFor(() => expect(queryClient.getQueryData<Task[]>(all)?.map((t) => t.id)).toEqual(["WL-2"]))
    await waitFor(() => expect(body).toEqual({ justification: "deleted from the task list" }))
  })

  it("editing a task invalidates a cached project summary", async () => {
    const { queryClient, wrapper } = setup()
    const summary = queryKeys.projects.summary("p1")
    queryClient.setQueryData(summary, {})
    const { result } = renderHook(useUpdateTask, { wrapper })

    act(() => result.current.mutate({ id: "WL-1", patch: { state: "done" } }))

    await waitFor(() => expect(queryClient.getQueryState(summary)?.isInvalidated).toBe(true))
  })
})

describe("useTasks", () => {
  it("lists a project's tasks under its own key", async () => {
    const queryClient = createQueryClient({ test: true })
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )
    const { result } = renderHook(() => useTasks({ project: "p2" }), { wrapper })

    await waitFor(() => expect(result.current.data?.map((t) => t.id)).toEqual(["DP-1"]))
    expect(queryClient.getQueryData(["tasks", { project: "p2" }])).toBeDefined()
  })
})
