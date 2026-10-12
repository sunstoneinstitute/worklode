import { screen, within } from "@testing-library/react"
import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { server } from "@/mocks/node"
import { renderApp } from "@/test/render"

describe("current actor", () => {
  it("shows the avatar of the actor from whoami", async () => {
    server.use(http.get("/api/v1/whoami", () => HttpResponse.json({ id: "marcus", kind: "human", admin: false })))
    await renderApp("/projects")
    expect(within(screen.getByRole("banner")).getByText("MA")).toBeInTheDocument()
  })
})
