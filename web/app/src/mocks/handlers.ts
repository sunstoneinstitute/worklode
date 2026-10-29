import { HttpResponse, delay, http } from "msw"
import type { components } from "@/api/schema"
import { sha256Hex } from "@/editor/bytes"
import { type CrewMember, type LeadHandoff, type LitmusTest, type MockLock, type ProjectDocument, type Task, db, idea, projectDetail, sharedIdea, visibleIdea } from "./db"
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
    const body: Ok<"/api/v1/projects/{id}/participants", "get"> = {
      participants: members,
      lead_handoff: db.leadHandoffs[String(params.id)],
    }
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

  http.post("/api/v1/projects/:id/lead-handoff", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const id = String(params.id)
    const lead = db.participants[id]?.find((m) => m.lead)
    if (!lead) return HttpResponse.json({ error: "project has no lead to hand off: invalid input" }, { status: 422 })
    if (db.leadHandoffs[id]) {
      return HttpResponse.json({ error: `project ${id} already has a pending lead handoff: invalid input` }, { status: 422 })
    }
    const input = (await request.json()) as components["schemas"]["RequestLeadHandoffInput"]
    const handoff: LeadHandoff = {
      id: 1,
      project: id,
      from: lead.actor ?? "",
      to: input.to ?? "",
      state: "pending",
      requested_by: db.me.id ?? "",
      requested_at: new Date().toISOString(),
    }
    db.leadHandoffs[id] = handoff
    return HttpResponse.json(handoff, { status: 201 })
  }),

  http.delete("/api/v1/projects/:id/lead-handoff", async ({ params }) => {
    await delay(LATENCY_MS)
    const id = String(params.id)
    if (!db.leadHandoffs[id]) return HttpResponse.json({ error: "no pending lead handoff: not found" }, { status: 404 })
    delete db.leadHandoffs[id]
    return new HttpResponse(null, { status: 204 })
  }),

  http.delete("/api/v1/projects/:id/participants/:actor", async ({ params }) => {
    await delay(LATENCY_MS)
    const members = db.participants[String(params.id)]
    const i = members?.findIndex((m) => m.actor === params.actor) ?? -1
    if (!members || i < 0) return HttpResponse.json({ error: "not found" }, { status: 404 })
    if (members[i].lead) {
      return HttpResponse.json(
        { error: "project lead cannot be removed; hand the lead off first (lode project crew transfer): invalid input" },
        { status: 422 },
      )
    }
    members.splice(i, 1)
    return new HttpResponse(null, { status: 204 })
  }),

  http.get("/api/v1/tasks", async ({ request }) => {
    await delay(LATENCY_MS)
    const params = new URL(request.url).searchParams
    const project = params.get("project")
    const states = params.getAll("state").flatMap((s) => s.split(","))
    const body: Ok<"/api/v1/tasks", "get"> = {
      tasks: db.tasks
        .filter((t) => !project || t.project === project)
        .filter((t) => !states.length || states.includes(t.state ?? ""))
        .map(({ body: _body, ...t }) => t),
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

  http.get("/api/v1/tasks/:id", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const task = db.tasks.find((t) => t.id === params.id)
    if (!task) return HttpResponse.json({ error: "task not found" }, { status: 404 })
    const body: Ok<"/api/v1/tasks/{id}", "get"> = { ...task, ...(await asBlocks(request, task.body ?? "")) }
    return HttpResponse.json(body)
  }),

  http.patch("/api/v1/tasks/:id", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const task = db.tasks.find((t) => t.id === params.id)
    if (!task) return HttpResponse.json({ error: "task not found" }, { status: 404 })
    const input = (await request.json()) as components["schemas"]["EditTaskInput"]
    const refused = await refuseBodySave("task", task.id ?? "", task.body ?? "", input)
    if (refused) return refused
    if (input.blocks) {
      task.body = blocksMarkdown(input.blocks)
      delete input.blocks
    }
    delete input.if_body_sha256
    delete input.lock_session
    const states = ["draft", "ready", "in_progress", "in_review", "merged", "completed", "deployed_dev", "deployed_prod", "released", "abandoned"]
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

  http.get("/api/v1/docs/resolve", async ({ request }) => {
    await delay(LATENCY_MS)
    const doc = db.docs.find((d) => d.ref === new URL(request.url).searchParams.get("ref"))
    if (!doc) return HttpResponse.json({ error: "document not found" }, { status: 404 })
    return HttpResponse.json(doc)
  }),

  http.get("/api/v1/docs/:id", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const doc = db.docs.find((d) => String(d.id) === params.id)
    if (!doc) return HttpResponse.json({ error: "document not found" }, { status: 404 })
    const body: Ok<"/api/v1/docs/{id}", "get"> = { ...doc, ...(await asBlocks(request, doc.body ?? "")) }
    return HttpResponse.json(body)
  }),

  http.put("/api/v1/docs/:id/body", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const doc = db.docs.find((d) => String(d.id) === params.id)
    if (!doc) return HttpResponse.json({ error: "document not found" }, { status: 404 })
    const input = (await request.json()) as components["schemas"]["UpdateDocBodyInput"]
    if (input.blocks && doc.kind === "spec") {
      return HttpResponse.json({ error: "a spec's body takes no blocks: edit its rules (WL-REQ-1300)" }, { status: 422 })
    }
    const refused = await refuseBodySave("doc", String(doc.id), doc.body ?? "", input)
    if (refused) return refused
    doc.body = input.blocks ? blocksMarkdown(input.blocks) : (input.body ?? "")
    const body: Ok<"/api/v1/docs/{id}/body", "put"> = doc
    return HttpResponse.json(body)
  }),

  http.get("/api/v1/rules/:id", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const rule = ruleByRef(String(params.id))
    if (!rule) return HttpResponse.json({ error: "rule not found" }, { status: 404 })
    const body: Ok<"/api/v1/rules/{id}", "get"> = { ...rule, ...(await asBlocks(request, rule.body ?? "")) }
    return HttpResponse.json(body)
  }),

  http.put("/api/v1/rules/:id", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const rule = ruleByRef(String(params.id))
    if (!rule) return HttpResponse.json({ error: "rule not found" }, { status: 404 })
    const input = (await request.json()) as components["schemas"]["EditRuleInput"]
    const refused = await refuseBodySave("rule", String(rule.id), rule.body ?? "", input)
    if (refused) return refused
    // A draft is rewritten in place; the mock keeps every rule a draft
    rule.heading = input.heading ?? rule.heading
    rule.body = input.blocks ? blocksMarkdown(input.blocks) : (input.body ?? "")
    const body: Ok<"/api/v1/rules/{id}", "put"> = rule
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

  http.patch("/api/v1/ideas/:id/intake", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const shared = sharedIdea(String(params.id))
    if ("error" in shared) return HttpResponse.json({ error: shared.error }, { status: shared.status })
    const input = (await request.json()) as components["schemas"]["PatchIdeaIntakeInput"]
    if (!["accept", "narrow", "park", "stop", ""].includes(input.decision ?? "")) {
      return HttpResponse.json({ error: `unknown decision ${input.decision}` }, { status: 400 })
    }
    shared.idea.intake = { pre_research_authorized: input.pre_research_authorized ?? false, decision: input.decision ?? "" }
    const body: Ok<"/api/v1/ideas/{id}/intake", "patch"> = shared.idea
    return HttpResponse.json(body)
  }),

  http.get("/api/v1/ideas/:id/litmus-tests", async ({ params }) => {
    await delay(LATENCY_MS)
    const shared = sharedIdea(String(params.id))
    if ("error" in shared) return HttpResponse.json({ error: shared.error }, { status: shared.status })
    const body: Ok<"/api/v1/ideas/{id}/litmus-tests", "get"> = { litmus_tests: db.litmusTests[String(params.id)] ?? [] }
    return HttpResponse.json(body)
  }),

  http.post("/api/v1/ideas/:id/litmus-tests", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const id = String(params.id)
    const shared = sharedIdea(id)
    if ("error" in shared) return HttpResponse.json({ error: shared.error }, { status: shared.status })
    const input = (await request.json()) as components["schemas"]["CreateLitmusTestInput"]
    const tests = (db.litmusTests[id] ??= [])
    const test: LitmusTest = { summary: "", recommendations: [], ...input, id: String(tests.length + 1) }
    tests.unshift(test)
    return HttpResponse.json(test, { status: 201 })
  }),

  http.get("/api/v1/ideas/:id/pre-research", async ({ params }) => {
    await delay(LATENCY_MS)
    const shared = sharedIdea(String(params.id))
    if ("error" in shared) return HttpResponse.json({ error: shared.error }, { status: shared.status })
    const body: Ok<"/api/v1/ideas/{id}/pre-research", "get"> = { findings: db.preResearch[String(params.id)] ?? [] }
    return HttpResponse.json(body)
  }),

  http.put("/api/v1/ideas/:id/pre-research", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const shared = sharedIdea(String(params.id))
    if ("error" in shared) return HttpResponse.json({ error: shared.error }, { status: shared.status })
    const input = (await request.json()) as components["schemas"]["PutPreResearchInput"]
    db.preResearch[String(params.id)] = input.findings ?? []
    const body: Ok<"/api/v1/ideas/{id}/pre-research", "put"> = { findings: input.findings ?? [] }
    return HttpResponse.json(body)
  }),

  http.get("/api/v1/ideas/:id/sources", async ({ params }) => {
    await delay(LATENCY_MS)
    const id = String(params.id)
    const shared = sharedIdea(id)
    if ("error" in shared) return HttpResponse.json({ error: shared.error }, { status: shared.status })
    const body: Ok<"/api/v1/ideas/{id}/sources", "get"> = { documents: db.documents.filter((d) => d.idea === id) }
    return HttpResponse.json(body)
  }),

  http.post("/api/v1/ideas/:id/sources", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const id = String(params.id)
    const shared = sharedIdea(id)
    if ("error" in shared) return HttpResponse.json({ error: shared.error }, { status: shared.status })
    const input = (await request.json()) as components["schemas"]["CreateProjectDocumentInput"]
    const document: ProjectDocument = {
      source: "",
      milestone: "",
      due: "",
      date: "",
      url: "",
      participants: [],
      ...input,
      category: "sources",
      id: `doc_${String(db.documents.length + 1).padStart(12, "0")}`,
      idea: id,
    }
    db.documents.push(document)
    return HttpResponse.json(document, { status: 201 })
  }),

  http.delete("/api/v1/ideas/:id", async ({ params }) => {
    await delay(LATENCY_MS)
    const found = visibleIdea(String(params.id))
    if (!found) return HttpResponse.json({ error: "idea not found" }, { status: 404 })
    if (!found.shared && found.author !== db.me.id) return HttpResponse.json({ error: "forbidden" }, { status: 403 })
    db.ideas.splice(db.ideas.indexOf(found), 1)
    return new HttpResponse(null, { status: 204 })
  }),

  // Body locks. One session per lock: a second take by the holder replaces it.
  http.get("/api/v1/locks/:kind/:id", async ({ params }) => {
    await delay(LATENCY_MS)
    const kind = String(params.kind)
    const subject_id = String(params.id)
    const lock = db.locks.get(`${kind}:${subject_id}`)
    const body: Ok<"/api/v1/locks/{kind}/{id}", "get"> =
      lock && !lock.ended ? { ...lockView(lock), session: undefined } : { kind, subject_id, holder: "", can_unlock: false }
    return HttpResponse.json(body)
  }),

  http.post("/api/v1/locks/:kind/:id", async ({ params }) => {
    await delay(LATENCY_MS)
    const kind = String(params.kind)
    const subject_id = String(params.id)
    const key = `${kind}:${subject_id}`
    const held = db.locks.get(key)
    if (held && !held.ended && held.holder && held.holder !== db.me.id) {
      return HttpResponse.json({ ...lockView(held), session: undefined }, { status: 409 })
    }
    const lock: Ok<"/api/v1/locks/{kind}/{id}", "post"> = {
      kind,
      subject_id,
      holder: db.me.id,
      can_unlock: true,
      session: `session-${++lockSessions}`,
    }
    db.locks.set(key, lock)
    return HttpResponse.json(lock)
  }),

  http.put("/api/v1/locks/:kind/:id", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const input = (await request.json()) as components["schemas"]["LockCheckIn"]
    const lock = db.locks.get(`${String(params.kind)}:${String(params.id)}`)
    if (!lock || lock.ended || lock.session !== input.session) {
      const lost: components["schemas"]["LockLost"] = {
        reason: lock?.ended?.reason ?? "released",
        by: lock?.ended?.by ?? "",
        lock: lock && !lock.ended ? { ...lockView(lock), session: undefined } : { holder: "" },
      }
      return HttpResponse.json(lost, { status: 409 })
    }
    const body: Ok<"/api/v1/locks/{kind}/{id}", "put"> = lockView(lock)
    return HttpResponse.json(body)
  }),

  http.delete("/api/v1/locks/:kind/:id", async ({ params, request }) => {
    await delay(LATENCY_MS)
    const key = `${String(params.kind)}:${String(params.id)}`
    const session = new URL(request.url).searchParams.get("session")
    if (db.locks.get(key)?.session === session) db.locks.delete(key)
    return new HttpResponse(null, { status: 204 })
  }),

  http.post("/api/v1/locks/:kind/:id/unlock", async ({ params }) => {
    await delay(LATENCY_MS)
    const key = `${String(params.kind)}:${String(params.id)}`
    const lock = db.locks.get(key)
    if (lock && !lock.can_unlock) return HttpResponse.json({ error: "forbidden" }, { status: 403 })
    db.locks.delete(key)
    return new HttpResponse(null, { status: 204 })
  }),
]

let lockSessions = 0

// The wire shape, without the mock's own fields
function lockView(lock: MockLock): components["schemas"]["BodyLock"] {
  const view = { ...lock }
  delete view.ended
  return view
}

// The body editor's wire form (WL-REQ-1867), simplified: one paragraph block
// per Markdown paragraph, its text as a single run. The server parses
// Worklode Markdown; the mock only needs text to survive the round trip.
type EditorBlock = components["schemas"]["EditorBlock"]

async function asBlocks(request: Request, body: string): Promise<{ blocks?: EditorBlock[]; body_sha256?: string }> {
  if (new URL(request.url).searchParams.get("as") !== "blocks") return {}
  const blocks = body
    .split(/\n{2,}/)
    .map((p) => p.trim())
    .filter(Boolean)
    .map((text): EditorBlock => ({ type: "paragraph", content: [{ type: "text", text, styles: {} }] }))
  return { blocks, body_sha256: await sha256Hex(body) }
}

function blocksMarkdown(blocks: EditorBlock[]): string {
  const paragraphs = blocks.map((b) => (b.content ?? []).map((run) => run.text ?? "").join("")).filter(Boolean)
  return paragraphs.length ? `${paragraphs.join("\n\n")}\n` : ""
}

/**
 * A body save's two checks (WL-REQ-1868), in the server's order: a
 * lock_session that is not the live session is 409 with the lock, then an
 * if_body_sha256 that is not the stored body's hash is 409 with the stored
 * text. Undefined when the save may go ahead.
 */
async function refuseBodySave(
  kind: string,
  id: string,
  stored: string,
  input: { if_body_sha256?: string; lock_session?: string },
) {
  if (input.lock_session) {
    const lock = db.locks.get(`${kind}:${id}`)
    if (!lock || lock.ended || lock.session !== input.lock_session) {
      const body: components["schemas"]["BodyLock"] = lock && !lock.ended ? { ...lockView(lock), session: undefined } : { kind, subject_id: id, holder: "" }
      return HttpResponse.json(body, { status: 409 })
    }
  }
  if (input.if_body_sha256) {
    const current = await asBlocks(new Request("http://mock/?as=blocks"), stored)
    if (current.body_sha256 !== input.if_body_sha256) {
      const body: components["schemas"]["BodyConflict"] = { kind, id, body: stored, ...current }
      return HttpResponse.json(body, { status: 409 })
    }
  }
  return undefined
}

// A rule ref (WL-REQ-12) names the rule by project key and number; its kind is not part of the name
function ruleByRef(ref: string) {
  const match = /^(.+)-[A-Z]+-(\d+)$/.exec(ref)
  if (!match) return undefined
  const [, key, n] = match
  const project = db.projects.find((p) => p.key === key)
  return db.rules.find((r) => r.project === project?.id && r.ref?.replace(/^.+-/, "") === n)
}
