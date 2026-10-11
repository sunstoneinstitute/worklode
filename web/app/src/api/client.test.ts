import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { server } from "@/mocks/node"
import { ApiError, apiFetch } from "./client"

describe("apiFetch", () => {
  it("resolves a relative path and parses JSON", async () => {
    server.use(http.get("/api/ping", () => HttpResponse.json({ ok: true })))
    await expect(apiFetch("/api/ping")).resolves.toEqual({ ok: true })
  })

  it("sends a JSON body with its method", async () => {
    server.use(
      http.post("/api/echo", async ({ request }) =>
        HttpResponse.json({
          contentType: request.headers.get("Content-Type"),
          body: await request.json(),
        }),
      ),
    )
    await expect(apiFetch("/api/echo", { method: "POST", body: { a: 1 } })).resolves.toEqual({
      contentType: "application/json",
      body: { a: 1 },
    })
  })

  it("returns undefined for 204 No Content", async () => {
    server.use(http.delete("/api/thing", () => new HttpResponse(null, { status: 204 })))
    await expect(apiFetch("/api/thing", { method: "DELETE" })).resolves.toBeUndefined()
  })

  it("throws ApiError with the server's message and field errors", async () => {
    server.use(
      http.patch("/api/thing", () =>
        HttpResponse.json({ message: "Invalid", errors: { status: "Unknown status" } }, { status: 422 }),
      ),
    )
    const error = await apiFetch("/api/thing", { method: "PATCH", body: {} }).catch((caught) => caught)
    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ status: 422, message: "Invalid", errors: { status: "Unknown status" } })
  })

  it("throws ApiError with a fallback message when the body's JSON is null", async () => {
    server.use(http.get("/api/broken-null", () => HttpResponse.json(null, { status: 500 })))
    const error = (await apiFetch("/api/broken-null").catch((caught) => caught)) as ApiError
    expect(error).toBeInstanceOf(ApiError)
    expect(error.status).toBe(500)
    expect(error.message).toMatch(/500/)
  })

  it("throws ApiError with a fallback message when the body isn't JSON", async () => {
    server.use(http.get("/api/broken", () => new HttpResponse("oops", { status: 500 })))
    // Cast needed: apiFetch's unresolved `T` makes the promise `unknown`,
    // and TS keeps `.catch()`'s result `unknown` too (it doesn't widen to
    // `any` the way it does for a concretely-typed source promise).
    const error = (await apiFetch("/api/broken").catch((caught) => caught)) as ApiError
    expect(error).toBeInstanceOf(ApiError)
    expect(error.status).toBe(500)
    expect(error.message).toMatch(/500/)
  })
})
