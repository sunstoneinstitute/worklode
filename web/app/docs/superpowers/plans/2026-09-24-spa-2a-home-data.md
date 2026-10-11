# SPA conversion, plan 2a: Home's data from the API

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Everything the Home page shows (the current user, project quick links, the task list, upcoming meetings) comes from `/api/...` endpoints through TanStack Query, answered in development and tests by MSW with dummy data. The backend developer gets one complete, working example of the pattern to copy.

**Architecture:** A small `fetch` wrapper (`src/api/client.ts`) and typed query/mutation hooks (`src/api/queries.ts`, `src/api/mutations.ts`) are the only code that knows URLs. MSW handlers (`src/mocks/handlers.ts`) answer those URLs from an in-memory dummy database (`src/mocks/db.ts`), in the browser during `pnpm dev` and in Node during tests. The router's root loader fetches the current user before any page renders. `DataTable` gains a controlled-rows mode so the task list's edits go through mutations with optimistic updates.

**Tech Stack:** TanStack Query 5, MSW 2, sonner (toasts), TanStack Router (from plan 1), date-fns, Vitest, Testing Library, pnpm.

**Spec:** `docs/superpowers/specs/2026-09-24-react-spa-conversion-design.md`, section 3 (Data layer), limited to what Home needs. **Run after plan 1** (`2026-09-24-spa-1-toolchain-routing.md`) has landed: this plan edits files as plan 1 leaves them.

**Scope:** Only Home's data. The task list is shared by Home, Tasks, Project Overview and Project Tasks, so those tables move to the API too. Everything else (Ideas, Projects table, Documents, intake page, project summary card, project crew pickers, column preferences, forms) keeps its inline dummy data until plan 2 (the rest of the data layer).

## Global Constraints

- Package manager pnpm 12.6.0; TypeScript `^7.0.2`, type checking only. `erasableSyntaxOnly` is on: no TypeScript parameter properties (`constructor(readonly x)`), no enums.
- Every existing screen must look the same: do not change existing Tailwind classes or `src/index.css`. New elements (loading blocks, error messages, toasts) use existing design tokens only.
- No arbitrary pixel values in `className`s.
- Only `src/api/` knows endpoint URLs. Components call hooks, never `fetch`.
- Dates in API data are ISO strings: dates `YYYY-MM-DD`, date-times `YYYY-MM-DDTHH:mm:ss`. People are referenced by user id.
- Error responses are JSON: `{ "message": string }`, and for validation `422 { "message": string, "errors": { "<field>": "<message>" } }`.
- MSW is never in a production build: it is loaded only through a dynamic import guarded by `import.meta.env.DEV`.
- Every MSW handler waits 150 ms before answering in `pnpm dev`, and 0 ms in tests (`import.meta.env.MODE === "test"`).
- Code style: ES modules, destructured imports, match surrounding comment density.
- Every commit message ends with a blank line and `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.
- Work happens on branch `react-spa-conversion`.

## Review Focus

1. **The server rejects an inline edit** (500, or 422 for a bad status): the cell must go back to its old value and a toast must say so; nothing stays silently wrong. Test in Task 5.
2. **Two tables showing the same tasks** (Home's list, then Tasks after navigating): an edit in one must show in the other without a reload, because both read the same cache. Test in Task 5 (edit, then check the cache for the other key).
3. **An endpoint fails on first load** (meetings, quick links, tasks): the section shows an error with a Retry button instead of an empty or broken layout, and Retry recovers. Tests in Tasks 3 and 5.
4. **Relative URLs under jsdom:** Node's `fetch` rejects `/api/tasks` without a base. The client must resolve against `window.location.origin` so the same code works in the browser and in tests. Test in Task 1.
5. **Meeting times crossing midnight or on another day** render as a readable label ("Tomorrow from 9am – 10:30am", "Sep 26 from 2pm – 3pm"), not a wrong "Today". Test in Task 3.

---

### Task 1: Mock API and fetch client

**Files:**
- Modify: `package.json`, `pnpm-workspace.yaml`, `vite.config.ts`, `src/main.tsx`, `src/test/setup.ts`
- Create: `public/mockServiceWorker.js` (generated), `.env.example`, `src/api/types.ts`, `src/api/client.ts`, `src/api/client.test.ts`, `src/mocks/db.ts`, `src/mocks/handlers.ts`, `src/mocks/handlers.test.ts`, `src/mocks/node.ts`, `src/mocks/browser.ts`

**Interfaces:**
- Produces:
  - Types in `src/api/types.ts`: `CrewMember { id; name; handle?; avatarUrl? }`, `Project { id; title; author; type; status; submitted; progress: number; daysRemaining: number; attention: string }`, `Task { id; name; project; assignee; due; status; priority }`, `NewTask = Omit<Task, "id">`, `TaskPatch = Partial<NewTask>`, `Meeting { id; title; start; end }` (all other fields `string`).
  - `apiFetch<T>(path: string, options?: { method?: string; body?: unknown }): Promise<T>` and `class ApiError extends Error { status: number; errors?: Record<string, string> }` in `src/api/client.ts`.
  - `db` and `resetDb()` in `src/mocks/db.ts`; `handlers` in `src/mocks/handlers.ts`; `server` in `src/mocks/node.ts`; `worker` in `src/mocks/browser.ts`.
  - Endpoints: `GET /api/me`, `GET /api/crew`, `GET /api/projects`, `GET /api/tasks[?projectId=]`, `POST /api/tasks`, `PATCH /api/tasks/:id`, `DELETE /api/tasks/:id`, `GET /api/meetings[?limit=]`.

- [ ] **Step 1: Install**

Add `msw: true` to `allowBuilds` in `pnpm-workspace.yaml` (MSW's install script only copies its worker file; pnpm blocks it otherwise):

```yaml
  # Keeps public/mockServiceWorker.js in step with the installed MSW
  msw: true
```

Then:

```bash
pnpm add @tanstack/react-query@^5.103.2 sonner@^2.0.8
pnpm add -D msw@^2.15.0
pnpm exec msw init public --save
```

Expected: `public/mockServiceWorker.js` exists and `package.json` gains `"msw": { "workerDirectory": ["public"] }`. If pnpm adds `minimumReleaseAgeExclude` entries to `pnpm-workspace.yaml`, keep them.

- [ ] **Step 2: Write the failing client test**

Create `src/api/client.test.ts`:

```ts
import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { server } from "@/mocks/node"
import { ApiError, apiFetch } from "./client"

describe("apiFetch", () => {
  it("resolves a relative path and parses JSON", async () => {
    server.use(http.get("/api/ping", () => HttpResponse.json({ ok: true })))
    await expect(apiFetch("/api/ping")).resolves.toEqual({ ok: true })
  })

  it("sends a JSON body with its method", async () => {
    server.use(
      http.post("/api/echo", async ({ request }) =>
        HttpResponse.json({
          contentType: request.headers.get("Content-Type"),
          body: await request.json(),
        }),
      ),
    )
    await expect(apiFetch("/api/echo", { method: "POST", body: { a: 1 } })).resolves.toEqual({
      contentType: "application/json",
      body: { a: 1 },
    })
  })

  it("returns undefined for 204 No Content", async () => {
    server.use(http.delete("/api/thing", () => new HttpResponse(null, { status: 204 })))
    await expect(apiFetch("/api/thing", { method: "DELETE" })).resolves.toBeUndefined()
  })

  it("throws ApiError with the server's message and field errors", async () => {
    server.use(
      http.patch("/api/thing", () =>
        HttpResponse.json({ message: "Invalid", errors: { status: "Unknown status" } }, { status: 422 }),
      ),
    )
    const error = await apiFetch("/api/thing", { method: "PATCH", body: {} }).catch((caught) => caught)
    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ status: 422, message: "Invalid", errors: { status: "Unknown status" } })
  })

  it("throws ApiError with a fallback message when the body isn't JSON", async () => {
    server.use(http.get("/api/broken", () => new HttpResponse("oops", { status: 500 })))
    const error = await apiFetch("/api/broken").catch((caught) => caught)
    expect(error).toBeInstanceOf(ApiError)
    expect(error.status).toBe(500)
    expect(error.message).toMatch(/500/)
  })
})
```

- [ ] **Step 3: Run it to verify it fails**

Run: `pnpm test src/api/client.test.ts`
Expected: FAIL, `Failed to resolve import "@/mocks/node"`.

- [ ] **Step 4: Types and client**

Create `src/api/types.ts`:

```ts
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
```

Create `src/api/client.ts`:

```ts
/** A non-2xx response. `errors` holds per-field messages from a 422. */
export class ApiError extends Error {
  status: number
  errors?: Record<string, string>

  constructor(status: number, message: string, errors?: Record<string, string>) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.errors = errors
  }
}

/**
 * Calls the API and returns its parsed JSON (undefined for 204). Paths are
 * relative ("/api/tasks") and resolved against the page's origin, which
 * also makes them work under jsdom in tests, where fetch needs a full URL.
 */
export async function apiFetch<T>(
  path: string,
  { method = "GET", body }: { method?: string; body?: unknown } = {},
): Promise<T> {
  const response = await fetch(new URL(path, window.location.origin), {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  })

  if (!response.ok) {
    const payload = (await response.json().catch(() => ({}))) as {
      message?: string
      errors?: Record<string, string>
    }
    throw new ApiError(
      response.status,
      payload.message ?? `${response.status} ${response.statusText}`.trim(),
      payload.errors,
    )
  }

  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}
```

- [ ] **Step 5: Dummy database**

Create `src/mocks/db.ts`. The tasks and crew are exactly today's `TASKS` (`src/components/TaskList.tsx`) and `CREW` (`src/lib/crew.ts`), so existing test expectations keep their meaning. Meetings are built relative to today so the UI always shows "Today".

```ts
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
```

- [ ] **Step 6: Write the failing handler tests**

Create `src/mocks/handlers.test.ts`:

```ts
import { describe, expect, it } from "vitest"
import { apiFetch } from "@/api/client"
import type { Task } from "@/api/types"
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
```

- [ ] **Step 7: Handlers, servers, test setup**

Create `src/mocks/handlers.ts`:

```ts
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
```

Create `src/mocks/node.ts`:

```ts
import { setupServer } from "msw/node"
import { handlers } from "./handlers"

/** MSW for tests: answers fetch calls in Node with the same handlers */
export const server = setupServer(...handlers)
```

Create `src/mocks/browser.ts`:

```ts
import { setupWorker } from "msw/browser"
import { handlers } from "./handlers"

/** MSW for `pnpm dev`: a service worker answers /api calls in the browser */
export const worker = setupWorker(...handlers)
```

In `src/test/setup.ts`, add at the top (after the jest-dom import):

```ts
import { afterAll, afterEach, beforeAll } from "vitest"
import { resetDb } from "@/mocks/db"
import { server } from "@/mocks/node"

// Every test talks to the mock API; an unmocked request is a test bug
beforeAll(() => server.listen({ onUnhandledRequest: "error" }))
afterEach(() => {
  server.resetHandlers()
  resetDb()
})
afterAll(() => server.close())
```

- [ ] **Step 8: Run the tests**

Run: `pnpm test src/api src/mocks`
Expected: all pass.

- [ ] **Step 9: Start MSW in development, or proxy to Go**

Replace `src/main.tsx` with (keeping whatever `createAppRouter` call plan 1 left; plan 2a Task 2 changes it again):

```tsx
import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { RouterProvider } from "@tanstack/react-router"
import "./index.css"
import { createAppRouter } from "./router"

/**
 * In development, MSW answers /api calls with dummy data unless
 * VITE_API_MOCKS=off, in which case Vite proxies them to the Go server
 * (see vite.config.ts). The dynamic import keeps MSW out of production.
 */
async function startMockApi() {
  if (!import.meta.env.DEV || import.meta.env.VITE_API_MOCKS === "off") return
  const { worker } = await import("./mocks/browser")
  await worker.start({ onUnhandledRequest: "bypass" })
}

await startMockApi()

const router = createAppRouter()

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <RouterProvider router={router} />
  </StrictMode>,
)
```

(Top-level `await` is fine: `target` is `es2023` and Vite's build target supports it. If `vite build` complains about top-level await, wrap the render in `startMockApi().then(() => { … })` instead.)

In `vite.config.ts`, switch to the function form so the proxy can read env vars:

```ts
/// <reference types="vitest/config" />
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  // Includes VITE_* variables from the shell, e.g. `VITE_API_MOCKS=off pnpm dev`
  const env = loadEnv(mode, process.cwd(), 'VITE_')
  const mocksOff = env.VITE_API_MOCKS === 'off'

  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: {
        '@': path.resolve(import.meta.dirname, './src'),
      },
    },
    // With the mock API off, /api goes to the Go server
    server: mocksOff
      ? { proxy: { '/api': env.VITE_API_TARGET || 'http://localhost:8080' } }
      : undefined,
    test: {
      environment: 'jsdom',
      globals: true,
      setupFiles: ['./src/test/setup.ts'],
      include: ['src/**/*.{test,spec}.{ts,tsx}'],
    },
  }
})
```

Create `.env.example` (committed; `.gitignore` already allows it):

```bash
# Copy to .env.local to change these for your machine.

# "off" turns off the mock API (MSW) in `pnpm dev`; /api requests then go
# to VITE_API_TARGET through Vite's proxy.
VITE_API_MOCKS=on

# Where /api goes when mocks are off (the Go server)
VITE_API_TARGET=http://localhost:8080
```

- [ ] **Step 10: Verify and commit**

```bash
pnpm typecheck && pnpm test && pnpm lint && pnpm build
grep -c "mockServiceWorker\|setupWorker" dist/assets/*.js || true
```

Expected: all pass; the grep prints `0` for every file (MSW isn't in the production bundle).

Run `pnpm dev`, open the browser console on `/`: MSW logs `[MSW] Mocking enabled.` (The UI doesn't use the API yet.)

```bash
git add -A package.json pnpm-lock.yaml pnpm-workspace.yaml vite.config.ts .env.example public/mockServiceWorker.js src
git commit -m "Mock API with MSW and a typed fetch client

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Query client, router context, current user, toasts

**Files:**
- Create: `src/api/query-client.ts`, `src/api/queries.ts`, `src/components/ui/sonner.tsx`, `src/components/ui/load-state.tsx`, `src/components/Header.test.tsx`
- Modify: `src/router.tsx`, `src/main.tsx`, `src/test/render.tsx`, `src/layouts/RootLayout.tsx`, `src/components/Header.tsx`, `src/pages/Home.tsx`

**Interfaces:**
- Consumes: `apiFetch`, `CrewMember`, `Project`, `Task`, `Meeting` (Task 1).
- Produces:
  - `createQueryClient(options?: { test?: boolean }): QueryClient`.
  - `queryKeys` = `{ me: ["me"], crew: ["crew"], projects: ["projects"], tasks: { all: ["tasks"], list: (projectId?: string) => ["tasks", { projectId: string | null }] }, meetings: ["meetings"] }`. Keys follow the URL path, so "refresh everything under /api/tasks" is `invalidateQueries({ queryKey: queryKeys.tasks.all })`.
  - `meQueryOptions`; hooks `useMe(): CrewMember` (suspense; the root loader guarantees the data), `useCrew()`, `useProjects()`, `useTasks({ projectId }?)`, `useMeetings()` (each returns the TanStack `UseQueryResult`).
  - `createAppRouter({ history?, queryClient }: { history?: RouterHistory; queryClient: QueryClient })`. Router context type `{ queryClient: QueryClient }`.
  - `renderWithRouter` and `renderApp` keep their signatures and now wrap a fresh test `QueryClientProvider`; new `renderWithQuery(ui: ReactElement)` for components that only need queries.
  - `<Toaster />` in `src/components/ui/sonner.tsx`; `toast` is imported from `sonner` directly by callers.
  - `<LoadingBlock label className? />` and `<LoadError message onRetry />` in `src/components/ui/load-state.tsx`.

- [ ] **Step 1: Write the failing test**

Create `src/components/Header.test.tsx`:

```tsx
import { screen } from "@testing-library/react"
import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { server } from "@/mocks/node"
import { renderApp } from "@/test/render"

describe("current user", () => {
  it("greets and shows the avatar of the user from /api/me", async () => {
    server.use(http.get("/api/me", () => HttpResponse.json({ id: "u2", name: "Marcus Chen" })))
    await renderApp("/")
    expect(screen.getAllByRole("heading", { name: "Welcome, @MarcusChen!" }).length).toBeGreaterThan(0)
    expect(screen.getByText("MC")).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `pnpm test src/components/Header.test.tsx`
Expected: FAIL: the heading still says `@Claudicia` (hard-coded).

- [ ] **Step 3: Query client and query hooks**

Create `src/api/query-client.ts`:

```ts
import { QueryClient } from "@tanstack/react-query"

/**
 * One per app (and a fresh one per test). Tests don't retry, so a failing
 * request fails the test immediately instead of after retry delays.
 */
export function createQueryClient({ test = false }: { test?: boolean } = {}) {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: test ? false : 1, staleTime: 30_000 },
      mutations: { retry: false },
    },
  })
}
```

Create `src/api/queries.ts`:

```ts
import { queryOptions, useQuery, useSuspenseQuery } from "@tanstack/react-query"
import { apiFetch } from "./client"
import type { CrewMember, Meeting, Project, Task } from "./types"

/**
 * Query keys follow the URL path, so everything under /api/tasks is
 * `["tasks", …]` and one invalidateQueries({ queryKey: ["tasks"] })
 * refreshes every task list, whatever its filter.
 */
export const queryKeys = {
  me: ["me"] as const,
  crew: ["crew"] as const,
  projects: ["projects"] as const,
  tasks: {
    all: ["tasks"] as const,
    list: (projectId?: string) => ["tasks", { projectId: projectId ?? null }] as const,
  },
  meetings: ["meetings"] as const,
}

export const meQueryOptions = queryOptions({
  queryKey: queryKeys.me,
  queryFn: () => apiFetch<CrewMember>("/api/me"),
})

/** The signed-in user. The root route's loader fetches it before any page renders. */
export function useMe() {
  return useSuspenseQuery(meQueryOptions).data
}

export function useCrew() {
  return useQuery({ queryKey: queryKeys.crew, queryFn: () => apiFetch<CrewMember[]>("/api/crew") })
}

export function useProjects() {
  return useQuery({ queryKey: queryKeys.projects, queryFn: () => apiFetch<Project[]>("/api/projects") })
}

/** All tasks, or one project's */
export function useTasks({ projectId }: { projectId?: string } = {}) {
  return useQuery({
    queryKey: queryKeys.tasks.list(projectId),
    queryFn: () =>
      apiFetch<Task[]>(
        projectId ? `/api/tasks?projectId=${encodeURIComponent(projectId)}` : "/api/tasks",
      ),
  })
}

export function useMeetings() {
  return useQuery({ queryKey: queryKeys.meetings, queryFn: () => apiFetch<Meeting[]>("/api/meetings") })
}
```

- [ ] **Step 4: Toasts and load states**

Create `src/components/ui/sonner.tsx`:

```tsx
import { Toaster as Sonner } from "sonner"

/**
 * Where toasts appear (mounted once, in the root layout). Colors come from
 * the design tokens, so they follow dark mode. Inline styles, because
 * sonner's own stylesheet would win over utility classes.
 */
export function Toaster() {
  return (
    <Sonner
      position="bottom-right"
      toastOptions={{
        style: {
          background: "var(--color-surface)",
          color: "var(--color-text-primary)",
          border: "none",
          borderRadius: "var(--radius-card)",
        },
      }}
    />
  )
}
```

Create `src/components/ui/load-state.tsx`:

```tsx
import { Button } from "@/components/ui/button"

/** A pulsing placeholder the size of the content it stands in for */
export function LoadingBlock({ label, className = "" }: { label: string; className?: string }) {
  return (
    <div
      role="status"
      aria-label={label}
      className={`w-full animate-pulse rounded-card bg-surface ${className}`}
    />
  )
}

/** A section that failed to load, with a way to try again */
export function LoadError({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div role="alert" className="flex w-full flex-wrap items-center gap-4 rounded-card bg-surface p-5 text-sm text-text-primary">
      <p className="flex-1">{message}</p>
      <Button
        variant="outline"
        size="sm"
        onClick={onRetry}
        // Same outlined style as the tables' toolbar buttons
        className="rounded-full border border-text-primary bg-transparent px-4 text-text-primary hover:bg-text-primary hover:text-background dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"
      >
        Retry
      </Button>
    </div>
  )
}
```

- [ ] **Step 5: Router context and the root loader**

In `src/router.tsx`:
- import `type QueryClient` from `@tanstack/react-query` and `meQueryOptions` from `@/api/queries`;
- replace `createRootRoute` in the router import with `createRootRouteWithContext`;
- replace the root route with:

```tsx
const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  component: RootLayout,
  notFoundComponent: NotFound,
  // The header and Home's welcome need the signed-in user on every page
  loader: ({ context }) => context.queryClient.ensureQueryData(meQueryOptions),
})
```

(keep whatever `notFoundComponent` / catch-all arrangement plan 1 ended with);
- replace `createAppRouter` with:

```tsx
/**
 * The app's router. Browser history by default; tests pass a memory
 * history. Loaders get the query client through the router context.
 */
export function createAppRouter({
  history,
  queryClient,
}: {
  history?: RouterHistory
  queryClient: QueryClient
}) {
  return createRouter({ routeTree, history, context: { queryClient }, scrollRestoration: true })
}
```

In `src/main.tsx`, add `import { QueryClientProvider } from "@tanstack/react-query"` and `import { createQueryClient } from "./api/query-client"`, and replace the router creation and render with:

```tsx
const queryClient = createQueryClient()
const router = createAppRouter({ queryClient })

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
)
```

In `src/test/render.tsx`: import `QueryClientProvider` and `createQueryClient`; in `renderWithRouter`, create `const queryClient = createQueryClient({ test: true })` and render `<QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider>`; in `renderApp`, create the same and call `createAppRouter({ history: createMemoryHistory({ initialEntries: [path] }), queryClient })`, rendering inside the provider. Return `queryClient` alongside `router` from both. Add:

```tsx
/** Renders a component that uses queries but no router */
export async function renderWithQuery(ui: ReactElement) {
  const queryClient = createQueryClient({ test: true })
  const result = render(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>)
  await act(async () => {})
  return { ...result, queryClient }
}
```

- [ ] **Step 6: Use the current user; mount the toaster**

In `src/components/Header.tsx`, add `import { useMe } from "@/api/queries"`; at the top of `Header`, `const me = useMe()`; change `{initials("Claudicia")}` to `{initials(me.name)}`.

In `src/pages/Home.tsx`, add `import { useMe } from "@/api/queries"` and `import { userHandle } from "@/components/ui/data-table/cells"`; at the top of `Home`, `const greeting = \`Welcome, ${userHandle(useMe())}!\``; replace both `Welcome, @Claudicia!` texts with `{greeting}`.

In `src/layouts/RootLayout.tsx`, add `import { Toaster } from "@/components/ui/sonner"` and render `<Toaster />` after `<Outlet />`.

- [ ] **Step 7: Run everything**

```bash
pnpm typecheck && pnpm test && pnpm lint
```

Expected: all pass, including `Header.test.tsx` and the existing `routes.test.tsx` (the default user is still Claudicia).

- [ ] **Step 8: Commit**

```bash
git add -A src
git commit -m "Query client in the router context; header and Home greet the user from /api/me

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Upcoming meetings and project quick links from the API

**Files:**
- Create: `src/lib/meeting-time.ts`, `src/lib/meeting-time.test.ts`, `src/components/UpcomingMeetings.test.tsx`, `src/components/ProjectQuickLinks.test.tsx`
- Modify: `src/components/UpcomingMeetings.tsx`, `src/components/ProjectQuickLinks.tsx`

**Interfaces:**
- Consumes: `useMeetings`, `useProjects` (Task 2); `LoadingBlock`, `LoadError` (Task 2); `renderWithQuery`, `renderWithRouter` (Task 2); `server`, `db` (Task 1).
- Produces: `formatMeetingTime(start: string, end: string, now?: Date): string`. `UpcomingMeetings` and `ProjectQuickLinks` keep their current props.

The quick links show the first four projects from `/api/projects`. Today's placeholder repeats "Melting Glaciers" on three cards; with real project data the third and fourth cards become "Night shift at the reservoir" and "Urban heat islands". Same layout, different dummy text.

- [ ] **Step 1: Write the failing tests**

Create `src/lib/meeting-time.test.ts`:

```ts
import { describe, expect, it } from "vitest"
import { formatMeetingTime } from "./meeting-time"

const NOW = new Date(2026, 8, 24, 8, 0) // Thu Sep 24 2026, 08:00

describe("formatMeetingTime", () => {
  it("says Today, with minutes only when they aren't :00", () => {
    expect(formatMeetingTime("2026-09-24T09:00:00", "2026-09-24T13:45:00", NOW)).toBe(
      "Today from 9am – 1:45pm",
    )
  })

  it("says Tomorrow for the next day", () => {
    expect(formatMeetingTime("2026-09-25T09:00:00", "2026-09-25T10:30:00", NOW)).toBe(
      "Tomorrow from 9am – 10:30am",
    )
  })

  it("uses the date further out", () => {
    expect(formatMeetingTime("2026-09-26T14:00:00", "2026-09-26T15:00:00", NOW)).toBe(
      "Sep 26 from 2pm – 3pm",
    )
  })
})
```

Create `src/components/UpcomingMeetings.test.tsx`:

```tsx
import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { server } from "@/mocks/node"
import { renderWithQuery } from "@/test/render"
import { UpcomingMeetings } from "./UpcomingMeetings"

describe("UpcomingMeetings", () => {
  it("lists meetings from the API", async () => {
    await renderWithQuery(<UpcomingMeetings />)
    expect(await screen.findAllByText("The Grid Startup Meeting")).toHaveLength(4)
    expect(screen.getAllByText("Today from 9am – 1:45pm")).toHaveLength(4)
  })

  it("respects limit", async () => {
    await renderWithQuery(<UpcomingMeetings limit={2} />)
    expect(await screen.findAllByText("The Grid Startup Meeting")).toHaveLength(2)
  })

  it("removes the loading placeholder once the meetings arrive", async () => {
    await renderWithQuery(<UpcomingMeetings />)
    await screen.findAllByText("The Grid Startup Meeting")
    expect(screen.queryByRole("status", { name: "Loading meetings" })).not.toBeInTheDocument()
  })

  it("shows an error with Retry, and Retry recovers", async () => {
    const user = userEvent.setup()
    server.use(http.get("/api/meetings", () => HttpResponse.json({ message: "down" }, { status: 500 }), { once: true }))
    await renderWithQuery(<UpcomingMeetings />)

    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't load meetings.")
    await user.click(screen.getByRole("button", { name: "Retry" }))
    expect(await screen.findAllByText("The Grid Startup Meeting")).toHaveLength(4)
  })
})
```

Create `src/components/ProjectQuickLinks.test.tsx`:

```tsx
import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { server } from "@/mocks/node"
import { renderWithRouter } from "@/test/render"
import { ProjectQuickLinks } from "./ProjectQuickLinks"

describe("ProjectQuickLinks", () => {
  it("shows the first four projects from the API, each linking to its overview", async () => {
    await renderWithRouter(<ProjectQuickLinks />)
    const links = await screen.findAllByRole("link")
    expect(links.map((link) => link.getAttribute("href"))).toEqual([
      "/projects/the-grid",
      "/projects/melting-glaciers",
      "/projects/night-shift",
      "/projects/urban-heat-islands",
    ])
    expect(screen.getByRole("heading", { name: "The Grid" })).toBeInTheDocument()
    expect(screen.getByText("past due tasks", { exact: false })).toBeInTheDocument()
  })

  it("shows an error with Retry, and Retry recovers", async () => {
    const user = userEvent.setup()
    server.use(http.get("/api/projects", () => HttpResponse.json({ message: "down" }, { status: 500 }), { once: true }))
    await renderWithRouter(<ProjectQuickLinks />)

    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't load your projects.")
    await user.click(screen.getByRole("button", { name: "Retry" }))
    expect(await screen.findAllByRole("link")).toHaveLength(4)
  })
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `pnpm test src/lib/meeting-time.test.ts src/components/UpcomingMeetings.test.tsx src/components/ProjectQuickLinks.test.tsx`
Expected: FAIL: `./meeting-time` doesn't exist; the components still render hard-coded data, so the error tests find no alert.

- [ ] **Step 3: Meeting time label**

Create `src/lib/meeting-time.ts`:

```ts
import { addDays, format, isSameDay, parseISO } from "date-fns"

/** "9am", or "1:45pm" when the minutes aren't :00 */
function clock(date: Date) {
  return format(date, date.getMinutes() === 0 ? "haaa" : "h:mmaaa")
}

/**
 * A meeting's time for its card: "Today from 9am – 1:45pm",
 * "Tomorrow from …", or "Sep 26 from …" further out.
 */
export function formatMeetingTime(start: string, end: string, now = new Date()) {
  const from = parseISO(start)
  const day = isSameDay(from, now)
    ? "Today"
    : isSameDay(from, addDays(now, 1))
      ? "Tomorrow"
      : format(from, "MMM d")
  return `${day} from ${clock(from)} – ${clock(parseISO(end))}`
}
```

- [ ] **Step 4: Meetings from the API**

In `src/components/UpcomingMeetings.tsx`:
- replace the `Meeting` type and the `MEETINGS` constant with imports: `import { useMeetings } from "@/api/queries"`, `import { LoadError, LoadingBlock } from "@/components/ui/load-state"`, `import { formatMeetingTime } from "@/lib/meeting-time"`;
- change the signature's default to `limit = Infinity`;
- at the top of the component: `const meetings = useMeetings()`;
- replace the `<div className="mt-4 flex w-full flex-col …">` element's children so the component's body becomes:

```tsx
      {meetings.isPending ? (
        <LoadingBlock label="Loading meetings" className="mt-4 h-64 md:mt-panel" />
      ) : meetings.isError ? (
        <div className="mt-4 md:mt-panel">
          <LoadError message="Couldn't load meetings." onRetry={() => void meetings.refetch()} />
        </div>
      ) : (
        <div className="mt-4 flex w-full flex-col gap-2 md:mt-panel md:grid md:grid-cols-2 md:gap-3.75 lg:flex">
          {meetings.data.slice(0, limit).map((meeting, index) => (
            <div
              key={meeting.id}
              className={`${index >= TABLET_ONLY_FROM ? "hidden md:flex lg:hidden" : "flex"} min-w-0 items-center gap-4 2xl:pl-[12.6%]`}
            >
              {/* card markup exactly as before */}
              …
                <p className="text-13 text-text-primary">
                  {formatMeetingTime(meeting.start, meeting.end)}
                </p>
              …
            </div>
          ))}
        </div>
      )}
```

Keep every existing class on the card elements unchanged; only the data source, the `key`, and the time line change. `Array.prototype.slice(0, Infinity)` returns every meeting.

- [ ] **Step 5: Quick links from the API**

In `src/components/ProjectQuickLinks.tsx`:
- remove the `ProjectQuickLink` type and the `PROJECTS` constant; add `import { useProjects } from "@/api/queries"`, `import type { Project } from "@/api/types"`, `import { LoadError, LoadingBlock } from "@/components/ui/load-state"`;
- change the card to take a project: `function ProjectQuickLinkCard({ project }: { project: Project })`, and inside use `project.id` in the `Link` params, `project.title` for the heading, `project.attention` where it used `status` (the bold-first-word split stays), `project.daysRemaining`, `project.progress`;
- add above the component: `// Home shows four cards: a 2×2 grid on desktop, a swipeable row on phones` and `const QUICK_LINK_COUNT = 4`;
- in `ProjectQuickLinks`, `const projects = useProjects()`, and render inside the existing `<section>` after the `<h2>`:

```tsx
      {projects.isPending ? (
        <LoadingBlock label="Loading projects" className="h-40" />
      ) : projects.isError ? (
        <LoadError message="Couldn't load your projects." onRetry={() => void projects.refetch()} />
      ) : (
        <div className="@container -mx-5 flex w-[calc(100%+40px)] …exactly the existing classes…">
          {projects.data.slice(0, QUICK_LINK_COUNT).map((project) => (
            <ProjectQuickLinkCard key={project.id} project={project} />
          ))}
        </div>
      )}
```

- [ ] **Step 6: Run the tests**

Run: `pnpm test src/lib/meeting-time.test.ts src/components/UpcomingMeetings.test.tsx src/components/ProjectQuickLinks.test.tsx src/routes.test.tsx`
Expected: all pass. (In date-fns, `aaa` is the lowercase `am`/`pm` token, so `haaa` prints `9am` and `h:mmaaa` prints `1:45pm`.)

- [ ] **Step 7: Full run, look, commit**

```bash
pnpm typecheck && pnpm test && pnpm lint
```

`pnpm dev`, open `/`: the quick links and meetings flash a pulsing placeholder for ~150 ms, then show the same layout as before.

```bash
git add -A src
git commit -m "Upcoming meetings and project quick links from the API

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: DataTable controlled rows

Let a page own a table's rows: the table renders `data` as given and reports edits, deletes and adds through callbacks instead of changing a private copy. Tables that don't pass the callbacks keep today's behavior until plan 2 moves them to the API.

**Files:**
- Modify: `src/components/ui/data-table/types.ts`, `src/components/ui/data-table/data-table.tsx`
- Test: `src/components/ui/data-table/data-table.test.tsx`

**Interfaces:**
- Produces: `DataTableProps<T>` gains `onUpdateRow?: (rowId: string, patch: Partial<T>) => void`, `onRemoveRow?: (rowId: string) => void`, `onAddRow?: (row: T) => void`. Passing any of them switches the table to controlled rows.

- [ ] **Step 1: Write the failing tests**

Add to the end of `src/components/ui/data-table/data-table.test.tsx`:

```tsx
describe("controlled rows", () => {
  const firstColumn = () =>
    screen
      .getAllByRole("row")
      .slice(1)
      .map((row) => within(row).getAllByRole("cell")[0].textContent)

  const DELETE_COLUMNS: DataTableColumn<Task>[] = [
    ...COLUMNS,
    {
      key: "actions",
      label: "",
      type: "actions",
      actions: [
        { id: "delete", label: "Delete task", icon: "×", confirm: false, onSelect: (_task, { removeRow }) => removeRow() },
      ],
    },
  ]

  it("reports an inline edit and shows data only as passed in", async () => {
    const user = userEvent.setup()
    const onUpdateRow = vi.fn()
    const { rerender } = render(
      <DataTable columns={COLUMNS} data={TASKS} getRowId={(task) => task.id} users={USERS} onUpdateRow={onUpdateRow} />,
    )

    const statusCell = screen.getByRole("row", { name: /Write copy/ }).querySelector('[data-column-id="status"]')!
    await user.click(within(statusCell as HTMLElement).getByRole("button"))
    await user.click(screen.getByRole("option", { name: "Done" }))

    expect(onUpdateRow).toHaveBeenCalledWith("t1", { status: "done" })
    // Still To Do: the page hasn't passed new data yet
    expect(within(statusCell as HTMLElement).getByText("To Do")).toBeInTheDocument()

    rerender(
      <DataTable
        columns={COLUMNS}
        data={TASKS.map((task) => (task.id === "t1" ? { ...task, status: "done" } : task))}
        getRowId={(task) => task.id}
        users={USERS}
        onUpdateRow={onUpdateRow}
      />,
    )
    const updated = screen.getByRole("row", { name: /Write copy/ }).querySelector('[data-column-id="status"]')!
    expect(within(updated as HTMLElement).getByText("Done")).toBeInTheDocument()
  })

  it("reports a delete without removing the row itself", async () => {
    const user = userEvent.setup()
    const onRemoveRow = vi.fn()
    render(
      <DataTable columns={DELETE_COLUMNS} data={TASKS} getRowId={(task) => task.id} users={USERS} onRemoveRow={onRemoveRow} />,
    )

    const row = screen.getByRole("row", { name: /Audit icons/ })
    await user.click(within(row).getByRole("button", { name: "Delete task" }))

    expect(onRemoveRow).toHaveBeenCalledWith("t2")
    expect(firstColumn()).toContain("Audit icons")
  })

  it("still works uncontrolled when no row callbacks are passed", async () => {
    const user = userEvent.setup()
    render(<DataTable columns={DELETE_COLUMNS} data={TASKS} getRowId={(task) => task.id} users={USERS} />)
    await user.click(within(screen.getByRole("row", { name: /Audit icons/ })).getByRole("button", { name: "Delete task" }))
    expect(firstColumn()).not.toContain("Audit icons")
  })
})
```

- [ ] **Step 2: Run to verify they fail**

Run: `pnpm test src/components/ui/data-table/data-table.test.tsx`
Expected: the first two new tests fail (callbacks never called; the table edits its own copy); the third passes.

- [ ] **Step 3: Implement**

In `src/components/ui/data-table/types.ts`, add to `DataTableProps<T>`:

```ts
  /**
   * Controlled rows: pass these and the table shows `data` exactly as
   * given, reporting edits, deletes and adds here instead of changing its
   * own copy. The page updates `data` (usually through a query and a
   * mutation). Leave all three out and the table keeps its own copy.
   */
  onUpdateRow?: (rowId: string, patch: Partial<T>) => void
  onRemoveRow?: (rowId: string) => void
  onAddRow?: (row: T) => void
```

Also update the `RowActionHelpers` doc comment's warning ("Call exactly ONE…") by adding a sentence: "In controlled-rows mode each call goes straight to the page's callback, so this limit applies only to uncontrolled tables."

In `src/components/ui/data-table/data-table.tsx`, add `onUpdateRow`, `onRemoveRow`, `onAddRow` to the destructured props, and replace the `rows` state and the `commit`/`updateRow`/`removeRow`/`addRow` callbacks with:

```tsx
  const controlledRows =
    onUpdateRow !== undefined || onRemoveRow !== undefined || onAddRow !== undefined
  // Uncontrolled tables keep a copy of their starting data and edit that
  const [ownRows, setOwnRows] = useState(data)
  const rows = controlledRows ? data : ownRows

  const commit = useCallback(
    (next: T[]) => {
      setOwnRows(next)
      onChange?.(next)
    },
    [onChange],
  )

  const updateRow = useCallback(
    (rowId: string, patch: Partial<T>) => {
      if (controlledRows) onUpdateRow?.(rowId, patch)
      else commit(rows.map((row) => (getRowId(row) === rowId ? { ...row, ...patch } : row)))
    },
    [controlledRows, onUpdateRow, commit, rows, getRowId],
  )

  const removeRow = useCallback(
    (rowId: string) => {
      if (controlledRows) onRemoveRow?.(rowId)
      else commit(rows.filter((row) => getRowId(row) !== rowId))
    },
    [controlledRows, onRemoveRow, commit, rows, getRowId],
  )

  const addRow = useCallback(
    (row: T) => {
      if (controlledRows) onAddRow?.(row)
      else commit([...rows, row])
    },
    [controlledRows, onAddRow, commit, rows],
  )
```

- [ ] **Step 4: Run and commit**

```bash
pnpm test src/components/ui/data-table && pnpm typecheck && pnpm lint
git add src/components/ui/data-table
git commit -m "DataTable: controlled rows through onUpdateRow / onRemoveRow / onAddRow

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

Expected: all data-table tests pass.

---

### Task 5: Task list on the API

**Files:**
- Create: `src/api/mutations.ts`
- Modify: `src/components/TaskList.tsx`, `src/pages/ProjectOverview.tsx`, `src/pages/ProjectTasks.tsx`
- Test: `src/components/TaskList.test.tsx`

**Interfaces:**
- Consumes: `useTasks`, `useCrew`, `queryKeys` (Task 2); `apiFetch`, `Task`, `NewTask`, `TaskPatch` (Task 1); controlled-rows props (Task 4); `LoadingBlock`, `LoadError`, `Toaster` (Task 2); `renderWithRouter` (Task 2, returns `queryClient`); `useTableUrlState` and `syncWithUrl` (plan 1).
- Produces: `useCreateTask()`, `useUpdateTask()` (variables `{ id: string; patch: TaskPatch }`), `useDeleteTask()` (variables: task id). `TaskList` gains prop `projectId?: string`.

- [ ] **Step 1: Rewrite the task list tests against the mock API**

Replace `src/components/TaskList.test.tsx` with the version below. It keeps every existing behavior test (now async, reading from the mock API) and adds the API-specific ones.

```tsx
import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { queryKeys } from "@/api/queries"
import type { Task } from "@/api/types"
import { Toaster } from "@/components/ui/sonner"
import { celebrate } from "@/lib/burst"
import { db } from "@/mocks/db"
import { server } from "@/mocks/node"
import { renderWithRouter } from "@/test/render"
import { TaskList } from "./TaskList"

vi.mock("@/lib/burst", () => ({ celebrate: vi.fn() }))

function rowNames() {
  const [, ...bodyRows] = screen.getAllByRole("row")
  return bodyRows.map((row) => within(row).getAllByRole("cell")[0].textContent)
}

async function renderList(props: Parameters<typeof TaskList>[0] = {}) {
  const result = await renderWithRouter(
    <>
      <TaskList {...props} />
      <Toaster />
    </>,
  )
  await screen.findByRole("columnheader", { name: /Task Name/ })
  return result
}

function cellOf(taskName: string, columnId: string) {
  const row = screen.getByRole("row", { name: new RegExp(taskName) })
  return row.querySelector(`[data-column-id="${columnId}"]`) as HTMLElement
}

async function setSelect(taskName: string, columnId: string, label: string) {
  const user = userEvent.setup()
  await user.click(within(cellOf(taskName, columnId)).getByRole("button"))
  await user.click(screen.getByRole("option", { name: label }))
}

describe("TaskList", () => {
  it("renders the Tasks heading and a New Task button", async () => {
    await renderList()
    expect(screen.getByRole("heading", { name: "Tasks" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "New Task" })).toBeInTheDocument()
  })

  it("renders every expected column header", async () => {
    await renderList()
    for (const label of ["Task Name", "Assignee", "Due Date", "Status", "Priority"]) {
      expect(screen.getByRole("columnheader", { name: new RegExp(label) })).toBeInTheDocument()
    }
  })

  it("loads its rows from the API", async () => {
    await renderList()
    expect(rowNames()).toEqual(["Draft launch copy", "Audit icon set", "Ship static export", "Review crew roster"])
  })

  it("shows only one project's tasks when given a projectId", async () => {
    await renderList({ projectId: "melting-glaciers" })
    expect(rowNames()).toEqual(["Audit icon set", "Review crew roster"])
  })

  it("resolves assignees to crew names from the API", async () => {
    await renderList()
    expect(screen.getByText("@MarcusChen")).toBeInTheDocument()
  })

  it("sorts by due date chronologically", async () => {
    const user = userEvent.setup()
    await renderList()
    await user.click(screen.getByRole("columnheader", { name: /Due Date/ }))
    expect(rowNames()).toEqual(["Draft launch copy", "Ship static export", "Audit icon set", "Review crew roster"])
  })

  it("saves an inline edit to the server", async () => {
    await renderList()
    await setSelect("Audit icon set", "priority", "High")
    expect(within(cellOf("Audit icon set", "priority")).getByText("High")).toBeInTheDocument()
    await vi.waitFor(() => expect(db.tasks.find((task) => task.id === "t2")?.priority).toBe("high"))
  })

  it("puts the old value back and says so when the server rejects an edit", async () => {
    server.use(http.patch("/api/tasks/:id", () => HttpResponse.json({ message: "nope" }, { status: 500 })))
    await renderList()

    await setSelect("Audit icon set", "status", "Blocked")

    expect(await screen.findByText("Couldn't save the task")).toBeInTheDocument()
    await vi.waitFor(() =>
      expect(within(cellOf("Audit icon set", "status")).getByText("To Do")).toBeInTheDocument(),
    )
  })

  it("updates every cached task list, so other tables show the edit", async () => {
    const { queryClient } = await renderList()
    queryClient.setQueryData(queryKeys.tasks.list("melting-glaciers"), db.tasks.filter((task) => task.project === "melting-glaciers"))

    await setSelect("Audit icon set", "priority", "High")

    const other = queryClient.getQueryData<Task[]>(queryKeys.tasks.list("melting-glaciers"))
    expect(other?.find((task) => task.id === "t2")?.priority).toBe("high")
  })

  it("deletes a row on the server", async () => {
    const user = userEvent.setup()
    await renderList()
    const row = screen.getByRole("row", { name: /Ship static export/ })
    await user.click(within(row).getByRole("button", { name: "Delete task" }))
    await user.click(screen.getByRole("button", { name: "Yes" }))

    await vi.waitFor(() => expect(rowNames()).not.toContain("Ship static export"))
    expect(db.tasks.map((task) => task.id)).not.toContain("t3")
  })

  it("adds a task through the server when New Task is clicked", async () => {
    const user = userEvent.setup()
    await renderList({ projectId: "the-grid" })
    await user.click(screen.getByRole("button", { name: "New Task" }))

    await vi.waitFor(() => expect(rowNames().at(-1)).toBe("Untitled task"))
    expect(db.tasks.at(-1)).toMatchObject({ name: "Untitled task", project: "the-grid", status: "todo", priority: "medium" })
  })

  it("shows an error with Retry when the tasks don't load", async () => {
    const user = userEvent.setup()
    server.use(http.get("/api/tasks", () => HttpResponse.json({ message: "down" }, { status: 500 }), { once: true }))
    await renderWithRouter(<TaskList />)

    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't load tasks.")
    await user.click(screen.getByRole("button", { name: "Retry" }))
    expect(await screen.findByRole("columnheader", { name: /Task Name/ })).toBeInTheDocument()
  })

  describe("Project column (top-level Tasks page)", () => {
    it("is left off by default", async () => {
      await renderList()
      expect(screen.queryByRole("columnheader", { name: /Project/ })).not.toBeInTheDocument()
    })

    it("shows each task's project right after Task Name when enabled", async () => {
      await renderList({ showProject: true })
      const headers = screen.getAllByRole("columnheader").map((header) => header.textContent)
      expect(headers.slice(0, 2)).toEqual(["Task Name", "Project"])
      expect(screen.getAllByText("The Grid")).toHaveLength(2)
      expect(screen.getAllByText("Melting Glaciers")).toHaveLength(2)
    })

    it("filters tasks by project, like assignees", async () => {
      const user = userEvent.setup()
      await renderList({ showProject: true })
      const header = screen.getByRole("columnheader", { name: /Project/ })
      await user.click(within(header).getByRole("button", { name: /filter/i }))
      await user.click(screen.getByRole("option", { name: "Melting Glaciers" }))
      expect(rowNames()).toEqual(["Audit icon set", "Review crew roster"])
    })
  })

  describe("done-status celebration", () => {
    beforeEach(() => vi.mocked(celebrate).mockClear())

    it("celebrates when a task is changed to Done", async () => {
      await renderList()
      await setSelect("Draft launch copy", "status", "Done")
      expect(celebrate).toHaveBeenCalledTimes(1)
    })

    it("doesn't burst for other status changes", async () => {
      await renderList()
      await setSelect("Draft launch copy", "status", "Blocked")
      expect(celebrate).not.toHaveBeenCalled()
    })

    it("doesn't burst when a task that's already done is edited", async () => {
      await renderList()
      await setSelect("Review crew roster", "priority", "High")
      await setSelect("Review crew roster", "status", "Done")
      expect(celebrate).not.toHaveBeenCalled()
    })
  })
})
```

If the existing file had tests not listed above (for example the "has no per-column filter on Task Name" test), keep them, converted to `await renderList(...)` the same way. The delete confirm button may be named differently from `"Yes"`; check `ConfirmDialog`'s default `confirmLabel` (it is `"Yes"`) and the task's delete action (it has no `confirm` override, and destructive actions confirm by default).

- [ ] **Step 2: Run to verify the new tests fail**

Run: `pnpm test src/components/TaskList.test.tsx`
Expected: the API tests fail (`projectId` ignored, `db` unchanged by edits, no error state); most behavior tests pass since the inline data still matches the seed.

- [ ] **Step 3: Mutations**

Create `src/api/mutations.ts`:

```ts
import { type QueryClient, type QueryKey, useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { apiFetch } from "./client"
import { queryKeys } from "./queries"
import type { NewTask, Task, TaskPatch } from "./types"

// Writes. Edits and deletes are optimistic: every cached task list changes
// at once, rolls back if the server says no, and is refetched either way.

type Snapshot = [QueryKey, Task[] | undefined][]

/** Stops in-flight task fetches, saves every cached task list, applies `change` to each */
async function changeTaskLists(queryClient: QueryClient, change: (tasks: Task[]) => Task[]) {
  await queryClient.cancelQueries({ queryKey: queryKeys.tasks.all })
  const snapshot: Snapshot = queryClient.getQueriesData<Task[]>({ queryKey: queryKeys.tasks.all })
  queryClient.setQueriesData<Task[]>({ queryKey: queryKeys.tasks.all }, (tasks) => tasks && change(tasks))
  return snapshot
}

function restore(queryClient: QueryClient, snapshot: Snapshot | undefined) {
  for (const [key, tasks] of snapshot ?? []) queryClient.setQueryData(key, tasks)
}

function refetchTasks(queryClient: QueryClient) {
  return queryClient.invalidateQueries({ queryKey: queryKeys.tasks.all })
}

export function useCreateTask() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (task: NewTask) => apiFetch<Task>("/api/tasks", { method: "POST", body: task }),
    onError: () => toast.error("Couldn't create the task"),
    onSettled: () => refetchTasks(queryClient),
  })
}

export function useUpdateTask() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ id, patch }: { id: string; patch: TaskPatch }) =>
      apiFetch<Task>(`/api/tasks/${encodeURIComponent(id)}`, { method: "PATCH", body: patch }),
    onMutate: ({ id, patch }) =>
      changeTaskLists(queryClient, (tasks) =>
        tasks.map((task) => (task.id === id ? { ...task, ...patch } : task)),
      ),
    onError: (_error, _variables, snapshot) => {
      restore(queryClient, snapshot)
      toast.error("Couldn't save the task")
    },
    onSettled: () => refetchTasks(queryClient),
  })
}

export function useDeleteTask() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<void>(`/api/tasks/${encodeURIComponent(id)}`, { method: "DELETE" }),
    onMutate: (id) => changeTaskLists(queryClient, (tasks) => tasks.filter((task) => task.id !== id)),
    onError: (_error, _id, snapshot) => {
      restore(queryClient, snapshot)
      toast.error("Couldn't delete the task")
    },
    onSettled: () => refetchTasks(queryClient),
  })
}
```

- [ ] **Step 4: The task list reads and writes through the API**

In `src/components/TaskList.tsx`:
- remove `import { useRef } from "react"`, `import { CREW } from "@/lib/crew"`, the local `Task` type, the `TASKS` constant, `nextTaskId` and `blankTask`;
- add:

```tsx
import { useCreateTask, useDeleteTask, useUpdateTask } from "@/api/mutations"
import { useCrew, useTasks } from "@/api/queries"
import type { Task, TaskPatch } from "@/api/types"
import { LoadError, LoadingBlock } from "@/components/ui/load-state"
```

- replace `celebrateNewlyDone` with:

```tsx
// Easter egg: marking a task done sets off a celebration from its status
// badge — one of several, picked at random (see celebrate() in
// src/lib/burst.ts).
function celebrateIfNewlyDone(task: Task | undefined, patch: TaskPatch) {
  if (!task || patch.status !== "done" || task.status === "done") return
  const cell = document.querySelector(
    `[data-row-id="${CSS.escape(task.id)}"] [data-column-id="status"]`,
  )
  const badge = cell?.querySelector("[data-slot=badge]") ?? cell
  if (!badge) return
  const rect = badge.getBoundingClientRect()
  celebrate(rect.left + rect.width / 2, rect.top + rect.height / 2)
}
```

- replace the `TaskList` component (keep its doc comment, adding a line: "`projectId` shows only that project's tasks, and new tasks join that project.") with:

```tsx
export function TaskList({
  showProject = false,
  showTitleIcon = true,
  syncWithUrl = false,
  projectId,
}: {
  showProject?: boolean
  showTitleIcon?: boolean
  syncWithUrl?: boolean
  projectId?: string
}) {
  const tasks = useTasks({ projectId })
  const crew = useCrew()
  const createTask = useCreateTask()
  const updateTask = useUpdateTask()
  const deleteTask = useDeleteTask()
  const columns = showProject ? COLUMNS_WITH_PROJECT : COLUMNS
  const urlState = useTableUrlState(columns, { enabled: syncWithUrl })

  if (tasks.isPending || crew.isPending) return <LoadingBlock label="Loading tasks" className="h-80" />
  if (tasks.isError || crew.isError) {
    return (
      <LoadError
        message="Couldn't load tasks."
        onRetry={() => {
          if (tasks.isError) void tasks.refetch()
          if (crew.isError) void crew.refetch()
        }}
      />
    )
  }

  return (
    <DataTable
      columns={columns}
      data={tasks.data}
      getRowId={(task) => task.id}
      onUpdateRow={(id, patch) => {
        celebrateIfNewlyDone(tasks.data.find((task) => task.id === id), patch)
        updateTask.mutate({ id, patch })
      }}
      onRemoveRow={(id) => deleteTask.mutate(id)}
      users={crew.data}
      title="Tasks"
      titleIcon={showTitleIcon ? <TasksIcon size={24} className="text-text-primary" /> : undefined}
      {...urlState}
      toolbarActions={
        <Button
          variant="outline"
          size="sm"
          disabled={createTask.isPending}
          onClick={() =>
            createTask.mutate({
              name: "Untitled task",
              project: projectId ?? "",
              assignee: "",
              due: "",
              status: "todo",
              priority: "medium",
            })
          }
          // Outlined buttons fill with their border color on hover; in dark
          // mode, highlight with dark text (white on it is only ~3:1)
          className="rounded-full border border-text-primary bg-transparent px-4 text-text-primary hover:bg-text-primary hover:text-background dark:hover:border-highlight dark:hover:bg-highlight dark:hover:text-text-dark"
        >
          New Task
        </Button>
      }
    />
  )
}
```

Keep `PROJECT_OPTIONS`, the status/priority options and the column constants as they are.

`src/pages/ProjectOverview.tsx` and `src/pages/ProjectTasks.tsx`: add `import { useParams } from "@tanstack/react-router"`, read `const { projectId } = useParams({ from: "/projects/$projectId" })` at the top, and pass `projectId={projectId}` to their `<TaskList … />`.

- [ ] **Step 5: Run everything**

```bash
pnpm typecheck && pnpm test && pnpm lint
```

Expected: all pass. The route tests for `/projects/the-grid/tasks` now show only The Grid's tasks, which they don't assert against; `src/url-state.test.tsx` from plan 1 filters `/tasks?status=blocked` to "Ship static export", which the API data still gives. If a plan-1 URL test now needs to wait for rows, change its first query after `renderApp` to `await screen.findByRole("columnheader", { name: /Task Name/ })`.

- [ ] **Step 6: Look, commit**

`pnpm dev`, `/`: change a task's status to Done (the burst plays), delete a task, add one. Reload: the changes are gone (in-memory mock data), which is expected. Open `/projects/melting-glaciers/tasks`: only its two tasks.

```bash
git add -A src
git commit -m "Task list reads and writes through the API

Optimistic edits and deletes across every cached list, rolled back with
a toast when the server refuses. Project pages show their own tasks.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: API document for the backend developer, and final check

**Files:**
- Create: `docs/API.md`

**Interfaces:** consumes everything above; no code changes unless a check fails.

- [ ] **Step 1: Write `docs/API.md`**

Plain language (see the spec's writing style). It must contain, in this order:

1. **Purpose** (two sentences): the frontend calls these endpoints; MSW answers them with dummy data in development (`src/mocks/handlers.ts` is a working reference implementation); the Go server replaces MSW.
2. **Running the frontend against Go:** `pnpm dev` uses mocks; `VITE_API_MOCKS=off pnpm dev` proxies `/api` to `http://localhost:8080`, or `VITE_API_TARGET` (see `.env.example`).
3. **Serving the built app** (spec section 2): `pnpm build` → `dist/`; serve `dist/assets/*` with long cache headers; answer `/api/*`; return `dist/index.html` for every other GET.
4. **Conventions:** JSON bodies; ISO dates (`YYYY-MM-DD`) and date-times (`YYYY-MM-DDTHH:mm:ss`); people by user id, never display name (with the why from `docs/TASKS-BACKEND.md`: ids resolve against the crew roster for avatars and pickers; display names don't); errors as `{ "message": string }`, validation as `422 { "message", "errors": { field: message } }`; `404` for an unknown id; `204` with no body for a delete.
5. **Endpoints**, one subsection each, with method + path, query params, request body (for writes), response status and a JSON example copied from `src/mocks/db.ts`: `GET /api/me`, `GET /api/crew`, `GET /api/projects`, `GET /api/tasks?projectId=`, `POST /api/tasks` (201, returns the task with its new id), `PATCH /api/tasks/:id` (partial body; returns the full task), `DELETE /api/tasks/:id` (204), `GET /api/meetings?limit=`.
6. **Allowed values** for task `status` (`todo`, `in-progress`, `done`, `blocked`) and `priority` (`low`, `medium`, `high`), project `status` (`litmus`, `on-hold`, `accepted`) and `type` (`data-story`, `short-story`): the `value` strings are the contract; labels and colors live in the frontend.
7. **Not yet on the API** (one list): ideas, the Projects table's writes, documents, intake page data, project summary card, project crew, column-layout preferences, forms. These still use inline dummy data and follow in later plans.

- [ ] **Step 2: All checks**

```bash
pnpm typecheck && pnpm lint && pnpm test && pnpm build
grep -c "mockServiceWorker\|setupWorker" dist/assets/*.js || true
```

Expected: all pass; the grep prints `0` per file.

- [ ] **Step 3: Mocks off really goes to the proxy**

```bash
VITE_API_MOCKS=off pnpm dev --port 5175 &
sleep 3
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:5175/api/me
kill %1
```

Expected: `500` or `502` (nothing listens on 8080, so Vite's proxy fails): the request went to the proxy instead of being answered by MSW or returning `index.html` (`200`).

- [ ] **Step 4: Click through**

`pnpm dev` at desktop and phone widths: Home (greeting, quick links linking to four different projects, meetings, task list edits/delete/add with the burst on Done), Tasks (Project column and filter), a project's Overview and Tasks (only that project's tasks). Ideas, Projects and Documents look and work as before. To see an error toast: in DevTools' Network tab, set throttling to "Offline", change a task's status, and check the toast appears and the status goes back. Do it in light and dark mode to check the toast reads in both.

- [ ] **Step 5: Commit**

```bash
git add docs/API.md
git commit -m "API document for the Go backend (Home's endpoints)

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```
