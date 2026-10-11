import { describe, expect, it } from "vitest"
import { initials } from "./initials"

describe("initials", () => {
  it("uses first and last initials for multi-part names", () => {
    expect(initials("Tobias Smith")).toBe("TS")
    expect(initials("Mary Jane Watson")).toBe("MW")
  })

  it("uses the first two letters of a single name", () => {
    expect(initials("Claudicia")).toBe("CL")
  })

  it("handles accented letters, stray whitespace, and empty names", () => {
    expect(initials("  José  Núñez ")).toBe("JN")
    expect(initials("")).toBe("")
  })
})
