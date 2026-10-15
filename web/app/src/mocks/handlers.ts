import { HttpResponse, delay, http } from "msw"
import type { components } from "@/api/schema"
import { type CrewMember, db, projectDetail } from "./db"
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

  http.get("/api/v1/crew", async () => {
    await delay(LATENCY_MS)
    const crew: Ok<"/api/v1/crew", "get"> = { crew: db.crew }
    return HttpResponse.json(crew)
  }),

  http.get("/api/v1/projects/:id/participants", async ({ params }) => {
    await delay(LATENCY_MS)
    const members = db.participants[String(params.id)]
    if (!members) return HttpResponse.json({ error: "not found" }, { status: 404 })
    const body: Ok<"/api/v1/projects/{id}/participants", "get"> = { participants: members }
    return HttpResponse.json(body)
  }),

  http.post("/api/v1/projects/:id/participants", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const members = db.participants[String(params.id)]
    if (!members) return HttpResponse.json({ error: "not found" }, { status: 404 })
    const input = (await request.json()) as components["schemas"]["AddCrewMemberInput"]
    const actor = db.crew.find((c) => c.actor === input.actor)
    if (!actor) return HttpResponse.json({ error: "not found" }, { status: 404 })
    const member: CrewMember = {
      actor: actor.actor,
      display_name: actor.display_name,
      roles: [input.role || "member"],
      lead: input.lead ?? false,
    }
    members.push(member)
    return HttpResponse.json(member, { status: 201 })
  }),

  http.delete("/api/v1/projects/:id/participants/:actor", async ({ params }) => {
    await delay(LATENCY_MS)
    const members = db.participants[String(params.id)]
    const i = members?.findIndex((m) => m.actor === params.actor) ?? -1
    if (!members || i < 0) return HttpResponse.json({ error: "not found" }, { status: 404 })
    if (members[i].lead) {
      return HttpResponse.json(
        { error: "project lead cannot be removed; lead handoff is not implemented: invalid input" },
        { status: 422 },
      )
    }
    members.splice(i, 1)
    return new HttpResponse(null, { status: 204 })
  }),
]
