// Renders backbone entities as Obsidian notes, and parses them back. Pure:
// no Obsidian, no I/O. This file builds the task half plus the machinery
// shared by every note kind (doc/project/index land in a later task).

import { createHash } from "node:crypto";
import { parse as parseYaml, stringify as stringifyYaml } from "yaml";
import type { TaskListDetail } from "../api/types";

/** A rendered note: where it goes, what it contains, and what it was made from. */
export interface Note {
  path: string; // relative to the mount root
  content: string;
  etag: string; // first 16 hex chars of sha256(canonical JSON of the source)
}

/** The reserved frontmatter block. Everything the backbone owns lives here. */
export interface WlBlock {
  type: "task" | "doc" | "project" | "index";
  serializer: 1;
  etag: string;
  /** True when the plugin added the note's `aliases` key. See the
   *  round-trip rule: without this bit, a write-back cannot tell a
   *  plugin-added alias from an author's own. */
  aliases_added: boolean;
  [key: string]: unknown;
}

// ---- Shared machinery (reused by doc/project/index in a later task) ----

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

/** First 16 hex chars of sha256 of the canonical (key-sorted) JSON of `payload`. */
export function computeEtag(payload: unknown): string {
  const canonicalJson = JSON.stringify(canonicalize(payload));
  return createHash("sha256").update(canonicalJson).digest("hex").slice(0, 16);
}

/** Locale-independent ascending sort on plain `<`. */
function sortIds(ids: string[]): string[] {
  return [...ids].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
}

function wikilink(id: string): string {
  return `[[${id}]]`;
}

/** Serializes a frontmatter document (an object whose own key order is the
 *  emitted order) with the `---` fences and a trailing blank line before
 *  the body, if any. */
function renderNote(frontmatter: Record<string, unknown>, title: string, body: string): string {
  const yamlBlock = stringifyYaml(frontmatter);
  const header = `---\n${yamlBlock}---\n# ${title}\n`;
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

  // rest is "# <title>\n" optionally followed by "\n<body>".
  const newlineIdx = rest.indexOf("\n");
  const afterHeading = newlineIdx === -1 ? "" : rest.slice(newlineIdx + 1);
  const body = afterHeading.startsWith("\n") ? afterHeading.slice(1) : afterHeading;

  return { wl, frontmatter, body };
}

// ---- Task notes ----

export function taskToNote(t: TaskListDetail): Note {
  const parentEdges = t.edges.out.filter((e) => e.type === "child_of");
  const parent = parentEdges.length > 0 ? parentEdges[0].to : undefined;
  const children = sortIds(t.edges.in.filter((e) => e.type === "child_of").map((e) => e.from));
  const blocks = sortIds(t.edges.out.filter((e) => e.type === "blocks").map((e) => e.to));
  const blockedBy = sortIds(t.edges.in.filter((e) => e.type === "blocks").map((e) => e.from));

  const etag = computeEtag(t);

  const wl: WlBlock = {
    type: "task",
    serializer: 1,
    aliases_added: true,
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

  const content = renderNote({ aliases: [t.title], wl }, t.title, t.body);

  return {
    path: `${t.project}/tasks/${t.id}.md`,
    content,
    etag,
  };
}
