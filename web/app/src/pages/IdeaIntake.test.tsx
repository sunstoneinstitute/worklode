import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it } from "vitest"
import { getIdeas, resetIdeas } from "@/lib/ideas"
import { renderWithRouter } from "@/test/render"
import { IdeaIntake } from "./IdeaIntake"

describe("IdeaIntake", () => {
  // The ideas store is shared module state — start each test from the samples
  beforeEach(() => resetIdeas())

  it("shows the idea's title, story type, a task highlight, and the pitch as the first section", async () => {
    await renderWithRouter(<IdeaIntake ideaId="i1" />, { path: "/ideas/i1/intake" })

    expect(screen.getByRole("heading", { name: "Where the glaciers went" })).toBeInTheDocument()
    expect(screen.getByText("Data Story")).toBeInTheDocument()
    const sections = within(screen.getByRole("group", { name: "Section" }))
      .getAllByRole("button")
      .map((chip) => chip.textContent)
    expect(sections).toEqual(["Pitch", "Litmus", "Sources"])
    expect(screen.getByRole("button", { name: "Pitch" })).toHaveAttribute("aria-pressed", "true")
    expect(within(screen.getByRole("region", { name: "Pitch" })).getByText(/What happens to a valley/)).toBeInTheDocument()
    expect(screen.getByRole("status")).toHaveTextContent("You have a decision to make on this pitch")
  })

  it("edits the pitch in a side panel", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<IdeaIntake ideaId="i1" />, { path: "/ideas/i1/intake" })

    await user.click(screen.getByRole("button", { name: "Edit pitch" }))
    const panel = screen.getByRole("dialog", { name: "Edit Pitch" })
    const pitch = within(panel).getByRole("textbox", { name: "Pitch" })
    await user.clear(pitch)
    await user.type(pitch, "A shorter pitch.")
    await user.click(within(panel).getByRole("button", { name: "Save" }))

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    expect(within(screen.getByRole("region", { name: "Pitch" })).getByText("A shorter pitch.")).toBeInTheDocument()
    expect(getIdeas().find((idea) => idea.id === "i1")?.body).toBe("A shorter pitch.")
  })

  it("walks the intake flow: authorize pre-research, then decide", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<IdeaIntake ideaId="i1" />, { path: "/ideas/i1/intake" })
    const steps = () =>
      within(screen.getByRole("list", { name: "Intake progress" }))
        .getAllByRole("listitem")
        .map((step) => step.textContent)

    expect(steps()).toEqual(["Litmus(done)", "AI Pre-Research(to do)", "Decision(to do)"])
    expect(screen.getByText("Your decision is required to authorize pre-research for this idea.")).toBeInTheDocument()
    // The decision's buttons live in the task highlight; the summary is status only
    expect(within(screen.getByRole("status")).getByRole("button", { name: "Authorize AI pre-research" })).toBeInTheDocument()
    expect(screen.getByText("Litmus test complete. Waiting on authorization for AI pre-research.")).toBeInTheDocument()

    const tabs = () =>
      within(screen.getByRole("group", { name: "Section" }))
        .getAllByRole("button")
        .map((chip) => chip.textContent)
    expect(tabs()).toEqual(["Pitch", "Litmus", "Sources"])

    await user.click(screen.getByRole("button", { name: "Authorize AI pre-research" }))
    expect(steps()).toEqual(["Litmus(done)", "AI Pre-Research(done)", "Decision(to do)"])
    // Authorizing adds the AI Pre-Research tab
    expect(tabs()).toEqual(["Pitch", "Litmus", "AI Pre-Research", "Sources"])
    await user.click(screen.getByRole("button", { name: "AI Pre-Research" }))
    expect(screen.getByRole("heading", { name: "AI Pre-Research" })).toBeInTheDocument()
    expect(screen.getByText(/The Science Lead has accepted this pitch/)).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Accept" }))
    expect(steps()).toEqual(["Litmus(done)", "AI Pre-Research(done)", "Decision(done)"])
    expect(screen.getByText("Decision recorded: Accepted")).toBeInTheDocument()
    expect(screen.queryByRole("status")).not.toBeInTheDocument()
    // Saved with the idea, so it's still there next time
    expect(getIdeas().find((idea) => idea.id === "i1")?.intake).toEqual({
      preResearchAuthorized: true,
      decision: "accept",
    })
  })

  it("can decide straight away, before pre-research", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<IdeaIntake ideaId="i1" />, { path: "/ideas/i1/intake" })

    await user.click(screen.getByRole("button", { name: "Park" }))

    expect(screen.getByText("Decision recorded: Parked")).toBeInTheDocument()
  })

  it("switches the Litmus view and test, and has a Sources tab like Documents' Sources", async () => {
    const user = userEvent.setup()
    await renderWithRouter(<IdeaIntake ideaId="i1" />, { path: "/ideas/i1/intake" })

    await user.click(screen.getByRole("button", { name: "Litmus" }))
    expect(screen.getByText(/Strong fit/)).toBeInTheDocument()
    await user.click(screen.getByRole("tab", { name: "Recommendations" }))
    expect(screen.getByText("Narrow to three valleys at different stages of retreat.")).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Sources" }))
    const headers = screen.getAllByRole("columnheader").map((header) => header.textContent)
    expect(headers).toEqual(["Name", "Type", "Status", "Source", ""])
    expect(screen.getByText("Swiss glacier mass balance, 1914–2025")).toBeInTheDocument()
    expect(screen.getAllByRole("button", { name: "Download source" }).length).toBeGreaterThan(0)

    await user.click(screen.getByRole("button", { name: "Add Source" }))
    expect(screen.getByText("Untitled source")).toBeInTheDocument()
  })

  it("says so when the idea doesn't exist", async () => {
    await renderWithRouter(<IdeaIntake ideaId="nope" />, { path: "/ideas/nope/intake" })

    expect(screen.getByRole("heading", { name: "Idea not found" })).toBeInTheDocument()
  })
})
