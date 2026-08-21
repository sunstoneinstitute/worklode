// Diffs the backbone's desired note set against a vault and applies the
// difference: write what changed, delete what disappeared, leave the rest
// alone. Pure logic against an injected writer interface -- no Obsidian
// import, so this runs under vitest without a runtime.

import {
  CONFLICT_FOLDER,
  docToNote,
  indexToNote,
  parseNote,
  projectToNote,
  SERIALIZER_VERSION,
  taskToNote,
  type Note,
  type ProjectMembers,
  type WlBlock,
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
  /** Takes the file out of the mirror. Recoverable in the Obsidian
   *  implementation (vault trash), because applyMirror deletes every .md
   *  under the root the backbone does not imply -- including files the
   *  mirror never created. */
  remove(root: string, path: string): Promise<void>;
  /** Whether a vault-relative path exists as a file or folder -- not
   *  necessarily under any mount root. Used only to check the mount root's
   *  own *parent* before write()'s ensureDir would silently create it; see
   *  mountRootParent and mountRootParentMissing. */
  exists(path: string): Promise<boolean>;
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

/** What the delete pass is allowed to remove. Every flag defaults to true --
 *  a full sync enumerated everything, so anything it does not imply is gone
 *  -- and is set false by a sync that did not enumerate that kind, where an
 *  absent note means "unknown", not "deleted". */
export interface MirrorOptions {
  /** Doc notes. False when the sync could not enumerate docs at all -- a
   *  server with no docs endpoint. */
  pruneDocNotes?: boolean;
  /** Task notes. False for an incremental sync, whose task set is "what
   *  changed since", not "what exists": a task deleted from the backbone
   *  never appears in that answer. */
  pruneTaskNotes?: boolean;
  /** Everything else under the root -- project notes, the index note, and any
   *  .md the mirror did not write. False for an incremental sync, which
   *  renders neither of the first two (see desiredTaskNotes) and would
   *  otherwise delete both. */
  pruneOtherNotes?: boolean;
}

type NoteKind = "project" | "doc" | "task";

/** The one rule for anything used as a single path segment: a project or
 *  task id, or a doc slug, each of which becomes one directory or file name
 *  under the mount root. Safe means: non-blank, already trimmed (the caller
 *  validates and uses the same value, so nothing is silently trimmed
 *  downstream), no separator, and no ".." anywhere -- neither as the whole
 *  segment nor as a substring.
 *
 *  Ids stay single-segment even though the mount root no longer has to be
 *  (see isSafeMountRoot). The root is the setting's own path, chosen once by
 *  the user and surveyed before anything is deleted under it; an id is
 *  whatever the server sent, and a separator in one would place a note
 *  outside the subtree the mirror actually surveyed while still looking
 *  local. The two jobs shared a predicate while the root happened to be one
 *  segment too; they are not the same rule, and only one of them may relax.
 *  What both still forbid is a value the writer creates and the vault lists
 *  back differently -- "." or an untrimmed name -- which would be rewritten
 *  forever. */
export function isSafePathSegment(segment: string): boolean {
  if (segment.length === 0 || segment !== segment.trim()) return false;
  if (segment.includes("/") || segment.includes("\\")) return false;
  return segment !== "." && !segment.includes("..");
}

/** The rule for the mount root: the one territory the mirror may write,
 *  delete and (for purge) recursively rmdir under, validated by src/main.ts
 *  before any of that happens. A root may be nested ("Team/Worklode"), so
 *  "/" joins segments here instead of disqualifying the value -- but every
 *  segment must independently clear isSafePathSegment. That rejects ".." at
 *  any depth (which would resolve above the intended folder), an empty
 *  segment (a leading or trailing "/", or "a//b" -- forms the vault lists
 *  back differently from how the writer spells them), "." as a segment, and
 *  a segment with edge whitespace.
 *
 *  A backslash is a forbidden character on the root, never a separator: the
 *  writer's assertInsideRoot splits relative paths on both "\" and "/", so a
 *  root containing one would have a different segment count here than there
 *  -- and Obsidian's adapter, which joins with "/" only, would read it as a
 *  literal character in a name. isSafePathSegment rejecting "\" gives that
 *  for free.
 *
 *  Nothing else about the root is relaxed: it stays the exact string every
 *  writer call prefixes with, so a root accepted here can never be one
 *  desiredNotes then judges unsafe. */
export function isSafeMountRoot(root: string): boolean {
  return root.split("/").every(isSafePathSegment);
}

/** The mount root's own folder name -- its last segment. A nested root has
 *  no single name, and the index note lives *inside* the root at a path
 *  relative to it, so naming it after the whole root ("Team/Worklode.md")
 *  would put it beside the folder it indexes rather than in it. The leaf is
 *  what identifies the folder in Obsidian's own UI, and for a single-segment
 *  root it is the root itself -- so this changes nothing for existing
 *  vaults. Safe by construction: every segment of a safe root is a safe
 *  segment. */
export function mountRootName(root: string): string {
  return root.slice(root.lastIndexOf("/") + 1);
}

/** The mount root's own parent path, or undefined when there is none worth
 *  asking about: a single-segment root's parent is the vault root, which
 *  always exists, so nothing there could be created by mistake. Splits on
 *  "/" only, mirroring mountRootName -- isSafeMountRoot already forbids a
 *  backslash in the root outright, so the two never disagree about where a
 *  segment ends. */
export function mountRootParent(root: string): string | undefined {
  const idx = root.lastIndexOf("/");
  return idx === -1 ? undefined : root.slice(0, idx);
}

/** Whether the mount root's own parent is missing and about to be created
 *  silently by write()'s ensureDir -- the question src/main.ts asks before a
 *  sync's first write, alongside the foreignNotes/adopt-root guard.
 *
 *  WL-82's adopt prompt already covers the root itself being absent (safe to
 *  take over silently, since there is nothing under a root that does not
 *  exist to lose); it does not catch a typo'd *parent* segment, whose blast
 *  radius is a stray folder created one level up -- possibly inside a real
 *  one. This is that missing check, asked once per sync rather than
 *  remembered: once the parent exists, whether because the user made it or
 *  because Worklode created it after a confirmation, it stays existing --
 *  nothing here ever removes a mount root's parent -- so there is nothing to
 *  persist the way adoptedRoots persists a confirmed root. */
export async function mountRootParentMissing(writer: VaultWriter, root: string): Promise<boolean> {
  const parent = mountRootParent(root);
  return parent !== undefined && !(await writer.exists(parent));
}

/** The vault-relative path for a project/doc/task note, or undefined when a
 *  backbone id it's built from fails isSafePathSegment. Exposed as the
 *  reference implementation of what a safe path looks like, built only from
 *  trusted (already-known-safe) components. */
export function desiredPath(kind: NoteKind, projectId: string, id?: string): string | undefined {
  if (!isSafePathSegment(projectId)) return undefined;
  if (kind === "project") return `${projectId}/${projectId}.md`;
  if (id === undefined || !isSafePathSegment(id)) return undefined;
  return kind === "doc" ? `${projectId}/docs/${id}.md` : `${projectId}/tasks/${id}.md`;
}

/** Whether a mirror-relative path has the doc-note shape desiredPath builds:
 *  `<project>/docs/<id>.md`, exactly three segments deep. Matching on the
 *  path rather than on the desired set is the point -- a degraded sync has no
 *  doc set to compare against, and what it must leave alone is whatever it
 *  wrote there on a healthier pass. */
export function isDocNotePath(path: string): boolean {
  return isMemberNotePath(path, "docs");
}

/** Whether a mirror-relative path has the task-note shape desiredPath builds:
 *  `<project>/tasks/<id>.md`. The counterpart of isDocNotePath, for the same
 *  reason: an incremental sync has no full task set to compare against, so
 *  what it must leave alone is identified by path. */
export function isTaskNotePath(path: string): boolean {
  return isMemberNotePath(path, "tasks");
}

/** Whether a mirror-relative path is a conflict note the write-back pass
 *  wrote (see conflictToNote). No desired set ever contains one, so without
 *  this the delete pass would sweep it up on the very sync that created it --
 *  destroying the body it exists to preserve. It is the user's copy to keep or
 *  delete, so the mirror never removes one. */
export function isConflictNotePath(path: string): boolean {
  return path.startsWith(`${CONFLICT_FOLDER}/`) && path.endsWith(".md");
}

function isMemberNotePath(path: string, folder: "docs" | "tasks"): boolean {
  const segments = path.split("/");
  return segments.length === 3 && segments[1] === folder && path.endsWith(".md");
}

function sortByKey<T>(items: T[], keyOf: (item: T) => string): T[] {
  return [...items].sort((a, b) => {
    const ka = keyOf(a);
    const kb = keyOf(b);
    return ka < kb ? -1 : ka > kb ? 1 : 0;
  });
}

/** Renders each item to a note, keeping only the ones whose produced path
 *  matches what `desiredPath` computes from the *trusted* project id (the
 *  key it was grouped under in byProject) and the item's own key -- `keyOf`,
 *  the path segment that identifies the item: a task's id, or a doc's slug.
 *  A note's path is actually built by *ToNote from the object's own fields
 *  (e.g. Doc.project), which can diverge from that grouping key -- comparing
 *  against the produced path, rather than trusting the object's own field,
 *  is what catches that divergence. A mismatch (including `desiredPath`
 *  itself rejecting the key) is dropped and reported in `conflicts`, along
 *  with any conflict the note itself carries (e.g. a wl-key collision).
 *
 *  Rendering itself is caught too: a row shaped differently from what the
 *  serializer expects -- a server predating the `detail=true` expansion
 *  returns tasks with no `edges` at all -- costs that one note, not the
 *  whole sync. A rendering failure is now a rejected promise rather than a
 *  synchronous throw, which allSettled turns back into a per-item outcome.
 *
 *  Every item is rendered concurrently and the outcomes are drained in the
 *  input order: rendering awaits a digest per note, so a serial loop would
 *  make a sync as slow as its note count, while draining by index keeps both
 *  the kept-note order and the order conflicts are reported in exactly what
 *  the serial version produced. */
async function filterSafe<T>(
  kind: "doc" | "task",
  projectId: string,
  items: T[],
  keyOf: (item: T) => string,
  toNote: (item: T) => Promise<Note>,
  conflicts: string[],
): Promise<{ items: T[]; notes: Note[] }> {
  const rendered = await Promise.allSettled(items.map((item) => toNote(item)));

  const keptItems: T[] = [];
  const keptNotes: Note[] = [];
  for (const [i, outcome] of rendered.entries()) {
    const item = items[i];
    const key = keyOf(item);
    if (outcome.status === "rejected") {
      const err: unknown = outcome.reason;
      const message = err instanceof Error ? err.message : String(err);
      conflicts.push(`${kind} ${JSON.stringify(key)}: could not be rendered (${message}), skipped`);
      continue;
    }
    const note = outcome.value;
    const expected = desiredPath(kind, projectId, key);
    if (expected === undefined || note.path !== expected) {
      conflicts.push(
        `${kind} ${JSON.stringify(key)}: unsafe or mismatched path ${JSON.stringify(note.path)}, skipped`,
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
 *  keeping the rest of its project. An unsafe mount root drops the index.
 *
 *  `mountRoot` is the vault folder the mirror owns -- the same string
 *  applyMirror is given as `root`. Note paths are relative to it; it appears
 *  here only to name the index note, after the root's own folder.
 *
 *  Projects render concurrently -- each note costs an awaited digest -- but
 *  each keeps its own notes and conflicts, which are concatenated in
 *  project-id order below. Concurrency therefore buys the wall time without
 *  costing the documented order, which the index note's own etag depends on:
 *  it hashes `safeProjects` as an array. Within a project, docs are awaited
 *  before tasks for the same reason -- they share one conflicts array, and
 *  the two are reported docs-first. */
export async function desiredNotes(
  projects: Project[],
  byProject: Map<string, ProjectMembers>,
  mountRoot: string,
  syncedAt: string,
): Promise<DesiredSet> {
  const rendered = await Promise.all(
    sortByKey(projects, (p) => p.id).map(async (p) => {
      const conflicts: string[] = [];
      if (desiredPath("project", p.id) === undefined) {
        conflicts.push(`project ${JSON.stringify(p.id)}: unsafe id, skipped along with its docs and tasks`);
        return { project: undefined, members: undefined, notes: [] as Note[], conflicts };
      }

      const members = byProject.get(p.id) ?? { docs: [], tasks: [] };
      const docs = await filterSafe(
        "doc",
        p.id,
        sortByKey(members.docs, (d) => d.slug),
        (d) => d.slug,
        docToNote,
        conflicts,
      );
      const tasks = await filterSafe(
        "task",
        p.id,
        sortByKey(members.tasks, (t) => t.id),
        (t) => t.id,
        taskToNote,
        conflicts,
      );

      const projectNote = await projectToNote(p, docs.items, tasks.items);
      if (projectNote.conflict) conflicts.push(projectNote.conflict);

      return {
        project: p,
        members: { docs: docs.items, tasks: tasks.items },
        notes: [projectNote, ...docs.notes, ...tasks.notes],
        conflicts,
      };
    }),
  );

  const conflicts: string[] = [];
  const notes: Note[] = [];
  const safeProjects: Project[] = [];
  const filteredByProject = new Map<string, ProjectMembers>();

  for (const result of rendered) {
    conflicts.push(...result.conflicts);
    if (result.project === undefined || result.members === undefined) continue;
    filteredByProject.set(result.project.id, result.members);
    safeProjects.push(result.project);
    notes.push(...result.notes);
  }

  if (isSafeMountRoot(mountRoot)) {
    notes.unshift(await indexToNote(safeProjects, filteredByProject, mountRootName(mountRoot), syncedAt));
  } else {
    conflicts.push(`index root name ${JSON.stringify(mountRoot)}: unsafe, index note skipped`);
  }

  return { notes, conflicts };
}

/** The task notes alone, for an incremental sync: the same notes desiredNotes
 *  would render for these tasks, without the project notes, the doc notes and
 *  the index note.
 *
 *  Those three are omitted rather than merely unchanged. Each rolls up a
 *  project's whole doc and task set, and an incremental sync holds only the
 *  tasks that changed since its watermark -- rendering them from that delta
 *  would give the wrong counts and rewrite every one of them on every tick.
 *  Left alone they stay as the last full sync wrote them: stale roll-ups that
 *  the next full sync corrects, which is cheaper than caching the full task
 *  set in the vault to merge deltas into.
 *
 *  A project whose own id is unsafe still drops its tasks and reports it,
 *  exactly as in desiredNotes -- there is no safe directory to put them
 *  under. */
export async function desiredTaskNotes(
  projects: Project[],
  byProject: Map<string, ProjectMembers>,
): Promise<DesiredSet> {
  const rendered = await Promise.all(
    sortByKey(projects, (p) => p.id).map(async (p) => {
      const conflicts: string[] = [];
      if (desiredPath("project", p.id) === undefined) {
        conflicts.push(`project ${JSON.stringify(p.id)}: unsafe id, skipped along with its tasks`);
        return { notes: [] as Note[], conflicts };
      }
      const members = byProject.get(p.id) ?? { docs: [], tasks: [] };
      const tasks = await filterSafe(
        "task",
        p.id,
        sortByKey(members.tasks, (t) => t.id),
        (t) => t.id,
        taskToNote,
        conflicts,
      );
      return { notes: tasks.notes, conflicts };
    }),
  );

  const conflicts: string[] = [];
  const notes: Note[] = [];
  for (const result of rendered) {
    conflicts.push(...result.conflicts);
    notes.push(...result.notes);
  }
  return { notes, conflicts };
}

/** Write what changed, delete what no longer belongs, leave the rest
 *  alone. The mount root is machine-owned: a note that exists but whose
 *  stored wl.etag no longer matches -- because the backbone data changed,
 *  or because a user edited (or otherwise replaced) the file -- is
 *  rewritten unconditionally, never merged.
 *
 *  The delete pass reads "not in the desired set" as "gone from the
 *  backbone", which only holds for a kind the sync actually enumerated -- see
 *  MirrorOptions. A degraded sync passes `pruneDocNotes: false`; an
 *  incremental one, which enumerated only the tasks that changed, prunes
 *  nothing at all. */
export async function applyMirror(
  writer: VaultWriter,
  root: string,
  desired: DesiredSet,
  options: MirrorOptions = {},
): Promise<MirrorStats> {
  const mayPrune = (path: string): boolean => {
    // Checked first and answered false unconditionally: a conflict note is
    // never desired and never pruned.
    if (isConflictNotePath(path)) return false;
    if (isDocNotePath(path)) return options.pruneDocNotes ?? true;
    if (isTaskNotePath(path)) return options.pruneTaskNotes ?? true;
    return options.pruneOtherNotes ?? true;
  };
  const stats: MirrorStats = { written: 0, skipped: 0, removed: 0, conflicts: [...desired.conflicts] };

  const existing = new Set(await writer.list(root));
  const desiredPaths = new Set(desired.notes.map((n) => n.path));

  for (const note of desired.notes) {
    if (existing.has(note.path)) {
      const current = await readWl(writer, root, note.path);
      // Current data *and* current rendering. The etag covers the backbone
      // source, not the layout it was rendered into, so a note an older
      // serializer wrote is stale even when its etag still matches — without
      // the version check it would never be re-rendered.
      if (current?.etag === note.etag && current.serializer === SERIALIZER_VERSION) {
        stats.skipped++;
        continue;
      }
    }
    await writer.write(root, note.path, note.content);
    stats.written++;
  }

  for (const path of existing) {
    if (!path.endsWith(".md")) continue;
    if (desiredPaths.has(path)) continue;
    if (!mayPrune(path)) continue;
    await writer.remove(root, path);
    stats.removed++;
  }

  return stats;
}

/** Every .md under `root` that the mirror did not write -- no readable `wl`
 *  block, so nothing identifies it as ours. Empty for a root the mirror owns
 *  and for one that does not exist yet.
 *
 *  applyMirror deletes every .md under the root the backbone does not imply,
 *  including files it never created, so this is the question a first-sync
 *  guard has to answer before a root is taken over: is anything under here
 *  the user's? Unreadable and malformed files count as foreign -- the
 *  conservative direction is to ask rather than to delete. */
export async function foreignNotes(writer: VaultWriter, root: string): Promise<string[]> {
  const foreign: string[] = [];
  for (const path of await writer.list(root)) {
    if (!path.endsWith(".md")) continue;
    if (!(await isMirrorNote(writer, root, path))) foreign.push(path);
  }
  return foreign.sort();
}

async function isMirrorNote(writer: VaultWriter, root: string, path: string): Promise<boolean> {
  try {
    parseNote(await writer.read(root, path));
    return true;
  } catch {
    return false;
  }
}

/** The stored note's wl block, or undefined if the file isn't a mirror note
 *  at all (malformed content, missing wl block) -- tolerated as "rewrite
 *  it" rather than a fatal error. */
async function readWl(writer: VaultWriter, root: string, path: string): Promise<WlBlock | undefined> {
  const content = await writer.read(root, path);
  try {
    return parseNote(content).wl;
  } catch {
    return undefined;
  }
}
