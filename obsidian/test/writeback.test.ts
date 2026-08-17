import { describe, expect, it } from "vitest";
import { applyMirror, desiredNotes, foreignNotes, isConflictNotePath, type VaultWriter } from "../src/sync/mirror";
import { classifyTaskNote, writeBackTaskNotes, type PushBody } from "../src/sync/writeback";
import { parseNote } from "../src/serialize/note";
import type { Project, Task, TaskListDetail } from "../src/api/types";

function fixtureProject(overrides: Partial<Project> = {}): Project {
  return {
    id: "worklode",
    name: "Worklode",
    key: "WL",
    repos: [{ repo: "sunstoneinstitute/worklode", done_state: "done" }],
    focus: [],
    ...overrides,
  };
}

function fixtureTask(overrides: Partial<TaskListDetail> = {}): TaskListDetail {
  return {
    id: "WL-1",
    project: "worklode",
    title: "Fix the thing",
    body: "task body",
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
    branch: "WL-1-fix-the-thing",
    blocked: false,
    edges: { out: [], in: [] },
    ...overrides,
  };
}

const ROOT = "/vault/Worklode Mount";
const ROOT_NAME = "Worklode Vault";
const SYNCED_AT = "2026-08-16T09:12:00Z";
const AT = "2026-08-17T14:30:00.000Z";
const TASK_NOTE = "worklode/tasks/WL-1.md";

class MapVaultWriter implements VaultWriter {
  files = new Map<string, string>();
  written: string[] = [];
  removed: string[] = [];

  async list(root: string): Promise<string[]> {
    const prefix = `${root}/`;
    return [...this.files.keys()].filter((k) => k.startsWith(prefix)).map((k) => k.slice(prefix.length));
  }

  async read(root: string, path: string): Promise<string> {
    const content = this.files.get(`${root}/${path}`);
    if (content === undefined) throw new Error(`not found: ${path}`);
    return content;
  }

  async write(root: string, path: string, content: string): Promise<void> {
    this.files.set(`${root}/${path}`, content);
    this.written.push(path);
  }

  async remove(root: string, path: string): Promise<void> {
    this.files.delete(`${root}/${path}`);
    this.removed.push(path);
  }
}

/** A push that answers the way PATCH /api/v1/tasks/{id} does: the plain task
 *  shape (no edges), with the new body and a fresh updated_at. */
function fakePush(task: TaskListDetail, updatedAt = "2026-08-17T14:30:00Z"): {
  push: PushBody;
  calls: { id: string; body: string }[];
} {
  const calls: { id: string; body: string }[] = [];
  const push: PushBody = async (id, body) => {
    calls.push({ id, body });
    const { blocked: _blocked, edges: _edges, ...plain } = task;
    return { ...plain, body, updated_at: updatedAt } satisfies Task;
  };
  return { push, calls };
}

/** The vault as a full sync leaves it, plus whatever the caller edits after. */
async function mirroredVault(task: TaskListDetail): Promise<MapVaultWriter> {
  const writer = new MapVaultWriter();
  const byProject = new Map([["worklode", { docs: [], tasks: [task] }]]);
  await applyMirror(writer, ROOT, await desiredNotes([fixtureProject()], byProject, ROOT_NAME, SYNCED_AT));
  writer.written = [];
  return writer;
}

/** Replaces a task note's body while keeping its frontmatter -- what a user
 *  typing into the note produces. */
async function editBody(writer: MapVaultWriter, newBody: string): Promise<void> {
  const content = await writer.read(ROOT, TASK_NOTE);
  const parsed = parseNote(content);
  await writer.write(ROOT, TASK_NOTE, content.replace(parsed.body, newBody));
  writer.written = [];
}

describe("classifyTaskNote", () => {
  it("reads an untouched note as clean", async () => {
    const task = fixtureTask();
    const writer = await mirroredVault(task);

    expect(await classifyTaskNote(task, await writer.read(ROOT, TASK_NOTE))).toEqual({ kind: "clean" });
  });

  it("reads a body edit against an unmoved backbone as an uncontested edit", async () => {
    const task = fixtureTask();
    const writer = await mirroredVault(task);
    await editBody(writer, "task body, with my notes\n");

    expect(await classifyTaskNote(task, await writer.read(ROOT, TASK_NOTE))).toEqual({
      kind: "edited",
      body: "task body, with my notes\n",
    });
  });

  it("reads a body edit against a moved backbone as a conflict", async () => {
    const writer = await mirroredVault(fixtureTask());
    await editBody(writer, "my version\n");

    // The backbone moved on since the note was rendered: same id, different
    // fields, so the note's stored etag no longer matches.
    const moved = fixtureTask({ body: "their version", updated_at: "2026-08-17T09:00:00Z" });

    expect(await classifyTaskNote(moved, await writer.read(ROOT, TASK_NOTE))).toEqual({
      kind: "conflict",
      body: "my version\n",
    });
  });

  it("reports an unparseable note rather than reading it as unedited", async () => {
    const edit = await classifyTaskNote(fixtureTask(), "I replaced this note wholesale.\n");

    expect(edit.kind).toBe("unreadable");
  });

  it("reports a note that is not a task note", async () => {
    const writer = await mirroredVault(fixtureTask());
    const projectNote = await writer.read(ROOT, "worklode/worklode.md");

    const edit = await classifyTaskNote(fixtureTask(), projectNote);

    expect(edit.kind).toBe("unreadable");
  });
});

describe("writeBackTaskNotes", () => {
  function members(tasks: TaskListDetail[]): Map<string, { docs: never[]; tasks: TaskListDetail[] }> {
    return new Map([["worklode", { docs: [], tasks }]]);
  }

  it("pushes nothing when no note was edited", async () => {
    const task = fixtureTask();
    const writer = await mirroredVault(task);
    const { push, calls } = fakePush(task);

    const { stats } = await writeBackTaskNotes(writer, ROOT, members([task]), push, AT);

    expect(calls).toEqual([]);
    expect(stats).toEqual({ pushed: 0, conflicted: 0, conflicts: [] });
    expect(writer.written).toEqual([]);
  });

  it("pushes an uncontested body edit and hands back the patched task", async () => {
    const task = fixtureTask();
    const writer = await mirroredVault(task);
    await editBody(writer, "task body, with my notes\n");
    const { push, calls } = fakePush(task);

    const result = await writeBackTaskNotes(writer, ROOT, members([task]), push, AT);

    expect(calls).toEqual([{ id: "WL-1", body: "task body, with my notes\n" }]);
    expect(result.stats.pushed).toBe(1);
    expect(result.stats.conflicted).toBe(0);

    const pushed = result.byProject.get("worklode")!.tasks[0];
    expect(pushed.body).toBe("task body, with my notes\n");
    expect(pushed.updated_at).toBe("2026-08-17T14:30:00Z");
    // The detail-only fields the PATCH response does not carry survive.
    expect(pushed.edges).toEqual({ out: [], in: [] });
    expect(pushed.blocked).toBe(false);
  });

  // The whole point of using the PATCH response: a note rendered from the
  // pre-push task would carry a stale etag, and the next sync would rewrite
  // it -- destroying nothing, but churning the file forever.
  it("leaves the note settled: the same sync renders the pushed body, the next one skips it", async () => {
    const task = fixtureTask();
    const writer = await mirroredVault(task);
    await editBody(writer, "task body, with my notes\n");
    const { push } = fakePush(task);

    const { byProject } = await writeBackTaskNotes(writer, ROOT, members([task]), push, AT);
    const first = await applyMirror(
      writer,
      ROOT,
      await desiredNotes([fixtureProject()], byProject, ROOT_NAME, SYNCED_AT),
    );
    expect(first.written).toBeGreaterThan(0);
    expect(await writer.read(ROOT, TASK_NOTE)).toContain("task body, with my notes");

    // The next tick, with the backbone now serving what it was sent.
    const nextTask = byProject.get("worklode")!.tasks[0];
    const nextWriteBack = await writeBackTaskNotes(writer, ROOT, members([nextTask]), push, AT);
    expect(nextWriteBack.stats.pushed).toBe(0);
    const second = await applyMirror(
      writer,
      ROOT,
      await desiredNotes([fixtureProject()], members([nextTask]), ROOT_NAME, SYNCED_AT),
    );
    expect(second.written).toBe(0);
  });

  it("saves the local body as a conflict note when the backbone moved too", async () => {
    const writer = await mirroredVault(fixtureTask());
    await editBody(writer, "my version\n");
    const moved = fixtureTask({ body: "their version", updated_at: "2026-08-17T09:00:00Z" });
    const { push, calls } = fakePush(moved);

    const { stats, byProject } = await writeBackTaskNotes(writer, ROOT, members([moved]), push, AT);

    expect(calls).toEqual([]);
    expect(stats.pushed).toBe(0);
    expect(stats.conflicted).toBe(1);
    expect(stats.conflicts).toHaveLength(1);
    // The backbone's task is handed back untouched: it wins.
    expect(byProject.get("worklode")!.tasks[0].body).toBe("their version");

    const conflictPath = writer.written.find((p) => isConflictNotePath(p))!;
    expect(conflictPath).toBe("_conflicts/worklode/WL-1 2026-08-17T14-30-00Z.md");
    const content = await writer.read(ROOT, conflictPath);
    expect(content).toContain("my version");
    expect(parseNote(content).wl.type).toBe("conflict");
    expect(parseNote(content).wl.task_note).toBe(TASK_NOTE);
    expect(stats.conflicts[0]).toContain(conflictPath);
  });

  // The failure mode this feature dies of silently: the conflict note is not
  // in any desired set, so the delete pass of the very sync that wrote it
  // would sweep it up.
  it("keeps the conflict note through the same sync's delete pass, and the backbone's note wins", async () => {
    const writer = await mirroredVault(fixtureTask());
    await editBody(writer, "my version\n");
    const moved = fixtureTask({ body: "their version", updated_at: "2026-08-17T09:00:00Z" });
    const { push } = fakePush(moved);

    const { byProject } = await writeBackTaskNotes(writer, ROOT, members([moved]), push, AT);
    const stats = await applyMirror(
      writer,
      ROOT,
      await desiredNotes([fixtureProject()], byProject, ROOT_NAME, SYNCED_AT),
    );

    expect(stats.removed).toBe(0);
    expect(writer.removed).toEqual([]);
    const remaining = await writer.list(ROOT);
    expect(remaining).toContain("_conflicts/worklode/WL-1 2026-08-17T14-30-00Z.md");
    // Backbone wins in the task note; the user's text lives only in the conflict note.
    const note = await writer.read(ROOT, TASK_NOTE);
    expect(note).toContain("their version");
    expect(note).not.toContain("my version");
    expect(await writer.read(ROOT, "_conflicts/worklode/WL-1 2026-08-17T14-30-00Z.md")).toContain("my version");
  });

  // A second sync must not prune it either, and it is the mirror's own file
  // rather than a foreign note the adoption prompt would ask about.
  it("writes a conflict note the mirror recognises as its own", async () => {
    const writer = await mirroredVault(fixtureTask());
    await editBody(writer, "my version\n");
    const moved = fixtureTask({ body: "their version", updated_at: "2026-08-17T09:00:00Z" });
    const { push } = fakePush(moved);
    await writeBackTaskNotes(writer, ROOT, members([moved]), push, AT);

    expect(await foreignNotes(writer, ROOT)).toEqual([]);
  });

  // A push that fails must not cost the user their text: the note keeps the
  // edit (its etag still matches, so applyMirror skips it) and the next full
  // sync tries again.
  it("reports a failed push and leaves the edited note in place", async () => {
    const task = fixtureTask();
    const writer = await mirroredVault(task);
    await editBody(writer, "task body, with my notes\n");
    const push: PushBody = async () => {
      throw new Error("worklode API request failed: 403 /api/v1/tasks/WL-1");
    };

    const { stats, byProject } = await writeBackTaskNotes(writer, ROOT, members([task]), push, AT);

    expect(stats.pushed).toBe(0);
    expect(stats.conflicts).toHaveLength(1);
    expect(stats.conflicts[0]).toContain("403");
    expect(byProject.get("worklode")!.tasks[0].body).toBe("task body");

    const applied = await applyMirror(
      writer,
      ROOT,
      await desiredNotes([fixtureProject()], byProject, ROOT_NAME, SYNCED_AT),
    );
    expect(applied.written).toBe(0);
    expect(await writer.read(ROOT, TASK_NOTE)).toContain("task body, with my notes");
  });

  it("reports an unparseable note and pushes nothing for it", async () => {
    const task = fixtureTask();
    const writer = await mirroredVault(task);
    await writer.write(ROOT, TASK_NOTE, "I replaced this note wholesale.\n");
    writer.written = [];
    const { push, calls } = fakePush(task);

    const { stats } = await writeBackTaskNotes(writer, ROOT, members([task]), push, AT);

    expect(calls).toEqual([]);
    expect(stats.pushed).toBe(0);
    expect(stats.conflicted).toBe(0);
    expect(stats.conflicts).toHaveLength(1);
    expect(writer.written).toEqual([]);
  });

  it("ignores a task with no note on disk", async () => {
    const task = fixtureTask();
    const writer = new MapVaultWriter();
    const { push, calls } = fakePush(task);

    const { stats } = await writeBackTaskNotes(writer, ROOT, members([task]), push, AT);

    expect(calls).toEqual([]);
    expect(stats.pushed).toBe(0);
  });

  // Same guard filterSafe applies: a task whose own project field disagrees
  // with the key it was grouped under has no note the mirror owns, and must
  // never place a conflict note by that field either.
  it("ignores a task whose project field disagrees with its grouping key", async () => {
    const writer = await mirroredVault(fixtureTask());
    await editBody(writer, "my version\n");
    const hostile = fixtureTask({ project: "../escape" });
    const { push, calls } = fakePush(hostile);

    const { stats } = await writeBackTaskNotes(writer, ROOT, members([hostile]), push, AT);

    expect(calls).toEqual([]);
    expect(stats.pushed).toBe(0);
    expect(writer.written).toEqual([]);
  });
});
