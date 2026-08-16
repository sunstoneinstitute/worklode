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
 *  vault in src/vault/writer.ts, and against a map in the tests.
 *  - `list` must return paths relative to root. Absolute paths would never
 *    match a desired path, and the first sync would delete every file.
 *  - `write` must create missing parent folders itself: Obsidian's
 *    `adapter.write` does not, and every desired path but the index is
 *    nested at least one directory deep. */
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

/** A single path segment (a raw backbone id, or a root name) is safe when
 *  non-blank and free of separators or "..". */
function isSafeId(id: string): boolean {
  return id.trim().length > 0 && !id.includes("/") && !id.includes("\\") && !id.includes("..");
}

/** The vault-relative path for a project/doc/task note, or undefined when
 *  a backbone id it's built from is unsafe: blank, or containing "/", "\",
 *  or "..". Exposed as the reference implementation of what a safe path
 *  looks like, built only from trusted (already-known-safe) components. */
export function desiredPath(kind: NoteKind, projectId: string, id?: string): string | undefined {
  if (!isSafeId(projectId)) return undefined;
  if (kind === "project") return `${projectId}/${projectId}.md`;
  if (id === undefined || !isSafeId(id)) return undefined;
  return kind === "doc" ? `${projectId}/docs/${id}.md` : `${projectId}/tasks/${id}.md`;
}

function sortById<T extends { id: string }>(items: T[]): T[] {
  return [...items].sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}

/** Renders each item to a note, keeping only the ones whose produced path
 *  matches what `desiredPath` computes from the *trusted* project id (the
 *  key it was grouped under in byProject) and the item's own id. A note's
 *  path is actually built by *ToNote from the object's own fields (e.g.
 *  Doc.project), which can diverge from that grouping key -- comparing
 *  against the produced path, rather than trusting the object's own field,
 *  is what catches that divergence. A mismatch (including `desiredPath`
 *  itself rejecting the id) is dropped and reported in `conflicts`, along
 *  with any conflict the note itself carries (e.g. a wl-key collision). */
function filterSafe<T extends { id: string }>(
  kind: "doc" | "task",
  projectId: string,
  items: T[],
  toNote: (item: T) => Note,
  conflicts: string[],
): { items: T[]; notes: Note[] } {
  const keptItems: T[] = [];
  const keptNotes: Note[] = [];
  for (const item of items) {
    const note = toNote(item);
    const expected = desiredPath(kind, projectId, item.id);
    if (expected === undefined || note.path !== expected) {
      conflicts.push(
        `${kind} ${JSON.stringify(item.id)}: unsafe or mismatched path ${JSON.stringify(note.path)}, skipped`,
      );
      continue;
    }
    keptItems.push(item);
    keptNotes.push(note);
    if (note.conflict) conflicts.push(note.conflict);
  }
  return { items: keptItems, notes: keptNotes };
}

/** Every note the backbone currently implies, in deterministic order: the
 *  index, then each project in id order with its own note followed by its
 *  docs and tasks, each in id order. A project whose own id is unsafe drops
 *  its entire subtree -- there is no safe directory to put its docs and
 *  tasks under -- while an unsafe doc or task drops just that one note,
 *  keeping the rest of its project. An unsafe root name drops the index. */
export function desiredNotes(
  projects: Project[],
  byProject: Map<string, ProjectMembers>,
  rootName: string,
  syncedAt: string,
): DesiredSet {
  const conflicts: string[] = [];
  const notes: Note[] = [];
  const safeProjects: Project[] = [];
  const filteredByProject = new Map<string, ProjectMembers>();

  for (const p of sortById(projects)) {
    if (desiredPath("project", p.id) === undefined) {
      conflicts.push(`project ${JSON.stringify(p.id)}: unsafe id, skipped along with its docs and tasks`);
      continue;
    }

    const members = byProject.get(p.id) ?? { docs: [], tasks: [] };
    const docs = filterSafe("doc", p.id, sortById(members.docs), docToNote, conflicts);
    const tasks = filterSafe("task", p.id, sortById(members.tasks), taskToNote, conflicts);

    filteredByProject.set(p.id, { docs: docs.items, tasks: tasks.items });
    safeProjects.push(p);

    const projectNote = projectToNote(p, docs.items, tasks.items);
    if (projectNote.conflict) conflicts.push(projectNote.conflict);
    notes.push(projectNote, ...docs.notes, ...tasks.notes);
  }

  if (isSafeId(rootName)) {
    notes.unshift(indexToNote(safeProjects, filteredByProject, rootName, syncedAt));
  } else {
    conflicts.push(`index root name ${JSON.stringify(rootName)}: unsafe, index note skipped`);
  }

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
    if (!path.endsWith(".md")) continue;
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
