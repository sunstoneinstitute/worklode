import { describe, expect, it } from "vitest";
import { WorklodeApiError, WorklodeClient, type HttpTransport } from "../src/api/client";

interface RecordedRequest {
  url: string;
  method: string;
  headers: Record<string, string>;
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

  it("requests docs with the body param and sends the bearer token", async () => {
    const { transport, requests } = fakeTransport(200, JSON.stringify({ docs: [] }));
    const client = new WorklodeClient("https://lode.example.com", "wl_abc123", transport);

    await client.listDocs("worklode");

    expect(requests).toHaveLength(1);
    expect(requests[0].url).toBe(
      "https://lode.example.com/api/v1/docs?project=worklode&body=true",
    );
    expect(requests[0].headers.Authorization).toBe("Bearer wl_abc123");
    expect(requests[0].headers.Accept).toBe("application/json");
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
