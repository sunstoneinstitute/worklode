import { describe, expect, it } from "vitest";
import { computeEtag, parseNote, taskToNote } from "../src/serialize/note";
import type { TaskListDetail } from "../src/api/types";

function fixtureTask(overrides: Partial<TaskListDetail> = {}): TaskListDetail {
  return {
    id: "WL-42",
    project: "worklode",
    title: "Fix the thing",
    body: "body markdown verbatim",
    priority: "medium",
    kind: "feature",
    state: "ready",
    concern: "",
    needs_decomposition: false,
    created_by: "stig",
    created_at: "2026-08-01T10:00:00Z",
    updated_at: "2026-08-14T12:00:00Z",
    skills: [],
    assignee: "stig",
    branch: "WL-42-fix-the-thing",
    blocked: false,
    edges: {
      out: [
        { to: "WL-7", type: "child_of" },
        { to: "WL-44", type: "blocks" },
      ],
      in: [
        { from: "WL-43", type: "child_of" },
        { from: "WL-41", type: "blocks" },
      ],
    },
    ...overrides,
  };
}

describe("taskToNote", () => {
  it("renders a task note with relations as wikilinks", () => {
    const t = fixtureTask();
    const note = taskToNote(t);

    expect(note.path).toBe("worklode/tasks/WL-42.md");
    expect(note.etag).toMatch(/^[0-9a-f]{16}$/);

    const expected = `---
aliases:
  - Fix the thing
wl:
  type: task
  serializer: 1
  aliases_added: true
  id: WL-42
  project: worklode
  title: Fix the thing
  state: ready
  kind: feature
  priority: medium
  concern: ""
  assignee: stig
  branch: WL-42-fix-the-thing
  blocked: false
  needs_decomposition: false
  skills: []
  created_by: stig
  created_at: 2026-08-01T10:00:00Z
  updated_at: 2026-08-14T12:00:00Z
  parent: "[[WL-7]]"
  children:
    - "[[WL-43]]"
  blocks:
    - "[[WL-44]]"
  blocked_by:
    - "[[WL-41]]"
  etag: ${note.etag}
---
# Fix the thing

body markdown verbatim`;

    expect(note.content).toBe(expected);
  });

  it("omits parent for a root task and renders empty relation lists", () => {
    const t = fixtureTask({
      edges: { out: [], in: [] },
    });
    const note = taskToNote(t);

    expect(note.content).not.toMatch(/^\s*parent:/m);
    expect(note.content).toMatch(/children: \[\]/);
    expect(note.content).toMatch(/blocks: \[\]/);
    expect(note.content).toMatch(/blocked_by: \[\]/);
  });

  it("writes the body verbatim", () => {
    const body = "line one\n---\nline with a\ttab\nline with trailing space   \n```\ncode fence\n```";
    const t = fixtureTask({ body });
    const note = taskToNote(t);

    expect(note.content.endsWith(body)).toBe(true);
    expect(parseNote(note.content).body).toBe(body);
  });

  it("round-trips: parseNote(taskToNote(t)) recovers the wl block and body", () => {
    const t = fixtureTask({
      body: "some body text\nwith multiple lines\n",
    });
    const note = taskToNote(t);
    const parsed = parseNote(note.content);

    expect(parsed.wl).toEqual({
      type: "task",
      serializer: 1,
      aliases_added: true,
      id: "WL-42",
      project: "worklode",
      title: "Fix the thing",
      state: "ready",
      kind: "feature",
      priority: "medium",
      concern: "",
      assignee: "stig",
      branch: "WL-42-fix-the-thing",
      blocked: false,
      needs_decomposition: false,
      skills: [],
      created_by: "stig",
      created_at: "2026-08-01T10:00:00Z",
      updated_at: "2026-08-14T12:00:00Z",
      parent: "[[WL-7]]",
      children: ["[[WL-43]]"],
      blocks: ["[[WL-44]]"],
      blocked_by: ["[[WL-41]]"],
      etag: note.etag,
    });
    expect(parsed.body).toBe("some body text\nwith multiple lines\n");
    expect(parsed.frontmatter).toEqual({});
  });

  it("changes the etag when any backbone field changes, and not otherwise", () => {
    const t = fixtureTask();
    const again = fixtureTask();
    expect(taskToNote(t).etag).toBe(taskToNote(again).etag);

    const changed = fixtureTask({ title: "Fix the other thing" });
    expect(taskToNote(changed).etag).not.toBe(taskToNote(t).etag);

    const changedEdges = fixtureTask({
      edges: {
        out: [{ to: "WL-7", type: "child_of" }],
        in: [],
      },
    });
    expect(taskToNote(changedEdges).etag).not.toBe(taskToNote(t).etag);
  });
});

describe("computeEtag", () => {
  it("is order-independent over object key order", () => {
    const a = computeEtag({ x: 1, y: 2 });
    const b = computeEtag({ y: 2, x: 1 });
    expect(a).toBe(b);
  });
});
