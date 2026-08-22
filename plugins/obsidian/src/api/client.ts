import type { Doc, DocDetail, Project, Task, TaskListDetail } from "./types";

/** The subset of Obsidian's requestUrl that this client needs. `body` is the
 *  serialized request body, absent on a GET -- requestUrl takes it under the
 *  same name. */
export interface HttpTransport {
  (req: { url: string; method: string; headers: Record<string, string>; body?: string }): Promise<{
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

/** HTTP client for the worklode API, used by the Obsidian sync loop. Reads
 *  everything the mirror renders; the one write is a task's body, sent by the
 *  opt-in write-back pass. */
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

  /**
   * GET /api/v1/tasks?project=&detail=true[&updated_since=]
   *
   * `updatedSince` is the incremental fetch: an RFC3339 instant narrowing the
   * response to the tasks touched at or after it. The server compares with
   * `>=`, so passing the highest `updated_at` already seen re-fetches that one
   * row rather than risking a write that shared its timestamp. "" (or
   * omitted) fetches every task.
   */
  listTasks(project: string, updatedSince?: string): Promise<TaskListDetail[]> {
    let path = `/api/v1/tasks?project=${encodeURIComponent(project)}&detail=true`;
    if (updatedSince) path += `&updated_since=${encodeURIComponent(updatedSince)}`;
    return this.get<{ tasks: TaskListDetail[] }>(path).then((r) => r.tasks);
  }

  /**
   * GET /api/v1/tasks?project=&deleted=true[&updated_since=]
   *
   * The tombstoned rows, which `deleted=true` switches the list to instead of
   * adding to it (044 §5) -- so this answers what listTasks never can: which
   * tasks were deleted. Each row carries a `tombstone`, and deletedTaskPaths
   * requires one before pruning anything, because a server predating 044
   * ignores the unknown parameter and answers with live tasks.
   *
   * `updatedSince` narrows it the same way it narrows listTasks: a delete sets
   * the row's `updated_at` to the deletion instant, so passing the mirror's
   * watermark asks for the deletes it has not seen rather than for every
   * tombstone the backbone has ever held.
   *
   * No `detail=true`: nothing is rendered from these rows, only removed, so
   * the edges and blocked flag would be two extra bulk queries spent on
   * fields the caller drops.
   */
  listDeletedTasks(project: string, updatedSince?: string): Promise<Task[]> {
    let path = `/api/v1/tasks?project=${encodeURIComponent(project)}&deleted=true`;
    if (updatedSince) path += `&updated_since=${encodeURIComponent(updatedSince)}`;
    return this.get<{ tasks: Task[] }>(path).then((r) => r.tasks);
  }

  /** GET /api/v1/docs?project= -- the list route never serves a body (every
   *  row comes back with body: ""); GET /api/v1/docs/{id} is the one that
   *  does. */
  listDocs(project: string): Promise<Doc[]> {
    const path = `/api/v1/docs?project=${encodeURIComponent(project)}`;
    return this.get<{ docs: Doc[] }>(path).then((r) => r.docs);
  }

  /**
   * GET /api/v1/docs/{id} -- one document, with the markdown body the list
   * route blanks, plus the rows derived from it (sections, edges both ways,
   * the open revision). This is the only way the mirror can obtain a doc's
   * text, so it is what makes a doc note more than its `wl` block.
   *
   * One request per document, which is why the sync fetches it only for the
   * documents whose vault note is out of date -- see hydrateDocBodies.
   */
  getDoc(id: number): Promise<DocDetail> {
    return this.get<DocDetail>(`/api/v1/docs/${id}`);
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

  /**
   * PATCH /api/v1/tasks/{id} with nothing but the body, answering with the
   * task as the backbone now holds it -- the plain task shape, without the
   * `detail=true` expansion's edges.
   *
   * The body is the only field the mirror ever writes: everything else a task
   * note shows lives in the backbone-owned `wl` block, and the state
   * transitions this endpoint also accepts belong to `lode`, not to a text
   * editor.
   */
  patchTaskBody(id: string, body: string): Promise<Task> {
    return this.send<Task>("PATCH", `/api/v1/tasks/${encodeURIComponent(id)}`, { body });
  }

  private get<T>(path: string): Promise<T> {
    return this.send<T>("GET", path);
  }

  private async send<T>(method: string, path: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = {
      Authorization: `Bearer ${this.token}`,
      Accept: "application/json",
    };
    if (body !== undefined) headers["Content-Type"] = "application/json";

    const res = await this.http({
      url: this.baseUrl + path,
      method,
      headers,
      ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
    });
    if (res.status < 200 || res.status >= 300) {
      throw new WorklodeApiError(res.status, path, res.text);
    }
    return JSON.parse(res.text) as T;
  }
}
