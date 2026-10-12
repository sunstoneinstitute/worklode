import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { server } from "@/mocks/node"
import { api } from "./client"

describe("api", () => {
  it("a write carries the page header and a JSON content type", async () => {
    let seen: { xRequestedWith: string | null; contentType: string | null } | undefined
    server.use(
      http.post("/api/v1/projects", ({ request }) => {
        seen = {
          xRequestedWith: request.headers.get("X-Requested-With"),
          contentType: request.headers.get("Content-Type"),
        }
        return HttpResponse.json({ id: "p1" }, { status: 201 })
      }),
    )
    await api.POST("/api/v1/projects", { body: { name: "Test" } })
    expect(seen).toEqual({ xRequestedWith: "lode-web", contentType: "application/json" })
  })

  it("a GET carries neither header", async () => {
    let seen: { xRequestedWith: string | null; contentType: string | null } | undefined
    server.use(
      http.get("/api/v1/projects", ({ request }) => {
        seen = {
          xRequestedWith: request.headers.get("X-Requested-With"),
          contentType: request.headers.get("Content-Type"),
        }
        return HttpResponse.json([])
      }),
    )
    await api.GET("/api/v1/projects")
    expect(seen).toEqual({ xRequestedWith: null, contentType: null })
  })
})
