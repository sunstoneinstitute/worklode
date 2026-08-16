// The VaultWriter implementation backed by Obsidian's vault adapter. Uses
// the adapter's path-based API (list/read/write/remove/mkdir/rmdir) rather
// than app.vault.create/modify: it maps directly onto VaultWriter and does
// not need a TFile handle for a file the plugin is about to overwrite.

import type { DataAdapter } from "obsidian";
import type { VaultWriter } from "../sync/mirror";

/** A path segment (relative to root) is safe when it has no ".." segment,
 *  no empty segment, and does not start with a path separator -- the same
 *  check test/mirror.test.ts's in-memory fake applies. Defense in depth:
 *  the whole mount-root guarantee already lives in desiredNotes, but this
 *  is the last line before a real delete/write reaches disk. */
function assertInsideRoot(path: string): void {
  const segments = path.split(/[\\/]/);
  if (path.startsWith("/") || path.startsWith("\\") || segments.includes("..") || segments.includes("")) {
    throw new Error(`refusing to touch a path outside the mount root: ${JSON.stringify(path)}`);
  }
}

export class ObsidianVaultWriter implements VaultWriter {
  constructor(private readonly adapter: DataAdapter) {}

  /** Every file under dir, recursively, as vault-relative paths (what the
   *  adapter's own list() returns them as -- not relative to dir). */
  private async listAll(dir: string): Promise<string[]> {
    if (!(await this.adapter.exists(dir))) return [];
    const { files, folders } = await this.adapter.list(dir);
    const nested = await Promise.all(folders.map((folder) => this.listAll(folder)));
    return [...files, ...nested.flat()];
  }

  async list(root: string): Promise<string[]> {
    const all = await this.listAll(root);
    const prefix = `${root}/`;
    return all.filter((p) => p.endsWith(".md")).map((p) => (p.startsWith(prefix) ? p.slice(prefix.length) : p));
  }

  async read(root: string, path: string): Promise<string> {
    assertInsideRoot(path);
    return this.adapter.read(`${root}/${path}`);
  }

  /** Creates every missing ancestor directory (root itself included) before
   *  writing -- adapter.write does not create parent folders, and every
   *  desired path but the index is nested at least one directory deep. */
  async write(root: string, path: string, content: string): Promise<void> {
    assertInsideRoot(path);
    const full = `${root}/${path}`;
    const dir = full.slice(0, full.lastIndexOf("/"));
    await this.ensureDir(dir);
    await this.adapter.write(full, content);
  }

  private async ensureDir(dir: string): Promise<void> {
    const segments = dir.split("/");
    let current = "";
    for (const segment of segments) {
      current = current ? `${current}/${segment}` : segment;
      if (!(await this.adapter.exists(current))) {
        await this.adapter.mkdir(current);
      }
    }
  }

  /** Removes the file, then prunes any ancestor directory left empty by the
   *  removal (stopping at root), so a deleted project does not leave a
   *  husk of empty folders under the mount. */
  async remove(root: string, path: string): Promise<void> {
    assertInsideRoot(path);
    const full = `${root}/${path}`;
    await this.adapter.remove(full);
    await this.pruneEmptyDirs(root, full.slice(0, full.lastIndexOf("/")));
  }

  private async pruneEmptyDirs(root: string, dir: string): Promise<void> {
    while (dir && dir !== root) {
      const { files, folders } = await this.adapter.list(dir);
      if (files.length > 0 || folders.length > 0) return;
      await this.adapter.rmdir(dir, false);
      dir = dir.slice(0, dir.lastIndexOf("/"));
    }
  }

  /** Deletes everything under root, root included -- used by the explicit
   *  "purge" command, which (unlike a mirror sync) is not limited to .md
   *  files: it is the user asking to wipe the whole mount. Returns the
   *  number of files that were under root, for the confirmation report. */
  async purgeRoot(root: string): Promise<number> {
    const all = await this.listAll(root);
    if (await this.adapter.exists(root)) {
      await this.adapter.rmdir(root, true);
    }
    return all.length;
  }
}
