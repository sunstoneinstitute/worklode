import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { renderWithRouter } from "@/test/render"
import { ProjectList } from "./ProjectList"

function rowTitles() {
  const [, ...bodyRows] = screen.getAllByRole("row")
  return bodyRows.map((row) => within(row).getAllByRole("cell")[0].textContent)
}

describe("ProjectList", () => {
  it("renders the Projects heading and a New Project button", async () => {
    await renderWithRouter(<ProjectList />, { path: "/projects" })

    expect(screen.getByRole("heading", { name: "Projects" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "New Project" })).toBeInTheDocument()
  })

  it("has Title, Author, Type, Status and Submitted columns, in that order", async () => {
    await renderWithRouter(<ProjectList />, { path: "/projects" })

    const headers = screen.getAllByRole("columnheader").map((header) => header.textContent)
    expect(headers.slice(0, 5)).toEqual(["Title", "Author", "Type", "Status", "Submitted"])
  })

  it("links each title to the project overview, and still sorts by title", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<ProjectList />, { path: "/projects" })

    expect(screen.getByRole("link", { name: "The Grid" })).toHaveAttribute(
      "href",
      "/projects/the-grid",
    )

    await user.click(screen.getByRole("columnheader", { name: /Title/ }))
    expect(rowTitles()).toEqual([
      "Grid outages, block by block",
      "Melting Glaciers",
      "Night shift at the reservoir",
      "The Grid",
      "Urban heat islands",
    ])
  })

  it("gives every row edit and delete actions only", async () => {
    await renderWithRouter(<ProjectList />, { path: "/projects" })

    const [firstRow] = screen.getAllByRole("row").slice(1)
    const actions = within(firstRow)
      .getAllByRole("button")
      .map((button) => button.getAttribute("aria-label"))
      .filter((label) => label?.endsWith(" project"))
    expect(actions).toEqual(["Edit project", "Delete project"])
  })

  it("offers the two story types", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<ProjectList />, { path: "/projects" })

    const header = screen.getByRole("columnheader", { name: /Type/ })
    await user.click(within(header).getByRole("button", { name: /filter/i }))

    const options = screen.getAllByRole("option").map((option) => option.textContent)
    expect(options).toEqual(["All", "Data Story", "Short Story"])
  })

  it("filters projects by status", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<ProjectList />, { path: "/projects" })

    const header = screen.getByRole("columnheader", { name: /Status/ })
    await user.click(within(header).getByRole("button", { name: /filter/i }))
    await user.click(screen.getByRole("option", { name: "Accepted" }))

    expect(rowTitles()).toEqual(["The Grid", "Melting Glaciers"])
  })

  it("lets you pick the author from the crew", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<ProjectList />, { path: "/projects" })

    const row = screen.getByRole("row", { name: /Urban heat islands/ })
    const authorCell = row.querySelector('[data-column-id="author"]') as HTMLElement
    await user.click(within(authorCell).getByRole("button"))
    await user.click(screen.getByRole("option", { name: /@PriyaPatel/ }))

    expect(within(authorCell).getByText("@PriyaPatel")).toBeInTheDocument()
  })
})
