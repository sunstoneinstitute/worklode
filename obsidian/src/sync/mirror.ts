// Diffs the backbone's desired note set against a vault and applies the
// difference: write what changed, delete what disappeared, leave the rest
// alone. Pure logic against an injected writer interface -- no Obsidian
// import, so this runs under vitest without a runtime.

import {
  docToNote,
  indexToNote,
  parseNote,
  projectToNote,
  taskToNote,
  type Note,
  type ProjectMembers,
} from "../serialize/note";
import type { Project } from "../api/types";

/** The file operations the mirror needs. Implemented against Obsidian's
 *  vault in src/vault/writer.ts, and against a map in the tests. */
export interface VaultWriter {
  /** Every .md path under root, relative to root. */
  list(root: string): Promise<string[]>;
  read(root: string, path: string): Promise<string>;
  write(root: string, path: string, content: string): Promise<void>;
  remove(root: string, path: string): Promise<void>;
}

export interface MirrorStats {
  written: number;
  skipped: number;
  removed: number;
  /** Every conflict found along the way: a wl-key collision surfaced by a
   *  rendered note, or a backbone id unsafe to use as a path segment. */
  conflicts: string[];
}

/** Every note the backbone currently implies, plus every conflict found
 *  while producing it. */
export interface DesiredSet {
  notes: Note[];
  conflicts: string[];
}

type NoteKind = "project" | "doc" | "task";

function isSafeId(id: string): boolean {
  return id.length > 0 && !id.includes("/") && !id.includes("\\") && !id.includes("..");
}

/** The vault-relative path for a project/doc/task note, or undefined when
 *  a backbone id it's built from is unsafe: empty, or containing "/", "\",
 *  or "..". This is the one guard between a hostile or malformed backbone
 *  id and a write outside the mount root -- callers must skip the object
 *  and record a conflict rather than write anyway. */
export function desiredPath(kind: NoteKind, projectId: string, id?: string): string | undefined {
  if (!isSafeId(projectId)) return undefined;
  if (kind === "project") return `${projectId}/${projectId}.md`;
  if (id === undefined || !isSafeId(id)) return undefined;
  return kind === "doc" ? `${projectId}/docs/${id}.md` : `${projectId}/tasks/${id}.md`;
}

function sortById<T extends { id: string }>(items: T[]): T[] {
  return [...items].sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}

/** Every note the backbone currently implies, in deterministic path order.
 *  A project whose own id is unsafe drops its entire subtree -- there is no
 *  safe directory to put its docs and tasks under -- while an unsafe doc or
 *  task id drops just that one note, keeping the rest of its project. */
export function desiredNotes(
  projects: Project[],
  byProject: Map<string, ProjectMembers>,
  rootName: string,
  syncedAt: string,
): DesiredSet {
  const conflicts: string[] = [];
  const notes: Note[] = [];

  const safeProjects = sortById(projects).filter((p) => {
    if (desiredPath("project", p.id) !== undefined) return true;
    conflicts.push(`project ${JSON.stringify(p.id)}: unsafe id, skipped along with its docs and tasks`);
    return false;
  });

  const filteredByProject = new Map<string, ProjectMembers>();

  for (const p of safeProjects) {
    const members = byProject.get(p.id) ?? { docs: [], tasks: [] };

    const safeDocs = sortById(members.docs).filter((d) => {
      if (desiredPath("doc", p.id, d.id) !== undefined) return true;
      conflicts.push(`doc ${JSON.stringify(d.id)} in project ${p.id}: unsafe id, skipped`);
      return false;
    });
    const safeTasks = sortById(members.tasks).filter((t) => {
      if (desiredPath("task", p.id, t.id) !== undefined) return true;
      conflicts.push(`task ${JSON.stringify(t.id)} in project ${p.id}: unsafe id, skipped`);
      return false;
    });

    filteredByProject.set(p.id, { docs: safeDocs, tasks: safeTasks });

    const projectNote = projectToNote(p, safeDocs, safeTasks);
    if (projectNote.conflict) conflicts.push(projectNote.conflict);
    notes.push(projectNote);

    for (const d of safeDocs) {
      const note = docToNote(d);
      if (note.conflict) conflicts.push(note.conflict);
      notes.push(note);
    }
    for (const t of safeTasks) {
      const note = taskToNote(t);
      if (note.conflict) conflicts.push(note.conflict);
      notes.push(note);
    }
  }

  const indexNote = indexToNote(safeProjects, filteredByProject, rootName, syncedAt);
  notes.unshift(indexNote);

  return { notes, conflicts };
}

/** Write what changed, delete what no longer belongs, leave the rest
 *  alone. The mount root is machine-owned: a note that exists but whose
 *  stored wl.etag no longer matches -- because the backbone data changed,
 *  or because a user edited (or otherwise replaced) the file -- is
 *  rewritten unconditionally, never merged. */
export async function applyMirror(writer: VaultWriter, root: string, desired: DesiredSet): Promise<MirrorStats> {
  const stats: MirrorStats = { written: 0, skipped: 0, removed: 0, conflicts: [...desired.conflicts] };

  const existing = new Set(await writer.list(root));
  const desiredPaths = new Set(desired.notes.map((n) => n.path));

  for (const note of desired.notes) {
    if (existing.has(note.path)) {
      const currentEtag = await readEtag(writer, root, note.path);
      if (currentEtag === note.etag) {
        stats.skipped++;
        continue;
      }
    }
    await writer.write(root, note.path, note.content);
    stats.written++;
  }

  for (const path of existing) {
    if (!desiredPaths.has(path)) {
      await writer.remove(root, path);
      stats.removed++;
    }
  }

  return stats;
}

/** The stored note's wl.etag, or undefined if the file isn't a mirror note
 *  at all (malformed content, missing wl block) -- tolerated as "rewrite
 *  it" rather than a fatal error. */
async function readEtag(writer: VaultWriter, root: string, path: string): Promise<string | undefined> {
  const content = await writer.read(root, path);
  try {
    return parseNote(content).wl.etag;
  } catch {
    return undefined;
  }
}
