import type { Ok } from "./typed"

// The dummy database MSW answers from. Held in memory: changes last until
// a reload (or, in tests, until resetDb() after each test).

function seed() {
  const me: Ok<"/api/v1/whoami", "get"> = { id: "actor-1", kind: "human", admin: false }
  const projects: NonNullable<Ok<"/api/v1/projects", "get">["projects"]> = [
    {
      id: "p1",
      key: "WL",
      name: "Worklode",
      horizon: "standing",
      repos: [{ repo: "sunstoneinstitute/worklode" }, { repo: "sunstoneinstitute/worklode-design" }],
    },
    { id: "p2", key: "DP", name: "Data platform", horizon: "bounded", repos: [{ repo: "sunstoneinstitute/data-platform" }] },
  ]
  const crew: CrewMember[] = [
    { actor: "stig", display_name: "Stig Bakken" },
    { actor: "ada", display_name: "Ada Lovelace" },
    { actor: "bob", display_name: "Bob" },
  ]
  // Each project's Crew roster, by project id
  const participants: Record<string, CrewMember[]> = {
    p1: [
      { actor: "stig", display_name: "Stig Bakken", roles: ["owner"], lead: true },
      { actor: "ada", display_name: "Ada Lovelace", roles: ["member"], lead: false },
    ],
    p2: [],
  }
  const tasks: Task[] = [
    { id: "WL-1", project: "p1", title: "Draft launch copy", state: "in_progress", priority: "high", assignee: "stig", due: "2026-10-30" },
    { id: "WL-2", project: "p1", title: "Audit icon set", state: "ready", priority: "medium", assignee: "", due: "" },
    { id: "DP-1", project: "p2", title: "Load the glacier data", state: "ready", priority: "low", assignee: "ada", due: "2026-11-02" },
  ]
  // GET /api/v1/projects/{id}/summary, by project id. Static: the server
  // derives it from the tasks, the mock does not. WL-1 is past due from
  // 2026-10-31 on.
  const summaries: Record<string, Ok<"/api/v1/projects/{id}/summary", "get">> = {
    p1: {
      past_due: [{ id: "WL-1", label: "Draft launch copy", due: "2026-10-30", url: "/projects/p1/feature/1" }],
      awaiting_review: [{ id: "WL-SPEC-3", label: "Launch plan", due: "", url: "/projects/p1/spec/3" }],
      workloads: [
        {
          actor: "stig",
          availability: "overbooked",
          done: 0,
          total: 1,
          past_due: [{ id: "WL-1", label: "Draft launch copy", due: "2026-10-30", url: "/projects/p1/feature/1" }],
          upcoming: [],
          awaiting_review: [],
        },
        { actor: "ada", availability: "open", done: 0, total: 0, past_due: [], upcoming: [], awaiting_review: [] },
      ],
    },
    p2: { past_due: [], awaiting_review: [], workloads: [] },
  }
  // Backbone documents and rules (GET /api/v1/docs, /api/v1/rules)
  const docs: Doc[] = [
    { id: 1, ref: "WL-SPEC-3", project: "p1", kind: "spec", title: "Launch plan", status: "draft", updated_at: "2026-10-01T09:00:00Z" },
    { id: 2, ref: "WL-SPEC-4", project: "p1", kind: "spec", title: "Icon set", status: "accepted", updated_at: "2026-10-02T09:00:00Z" },
    { id: 3, ref: "WL-PLAN-5", project: "p1", kind: "plan", title: "Launch rollout", status: "accepted", updated_at: "2026-10-03T09:00:00Z" },
    { id: 4, ref: "DP-SPEC-1", project: "p2", kind: "spec", title: "Glacier schema", status: "draft", updated_at: "2026-10-04T09:00:00Z" },
  ]
  const rules: Rule[] = [
    { id: 10, ref: "WL-REQ-10", project: "p1", heading: "Launch copy is reviewed", kind: "requirement", status: "accepted" },
    { id: 11, ref: "WL-RULE-11", project: "p1", heading: "One icon font", kind: "invariant", status: "draft" },
    { id: 12, ref: "DP-REQ-1", project: "p2", heading: "Glacier ids are stable", kind: "requirement", status: "draft" },
  ]
  // The documents register (WL-REQ-1798)
  const documents: ProjectDocument[] = [
    { id: "doc_000000000001", project: "p1", name: "Launch brief", category: "spec", type: "document", status: "draft", url: "https://docs.example.com/brief", source: "", milestone: "", due: "", date: "", participants: [] },
    { id: "doc_000000000002", project: "p1", name: "Kickoff notes", category: "meeting-notes", type: "document", status: "final", url: "", source: "", milestone: "", due: "", date: "2026-10-05", participants: ["stig", "ada"] },
    { id: "doc_000000000003", project: "p2", name: "Glacier dataset", category: "sources", type: "dataset", status: "final", url: "", source: "NSIDC", milestone: "", due: "", date: "", participants: [] },
    // An idea's source (GET /api/v1/ideas/{id}/sources)
    { id: "doc_000000000004", idea: "idea_000000000002", name: "Station registry", category: "sources", type: "dataset", status: "final", url: "", source: "WMO", milestone: "", due: "", date: "", participants: [] },
  ]
  // Ideas (WL-REQ-1799): private to their author until shared
  const ideas: Idea[] = [
    idea({ id: "idea_000000000001", author: "actor-1", title: "Glacier retreat explainer", type: "story-pitch", story_type: "data-story", submitted: "2026-10-10" }),
    idea({ id: "idea_000000000002", author: "ada", title: "Map every weather station", submitted: "2026-10-08", shared: "2026-10-09", task: "WL-3" }),
    // Another actor's private idea: the list never shows it
    idea({ id: "idea_000000000003", author: "bob", title: "Bob's secret", submitted: "2026-10-07" }),
  ]
  // Findings on shared ideas (WL-REQ-1800), by idea id; litmus tests newest first
  const litmusTests: Record<string, LitmusTest[]> = {
    idea_000000000002: [
      { id: "2", date: "2026-10-12", summary: "Strong fit once narrowed to one region.", recommendations: ["Start with the Alps."] },
      { id: "1", date: "2026-10-09", summary: "Too broad as pitched.", recommendations: ["Pick a smaller set of stations."] },
    ],
  }
  const preResearch: Record<string, string[]> = {
    idea_000000000002: ["Station metadata is public through the WMO registry."],
  }
  // LODE_INTAKE_PROJECT is set; false makes share answer 409
  const intakeConfigured: boolean = true
  // Body locks by "kind:id" (GET/POST/PUT/DELETE /api/v1/locks/{kind}/{id})
  const locks = new MockLocks()
  return { me, projects, crew, participants, tasks, summaries, docs, rules, documents, ideas, litmusTests, preResearch, intakeConfigured, locks }
}

type BodyLock = Ok<"/api/v1/locks/{kind}/{id}", "get">

/** A held lock; `ended` is set when its session ends under the holder (unlocked, inactive, expired) */
export type MockLock = BodyLock & { ended?: { reason: string; by: string } }

export class MockLocks extends Map<string, MockLock> {
  /** Ends the lock's session the way the server's unlock or sweep does */
  end(key: string, ended: { reason: string; by: string }) {
    const lock = this.get(key)
    if (lock) lock.ended = ended
  }
}

export type Idea = Ok<"/api/v1/ideas/{id}", "get">
export type LitmusTest = NonNullable<Ok<"/api/v1/ideas/{id}/litmus-tests", "get">["litmus_tests"]>[number]

/** An idea with the server's defaults; status follows `shared` */
export function idea(fields: Partial<Idea> & { id: string; author: string }): Idea {
  return {
    title: "",
    body: "",
    type: "general",
    story_type: "",
    litmus_test_id: "",
    shared: "",
    task: "",
    submitted: "",
    intake: { pre_research_authorized: false, decision: "" },
    ...fields,
    status: fields.shared ? "intake" : "private",
  }
}

/**
 * The intake-side routes' check (WL-REQ-1799): a shared idea, else the
 * error answer — 409 to a private idea's author, 404 to anyone else.
 */
export function sharedIdea(id: string): { idea: Idea } | { error: string; status: number } {
  const found = visibleIdea(id)
  if (!found) return { error: "idea not found", status: 404 }
  if (!found.shared) return { error: "idea not shared", status: 409 }
  return { idea: found }
}

/** What the store's visibility predicate lets the caller see: author = caller OR shared */
export function visibleIdea(id: string) {
  return db.ideas.find((i) => i.id === id && (i.author === db.me.id || i.shared))
}

export type Doc = NonNullable<Ok<"/api/v1/docs", "get">["docs"]>[number]
export type Rule = Ok<"/api/v1/rules", "get">[number]
export type ProjectDocument = NonNullable<Ok<"/api/v1/projects/{id}/documents", "get">["documents"]>[number]

export type Task = NonNullable<Ok<"/api/v1/tasks", "get">["tasks"]>[number]
export type CrewMember = NonNullable<Ok<"/api/v1/projects/{id}/participants", "get">["participants"]>[number]

/** What GET /api/v1/projects/{id} answers, or undefined for an unknown id */
export function projectDetail(id: string): Ok<"/api/v1/projects/{id}", "get"> | undefined {
  const project = db.projects.find((p) => p.id === id)
  return project && { ...project, focus: [], labels: {}, settings: {} }
}

export const db = seed()


/** Puts the dummy data back the way it started */
export function resetDb() {
  Object.assign(db, seed())
}
