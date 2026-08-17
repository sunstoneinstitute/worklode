import { describe, expect, it } from "vitest";
import { computeEtag, docToNote, indexToNote, parseNote, projectToNote, taskToNote } from "../src/serialize/note";
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

function fixtureDoc(overrides: Partial<Doc> = {}): Doc {
  return {
    id: "WL-SPEC-025",
    project: "worklode",
    kind: "spec",
    ordinal: "025",
    status: "draft",
    title: "Documents in the backbone",
    version: 3,
    source_branch: "main",
    source_dirty: false,
    synced_at: "2026-08-16T09:12:00Z",
    body: "body markdown verbatim",
    frontmatter: {
      status: "draft",
      covers: "docs/specs/025-documents-in-the-backbone.md",
    },
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
  it("renders a task note with relations as wikilinks", () => {
    const t = fixtureTask();
    const note = taskToNote(t);

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

  it("omits parent for a root task and renders empty relation lists", () => {
    const t = fixtureTask({
      edges: { out: [], in: [] },
    });
    const note = taskToNote(t);

    expect(note.content).not.toMatch(/^\s*parent:/m);
    expect(note.content).toMatch(/children: \[\]/);
    expect(note.content).toMatch(/blocks: \[\]/);
    expect(note.content).toMatch(/blocked_by: \[\]/);
  });

  it("writes the body verbatim", () => {
    const body = "line one\n---\nline with a\ttab\nline with trailing space   \n```\ncode fence\n```";
    const t = fixtureTask({ body });
    const note = taskToNote(t);

    expect(note.content.endsWith(body)).toBe(true);
    expect(parseNote(note.content).body).toBe(body);
  });

  it("round-trips: parseNote(taskToNote(t)) recovers the wl block and body", () => {
    const t = fixtureTask({
      body: "some body text\nwith multiple lines\n",
    });
    const note = taskToNote(t);
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

  it("changes the etag when any backbone field changes, and not otherwise", () => {
    const t = fixtureTask();
    const again = fixtureTask();
    expect(taskToNote(t).etag).toBe(taskToNote(again).etag);

    const changed = fixtureTask({ title: "Fix the other thing" });
    expect(taskToNote(changed).etag).not.toBe(taskToNote(t).etag);

    const changedEdges = fixtureTask({
      edges: {
        out: [{ to: "WL-7", type: "child_of" }],
        in: [],
      },
    });
    expect(taskToNote(changedEdges).etag).not.toBe(taskToNote(t).etag);

    // body is not part of the wl block; a naive computeEtag(wl) refactor
    // would stop seeing this change.
    const changedBody = fixtureTask({ body: "different body" });
    expect(taskToNote(changedBody).etag).not.toBe(taskToNote(t).etag);
  });

  it("ignores edge types other than child_of and blocks", () => {
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
    const note = taskToNote(t);

    expect(note.content).not.toContain("WL-99");
  });
});

describe("docToNote", () => {
  it("preserves the doc's own frontmatter verbatim", () => {
    const frontmatter = {
      status: "draft",
      covers: "docs/specs/025-documents-in-the-backbone.md",
      nested: { a: 1, b: { c: 2 } },
      list: ["x", "y"],
      unknown_key: "surprise",
    };
    const d = fixtureDoc({ frontmatter });
    const note = docToNote(d);
    const parsed = parseNote(note.content);

    // plugin-added aliases are stripped by parseNote, so what remains is
    // exactly the doc's own frontmatter, structurally unchanged.
    expect(parsed.frontmatter).toEqual(frontmatter);
    expect(parsed.wl.aliases_added).toBe(true);
  });

  it("does not add aliases when the doc already has them", () => {
    const frontmatter = {
      status: "draft",
      aliases: ["Custom Alias"],
    };
    const d = fixtureDoc({ frontmatter });
    const note = docToNote(d);
    const parsed = parseNote(note.content);

    expect(parsed.wl.aliases_added).toBe(false);
    expect(parsed.frontmatter.aliases).toEqual(["Custom Alias"]);
  });

  it("keeps ordinal a string", () => {
    const d = fixtureDoc({ ordinal: "025" });
    const note = docToNote(d);

    expect(note.content).toContain('ordinal: "025"');
    expect(note.content).not.toMatch(/ordinal: 025\b/);
  });

  it("reports a wl key collision instead of dropping either", () => {
    const frontmatter = {
      status: "draft",
      wl: { author_note: "this collides with the reserved block" },
    };
    const d = fixtureDoc({ frontmatter });
    const note = docToNote(d);

    expect(note.conflict).toBeDefined();
    expect(note.conflict).toContain(d.id);
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

  it("reports a conflict and ignores non-object frontmatter instead of corrupting the note", () => {
    const d = fixtureDoc({ frontmatter: "not an object" as unknown as Doc["frontmatter"] });
    const note = docToNote(d);

    expect(note.conflict).toBeDefined();
    expect(note.conflict).toContain(d.id);
    // rendered as if there were no author frontmatter at all, never as
    // spread-out garbage keys.
    expect(note.content).not.toContain('"0":');
    expect(note.content).toContain("aliases:");
    expect(note.content).toContain("wl:");
  });

  // The backbone bumps docs.synced_at on every `lode doc sync`, unchanged
  // content included. An etag covering it would make every sync rewrite
  // every doc note -- and every project note, whose etag covers its docs.
  it("ignores synced_at in the etag, and carries no synced_at in the note", () => {
    const d = fixtureDoc();
    const resynced = fixtureDoc({ synced_at: "2026-09-01T00:00:00Z" });

    expect(docToNote(resynced).etag).toBe(docToNote(d).etag);
    expect(docToNote(d).content).not.toContain("synced_at");
    expect(projectToNote(fixtureProject(), [resynced], []).etag).toBe(
      projectToNote(fixtureProject(), [d], []).etag,
    );
  });

  it("still changes the etag when the body or the version changes", () => {
    const d = fixtureDoc();

    expect(docToNote(fixtureDoc({ body: "different body" })).etag).not.toBe(docToNote(d).etag);
    expect(docToNote(fixtureDoc({ version: 4 })).etag).not.toBe(docToNote(d).etag);
  });

  it("round-trips a doc note back to its own frontmatter and body", () => {
    const frontmatter = {
      status: "draft",
      covers: "docs/specs/025-documents-in-the-backbone.md",
      aliases: ["Documents in the backbone"],
    };
    const body = "# Documents in the backbone\n\nSome body text.\n";
    const d = fixtureDoc({ frontmatter, body });
    const note = docToNote(d);
    const parsed = parseNote(note.content);

    expect(parsed.frontmatter).toEqual(frontmatter);
    expect(parsed.body).toBe(body);
  });
});

describe("the injected # <title> heading", () => {
  /** Every line of the rendered note after the closing frontmatter fence
   *  that is an ATX H1. Two of them is the bug this suite exists for. */
  function h1Lines(content: string): string[] {
    const body = content.slice(content.indexOf("\n---\n", 4) + 5);
    return body.split("\n").filter((line) => /^ {0,3}#(?:[ \t]|$)/.test(line));
  }

  it("does not inject one when the doc body already opens with its own H1", () => {
    const body = "# Documents in the backbone\n\nSome body text.\n";
    const note = docToNote(fixtureDoc({ title: "Documents in the backbone", body }));

    expect(h1Lines(note.content)).toEqual(["# Documents in the backbone"]);
    expect(parseNote(note.content).wl.heading_added).toBe(false);
    expect(parseNote(note.content).body).toBe(body);
  });

  it("injects one when the doc body does not open with an H1", () => {
    const body = "Some body text with no heading.\n";
    const note = docToNote(fixtureDoc({ title: "A doc", body }));

    expect(h1Lines(note.content)).toEqual(["# A doc"]);
    expect(parseNote(note.content).wl.heading_added).toBe(true);
    expect(parseNote(note.content).body).toBe(body);
  });

  it("treats leading blank lines as not part of the question", () => {
    const body = "\n\n# Real Title\n\ntext\n";
    const note = docToNote(fixtureDoc({ title: "Real Title", body }));

    expect(h1Lines(note.content)).toEqual(["# Real Title"]);
    expect(parseNote(note.content).wl.heading_added).toBe(false);
    expect(parseNote(note.content).body).toBe(body);
  });

  it("injects one when the body only looks like it opens with a heading", () => {
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
      const note = docToNote(fixtureDoc({ title: "A doc", body }));
      expect(parseNote(note.content).wl.heading_added, JSON.stringify(body)).toBe(true);
      expect(note.content, JSON.stringify(body)).toContain("---\n# A doc\n");
      expect(parseNote(note.content).body, JSON.stringify(body)).toBe(body);
    }
  });

  it("recognises the corner forms CommonMark counts as an H1", () => {
    // Up to three spaces of indent is still a heading; so is a bare "#" and
    // a tab-separated one.
    const headings = ["   # Indented\n", "#\n\ntext\n", "#\tTabbed\n"];

    for (const body of headings) {
      const note = docToNote(fixtureDoc({ title: "A doc", body }));
      expect(parseNote(note.content).wl.heading_added, JSON.stringify(body)).toBe(false);
      expect(parseNote(note.content).body, JSON.stringify(body)).toBe(body);
    }
  });

  it("applies the same rule to task bodies, which are author-written too", () => {
    const body = "# Fix the thing\n\nthe real explanation\n";
    const note = taskToNote(fixtureTask({ title: "Fix the thing", body }));

    expect(h1Lines(note.content)).toEqual(["# Fix the thing"]);
    expect(parseNote(note.content).wl.heading_added).toBe(false);
    expect(parseNote(note.content).body).toBe(body);
  });

  it("always injects one into the generated project and index bodies", () => {
    // Their bodies are the plugin's own and open with the generated-by
    // notice, never a heading -- the bit is recorded all the same so every
    // note kind answers the same question the same way.
    const projectNote = projectToNote(fixtureProject(), [fixtureDoc()], [fixtureTask()]);
    const indexNote = indexToNote([fixtureProject()], new Map(), "Worklode", "2026-08-16T09:12:00Z");

    for (const note of [projectNote, indexNote]) {
      expect(parseNote(note.content).wl.heading_added).toBe(true);
      expect(h1Lines(note.content)).toHaveLength(1);
    }
  });

  it("reads a note written before the bit existed as heading-injected", () => {
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
  it("renders project and index notes with wikilinks to their members", () => {
    const p = fixtureProject();
    const docs = [fixtureDoc()];
    const tasks = [fixtureTask()];

    const projectNote = projectToNote(p, docs, tasks);
    expect(projectNote.path).toBe("worklode/worklode.md");
    expect(projectNote.content).toContain("[[WL-SPEC-025]]");
    expect(projectNote.content).toContain("[[WL-42]]");
    expect(projectNote.content).toContain(
      "> Generated by the Worklode plugin. Edits here are overwritten on sync.",
    );
  });

  it("does not double-blank the body when a project has no docs", () => {
    const p = fixtureProject();
    const note = projectToNote(p, [], [fixtureTask()]);

    expect(note.content).not.toContain("\n\n\n");
  });

  it("renders the index body with each project's doc and task counts", () => {
    const withMembers = fixtureProject({ id: "worklode", name: "Worklode" });
    const withoutMembers = fixtureProject({ id: "other", name: "Other Project", key: "OP" });

    const byProject = new Map([
      [
        "worklode",
        {
          docs: [fixtureDoc({ id: "WL-SPEC-025" }), fixtureDoc({ id: "WL-SPEC-026" })],
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

    const indexNote = indexToNote(
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
  it("round-trips the root/empty-list shape too", () => {
    const t = fixtureTask({
      edges: { out: [], in: [] },
    });
    const note = taskToNote(t);
    const parsed = parseNote(note.content);

    expect(parsed.wl.parent).toBeUndefined();
    expect(parsed.wl.children).toEqual([]);
    expect(parsed.wl.blocks).toEqual([]);
    expect(parsed.wl.blocked_by).toEqual([]);
  });
});

describe("computeEtag", () => {
  it("is order-independent over object key order, at every nesting level", () => {
    const a = computeEtag({ x: 1, y: 2 });
    const b = computeEtag({ y: 2, x: 1 });
    expect(a).toBe(b);

    const nestedA = computeEtag({ a: { y: 2, x: 1 } });
    const nestedB = computeEtag({ a: { x: 1, y: 2 } });
    expect(nestedA).toBe(nestedB);
  });

  it("treats array order as significant", () => {
    const a = computeEtag([1, 2]);
    const b = computeEtag([2, 1]);
    expect(a).not.toBe(b);
  });
});
