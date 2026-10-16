import { describe, expect, it } from "vitest"
import { dueLabel } from "./due-label"

// Late in the evening, so a 24-hour count would already be wrong for "tomorrow"
const TODAY = new Date(2026, 8, 25, 23, 30)

describe("dueLabel", () => {
  it("counts past due dates in days late", () => {
    expect(dueLabel("2026-09-24", TODAY)).toBe("1 day late")
    expect(dueLabel("2026-09-23", TODAY)).toBe("2 days late")
  })

  it("says Due today for today", () => {
    expect(dueLabel("2026-09-25", TODAY)).toBe("Due today")
  })

  it("counts calendar days, not 24-hour periods", () => {
    expect(dueLabel("2026-09-26", TODAY)).toBe("1 day left")
    expect(dueLabel("2026-09-26", new Date(2026, 8, 25, 0, 5))).toBe("1 day left")
  })

  it("uses days up to six, then whole weeks", () => {
    expect(dueLabel("2026-10-01", TODAY)).toBe("6 days left")
    expect(dueLabel("2026-10-02", TODAY)).toBe("1 week left")
    expect(dueLabel("2026-10-09", TODAY)).toBe("2 weeks left")
    expect(dueLabel("2026-10-16", TODAY)).toBe("3 weeks left")
  })

  it("is empty without a due date", () => {
    expect(dueLabel("", TODAY)).toBe("")
  })
})
