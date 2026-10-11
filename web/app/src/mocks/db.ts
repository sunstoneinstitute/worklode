import type { CrewMember, Meeting, Project, Task } from "@/api/types"
import { CREW } from "@/lib/crew"

// The dummy database MSW answers from. Held in memory: changes last until
// a reload (or, in tests, until resetDb() after each test).

function todayAt(hours: number, minutes = 0) {
  const date = new Date()
  date.setHours(hours, minutes, 0, 0)
  // Local ISO date-time without a timezone, e.g. 2026-09-24T09:00:00
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:00`
}

function seed() {
  // CREW stays in src/lib/crew.ts until plan 2 moves every table to the API
  const users: CrewMember[] = CREW.map((member) => ({ ...member }))

  const projects: Project[] = [
    { id: "the-grid", title: "The Grid", author: "u1", type: "data-story", status: "accepted", submitted: "2026-07-14", progress: 55, daysRemaining: 20, attention: "5 past due tasks" },
    { id: "melting-glaciers", title: "Melting Glaciers", author: "u2", type: "data-story", status: "accepted", submitted: "2026-07-30", progress: 30, daysRemaining: 20, attention: "2 reviews waiting" },
    { id: "night-shift", title: "Night shift at the reservoir", author: "u3", type: "short-story", status: "litmus", submitted: "2026-09-08", progress: 10, daysRemaining: 45, attention: "1 review waiting" },
    { id: "urban-heat-islands", title: "Urban heat islands", author: "u4", type: "data-story", status: "on-hold", submitted: "2026-08-21", progress: 70, daysRemaining: 12, attention: "3 past due tasks" },
    { id: "grid-outages", title: "Grid outages, block by block", author: "u1", type: "data-story", status: "litmus", submitted: "2026-08-18", progress: 5, daysRemaining: 60, attention: "0 past due tasks" },
  ]

  const tasks: Task[] = [
    { id: "t1", name: "Draft launch copy", project: "the-grid", assignee: "u1", due: "2026-09-15", status: "in-progress", priority: "high" },
    { id: "t2", name: "Audit icon set", project: "melting-glaciers", assignee: "u2", due: "2026-10-02", status: "todo", priority: "medium" },
    { id: "t3", name: "Ship static export", project: "the-grid", assignee: "u3", due: "2026-09-28", status: "blocked", priority: "high" },
    { id: "t4", name: "Review crew roster", project: "melting-glaciers", assignee: "u4", due: "2026-11-20", status: "done", priority: "low" },
  ]

  const meetings: Meeting[] = [
    { id: "m1", title: "The Grid Startup Meeting", start: todayAt(9), end: todayAt(13, 45) },
    { id: "m2", title: "The Grid Startup Meeting", start: todayAt(9), end: todayAt(13, 45) },
    { id: "m3", title: "The Grid Startup Meeting", start: todayAt(9), end: todayAt(13, 45) },
    { id: "m4", title: "The Grid Startup Meeting", start: todayAt(9), end: todayAt(13, 45) },
  ]

  return { meId: "u1", users, projects, tasks, meetings, nextTaskNumber: tasks.length + 1 }
}

export const db = seed()

/** Puts the dummy data back the way it started */
export function resetDb() {
  Object.assign(db, seed())
}
