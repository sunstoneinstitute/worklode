// The write-back pass: the one direction of travel that is not backbone to
// vault. A task note's body is the whole writable surface -- everything else
// it shows lives in the backbone-owned `wl` block -- and the backbone always
// wins a genuine conflict. Pure logic against the injected VaultWriter and a
// push function, so vitest drives the whole pass without an Obsidian runtime.
//
// Full syncs only. An incremental sync holds just the tasks that changed, so
// it cannot classify a note it did not fetch.

import { computeEtag, conflictToNote, parseNote, type ProjectMembers } from "../serialize/note";
import { desiredPath, type VaultWriter } from "./mirror";
import type { Task, TaskListDetail } from "../api/types";

/** Sends one task's edited body to the backbone, answering with the task as
 *  the server now holds it. PATCH /api/v1/tasks/{id} returns the plain task
 *  shape, without the detail-only `edges` and `blocked`. */
export type PushBody = (id: string, body: string) => Promise<Task>;

export interface WriteBackStats {
  /** Uncontested local edits pushed to the backbone. */
  pushed: number;
  /** Local edits the backbone had moved past, saved as conflict notes. */
  conflicted: number;
  /** One line per conflict, unreadable note, and failed push. */
  conflicts: string[];
}

/** What a task note on disk says about the body beside it. */
export type NoteEdit =
  /** The note's body is the backbone's; nothing to push. */
  | { kind: "clean" }
  /** Edited locally, and the backbone has not moved since the note was
   *  rendered -- the edit is uncontested. */
  | { kind: "edited"; body: string }
  /** Edited locally *and* changed in the backbone. */
  | { kind: "conflict"; body: string }
  /** Not a task note this plugin can read. Never treated as "no local edit":
   *  an unreadable note may be nothing but the user's own text. */
  | { kind: "unreadable"; reason: string };

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** Compares a stored task note with the freshly fetched task. The etag is what
 *  separates an uncontested edit from a conflict: it is the digest of the task
 *  the note was rendered from, so a match means the backbone has not moved
 *  since. */
export async function classifyTaskNote(task: TaskListDetail, content: string): Promise<NoteEdit> {
  let parsed;
  try {
    parsed = parseNote(content);
  } catch (err) {
    return { kind: "unreadable", reason: errorMessage(err) };
  }
  if (parsed.wl.type !== "task") {
    return { kind: "unreadable", reason: `wl.type is ${JSON.stringify(parsed.wl.type)}, not "task"` };
  }
  if (parsed.body === task.body) return { kind: "clean" };
  return { kind: parsed.wl.etag === (await computeEtag(task)) ? "edited" : "conflict", body: parsed.body };
}

/** Pushes every uncontested body edit found under `root`, and preserves every
 *  contested one as a conflict note. Returns the tasks the caller should
 *  render from: a pushed task is replaced by the PATCH response, so the note
 *  written straight after carries the new body *and* the etag the backbone
 *  will answer with next time -- otherwise the note would be rewritten on
 *  every sync from then on.
 *
 *  A task that could not be pushed is handed back unchanged, which leaves its
 *  note exactly as the user edited it: the note's etag still matches, so the
 *  delete-and-write pass skips the file and the next full sync tries again.
 *
 *  `at` is the sync's own instant, RFC3339, stamped into any conflict note. */
export async function writeBackTaskNotes(
  writer: VaultWriter,
  root: string,
  byProject: Map<string, ProjectMembers>,
  push: PushBody,
  at: string,
): Promise<{ byProject: Map<string, ProjectMembers>; stats: WriteBackStats }> {
  const stats: WriteBackStats = { pushed: 0, conflicted: 0, conflicts: [] };
  const existing = new Set(await writer.list(root));
  const updated = new Map<string, ProjectMembers>();

  // Serial on purpose: the report order follows the fetch order, and a vault
  // full of edits should not open one request per task at once.
  for (const [projectId, members] of byProject) {
    const tasks: TaskListDetail[] = [];
    for (const task of members.tasks) {
      tasks.push(await writeBackTask(writer, root, existing, projectId, task, push, at, stats));
    }
    updated.set(projectId, { docs: members.docs, tasks });
  }

  return { byProject: updated, stats };
}

async function writeBackTask(
  writer: VaultWriter,
  root: string,
  existing: Set<string>,
  projectId: string,
  task: TaskListDetail,
  push: PushBody,
  at: string,
  stats: WriteBackStats,
): Promise<TaskListDetail> {
  // The same guard filterSafe applies, for the same reason: the note's path is
  // built from the trusted grouping key, and a task whose own `project` field
  // disagrees with it owns no note here -- and must not place a conflict note
  // by that field either, since conflictToNote builds its path from it.
  const path = desiredPath("task", projectId, task.id);
  if (path === undefined || task.project !== projectId) return task;
  if (!existing.has(path)) return task;

  const edit = await classifyTaskNote(task, await writer.read(root, path));
  switch (edit.kind) {
    case "clean":
      return task;
    case "unreadable":
      stats.conflicts.push(
        `task ${JSON.stringify(task.id)}: note at ${path} could not be read (${edit.reason}); nothing pushed`,
      );
      return task;
    case "conflict": {
      const note = await conflictToNote(task, edit.body, at);
      await writer.write(root, note.path, note.content);
      stats.conflicted++;
      stats.conflicts.push(
        `task ${JSON.stringify(task.id)}: edited here and changed in worklode; the backbone's ` +
          `version was kept and your text saved to ${note.path}`,
      );
      return task;
    }
    case "edited":
      try {
        // The detail-only fields PATCH does not answer with are unaffected by
        // a body change, so the fetched task supplies them.
        const patched = await push(task.id, edit.body);
        stats.pushed++;
        return { ...task, ...patched };
      } catch (err) {
        stats.conflicts.push(
          `task ${JSON.stringify(task.id)}: local edit could not be pushed (${errorMessage(err)}); ` +
            `the note was left as you edited it, and the next full sync retries`,
        );
        return task;
      }
  }
}
