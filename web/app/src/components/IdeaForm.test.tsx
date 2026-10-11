import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { IdeaForm, NewIdeaFlow } from "./IdeaForm"

function renderForm() {
  const onSubmit = vi.fn()
  const onClose = vi.fn()
  render(<IdeaForm onSubmit={onSubmit} onClose={onClose} />)
  return { onSubmit, onClose }
}

async function chooseType(user: ReturnType<typeof userEvent.setup>, label: string) {
  await user.click(screen.getByRole("combobox", { name: "Choose idea type:" }))
  await user.click(screen.getByRole("option", { name: label }))
}

describe("IdeaForm", () => {
  it("starts as a General Idea: title, body and type only", () => {
    renderForm()

    expect(screen.getByRole("heading", { name: "New Idea" })).toBeInTheDocument()
    expect(screen.getByRole("combobox", { name: "Choose idea type:" })).toHaveTextContent("General Idea")
    expect(screen.getByRole("textbox", { name: "Title" })).toBeInTheDocument()
    expect(screen.getByRole("textbox", { name: "Idea" })).toBeInTheDocument()
    expect(screen.queryByRole("combobox", { name: "Status" })).not.toBeInTheDocument()
    expect(screen.queryByText("What should my pitch contain?")).not.toBeInTheDocument()
  })

  it("adds Story Type, Status, Litmus Test Result ID and the pitch guidance for a Story Pitch", async () => {
    const user = userEvent.setup()
    renderForm()

    await chooseType(user, "Story Pitch")

    expect(screen.getByRole("combobox", { name: "Story Type" })).toHaveTextContent("Data Story")
    expect(screen.getByRole("combobox", { name: "Status" })).toHaveTextContent("Draft")
    expect(screen.getByRole("textbox", { name: "Litmus Test Result ID" })).toBeInTheDocument()
    expect(screen.getByText("What should my pitch contain?")).toBeInTheDocument()

    await chooseType(user, "General Idea")
    expect(screen.queryByRole("combobox", { name: "Status" })).not.toBeInTheDocument()
  })

  it("clears the Litmus Test Result ID from its clear button", async () => {
    const user = userEvent.setup()
    renderForm()
    await chooseType(user, "Story Pitch")

    const litmus = screen.getByRole("textbox", { name: "Litmus Test Result ID" })
    await user.type(litmus, "LT-42")
    await user.click(screen.getByRole("button", { name: "Clear Litmus Test Result ID" }))

    expect(litmus).toHaveValue("")
  })

  it("needs a title before it submits", async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderForm()

    await user.click(screen.getByRole("button", { name: "Submit" }))

    expect(onSubmit).not.toHaveBeenCalled()
    expect(screen.getByText("Give your idea a title.")).toBeInTheDocument()
  })

  it("submits the idea with its type-specific fields", async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderForm()

    await user.type(screen.getByRole("textbox", { name: "Title" }), "Heat maps of the grid")
    await chooseType(user, "Story Pitch")
    await user.click(screen.getByRole("combobox", { name: "Story Type" }))
    await user.click(screen.getByRole("option", { name: "Short Story" }))
    await user.click(screen.getByRole("combobox", { name: "Status" }))
    await user.click(screen.getByRole("option", { name: "Intake" }))
    await user.type(screen.getByRole("textbox", { name: "Litmus Test Result ID" }), "LT-7")
    await user.click(screen.getByRole("button", { name: "Submit" }))

    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        title: "Heat maps of the grid",
        type: "story-pitch",
        storyType: "short-story",
        status: "intake",
        litmusTestId: "LT-7",
      }),
    )
  })

  it("closes from the X", async () => {
    const user = userEvent.setup()
    const { onClose } = renderForm()

    await user.click(screen.getByRole("button", { name: "Close" }))

    expect(onClose).toHaveBeenCalledTimes(1)
  })
})

describe("NewIdeaFlow", () => {
  it("asks for the idea type first, then opens the form on that type", async () => {
    const user = userEvent.setup()
    render(<NewIdeaFlow onSubmit={vi.fn()} onClose={vi.fn()} />)

    const choices = screen.getByRole("group", { name: "Choose idea type" })
    expect(
      within(choices)
        .getAllByRole("button")
        .map((button) => button.textContent),
    ).toEqual(["General Idea", "Story Pitch"])
    expect(screen.queryByRole("textbox", { name: "Title" })).not.toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Story Pitch" }))

    expect(screen.getByRole("textbox", { name: "Title" })).toBeInTheDocument()
    expect(screen.getByRole("combobox", { name: "Choose idea type:" })).toHaveTextContent("Story Pitch")
    expect(screen.getByRole("combobox", { name: "Story Type" })).toBeInTheDocument()
  })

  it("closes from the chooser's X too", async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    render(<NewIdeaFlow onSubmit={vi.fn()} onClose={onClose} />)

    await user.click(screen.getByRole("button", { name: "Close" }))

    expect(onClose).toHaveBeenCalledTimes(1)
  })
})
