import { describe, expect, it } from "vitest"
import { apiFetch } from "@/api/mock-client"
import type { Task } from "@/api/mock-types"
import { db } from "./db"

describe("mock API", () => {
  it("GET /api/me is the signed-in user", async () => {
    await expect(apiFetch("/api/me")).resolves.toMatchObject({ id: "u1", name: "Claudicia" })
  })

  it("GET /api/crew lists everyone", async () => {
    const crew = await apiFetch<unknown[]>("/api/crew")
    expect(crew).toHaveLength(4)
  })

  it("GET /api/projects lists projects with quick-link fields", async () => {
    const [first] = await apiFetch<{ id: string; progress: number }[]>("/api/projects")
    expect(first).toMatchObject({ id: "the-grid", progress: 55 })
  })

  it("GET /api/tasks lists all tasks, or one project's", async () => {
    expect(await apiFetch<Task[]>("/api/tasks")).toHaveLength(4)
    const grid = await apiFetch<Task[]>("/api/tasks?projectId=the-grid")
    expect(grid.map((task) => task.id)).toEqual(["t1", "t3"])
  })

  it("POST /api/tasks creates a task with a new id", async () => {
    const created = await apiFetch<Task>("/api/tasks", {
      method: "POST",
      body: { name: "New", project: "", assignee: "", due: "", status: "todo", priority: "medium" },
    })
    expect(created.id).toBe("t5")
    expect(db.tasks.at(-1)).toEqual(created)
  })

  it("PATCH /api/tasks/:id changes only the given fields", async () => {
    const updated = await apiFetch<Task>("/api/tasks/t2", { method: "PATCH", body: { status: "done" } })
    expect(updated).toMatchObject({ id: "t2", name: "Audit icon set", status: "done" })
  })

  it("PATCH rejects an unknown status with 422 and a field error", async () => {
    const error = await apiFetch("/api/tasks/t2", { method: "PATCH", body: { status: "nope" } }).catch(
      (caught) => caught,
    )
    expect(error).toMatchObject({ status: 422, errors: { status: expect.any(String) } })
  })

  it("PATCH and DELETE answer 404 for an unknown task", async () => {
    await expect(apiFetch("/api/tasks/zz", { method: "PATCH", body: {} })).rejects.toMatchObject({ status: 404 })
    await expect(apiFetch("/api/tasks/zz", { method: "DELETE" })).rejects.toMatchObject({ status: 404 })
  })

  it("DELETE /api/tasks/:id removes it", async () => {
    await apiFetch("/api/tasks/t1", { method: "DELETE" })
    expect(db.tasks.map((task) => task.id)).toEqual(["t2", "t3", "t4"])
  })

  it("GET /api/meetings honors limit", async () => {
    expect(await apiFetch<unknown[]>("/api/meetings")).toHaveLength(4)
    expect(await apiFetch<unknown[]>("/api/meetings?limit=2")).toHaveLength(2)
  })

  it("resets between tests", () => {
    expect(db.tasks).toHaveLength(4)
  })
})
