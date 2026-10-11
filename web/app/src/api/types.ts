// Every data shape the API sends and receives. Dates are ISO strings
// (YYYY-MM-DD, date-times YYYY-MM-DDTHH:mm:ss); people are user ids.

/** Anyone on the crew roster; also the signed-in user (GET /api/me) */
export interface CrewMember {
  id: string
  /** Full name, e.g. "Tobias Smith" */
  name: string
  /** Shown as "@handle"; derived from the name when missing */
  handle?: string
  avatarUrl?: string
}

export interface Project {
  id: string
  title: string
  /** User id */
  author: string
  /** A story type value: "data-story" | "short-story" */
  type: string
  /** "litmus" | "on-hold" | "accepted" */
  status: string
  /** ISO date */
  submitted: string
  /** 0–100 */
  progress: number
  daysRemaining: number
  /** The quick-link card's headline, e.g. "5 past due tasks" (first word shown bold) */
  attention: string
}

export interface Task {
  id: string
  name: string
  /** Project id */
  project: string
  /** User id, or "" for nobody */
  assignee: string
  /** ISO date, or "" for none */
  due: string
  /** "todo" | "in-progress" | "done" | "blocked" */
  status: string
  /** "low" | "medium" | "high" */
  priority: string
}

/** POST /api/tasks body: a task without its id (the server assigns one) */
export type NewTask = Omit<Task, "id">

/** PATCH /api/tasks/:id body: only the fields that change */
export type TaskPatch = Partial<NewTask>

export interface Meeting {
  id: string
  title: string
  /** ISO date-time */
  start: string
  /** ISO date-time */
  end: string
}
