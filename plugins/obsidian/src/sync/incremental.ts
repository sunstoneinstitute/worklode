// The incremental sync's decisions -- when to run one, whether a stored
// watermark still applies, and how far it has read. Pure functions, kept out
// of main.ts so they are testable without an Obsidian runtime.

import { computeEtag } from "../serialize/note";

export type SyncMode = "full" | "incremental";

/** Automatic ticks per full sync: the 5th tick is full, the four before it
 *  incremental. An incremental sync sees no deletions and refreshes no
 *  project or index note, so the full one is what prunes a deleted task's
 *  note and corrects the roll-ups -- this is how stale either may get. Five
 *  keeps that within a handful of intervals while still saving four of every
 *  five full re-fetches. */
export const FULL_SYNC_EVERY = 5;

/** The mode for the nth automatic tick, counting from 1. Plugin load and the
 *  "Sync now" command always sync fully and never come through here. */
export function syncModeForTick(tick: number): SyncMode {
  return tick % FULL_SYNC_EVERY === 0 ? "full" : "incremental";
}

/** Identity of the backbone a watermark was collected from, and of the folder
 *  it was mirrored into. A watermark is a position in one server's task
 *  history, read with one token's visibility, against notes in one mount
 *  root: change any of the three and it says nothing about what the vault
 *  holds, so the plugin stores this beside it and falls back to a full sync
 *  when it no longer matches.
 *
 *  Hashed with computeEtag rather than stored as JSON: the origin has to
 *  stay token-sensitive (a different token can see a different project set,
 *  so reusing a watermark across tokens would silently skip newly-visible
 *  tasks), but the plaintext token has no business sitting a second time in
 *  data.json next to `settings.token` under a key nobody scanning for
 *  secrets would think to check. computeEtag's canonical-JSON encoding of
 *  the triple keeps the same "two different settings cannot spell the same
 *  origin" guarantee the JSON encoding gave before. */
export async function syncOrigin(baseUrl: string, token: string, mountRoot: string): Promise<string> {
  return computeEtag([baseUrl, token, mountRoot]);
}

/** The highest `updated_at` across the current watermark and the tasks just
 *  fetched, as an RFC3339 UTC instant; "" when there is nothing to go on.
 *  Never moves backwards -- a response missing a task says nothing about the
 *  watermark, and the server compares with `>=` anyway.
 *
 *  Compared as instants, not strings: RFC3339 does not sort lexically across
 *  UTC offsets or fractional-second forms. Sub-millisecond precision is lost
 *  to Date, which truncates downwards, so the worst case is re-fetching one
 *  boundary row. A value that does not parse is ignored rather than allowed
 *  to poison the watermark. */
export function highestUpdatedAt(current: string, tasks: { updated_at: string }[]): string {
  let best = Number.NaN;
  for (const value of [current, ...tasks.map((t) => t.updated_at)]) {
    const ms = Date.parse(value);
    if (Number.isNaN(ms)) continue;
    if (Number.isNaN(best) || ms > best) best = ms;
  }
  return Number.isNaN(best) ? "" : new Date(best).toISOString();
}
