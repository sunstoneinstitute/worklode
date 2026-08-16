import { describe, expect, it } from "vitest";
import { applyMirror, desiredNotes, desiredPath, type VaultWriter } from "../src/sync/mirror";
import { parseNote } from "../src/serialize/note";
import type { Doc, Project, TaskListDetail } from "../src/api/types";

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

function fixtureDoc(overrides: Partial<Doc> = {}): Doc {
  return {
    id: "WL-SPEC-1",
    project: "worklode",
    kind: "spec",
    ordinal: "001",
    status: "draft",
    title: "A doc",
    version: 1,
    source_branch: "main",
    source_dirty: false,
    synced_at: "2026-08-16T09:12:00Z",
    body: "doc body",
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

/** In-memory VaultWriter for tests. Guards against any path escaping the
 *  mount root the way a real filesystem-backed writer would refuse to (or
 *  worse, silently wouldn't) -- so a mirror.ts regression that ever tries
 *  to write outside root fails the test loudly instead of getting masked
 *  by the Map happily accepting any key. */
class MapVaultWriter implements VaultWriter {
  files = new Map<string, string>();
  written: string[] = [];
  removed: string[] = [];

  private assertInsideRoot(root: string, path: string): void {
    const segments = path.split(/[\\/]/);
    if (path.startsWith("/") || path.startsWith("\\") || segments.includes("..") || segments.includes("")) {
      throw new Error(`writer received a path outside the mount root: ${root} / ${path}`);
    }
  }

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
    this.assertInsideRoot(root, path);
    this.files.set(`${root}/${path}`, content);
    this.written.push(path);
  }

  async remove(root: string, path: string): Promise<void> {
    this.assertInsideRoot(root, path);
    this.files.delete(`${root}/${path}`);
    this.removed.push(path);
  }
}

describe("desiredPath", () => {
  it("builds the vault-relative path for each note kind", () => {
    expect(desiredPath("project", "worklode")).toBe("worklode/worklode.md");
    expect(desiredPath("doc", "worklode", "WL-SPEC-1")).toBe("worklode/docs/WL-SPEC-1.md");
    expect(desiredPath("task", "worklode", "WL-1")).toBe("worklode/tasks/WL-1.md");
  });

  it("rejects an id containing '/', '\\', '..', or that is empty", () => {
    expect(desiredPath("project", "../escape")).toBeUndefined();
    expect(desiredPath("task", "worklode", "a/b")).toBeUndefined();
    expect(desiredPath("task", "worklode", "a\\b")).toBeUndefined();
    expect(desiredPath("doc", "worklode", "..")).toBeUndefined();
    expect(desiredPath("task", "worklode", "")).toBeUndefined();
    expect(desiredPath("project", "")).toBeUndefined();
  });
});

describe("applyMirror", () => {
  function fullScenario() {
    const project = fixtureProject();
    const doc = fixtureDoc();
    const task = fixtureTask();
    const byProject = new Map([["worklode", { docs: [doc], tasks: [task] }]]);
    return { project, doc, task, byProject };
  }

  it("writes every note on a first sync", async () => {
    const { project, byProject } = fullScenario();
    const desired = desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);
    expect(desired.conflicts).toEqual([]);
    // index + project + doc + task
    expect(desired.notes).toHaveLength(4);

    const writer = new MapVaultWriter();
    const stats = await applyMirror(writer, ROOT, desired);

    expect(stats.written).toBe(4);
    expect(stats.skipped).toBe(0);
    expect(stats.removed).toBe(0);
    expect(stats.conflicts).toEqual([]);
  });

  it("skips a file whose etag is unchanged", async () => {
    // Same syncedAt on both passes: the index note's wl block carries
    // syncedAt as part of its etag payload, so a different value would
    // legitimately cause an extra write on the second pass. Holding it
    // fixed keeps this test deterministic.
    const { project, byProject } = fullScenario();
    const desired = desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

    const writer = new MapVaultWriter();
    await applyMirror(writer, ROOT, desired);

    const second = await applyMirror(writer, ROOT, desired);
    expect(second.written).toBe(0);
    expect(second.skipped).toBe(4);
    expect(second.removed).toBe(0);
  });

  it("rewrites a file whose etag changed", async () => {
    const { project, doc, task, byProject } = fullScenario();
    const desired1 = desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

    const writer = new MapVaultWriter();
    await applyMirror(writer, ROOT, desired1);

    const changedTask = { ...task, title: "Fix the other thing" };
    const byProject2 = new Map([["worklode", { docs: [doc], tasks: [changedTask] }]]);
    const desired2 = desiredNotes([project], byProject2, ROOT_NAME, SYNCED_AT);

    const stats = await applyMirror(writer, ROOT, desired2);

    // The task note itself changes, and so does the project note: its etag
    // is computed over the full task list (projectToNote(p, docs, tasks)),
    // so any member's field change dirties the project note's etag too.
    // The index note's etag only covers doc/task counts, and the doc is
    // untouched, so both of those are skipped.
    expect(stats.written).toBe(2);
    expect(stats.skipped).toBe(2);
    expect(stats.removed).toBe(0);

    const taskContent = await writer.read(ROOT, "worklode/tasks/WL-1.md");
    expect(taskContent).toContain("Fix the other thing");
    expect(taskContent).not.toContain("Fix the thing\n");
  });

  it("rewrites a file a user edited, discarding the edit", async () => {
    const { project, byProject } = fullScenario();
    const desired = desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

    const writer = new MapVaultWriter();
    await applyMirror(writer, ROOT, desired);

    // Simulate a user replacing the note's content by hand -- no frontmatter
    // fence at all, so parseNote throws on it.
    await writer.write(ROOT, "worklode/tasks/WL-1.md", "I edited this note by hand.\nNo frontmatter here.\n");

    // Same backbone data, same syncedAt: everything else still matches.
    const stats = await applyMirror(writer, ROOT, desired);

    expect(stats.written).toBe(1);
    expect(stats.skipped).toBe(3);
    expect(stats.removed).toBe(0);

    const restored = await writer.read(ROOT, "worklode/tasks/WL-1.md");
    const wantTask = desired.notes.find((n) => n.path === "worklode/tasks/WL-1.md")!;
    expect(restored).toBe(wantTask.content);
    expect(restored).not.toContain("I edited this note by hand.");
  });

  it("removes a note whose backbone object disappeared", async () => {
    const { project, doc, byProject } = fullScenario();
    const desired1 = desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

    const writer = new MapVaultWriter();
    await applyMirror(writer, ROOT, desired1);

    // The task is gone from the backbone.
    const byProject2 = new Map([["worklode", { docs: [doc], tasks: [] }]]);
    const desired2 = desiredNotes([project], byProject2, ROOT_NAME, SYNCED_AT);

    const stats = await applyMirror(writer, ROOT, desired2);

    // The task file is removed. The project and index notes both embed
    // task counts/lists in their etag payload, so losing a task dirties
    // both of them too; the doc note is untouched.
    expect(stats.removed).toBe(1);
    expect(stats.written).toBe(2);
    expect(stats.skipped).toBe(1);

    const remaining = await writer.list(ROOT);
    expect(remaining).not.toContain("worklode/tasks/WL-1.md");
  });

  it("never touches a path outside the mount root", async () => {
    const badProject = fixtureProject({ id: "../escape", name: "Escape" });
    const goodProject = fixtureProject({ id: "worklode" });

    const badProjectDoc = fixtureDoc({ id: "WL-SPEC-9", project: "../escape" });
    const badProjectTask = fixtureTask({ id: "WL-9", project: "../escape" });
    const badIdTask = fixtureTask({ id: "a/b", project: "worklode" });
    const goodTask = fixtureTask({ id: "WL-1", project: "worklode" });

    const byProject = new Map([
      ["../escape", { docs: [badProjectDoc], tasks: [badProjectTask] }],
      ["worklode", { docs: [], tasks: [badIdTask, goodTask] }],
    ]);

    const desired = desiredNotes([badProject, goodProject], byProject, ROOT_NAME, SYNCED_AT);

    expect(desired.conflicts.some((c) => c.includes("../escape"))).toBe(true);
    expect(desired.conflicts.some((c) => c.includes("a/b"))).toBe(true);
    // The whole "../escape" project subtree is unrepresentable without a
    // safe directory, so none of its notes exist in the desired set.
    expect(desired.notes.some((n) => n.path.includes(".."))).toBe(false);
    expect(desired.notes.some((n) => n.path.includes("a/b"))).toBe(false);
    // Only the good project's index/project/task notes remain.
    expect(desired.notes.map((n) => n.path).sort()).toEqual(
      ["Worklode Vault.md", "worklode/tasks/WL-1.md", "worklode/worklode.md"].sort(),
    );

    const writer = new MapVaultWriter();
    // Should not throw: the writer's own guard would reject any path that
    // ever escaped the mount root, which is what "never touches" asserts.
    const stats = await applyMirror(writer, ROOT, desired);

    expect(stats.conflicts.some((c) => c.includes("../escape"))).toBe(true);
    expect(stats.conflicts.some((c) => c.includes("a/b"))).toBe(true);
    expect(writer.written.every((p) => !p.includes(".."))).toBe(true);
    expect(writer.written).not.toContain("worklode/tasks/a/b.md");
    for (const key of writer.files.keys()) {
      expect(key.startsWith(`${ROOT}/`)).toBe(true);
      expect(key.includes("..")).toBe(false);
    }
  });
});

describe("desiredNotes conflicts", () => {
  it("drains a rendered note's own conflict (e.g. a wl key collision) into the report", () => {
    const project = fixtureProject();
    const collidingDoc = fixtureDoc({ frontmatter: { wl: { author_note: "collides" } } });
    const byProject = new Map([["worklode", { docs: [collidingDoc], tasks: [] }]]);

    const desired = desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

    expect(desired.conflicts.some((c) => c.includes(collidingDoc.id))).toBe(true);
    // The note is still produced -- the backbone block wins, per note.ts.
    expect(desired.notes.some((n) => n.path === "worklode/docs/WL-SPEC-1.md")).toBe(true);
  });
});

// Sanity check that parseNote itself still throws on non-mirror content --
// the behaviour applyMirror's "not a mirror file, rewrite it" path relies on.
describe("parseNote tolerance assumption", () => {
  it("throws on content with no frontmatter fence", () => {
    expect(() => parseNote("plain text, no frontmatter")).toThrow();
  });
});
