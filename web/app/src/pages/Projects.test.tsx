import { screen, within } from "@testing-library/react"
import { http, HttpResponse } from "msw"
import { describe, expect, it, vi } from "vitest"
import { browser } from "@/api/queries"
import { server } from "@/mocks/node"
import { renderApp } from "@/test/render"

describe("/projects", () => {
  it("lists the projects' names and keys", async () => {
    await renderApp("/projects")
    const table = within(await screen.findByRole("table"))
    expect(table.getByRole("link", { name: "Worklode" })).toHaveAttribute("href", "/projects/p1")
    expect(table.getByRole("link", { name: "Data platform" })).toHaveAttribute("href", "/projects/p2")
    expect(table.getByText("WL")).toBeInTheDocument()
    expect(table.getByText("DP")).toBeInTheDocument()
    expect(table.getByText("sunstoneinstitute/worklode, sunstoneinstitute/worklode-design")).toBeInTheDocument()
  })

  it("sends a 401 from whoami to the login page, coming back here", async () => {
    const assign = vi.spyOn(browser, "assign").mockImplementation(() => {})
    server.use(http.get("/api/v1/whoami", () => HttpResponse.json({ error: "unauthorized" }, { status: 401 })))
    window.history.replaceState(null, "", "/projects?sort=name.asc")

    await renderApp("/projects?sort=name.asc")

    expect(assign).toHaveBeenCalledWith(`/auth/login?next=${encodeURIComponent("/projects?sort=name.asc")}`)
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
    assign.mockRestore()
    window.history.replaceState(null, "", "/")
  })
})
