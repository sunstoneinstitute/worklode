// The Obsidian-bound shell: settings, commands, status bar, and the glue
// between the worklode HTTP API and the pure sync/serialize logic in
// src/sync/mirror.ts and src/serialize/note.ts. Together with
// src/vault/writer.ts, this is the only file in the plugin that imports
// "obsidian" -- everything it drives stays unit-testable without one.

import { App, Modal, Notice, Plugin, PluginSettingTab, Setting, debounce, requestUrl } from "obsidian";
import { WorklodeApiError, WorklodeClient, type HttpTransport } from "./api/client";
import type { ProjectMembers } from "./serialize/note";
import {
  FULL_SYNC_EVERY,
  highestUpdatedAt,
  syncModeForTick,
  syncOrigin,
  type SyncMode,
} from "./sync/incremental";
import {
  applyMirror,
  desiredNotes,
  desiredTaskNotes,
  foreignNotes,
  hydrateDocBodies,
  isSafeMountRoot,
  mountRootParent,
  mountRootParentMissing,
  type HydratedDocs,
  type MirrorStats,
} from "./sync/mirror";
import { writeBackTaskNotes, type WriteBackStats } from "./sync/writeback";
import { ObsidianVaultWriter } from "./vault/writer";

interface WorklodeSettings {
  baseUrl: string; // "" until configured
  token: string; // wl_<40 hex>
  mountRoot: string; // default "Worklode"; may be nested, e.g. "Team/Worklode"
  projects: string; // comma-separated allow-list; "" means all
  syncOnStartup: boolean; // default false
  intervalMinutes: number; // 0 = manual only; default 0
  /** Push a task note's edited body back to the backbone. Default false: it
   *  turns the mount root from machine-owned into jointly written, which is
   *  the user's decision to make, not a default to inherit. Full syncs only --
   *  an incremental one holds only the tasks that changed and cannot classify
   *  a note it did not fetch. */
  writeBack: boolean;
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
  writeBack: false,
  adoptedRoots: [],
};

/** How far the mirror has read, persisted next to the settings in data.json.
 *  Not a setting: nothing here is user-editable, and it is meaningless on its
 *  own. */
interface MirrorState {
  /** Highest task `updated_at` seen across every synced project, RFC3339.
   *  "" means "nothing read yet", which forces a full sync. */
  watermark: string;
  /** syncOrigin() of the settings the watermark was collected under. */
  origin: string;
}

const DEFAULT_STATE: MirrorState = { watermark: "", origin: "" };

/** The shape of data.json: the settings plus the mirror state under its own
 *  key, so a state field can never be mistaken for a setting. */
type PersistedData = Partial<WorklodeSettings> & { mirrorState?: Partial<MirrorState> };

/** How long the mount-root field waits after the last keystroke before it
 *  saves. The tab otherwise saves on every character, so with an interval
 *  armed a sync could fire against a transient prefix of the name being
 *  typed -- "Not" on the way to "Notes". */
const MOUNT_ROOT_SAVE_DELAY_MS = 800;

/** requestUrl bypasses CORS, which a plain fetch from the renderer does not.
 *  throw:false hands control of non-2xx handling to WorklodeClient, which
 *  turns it into a WorklodeApiError with a legible message. */
const obsidianHttp: HttpTransport = async (req) => {
  const res = await requestUrl({
    url: req.url,
    method: req.method,
    headers: req.headers,
    body: req.body,
    throw: false,
  });
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

  private state: MirrorState = { ...DEFAULT_STATE };
  /** Automatic ticks since load; every FULL_SYNC_EVERY-th one syncs fully. */
  private tick = 0;
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
  /** The root a create-parent prompt is currently open for, mirroring
   *  adoptPromptFor for the same reason: an interval sync must not stack a
   *  second modal on top of one already open. */
  private parentPromptFor: string | undefined;

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
    const { mirrorState, ...settings } = ((await this.loadData()) as PersistedData | null) ?? {};
    this.settings = { ...DEFAULT_SETTINGS, ...settings };
    this.state = { ...DEFAULT_STATE, ...mirrorState };
  }

  /** Writes settings and mirror state together: they share one data.json and
   *  saveData replaces the whole file, so persisting either alone would drop
   *  the other. */
  async saveSettings(): Promise<void> {
    await this.saveData({ ...this.settings, mirrorState: this.state } satisfies PersistedData);
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
            // Automatic ticks alternate: mostly incremental, fully every
            // FULL_SYNC_EVERY-th. The counter survives a settings edit, which
            // only re-arms the timer.
            void this.sync(syncModeForTick(++this.tick));
          },
          this.settings.intervalMinutes * 60 * 1000,
        ),
      );
    }
  }

  /** Fetches projects, filters by the allow-list, fetches each project's
   *  tasks and docs, diffs against the vault, and applies the difference.
   *  Guarded against overlapping runs: a slow sync plus a short interval
   *  must not stack.
   *
   *  An "incremental" run asks only for the tasks changed since the stored
   *  watermark, writes only task notes, and deletes nothing -- see
   *  desiredTaskNotes and MirrorOptions for why both restrictions are
   *  required rather than an optimisation. It degrades to a full sync
   *  whenever the watermark cannot be trusted.
   *
   *  A full run with write-back enabled first pushes any locally edited task
   *  body (src/sync/writeback.ts) and renders from what the backbone answered
   *  with. */
  async sync(mode: SyncMode = "full"): Promise<void> {
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
      if (!(await this.ensureParentConfirmed(mountRoot))) return;
      if (!(await this.ensureRootAdopted(mountRoot))) return;

      // A watermark is a position in one server's task history, read with one
      // token, against notes in one folder: a first run has none, and a
      // changed server, token or mount root invalidates the one it has.
      const origin = await syncOrigin(baseUrl, token, mountRoot);
      const incremental = mode === "incremental" && this.state.watermark !== "" && this.state.origin === origin;

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

      // A server with no docs endpoint costs the doc notes and nothing else:
      // projects and tasks still mirror, the notice says docs were skipped,
      // and the delete pass is told not to prune doc notes it never
      // enumerated. Every other failure still fails the sync.
      let docsUnavailable = false;
      const byProject = new Map<string, ProjectMembers>();
      for (const project of selected) {
        if (incremental) {
          // Tasks only, and only the changed ones: an incremental run writes
          // no doc note, so fetching the documents would be wasted bytes.
          byProject.set(project.id, { docs: [], tasks: await client.listTasks(project.id, this.state.watermark) });
          continue;
        }
        const [tasks, docs] = await Promise.all([
          client.listTasks(project.id),
          client.listDocsIfPresent(project.id),
        ]);
        if (docs === undefined) docsUnavailable = true;
        byProject.set(project.id, { docs: docs ?? [], tasks });
      }

      // The doc list carries no bodies at all, so the text every doc note
      // shows comes from a second request per document -- spent only on the
      // documents whose vault note is out of date. An incremental run renders
      // no doc note and fetched no doc list, so it has nothing to hydrate.
      let members = byProject;
      let docs: HydratedDocs | undefined;
      if (!incremental) {
        docs = await hydrateDocBodies(this.writer, mountRoot, byProject, (id) => client.getDoc(id));
        members = docs.byProject;
      }

      // Write-back runs before the notes are rendered, so the render sees the
      // bodies the backbone just accepted rather than the ones it was fetched
      // with. Full syncs only: an incremental run holds only the tasks that
      // changed, and a note it did not fetch cannot be classified.
      let toRender = members;
      let writeBack: WriteBackStats | undefined;
      if (this.settings.writeBack && !incremental) {
        const pushed = await writeBackTaskNotes(
          this.writer,
          mountRoot,
          members,
          (id, body) => client.patchTaskBody(id, body),
          new Date().toISOString(),
        );
        toRender = pushed.byProject;
        writeBack = pushed.stats;
      }

      const stats = incremental
        ? await applyMirror(this.writer, mountRoot, await desiredTaskNotes(selected, toRender), {
            pruneDocNotes: false,
            pruneTaskNotes: false,
            pruneOtherNotes: false,
          })
        : await applyMirror(
            this.writer,
            mountRoot,
            await desiredNotes(selected, toRender, mountRoot, new Date().toISOString()),
            { pruneDocNotes: !docsUnavailable, alreadyCurrent: docs?.unfetched },
          );

      await this.advanceWatermark(origin, toRender);
      this.reportSuccess(stats, docsUnavailable, incremental, writeBack, docs?.fetched ?? 0);
    } catch (err) {
      this.reportFailure(err);
    } finally {
      this.busy = false;
    }
  }

  /** The typo guard. write()'s ensureDir creates every missing ancestor of a
   *  note's path, the mount root's own parent included -- so a mistyped
   *  parent segment ("Tema/Worklode" for "Team/Worklode") would otherwise
   *  create a stray folder and mirror into it silently, rather than
   *  failing. WL-82's adopt prompt (ensureRootAdopted, below) does not catch
   *  this: an absent root has no foreign notes under it, so it is the
   *  correct case for a silent take-over. This is the one case that isn't --
   *  the root's own *parent* is missing -- asked about before that check
   *  runs. Returns whether the sync may proceed now; a confirmed prompt
   *  starts its own. */
  private async ensureParentConfirmed(mountRoot: string): Promise<boolean> {
    if (!(await mountRootParentMissing(this.writer, mountRoot))) return true;
    if (this.parentPromptFor !== undefined) return false;

    const parent = mountRootParent(mountRoot);
    if (parent === undefined) return true; // unreachable: mountRootParentMissing already checked this

    this.parentPromptFor = mountRoot;
    new CreateParentModal(this.app, parent, (confirmed) => {
      this.parentPromptFor = undefined;
      if (!confirmed) return;
      void this.sync();
    }).open();
    return false;
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

  /** Records how far this sync read, so the next incremental one can ask for
   *  what changed after it. It only ever moves forward -- see
   *  highestUpdatedAt -- except when the settings now point somewhere else,
   *  where the stored watermark belongs to another backbone and is dropped
   *  rather than carried over. */
  private async advanceWatermark(origin: string, byProject: Map<string, ProjectMembers>): Promise<void> {
    const from = origin === this.state.origin ? this.state.watermark : "";
    const watermark = highestUpdatedAt(from, [...byProject.values()].flatMap((m) => m.tasks));
    if (watermark === this.state.watermark && origin === this.state.origin) return;
    this.state = { watermark, origin };
    await this.saveSettings();
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

  private reportSuccess(
    stats: MirrorStats,
    docsUnavailable: boolean,
    incremental: boolean,
    writeBack: WriteBackStats | undefined,
    docsFetched: number,
  ): void {
    const noteCount = stats.written + stats.skipped;
    // An incremental run saw only the changed tasks, so its count is not the
    // vault's note count and must not read like one.
    const seen = incremental ? `${noteCount} changed` : `${noteCount} notes`;
    this.statusBarItem.setText(`Worklode: ${seen} · ${formatTime(new Date())}`);

    const kind = incremental ? "incremental sync" : "sync";
    let message = `Worklode ${kind}: ${stats.written} written, ${stats.skipped} unchanged, ${stats.removed} removed.`;
    // Once per sync, not once per project: the endpoint is a property of the
    // server, and the user needs to know the empty docs folder means "this
    // server has no docs endpoint", not "this project has no docs".
    if (docsUnavailable) {
      message += " Doc notes skipped and left as they were: this server has no /api/v1/docs endpoint.";
    }
    // The honest explanation for a slow first sync: a document's text costs a
    // request of its own, so a corpus arriving for the first time is one per
    // document, and an unchanged one afterwards is none.
    if (docsFetched > 0) {
      message += ` Fetched ${docsFetched} document body/bodies.`;
    }
    // Write-back gets its own clause: "pushed" and "written" count opposite
    // directions, and a user who edited a note needs to see where their edit
    // went without opening the console.
    if (writeBack && (writeBack.pushed > 0 || writeBack.conflicts.length > 0)) {
      message += ` Write-back: ${writeBack.pushed} edit(s) pushed, ${writeBack.conflicted} conflict note(s) saved.`;
      // An edit that was neither pushed nor turned into a conflict note --
      // a refused PATCH, or a note that no longer parses -- is still on disk.
      const left = writeBack.conflicts.length - writeBack.conflicted;
      if (left > 0) message += ` ${left} edit(s) left in place.`;
      if (writeBack.conflicts.length > 0) console.warn("Worklode write-back:", writeBack.conflicts);
    }
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
      .setName("Write edits back")
      .setDesc(
        "Off by default. When on, an edit to a task note's body is pushed to worklode on " +
          'the next full sync ("Worklode: Sync now", startup, or every ' +
          `${FULL_SYNC_EVERY}th automatic sync). Only the body: everything else a note shows ` +
          "is worklode's and is restored. If the task also changed in worklode, worklode wins " +
          'and your text is saved under "_conflicts".',
      )
      .addToggle((toggle) =>
        toggle.setValue(this.plugin.settings.writeBack).onChange(async (value) => {
          this.plugin.settings.writeBack = value;
          await this.plugin.saveSettings();
        }),
      );

    new Setting(containerEl)
      .setName("Sync interval (minutes)")
      .setDesc(
        '0 = manual only (use the "Worklode: Sync now" command). ' +
          `Every ${FULL_SYNC_EVERY}th automatic sync is a full one; the others fetch only what changed.`,
      )
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

/** Confirms before write()'s ensureDir would silently create the mount
 *  root's own missing parent folder -- the one thing WL-82's adopt prompt
 *  cannot catch, since an absent root has no foreign notes under it and is
 *  always safe to take over. Same shape as AdoptRootModal: answers exactly
 *  once, on the first of confirm, cancel, or dismissal. */
class CreateParentModal extends Modal {
  private answered = false;

  constructor(
    app: App,
    private readonly parent: string,
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

    contentEl.createEl("h2", { text: `"${this.parent}" does not exist -- create it?` });
    contentEl.createEl("p", {
      text:
        `The mount root's parent folder, "${this.parent}", was not found in this vault. ` +
        `If the mount root setting has a typo, cancel and fix it there. Otherwise, Worklode ` +
        `will create "${this.parent}" and sync into it.`,
    });

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
          .setButtonText(`Create "${this.parent}"`)
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
