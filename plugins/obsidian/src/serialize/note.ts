// Renders backbone entities as Obsidian notes, and parses them back. Pure:
// no Obsidian, no I/O. Covers all four note kinds — task, doc, project,
// index — plus the machinery shared across them.

import { parse as parseYaml, stringify as stringifyYaml } from "yaml";
import type { Doc, DocDetail, Project, Task, TaskListDetail } from "../api/types";

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
  type: "task" | "doc" | "project" | "index" | "conflict";
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

/** A doc wikilink, path-qualified by project and displayed as the slug
 *  alone. A task id is globally unique, so a bare `wikilink(id)` resolves
 *  unambiguously; a doc slug is unique only within its project
 *  (`docs_project_slug`, deploy/base/migrations/0027_docs.up.sql), so two
 *  projects can each hold a doc slugged e.g. "overview" and a bare
 *  `[[overview]]` would resolve to whichever one Obsidian picks. Pointing at
 *  the full note path -- the same one docToNote writes -- with the slug as
 *  the display alias keeps the link both unambiguous and readable. */
function docWikilink(projectId: string, slug: string): string {
  return `[[${projectId}/docs/${slug}|${slug}]]`;
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

/** Locates a leading YAML frontmatter fence: the opening "---\n" and the
 *  first closing "\n---\n" after it. Returns the raw YAML block (including
 *  its own trailing newline, as parseYaml expects) and everything after the
 *  closing fence; undefined when there is no opening fence, or it never
 *  closes. Shared by parseNote (a rendered note always has a fence) and
 *  splitDocFrontmatter (a document body may or may not).
 *
 *  Safe to stop at the first closing match: yaml.stringify indents every
 *  multi-line scalar, so generated frontmatter never emits a bare column-0
 *  "---". */
function scanFrontmatterFence(content: string): { yamlBlock: string; rest: string } | undefined {
  if (!content.startsWith("---\n")) return undefined;
  const closeIdx = content.indexOf("\n---\n", 4);
  if (closeIdx === -1) return undefined;
  return { yamlBlock: content.slice(4, closeIdx + 1), rest: content.slice(closeIdx + 5) };
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
  const fence = scanFrontmatterFence(content);
  if (!fence) {
    throw new Error("note frontmatter is not closed");
  }
  const { yamlBlock, rest } = fence;

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

// ---- Conflict notes ----

/** The folder conflict notes live in, directly under the mount root. Its own
 *  segment, rather than a suffix beside the task note, so a conflict is
 *  obvious in the file tree and cannot be mistaken for a mirrored note: every
 *  mirrored path starts with a project id, and the delete pass exempts this
 *  prefix outright (see mirror.ts's isConflictNotePath). */
export const CONFLICT_FOLDER = "_conflicts";

/** The vault-relative path of a conflict note. `at` is stamped into the name
 *  so a second conflict on the same task never overwrites the first; ":" is
 *  not a legal filename character on every platform Obsidian runs on, so the
 *  instant is spelled with "-" and without its milliseconds. */
function conflictPath(t: Task, at: string): string {
  return `${CONFLICT_FOLDER}/${t.project}/${t.id} ${at.replace(/\.\d+Z$/, "Z").replace(/:/g, "-")}.md`;
}

/** Preserves a task body the backbone overwrote. Written when a task note was
 *  edited locally *and* the backbone moved on since it was rendered: the task
 *  note is re-rendered from the backbone as usual, and the local body is kept
 *  here verbatim so nothing is destroyed.
 *
 *  Never part of a desired set -- the write-back pass writes it directly and
 *  the delete pass exempts it -- so its `etag` identifies the capture rather
 *  than driving a re-render. It carries a `wl` block all the same, so
 *  foreignNotes counts it as the mirror's own rather than a note the user
 *  would be asked about. */
export async function conflictToNote(t: Task, localBody: string, at: string): Promise<Note> {
  const path = conflictPath(t, at);
  const etag = await computeEtag({ id: t.id, at, body: localBody });
  const title = `Conflict: ${t.title}`;

  const wl: WlBlock = {
    type: "conflict",
    serializer: SERIALIZER_VERSION,
    aliases_added: true,
    heading_added: true,
    task: wikilink(t.id),
    project: t.project,
    task_note: `${t.project}/tasks/${t.id}.md`,
    detected_at: at,
    etag,
  };

  const body =
    `> Worklode changed ${wikilink(t.id)} while this vault held an edit to its body.\n` +
    `> The backbone's version was written to \`${wl.task_note as string}\`; your text is\n` +
    `> below, verbatim. Nothing here syncs anywhere -- keep what you need and\n` +
    `> delete this note.\n\n` +
    localBody;

  return { path, content: renderNote({ aliases: [`${t.id} conflict`], wl }, title, body, true), etag };
}

// ---- Doc notes ----

/** Splits a document body into its own YAML frontmatter block and the
 *  markdown after it. `internal/model.Doc.Body` is the full markdown,
 *  frontmatter included, so this is what lets docToNote lift that block into
 *  the note's own frontmatter instead of leaving a second `---` fence inside
 *  the rendered body.
 *
 *  A body that does not open with a `---\n` fence, or whose fence never
 *  closes, is returned unchanged: `frontmatter` undefined, `malformed`
 *  false -- there is no block to disagree with. A closed fence whose YAML
 *  parses to a plain object (not null, not an array) is that object. A
 *  closed fence whose YAML parses to null -- an empty block, `---\n\n---\n`
 *  -- is treated as absent, the same as no fence at all: `frontmatter`
 *  undefined, `malformed` false, and the fence is still stripped from the
 *  returned `body` so it cannot render as a duplicate. A closed fence whose
 *  YAML throws, or parses to an array or a scalar, is reported `malformed`
 *  and the block is stripped from `body` rather than left to render as a
 *  duplicate, unparseable fence. */
function splitDocFrontmatter(body: string): {
  frontmatter: Record<string, unknown> | undefined;
  malformed: boolean;
  body: string;
} {
  const fence = scanFrontmatterFence(body);
  if (!fence) return { frontmatter: undefined, malformed: false, body };

  let parsed: unknown;
  try {
    parsed = parseYaml(fence.yamlBlock);
  } catch {
    return { frontmatter: undefined, malformed: true, body: fence.rest };
  }
  if (parsed === null) {
    return { frontmatter: undefined, malformed: false, body: fence.rest };
  }
  if (typeof parsed !== "object" || Array.isArray(parsed)) {
    return { frontmatter: undefined, malformed: true, body: fence.rest };
  }
  return { frontmatter: parsed as Record<string, unknown>, malformed: false, body: fence.rest };
}

/** A doc note's etag payload: the list row, and pointedly not the body.
 *
 *  A document's text costs its own request, so the mirror holds it only for the
 *  documents it is about to rewrite (hydrateDocBodies); everything else it
 *  carries is the blank-bodied row `GET /api/v1/docs` answers with. Hashing the
 *  body would therefore give the same unchanged document two different etags
 *  depending on whether this sync happened to fetch it. Hashing the row alone
 *  makes the etag a property of the document rather than of the fetch — which
 *  is what lets a sync decide, before spending a request, whether the note it
 *  already holds is current.
 *
 *  The rows only a fetched DocDetail carries (sections, edges, revision) are
 *  destructured away for that same reason: a list row cannot produce them.
 *  Everything else is in by default, so a field added to internal/model.Doc
 *  joins the digest rather than silently falling out of it.
 *
 *  This detects a changed body only as far as the list row reflects one.
 *  `store.UpdateDocBody` bumps `version` for a plan but not for a draft spec
 *  or ADR; for the latter, `updated_at` carries full sub-second precision
 *  instead (WL-285), so two edits to one draft within the same wall-clock
 *  second still move the etag even when neither edit changes title or issued.
 *
 *  Upgrading a vault re-renders every doc note once, since the old payload
 *  included `body: ""`. That is the intended effect: those are the notes with
 *  no text in them. */
function docIdentity(d: Doc): Record<string, unknown> {
  // The named bindings exist to be discarded; `identity` is the payload.
  const { body, sections, edges, edges_in, revision, ...identity } = d as Doc & Partial<DocDetail>;
  return identity;
}

/** The etag `docToNote` will stamp into a document's note, computable from a
 *  list row alone. Exported so the sync can ask "is the note I already hold
 *  current?" before deciding to spend a request on the body. */
export function docEtag(d: Doc): Promise<string> {
  return computeEtag(docIdentity(d));
}

/** Renders a stored document: its own frontmatter (lifted out of the body,
 *  see splitDocFrontmatter) verbatim, plus the reserved `wl` block beside it.
 *  The document's own frontmatter is never edited, only accompanied — see
 *  the round-trip rules in the module docs. */
export async function docToNote(d: Doc): Promise<Note> {
  const split = splitDocFrontmatter(d.body);
  const source = split.frontmatter ?? {};
  const hasAliases = Object.prototype.hasOwnProperty.call(source, "aliases");
  const hasWlCollision = Object.prototype.hasOwnProperty.call(source, "wl");

  const etag = await docEtag(d);
  const body = split.body;
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
    ...(d.number !== 0 ? { number: d.number } : {}),
    slug: d.slug,
    status: d.status,
    title: d.title,
    version: d.version,
    issued: d.issued,
    assignee: d.assignee,
    // The task that wrote the document (025 §12). Omitted when none did,
    // like `number` above: most documents have no authoring task, and a key
    // whose value is always "" is noise in every note's frontmatter.
    ...(d.generated_by_task ? { generated_by_task: d.generated_by_task } : {}),
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
    path: `${d.project}/docs/${d.slug}.md`,
    content,
    etag,
  };

  if (split.malformed) {
    note.conflict =
      `${d.slug}: frontmatter block is not a YAML mapping; ignored, and the note was rendered with no ` +
      `author frontmatter.`;
  } else if (hasWlCollision) {
    note.conflict =
      `${d.slug}: frontmatter already has a "wl" key, which collides with the ` +
      `backbone-reserved block. The backbone wl block was kept; the doc's own ` +
      `wl key was dropped from the rendered note. dropped value: ${JSON.stringify(source.wl)}`;
  }

  return note;
}

// ---- Project and index notes ----
// Generated bodies, not backbone-owned: the round-trip rule does not apply.

const GENERATED_NOTICE = "> Generated by the Worklode plugin. Edits here are overwritten on sync.";

function renderProjectBody(projectId: string, docs: Doc[], tasks: TaskListDetail[]): string {
  const docLines = sortIds(docs.map((d) => d.slug)).map((slug) => `- ${docWikilink(projectId, slug)}`);

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
  // Docs enter by identity, for the reason docIdentity gives: hashing them
  // whole would tie this note's etag to which of its docs this sync happened
  // to fetch. The rendered body only ever names slugs anyway.
  const etag = await computeEtag({ project: p, docs: docs.map(docIdentity), tasks });

  // A generated body opens with the generated-by notice, never a heading, so
  // this is always true. Asked anyway: one rule, put to every note kind.
  const body = renderProjectBody(p.id, docs, tasks);
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
