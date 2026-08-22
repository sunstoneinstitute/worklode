import { describe, expect, it } from "vitest";
import { WorklodeApiError, WorklodeClient, type HttpTransport } from "../src/api/client";

interface RecordedRequest {
  url: string;
  method: string;
  headers: Record<string, string>;
  body?: string;
}

function fakeTransport(
  status: number,
  text: string,
): { transport: HttpTransport; requests: RecordedRequest[] } {
  const requests: RecordedRequest[] = [];
  const transport: HttpTransport = async (req) => {
    requests.push(req);
    return { status, text };
  };
  return { transport, requests };
}

describe("WorklodeClient", () => {
  it("requests the expanded shapes and sends the bearer token", async () => {
    const { transport, requests } = fakeTransport(200, JSON.stringify({ tasks: [] }));
    const client = new WorklodeClient("https://lode.example.com", "wl_abc123", transport);

    await client.listTasks("worklode");

    expect(requests).toHaveLength(1);
    expect(requests[0].method).toBe("GET");
    expect(requests[0].url).toBe(
      "https://lode.example.com/api/v1/tasks?project=worklode&detail=true",
    );
    expect(requests[0].headers.Authorization).toBe("Bearer wl_abc123");
    expect(requests[0].headers.Accept).toBe("application/json");
  });

  // The incremental path: the watermark rides along as updated_since, and
  // the "+" in a non-UTC offset must survive as %2B rather than reaching the
  // server as a space.
  it("appends the watermark as an encoded updated_since", async () => {
    const { transport, requests } = fakeTransport(200, JSON.stringify({ tasks: [] }));
    const client = new WorklodeClient("https://lode.example.com", "wl_abc123", transport);

    await client.listTasks("worklode", "2026-08-16T09:12:00+02:00");

    expect(requests[0].url).toBe(
      "https://lode.example.com/api/v1/tasks?project=worklode&detail=true" +
        "&updated_since=2026-08-16T09%3A12%3A00%2B02%3A00",
    );
  });

  it("omits updated_since when there is no watermark", async () => {
    const { transport, requests } = fakeTransport(200, JSON.stringify({ tasks: [] }));
    const client = new WorklodeClient("https://lode.example.com", "wl_abc123", transport);

    await client.listTasks("worklode", "");

    expect(requests[0].url).toBe("https://lode.example.com/api/v1/tasks?project=worklode&detail=true");
  });

  it("requests docs and sends the bearer token", async () => {
    const { transport, requests } = fakeTransport(200, JSON.stringify({ docs: [] }));
    const client = new WorklodeClient("https://lode.example.com", "wl_abc123", transport);

    await client.listDocs("worklode");

    expect(requests).toHaveLength(1);
    expect(requests[0].url).toBe("https://lode.example.com/api/v1/docs?project=worklode");
    expect(requests[0].headers.Authorization).toBe("Bearer wl_abc123");
    expect(requests[0].headers.Accept).toBe("application/json");
  });

  // The one route that serves a document's markdown. The list route above
  // blanks it on every row, so without this the mirror has no text to render
  // a doc note from at all.
  it("fetches one document by id, with its body", async () => {
    const detail = {
      id: 25,
      project: "worklode",
      kind: "spec",
      number: 25,
      slug: "documents-in-the-backbone",
      title: "Documents in the backbone",
      body: "---\nstatus: draft\n---\n# Documents in the backbone\n",
      status: "draft",
      version: 3,
      issued: "2026-06-01",
      assignee: "stig",
      created_by: "stig",
      created_at: "2026-06-01T09:00:00Z",
      updated_at: "2026-08-16T09:12:00Z",
      sections: [],
      edges: [],
      edges_in: [],
      revision: null,
    };
    const { transport, requests } = fakeTransport(200, JSON.stringify(detail));
    const client = new WorklodeClient("https://lode.example.com", "wl_abc123", transport);

    // Unwrapped: getDoc answers with the DocDetail itself, not a { doc: ... }
    // envelope the way the list routes wrap their arrays.
    expect(await client.getDoc(25)).toEqual(detail);
    expect(requests).toHaveLength(1);
    expect(requests[0].method).toBe("GET");
    expect(requests[0].url).toBe("https://lode.example.com/api/v1/docs/25");
    expect(requests[0].headers.Authorization).toBe("Bearer wl_abc123");
    expect(requests[0].body).toBeUndefined();
  });

  it("surfaces a refused document fetch as a WorklodeApiError", async () => {
    const { transport } = fakeTransport(404, JSON.stringify({ error: "not found" }));
    const client = new WorklodeClient("https://lode.example.com", "wl_abc123", transport);

    // Deliberately not degraded the way listDocsIfPresent degrades a missing
    // route: a document the list just named must exist, and rendering its note
    // from the blank list row would overwrite the text already in the vault.
    await expect(client.getDoc(25)).rejects.toBeInstanceOf(WorklodeApiError);
  });

  it("normalizes a base URL with a trailing slash", async () => {
    const { transport, requests } = fakeTransport(200, JSON.stringify({ projects: [] }));
    const client = new WorklodeClient("https://lode.example.com/", "wl_abc123", transport);

    await client.listProjects();

    expect(requests[0].url).toBe("https://lode.example.com/api/v1/projects");
  });

  it("throws WorklodeApiError with the status and path on 401", async () => {
    const { transport } = fakeTransport(401, JSON.stringify({ error: "invalid token" }));
    const client = new WorklodeClient("https://lode.example.com", "wl_bad", transport);

    let caught: unknown;
    try {
      await client.listTasks("worklode");
    } catch (err) {
      caught = err;
    }

    expect(caught).toBeInstanceOf(WorklodeApiError);
    const err = caught as WorklodeApiError;
    expect(err.status).toBe(401);
    expect(err.path).toBe("/api/v1/tasks?project=worklode&detail=true");
    expect(err.body).toBe(JSON.stringify({ error: "invalid token" }));
    // 401 must be legible without inspecting the error object: the message
    // alone (as shown in a status bar) needs the status and the path.
    expect(err.message).toContain("401");
    expect(err.message).toContain("/api/v1/tasks");
  });

  // The docs endpoint is optional: a server that never shipped it (or that
  // retired it) answers 404 for the route itself. That must cost the doc
  // notes and nothing else, so absence is a value, not a throw.
  it("reports an absent docs endpoint as undefined, not an error", async () => {
    const { transport } = fakeTransport(404, "404 page not found\n");
    const client = new WorklodeClient("https://lode.example.com", "wl_abc123", transport);

    expect(await client.listDocsIfPresent("worklode")).toBeUndefined();
  });

  it("returns the docs when the endpoint is present", async () => {
    const { transport } = fakeTransport(200, JSON.stringify({ docs: [] }));
    const client = new WorklodeClient("https://lode.example.com", "wl_abc123", transport);

    expect(await client.listDocsIfPresent("worklode")).toEqual([]);
  });

  // Only 404 degrades. A rejected token or a broken server is a real failure
  // and has to stay one -- silently mirroring no docs would hide it.
  it("still throws for any other non-2xx status", async () => {
    for (const status of [401, 403, 500, 502]) {
      const { transport } = fakeTransport(status, "nope");
      const client = new WorklodeClient("https://lode.example.com", "wl_abc123", transport);

      await expect(client.listDocsIfPresent("worklode")).rejects.toBeInstanceOf(WorklodeApiError);
    }
  });

  it("still throws when the transport itself fails", async () => {
    const transport: HttpTransport = async () => {
      throw new Error("ECONNREFUSED");
    };
    const client = new WorklodeClient("https://lode.example.com", "wl_abc123", transport);

    await expect(client.listDocsIfPresent("worklode")).rejects.toThrow("ECONNREFUSED");
  });

  // The one write the plugin makes. The body is the whole payload: a state or
  // priority field here would let a note's frontmatter drive a lifecycle
  // transition, which is `lode`'s job.
  it("patches a task body and sends nothing else", async () => {
    const patched = { id: "WL-1", body: "edited", updated_at: "2026-08-17T14:30:00Z" };
    const { transport, requests } = fakeTransport(200, JSON.stringify(patched));
    const client = new WorklodeClient("https://lode.example.com", "wl_abc123", transport);

    const task = await client.patchTaskBody("WL-1", "edited");

    expect(requests).toHaveLength(1);
    expect(requests[0].method).toBe("PATCH");
    expect(requests[0].url).toBe("https://lode.example.com/api/v1/tasks/WL-1");
    expect(requests[0].headers.Authorization).toBe("Bearer wl_abc123");
    expect(requests[0].headers["Content-Type"]).toBe("application/json");
    expect(requests[0].body).toBe(JSON.stringify({ body: "edited" }));
    expect(task.updated_at).toBe("2026-08-17T14:30:00Z");
  });

  it("sends no body and no content type on a read", async () => {
    const { transport, requests } = fakeTransport(200, JSON.stringify({ projects: [] }));
    const client = new WorklodeClient("https://lode.example.com", "wl_abc123", transport);

    await client.listProjects();

    expect(requests[0].body).toBeUndefined();
    expect(requests[0].headers["Content-Type"]).toBeUndefined();
  });

  it("escapes a task id in the patch path", async () => {
    const { transport, requests } = fakeTransport(200, JSON.stringify({ id: "a b" }));
    const client = new WorklodeClient("https://lode.example.com", "wl_abc123", transport);

    await client.patchTaskBody("a b", "edited");

    expect(requests[0].url).toBe("https://lode.example.com/api/v1/tasks/a%20b");
  });

  it("throws WorklodeApiError when a patch is refused", async () => {
    const { transport } = fakeTransport(403, JSON.stringify({ error: "forbidden" }));
    const client = new WorklodeClient("https://lode.example.com", "wl_abc123", transport);

    await expect(client.patchTaskBody("WL-1", "edited")).rejects.toBeInstanceOf(WorklodeApiError);
  });

  it("parses a task row's edges", async () => {
    const body = JSON.stringify({
      tasks: [
        {
          id: "WL-1",
          project: "worklode",
          title: "Do the thing",
          body: "",
          priority: "high",
          kind: "feature",
          state: "ready",
          concern: "",
          needs_decomposition: false,
          created_by: "stig",
          created_at: "2026-08-16T00:00:00Z",
          updated_at: "2026-08-16T00:00:00Z",
          skills: [],
          assignee: "",
          branch: "WL-1-do-the-thing",
          blocked: true,
          edges: {
            out: [{ to: "WL-2", type: "blocks" }],
            in: [{ from: "WL-0", type: "child_of" }],
          },
        },
      ],
    });
    const { transport } = fakeTransport(200, body);
    const client = new WorklodeClient("https://lode.example.com", "wl_abc123", transport);

    const tasks = await client.listTasks("worklode");

    expect(tasks).toHaveLength(1);
    expect(tasks[0].blocked).toBe(true);
    expect(tasks[0].edges.out).toEqual([{ to: "WL-2", type: "blocks" }]);
    expect(tasks[0].edges.in).toEqual([{ from: "WL-0", type: "child_of" }]);
  });
});
