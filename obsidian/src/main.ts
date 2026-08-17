// The Obsidian-bound shell: settings, commands, status bar, and the glue
// between the worklode HTTP API and the pure sync/serialize logic in
// src/sync/mirror.ts and src/serialize/note.ts. Together with
// src/vault/writer.ts, this is the only file in the plugin that imports
// "obsidian" -- everything it drives stays unit-testable without one.

import { App, Modal, Notice, Plugin, PluginSettingTab, Setting, debounce, requestUrl } from "obsidian";
import { WorklodeApiError, WorklodeClient, type HttpTransport } from "./api/client";
import type { ProjectMembers } from "./serialize/note";
import { applyMirror, desiredNotes, foreignNotes, isSafeMountRoot, type MirrorStats } from "./sync/mirror";
import { ObsidianVaultWriter } from "./vault/writer";

interface WorklodeSettings {
  baseUrl: string; // "" until configured
  token: string; // wl_<40 hex>
  mountRoot: string; // default "Worklode"; may be nested, e.g. "Team/Worklode"
  projects: string; // comma-separated allow-list; "" means all
  syncOnStartup: boolean; // default false
  intervalMinutes: number; // 0 = manual only; default 0
  /** Mount roots the mirror is allowed to own, by their full path. A root lands here
   *  when the mirror first syncs into it while it holds nothing but mirror
   *  notes, or when the user confirms taking over a folder that already
   *  held their own. Kept as a list rather than a single value so switching
   *  the setting back to a previously confirmed root does not ask again. */
  adoptedRoots: string[];
}

const DEFAULT_SETTINGS: WorklodeSettings = {
  baseUrl: "",
  token: "",
  mountRoot: "Worklode",
  projects: "",
  syncOnStartup: false,
  intervalMinutes: 0,
  adoptedRoots: [],
};

/** How long the mount-root field waits after the last keystroke before it
 *  saves. The tab otherwise saves on every character, so with an interval
 *  armed a sync could fire against a transient prefix of the name being
 *  typed -- "Not" on the way to "Notes". */
const MOUNT_ROOT_SAVE_DELAY_MS = 800;

/** requestUrl bypasses CORS, which a plain fetch from the renderer does not.
 *  throw:false hands control of non-2xx handling to WorklodeClient, which
 *  turns it into a WorklodeApiError with a legible message. */
const obsidianHttp: HttpTransport = async (req) => {
  const res = await requestUrl({ url: req.url, method: req.method, headers: req.headers, throw: false });
  return { status: res.status, text: res.text };
};

/** "" means no filter (sync every project); otherwise a trimmed, non-empty
 *  comma-separated id list. */
function parseAllowList(raw: string): Set<string> | undefined {
  const ids = raw
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
  return ids.length > 0 ? new Set(ids) : undefined;
}

function formatTime(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export default class WorklodePlugin extends Plugin {
  override settings: WorklodeSettings = { ...DEFAULT_SETTINGS };

  private writer!: ObsidianVaultWriter;
  private statusBarItem!: HTMLElement;
  /** True while a sync or a purge is running. Shared between the two: a
   *  purge mid-flight during an interval sync would otherwise remove the
   *  root out from under applyMirror, which throws partway and can then
   *  re-create part of the tree the purge just deleted. */
  private busy = false;
  private intervalId: number | undefined;
  /** The root an adoption prompt is currently open for. Interval syncs keep
   *  arriving while the user reads the modal; without this they would stack
   *  one on top of another. */
  private adoptPromptFor: string | undefined;

  override async onload(): Promise<void> {
    await this.loadSettings();
    this.writer = new ObsidianVaultWriter(this.app.vault.adapter);

    this.statusBarItem = this.addStatusBarItem();
    this.statusBarItem.setText("Worklode: not yet synced");

    this.addSettingTab(new WorklodeSettingTab(this.app, this));

    this.addCommand({
      id: "sync-now",
      name: "Sync now",
      callback: () => {
        void this.sync();
      },
    });

    this.addCommand({
      id: "purge-worklode-folder",
      name: "Purge the Worklode folder",
      callback: () => {
        new PurgeConfirmModal(this.app, this.settings.mountRoot.trim(), () => {
          void this.purge();
        }).open();
      },
    });

    this.scheduleInterval();

    if (this.settings.syncOnStartup) {
      void this.sync();
    }
  }

  async loadSettings(): Promise<void> {
    const data = (await this.loadData()) as Partial<WorklodeSettings> | null;
    this.settings = { ...DEFAULT_SETTINGS, ...data };
  }

  async saveSettings(): Promise<void> {
    await this.saveData(this.settings);
  }

  /** Re-registers the interval sync from the current settings. Clearing any
   *  previous interval first means a settings-tab edit takes effect without
   *  requiring a reload; the previous id is still safe for Obsidian's own
   *  registerInterval-driven cleanup to clear again on unload. */
  scheduleInterval(): void {
    if (this.intervalId !== undefined) {
      window.clearInterval(this.intervalId);
      this.intervalId = undefined;
    }
    if (this.settings.intervalMinutes > 0) {
      this.intervalId = this.registerInterval(
        window.setInterval(
          () => {
            void this.sync();
          },
          this.settings.intervalMinutes * 60 * 1000,
        ),
      );
    }
  }

  /** Fetches projects, filters by the allow-list, fetches each project's
   *  tasks and docs, diffs against the vault, and applies the difference.
   *  Guarded against overlapping runs: a slow sync plus a short interval
   *  must not stack. */
  async sync(): Promise<void> {
    if (this.busy) return;

    const baseUrl = this.settings.baseUrl.trim();
    const token = this.settings.token.trim();
    const mountRoot = this.settings.mountRoot.trim();
    if (!baseUrl) {
      new Notice("Worklode: set the base URL in plugin settings before syncing.");
      return;
    }
    if (!token) {
      new Notice("Worklode: set the API token in plugin settings before syncing.");
      return;
    }
    // The mount root is the only writable territory: every write, delete and
    // (for purge) recursive rmdir happens under it. isSafeMountRoot is the
    // same predicate desiredNotes applies to it, so a root accepted here can
    // never be one desiredNotes then judges unsafe. It may be nested, but
    // every segment must clear the single-segment rule backbone ids clear.
    if (!isSafeMountRoot(mountRoot)) {
      new Notice(
        'Worklode: set a valid mount root (a folder path like "Worklode" or "Team/Worklode", ' +
          'with no "." or ".." segment) in plugin settings before syncing.',
      );
      return;
    }
    // Inside the busy window: the guard awaits the vault, so two syncs
    // started close together would otherwise both get past the check above
    // while the first one is still surveying.
    this.busy = true;
    try {
      if (!(await this.ensureRootAdopted(mountRoot))) return;

      const client = new WorklodeClient(baseUrl, token, obsidianHttp);
      const allProjects = await client.listProjects();
      const allowList = parseAllowList(this.settings.projects);
      const selected = allowList ? allProjects.filter((p) => allowList.has(p.id)) : allProjects;

      if (allowList) {
        const foundIds = new Set(selected.map((p) => p.id));
        const missing = [...allowList].filter((id) => !foundIds.has(id));
        if (missing.length > 0) {
          new Notice(`Worklode: project id(s) not found, skipped: ${missing.join(", ")}.`);
        }
      }

      const byProject = new Map<string, ProjectMembers>();
      for (const project of selected) {
        const [tasks, docs] = await Promise.all([client.listTasks(project.id), client.listDocs(project.id)]);
        byProject.set(project.id, { docs, tasks });
      }

      const syncedAt = new Date().toISOString();
      const desired = desiredNotes(selected, byProject, mountRoot, syncedAt);
      const stats = await applyMirror(this.writer, mountRoot, desired);

      this.reportSuccess(stats);
    } catch (err) {
      this.reportFailure(err);
    } finally {
      this.busy = false;
    }
  }

  /** The first-sync guard. applyMirror deletes every .md under the mount
   *  root the backbone does not imply -- including files it never wrote --
   *  so a root that already holds the user's own notes is never taken over
   *  silently: the sync stops and asks. Returns whether the sync may
   *  proceed now; a confirmed prompt starts its own.
   *
   *  Deliberately checked here rather than when the setting is saved: every
   *  way a sync starts (the command, startup, the interval) goes through
   *  this one path, and it is the state at sync time that decides whether
   *  files are about to be deleted. */
  private async ensureRootAdopted(mountRoot: string): Promise<boolean> {
    if (this.settings.adoptedRoots.includes(mountRoot)) return true;
    if (this.adoptPromptFor !== undefined) return false;

    const foreign = await foreignNotes(this.writer, mountRoot);
    if (foreign.length === 0) {
      // Empty, absent, or already all ours: the mirror owns it from here on,
      // so the question is asked once and never again.
      await this.adoptRoot(mountRoot);
      return true;
    }

    this.adoptPromptFor = mountRoot;
    new AdoptRootModal(this.app, mountRoot, foreign, (confirmed) => {
      this.adoptPromptFor = undefined;
      if (!confirmed) return;
      void this.adoptRoot(mountRoot).then(() => this.sync());
    }).open();
    return false;
  }

  private async adoptRoot(root: string): Promise<void> {
    if (this.settings.adoptedRoots.includes(root)) return;
    this.settings.adoptedRoots = [...this.settings.adoptedRoots, root];
    await this.saveSettings();
  }

  async purge(): Promise<void> {
    if (this.busy) {
      new Notice("Worklode: a sync is already in progress; try purging again once it finishes.");
      return;
    }

    const mountRoot = this.settings.mountRoot.trim();
    if (!isSafeMountRoot(mountRoot)) {
      new Notice(
        'Worklode: set a valid mount root (a folder path like "Worklode" or "Team/Worklode", ' +
          'with no "." or ".." segment) in plugin settings before purging.',
      );
      return;
    }

    this.busy = true;
    try {
      const removed = await this.writer.purgeRoot(mountRoot);
      new Notice(`Worklode: purged ${removed} file(s) from "${mountRoot}".`);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      new Notice(`Worklode purge failed: ${message}`);
      console.error("Worklode purge failed:", err);
    } finally {
      this.busy = false;
    }
  }

  private reportSuccess(stats: MirrorStats): void {
    const noteCount = stats.written + stats.skipped;
    this.statusBarItem.setText(`Worklode: ${noteCount} notes · ${formatTime(new Date())}`);

    let message = `Worklode sync: ${stats.written} written, ${stats.skipped} unchanged, ${stats.removed} removed.`;
    if (stats.conflicts.length > 0) {
      message += ` ${stats.conflicts.length} conflict(s) skipped -- see console for details.`;
      console.warn("Worklode sync conflicts:", stats.conflicts);
    }
    new Notice(message);
  }

  private reportFailure(err: unknown): void {
    this.statusBarItem.setText("Worklode: sync failed");
    const message = err instanceof WorklodeApiError ? err.message : String(err);
    new Notice(`Worklode sync failed: ${message}`);
    console.error("Worklode sync failed:", err);
  }
}

class WorklodeSettingTab extends PluginSettingTab {
  constructor(
    app: App,
    private readonly plugin: WorklodePlugin,
  ) {
    super(app, plugin);
  }

  override display(): void {
    const { containerEl } = this;
    containerEl.empty();

    new Setting(containerEl)
      .setName("Base URL")
      .setDesc("The worklode server, e.g. https://worklode.example.com.")
      .addText((text) =>
        text
          .setPlaceholder("https://worklode.example.com")
          .setValue(this.plugin.settings.baseUrl)
          .onChange(async (value) => {
            this.plugin.settings.baseUrl = value;
            await this.plugin.saveSettings();
          }),
      );

    new Setting(containerEl)
      .setName("Token")
      .setDesc("A worklode API token (wl_ + 40 hex chars).")
      .addText((text) => {
        text.inputEl.type = "password";
        text
          .setPlaceholder("wl_...")
          .setValue(this.plugin.settings.token)
          .onChange(async (value) => {
            this.plugin.settings.token = value;
            await this.plugin.saveSettings();
          });
      });

    // The one debounced field: a half-typed root names a real folder as
    // often as not, and the value is what a sync is allowed to delete
    // under. resetTimer:true so it saves once the typing stops, not once
    // per burst.
    const saveMountRoot = debounce(
      (value: string) => {
        this.plugin.settings.mountRoot = value;
        void this.plugin.saveSettings();
      },
      MOUNT_ROOT_SAVE_DELAY_MS,
      true,
    );

    new Setting(containerEl)
      .setName("Mount root")
      .setDesc(
        'The vault folder the mirror owns; may be nested, e.g. "Team/Worklode". ' +
          "Everything under it is machine-managed: a sync deletes anything under it " +
          "Worklode does not mirror.",
      )
      .addText((text) =>
        text
          .setPlaceholder("Worklode")
          .setValue(this.plugin.settings.mountRoot)
          .onChange((value) => saveMountRoot(value)),
      );

    new Setting(containerEl)
      .setName("Projects")
      .setDesc("Comma-separated project ids to sync. Empty means every project.")
      .addText((text) =>
        text
          .setPlaceholder("worklode, another-project")
          .setValue(this.plugin.settings.projects)
          .onChange(async (value) => {
            this.plugin.settings.projects = value;
            await this.plugin.saveSettings();
          }),
      );

    new Setting(containerEl)
      .setName("Sync on startup")
      .setDesc("Run a sync automatically when Obsidian loads the plugin.")
      .addToggle((toggle) =>
        toggle.setValue(this.plugin.settings.syncOnStartup).onChange(async (value) => {
          this.plugin.settings.syncOnStartup = value;
          await this.plugin.saveSettings();
        }),
      );

    new Setting(containerEl)
      .setName("Sync interval (minutes)")
      .setDesc("0 = manual only (use the \"Worklode: Sync now\" command).")
      .addText((text) => {
        text.inputEl.type = "number";
        text
          .setPlaceholder("0")
          .setValue(String(this.plugin.settings.intervalMinutes))
          .onChange(async (value) => {
            const parsed = Number(value);
            this.plugin.settings.intervalMinutes = Number.isFinite(parsed) && parsed >= 0 ? Math.floor(parsed) : 0;
            await this.plugin.saveSettings();
            this.plugin.scheduleInterval();
          });
      });
  }
}

/** Confirms before the mirror takes over a mount root that already holds
 *  notes it did not write. Answers exactly once, on the first of confirm,
 *  cancel, or dismissal, so the caller's prompt flag is always cleared. */
class AdoptRootModal extends Modal {
  private answered = false;

  constructor(
    app: App,
    private readonly mountRoot: string,
    private readonly foreign: string[],
    private readonly onAnswer: (confirmed: boolean) => void,
  ) {
    super(app);
  }

  private answer(confirmed: boolean): void {
    if (this.answered) return;
    this.answered = true;
    this.onAnswer(confirmed);
  }

  override onOpen(): void {
    const { contentEl } = this;
    const shown = this.foreign.slice(0, 10);

    contentEl.createEl("h2", { text: `Let Worklode take over "${this.mountRoot}"?` });
    contentEl.createEl("p", {
      text:
        `"${this.mountRoot}" already contains ${this.foreign.length} note(s) this plugin did not write. ` +
        `The mount root is machine-managed: every sync deletes anything under it that Worklode does not ` +
        `currently mirror. Those notes would be moved to this vault's trash on the next sync.`,
    });
    contentEl.createEl("p", {
      text: "If this is not the folder you meant, cancel and change the mount root in plugin settings.",
    });

    const list = contentEl.createEl("ul");
    for (const path of shown) list.createEl("li", { text: path });
    if (this.foreign.length > shown.length) {
      list.createEl("li", { text: `... and ${this.foreign.length - shown.length} more` });
    }

    new Setting(contentEl)
      .addButton((btn) =>
        btn
          .setButtonText("Cancel")
          .setCta()
          .onClick(() => {
            this.answer(false);
            this.close();
          }),
      )
      .addButton((btn) =>
        btn
          .setButtonText("Take over the folder")
          .setWarning()
          .onClick(() => {
            this.answer(true);
            this.close();
          }),
      );
  }

  override onClose(): void {
    this.answer(false);
    this.contentEl.empty();
  }
}

/** Confirms before the purge command deletes the whole mount root. */
class PurgeConfirmModal extends Modal {
  constructor(
    app: App,
    private readonly mountRoot: string,
    private readonly onConfirm: () => void,
  ) {
    super(app);
  }

  override onOpen(): void {
    const { contentEl } = this;
    contentEl.createEl("h2", { text: "Purge the Worklode folder?" });
    contentEl.createEl("p", {
      text:
        `This deletes the entire "${this.mountRoot}" folder in this vault -- ` +
        `every file in it, including anything not created by this plugin. This cannot be undone.`,
    });

    new Setting(contentEl)
      .addButton((btn) => btn.setButtonText("Cancel").onClick(() => this.close()))
      .addButton((btn) =>
        btn
          .setButtonText("Purge")
          .setWarning()
          .onClick(() => {
            this.close();
            this.onConfirm();
          }),
      );
  }

  override onClose(): void {
    this.contentEl.empty();
  }
}
