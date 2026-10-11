import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it } from "vitest"
import { resetIdeas } from "@/lib/ideas"
import { renderWithRouter } from "@/test/render"
import { IdeaList } from "./IdeaList"

function rowTitles() {
  const [, ...bodyRows] = screen.getAllByRole("row")
  return bodyRows.map((row) => within(row).getAllByRole("cell")[0].textContent)
}

describe("IdeaList", () => {
  // The ideas store is shared module state — start each test from the samples
  beforeEach(() => resetIdeas())

  it("opens the New Idea form in a panel, and adds the submitted idea to the table", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<IdeaList />, { path: "/ideas" })

    await user.click(screen.getByRole("button", { name: "New Idea" }))
    const panel = screen.getByRole("dialog", { name: "New Idea" })
    // Type first, then the form
    await user.click(within(panel).getByRole("button", { name: "General Idea" }))
    await user.type(within(panel).getByRole("textbox", { name: "Title" }), "Night buses")
    await user.click(within(panel).getByRole("button", { name: "Submit" }))

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    expect(rowTitles()).toContain("Night buses")
  })

  it("starts on All, and narrows to one idea type from its chip", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<IdeaList />, { path: "/ideas" })

    const chips = within(screen.getByRole("group", { name: "Idea type" })).getAllByRole("button")
    expect(chips.map((chip) => chip.textContent)).toEqual([
      "All",
      "General Ideas",
      "Story Pitches",
    ])
    expect(screen.getByRole("button", { name: "All" })).toHaveAttribute("aria-pressed", "true")
    expect(rowTitles()).toHaveLength(4)

    await user.click(screen.getByRole("button", { name: "Story Pitches" }))
    expect(rowTitles()).toEqual([
      "Where the glaciers went",
      "The ferryman's last season",
    ])
    // The chip stands in for the Type column; Story Type stays, since it applies
    const headers = screen.getAllByRole("columnheader").map((header) => header.textContent)
    expect(headers.slice(0, 5)).toEqual(["Title", "Author", "Story Type", "Status", "Submitted"])

    await user.click(screen.getByRole("button", { name: "General Ideas" }))
    expect(rowTitles()).toEqual(["Reusable map legend", "Learn D3 transitions"])
    expect(screen.queryByRole("columnheader", { name: /Story Type/ })).not.toBeInTheDocument()
  })

  it("switches to a new idea's chip if it would otherwise be hidden", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<IdeaList />, { path: "/ideas" })
    await user.click(screen.getByRole("button", { name: "Story Pitches" }))

    await user.click(screen.getByRole("button", { name: "New Idea" }))
    const panel = screen.getByRole("dialog", { name: "New Idea" })
    await user.click(within(panel).getByRole("button", { name: "General Idea" }))
    await user.type(within(panel).getByRole("textbox", { name: "Title" }), "Night buses")
    await user.click(within(panel).getByRole("button", { name: "Submit" }))

    expect(screen.getByRole("button", { name: "General Ideas" })).toHaveAttribute("aria-pressed", "true")
    expect(rowTitles()).toContain("Night buses")
  })

  it("renders the Ideas heading and a New Idea button", async () => {
    await renderWithRouter(<IdeaList />, { path: "/ideas" })

    expect(screen.getByRole("heading", { name: "Ideas" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "New Idea" })).toBeInTheDocument()
  })

  it("edits an idea in the panel from its title, filled in with that idea", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<IdeaList />, { path: "/ideas" })

    const row = screen.getByRole("row", { name: /Reusable map legend/ })
    await user.click(within(row).getByRole("button", { name: "Reusable map legend" }))

    const panel = screen.getByRole("dialog", { name: "Edit Idea" })
    const title = within(panel).getByRole("textbox", { name: "Title" })
    expect(title).toHaveValue("Reusable map legend")
    expect(within(panel).getByRole("combobox", { name: "Choose idea type:" })).toHaveTextContent("General Idea")

    await user.clear(title)
    await user.type(title, "Reusable map legend kit")
    await user.click(within(panel).getByRole("button", { name: "Save" }))

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    expect(rowTitles()).toContain("Reusable map legend kit")
    expect(rowTitles()).not.toContain("Reusable map legend")
  })

  it("opens the full-page form from the pencil", async () => {
    const user = userEvent.setup()
    const { router } = await renderWithRouter(<IdeaList />, { path: "/ideas" })

    const row = screen.getByRole("row", { name: /Reusable map legend/ })
    await user.click(within(row).getByRole("button", { name: "Edit idea" }))

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    expect(router.state.location.pathname).toBe("/ideas/i2/edit")
  })

  it("sends Edit on an idea in intake to its Intake view instead of the form", async () => {
    const user = userEvent.setup()
    const { router } = await renderWithRouter(<IdeaList />, { path: "/ideas" })

    const row = screen.getByRole("row", { name: /Where the glaciers went/ })
    await user.click(within(row).getByRole("button", { name: "Edit idea" }))

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    expect(router.state.location.pathname).toBe("/ideas/i1/intake")
  })

  it("shows no chip for a field that doesn't apply, like Story Type on a General Idea", async () => {
    await renderWithRouter(<IdeaList />, { path: "/ideas" })

    const row = screen.getByRole("row", { name: /Reusable map legend/ })
    const storyType = row.querySelector('[data-column-id="storyType"]') as HTMLElement
    expect(storyType).toBeEmptyDOMElement()
  })

  it("has Title, Author, Type, Story Type, Status and Submitted columns, in that order", async () => {
    await renderWithRouter(<IdeaList />, { path: "/ideas" })

    const headers = screen.getAllByRole("columnheader").map((header) => header.textContent)
    expect(headers.slice(0, 6)).toEqual(["Title", "Author", "Type", "Story Type", "Status", "Submitted"])
  })

  it("gives draft Story Pitches forward, edit and delete; the rest just edit and delete", async () => {
    await renderWithRouter(<IdeaList />, { path: "/ideas" })

    const actionsOf = (title: RegExp) =>
      within(screen.getByRole("row", { name: title }))
        .getAllByRole("button")
        .map((button) => button.getAttribute("aria-label"))
        .filter((label) => label?.endsWith(" idea"))
    // Forward only on Story Pitches not yet in intake
    expect(actionsOf(/The ferryman's last season/)).toEqual(["Forward idea", "Edit idea", "Delete idea"])
    expect(actionsOf(/Where the glaciers went/)).toEqual(["Edit idea", "Delete idea"])
    expect(actionsOf(/Reusable map legend/)).toEqual(["Edit idea", "Delete idea"])
    expect(actionsOf(/Learn D3 transitions/)).toEqual(["Edit idea", "Delete idea"])
  })

  it("filters ideas by type", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<IdeaList />, { path: "/ideas" })

    const header = screen.getByRole("columnheader", { name: /^Type/ })
    await user.click(within(header).getByRole("button", { name: /filter/i }))
    await user.click(screen.getByRole("option", { name: "Story Pitch" }))

    expect(rowTitles()).toEqual([
      "Where the glaciers went",
      "The ferryman's last season",
    ])
  })

  it("deletes an idea with its delete action", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<IdeaList />, { path: "/ideas" })

    const [firstRow] = screen.getAllByRole("row").slice(1)
    await user.click(within(firstRow).getByRole("button", { name: "Delete idea" }))
    await user.click(screen.getByRole("button", { name: "Yes" }))

    expect(rowTitles()).not.toContain("Where the glaciers went")
  })

  it("forwards an idea into intake after confirming, with a preview of the idea", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<IdeaList />, { path: "/ideas" })

    const row = screen.getByRole("row", { name: /The ferryman's last season/ })
    await user.click(within(row).getByRole("button", { name: "Forward idea" }))

    const dialog = screen.getByRole("dialog", {
      name: "Are you sure you want to move this idea to the intake flow?",
    })
    expect(within(dialog).getByText("The ferryman's last season")).toBeInTheDocument()
    // Shown the way the table shows them: chips, @handle — no labels
    expect(within(dialog).getByText("Draft")).toBeInTheDocument()
    expect(within(dialog).getByText("Short Story")).toBeInTheDocument()
    expect(within(dialog).getByText("@MarcusChen")).toBeInTheDocument()
    expect(within(dialog).queryByText(/Status:/)).not.toBeInTheDocument()
    expect(within(dialog).getByText("Submitted:")).toBeInTheDocument()

    await user.click(within(dialog).getByRole("button", { name: "Yes" }))
    expect(within(row).getByText("Intake")).toBeInTheDocument()
  })
})
