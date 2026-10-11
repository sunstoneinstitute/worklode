import { screen, within } from "@testing-library/react"
import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { server } from "@/mocks/node"
import { renderApp } from "@/test/render"

describe("current user", () => {
  it("greets and shows the avatar of the user from /api/me", async () => {
    server.use(http.get("/api/me", () => HttpResponse.json({ id: "u2", name: "Marcus Chen" })))
    await renderApp("/")
    expect(screen.getAllByRole("heading", { name: "Welcome, @MarcusChen!" }).length).toBeGreaterThan(0)
    expect(within(screen.getByRole("banner")).getByText("MC")).toBeInTheDocument()
  })
})
