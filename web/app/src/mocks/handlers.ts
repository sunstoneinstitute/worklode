import { HttpResponse, delay, http } from "msw"
import type { NewTask, TaskPatch } from "@/api/types"
import { db } from "./db"

// One handler per endpoint. These double as a working example of what the
// Go server must do; docs/API.md describes the same contract.

// Visible loading states in development; instant in tests
const LATENCY_MS = import.meta.env.MODE === "test" ? 0 : 150

const TASK_STATUSES = ["todo", "in-progress", "done", "blocked"]
const TASK_PRIORITIES = ["low", "medium", "high"]

function notFound(what: string) {
  return HttpResponse.json({ message: `${what} not found` }, { status: 404 })
}

/** Field errors for a task body, or null when it's valid */
function taskErrors(task: TaskPatch): Record<string, string> | null {
  const errors: Record<string, string> = {}
  if (task.status !== undefined && !TASK_STATUSES.includes(task.status)) {
    errors.status = `Status must be one of ${TASK_STATUSES.join(", ")}`
  }
  if (task.priority !== undefined && !TASK_PRIORITIES.includes(task.priority)) {
    errors.priority = `Priority must be one of ${TASK_PRIORITIES.join(", ")}`
  }
  return Object.keys(errors).length > 0 ? errors : null
}

export const handlers = [
  http.get("/api/me", async () => {
    await delay(LATENCY_MS)
    return HttpResponse.json(db.users.find((user) => user.id === db.meId))
  }),

  http.get("/api/crew", async () => {
    await delay(LATENCY_MS)
    return HttpResponse.json(db.users)
  }),

  http.get("/api/projects", async () => {
    await delay(LATENCY_MS)
    return HttpResponse.json(db.projects)
  }),

  http.get("/api/tasks", async ({ request }) => {
    await delay(LATENCY_MS)
    const projectId = new URL(request.url).searchParams.get("projectId")
    return HttpResponse.json(projectId ? db.tasks.filter((task) => task.project === projectId) : db.tasks)
  }),

  http.post("/api/tasks", async ({ request }) => {
    await delay(LATENCY_MS)
    const body = (await request.json()) as NewTask
    const errors = taskErrors(body)
    if (errors) return HttpResponse.json({ message: "Invalid task", errors }, { status: 422 })
    const task = { ...body, id: `t${db.nextTaskNumber++}` }
    db.tasks.push(task)
    return HttpResponse.json(task, { status: 201 })
  }),

  http.patch("/api/tasks/:id", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const task = db.tasks.find((candidate) => candidate.id === params.id)
    if (!task) return notFound("Task")
    const patch = (await request.json()) as TaskPatch
    const errors = taskErrors(patch)
    if (errors) return HttpResponse.json({ message: "Invalid task", errors }, { status: 422 })
    Object.assign(task, patch, { id: task.id })
    return HttpResponse.json(task)
  }),

  http.delete("/api/tasks/:id", async ({ params }) => {
    await delay(LATENCY_MS)
    const index = db.tasks.findIndex((task) => task.id === params.id)
    if (index === -1) return notFound("Task")
    db.tasks.splice(index, 1)
    return new HttpResponse(null, { status: 204 })
  }),

  http.get("/api/meetings", async ({ request }) => {
    await delay(LATENCY_MS)
    const limit = Number(new URL(request.url).searchParams.get("limit"))
    return HttpResponse.json(limit > 0 ? db.meetings.slice(0, limit) : db.meetings)
  }),
]
