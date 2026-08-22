import { describe, expect, it } from "vitest";
import {
  applyMirror,
  desiredNotes,
  desiredPath,
  desiredTaskNotes,
  DOC_FETCH_CONCURRENCY,
  foreignNotes,
  hydrateDocBodies,
  isConflictNotePath,
  isDocNotePath,
  isSafeMountRoot,
  isSafePathSegment,
  isTaskNotePath,
  mountRootName,
  mountRootParent,
  mountRootParentMissing,
  type VaultWriter,
} from "../src/sync/mirror";
import { stringify as stringifyYaml } from "yaml";
import { parseNote, SERIALIZER_VERSION } from "../src/serialize/note";
import type { Doc, Project, TaskListDetail } from "../src/api/types";

/** Renders a frontmatter object as a `---`-fenced YAML block, the shape a
 *  doc body carries its own frontmatter in (internal/model.Doc.Body: "the
 *  full markdown, frontmatter included"). */
function withFrontmatter(frontmatter: Record<string, unknown>, body: string): string {
  return `---\n${stringifyYaml(frontmatter)}---\n${body}`;
}

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
    id: 1,
    project: "worklode",
    kind: "spec",
    number: 1,
    slug: "WL-SPEC-1",
    status: "draft",
    title: "A doc",
    version: 1,
    issued: "2026-01-01",
    assignee: "stig",
    created_by: "stig",
    generated_by_task: "",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-08-16T09:12:00Z",
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
  /** Folders known to exist but holding no file -- exists() checks this in
   *  addition to the files map, so a test can assert on an empty parent
   *  folder without also giving it content. */
  dirs = new Set<string>();

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

  async exists(path: string): Promise<boolean> {
    if (this.dirs.has(path)) return true;
    const prefix = `${path}/`;
    for (const key of this.files.keys()) {
      if (key === path || key.startsWith(prefix)) return true;
    }
    return false;
  }
}

// The predicate behind every backbone id (project/doc/task): each becomes
// one path segment under the mount root, so a separator in one would move
// the note somewhere the mirror never surveyed. The cases below are the ones
// the two earlier predicates disagreed on.
describe("isSafePathSegment", () => {
  it("accepts an ordinary segment", async () => {
    expect(isSafePathSegment("Worklode")).toBe(true);
    expect(isSafePathSegment("WL-SPEC-025")).toBe(true);
    expect(isSafePathSegment("My Notes")).toBe(true);
  });

  it("rejects blank, empty, and edge-whitespace segments", async () => {
    expect(isSafePathSegment("")).toBe(false);
    expect(isSafePathSegment("   ")).toBe(false);
    expect(isSafePathSegment(" x ")).toBe(false);
    expect(isSafePathSegment("x ")).toBe(false);
  });

  it('rejects ".", "..", and any ".." substring', async () => {
    expect(isSafePathSegment(".")).toBe(false);
    expect(isSafePathSegment("..")).toBe(false);
    expect(isSafePathSegment("My..Notes")).toBe(false);
  });

  it("rejects separators", async () => {
    expect(isSafePathSegment("Team/Worklode")).toBe(false);
    expect(isSafePathSegment("Team\\Worklode")).toBe(false);
  });
});

// The mount root's own predicate (applied in src/main.ts, which vitest cannot
// import). It differs from isSafePathSegment in exactly one way: "/" joins
// segments instead of disqualifying the value. Every segment still has to
// clear the single-segment bar on its own.
describe("isSafeMountRoot", () => {
  it("accepts a single segment, exactly as before", async () => {
    expect(isSafeMountRoot("Worklode")).toBe(true);
    expect(isSafeMountRoot("My Notes")).toBe(true);
  });

  it("accepts a nested root whose every segment is safe", async () => {
    expect(isSafeMountRoot("Team/Worklode")).toBe(true);
    expect(isSafeMountRoot("Team/Shared Notes/Worklode")).toBe(true);
  });

  it('rejects ".." in any segment, at any depth', async () => {
    expect(isSafeMountRoot("..")).toBe(false);
    expect(isSafeMountRoot("../Worklode")).toBe(false);
    expect(isSafeMountRoot("Team/..")).toBe(false);
    expect(isSafeMountRoot("Team/../Worklode")).toBe(false);
    expect(isSafeMountRoot("Team/../../etc/Worklode")).toBe(false);
    // ".." as a substring too, not just as a whole segment.
    expect(isSafeMountRoot("Team/My..Notes/Worklode")).toBe(false);
  });

  it('rejects "." as any segment', async () => {
    expect(isSafeMountRoot(".")).toBe(false);
    expect(isSafeMountRoot("./Worklode")).toBe(false);
    expect(isSafeMountRoot("Team/./Worklode")).toBe(false);
    expect(isSafeMountRoot("Team/.")).toBe(false);
  });

  it("rejects empty segments: blank, doubled, leading or trailing separator", async () => {
    expect(isSafeMountRoot("")).toBe(false);
    expect(isSafeMountRoot("/")).toBe(false);
    expect(isSafeMountRoot("/Team/Worklode")).toBe(false);
    expect(isSafeMountRoot("Team/Worklode/")).toBe(false);
    expect(isSafeMountRoot("Team//Worklode")).toBe(false);
  });

  it("rejects a segment that is blank or not already trimmed", async () => {
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
  it("rejects a backslash anywhere, rather than reading it as a separator", async () => {
    expect(isSafeMountRoot("Team\\Worklode")).toBe(false);
    expect(isSafeMountRoot("Team/Sub\\Worklode")).toBe(false);
    expect(isSafeMountRoot("Team\\..\\Worklode")).toBe(false);
  });
});

// A nested root has no single name, so the index note takes the root's own
// folder name -- its last segment -- and lands inside the root, as it always
// has for a single-segment root.
describe("mountRootName", () => {
  it("is the root itself when the root is a single segment", async () => {
    expect(mountRootName("Worklode")).toBe("Worklode");
  });

  it("is the last segment of a nested root", async () => {
    expect(mountRootName("Team/Worklode")).toBe("Worklode");
    expect(mountRootName("Team/Shared Notes/Worklode")).toBe("Worklode");
  });
});

// The counterpart of mountRootName: everything before the last segment.
// undefined for a single-segment root, whose "parent" is the vault root and
// always exists -- there is nothing there that write()'s ensureDir could
// create by mistake.
describe("mountRootParent", () => {
  it("is undefined for a single-segment root", async () => {
    expect(mountRootParent("Worklode")).toBeUndefined();
  });

  it("is everything before the last segment of a nested root", async () => {
    expect(mountRootParent("Team/Worklode")).toBe("Team");
    expect(mountRootParent("Team/Shared Notes/Worklode")).toBe("Team/Shared Notes");
  });
});

// The question src/main.ts asks before a sync's first write: is the mount
// root's parent about to be created silently by ensureDir? WL-82's adopt
// prompt already covers the root itself being absent (safe to take over);
// this is the one case it cannot catch -- a typo'd *parent* segment, whose
// blast radius is a stray folder created one level up, possibly inside a
// real one.
describe("mountRootParentMissing", () => {
  it("is false for a single-segment root, regardless of vault state", async () => {
    const writer = new MapVaultWriter();
    expect(await mountRootParentMissing(writer, "Worklode")).toBe(false);
  });

  it("is true when a nested root's parent does not exist", async () => {
    const writer = new MapVaultWriter();
    expect(await mountRootParentMissing(writer, "Team/Worklode")).toBe(true);
  });

  it("is false when the parent already exists (the normal case)", async () => {
    const writer = new MapVaultWriter();
    writer.dirs.add("Team");
    expect(await mountRootParentMissing(writer, "Team/Worklode")).toBe(false);
  });

  it("is false when the parent exists and holds the user's own notes", async () => {
    const writer = new MapVaultWriter();
    await writer.write("Team", "Retro.md", "the team's own note");
    expect(await mountRootParentMissing(writer, "Team/Worklode")).toBe(false);
  });

  // The WL-82 case: the parent is fine, only the root itself (one level
  // deeper) is absent -- foreignNotes/ensureRootAdopted already handles that
  // by adopting silently, and this check must stay out of its way.
  it("is false when only the root itself, not its parent, is absent", async () => {
    const writer = new MapVaultWriter();
    writer.dirs.add("Team");
    expect(await mountRootParentMissing(writer, "Team/Worklode")).toBe(false);
    expect(await foreignNotes(writer, "Team/Worklode")).toEqual([]);
  });
});

describe("desiredPath", () => {
  it("builds the vault-relative path for each note kind", async () => {
    expect(desiredPath("project", "worklode")).toBe("worklode/worklode.md");
    expect(desiredPath("doc", "worklode", "WL-SPEC-1")).toBe("worklode/docs/WL-SPEC-1.md");
    expect(desiredPath("task", "worklode", "WL-1")).toBe("worklode/tasks/WL-1.md");
  });

  it("rejects an id containing '/', '\\', '..', or that is empty", async () => {
    expect(desiredPath("project", "../escape")).toBeUndefined();
    expect(desiredPath("task", "worklode", "a/b")).toBeUndefined();
    expect(desiredPath("task", "worklode", "a\\b")).toBeUndefined();
    expect(desiredPath("doc", "worklode", "..")).toBeUndefined();
    expect(desiredPath("task", "worklode", "")).toBeUndefined();
    expect(desiredPath("project", "")).toBeUndefined();
  });
});

// Which existing files a degraded sync must leave alone: exactly the shape
// desiredPath builds for a doc, and nothing else.
describe("isDocNotePath", () => {
  it("matches a doc note and nothing else", async () => {
    expect(isDocNotePath("worklode/docs/WL-SPEC-1.md")).toBe(true);
    expect(isDocNotePath("worklode/tasks/WL-1.md")).toBe(false);
    expect(isDocNotePath("worklode/worklode.md")).toBe(false);
    expect(isDocNotePath("Worklode Vault.md")).toBe(false);
    // Not the doc-note shape: too deep, or not markdown.
    expect(isDocNotePath("worklode/docs/nested/WL-SPEC-1.md")).toBe(false);
    expect(isDocNotePath("worklode/docs/diagram.png")).toBe(false);
  });
});

describe("isTaskNotePath", () => {
  it("matches a task note and nothing else", async () => {
    expect(isTaskNotePath("worklode/tasks/WL-1.md")).toBe(true);
    expect(isTaskNotePath("worklode/docs/WL-SPEC-1.md")).toBe(false);
    expect(isTaskNotePath("worklode/worklode.md")).toBe(false);
    expect(isTaskNotePath("Worklode Vault.md")).toBe(false);
    expect(isTaskNotePath("worklode/tasks/nested/WL-1.md")).toBe(false);
    expect(isTaskNotePath("worklode/tasks/WL-1.png")).toBe(false);
  });
});

// Conflict notes are the one thing under the root the mirror writes but never
// desires, so the delete pass has to know them by their path.
describe("isConflictNotePath", () => {
  it("matches a conflict note and nothing the mirror renders", async () => {
    expect(isConflictNotePath("_conflicts/worklode/WL-1 2026-08-17T14-30-00Z.md")).toBe(true);
    expect(isConflictNotePath("worklode/tasks/WL-1.md")).toBe(false);
    expect(isConflictNotePath("worklode/docs/WL-SPEC-1.md")).toBe(false);
    expect(isConflictNotePath("worklode/worklode.md")).toBe(false);
    expect(isConflictNotePath("Worklode Vault.md")).toBe(false);
    expect(isConflictNotePath("_conflicts/worklode/screenshot.png")).toBe(false);
  });
});

// An incremental sync fetches only the tasks that changed, which is why it
// renders task notes and nothing else: the project and index notes roll up
// the whole task set, and rendering them from a delta would show wrong
// counts and rewrite both notes on every tick.
describe("desiredTaskNotes", () => {
  it("renders only task notes", async () => {
    const project = fixtureProject();
    const byProject = new Map([["worklode", { docs: [fixtureDoc()], tasks: [fixtureTask()] }]]);

    const desired = await desiredTaskNotes([project], byProject);

    expect(desired.notes.map((n) => n.path)).toEqual(["worklode/tasks/WL-1.md"]);
    expect(desired.conflicts).toEqual([]);
  });

  it("renders the same task note the full sync would", async () => {
    const project = fixtureProject();
    const byProject = new Map([["worklode", { docs: [], tasks: [fixtureTask()] }]]);

    const full = await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);
    const partial = await desiredTaskNotes([project], byProject);

    const fullTask = full.notes.find((n) => n.path === "worklode/tasks/WL-1.md");
    expect(partial.notes[0]).toEqual(fullTask);
  });

  it("drops a project whose id is unsafe and reports it", async () => {
    const project = fixtureProject({ id: "../escape" });
    const byProject = new Map([["../escape", { docs: [], tasks: [fixtureTask({ project: "../escape" })] }]]);

    const desired = await desiredTaskNotes([project], byProject);

    expect(desired.notes).toEqual([]);
    expect(desired.conflicts).toHaveLength(1);
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
    const desired = await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);
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
    const desired = await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

    const writer = new MapVaultWriter();
    await applyMirror(writer, ROOT, desired);

    const second = await applyMirror(writer, ROOT, desired);
    expect(second.written).toBe(0);
    expect(second.skipped).toBe(4);
    expect(second.removed).toBe(0);
  });

  it("rewrites a file whose etag changed", async () => {
    const { project, doc, task, byProject } = fullScenario();
    const desired1 = await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

    const writer = new MapVaultWriter();
    await applyMirror(writer, ROOT, desired1);

    const changedTask = { ...task, title: "Fix the other thing" };
    const byProject2 = new Map([["worklode", { docs: [doc], tasks: [changedTask] }]]);
    const desired2 = await desiredNotes([project], byProject2, ROOT_NAME, SYNCED_AT);

    const stats = await applyMirror(writer, ROOT, desired2);

    // The task note itself changes, and so does the project note: its etag
    // is computed over the full task list (await projectToNote(p, docs, tasks)),
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
    const desired = await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

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

  it("rewrites a note an older serializer wrote, unchanged etag and all", async () => {
    const { project, byProject } = fullScenario();
    const desired = await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

    const writer = new MapVaultWriter();
    await applyMirror(writer, ROOT, desired);

    // A note left by a previous plugin version: same backbone data, so the
    // same etag, but rendered by a serializer whose layout has since changed.
    // The etag covers the source, not the layout, so only the version stamp
    // can say the file needs re-rendering.
    const path = "worklode/docs/WL-SPEC-1.md";
    const current = await writer.read(ROOT, path);
    await writer.write(ROOT, path, current.replace(`serializer: ${SERIALIZER_VERSION}`, "serializer: 1"));

    const stats = await applyMirror(writer, ROOT, desired);

    expect(stats.written).toBe(1);
    expect(stats.skipped).toBe(3);
    expect(await writer.read(ROOT, path)).toBe(current);
  });

  it("removes a note whose backbone object disappeared", async () => {
    const { project, doc, byProject } = fullScenario();
    const desired1 = await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

    const writer = new MapVaultWriter();
    await applyMirror(writer, ROOT, desired1);

    // The task is gone from the backbone.
    const byProject2 = new Map([["worklode", { docs: [doc], tasks: [] }]]);
    const desired2 = await desiredNotes([project], byProject2, ROOT_NAME, SYNCED_AT);

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

  // A sync that could not enumerate docs (the server has no docs endpoint)
  // hands desiredNotes an empty doc list. Without pruneDocNotes:false that
  // reads as "every doc was deleted" and the delete pass takes the user's
  // mirrored doc notes with it -- data loss on a signal that says nothing
  // about docs.
  it("keeps doc notes a degraded sync could not enumerate, and still prunes tasks", async () => {
    const { project, byProject } = fullScenario();
    const writer = new MapVaultWriter();
    await applyMirror(writer, ROOT, await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT));

    // No docs enumerated, and the task really is gone from the backbone.
    const degraded = new Map([["worklode", { docs: [], tasks: [] }]]);
    const desired = await desiredNotes([project], degraded, ROOT_NAME, SYNCED_AT);
    const stats = await applyMirror(writer, ROOT, desired, { pruneDocNotes: false });

    expect(writer.removed).toEqual(["worklode/tasks/WL-1.md"]);
    expect(stats.removed).toBe(1);

    const remaining = await writer.list(ROOT);
    expect(remaining).toContain("worklode/docs/WL-SPEC-1.md");
    expect(await writer.read(ROOT, "worklode/docs/WL-SPEC-1.md")).toContain("doc body");
  });

  it("prunes a doc note when docs were enumerated", async () => {
    const { project, byProject } = fullScenario();
    const writer = new MapVaultWriter();
    await applyMirror(writer, ROOT, await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT));

    // The doc is gone from the backbone, and this sync did enumerate docs.
    const without = new Map([["worklode", { docs: [], tasks: [fixtureTask()] }]]);
    const desired = await desiredNotes([project], without, ROOT_NAME, SYNCED_AT);
    const stats = await applyMirror(writer, ROOT, desired);

    expect(writer.removed).toEqual(["worklode/docs/WL-SPEC-1.md"]);
    expect(stats.removed).toBe(1);
    expect(await writer.list(ROOT)).not.toContain("worklode/docs/WL-SPEC-1.md");
  });

  // The whole point of the incremental path. "Not in the desired set" means
  // "gone from the backbone" only for a sync that enumerated everything; an
  // updated_since fetch answers "what changed", and a deleted task never
  // appears in that answer. So it prunes nothing at all -- not the note of a
  // task it simply did not ask about, and not the project, index and doc
  // notes it does not render either.
  it("deletes nothing when the sync only enumerated what changed", async () => {
    const project = fixtureProject();
    const stale = fixtureTask();
    const changed = fixtureTask({ id: "WL-2", title: "Ship the thing", branch: "WL-2-ship-the-thing" });
    const writer = new MapVaultWriter();
    const byProject = new Map([["worklode", { docs: [fixtureDoc()], tasks: [stale, changed] }]]);
    await applyMirror(writer, ROOT, await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT));

    // Only WL-2 changed since the watermark; WL-1 may be untouched or gone
    // from the backbone entirely, and this response cannot tell the mirror
    // which.
    const delta = new Map([["worklode", { docs: [], tasks: [fixtureTask({ id: "WL-2", title: "Ship it" })] }]]);
    const stats = await applyMirror(writer, ROOT, await desiredTaskNotes([project], delta), {
      pruneDocNotes: false,
      pruneTaskNotes: false,
      pruneOtherNotes: false,
    });

    expect(writer.removed).toEqual([]);
    expect(stats.removed).toBe(0);
    const remaining = await writer.list(ROOT);
    expect(remaining).toContain("worklode/tasks/WL-1.md");
    expect(remaining).toContain("worklode/docs/WL-SPEC-1.md");
    expect(remaining).toContain("worklode/worklode.md");
    expect(remaining).toContain(`${ROOT_NAME}.md`);
  });

  // The roll-up half: a delta cannot render the project note's counts or the
  // index's project list correctly, so an incremental sync must not touch
  // either. They stay as the last full sync left them -- stale until the next
  // one, which is the cheaper wrong answer than deriving them from a delta.
  it("leaves the project and index notes byte-identical on an incremental sync", async () => {
    const project = fixtureProject();
    const byProject = new Map([["worklode", { docs: [fixtureDoc()], tasks: [fixtureTask()] }]]);
    const writer = new MapVaultWriter();
    await applyMirror(writer, ROOT, await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT));

    const before = new Map(writer.files);
    writer.written = [];

    const delta = new Map([["worklode", { docs: [], tasks: [fixtureTask({ title: "Fix the other thing" })] }]]);
    const stats = await applyMirror(writer, ROOT, await desiredTaskNotes([project], delta), {
      pruneDocNotes: false,
      pruneTaskNotes: false,
      pruneOtherNotes: false,
    });

    expect(writer.written).toEqual(["worklode/tasks/WL-1.md"]);
    expect(stats.written).toBe(1);
    for (const path of [`${ROOT}/${ROOT_NAME}.md`, `${ROOT}/worklode/worklode.md`, `${ROOT}/worklode/docs/WL-SPEC-1.md`]) {
      expect(writer.files.get(path)).toBe(before.get(path));
    }
    expect(await writer.read(ROOT, "worklode/tasks/WL-1.md")).toContain("Fix the other thing");
  });

  it("never deletes a non-.md file under root", async () => {
    const { project, byProject } = fullScenario();
    const desired = await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

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
    const hostileDoc = fixtureDoc({ slug: "WL-SPEC-9", project: "../../../escape" });
    const hostileTask = fixtureTask({ id: "WL-9", project: "../../../escape" });
    const goodTask = fixtureTask({ id: "WL-1", project: "worklode" });

    const byProject = new Map([["worklode", { docs: [hostileDoc], tasks: [hostileTask, goodTask] }]]);

    const desired = await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

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

    const badProjectDoc = fixtureDoc({ slug: "WL-SPEC-9", project: "../escape" });
    const badProjectTask = fixtureTask({ id: "WL-9", project: "../escape" });
    const badIdTask = fixtureTask({ id: "a/b", project: "worklode" });
    const goodTask = fixtureTask({ id: "WL-1", project: "worklode" });

    const byProject = new Map([
      ["../escape", { docs: [badProjectDoc], tasks: [badProjectTask] }],
      ["worklode", { docs: [], tasks: [badIdTask, goodTask] }],
    ]);

    const desired = await desiredNotes([badProject, goodProject], byProject, ROOT_NAME, SYNCED_AT);

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

  it("names the index note after the root's own folder, inside the root", async () => {
    const project = fixtureProject();
    const byProject = new Map([["worklode", { docs: [fixtureDoc()], tasks: [fixtureTask()] }]]);

    const desired = await desiredNotes([project], byProject, NESTED, SYNCED_AT);

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
    const desired = await desiredNotes([project], byProject, NESTED, SYNCED_AT);

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

  it("skips the index and records a conflict when a segment of the root is unsafe", async () => {
    const project = fixtureProject();
    const byProject = new Map([["worklode", { docs: [], tasks: [] }]]);

    const desired = await desiredNotes([project], byProject, "Team/../evil", SYNCED_AT);

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
    await applyMirror(writer, ROOT, await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT));

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
    await applyMirror(writer, ROOT, await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT));
    await writer.write(ROOT, "Groceries.md", "# Groceries\n\nmilk\n");

    expect(await foreignNotes(writer, ROOT)).toEqual(["Groceries.md"]);
  });

  it("ignores non-markdown files", async () => {
    const writer = new MapVaultWriter();
    await writer.write(ROOT, "attachments/diagram.png", "binary-ish");

    expect(await foreignNotes(writer, ROOT)).toEqual([]);
  });
});

// desiredNotes renders projects concurrently (each note costs an awaited
// digest), so the order it documents is no longer a free consequence of a
// serial loop -- it is stitched back together deliberately, and these are the
// assertions that say so. The index note's own etag hashes the project array,
// so a reordering here would change it and rewrite the note for nothing.
describe("desiredNotes ordering", () => {
  it("emits the index first, then each project in id order with its docs then tasks", async () => {
    const projects = [
      fixtureProject({ id: "zebra", name: "Zebra" }),
      fixtureProject({ id: "alpha", name: "Alpha" }),
      fixtureProject({ id: "middle", name: "Middle" }),
    ];
    // Members are supplied out of id order, so the sorting is the code's own
    // doing rather than the fixture's. A member's `project` field has to
    // match its grouping key, or filterSafe drops it as a mismatched path.
    const members = (project: string, prefix: string) => ({
      docs: [
        fixtureDoc({ slug: `${prefix}-DOC-2`, project }),
        fixtureDoc({ slug: `${prefix}-DOC-1`, project }),
      ],
      tasks: [fixtureTask({ id: `${prefix}-2`, project }), fixtureTask({ id: `${prefix}-1`, project })],
    });
    const byProject = new Map([
      ["zebra", members("zebra", "Z")],
      ["alpha", members("alpha", "A")],
      ["middle", members("middle", "M")],
    ]);

    const desired = await desiredNotes(projects, byProject, ROOT_NAME, SYNCED_AT);

    expect(desired.conflicts).toEqual([]);
    expect(desired.notes.map((n) => n.path)).toEqual([
      "Worklode Vault.md",
      "alpha/alpha.md",
      "alpha/docs/A-DOC-1.md",
      "alpha/docs/A-DOC-2.md",
      "alpha/tasks/A-1.md",
      "alpha/tasks/A-2.md",
      "middle/middle.md",
      "middle/docs/M-DOC-1.md",
      "middle/docs/M-DOC-2.md",
      "middle/tasks/M-1.md",
      "middle/tasks/M-2.md",
      "zebra/zebra.md",
      "zebra/docs/Z-DOC-1.md",
      "zebra/docs/Z-DOC-2.md",
      "zebra/tasks/Z-1.md",
      "zebra/tasks/Z-2.md",
    ]);
  });

  it("reports conflicts in project-id order, docs before tasks within a project", async () => {
    const projects = [fixtureProject({ id: "zebra" }), fixtureProject({ id: "alpha" })];
    const bad = (slug: string, project: string) => ({ ...fixtureDoc({ slug, project }) });
    const byProject = new Map([
      [
        "zebra",
        {
          docs: [bad("Z/DOC", "zebra")],
          tasks: [fixtureTask({ id: "Z/TASK", project: "zebra" })],
        },
      ],
      [
        "alpha",
        {
          docs: [bad("A/DOC", "alpha")],
          tasks: [fixtureTask({ id: "A/TASK", project: "alpha" })],
        },
      ],
    ]);

    const desired = await desiredNotes(projects, byProject, ROOT_NAME, SYNCED_AT);

    // alpha before zebra, and within each, the doc before the task -- the
    // order a serial loop produced, preserved across the concurrency.
    expect(desired.conflicts.map((c) => c.split(":")[0])).toEqual([
      'doc "A/DOC"',
      'task "A/TASK"',
      'doc "Z/DOC"',
      'task "Z/TASK"',
    ]);
  });
});

describe("desiredNotes conflicts", () => {
  it("drains a rendered note's own conflict (e.g. a wl key collision) into the report", async () => {
    const project = fixtureProject();
    const collidingDoc = fixtureDoc({ body: withFrontmatter({ wl: { author_note: "collides" } }, "doc body") });
    const byProject = new Map([["worklode", { docs: [collidingDoc], tasks: [] }]]);

    const desired = await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

    expect(
      desired.conflicts.some((c) => c.includes(collidingDoc.slug) && c.includes('already has a "wl" key')),
    ).toBe(true);
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

    const desired = await desiredNotes([project], byProject, ROOT_NAME, SYNCED_AT);

    expect(desired.conflicts.some((c) => c.includes("WL-9"))).toBe(true);
    expect(desired.notes.some((n) => n.path === "worklode/tasks/WL-9.md")).toBe(false);
    // Everything else still syncs.
    expect(desired.notes.some((n) => n.path === "worklode/tasks/WL-1.md")).toBe(true);

    const writer = new MapVaultWriter();
    const stats = await applyMirror(writer, ROOT, desired);

    expect(stats.written).toBe(desired.notes.length);
    expect(stats.conflicts.some((c) => c.includes("WL-9"))).toBe(true);
  });

  it("skips the index and records a conflict when the root name is unsafe", async () => {
    const project = fixtureProject();
    const byProject = new Map([["worklode", { docs: [], tasks: [] }]]);

    const desired = await desiredNotes([project], byProject, "../evil", SYNCED_AT);

    expect(desired.conflicts.some((c) => c.includes("../evil"))).toBe(true);
    expect(desired.notes.some((n) => n.path.includes(".."))).toBe(false);
    // The rest of the sync is unaffected.
    expect(desired.notes.some((n) => n.path === "worklode/worklode.md")).toBe(true);
  });
});

// GET /api/v1/docs blanks every body, so a doc note rendered from a list row
// alone is a `wl` block and no text -- the bug WL-196 fixes. The fix is one
// extra request per document, spent only where the vault's note is out of
// date, so these tests are as much about the requests *not* made.
describe("hydrateDocBodies", () => {
  /** A list row: the shape GET /api/v1/docs actually answers with. */
  function listRow(overrides: Partial<Doc> = {}): Doc {
    return fixtureDoc({ body: "", ...overrides });
  }

  /** The same document as GET /api/v1/docs/{id} serves it, body and all. */
  function fetchedFor(row: Doc): Doc {
    return { ...row, body: withFrontmatter({ status: row.status }, `# ${row.title}\n\ntext of ${row.slug}\n`) };
  }

  /** A fetchDoc that records what it was asked for, answers from `rows`, and
   *  tracks how many calls were ever in flight at once -- the bound
   *  DOC_FETCH_CONCURRENCY claims. Resolving on a later microtask is what makes
   *  overlap observable at all; a synchronous answer would serialize. */
  function recordingFetch(rows: Doc[]): {
    fetch: (id: number) => Promise<Doc>;
    asked: number[];
    maxInFlight: () => number;
  } {
    const asked: number[] = [];
    const byId = new Map(rows.map((r) => [r.id, r]));
    let inFlight = 0;
    let peak = 0;
    return {
      asked,
      maxInFlight: () => peak,
      fetch: async (id) => {
        asked.push(id);
        inFlight++;
        peak = Math.max(peak, inFlight);
        try {
          await Promise.resolve();
          await Promise.resolve();
          const row = byId.get(id);
          if (row === undefined) throw new Error(`no such doc: ${id}`);
          return fetchedFor(row);
        } finally {
          inFlight--;
        }
      },
    };
  }

  /** Renders and applies one full sync over `rows`, hydrating first and
   *  carrying `unfetched` into applyMirror, exactly as src/main.ts does. */
  async function syncOnce(
    writer: MapVaultWriter,
    rows: Doc[],
  ): Promise<{ asked: number[]; fetched: number; written: number; skipped: number; maxInFlight: number }> {
    const { fetch, asked, maxInFlight } = recordingFetch(rows);
    const byProject = new Map([["worklode", { docs: rows, tasks: [fixtureTask()] }]]);
    const hydrated = await hydrateDocBodies(writer, ROOT, byProject, fetch);
    const desired = await desiredNotes([fixtureProject()], hydrated.byProject, ROOT_NAME, SYNCED_AT);
    expect(desired.conflicts).toEqual([]);
    const stats = await applyMirror(writer, ROOT, desired, { alreadyCurrent: hydrated.unfetched });
    return {
      asked,
      fetched: hydrated.fetched,
      written: stats.written,
      skipped: stats.skipped,
      maxInFlight: maxInFlight(),
    };
  }

  it("fetches every document on a first sync, and renders its body", async () => {
    const writer = new MapVaultWriter();
    const rows = [listRow({ id: 25, slug: "documents" }), listRow({ id: 4, slug: "backbone" })];

    const first = await syncOnce(writer, rows);

    expect(first.asked.sort((a, b) => a - b)).toEqual([4, 25]);
    expect(first.fetched).toBe(2);

    // The whole point: text in the note, and the document's own frontmatter
    // lifted out of the body into the note's -- splitDocFrontmatter's path,
    // which no production sync could reach before.
    const note = await writer.read(ROOT, "worklode/docs/documents.md");
    const parsed = parseNote(note);
    expect(parsed.body).toBe("# A doc\n\ntext of documents\n");
    expect(parsed.frontmatter).toEqual({ status: "draft" });
    // The document brought its own H1, so no second one was injected -- a
    // decision docToNote could only ever get right once it had a body.
    expect(parsed.wl.heading_added).toBe(false);
    expect(note.match(/^# /gm)).toHaveLength(1);
  });

  it("fetches nothing on a second sync of unchanged documents", async () => {
    const writer = new MapVaultWriter();
    const rows = [listRow({ id: 25, slug: "documents" })];
    await syncOnce(writer, rows);
    const before = await writer.read(ROOT, "worklode/docs/documents.md");

    const second = await syncOnce(writer, rows);

    expect(second.asked).toEqual([]);
    expect(second.fetched).toBe(0);
    // And the blank-bodied render the sync produced for the doc it did not
    // fetch never reaches disk: applyMirror skips it, because it asks the
    // same question hydrateDocBodies asked.
    expect(second.written).toBe(0);
    expect(await writer.read(ROOT, "worklode/docs/documents.md")).toBe(before);
  });

  it("re-fetches a document whose version bumped", async () => {
    const writer = new MapVaultWriter();
    await syncOnce(writer, [listRow({ id: 25, slug: "documents", version: 3 })]);

    const bumped = await syncOnce(writer, [
      listRow({ id: 25, slug: "documents", version: 4, title: "Documents, revised" }),
    ]);

    expect(bumped.asked).toEqual([25]);
    const parsed = parseNote(await writer.read(ROOT, "worklode/docs/documents.md"));
    expect(parsed.wl.version).toBe(4);
    expect(parsed.body).toContain("text of documents");
  });

  // The assertion that actually catches a blank write in a mixed batch: the
  // document that was not fetched still has its text afterwards, even though
  // it was rendered from a blank list row in the same pass.
  it("re-fetches only the document that changed, and leaves the other's text intact", async () => {
    const writer = new MapVaultWriter();
    const stable = listRow({ id: 4, slug: "backbone" });
    await syncOnce(writer, [listRow({ id: 25, slug: "documents", version: 3 }), stable]);
    const stablePath = "worklode/docs/backbone.md";
    const before = await writer.read(ROOT, stablePath);

    const second = await syncOnce(writer, [listRow({ id: 25, slug: "documents", version: 4 }), stable]);

    expect(second.asked).toEqual([25]);
    expect(await writer.read(ROOT, stablePath)).toBe(before);
    expect(parseNote(await writer.read(ROOT, stablePath)).body).toContain("text of backbone");
  });

  // The TOCTOU applyMirror's `alreadyCurrent` closes. Between hydrate's read
  // and applyMirror's there is a whole fetch phase; a note that disappears in
  // that window must not be recreated from the blank list row, because the
  // blank would carry the correct etag and no later sync would find it stale.
  it("does not write a blank note when an unfetched note vanishes mid-sync", async () => {
    const writer = new MapVaultWriter();
    const rows = [listRow({ id: 25, slug: "documents" })];
    await syncOnce(writer, rows);

    const path = "worklode/docs/documents.md";
    const byProject = new Map([["worklode", { docs: rows, tasks: [fixtureTask()] }]]);
    const hydrated = await hydrateDocBodies(writer, ROOT, byProject, () => {
      throw new Error("must not fetch: the note was current when it was asked about");
    });
    expect(hydrated.unfetched).toContain(path);

    writer.files.delete(`${ROOT}/${path}`);
    const desired = await desiredNotes([fixtureProject()], hydrated.byProject, ROOT_NAME, SYNCED_AT);
    await applyMirror(writer, ROOT, desired, { alreadyCurrent: hydrated.unfetched });

    // Left missing rather than written blank, and the next sync restores it
    // with its text, because a missing note is stale.
    expect(writer.files.has(`${ROOT}/${path}`)).toBe(false);
    const repair = await syncOnce(writer, rows);
    expect(repair.asked).toEqual([25]);
    expect(parseNote(await writer.read(ROOT, path)).body).toContain("text of documents");
  });

  it("re-fetches a document whose note was deleted or corrupted", async () => {
    const writer = new MapVaultWriter();
    const rows = [listRow({ id: 25, slug: "documents" })];
    await syncOnce(writer, rows);

    writer.files.delete(`${ROOT}/worklode/docs/documents.md`);
    expect((await syncOnce(writer, rows)).asked).toEqual([25]);

    await writer.write(ROOT, "worklode/docs/documents.md", "a hand-written note, no wl block");
    expect((await syncOnce(writer, rows)).asked).toEqual([25]);
  });

  it("re-fetches every document when the serializer version moves", async () => {
    const writer = new MapVaultWriter();
    const rows = [listRow({ id: 25, slug: "documents" })];
    await syncOnce(writer, rows);

    // A note an older plugin wrote: current etag, stale layout. applyMirror
    // rewrites it, so hydrateDocBodies has to have fetched it -- the two
    // predicates being one function is what guarantees that.
    const path = `${ROOT}/worklode/docs/documents.md`;
    writer.files.set(path, writer.files.get(path)!.replace(`serializer: ${SERIALIZER_VERSION}`, "serializer: 1"));

    expect((await syncOnce(writer, rows)).asked).toEqual([25]);
  });

  it("spends no request on a document whose note path is unsafe", async () => {
    const writer = new MapVaultWriter();
    const { fetch, asked } = recordingFetch([]);
    const byProject = new Map([["worklode", { docs: [listRow({ id: 25, slug: "../evil" })], tasks: [] }]]);

    const hydrated = await hydrateDocBodies(writer, ROOT, byProject, fetch);

    expect(asked).toEqual([]);
    expect(hydrated.fetched).toBe(0);
  });

  // The grouping key is the trusted one, exactly as in filterSafe and
  // writeBackTask: a document whose own `project` disagrees is one desiredNotes
  // drops, and asking about doc.project would ask about a different file than
  // the one applyMirror decides on.
  it("spends no request on a document whose own project is not the one it came under", async () => {
    const writer = new MapVaultWriter();
    const { fetch, asked } = recordingFetch([]);
    const stray = listRow({ id: 25, slug: "documents", project: "elsewhere" });
    const byProject = new Map([["worklode", { docs: [stray], tasks: [] }]]);

    const hydrated = await hydrateDocBodies(writer, ROOT, byProject, fetch);

    expect(asked).toEqual([]);
    expect(hydrated.unfetched.size).toBe(0);
    const desired = await desiredNotes([fixtureProject()], hydrated.byProject, ROOT_NAME, SYNCED_AT);
    expect(desired.notes.some((n) => n.path.includes("docs/"))).toBe(false);
    expect(desired.conflicts).toHaveLength(1);
  });

  it("leaves the input map alone and keeps the tasks beside the docs", async () => {
    const writer = new MapVaultWriter();
    const row = listRow({ id: 25, slug: "documents" });
    const task = fixtureTask();
    const byProject = new Map([["worklode", { docs: [row], tasks: [task] }]]);
    const { fetch } = recordingFetch([row]);

    const hydrated = await hydrateDocBodies(writer, ROOT, byProject, fetch);

    expect(byProject.get("worklode")!.docs[0].body).toBe("");
    expect(hydrated.byProject.get("worklode")!.docs[0].body).not.toBe("");
    expect(hydrated.byProject.get("worklode")!.tasks).toEqual([task]);
  });

  // A document the list just named must be fetchable; degrading here would
  // render its note from the blank list row, and applyMirror would then write
  // that over the text already in the vault.
  it("fails the sync when a document's body cannot be fetched", async () => {
    const writer = new MapVaultWriter();
    const byProject = new Map([["worklode", { docs: [listRow({ id: 25 })], tasks: [] }]]);

    await expect(
      hydrateDocBodies(writer, ROOT, byProject, () => Promise.reject(new Error("boom"))),
    ).rejects.toThrow("boom");
  });

  it("fetches more documents than it runs in parallel, without losing any", async () => {
    const writer = new MapVaultWriter();
    const rows = Array.from({ length: 11 }, (_, i) => listRow({ id: i + 1, slug: `doc-${i + 1}` }));

    const first = await syncOnce(writer, rows);

    expect(first.asked.sort((a, b) => a - b)).toEqual(rows.map((r) => r.id));
    for (const row of rows) {
      expect(await writer.read(ROOT, `worklode/docs/${row.slug}.md`)).toContain(`text of ${row.slug}`);
    }
    // Pins the bound in both directions: more than one at a time (or the
    // limit buys nothing over a serial loop), never more than the constant
    // (or a corpus arrives as one burst of simultaneous requests).
    expect(first.maxInFlight).toBeGreaterThan(1);
    expect(first.maxInFlight).toBe(DOC_FETCH_CONCURRENCY);
  });
});

// Sanity check that parseNote itself still throws on non-mirror content --
// the behaviour applyMirror's "not a mirror file, rewrite it" path relies on.
describe("parseNote tolerance assumption", () => {
  it("throws on content with no frontmatter fence", async () => {
    expect(() => parseNote("plain text, no frontmatter")).toThrow();
  });
});
