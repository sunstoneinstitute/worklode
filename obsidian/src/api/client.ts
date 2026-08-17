import type { Doc, Project, TaskListDetail } from "./types";

/** The subset of Obsidian's requestUrl that this client needs. */
export interface HttpTransport {
  (req: { url: string; method: string; headers: Record<string, string> }): Promise<{
    status: number;
    text: string;
  }>;
}

/**
 * Thrown for any non-2xx response. status, path and body are all readable
 * off the error, and the message alone (status + path) is legible on its
 * own — a pasted-token typo surfacing as 401 is the most likely failure, and
 * a bare "sync failed" would send the user hunting.
 */
export class WorklodeApiError extends Error {
  constructor(
    readonly status: number,
    readonly path: string,
    readonly body: string,
  ) {
    super(`worklode API request failed: ${status} ${path}`);
    this.name = "WorklodeApiError";
  }
}

/** Read-only HTTP client for the worklode API, used by the Obsidian sync loop. */
export class WorklodeClient {
  private readonly baseUrl: string;

  constructor(
    baseUrl: string,
    private readonly token: string,
    private readonly http: HttpTransport,
  ) {
    this.baseUrl = baseUrl.replace(/\/+$/, "");
  }

  listProjects(): Promise<Project[]> {
    return this.get<{ projects: Project[] }>("/api/v1/projects").then((r) => r.projects);
  }

  /** GET /api/v1/tasks?project=&detail=true */
  listTasks(project: string): Promise<TaskListDetail[]> {
    const path = `/api/v1/tasks?project=${encodeURIComponent(project)}&detail=true`;
    return this.get<{ tasks: TaskListDetail[] }>(path).then((r) => r.tasks);
  }

  /** GET /api/v1/docs?project=&body=true */
  listDocs(project: string): Promise<Doc[]> {
    const path = `/api/v1/docs?project=${encodeURIComponent(project)}&body=true`;
    return this.get<{ docs: Doc[] }>(path).then((r) => r.docs);
  }

  /**
   * listDocs, but `undefined` when the server has no docs endpoint at all.
   * The plugin ships independently of the binary, so a server without that
   * route is ordinary rather than exotic, and it must cost the doc notes
   * only -- not the projects and tasks synced in the same pass.
   *
   * Only 404 (the route is absent) degrades: 401, 403, 5xx and a failing
   * transport are real failures and stay thrown, because mirroring no docs
   * for one of those would hide it. `undefined` rather than `[]` so the
   * caller can tell "no docs endpoint" from "no docs" -- the sync report
   * says which, and the delete pass must not prune doc notes it never
   * enumerated.
   */
  async listDocsIfPresent(project: string): Promise<Doc[] | undefined> {
    try {
      return await this.listDocs(project);
    } catch (err) {
      if (err instanceof WorklodeApiError && err.status === 404) return undefined;
      throw err;
    }
  }

  private async get<T>(path: string): Promise<T> {
    const res = await this.http({
      url: this.baseUrl + path,
      method: "GET",
      headers: {
        Authorization: `Bearer ${this.token}`,
        Accept: "application/json",
      },
    });
    if (res.status < 200 || res.status >= 300) {
      throw new WorklodeApiError(res.status, path, res.text);
    }
    return JSON.parse(res.text) as T;
  }
}
