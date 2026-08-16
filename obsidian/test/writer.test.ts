import { describe, expect, it } from "vitest";
import type { DataAdapter, DataWriteOptions, ListedFiles, Stat } from "obsidian";
import { ObsidianVaultWriter } from "../src/vault/writer";

/** In-memory DataAdapter fake, backing only the methods ObsidianVaultWriter
 *  actually calls (exists/list/read/write/mkdir/remove/rmdir); everything
 *  else throws, so a stray call is a loud test failure rather than a silent
 *  no-op. list()/exists() resolve case-insensitively, mirroring a
 *  case-insensitive filesystem (the common case for desktop Obsidian) --
 *  the scenario the writer's own case-sensitive prefix check in list() has
 *  to defend against. write/read/remove/mkdir/rmdir stay exact-match: the
 *  writer always uses one consistently-cased root per call chain, so
 *  fidelity there isn't needed to exercise the case-mismatch guard. */
class FakeAdapter implements DataAdapter {
  files = new Map<string, string>();
  dirs = new Set<string>();
  mkdirCalls = 0;

  private normalize(path: string): string {
    if (path === ".") return "";
    return path.endsWith("/") ? path.slice(0, -1) : path;
  }

  /** "" (the vault root, including the "." alias normalize() maps to it)
   *  is every stored key's ancestor by construction -- every key is a plain
   *  relative path with no leading slash -- so it always matches "child"
   *  rather than going through the queryLower + "/" prefix test below. */
  private matchKind(key: string, queryLower: string): "self" | "child" | "none" {
    if (queryLower === "") return "child";
    const keyLower = key.toLowerCase();
    if (keyLower === queryLower) return "self";
    if (keyLower.startsWith(`${queryLower}/`)) return "child";
    return "none";
  }

  async exists(path: string): Promise<boolean> {
    const p = this.normalize(path);
    if (p === "") return true;
    const lower = p.toLowerCase();
    for (const key of [...this.files.keys(), ...this.dirs]) {
      if (this.matchKind(key, lower) !== "none") return true;
    }
    return false;
  }

  async list(path: string): Promise<ListedFiles> {
    const p = this.normalize(path);
    const lower = p.toLowerCase();
    const files = new Set<string>();
    const folders = new Set<string>();
    for (const key of [...this.files.keys(), ...this.dirs]) {
      if (this.matchKind(key, lower) !== "child") continue;
      const rest = p === "" ? key : key.slice(p.length + 1);
      const idx = rest.indexOf("/");
      if (idx === -1) {
        files.add(key);
      } else {
        folders.add(p === "" ? rest.slice(0, idx) : key.slice(0, p.length + 1 + idx));
      }
    }
    return { files: [...files], folders: [...folders] };
  }

  async read(path: string): Promise<string> {
    const content = this.files.get(path);
    if (content === undefined) throw new Error(`not found: ${path}`);
    return content;
  }

  async write(path: string, data: string): Promise<void> {
    this.files.set(path, data);
  }

  async mkdir(path: string): Promise<void> {
    this.mkdirCalls++;
    this.dirs.add(path);
  }

  async remove(path: string): Promise<void> {
    if (!this.files.delete(path)) throw new Error(`not found: ${path}`);
  }

  async rmdir(path: string, recursive: boolean): Promise<void> {
    const prefix = `${path}/`;
    const childFiles = [...this.files.keys()].filter((k) => k.startsWith(prefix));
    const childDirs = [...this.dirs].filter((k) => k.startsWith(prefix));
    if (!recursive && (childFiles.length > 0 || childDirs.length > 0)) {
      throw new Error(`rmdir refused, not empty: ${path}`);
    }
    for (const f of childFiles) this.files.delete(f);
    for (const d of childDirs) this.dirs.delete(d);
    this.dirs.delete(path);
  }

  getName(): string {
    throw new Error("not implemented in test fake");
  }
  stat(_path: string): Promise<Stat | null> {
    throw new Error("not implemented in test fake");
  }
  readBinary(_path: string): Promise<ArrayBuffer> {
    throw new Error("not implemented in test fake");
  }
  writeBinary(_path: string, _data: ArrayBuffer, _options?: DataWriteOptions): Promise<void> {
    throw new Error("not implemented in test fake");
  }
  append(_path: string, _data: string, _options?: DataWriteOptions): Promise<void> {
    throw new Error("not implemented in test fake");
  }
  appendBinary(_path: string, _data: ArrayBuffer, _options?: DataWriteOptions): Promise<void> {
    throw new Error("not implemented in test fake");
  }
  process(_path: string, _fn: (data: string) => string, _options?: DataWriteOptions): Promise<string> {
    throw new Error("not implemented in test fake");
  }
  getResourcePath(_path: string): string {
    throw new Error("not implemented in test fake");
  }
  trashSystem(_path: string): Promise<boolean> {
    throw new Error("not implemented in test fake");
  }
  trashLocal(_path: string): Promise<void> {
    throw new Error("not implemented in test fake");
  }
  rename(_path: string, _newPath: string): Promise<void> {
    throw new Error("not implemented in test fake");
  }
  copy(_path: string, _newPath: string): Promise<void> {
    throw new Error("not implemented in test fake");
  }
}

const ROOT = "Worklode";

describe("ObsidianVaultWriter.list", () => {
  it("returns root-relative .md paths and drops non-.md files", async () => {
    const adapter = new FakeAdapter();
    adapter.files.set("Worklode/worklode/worklode.md", "a");
    adapter.files.set("Worklode/worklode/tasks/WL-1.md", "b");
    adapter.files.set("Worklode/attachment.png", "not markdown");
    // A file that just happens to share the root name as a prefix, but is
    // actually a sibling, not a child -- "Worklode2/x.md" must never be
    // mistaken for something under "Worklode".
    adapter.files.set("Worklode2/x.md", "sibling, not a child");

    const writer = new ObsidianVaultWriter(adapter);
    const result = await writer.list(ROOT);

    expect(result.sort()).toEqual(["worklode/tasks/WL-1.md", "worklode/worklode.md"].sort());
  });

  it("returns [] when root does not exist", async () => {
    const writer = new ObsidianVaultWriter(new FakeAdapter());
    expect(await writer.list(ROOT)).toEqual([]);
  });

  // Regression coverage for the CRITICAL finding: a mount root that
  // resolves to the vault root (or a variant that a naive prefix-strip
  // would fail to match) must never leak a path list() hands back as if it
  // were safely root-relative -- that list feeds straight into applyMirror's
  // delete pass.
  it('list(".") never returns paths from outside the intended root, even though "." resolves to the vault root', async () => {
    const adapter = new FakeAdapter();
    adapter.files.set("Daily/2026-08-16.md", "a note nothing to do with worklode");
    adapter.files.set(".obsidian/workspace.json", "app config, definitely not ours");
    adapter.files.set("Worklode/worklode/worklode.md", "a mirror note, for contrast");

    const writer = new ObsidianVaultWriter(adapter);
    const result = await writer.list(".");

    expect(result).toEqual([]);
  });

  it('list("Worklode/") (trailing slash) never returns a path remove would resolve outside root', async () => {
    const adapter = new FakeAdapter();
    adapter.files.set("Worklode/worklode/worklode.md", "a");
    adapter.files.set("Worklode/worklode/tasks/WL-1.md", "b");

    const writer = new ObsidianVaultWriter(adapter);
    const result = await writer.list("Worklode/");

    expect(result).toEqual([]);
  });

  it('list("worklode") (wrong case) never returns a path remove would resolve outside root', async () => {
    const adapter = new FakeAdapter();
    adapter.files.set("Worklode/worklode/worklode.md", "a");
    adapter.files.set("Worklode/worklode/tasks/WL-1.md", "b");

    const writer = new ObsidianVaultWriter(adapter);
    // The fake resolves this case-insensitively to the real "Worklode"
    // folder (as a case-insensitive OS filesystem would), returning
    // canonically-cased paths -- which the writer's own case-sensitive
    // prefix check must still refuse to treat as relative to "worklode".
    const result = await writer.list("worklode");

    expect(result).toEqual([]);
  });
});

describe("ObsidianVaultWriter.write", () => {
  it("creates every missing ancestor directory before writing", async () => {
    const adapter = new FakeAdapter();
    const writer = new ObsidianVaultWriter(adapter);

    await writer.write(ROOT, "worklode/tasks/WL-1.md", "content");

    expect(adapter.files.get("Worklode/worklode/tasks/WL-1.md")).toBe("content");
    expect(await adapter.exists("Worklode")).toBe(true);
    expect(await adapter.exists("Worklode/worklode")).toBe(true);
    expect(await adapter.exists("Worklode/worklode/tasks")).toBe(true);
  });

  it("overwrites an existing file without recreating existing ancestors", async () => {
    const adapter = new FakeAdapter();
    const writer = new ObsidianVaultWriter(adapter);
    await writer.write(ROOT, "worklode/worklode.md", "v1");
    const mkdirCallsAfterFirstWrite = adapter.mkdirCalls;

    await writer.write(ROOT, "worklode/worklode.md", "v2");

    expect(adapter.files.get("Worklode/worklode/worklode.md")).toBe("v2");
    expect(adapter.mkdirCalls).toBe(mkdirCallsAfterFirstWrite);
  });

  it("refuses an adversarial path before touching the adapter", async () => {
    const adapter = new FakeAdapter();
    const writer = new ObsidianVaultWriter(adapter);

    await expect(writer.write(ROOT, "../escape.md", "x")).rejects.toThrow();
    await expect(writer.write(ROOT, "/abs.md", "x")).rejects.toThrow();
    await expect(writer.write(ROOT, "worklode/../../escape.md", "x")).rejects.toThrow();
    expect(adapter.files.size).toBe(0);
  });
});

describe("ObsidianVaultWriter.remove", () => {
  it("prunes emptied ancestor folders, stopping at root", async () => {
    const adapter = new FakeAdapter();
    const writer = new ObsidianVaultWriter(adapter);
    await writer.write(ROOT, "worklode/tasks/WL-1.md", "content");

    await writer.remove(ROOT, "worklode/tasks/WL-1.md");

    expect(adapter.files.has("Worklode/worklode/tasks/WL-1.md")).toBe(false);
    // Both "worklode/tasks" and "worklode" are now empty and pruned...
    expect(await adapter.exists("Worklode/worklode/tasks")).toBe(false);
    expect(await adapter.exists("Worklode/worklode")).toBe(false);
    // ...but root itself is never removed.
    expect(await adapter.exists(ROOT)).toBe(true);
  });

  it("stops pruning at the first ancestor that still has content", async () => {
    const adapter = new FakeAdapter();
    const writer = new ObsidianVaultWriter(adapter);
    await writer.write(ROOT, "worklode/tasks/WL-1.md", "a");
    await writer.write(ROOT, "worklode/tasks/WL-2.md", "b");
    await writer.write(ROOT, "worklode/worklode.md", "project note");

    await writer.remove(ROOT, "worklode/tasks/WL-1.md");

    // "worklode/tasks" still has WL-2, so it survives, and so does its
    // parent -- pruning must never remove a folder still holding content.
    expect(await adapter.exists("Worklode/worklode/tasks")).toBe(true);
    expect(adapter.files.has("Worklode/worklode/tasks/WL-2.md")).toBe(true);
    expect(adapter.files.has("Worklode/worklode/worklode.md")).toBe(true);
  });

  it("refuses an adversarial path before touching the adapter", async () => {
    const adapter = new FakeAdapter();
    adapter.files.set("Worklode/worklode/worklode.md", "a");
    const writer = new ObsidianVaultWriter(adapter);

    await expect(writer.remove(ROOT, "../escape.md")).rejects.toThrow();
    await expect(writer.remove(ROOT, "/abs.md")).rejects.toThrow();
    await expect(writer.remove(ROOT, "worklode/../../escape.md")).rejects.toThrow();
    // Untouched: the guard fires before any adapter call.
    expect(adapter.files.has("Worklode/worklode/worklode.md")).toBe(true);
  });
});

describe("ObsidianVaultWriter.purgeRoot", () => {
  it("deletes everything under root, including non-.md files, and reports the count", async () => {
    const adapter = new FakeAdapter();
    adapter.files.set("Worklode/worklode/worklode.md", "a");
    adapter.files.set("Worklode/worklode/tasks/WL-1.md", "b");
    adapter.files.set("Worklode/attachment.png", "not markdown, purged anyway");
    adapter.files.set("Elsewhere/untouched.md", "outside the mount root");

    const writer = new ObsidianVaultWriter(adapter);
    const removed = await writer.purgeRoot(ROOT);

    expect(removed).toBe(3);
    expect(await adapter.exists(ROOT)).toBe(false);
    expect(adapter.files.has("Elsewhere/untouched.md")).toBe(true);
  });

  it("does nothing when root does not exist", async () => {
    const adapter = new FakeAdapter();
    const writer = new ObsidianVaultWriter(adapter);

    expect(await writer.purgeRoot(ROOT)).toBe(0);
  });
});
