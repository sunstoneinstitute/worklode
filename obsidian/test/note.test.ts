import { describe, expect, it } from "vitest";
import { stringify as stringifyYaml } from "yaml";
import {
  computeEtag,
  conflictToNote,
  docToNote,
  indexToNote,
  parseNote,
  projectToNote,
  taskToNote,
} from "../src/serialize/note";
import type { Doc, Project, TaskListDetail } from "../src/api/types";

function fixtureTask(overrides: Partial<TaskListDetail> = {}): TaskListDetail {
  return {
    id: "WL-42",
    project: "worklode",
    title: "Fix the thing",
    body: "body markdown verbatim",
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
    branch: "WL-42-fix-the-thing",
    blocked: false,
    edges: {
      out: [
        { to: "WL-7", type: "child_of" },
        { to: "WL-44", type: "blocks" },
      ],
      in: [
        { from: "WL-43", type: "child_of" },
        { from: "WL-41", type: "blocks" },
      ],
    },
    ...overrides,
  };
}

/** Renders a frontmatter object as a `---`-fenced YAML block, the shape a
 *  doc body carries its own frontmatter in (internal/model.Doc.Body: "the
 *  full markdown, frontmatter included"). */
function withFrontmatter(frontmatter: Record<string, unknown>, body: string): string {
  return `---\n${stringifyYaml(frontmatter)}---\n${body}`;
}

function fixtureDoc(overrides: Partial<Doc> = {}): Doc {
  return {
    id: 25,
    project: "worklode",
    kind: "spec",
    number: 25,
    slug: "documents-in-the-backbone",
    status: "draft",
    title: "Documents in the backbone",
    version: 3,
    issued: "2026-06-01",
    assignee: "stig",
    created_by: "stig",
    created_at: "2026-06-01T09:00:00Z",
    updated_at: "2026-08-16T09:12:00Z",
    body: withFrontmatter(
      { status: "draft", covers: "docs/specs/025-documents-in-the-backbone.md" },
      "body markdown verbatim",
    ),
    ...overrides,
  };
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

describe("taskToNote", () => {
  it("renders a task note with relations as wikilinks", async () => {
    const t = fixtureTask();
    const note = await taskToNote(t);

    expect(note.path).toBe("worklode/tasks/WL-42.md");
    expect(note.etag).toMatch(/^[0-9a-f]{16}$/);

    const expected = `---
aliases:
  - Fix the thing
wl:
  type: task
  serializer: 2
  aliases_added: true
  heading_added: true
  id: WL-42
  project: worklode
  title: Fix the thing
  state: ready
  kind: feature
  priority: medium
  concern: ""
  assignee: stig
  branch: WL-42-fix-the-thing
  blocked: false
  needs_decomposition: false
  skills: []
  created_by: stig
  created_at: 2026-08-01T10:00:00Z
  updated_at: 2026-08-14T12:00:00Z
  parent: "[[WL-7]]"
  children:
    - "[[WL-43]]"
  blocks:
    - "[[WL-44]]"
  blocked_by:
    - "[[WL-41]]"
  etag: ${note.etag}
---
# Fix the thing

body markdown verbatim`;

    expect(note.content).toBe(expected);
  });

  it("omits parent for a root task and renders empty relation lists", async () => {
    const t = fixtureTask({
      edges: { out: [], in: [] },
    });
    const note = await taskToNote(t);

    expect(note.content).not.toMatch(/^\s*parent:/m);
    expect(note.content).toMatch(/children: \[\]/);
    expect(note.content).toMatch(/blocks: \[\]/);
    expect(note.content).toMatch(/blocked_by: \[\]/);
  });

  it("writes the body verbatim", async () => {
    const body = "line one\n---\nline with a\ttab\nline with trailing space   \n```\ncode fence\n```";
    const t = fixtureTask({ body });
    const note = await taskToNote(t);

    expect(note.content.endsWith(body)).toBe(true);
    expect(parseNote(note.content).body).toBe(body);
  });

  it("round-trips: parseNote(taskToNote(t)) recovers the wl block and body", async () => {
    const t = fixtureTask({
      body: "some body text\nwith multiple lines\n",
    });
    const note = await taskToNote(t);
    const parsed = parseNote(note.content);

    expect(parsed.wl).toEqual({
      type: "task",
      serializer: 2,
      aliases_added: true,
      heading_added: true,
      id: "WL-42",
      project: "worklode",
      title: "Fix the thing",
      state: "ready",
      kind: "feature",
      priority: "medium",
      concern: "",
      assignee: "stig",
      branch: "WL-42-fix-the-thing",
      blocked: false,
      needs_decomposition: false,
      skills: [],
      created_by: "stig",
      created_at: "2026-08-01T10:00:00Z",
      updated_at: "2026-08-14T12:00:00Z",
      parent: "[[WL-7]]",
      children: ["[[WL-43]]"],
      blocks: ["[[WL-44]]"],
      blocked_by: ["[[WL-41]]"],
      etag: note.etag,
    });
    expect(parsed.body).toBe("some body text\nwith multiple lines\n");
    expect(parsed.frontmatter).toEqual({});
  });

  it("changes the etag when any backbone field changes, and not otherwise", async () => {
    const t = fixtureTask();
    const again = fixtureTask();
    expect((await taskToNote(t)).etag).toBe((await taskToNote(again)).etag);

    const changed = fixtureTask({ title: "Fix the other thing" });
    expect((await taskToNote(changed)).etag).not.toBe((await taskToNote(t)).etag);

    const changedEdges = fixtureTask({
      edges: {
        out: [{ to: "WL-7", type: "child_of" }],
        in: [],
      },
    });
    expect((await taskToNote(changedEdges)).etag).not.toBe((await taskToNote(t)).etag);

    // body is not part of the wl block; a naive computeEtag(wl) refactor
    // would stop seeing this change.
    const changedBody = fixtureTask({ body: "different body" });
    expect((await taskToNote(changedBody)).etag).not.toBe((await taskToNote(t)).etag);
  });

  it("ignores edge types other than child_of and blocks", async () => {
    const t = fixtureTask({
      edges: {
        out: [
          { to: "WL-7", type: "child_of" },
          { to: "WL-44", type: "blocks" },
          { to: "WL-99", type: "relates_to" },
        ],
        in: [
          { from: "WL-43", type: "child_of" },
          { from: "WL-41", type: "blocks" },
        ],
      },
    });
    const note = await taskToNote(t);

    expect(note.content).not.toContain("WL-99");
  });
});

describe("docToNote", () => {
  it("preserves the doc's own frontmatter verbatim", async () => {
    const frontmatter = {
      status: "draft",
      covers: "docs/specs/025-documents-in-the-backbone.md",
      nested: { a: 1, b: { c: 2 } },
      list: ["x", "y"],
      unknown_key: "surprise",
    };
    const d = fixtureDoc({ body: withFrontmatter(frontmatter, "body text\n") });
    const note = await docToNote(d);
    const parsed = parseNote(note.content);

    // plugin-added aliases are stripped by parseNote, so what remains is
    // exactly the doc's own frontmatter, structurally unchanged.
    expect(parsed.frontmatter).toEqual(frontmatter);
    expect(parsed.wl.aliases_added).toBe(true);
  });

  it("does not add aliases when the doc already has them", async () => {
    const frontmatter = {
      status: "draft",
      aliases: ["Custom Alias"],
    };
    const d = fixtureDoc({ body: withFrontmatter(frontmatter, "body text\n") });
    const note = await docToNote(d);
    const parsed = parseNote(note.content);

    expect(parsed.wl.aliases_added).toBe(false);
    expect(parsed.frontmatter.aliases).toEqual(["Custom Alias"]);
  });

  it("does not render a duplicate --- fence when the body carries its own frontmatter", async () => {
    const frontmatter = { status: "draft" };
    const body = "Some body text.\n";
    const d = fixtureDoc({ body: withFrontmatter(frontmatter, body) });
    const note = await docToNote(d);

    // Exactly the two fences renderNote itself writes -- none left over from
    // the doc's own block.
    expect(note.content.match(/^---$/gm)).toHaveLength(2);
    expect(parseNote(note.content).body).toBe(body);
  });

  it("reports a wl key collision instead of dropping either", async () => {
    const frontmatter = {
      status: "draft",
      wl: { author_note: "this collides with the reserved block" },
    };
    const d = fixtureDoc({ body: withFrontmatter(frontmatter, "body text\n") });
    const note = await docToNote(d);

    expect(note.conflict).toBeDefined();
    expect(note.conflict).toContain(String(d.id));
    // the backbone block is kept, with its own reserved shape
    expect(note.content).toContain("type: doc");
    expect(note.content).toContain(`etag: ${note.etag}`);
    // the author's own wl sub-key does not leak into the rendered note...
    expect(note.content).not.toContain("author_note");
    // ...but it is not lost either: the conflict message carries it, so a
    // human or a future write-back can recover what was dropped.
    expect(note.conflict).toContain("author_note");
    expect(note.conflict).toContain(JSON.stringify(frontmatter.wl));
  });

  it("reports a conflict and ignores non-mapping frontmatter instead of corrupting the note", async () => {
    const d = fixtureDoc({ body: "---\nnot a mapping\n---\nbody text\n" });
    const note = await docToNote(d);

    expect(note.conflict).toBeDefined();
    expect(note.conflict).toContain(String(d.id));
    // rendered as if there were no author frontmatter at all, never as
    // spread-out garbage keys.
    expect(note.content).not.toContain('"0":');
    expect(note.content).toContain("aliases:");
    expect(note.content).toContain("wl:");
    expect(parseNote(note.content).body).toBe("body text\n");
  });

  it("reports a conflict and ignores unparseable frontmatter instead of throwing", async () => {
    const d = fixtureDoc({ body: "---\nkey: [unclosed\n---\nbody text\n" });
    const note = await docToNote(d);

    expect(note.conflict).toBeDefined();
    expect(note.conflict).toContain(String(d.id));
    expect(parseNote(note.content).body).toBe("body text\n");
  });

  it("still changes the etag when the body or the version changes", async () => {
    const d = fixtureDoc();

    expect((await docToNote(fixtureDoc({ body: "different body" }))).etag).not.toBe((await docToNote(d)).etag);
    expect((await docToNote(fixtureDoc({ version: 4 }))).etag).not.toBe((await docToNote(d)).etag);
  });

  it("round-trips a doc note back to its own frontmatter and body", async () => {
    const frontmatter = {
      status: "draft",
      covers: "docs/specs/025-documents-in-the-backbone.md",
      aliases: ["Documents in the backbone"],
    };
    const body = "# Documents in the backbone\n\nSome body text.\n";
    const d = fixtureDoc({ body: withFrontmatter(frontmatter, body) });
    const note = await docToNote(d);
    const parsed = parseNote(note.content);

    expect(parsed.frontmatter).toEqual(frontmatter);
    expect(parsed.body).toBe(body);
  });

  it("uses the doc's slug for the note path and its wl block's id", async () => {
    const d = fixtureDoc({ slug: "documents-in-the-backbone", id: 25 });
    const note = await docToNote(d);
    const parsed = parseNote(note.content);

    expect(note.path).toBe("worklode/docs/documents-in-the-backbone.md");
    expect(parsed.wl.slug).toBe("documents-in-the-backbone");
    expect(parsed.wl.id).toBe(25);
  });

  it("emits number only for a numbered document, never 0 for a plan", async () => {
    const spec = fixtureDoc({ number: 25 });
    const plan = fixtureDoc({ kind: "plan", number: 0, slug: "025-1-something" });

    expect(parseNote((await docToNote(spec)).content).wl.number).toBe(25);
    expect(parseNote((await docToNote(plan)).content).wl.number).toBeUndefined();
    expect((await docToNote(plan)).content).not.toMatch(/^\s*number:/m);
  });
});

describe("the injected # <title> heading", () => {
  /** Every line of the rendered note after the closing frontmatter fence
   *  that is an ATX H1. Two of them is the bug this suite exists for. */
  function h1Lines(content: string): string[] {
    const body = content.slice(content.indexOf("\n---\n", 4) + 5);
    return body.split("\n").filter((line) => /^ {0,3}#(?:[ \t]|$)/.test(line));
  }

  it("does not inject one when the doc body already opens with its own H1", async () => {
    const body = "# Documents in the backbone\n\nSome body text.\n";
    const note = await docToNote(fixtureDoc({ title: "Documents in the backbone", body }));

    expect(h1Lines(note.content)).toEqual(["# Documents in the backbone"]);
    expect(parseNote(note.content).wl.heading_added).toBe(false);
    expect(parseNote(note.content).body).toBe(body);
  });

  it("injects one when the doc body does not open with an H1", async () => {
    const body = "Some body text with no heading.\n";
    const note = await docToNote(fixtureDoc({ title: "A doc", body }));

    expect(h1Lines(note.content)).toEqual(["# A doc"]);
    expect(parseNote(note.content).wl.heading_added).toBe(true);
    expect(parseNote(note.content).body).toBe(body);
  });

  it("treats leading blank lines as not part of the question", async () => {
    const body = "\n\n# Real Title\n\ntext\n";
    const note = await docToNote(fixtureDoc({ title: "Real Title", body }));

    expect(h1Lines(note.content)).toEqual(["# Real Title"]);
    expect(parseNote(note.content).wl.heading_added).toBe(false);
    expect(parseNote(note.content).body).toBe(body);
  });

  it("injects one when the body only looks like it opens with a heading", async () => {
    // None of these is an ATX H1: "#" needs a space, a tab or the end of the
    // line after it; four spaces of indent is an indented code block; "##" is
    // an H2; and the Setext form is deliberately not recognised.
    const notHeadings = [
      "#Documents\n\ntext\n",
      "    # Documents\n\ntext\n",
      "## Documents\n\ntext\n",
      "Documents\n=========\n\ntext\n",
      "> # Documents\n",
    ];

    for (const body of notHeadings) {
      const note = await docToNote(fixtureDoc({ title: "A doc", body }));
      expect(parseNote(note.content).wl.heading_added, JSON.stringify(body)).toBe(true);
      expect(note.content, JSON.stringify(body)).toContain("---\n# A doc\n");
      expect(parseNote(note.content).body, JSON.stringify(body)).toBe(body);
    }
  });

  it("recognises the corner forms CommonMark counts as an H1", async () => {
    // Up to three spaces of indent is still a heading; so is a bare "#" and
    // a tab-separated one.
    const headings = ["   # Indented\n", "#\n\ntext\n", "#\tTabbed\n"];

    for (const body of headings) {
      const note = await docToNote(fixtureDoc({ title: "A doc", body }));
      expect(parseNote(note.content).wl.heading_added, JSON.stringify(body)).toBe(false);
      expect(parseNote(note.content).body, JSON.stringify(body)).toBe(body);
    }
  });

  it("applies the same rule to task bodies, which are author-written too", async () => {
    const body = "# Fix the thing\n\nthe real explanation\n";
    const note = await taskToNote(fixtureTask({ title: "Fix the thing", body }));

    expect(h1Lines(note.content)).toEqual(["# Fix the thing"]);
    expect(parseNote(note.content).wl.heading_added).toBe(false);
    expect(parseNote(note.content).body).toBe(body);
  });

  it("always injects one into the generated project and index bodies", async () => {
    // Their bodies are the plugin's own and open with the generated-by
    // notice, never a heading -- the bit is recorded all the same so every
    // note kind answers the same question the same way.
    const projectNote = await projectToNote(fixtureProject(), [fixtureDoc()], [fixtureTask()]);
    const indexNote = await indexToNote([fixtureProject()], new Map(), "Worklode", "2026-08-16T09:12:00Z");

    for (const note of [projectNote, indexNote]) {
      expect(parseNote(note.content).wl.heading_added).toBe(true);
      expect(h1Lines(note.content)).toHaveLength(1);
    }
  });

  it("reads a note written before the bit existed as heading-injected", async () => {
    // Serializer 1 injected the heading unconditionally, so a missing bit
    // can only mean "injected" -- anything else would leave the old title
    // line stuck to the front of the body.
    const legacy = [
      "---",
      "aliases:",
      "  - A doc",
      "wl:",
      "  type: doc",
      "  serializer: 1",
      "  aliases_added: true",
      "  etag: 0123456789abcdef",
      "---",
      "# A doc",
      "",
      "body text",
      "",
    ].join("\n");
    const parsed = parseNote(legacy);

    expect(parsed.wl.heading_added).toBeUndefined();
    expect(parsed.body).toBe("body text\n");
    expect(parsed.frontmatter).toEqual({});
  });
});

describe("projectToNote / indexToNote", () => {
  it("renders project and index notes with wikilinks to their members", async () => {
    const p = fixtureProject();
    const docs = [fixtureDoc()];
    const tasks = [fixtureTask()];

    const projectNote = await projectToNote(p, docs, tasks);
    expect(projectNote.path).toBe("worklode/worklode.md");
    expect(projectNote.content).toContain("[[documents-in-the-backbone]]");
    expect(projectNote.content).toContain("[[WL-42]]");
    expect(projectNote.content).toContain(
      "> Generated by the Worklode plugin. Edits here are overwritten on sync.",
    );
  });

  it("does not double-blank the body when a project has no docs", async () => {
    const p = fixtureProject();
    const note = await projectToNote(p, [], [fixtureTask()]);

    expect(note.content).not.toContain("\n\n\n");
  });

  it("renders the index body with each project's doc and task counts", async () => {
    const withMembers = fixtureProject({ id: "worklode", name: "Worklode" });
    const withoutMembers = fixtureProject({ id: "other", name: "Other Project", key: "OP" });

    const byProject = new Map([
      [
        "worklode",
        {
          docs: [fixtureDoc({ slug: "doc-a" }), fixtureDoc({ slug: "doc-b" })],
          tasks: [
            fixtureTask({ id: "WL-42" }),
            fixtureTask({ id: "WL-43" }),
            fixtureTask({ id: "WL-44" }),
          ],
        },
      ],
      // "other" is deliberately absent: it's in `projects` but has no entry
      // here, the edge case the map lookup invites.
    ]);

    const indexNote = await indexToNote(
      [withMembers, withoutMembers],
      byProject,
      "Worklode Vault",
      "2026-08-16T09:12:00Z",
    );

    expect(indexNote.path).toBe("Worklode Vault.md");
    expect(indexNote.content).toContain("[[worklode]]");
    expect(indexNote.content).toContain("2026-08-16T09:12:00Z");
    // the project with members shows its real counts...
    expect(indexNote.content).toContain("[[worklode]] Worklode — 2 docs, 3 tasks");
    // ...and the project absent from the map renders zeros, not an omission.
    expect(indexNote.content).toContain("[[other]] Other Project — 0 docs, 0 tasks");
  });
});

describe("parseNote(taskToNote(t))", () => {
  it("round-trips the root/empty-list shape too", async () => {
    const t = fixtureTask({
      edges: { out: [], in: [] },
    });
    const note = await taskToNote(t);
    const parsed = parseNote(note.content);

    expect(parsed.wl.parent).toBeUndefined();
    expect(parsed.wl.children).toEqual([]);
    expect(parsed.wl.blocks).toEqual([]);
    expect(parsed.wl.blocked_by).toEqual([]);
  });
});

// The write-back pass's escape hatch: a body the backbone overwrote, kept
// verbatim beside the note it was taken from.
describe("conflictToNote", () => {
  const AT = "2026-08-17T14:30:00.000Z";

  it("puts the note under _conflicts, stamped with the instant and free of ':'", async () => {
    const note = await conflictToNote(fixtureTask(), "my version\n", AT);

    expect(note.path).toBe("_conflicts/worklode/WL-42 2026-08-17T14-30-00Z.md");
    expect(note.path).not.toContain(":");
  });

  it("names the task, the note it came from, and when", async () => {
    const note = await conflictToNote(fixtureTask(), "my version\n", AT);
    const parsed = parseNote(note.content);

    expect(parsed.wl.type).toBe("conflict");
    expect(parsed.wl.task).toBe("[[WL-42]]");
    expect(parsed.wl.task_note).toBe("worklode/tasks/WL-42.md");
    expect(parsed.wl.detected_at).toBe(AT);
  });

  it("keeps the local body verbatim, after the explanation", async () => {
    const localBody = "# My heading\n\n- a list item\n- another\n";
    const note = await conflictToNote(fixtureTask(), localBody, AT);
    const parsed = parseNote(note.content);

    expect(parsed.body.endsWith(localBody)).toBe(true);
    expect(parsed.body).toContain("your text is");
  });

  it("distinguishes two conflicts on the same task", async () => {
    const first = await conflictToNote(fixtureTask(), "one\n", AT);
    const second = await conflictToNote(fixtureTask(), "two\n", "2026-08-17T15:00:00.000Z");

    expect(second.path).not.toBe(first.path);
  });
});

describe("computeEtag", () => {
  it("is order-independent over object key order, at every nesting level", async () => {
    const a = await computeEtag({ x: 1, y: 2 });
    const b = await computeEtag({ y: 2, x: 1 });
    expect(a).toBe(b);

    const nestedA = await computeEtag({ a: { y: 2, x: 1 } });
    const nestedB = await computeEtag({ a: { x: 1, y: 2 } });
    expect(nestedA).toBe(nestedB);
  });

  it("treats array order as significant", async () => {
    const a = await computeEtag([1, 2]);
    const b = await computeEtag([2, 1]);
    expect(a).not.toBe(b);
  });

  // The etag is a persisted value: it sits in every mirrored note's wl block,
  // and applyMirror skips a note whose stored etag still matches. Change what
  // is hashed, or how, and every vault in the org silently rewrites itself
  // whole on the next sync. These digests are therefore pinned rather than
  // derived -- each is `sha256(<the literal below>) | head -c 16`, verifiable
  // outside this codebase with `printf '%s' '{}' | sha256sum`, so a change to
  // the payload or the algorithm fails here instead of in a user's vault.
  //
  // They also pin the move off node:crypto's createHash to Web Crypto's
  // subtle.digest (the change that lets the plugin run on Obsidian mobile):
  // same canonical JSON, same UTF-8 bytes, same sha256, same 16-char prefix.
  // The multi-byte case is the one that would catch an encoding slip.
  it("produces known digests for known payloads", async () => {
    expect(await computeEtag({})).toBe("44136fa355b3678a"); // {}
    expect(await computeEtag({ a: 1, b: "two", c: [3, null, true] })).toBe("01998eb1fc2cc3f2");
    expect(await computeEtag("")).toBe("12ae32cb1ec02d01"); // ""
    expect(await computeEtag({ "å": "ø — ünïcode" })).toBe("6426518f21ed682c");
  });

  it("is 16 lowercase hex chars", async () => {
    expect(await computeEtag({ any: "payload" })).toMatch(/^[0-9a-f]{16}$/);
  });
});

// The end-to-end half of the pin above: not just the digest function, but the
// payload each note kind feeds it. A change to what a note hashes -- a field
// added to or dropped from the etag source -- rewrites every note of that
// kind in every mirrored vault, so it has to be a deliberate act, not a side
// effect. If one of these fails, check whether the fixture above changed
// before changing the constant.
describe("golden note etags", () => {
  it("hashes the same payload it always has, per note kind", async () => {
    expect((await taskToNote(fixtureTask())).etag).toBe("9cf7929f46269e43");
    expect((await docToNote(fixtureDoc())).etag).toBe("7cb835063c8dc0a8");
    expect((await projectToNote(fixtureProject(), [fixtureDoc()], [fixtureTask()])).etag).toBe(
      "54a5e9210790c806",
    );
    const byProject = new Map([["worklode", { docs: [fixtureDoc()], tasks: [fixtureTask()] }]]);
    expect(
      (await indexToNote([fixtureProject()], byProject, "Worklode", "2026-08-16T09:12:00Z")).etag,
    ).toBe("4064b66232456442");
  });
});
