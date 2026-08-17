import { describe, expect, it } from "vitest";
import { FULL_SYNC_EVERY, highestUpdatedAt, syncModeForTick, syncOrigin } from "../src/sync/incremental";

describe("syncModeForTick", () => {
  it("runs a full sync on every FULL_SYNC_EVERY-th automatic tick", async () => {
    const modes = Array.from({ length: 2 * FULL_SYNC_EVERY }, (_, i) => syncModeForTick(i + 1));

    expect(modes.filter((m) => m === "full")).toHaveLength(2);
    expect(modes[FULL_SYNC_EVERY - 1]).toBe("full");
    expect(modes[2 * FULL_SYNC_EVERY - 1]).toBe("full");
    expect(modes[0]).toBe("incremental");
  });
});

describe("syncOrigin", () => {
  it("changes with the server, the token, or the mount root", async () => {
    const base = syncOrigin("https://lode.example.com", "wl_abc", "Worklode");

    expect(syncOrigin("https://lode.example.com", "wl_abc", "Worklode")).toBe(base);
    expect(syncOrigin("https://other.example.com", "wl_abc", "Worklode")).not.toBe(base);
    expect(syncOrigin("https://lode.example.com", "wl_other", "Worklode")).not.toBe(base);
    expect(syncOrigin("https://lode.example.com", "wl_abc", "Team/Worklode")).not.toBe(base);
  });

  it("does not confuse two settings that concatenate to the same string", async () => {
    expect(syncOrigin("https://lode.example.com", "wl_a", "bWorklode")).not.toBe(
      syncOrigin("https://lode.example.com", "wl_ab", "Worklode"),
    );
  });
});

describe("highestUpdatedAt", () => {
  it("takes the highest of the current watermark and the fetched tasks", async () => {
    const tasks = [{ updated_at: "2026-08-14T12:00:00Z" }, { updated_at: "2026-08-16T09:12:00Z" }];

    expect(highestUpdatedAt("", tasks)).toBe("2026-08-16T09:12:00.000Z");
    expect(highestUpdatedAt("2026-08-15T00:00:00Z", tasks)).toBe("2026-08-16T09:12:00.000Z");
  });

  it("never moves backwards, even on an empty response", async () => {
    expect(highestUpdatedAt("2026-08-17T00:00:00Z", [])).toBe("2026-08-17T00:00:00.000Z");
    expect(highestUpdatedAt("2026-08-17T00:00:00Z", [{ updated_at: "2026-08-01T00:00:00Z" }])).toBe(
      "2026-08-17T00:00:00.000Z",
    );
  });

  it("compares instants, not strings", async () => {
    // Lexically "…09:12:00+02:00" sorts above "…09:12:00Z", but it is two
    // hours earlier; and a fractional second sorts below a whole one.
    expect(highestUpdatedAt("2026-08-16T09:12:00Z", [{ updated_at: "2026-08-16T09:12:00+02:00" }])).toBe(
      "2026-08-16T09:12:00.000Z",
    );
    expect(highestUpdatedAt("2026-08-16T09:12:00Z", [{ updated_at: "2026-08-16T09:12:00.5Z" }])).toBe(
      "2026-08-16T09:12:00.500Z",
    );
  });

  it("ignores a value it cannot parse rather than poisoning the watermark", async () => {
    expect(highestUpdatedAt("2026-08-16T09:12:00Z", [{ updated_at: "never" }])).toBe("2026-08-16T09:12:00.000Z");
    expect(highestUpdatedAt("", [{ updated_at: "never" }])).toBe("");
  });
});
