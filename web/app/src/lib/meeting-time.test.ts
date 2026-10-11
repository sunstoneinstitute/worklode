import { describe, expect, it } from "vitest"
import { formatMeetingTime } from "./meeting-time"

const NOW = new Date(2026, 8, 24, 8, 0) // Thu Sep 24 2026, 08:00

describe("formatMeetingTime", () => {
  it("says Today, with minutes only when they aren't :00", () => {
    expect(formatMeetingTime("2026-09-24T09:00:00", "2026-09-24T13:45:00", NOW)).toBe(
      "Today from 9am – 1:45pm",
    )
  })

  it("says Tomorrow for the next day", () => {
    expect(formatMeetingTime("2026-09-25T09:00:00", "2026-09-25T10:30:00", NOW)).toBe(
      "Tomorrow from 9am – 10:30am",
    )
  })

  it("uses the date further out", () => {
    expect(formatMeetingTime("2026-09-26T14:00:00", "2026-09-26T15:00:00", NOW)).toBe(
      "Sep 26 from 2pm – 3pm",
    )
  })
})
