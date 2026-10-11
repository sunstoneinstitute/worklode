import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { ProjectSummary } from "./ProjectSummary"

function setup() {
  const user = userEvent.setup()
  const { container } = render(<ProjectSummary />)
  const card = container.querySelector<HTMLElement>("[data-project-summary]")!
  const rows = () => [...container.querySelectorAll<HTMLElement>("[data-crew-row]")]
  const panels = () => [...container.querySelectorAll<HTMLElement>("[data-crew-panel]")]
  const toggleOf = (row: HTMLElement) => row.querySelector<HTMLElement>("[data-crew-row-toggle]")!
  return { user, card, rows, panels, toggleOf }
}

describe("ProjectSummary", () => {
  it("starts on the summary view, without moving focus", () => {
    const { card } = setup()
    expect(card).not.toHaveAttribute("data-view")
    expect(document.body).toHaveFocus()
  })

  it("switches to the crew view and back, moving focus to the other view's button", async () => {
    const { user, card } = setup()

    await user.click(screen.getByRole("button", { name: /What is the crew working on\?/ }))
    expect(card).toHaveAttribute("data-view", "crew")
    expect(screen.getByRole("button", { name: "Back to project summary" })).toHaveFocus()

    await user.click(screen.getByRole("button", { name: "Back to project summary" }))
    expect(card).not.toHaveAttribute("data-view")
    expect(screen.getByRole("button", { name: /What is the crew working on\?/ })).toHaveFocus()
  })

  it("starts with the first person selected and no row open", () => {
    const { rows, panels, toggleOf } = setup()
    expect(rows()[0]).toHaveAttribute("data-selected")
    expect(panels()[0]).toHaveAttribute("data-selected")
    expect(rows()[1]).not.toHaveAttribute("data-selected")
    for (const row of rows()) {
      expect(row).not.toHaveAttribute("data-open")
      expect(toggleOf(row)).toHaveAttribute("aria-expanded", "false")
    }
  })

  it("clicking a row selects that person and opens their row", async () => {
    const { user, rows, panels, toggleOf } = setup()
    const second = rows()[1]

    await user.click(toggleOf(second))

    expect(second).toHaveAttribute("data-selected")
    expect(second).toHaveAttribute("data-open")
    expect(toggleOf(second)).toHaveAttribute("aria-expanded", "true")
    expect(panels()[1]).toHaveAttribute("data-selected")
    expect(rows()[0]).not.toHaveAttribute("data-selected")
    expect(panels()[0]).not.toHaveAttribute("data-selected")
  })

  it("clicking an open row again closes it but keeps it selected", async () => {
    const { user, rows, toggleOf } = setup()
    const second = rows()[1]

    await user.click(toggleOf(second))
    await user.click(toggleOf(second))

    expect(second).not.toHaveAttribute("data-open")
    expect(toggleOf(second)).toHaveAttribute("aria-expanded", "false")
    expect(second).toHaveAttribute("data-selected")
  })

  it("rows open independently", async () => {
    const { user, rows, toggleOf } = setup()
    await user.click(toggleOf(rows()[0]))
    await user.click(toggleOf(rows()[1]))
    expect(rows()[0]).toHaveAttribute("data-open")
    expect(rows()[1]).toHaveAttribute("data-open")
  })
})
