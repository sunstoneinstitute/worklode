import { keepPreviousData, queryOptions, useQuery, useSuspenseQuery } from "@tanstack/react-query"
import { api } from "./client"
import type { components } from "./schema"
import type { ErrorResponse } from "./types"

/** Full-page navigation, behind an object so tests can spy on it. */
export const browser = {
  assign: (url: string) => window.location.assign(url),
}

/** A non-2xx answer, carrying its status so the query client can decide on a retry. */
export class HttpError extends Error {
  status: number
  /** A 422's per-field messages, keyed by wire name (WL-REQ-1811) */
  fields?: Record<string, string>

  constructor(status: number, message: string, fields?: Record<string, string>) {
    super(message)
    this.name = "HttpError"
    this.status = status
    this.fields = fields
  }
}

/** Thrown after starting the login redirect, so no error page renders meanwhile. A 4xx, so never retried. */
export class LoginRedirect extends HttpError {
  constructor() {
    super(401, "login required")
  }
}

/** A body save's 409 carries a BodyConflict or BodyLock, not an ErrorResponse; its message is the status line. */
export function failure(error: ErrorResponse | object, response: Response) {
  const e = error as ErrorResponse
  return new HttpError(response.status, e.error ?? `${response.status} ${response.statusText}`.trim(), e.fields)
}

/**
 * Query keys follow the URL path, so everything under /api/v1/tasks is
 * `["tasks", ...]` and one invalidateQueries({ queryKey: ["tasks"] })
 * refreshes every task list, whatever its filter.
 */
export const queryKeys = {
  me: ["whoami"] as const,
  crew: ["crew"] as const,
  projects: {
    all: ["projects"] as const,
    list: ["projects"] as const,
    detail: (id: string) => ["projects", "detail", id] as const,
    crew: (id: string) => ["projects", "crew", id] as const,
    summary: (id: string) => ["projects", "summary", id] as const,
  },
  tasks: {
    all: ["tasks"] as const,
    list: (project?: string, states?: string[]) =>
      ["tasks", { project: project ?? null, states: states ?? null }] as const,
  },
  documents: {
    all: ["documents"] as const,
    list: (project: string) => ["documents", { project }] as const,
    idea: (idea: string) => ["documents", { idea }] as const,
  },
  ideas: {
    all: ["ideas"] as const,
    list: ["ideas", "list"] as const,
    detail: (id: string) => ["ideas", "detail", id] as const,
    litmusTests: (id: string) => ["ideas", "litmus-tests", id] as const,
    preResearch: (id: string) => ["ideas", "pre-research", id] as const,
  },
  docs: (project: string, kind: string) => ["docs", { project, kind }] as const,
  rules: (project: string) => ["rules", { project }] as const,
}

export const meQueryOptions = queryOptions({
  queryKey: queryKeys.me,
  queryFn: async () => {
    const { data, error, response } = await api.GET("/api/v1/whoami")
    if (response.status === 401) {
      // WL-REQ-1308: no session sends the browser to log in and come back here
      browser.assign(`/auth/login?next=${encodeURIComponent(location.pathname + location.search)}`)
      throw new LoginRedirect()
    }
    if (error) throw failure(error, response)
    return data
  },
})

/** The signed-in actor. The root route's loader fetches it before any page renders. */
export function useMe() {
  return useSuspenseQuery(meQueryOptions).data
}

export function useProjects() {
  return useQuery({
    queryKey: queryKeys.projects.list,
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/projects")
      if (error) throw failure(error, response)
      return data.projects ?? []
    },
  })
}

export function useProject(id: string) {
  return useQuery({
    queryKey: queryKeys.projects.detail(id),
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/projects/{id}", { params: { path: { id } } })
      if (error) throw failure(error, response)
      return data
    },
  })
}

export function useCrew() {
  return useQuery({
    queryKey: queryKeys.crew,
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/crew")
      if (error) throw failure(error, response)
      return data.crew ?? []
    },
  })
}

/** The people an x-options route answers: a model.CrewListResponse or model.ParticipantListResponse (WL-REQ-1887) */
export function useOptions(url: string | undefined) {
  return useQuery({
    queryKey: ["options", url],
    enabled: url !== undefined,
    queryFn: async () => {
      const { data, error, response } = await api.GET(url as "/api/v1/crew")
      if (error) throw failure(error, response)
      const { crew, participants } = data as components["schemas"]["CrewListResponse"] & components["schemas"]["ParticipantListResponse"]
      return (crew ?? participants ?? []).map((member) => ({
        id: member.actor ?? "",
        name: member.display_name || (member.actor ?? ""),
        roles: member.roles,
      }))
    },
  })
}

export const projectCrewKey = queryKeys.projects.crew

async function fetchProjectCrew(id: string) {
  const { data, error, response } = await api.GET("/api/v1/projects/{id}/participants", {
    params: { path: { id } },
  })
  if (error) throw failure(error, response)
  return data
}

/** A project's Crew roster */
export function useProjectCrew(id: string) {
  return useQuery({
    queryKey: projectCrewKey(id),
    queryFn: () => fetchProjectCrew(id),
    select: (data) => data.participants ?? [],
  })
}

/** A project's pending lead handoff, or undefined when none is open (WL-REQ-1605) */
export function useLeadHandoff(id: string) {
  return useQuery({
    queryKey: projectCrewKey(id),
    queryFn: () => fetchProjectCrew(id),
    select: (data) => data.lead_handoff,
  })
}

/**
 * Tasks, optionally those of one project and in some states
 * (`GET /api/v1/tasks?project=&state=`). Bodies are left out (WL-REQ-1846).
 */
export function useTasks({ project, states }: { project?: string; states?: string[] } = {}) {
  return useQuery({
    queryKey: queryKeys.tasks.list(project, states),
    // A State filter change refetches; keep the old rows up meanwhile
    placeholderData: keepPreviousData,
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/tasks", {
        params: { query: { project, state: states } },
      })
      if (error) throw failure(error, response)
      return data.tasks ?? []
    },
  })
}

/** What is late, what awaits review and each Crew member's load (WL-REQ-1797) */
export function useProjectSummary(id: string) {
  return useQuery({
    queryKey: queryKeys.projects.summary(id),
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/projects/{id}/summary", { params: { path: { id } } })
      if (error) throw failure(error, response)
      return data
    },
  })
}

/** A project's documents register: its files, datasets and links (WL-REQ-1798) */
export function useProjectDocuments(id: string) {
  return useQuery({
    queryKey: queryKeys.documents.list(id),
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/projects/{id}/documents", { params: { path: { id } } })
      if (error) throw failure(error, response)
      return data.documents ?? []
    },
  })
}

/** A project's backbone documents of one kind, "spec" or "plan" (WL-REQ-1805) */
export function useProjectDocs(id: string, kind: "spec" | "plan") {
  return useQuery({
    queryKey: queryKeys.docs(id, kind),
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/docs", { params: { query: { project: id, kind } } })
      if (error) throw failure(error, response)
      return data.docs ?? []
    },
  })
}

/** A project's rules (WL-REQ-1805) */
export function useProjectRules(id: string) {
  return useQuery({
    queryKey: queryKeys.rules(id),
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/rules", { params: { query: { project: id } } })
      if (error) throw failure(error, response)
      return data ?? []
    },
  })
}

/** The caller's private ideas and every shared idea (WL-REQ-1799) */
export function useIdeas() {
  return useQuery({
    queryKey: queryKeys.ideas.list,
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/ideas")
      if (error) throw failure(error, response)
      return data.ideas ?? []
    },
  })
}

/**
 * One idea, for its edit form. Never cached past the form, so the form opens
 * with a fresh read each time (WL-REQ-1810).
 */
export function useIdea(id: string) {
  return useQuery({
    queryKey: queryKeys.ideas.detail(id),
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/ideas/{id}", { params: { path: { id } } })
      if (error) throw failure(error, response)
      return data
    },
    gcTime: 0,
    refetchOnWindowFocus: false,
  })
}

// An idea's findings and sources exist for shared ideas only (WL-REQ-1800);
// a private idea's page passes enabled: false so none of them is requested.

/** A shared idea's litmus tests, newest first */
export function useLitmusTests(id: string, enabled = true) {
  return useQuery({
    queryKey: queryKeys.ideas.litmusTests(id),
    enabled,
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/ideas/{id}/litmus-tests", { params: { path: { id } } })
      if (error) throw failure(error, response)
      return data.litmus_tests ?? []
    },
  })
}

/** A shared idea's latest pre-research findings; empty when there is none */
export function usePreResearch(id: string, enabled = true) {
  return useQuery({
    queryKey: queryKeys.ideas.preResearch(id),
    enabled,
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/ideas/{id}/pre-research", { params: { path: { id } } })
      if (error) throw failure(error, response)
      return data.findings ?? []
    },
  })
}

/** A shared idea's sources, rows of the documents register (WL-REQ-1798) */
export function useIdeaSources(id: string, enabled = true) {
  return useQuery({
    queryKey: queryKeys.documents.idea(id),
    enabled,
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/ideas/{id}/sources", { params: { path: { id } } })
      if (error) throw failure(error, response)
      return data.documents ?? []
    },
  })
}

/**
 * What a body editor page edits (WL-REQ-1868): a document by ref, a rule
 * by ref (WL-REQ-12) or a task by id. `lockId` is what the lock routes
 * name it by: the doc id, the numeric rule id, the task id.
 */
export type BodyTarget = { kind: "doc"; ref: string } | { kind: "rule"; ref: string }

export type BodySubject = {
  lockId: string
  title: string
  /** A rule's heading, which its PUT replaces along with the body */
  heading: string
  /** The document kind ("spec", "plan"); "" for a rule */
  docKind: string
  blocks: components["schemas"]["EditorBlock"][]
  body_sha256: string
  /** The stored Markdown the blocks' offsets count into, and its version: what a thread anchors to */
  body: string
  version: number
  /** Where threads go (WL-REQ-1317) */
  threads: ThreadSubject
}

/**
 * A document or rule as thread subject. `openOn` is the document a new thread
 * is posted to: the document itself, or a document arranging the rule (none
 * when no document arranges it).
 */
export type ThreadSubject = { kind: "doc" | "rule"; id: number; ref: string; openOn?: number }
export type ReviewThread = components["schemas"]["ReviewThread"]

/** A task for its edit form with ?as=blocks, never from cache (WL-REQ-1810) */
export function useTaskRecord(ref: string) {
  return useQuery({
    queryKey: ["body", "task", ref],
    staleTime: 0,
    gcTime: 0,
    refetchOnWindowFocus: false,
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/tasks/{id}", { params: { path: { id: ref }, query: { as: "blocks" } } })
      if (error) throw failure(error, response)
      return data
    },
  })
}

/** Reads the subject with ?as=blocks, never from cache: the hash must be the stored body's now */
export function useBodySubject(target: BodyTarget) {
  return useQuery({
    queryKey: ["body", target.kind, target.ref],
    staleTime: 0,
    gcTime: 0,
    queryFn: async (): Promise<BodySubject> => {
      const query = { as: "blocks" }
      if (target.kind === "rule") {
        const { data, error, response } = await api.GET("/api/v1/rules/{id}", { params: { path: { id: target.ref }, query } })
        if (error) throw failure(error, response)
        const heading = data.heading ?? ""
        const id = data.id ?? 0
        return {
          lockId: String(id), title: `${data.ref}: ${heading}`, heading, docKind: "", blocks: data.blocks ?? [], body_sha256: data.body_sha256 ?? "",
          body: data.body ?? "", version: data.version ?? 0,
          threads: { kind: "rule", id, ref: target.ref, openOn: data.arranged_in?.[0]?.doc },
        }
      }
      const resolved = await api.GET("/api/v1/docs/resolve", { params: { query: { ref: target.ref } } })
      if (resolved.error) throw failure(resolved.error, resolved.response)
      const id = resolved.data.id ?? 0
      const { data, error, response } = await api.GET("/api/v1/docs/{id}", { params: { path: { id: String(id) }, query } })
      if (error) throw failure(error, response)
      return {
        lockId: String(id), title: `${data.ref}: ${data.title}`, heading: "", docKind: data.kind ?? "", blocks: data.blocks ?? [], body_sha256: data.body_sha256 ?? "",
        body: data.body ?? "", version: data.version ?? 0,
        threads: { kind: "doc", id, ref: target.ref, openOn: id },
      }
    },
  })
}

/** A document's or rule's threads, bare notes and review threads together, `obsolete` included (WL-REQ-1323) */
export function useThreads(subject: ThreadSubject | undefined) {
  return useQuery({
    queryKey: ["threads", subject?.kind, subject?.ref],
    enabled: subject !== undefined,
    queryFn: async (): Promise<ReviewThread[]> => {
      const s = subject!
      const { data, error, response } =
        s.kind === "doc"
          ? await api.GET("/api/v1/docs/{id}/threads", { params: { path: { id: String(s.id) } } })
          : await api.GET("/api/v1/rules/{id}/threads", { params: { path: { id: s.ref } } })
      if (error) throw failure(error, response)
      return data
    },
  })
}

export type Review = components["schemas"]["Review"]
export type ReviewGuide = components["schemas"]["ReviewGuide"]

/** One review with its threads, rounds and verdicts (WL-REQ-1775) */
export function useReview(id: string) {
  return useQuery({
    queryKey: ["reviews", "detail", id],
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/reviews/{id}", { params: { path: { id } } })
      if (error) throw failure(error, response)
      return data
    },
  })
}

/** The review's guide, with its skims only when `focused`; null when none was proposed (404) */
export function useReviewGuide(id: string, focused: boolean) {
  return useQuery({
    queryKey: ["reviews", "guide", id, focused],
    queryFn: async (): Promise<ReviewGuide | null> => {
      const { data, error, response } = await api.GET("/api/v1/reviews/{id}/guide", { params: { path: { id }, query: { focused } } })
      if (response.status === 404) return null
      if (error) throw failure(error, response)
      return data
    },
  })
}

/**
 * A project's open reviews. The list route has no project filter, so this keeps
 * the reviews whose subject belongs to the project: its specs, plans and rules
 * by id, its tasks by key prefix.
 */
export function useProjectReviews(projectId: string) {
  const open = useQuery({
    queryKey: ["reviews", "open"],
    queryFn: async () => {
      const { data, error, response } = await api.GET("/api/v1/reviews", { params: { query: { state: "open" } } })
      if (error) throw failure(error, response)
      return data ?? []
    },
  })
  const project = useProject(projectId)
  const specs = useProjectDocs(projectId, "spec")
  const plans = useProjectDocs(projectId, "plan")
  const rules = useProjectRules(projectId)
  const queries = [open, project, specs, plans, rules]
  const data = (() => {
    if (!open.data || !project.data || !specs.data || !plans.data || !rules.data) return undefined
    const docs = new Set([...specs.data, ...plans.data].map((d) => d.id))
    const ruleIds = new Set(rules.data.map((r) => r.id))
    const prefix = `${project.data.key}-`
    return open.data.filter((r) => (r.doc ? docs.has(r.doc) : r.rule ? ruleIds.has(r.rule) : r.task?.startsWith(prefix)))
  })()
  return { data, isPending: data === undefined && !queries.some((q) => q.isError), isError: queries.some((q) => q.isError), refetch: () => queries.forEach((q) => void q.refetch()) }
}
