import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { server } from "@/mocks/node"
import { renderWithRouter } from "@/test/render"
import { ProjectQuickLinks } from "./ProjectQuickLinks"

describe("ProjectQuickLinks", () => {
  it("shows the first four projects from the API, each linking to its overview", async () => {
    await renderWithRouter(<ProjectQuickLinks />)
    const links = await screen.findAllByRole("link")
    expect(links.map((link) => link.getAttribute("href"))).toEqual([
      "/projects/the-grid",
      "/projects/melting-glaciers",
      "/projects/night-shift",
      "/projects/urban-heat-islands",
    ])
    const gridHeading = screen.getByRole("heading", { name: "The Grid" })
    expect(gridHeading).toBeInTheDocument()
    // Scoped to the Grid card: "past due tasks" also appears on Urban Heat
    // Islands' card among these four projects.
    expect(
      within(gridHeading.closest("a")!).getByText("past due tasks", { exact: false }),
    ).toBeInTheDocument()
  })

  it("shows an error with Retry, and Retry recovers", async () => {
    const user = userEvent.setup()
    server.use(http.get("/api/projects", () => HttpResponse.json({ message: "down" }, { status: 500 }), { once: true }))
    await renderWithRouter(<ProjectQuickLinks />)

    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't load your projects.")
    await user.click(screen.getByRole("button", { name: "Retry" }))
    expect(await screen.findAllByRole("link")).toHaveLength(4)
  })
})
