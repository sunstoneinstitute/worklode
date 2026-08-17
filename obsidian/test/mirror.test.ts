import { describe, expect, it } from "vitest";
import {
  applyMirror,
  desiredNotes,
  desiredPath,
  foreignNotes,
  isSafeMountRoot,
  isSafePathSegment,
  mountRootName,
  type VaultWriter,
} from "../src/sync/mirror";
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

/** In-memory VaultWriter for tests. Throws if ever asked to touch a path
 *  outside the mount root, so a mirror.ts regression fails the test loudly. */
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

// The predicate behind every backbone id (project/doc/task): each becomes
// one path segment under the mount root, so a separator in one would move
// the note somewhere the mirror never surveyed. The cases below are the ones
// the two earlier predicates disagreed on.
describe("isSafePathSegment", () => {
  it("accepts an ordinary segment", () => {
    expect(isSafePathSegment("Worklode")).toBe(true);
    expect(isSafePathSegment("WL-SPEC-025")).toBe(true);
    expect(isSafePathSegment("My Notes")).toBe(true);
  });

  it("rejects blank, empty, and edge-whitespace segments", () => {
    expect(isSafePathSegment("")).toBe(false);
    expect(isSafePathSegment("   ")).toBe(false);
    expect(isSafePathSegment(" x ")).toBe(false);
    expect(isSafePathSegment("x ")).toBe(false);
  });

  it('rejects ".", "..", and any ".." substring', () => {
    expect(isSafePathSegment(".")).toBe(false);
    expect(isSafePathSegment("..")).toBe(false);
    expect(isSafePathSegment("My..Notes")).toBe(false);
  });

  it("rejects separators", () => {
    expect(isSafePathSegment("Team/Worklode")).toBe(false);
    expect(isSafePathSegment("Team\\Worklode")).toBe(false);
  });
});

// The mount root's own predicate (applied in src/main.ts, which vitest cannot
// import). It differs from isSafePathSegment in exactly one way: "/" joins
// segments instead of disqualifying the value. Every segment still has to
// clear the single-segment bar on its own.
describe("isSafeMountRoot", () => {
  it("accepts a single segment, exactly as before", () => {
    expect(isSafeMountRoot("Worklode")).toBe(true);
    expect(isSafeMountRoot("My Notes")).toBe(true);
  });

  it("accepts a nested root whose every segment is safe", () => {
    expect(isSafeMountRoot("Team/Worklode")).toBe(true);
    expect(isSafeMountRoot("Team/Shared Notes/Worklode")).toBe(true);
  });

  it('rejects ".." in any segment, at any depth', () => {
    expect(isSafeMountRoot("..")).toBe(false);
    expect(isSafeMountRoot("../Worklode")).toBe(false);
    expect(isSafeMountRoot("Team/..")).toBe(false);
    expect(isSafeMountRoot("Team/../Worklode")).toBe(false);
    expect(isSafeMountRoot("Team/../../etc/Worklode")).toBe(false);
    // ".." as a substring too, not just as a whole segment.
    expect(isSafeMountRoot("Team/My..Notes/Worklode")).toBe(false);
  });

  it('rejects "." as any segment', () => {
    expect(isSafeMountRoot(".")).toBe(false);
    expect(isSafeMountRoot("./Worklode")).toBe(false);
    expect(isSafeMountRoot("Team/./Worklode")).toBe(false);
    expect(isSafeMountRoot("Team/.")).toBe(false);
  });

  it("rejects empty segments: blank, doubled, leading or trailing separator", () => {
    expect(isSafeMountRoot("")).toBe(false);
    expect(isSafeMountRoot("/")).toBe(false);
    expect(isSafeMountRoot("/Team/Worklode")).toBe(false);
    expect(isSafeMountRoot("Team/Worklode/")).toBe(false);
    expect(isSafeMountRoot("Team//Worklode")).toBe(false);
  });

  it("rejects a segment that is blank or not already trimmed", () => {
    expect(isSafeMountRoot("   ")).toBe(false);
    expect(isSafeMountRoot(" Team/Worklode")).toBe(false);
    expect(isSafeMountRoot("Team /Worklode")).toBe(false);
    expect(isSafeMountRoot("Team/ Worklode")).toBe(false);
    expect(isSafeMountRoot("Team/Worklode ")).toBe(false);
    expect(isSafeMountRoot("Team/   /Worklode")).toBe(false);
  });

  // A backslash is a forbidden character on the root, never a separator: the
  // writer's assertInsideRoot splits relative paths on both "\" and "/", so a
  // root carrying one would have a different segment count there than here.
  it("rejects a backslash anywhere, rather than reading it as a separator", () => {
    expect(isSafeMountRoot("Team\\Worklode")).toBe(false);
    expect(isSafeMountRoot("Team/Sub\\Worklode")).toBe(false);
    expect(isSafeMountRoot("Team\\..\\Worklode")).toBe(false);
  });
});

// A nested root has no single name, so the index note takes the root's own
// folder name -- its last segment -- and lands inside the root, as it always
// has for a single-segment root.
describe("mountRootName", () => {
  it("is the root itself when the root is a single segment", () => {
    expect(mountRootName("Worklode")).toBe("Worklode");
  });

  it("is the last segment of a nested root", () => {
    expect(mountRootName("Team/Worklode")).toBe("Worklode");
    expect(mountRootName("Team/Shared Notes/Worklode")).toBe("Worklode");
  });
});

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

  it("never deletes a non-.md file under root", async () => {
    const { project, byProject } = fullScenario();
    const desired = desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

    const writer = new MapVaultWriter();
    // A file a user dropped directly into the mount; not a mirror note, so
    // it must never be swept up by the delete pass.
    writer.files.set(`${ROOT}/worklode/attachment.png`, "not markdown");

    const stats = await applyMirror(writer, ROOT, desired);

    expect(stats.removed).toBe(0);
    const remaining = await writer.list(ROOT);
    expect(remaining).toContain("worklode/attachment.png");
  });

  it("never touches a path outside the mount root when a doc/task's own project field disagrees with its grouping key", async () => {
    // The bug this guards against: a doc/task's rendered path comes from
    // its own `project` field (note.ts's *ToNote), not from the key it was
    // grouped under in byProject. A backbone response could carry either
    // one hostile while the other looks fine.
    const project = fixtureProject({ id: "worklode" });
    const hostileDoc = fixtureDoc({ id: "WL-SPEC-9", project: "../../../escape" });
    const hostileTask = fixtureTask({ id: "WL-9", project: "../../../escape" });
    const goodTask = fixtureTask({ id: "WL-1", project: "worklode" });

    const byProject = new Map([["worklode", { docs: [hostileDoc], tasks: [hostileTask, goodTask] }]]);

    const desired = desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

    expect(desired.conflicts.some((c) => c.includes("WL-SPEC-9"))).toBe(true);
    expect(desired.conflicts.some((c) => c.includes("WL-9"))).toBe(true);
    expect(desired.notes.some((n) => n.path.includes(".."))).toBe(false);
    expect(desired.notes.map((n) => n.path).sort()).toEqual(
      ["Worklode Vault.md", "worklode/tasks/WL-1.md", "worklode/worklode.md"].sort(),
    );

    const writer = new MapVaultWriter();
    const stats = await applyMirror(writer, ROOT, desired);

    expect(stats.conflicts.some((c) => c.includes("WL-SPEC-9"))).toBe(true);
    expect(writer.written.every((p) => !p.includes(".."))).toBe(true);
    for (const key of writer.files.keys()) {
      expect(key.startsWith(`${ROOT}/`)).toBe(true);
      expect(key.includes("..")).toBe(false);
    }
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
    // The whole "../escape" subtree has no safe directory, so none of its notes exist.
    expect(desired.notes.some((n) => n.path.includes(".."))).toBe(false);
    expect(desired.notes.some((n) => n.path.includes("a/b"))).toBe(false);
    // Only the good project's index/project/task notes remain.
    expect(desired.notes.map((n) => n.path).sort()).toEqual(
      ["Worklode Vault.md", "worklode/tasks/WL-1.md", "worklode/worklode.md"].sort(),
    );

    const writer = new MapVaultWriter();
    // Should not throw: the writer's guard rejects any path outside root.
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

// A nested mount root, end to end: what src/main.ts hands desiredNotes and
// applyMirror is one and the same string, so the two have to agree about a
// multi-segment root the way they already do about a single-segment one.
describe("a nested mount root", () => {
  const NESTED = "Team/Worklode";

  it("names the index note after the root's own folder, inside the root", () => {
    const project = fixtureProject();
    const byProject = new Map([["worklode", { docs: [fixtureDoc()], tasks: [fixtureTask()] }]]);

    const desired = desiredNotes([project], byProject, NESTED, SYNCED_AT);

    expect(desired.conflicts).toEqual([]);
    // "Worklode.md", not "Team/Worklode.md": paths are relative to the root,
    // so naming the index after the whole root would put it one level above
    // the folder it indexes.
    expect(desired.notes.map((n) => n.path).sort()).toEqual(
      ["Worklode.md", "worklode/docs/WL-SPEC-1.md", "worklode/tasks/WL-1.md", "worklode/worklode.md"].sort(),
    );
    // The note's title and alias are the folder name too -- parseNote strips
    // the alias back out (aliases_added), so this reads the rendered form.
    const index = desired.notes.find((n) => n.path === "Worklode.md")!;
    expect(index.content).toContain("aliases:\n  - Worklode\n");
    expect(index.content).toContain("# Worklode\n");
    // The root's parent is not part of the note's identity.
    expect(index.content).not.toContain("Team");
  });

  it("writes every note under the nested root and nowhere else", async () => {
    const project = fixtureProject();
    const byProject = new Map([["worklode", { docs: [fixtureDoc()], tasks: [fixtureTask()] }]]);
    const desired = desiredNotes([project], byProject, NESTED, SYNCED_AT);

    const writer = new MapVaultWriter();
    const stats = await applyMirror(writer, NESTED, desired);

    expect(stats.written).toBe(4);
    expect(stats.conflicts).toEqual([]);
    expect([...writer.files.keys()].sort()).toEqual(
      [
        "Team/Worklode/Worklode.md",
        "Team/Worklode/worklode/docs/WL-SPEC-1.md",
        "Team/Worklode/worklode/tasks/WL-1.md",
        "Team/Worklode/worklode/worklode.md",
      ].sort(),
    );

    // Second pass: nothing moved, so nothing is rewritten or swept up.
    const second = await applyMirror(writer, NESTED, desired);
    expect(second.written).toBe(0);
    expect(second.skipped).toBe(4);
    expect(second.removed).toBe(0);
  });

  it("skips the index and records a conflict when a segment of the root is unsafe", () => {
    const project = fixtureProject();
    const byProject = new Map([["worklode", { docs: [], tasks: [] }]]);

    const desired = desiredNotes([project], byProject, "Team/../evil", SYNCED_AT);

    expect(desired.conflicts.some((c) => c.includes("Team/../evil"))).toBe(true);
    expect(desired.notes.some((n) => n.path.includes(".."))).toBe(false);
    // The rest of the sync is unaffected, as with a single-segment bad root.
    expect(desired.notes.some((n) => n.path === "worklode/worklode.md")).toBe(true);
  });
});

// What the first-sync guard in src/main.ts asks before letting applyMirror
// loose on a root: is anything under here not ours?
describe("foreignNotes", () => {
  it("reports nothing for a root that does not exist yet", async () => {
    const writer = new MapVaultWriter();
    expect(await foreignNotes(writer, ROOT)).toEqual([]);
  });

  it("reports nothing for a root the mirror wrote", async () => {
    const project = fixtureProject();
    const byProject = new Map([["worklode", { docs: [fixtureDoc()], tasks: [fixtureTask()] }]]);
    const writer = new MapVaultWriter();
    await applyMirror(writer, ROOT, desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT));

    expect(await foreignNotes(writer, ROOT)).toEqual([]);
  });

  it("reports every .md with no readable wl block, sorted", async () => {
    const writer = new MapVaultWriter();
    await writer.write(ROOT, "Groceries.md", "# Groceries\n\nmilk\n");
    await writer.write(ROOT, "journal/2026-08-16.md", "no frontmatter at all");
    await writer.write(ROOT, "half-fenced.md", "---\ntitle: unterminated\n");
    await writer.write(ROOT, "no-wl.md", "---\ntitle: mine\n---\n# Mine\n");

    expect(await foreignNotes(writer, ROOT)).toEqual([
      "Groceries.md",
      "half-fenced.md",
      "journal/2026-08-16.md",
      "no-wl.md",
    ]);
  });

  it("separates the user's notes from the mirror's in a shared root", async () => {
    const project = fixtureProject();
    const byProject = new Map([["worklode", { docs: [], tasks: [] }]]);
    const writer = new MapVaultWriter();
    await applyMirror(writer, ROOT, desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT));
    await writer.write(ROOT, "Groceries.md", "# Groceries\n\nmilk\n");

    expect(await foreignNotes(writer, ROOT)).toEqual(["Groceries.md"]);
  });

  it("ignores non-markdown files", async () => {
    const writer = new MapVaultWriter();
    await writer.write(ROOT, "attachments/diagram.png", "binary-ish");

    expect(await foreignNotes(writer, ROOT)).toEqual([]);
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

  // A server predating the detail=true expansion returns task rows with no
  // `edges`, which taskToNote dereferences. That must cost one note, not the
  // whole sync -- the plugin ships independently of the binary, so the skew
  // is ordinary rather than exotic. The cast through unknown is deliberate:
  // the shape is not type-valid, which is exactly the point.
  it("reports a serializer failure as a conflict instead of failing the sync", async () => {
    const project = fixtureProject();
    const oldServerTask = { ...fixtureTask({ id: "WL-9" }) } as Record<string, unknown>;
    delete oldServerTask.edges;
    const goodTask = fixtureTask({ id: "WL-1" });

    const byProject = new Map([
      ["worklode", { docs: [], tasks: [oldServerTask as unknown as TaskListDetail, goodTask] }],
    ]);

    const desired = desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

    expect(desired.conflicts.some((c) => c.includes("WL-9"))).toBe(true);
    expect(desired.notes.some((n) => n.path === "worklode/tasks/WL-9.md")).toBe(false);
    // Everything else still syncs.
    expect(desired.notes.some((n) => n.path === "worklode/tasks/WL-1.md")).toBe(true);

    const writer = new MapVaultWriter();
    const stats = await applyMirror(writer, ROOT, desired);

    expect(stats.written).toBe(desired.notes.length);
    expect(stats.conflicts.some((c) => c.includes("WL-9"))).toBe(true);
  });

  it("skips the index and records a conflict when the root name is unsafe", () => {
    const project = fixtureProject();
    const byProject = new Map([["worklode", { docs: [], tasks: [] }]]);

    const desired = desiredNotes([project], byProject, "../evil", SYNCED_AT);

    expect(desired.conflicts.some((c) => c.includes("../evil"))).toBe(true);
    expect(desired.notes.some((n) => n.path.includes(".."))).toBe(false);
    // The rest of the sync is unaffected.
    expect(desired.notes.some((n) => n.path === "worklode/worklode.md")).toBe(true);
  });
});

// Sanity check that parseNote itself still throws on non-mirror content --
// the behaviour applyMirror's "not a mirror file, rewrite it" path relies on.
describe("parseNote tolerance assumption", () => {
  it("throws on content with no frontmatter fence", () => {
    expect(() => parseNote("plain text, no frontmatter")).toThrow();
  });
});
