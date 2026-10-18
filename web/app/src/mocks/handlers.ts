import { HttpResponse, delay, http } from "msw"
import type { components } from "@/api/schema"
import { type CrewMember, type ProjectDocument, type Task, db, idea, projectDetail, visibleIdea } from "./db"
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

  http.get("/api/v1/projects/:id/summary", async ({ params }) => {
    await delay(LATENCY_MS)
    const summary = db.summaries[String(params.id)]
    if (!summary) return HttpResponse.json({ error: "project not found" }, { status: 404 })
    return HttpResponse.json(summary)
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

  http.get("/api/v1/tasks", async ({ request }) => {
    await delay(LATENCY_MS)
    const project = new URL(request.url).searchParams.get("project")
    const body: Ok<"/api/v1/tasks", "get"> = {
      tasks: db.tasks.filter((t) => !project || t.project === project),
    }
    return HttpResponse.json(body)
  }),

  http.post("/api/v1/tasks", async ({ request }) => {
    await delay(LATENCY_MS)
    const input = (await request.json()) as components["schemas"]["CreateTaskInput"]
    const project = db.projects.find((p) => p.id === input.project)
    if (!project) return HttpResponse.json({ error: "project not found" }, { status: 404 })
    const n = db.tasks.filter((t) => t.project === project.id).length + 1
    const task: Task = {
      id: `${project.key}-${n}`,
      project: project.id,
      title: input.title ?? "",
      state: "ready",
      priority: input.priority ?? "medium",
      assignee: "",
      due: input.due ?? "",
    }
    db.tasks.push(task)
    return HttpResponse.json(task, { status: 201 })
  }),

  http.patch("/api/v1/tasks/:id", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const task = db.tasks.find((t) => t.id === params.id)
    if (!task) return HttpResponse.json({ error: "task not found" }, { status: 404 })
    const input = (await request.json()) as components["schemas"]["EditTaskInput"]
    const states = ["draft", "ready", "in_progress", "in_review", "merged", "deployed_dev", "deployed_prod", "released", "abandoned"]
    if (input.state && !states.includes(input.state)) {
      return HttpResponse.json({ error: `unknown state ${input.state}` }, { status: 400 })
    }
    for (const [key, value] of Object.entries(input)) {
      if (value !== null && value !== undefined && key in task) Object.assign(task, { [key]: value })
    }
    return HttpResponse.json(task)
  }),

  http.delete("/api/v1/tasks/:id", async ({ params }) => {
    await delay(LATENCY_MS)
    const i = db.tasks.findIndex((t) => t.id === params.id)
    if (i < 0) return HttpResponse.json({ error: "task not found" }, { status: 404 })
    const [task] = db.tasks.splice(i, 1)
    return HttpResponse.json(task)
  }),

  http.post("/api/v1/tasks/:id/assign", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const task = db.tasks.find((t) => t.id === params.id)
    if (!task) return HttpResponse.json({ error: "task not found" }, { status: 404 })
    const input = (await request.json()) as components["schemas"]["AssignInput"]
    task.assignee = input.assignee ?? ""
    return HttpResponse.json(task)
  }),

  http.post("/api/v1/tasks/:id/unassign", async ({ params }) => {
    await delay(LATENCY_MS)
    const task = db.tasks.find((t) => t.id === params.id)
    if (!task) return HttpResponse.json({ error: "task not found" }, { status: 404 })
    task.assignee = ""
    return HttpResponse.json(task)
  }),

  http.get("/api/v1/docs", async ({ request }) => {
    await delay(LATENCY_MS)
    const query = new URL(request.url).searchParams
    const project = query.get("project")
    const kind = query.get("kind")
    const body: Ok<"/api/v1/docs", "get"> = {
      docs: db.docs.filter((d) => (!project || d.project === project) && (!kind || d.kind === kind)),
    }
    return HttpResponse.json(body)
  }),

  http.get("/api/v1/rules", async ({ request }) => {
    await delay(LATENCY_MS)
    const project = new URL(request.url).searchParams.get("project")
    const body: Ok<"/api/v1/rules", "get"> = db.rules.filter((r) => !project || r.project === project)
    return HttpResponse.json(body)
  }),

  http.get("/api/v1/projects/:id/documents", async ({ params }) => {
    await delay(LATENCY_MS)
    const id = String(params.id)
    if (!projectDetail(id)) return HttpResponse.json({ error: "project not found" }, { status: 404 })
    const body: Ok<"/api/v1/projects/{id}/documents", "get"> = { documents: db.documents.filter((d) => d.project === id) }
    return HttpResponse.json(body)
  }),

  http.post("/api/v1/projects/:id/documents", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const id = String(params.id)
    if (!projectDetail(id)) return HttpResponse.json({ error: "project not found" }, { status: 404 })
    const input = (await request.json()) as components["schemas"]["CreateProjectDocumentInput"]
    const document: ProjectDocument = {
      source: "",
      milestone: "",
      due: "",
      date: "",
      url: "",
      participants: [],
      ...input,
      id: `doc_${String(db.documents.length + 1).padStart(12, "0")}`,
      project: id,
    }
    db.documents.push(document)
    return HttpResponse.json(document, { status: 201 })
  }),

  http.patch("/api/v1/documents/:id", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const document = db.documents.find((d) => d.id === params.id)
    if (!document) return HttpResponse.json({ error: "document not found" }, { status: 404 })
    const input = (await request.json()) as components["schemas"]["PatchProjectDocumentInput"]
    for (const [key, value] of Object.entries(input)) {
      if (value !== null && value !== undefined) Object.assign(document, { [key]: value })
    }
    const body: Ok<"/api/v1/documents/{id}", "patch"> = document
    return HttpResponse.json(body)
  }),

  http.delete("/api/v1/documents/:id", async ({ params }) => {
    await delay(LATENCY_MS)
    const i = db.documents.findIndex((d) => d.id === params.id)
    if (i < 0) return HttpResponse.json({ error: "document not found" }, { status: 404 })
    db.documents.splice(i, 1)
    return new HttpResponse(null, { status: 204 })
  }),

  http.get("/api/v1/ideas", async () => {
    await delay(LATENCY_MS)
    const body: Ok<"/api/v1/ideas", "get"> = { ideas: db.ideas.filter((i) => visibleIdea(i.id ?? "")) }
    return HttpResponse.json(body)
  }),

  http.get("/api/v1/ideas/:id", async ({ params }) => {
    await delay(LATENCY_MS)
    const found = visibleIdea(String(params.id))
    if (!found) return HttpResponse.json({ error: "idea not found" }, { status: 404 })
    return HttpResponse.json(found)
  }),

  http.post("/api/v1/ideas", async ({ request }) => {
    await delay(LATENCY_MS)
    const input = (await request.json()) as components["schemas"]["CreateIdeaInput"]
    if (!input.title?.trim()) {
      return HttpResponse.json({ error: "invalid idea", fields: { title: "Give your idea a title." } }, { status: 422 })
    }
    const created = idea({
      ...input,
      id: `idea_${String(db.ideas.length + 1).padStart(12, "0")}`,
      author: db.me.id ?? "",
      submitted: "2026-10-18",
    })
    db.ideas.unshift(created)
    return HttpResponse.json(created, { status: 201 })
  }),

  http.patch("/api/v1/ideas/:id", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const found = visibleIdea(String(params.id))
    if (!found) return HttpResponse.json({ error: "idea not found" }, { status: 404 })
    if (found.author !== db.me.id) return HttpResponse.json({ error: "forbidden" }, { status: 403 })
    const input = (await request.json()) as components["schemas"]["PatchIdeaInput"]
    if (input.title !== undefined && !input.title?.trim()) {
      return HttpResponse.json({ error: "invalid idea", fields: { title: "Give your idea a title." } }, { status: 422 })
    }
    for (const [key, value] of Object.entries(input)) {
      if (value !== null && value !== undefined) Object.assign(found, { [key]: value })
    }
    const body: Ok<"/api/v1/ideas/{id}", "patch"> = found
    return HttpResponse.json(body)
  }),

  http.post("/api/v1/ideas/:id/share", async ({ params }) => {
    await delay(LATENCY_MS)
    const found = visibleIdea(String(params.id))
    if (!found) return HttpResponse.json({ error: "idea not found" }, { status: 404 })
    if (found.author !== db.me.id) return HttpResponse.json({ error: "forbidden" }, { status: 403 })
    if (!db.intakeConfigured) {
      return HttpResponse.json({ error: "intake project not configured" }, { status: 409 })
    }
    if (!found.shared) Object.assign(found, { shared: "2026-10-18", status: "intake", task: "WL-99" })
    const body: Ok<"/api/v1/ideas/{id}/share", "post"> = found
    return HttpResponse.json(body)
  }),

  http.delete("/api/v1/ideas/:id", async ({ params }) => {
    await delay(LATENCY_MS)
    const found = visibleIdea(String(params.id))
    if (!found) return HttpResponse.json({ error: "idea not found" }, { status: 404 })
    if (!found.shared && found.author !== db.me.id) return HttpResponse.json({ error: "forbidden" }, { status: 403 })
    db.ideas.splice(db.ideas.indexOf(found), 1)
    return new HttpResponse(null, { status: 204 })
  }),
]
