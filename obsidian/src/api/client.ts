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
