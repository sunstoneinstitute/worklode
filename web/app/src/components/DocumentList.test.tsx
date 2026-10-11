import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { renderWithRouter } from "@/test/render"
import { DocumentList } from "./DocumentList"

const DOCUMENTS_PATH = "/projects/the-grid/documents"

function rowTitles() {
  const [, ...bodyRows] = screen.getAllByRole("row")
  return bodyRows.map((row) => within(row).getAllByRole("cell")[0].textContent)
}

describe("DocumentList", () => {
  it("renders the Documents heading and a chip for every category", async () => {
    await renderWithRouter(<DocumentList />, { path: DOCUMENTS_PATH })

    expect(screen.getByRole("heading", { name: "Documents" })).toBeInTheDocument()
    const chips = within(screen.getByRole("group", { name: "Document category" }))
      .getAllByRole("button")
      .map((chip) => chip.textContent)
    expect(chips).toEqual(["Spec", "Plan", "Meeting Notes", "Deliverables", "Sources"])
  })

  it("starts on Spec, showing only spec documents", async () => {
    await renderWithRouter(<DocumentList />, { path: DOCUMENTS_PATH })

    expect(screen.getByRole("button", { name: "Spec" })).toHaveAttribute("aria-pressed", "true")
    expect(rowTitles()).toEqual(["Project spec", "Data requirements"])
  })

  it("switches the table to another category from its chip", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<DocumentList />, { path: DOCUMENTS_PATH })

    await user.click(screen.getByRole("button", { name: "Meeting Notes" }))

    expect(screen.getByRole("button", { name: "Meeting Notes" })).toHaveAttribute("aria-pressed", "true")
    expect(screen.getByRole("button", { name: "Spec" })).toHaveAttribute("aria-pressed", "false")
    expect(rowTitles()).toEqual(["Kickoff meeting", "Weekly sync, Sep 15"])
  })

  function headerNames() {
    return screen.getAllByRole("columnheader").map((header) => header.textContent)
  }

  function rowActions() {
    const [firstRow] = screen.getAllByRole("row").slice(1)
    return within(firstRow)
      .getAllByRole("button")
      .map((button) => button.getAttribute("aria-label"))
      .filter((label) => /^(Download|Edit|Review|Delete) /.test(label ?? ""))
  }

  it("shows Name, Type and Status, with Edit and Delete, on most categories", async () => {
    await renderWithRouter(<DocumentList />, { path: DOCUMENTS_PATH })

    expect(headerNames()).toEqual(["Name", "Type", "Status", ""])
    expect(rowActions()).toEqual(["Edit document", "Delete document"])
  })

  it("adds Date and Participants on Meeting Notes", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<DocumentList />, { path: DOCUMENTS_PATH })

    await user.click(screen.getByRole("button", { name: "Meeting Notes" }))

    expect(headerNames()).toEqual(["Name", "Type", "Status", "Date", "Participants", ""])
    expect(screen.getByText("Aug 2, 2026")).toBeInTheDocument()
    expect(
      screen.getByRole("button", { name: "Participants: @Claudicia, @PriyaPatel" }),
    ).toBeInTheDocument()
  })

  it("picks several participants at once, and filters by any of them", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<DocumentList />, { path: DOCUMENTS_PATH })
    await user.click(screen.getByRole("button", { name: "Meeting Notes" }))

    await user.click(screen.getByRole("button", { name: "Participants: @Claudicia, @PriyaPatel" }))
    await user.click(screen.getByRole("option", { name: /@TobiasSmith/ }))
    await user.keyboard("{Escape}")
    expect(
      screen.getByRole("button", { name: "Participants: @Claudicia, @PriyaPatel, @TobiasSmith" }),
    ).toBeInTheDocument()

    const header = screen.getByRole("columnheader", { name: /Participants/ })
    await user.click(within(header).getByRole("button", { name: /filter/i }))
    await user.click(screen.getByRole("option", { name: "@MarcusChen" }))
    expect(rowTitles()).toEqual(["Kickoff meeting"])
  })

  it("adds Source and a Download action on Sources", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<DocumentList />, { path: DOCUMENTS_PATH })

    await user.click(screen.getByRole("button", { name: "Sources" }))

    expect(headerNames()).toEqual(["Name", "Type", "Status", "Source", ""])
    expect(screen.getByText("EIA Form 861")).toBeInTheDocument()
    expect(rowActions()).toEqual(["Download source", "Edit document", "Delete document"])
  })

  it("adds Milestone, Due Date and a Review action on Deliverables", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<DocumentList />, { path: DOCUMENTS_PATH })

    await user.click(screen.getByRole("button", { name: "Deliverables" }))

    expect(headerNames()).toEqual(["Name", "Type", "Status", "Milestone", "Due Date", ""])
    expect(screen.getByText("First draft")).toBeInTheDocument()
    expect(screen.getByText("Oct 5, 2026")).toBeInTheDocument()
    expect(rowActions()).toEqual(["Edit document", "Review deliverable", "Delete document"])
  })

  it("filters by Type and Status, but not by Name", async () => {
    await renderWithRouter(<DocumentList />, { path: DOCUMENTS_PATH })

    for (const label of ["Type", "Status"]) {
      const header = screen.getByRole("columnheader", { name: new RegExp(label) })
      expect(within(header).getByRole("button", { name: /filter/i })).toBeInTheDocument()
    }
    const name = screen.getByRole("columnheader", { name: /Name/ })
    expect(within(name).queryByRole("button", { name: /filter/i })).not.toBeInTheDocument()
  })

  it("adds a new document to the category showing, and keeps it after switching away and back", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<DocumentList />, { path: DOCUMENTS_PATH })

    await user.click(screen.getByRole("button", { name: "Sources" }))
    await user.click(screen.getByRole("button", { name: "New Document" }))
    expect(rowTitles().at(-1)).toBe("Untitled document")

    await user.click(screen.getByRole("button", { name: "Spec" }))
    expect(rowTitles()).not.toContain("Untitled document")
    await user.click(screen.getByRole("button", { name: "Sources" }))
    expect(rowTitles()).toContain("Untitled document")
  })
})
