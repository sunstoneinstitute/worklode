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
  return { me, projects, crew, participants }
}

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
