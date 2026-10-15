import { HttpResponse, delay, http } from "msw"
import { db, projectDetail } from "./db"
import type { Ok } from "./typed"

// Visible loading states in development; instant in tests
const LATENCY_MS = import.meta.env.MODE === "test" ? 0 : 150

export const handlers = [
  http.get("/api/v1/whoami", async () => {
    await delay(LATENCY_MS)
    const me: Ok<"/api/v1/whoami", "get"> = db.me
    return HttpResponse.json(me)
  }),

  http.get("/api/v1/projects", async () => {
    await delay(LATENCY_MS)
    const projects: Ok<"/api/v1/projects", "get"> = { projects: db.projects }
    return HttpResponse.json(projects)
  }),

  http.get("/api/v1/projects/:id", async ({ params }) => {
    await delay(LATENCY_MS)
    const project = projectDetail(String(params.id))
    if (!project) return HttpResponse.json({ error: "project not found" }, { status: 404 })
    return HttpResponse.json(project)
  }),
]
