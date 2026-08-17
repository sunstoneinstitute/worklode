// Renders backbone entities as Obsidian notes, and parses them back. Pure:
// no Obsidian, no I/O. Covers all four note kinds — task, doc, project,
// index — plus the machinery shared across them.

import { parse as parseYaml, stringify as stringifyYaml } from "yaml";
import type { Doc, Project, TaskListDetail } from "../api/types";

/** A rendered note: where it goes, what it contains, and what it was made from. */
export interface Note {
  path: string; // relative to the mount root
  content: string;
  etag: string; // first 16 hex chars of sha256(canonical JSON of the source)
  /** Set when the doc's own frontmatter collided with the reserved `wl` key.
   *  The backbone block is always kept; this is how the collision surfaces
   *  to the sync report instead of being silently resolved either way. */
  conflict?: string;
}

/** The version of the rendering contract every note is stamped with. Bumped
 *  when the rendered layout changes in a way `etag` cannot see — the etag
 *  covers the backbone source, not how it was laid out — so applyMirror can
 *  tell a note an older plugin wrote from one that is already current.
 *
 *  2: a body that already opens with its own H1 no longer gets one injected. */
export const SERIALIZER_VERSION = 2;

/** The reserved frontmatter block. Everything the backbone owns lives here. */
export interface WlBlock {
  type: "task" | "doc" | "project" | "index";
  /** SERIALIZER_VERSION at the time the note was written. Not narrowed to
   *  the current value: parseNote reads notes older plugins wrote. */
  serializer: number;
  etag: string;
  /** True when the plugin added the note's `aliases` key. See the
   *  round-trip rule: without this bit, a write-back cannot tell a
   *  plugin-added alias from an author's own. */
  aliases_added: boolean;
  /** True when the plugin added the note's `# <title>` heading, which it
   *  does unless `bodyOpensWithH1` says the body already brought one. Same
   *  round-trip rule as `aliases_added`: without this bit parseNote cannot
   *  tell the injected heading from the source document's own, and stripping
   *  the wrong one eats a line of body. Absent on notes written before
   *  serializer 2, which always injected — parseNote reads a missing value
   *  as true. */
  heading_added: boolean;
  [key: string]: unknown;
}

// ---- Shared machinery (reused by every note kind below) ----

/** Recursively sorts object keys so JSON.stringify output is deterministic. */
function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(canonicalize);
  }
  if (value !== null && typeof value === "object") {
    const source = value as Record<string, unknown>;
    const sorted: Record<string, unknown> = {};
    for (const key of Object.keys(source).sort()) {
      sorted[key] = canonicalize(source[key]);
    }
    return sorted;
  }
  return value;
}

/** First 16 hex chars of sha256 of the canonical (key-sorted) JSON of `payload`.
 *
 *  Async because it hashes through Web Crypto's `crypto.subtle`, which is
 *  promise-returning and has no sync twin. That is the whole reason every
 *  *ToNote function below is async: `node:crypto`'s sync `createHash` exists
 *  only on desktop, and the mirror has to run on Obsidian mobile too. The
 *  digest itself is unchanged — same UTF-8 bytes of the same canonical JSON,
 *  same sha256, same 16-char prefix — so no mirrored vault is invalidated by
 *  the move. `test/note.test.ts` pins known digests against that promise. */
export async function computeEtag(payload: unknown): Promise<string> {
  const canonicalJson = JSON.stringify(canonicalize(payload));
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(canonicalJson));
  // 8 bytes is the 16 hex chars the etag keeps; hashing produces 32.
  return [...new Uint8Array(digest, 0, 8)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

/** Locale-independent ascending sort on plain `<`. */
function sortIds(ids: string[]): string[] {
  return [...ids].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
}

function wikilink(id: string): string {
  return `[[${id}]]`;
}

/** Whether `body` already opens with an H1 of its own: its first non-blank
 *  line, indented by at most three spaces, is `#` followed by a space, a tab,
 *  or the end of the line.
 *
 *  That is CommonMark's ATX rule and nothing more. The Setext form
 *  (`Title\n=====`) is deliberately not recognised: telling one from a
 *  paragraph followed by a row of `=` needs real block parsing, and no
 *  document in the corpus this mirrors uses it — every spec, ADR and plan
 *  under docs/ opens with `# <Title>` on the first body line. Missing a
 *  Setext heading costs a second H1 in the rendered note, which is cosmetic;
 *  claiming a heading that is not one would strip a line of body on the way
 *  back, which is not. */
function bodyOpensWithH1(body: string): boolean {
  for (const line of body.split("\n")) {
    if (line.trim() === "") continue;
    return /^ {0,3}#(?:[ \t]|$)/.test(line);
  }
  return false;
}

/** Serializes a frontmatter document (an object whose own key order is the
 *  emitted order) with the `---` fences, and — when `injectHeading` — an
 *  `# <title>` line and a blank line before the body, if any. The caller
 *  passes the same bit it recorded as `wl.heading_added`, so what parseNote
 *  is told to strip is exactly what was added here. */
function renderNote(
  frontmatter: Record<string, unknown>,
  title: string,
  body: string,
  injectHeading: boolean,
): string {
  const fence = `---\n${stringifyYaml(frontmatter)}---\n`;
  if (!injectHeading) return `${fence}${body}`;
  const header = `${fence}# ${title}\n`;
  return body ? `${header}\n${body}` : header;
}

/** Split a rendered note back into its wl block, the surrounding
 *  frontmatter, and the verbatim body. The inverse of the *ToNote
 *  functions; the write-back half is not implemented, but this is what
 *  makes round-trippability testable. */
export function parseNote(content: string): {
  wl: WlBlock;
  frontmatter: Record<string, unknown>;
  body: string;
} {
  if (!content.startsWith("---\n")) {
    throw new Error("note is missing its opening frontmatter fence");
  }
  // Safe to stop at the first match: yaml.stringify indents every multi-line
  // scalar, so generated frontmatter never emits a bare column-0 "---".
  const closeIdx = content.indexOf("\n---\n", 4);
  if (closeIdx === -1) {
    throw new Error("note frontmatter is not closed");
  }
  const yamlBlock = content.slice(4, closeIdx + 1);
  const rest = content.slice(closeIdx + 5);

  const parsed = parseYaml(yamlBlock) as Record<string, unknown>;
  const wl = parsed.wl as WlBlock | undefined;
  if (!wl) {
    throw new Error("note is missing the wl block");
  }

  const frontmatter = { ...parsed };
  delete frontmatter.wl;
  if (wl.aliases_added) {
    delete frontmatter.aliases;
  }

  // With an injected heading, rest is "# <title>\n" optionally followed by
  // "\n<body>"; without one it is the body verbatim. A note written before
  // serializer 2 carries no heading_added and always had one injected, so a
  // missing value reads as true.
  return { wl, frontmatter, body: wl.heading_added === false ? rest : stripHeading(rest) };
}

function stripHeading(rest: string): string {
  const newlineIdx = rest.indexOf("\n");
  const afterHeading = newlineIdx === -1 ? "" : rest.slice(newlineIdx + 1);
  return afterHeading.startsWith("\n") ? afterHeading.slice(1) : afterHeading;
}

// ---- Task notes ----

export async function taskToNote(t: TaskListDetail): Promise<Note> {
  const parentEdges = t.edges.out.filter((e) => e.type === "child_of");
  const parent = parentEdges.length > 0 ? parentEdges[0].to : undefined;
  const children = sortIds(t.edges.in.filter((e) => e.type === "child_of").map((e) => e.from));
  const blocks = sortIds(t.edges.out.filter((e) => e.type === "blocks").map((e) => e.to));
  const blockedBy = sortIds(t.edges.in.filter((e) => e.type === "blocks").map((e) => e.from));

  const etag = await computeEtag(t);
  const headingAdded = !bodyOpensWithH1(t.body);

  const wl: WlBlock = {
    type: "task",
    serializer: SERIALIZER_VERSION,
    aliases_added: true,
    heading_added: headingAdded,
    id: t.id,
    project: t.project,
    title: t.title,
    state: t.state,
    kind: t.kind,
    priority: t.priority,
    concern: t.concern,
    assignee: t.assignee,
    branch: t.branch,
    blocked: t.blocked,
    needs_decomposition: t.needs_decomposition,
    skills: t.skills,
    created_by: t.created_by,
    created_at: t.created_at,
    updated_at: t.updated_at,
    ...(parent !== undefined ? { parent: wikilink(parent) } : {}),
    children: children.map(wikilink),
    blocks: blocks.map(wikilink),
    blocked_by: blockedBy.map(wikilink),
    etag,
  };

  const content = renderNote({ aliases: [t.title], wl }, t.title, t.body, headingAdded);

  return {
    path: `${t.project}/tasks/${t.id}.md`,
    content,
    etag,
  };
}

// ---- Doc notes ----

/** The doc as the etag is computed over it: everything but `synced_at`. The
 *  backbone rewrites `docs.synced_at` on every `lode doc sync`, including for
 *  a doc whose content did not change, so an etag covering it would rewrite
 *  every doc note (and, since a project note's etag covers its member docs,
 *  every project note) on every sync. `synced_at` is backbone bookkeeping the
 *  mirror does not carry, so it is out of the rendered `wl` block too. */
function docEtagSource(d: Doc): Record<string, unknown> {
  const source: Record<string, unknown> = { ...d };
  delete source.synced_at;
  return source;
}

/** Renders a stored document: its own frontmatter verbatim, plus the
 *  reserved `wl` block beside it. The document's own frontmatter is never
 *  edited, only accompanied — see the round-trip rules in the module docs. */
export async function docToNote(d: Doc): Promise<Note> {
  // frontmatter is unknown (it's json.RawMessage on the Go side): a non-null,
  // non-object payload (a string, a number, an array) would otherwise spread
  // into garbage index keys. Treat it as absent and surface a conflict
  // instead of rendering corrupted frontmatter silently.
  const raw = d.frontmatter;
  const isFrontmatterObject = typeof raw === "object" && raw !== null && !Array.isArray(raw);
  const malformedFrontmatter = raw !== undefined && raw !== null && !isFrontmatterObject;
  const source = isFrontmatterObject ? (raw as Record<string, unknown>) : {};
  const hasAliases = Object.prototype.hasOwnProperty.call(source, "aliases");
  const hasWlCollision = Object.prototype.hasOwnProperty.call(source, "wl");

  const etag = await computeEtag(docEtagSource(d));
  const body = d.body ?? "";
  // The one case this bit exists for: a spec, ADR or plan body opens with its
  // own "# <Title>", so injecting one would render the note with two.
  const headingAdded = !bodyOpensWithH1(body);

  const wl: WlBlock = {
    type: "doc",
    serializer: SERIALIZER_VERSION,
    aliases_added: !hasAliases,
    heading_added: headingAdded,
    id: d.id,
    project: d.project,
    kind: d.kind,
    ordinal: d.ordinal,
    status: d.status,
    title: d.title,
    version: d.version,
    source_branch: d.source_branch,
    source_dirty: d.source_dirty,
    etag,
  };

  // Own frontmatter first (preserving its key order), then the alias we may
  // add, then the reserved block last. A doc's own `wl` key, if any, is a
  // collision: the backbone block always wins and the collision is reported
  // via Note.conflict rather than silently dropped.
  const frontmatter: Record<string, unknown> = { ...source };
  if (!hasAliases) {
    frontmatter.aliases = [d.title];
  }
  frontmatter.wl = wl;

  const content = renderNote(frontmatter, d.title, body, headingAdded);

  const note: Note = {
    path: `${d.project}/docs/${d.id}.md`,
    content,
    etag,
  };

  if (malformedFrontmatter) {
    note.conflict =
      `${d.id}: frontmatter is not a JSON object (got ${Array.isArray(raw) ? "an array" : typeof raw}); ` +
      `ignored, and the note was rendered with no author frontmatter.`;
  } else if (hasWlCollision) {
    note.conflict =
      `${d.id}: frontmatter already has a "wl" key, which collides with the ` +
      `backbone-reserved block. The backbone wl block was kept; the doc's own ` +
      `wl key was dropped from the rendered note. dropped value: ${JSON.stringify(source.wl)}`;
  }

  return note;
}

// ---- Project and index notes ----
// Generated bodies, not backbone-owned: the round-trip rule does not apply.

const GENERATED_NOTICE = "> Generated by the Worklode plugin. Edits here are overwritten on sync.";

function renderProjectBody(docs: Doc[], tasks: TaskListDetail[]): string {
  const docLines = sortIds(docs.map((d) => d.id)).map((id) => `- ${wikilink(id)}`);

  const byState = new Map<string, string[]>();
  for (const t of tasks) {
    const ids = byState.get(t.state) ?? [];
    ids.push(t.id);
    byState.set(t.state, ids);
  }

  const taskLines: string[] = [];
  for (const state of [...byState.keys()].sort()) {
    taskLines.push(`### ${state}`, "");
    for (const id of sortIds(byState.get(state) ?? [])) {
      taskLines.push(`- ${wikilink(id)}`);
    }
    taskLines.push("");
  }

  const lines = [
    GENERATED_NOTICE,
    "",
    "## Docs",
    "",
    ...docLines,
    "",
    "## Tasks",
    "",
    ...taskLines,
  ];
  // An empty docs/tasks list otherwise leaves two blank lines in a row
  // (the section's own separator plus the empty content it precedes).
  const collapsed = lines.filter((line, i) => line !== "" || lines[i - 1] !== "");
  return `${collapsed.join("\n").trimEnd()}\n`;
}

export async function projectToNote(p: Project, docs: Doc[], tasks: TaskListDetail[]): Promise<Note> {
  // Same `synced_at` exclusion as docToNote: otherwise every doc sync would
  // rewrite every project note as well.
  const etag = await computeEtag({ project: p, docs: docs.map(docEtagSource), tasks });

  // A generated body opens with the generated-by notice, never a heading, so
  // this is always true. Asked anyway: one rule, put to every note kind.
  const body = renderProjectBody(docs, tasks);
  const headingAdded = !bodyOpensWithH1(body);

  const wl: WlBlock = {
    type: "project",
    serializer: SERIALIZER_VERSION,
    aliases_added: true,
    heading_added: headingAdded,
    id: p.id,
    name: p.name,
    key: p.key,
    repos: p.repos,
    doc_count: docs.length,
    task_count: tasks.length,
    etag,
  };

  const content = renderNote({ aliases: [p.name], wl }, p.name, body, headingAdded);

  return {
    path: `${p.id}/${p.id}.md`,
    content,
    etag,
  };
}

/** The docs and tasks belonging to one project, keyed by project id — the
 *  same shape Task 8's desiredNotes builds and passes straight through, so
 *  its call site costs one argument. A project id absent from the map (the
 *  edge case a map lookup invites) has zero of both — rendered as `0`, the
 *  project itself is never omitted. */
export type ProjectMembers = { docs: Doc[]; tasks: TaskListDetail[] };

function projectCounts(id: string, byProject: Map<string, ProjectMembers>): { docs: number; tasks: number } {
  const members = byProject.get(id);
  return { docs: members?.docs.length ?? 0, tasks: members?.tasks.length ?? 0 };
}

function renderIndexBody(projects: Project[], byProject: Map<string, ProjectMembers>): string {
  const byId = new Map(projects.map((p) => [p.id, p]));
  const lines = [
    GENERATED_NOTICE,
    "",
    "## Projects",
    "",
    ...sortIds(projects.map((p) => p.id)).map((id) => {
      const p = byId.get(id)!;
      const counts = projectCounts(id, byProject);
      return `- ${wikilink(p.id)} ${p.name} — ${counts.docs} docs, ${counts.tasks} tasks`;
    }),
  ];
  return `${lines.join("\n")}\n`;
}

export async function indexToNote(
  projects: Project[],
  byProject: Map<string, ProjectMembers>,
  rootName: string,
  syncedAt: string,
): Promise<Note> {
  const counts = sortIds(projects.map((p) => p.id)).map((id) => ({ id, ...projectCounts(id, byProject) }));
  const etag = await computeEtag({ projects, counts, syncedAt });

  const body = renderIndexBody(projects, byProject);
  const headingAdded = !bodyOpensWithH1(body);

  const wl: WlBlock = {
    type: "index",
    serializer: SERIALIZER_VERSION,
    aliases_added: true,
    heading_added: headingAdded,
    projects: sortIds(projects.map((p) => p.id)).map(wikilink),
    synced_at: syncedAt,
    etag,
  };

  const content = renderNote({ aliases: [rootName], wl }, rootName, body, headingAdded);

  return {
    path: `${rootName}.md`,
    content,
    etag,
  };
}
