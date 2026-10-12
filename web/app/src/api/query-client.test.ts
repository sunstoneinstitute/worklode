import { describe, expect, it } from "vitest"
import { HttpError } from "./queries"
import { retryRead } from "./query-client"

describe("retryRead", () => {
  it("retries a 5xx or a network failure once", () => {
    expect(retryRead(0, new HttpError(503, "down"))).toBe(true)
    expect(retryRead(0, new TypeError("Failed to fetch"))).toBe(true)
    expect(retryRead(1, new HttpError(503, "down"))).toBe(false)
  })

  it("never retries a 4xx", () => {
    expect(retryRead(0, new HttpError(404, "not found"))).toBe(false)
  })
})
