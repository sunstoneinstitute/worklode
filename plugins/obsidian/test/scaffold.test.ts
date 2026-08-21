import { describe, expect, it } from "vitest";
import manifest from "../manifest.json";
import pkg from "../package.json";

describe("manifest.json", () => {
  it("has the fixed plugin id and stays in sync with package.json's version", () => {
    expect(manifest.id).toBe("worklode");
    expect(manifest.version).toBe(pkg.version);
  });
});
