import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { ActionsCell, DateCell, SelectCell, TextCell, UserCell, formatIsoDate, userHandle } from "./cells"
import type { RowAction, SelectOption } from "./types"
import type { DataTableUser } from "./types"

const STATUS_OPTIONS: SelectOption[] = [
  { value: "todo", label: "To Do" },
  { value: "in-progress", label: "In Progress", colorVar: "--color-accent" },
  { value: "done", label: "Done", colorVar: "--color-accent-success" },
]

describe("TextCell", () => {
  it("renders the value", () => {
    render(<TextCell value="Sample task name" />)

    expect(screen.getByText("Sample task name")).toBeInTheDocument()
  })

  it("renders nothing for a nullish value", () => {
    const { container } = render(<TextCell value={undefined} />)

    expect(container).toHaveTextContent("")
  })
})

describe("SelectCell", () => {
  it("renders the label for the current value, not the raw value", () => {
    render(<SelectCell value="in-progress" options={STATUS_OPTIONS} onChange={vi.fn()} />)

    expect(screen.getByRole("button")).toHaveTextContent("In Progress")
    expect(screen.queryByText("in-progress")).not.toBeInTheDocument()
  })

  it("shows unknownLabel, not the raw value, when the value is not an option", () => {
    render(<SelectCell value="p-gone" options={STATUS_OPTIONS} unknownLabel="Deleted project" onChange={vi.fn()} />)

    expect(screen.getByRole("button")).toHaveTextContent("Deleted project")
    expect(screen.queryByText("p-gone")).not.toBeInTheDocument()
  })

  it("tints a colored badge with its colorVar, text via the --badge-* tokens", () => {
    render(<SelectCell value="done" options={STATUS_OPTIONS} onChange={vi.fn()} />)

    const badge = screen.getByText("Done")
    expect(badge.style.backgroundColor).toContain("var(--color-accent-success)")
    expect(badge.style.color).toContain("var(--badge-text-base)")
  })

  it("opens a list of every option when clicked", async () => {
    const user = userEvent.setup()
    render(<SelectCell value="todo" options={STATUS_OPTIONS} onChange={vi.fn()} />)

    await user.click(screen.getByRole("button"))

    for (const option of STATUS_OPTIONS) {
      expect(screen.getByRole("option", { name: option.label })).toBeInTheDocument()
    }
  })

  it("calls onChange with the chosen value", async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<SelectCell value="todo" options={STATUS_OPTIONS} onChange={onChange} />)

    await user.click(screen.getByRole("button"))
    await user.click(screen.getByRole("option", { name: "Done" }))

    expect(onChange).toHaveBeenCalledWith("done")
  })
})

const USERS: DataTableUser[] = [
  { id: "u1", name: "Claudicia" },
  { id: "u2", name: "Marcus" },
]

describe("formatIsoDate", () => {
  it("formats an ISO date for display", () => {
    expect(formatIsoDate("2026-09-15")).toBe("Sep 15, 2026")
  })

  it("does not shift the day across timezones", () => {
    expect(formatIsoDate("2026-01-01")).toContain("1, 2026")
  })

  it("returns an empty string for an empty value", () => {
    expect(formatIsoDate("")).toBe("")
  })
})

describe("DateCell", () => {
  it("renders the formatted date, not the raw ISO string", () => {
    render(<DateCell value="2026-09-15" onChange={vi.fn()} />)

    expect(screen.getByRole("button")).toHaveTextContent("Sep 15, 2026")
  })

  it("shows a calendar icon after the date", () => {
    const { container } = render(<DateCell value="2026-09-15" onChange={vi.fn()} />)
    expect(container.querySelector("button i")).toHaveAttribute("aria-hidden", "true")
  })

  it("opens a calendar when clicked", async () => {
    const user = userEvent.setup()
    render(<DateCell value="2026-09-15" onChange={vi.fn()} />)

    await user.click(screen.getByRole("button"))

    expect(screen.getByRole("grid")).toBeInTheDocument()
  })

  it("opens the calendar on the month of the set date", async () => {
    const user = userEvent.setup()
    render(<DateCell value="2020-03-15" onChange={vi.fn()} />)

    await user.click(screen.getByRole("button"))

    expect(screen.getByRole("grid")).toHaveAccessibleName(/March 2020/)
  })

  it("calls onChange with an ISO date when a day is picked", async () => {
    // The calendar opens on the current month, not the selected value's
    // month, so the value and the expectation are both derived from today
    // rather than a fixed month — otherwise the "22" button picked belongs
    // to whatever month the test happens to run in.
    const today = new Date()
    const year = today.getFullYear()
    const month = String(today.getMonth() + 1).padStart(2, "0")
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<DateCell value={`${year}-${month}-15`} onChange={onChange} />)

    await user.click(screen.getByRole("button"))
    // React-day-picker v10 renders calendar days in a grid structure
    // Find and click the day button for the 22nd
    const dayButtons = screen.getAllByRole("button")
    const day22Button = dayButtons.find(btn => btn.textContent?.trim() === "22")
    if (day22Button) await user.click(day22Button)

    expect(onChange).toHaveBeenCalledWith(`${year}-${month}-22`)
  })
})

describe("userHandle", () => {
  it("strips spaces and punctuation from the full name", () => {
    expect(userHandle({ id: "u1", name: "Tobias Smith" })).toBe("@TobiasSmith")
    expect(userHandle({ id: "u2", name: "Mary-Jane O'Neil" })).toBe("@MaryJaneONeil")
    expect(userHandle({ id: "u3", name: "José Núñez" })).toBe("@JoséNúñez")
  })

  it("prefers an explicit handle", () => {
    expect(userHandle({ id: "u1", name: "Tobias Smith", handle: "toby" })).toBe("@toby")
  })
})

describe("UserCell", () => {
  it("renders the user's name, not their id", () => {
    render(<UserCell value="u1" users={USERS} onChange={vi.fn()} />)

    expect(screen.getByRole("button")).toHaveTextContent("Claudicia")
    expect(screen.queryByText("u1")).not.toBeInTheDocument()
  })

  it("renders unassigned when the value matches no one", () => {
    render(<UserCell value="" users={USERS} onChange={vi.fn()} />)

    expect(screen.getByRole("button")).toHaveTextContent("Unassigned")
  })

  it("calls onChange with the chosen user id", async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<UserCell value="u1" users={USERS} onChange={onChange} />)

    await user.click(screen.getByRole("button"))
    await user.click(screen.getByRole("option", { name: /Marcus/ }))

    expect(onChange).toHaveBeenCalledWith("u2")
  })
})

describe("ActionsCell", () => {
  type Row = { id: string; name: string }
  const row: Row = { id: "r1", name: "Sample" }

  function makeHelpers() {
    return { addRow: vi.fn(), removeRow: vi.fn(), updateRow: vi.fn() }
  }

  it("renders one button per declared action, with its label", () => {
    const actions: RowAction<Row>[] = [
      { id: "edit", label: "Edit task", icon: <span>E</span>, onSelect: vi.fn() },
      { id: "delete", label: "Delete task", icon: <span>D</span>, onSelect: vi.fn() },
    ]
    render(<ActionsCell row={row} actions={actions} helpers={makeHelpers()} />)

    expect(screen.getByRole("button", { name: "Edit task" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Delete task" })).toBeInTheDocument()
  })

  it("renders a content type's own action set, not a hardcoded edit/delete pair", () => {
    const actions: RowAction<Row>[] = [
      { id: "download", label: "Download", icon: <span>v</span>, onSelect: vi.fn() },
      { id: "review", label: "Review", icon: <span>r</span>, onSelect: vi.fn() },
    ]
    render(<ActionsCell row={row} actions={actions} helpers={makeHelpers()} />)

    expect(screen.getByRole("button", { name: "Download" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Review" })).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /edit/i })).not.toBeInTheDocument()
  })

  it("passes the row and the helpers to onSelect", async () => {
    const user = userEvent.setup()
    const onSelect = vi.fn()
    const helpers = makeHelpers()
    const actions: RowAction<Row>[] = [
      { id: "edit", label: "Edit task", icon: <span>E</span>, onSelect },
    ]
    render(<ActionsCell row={row} actions={actions} helpers={helpers} />)

    await user.click(screen.getByRole("button", { name: "Edit task" }))

    expect(onSelect).toHaveBeenCalledWith(row, helpers)
  })

  it("lets a delete action remove its own row through the helpers", async () => {
    const user = userEvent.setup()
    const helpers = makeHelpers()
    const actions: RowAction<Row>[] = [
      {
        id: "delete",
        label: "Delete task",
        icon: <span>D</span>,
        variant: "destructive",
        onSelect: (_row, h) => h.removeRow(),
      },
    ]
    render(<ActionsCell row={row} actions={actions} helpers={helpers} />)

    await user.click(screen.getByRole("button", { name: "Delete task" }))
    // Destructive actions ask first — nothing happens until Yes
    expect(helpers.removeRow).not.toHaveBeenCalled()
    expect(
      screen.getByRole("dialog", { name: "Are you sure you want to delete this item?" }),
    ).toBeInTheDocument()
    // Marked destructive, which tints the preview red
    expect(screen.getByRole("dialog")).toHaveAttribute("data-destructive")
    await user.click(screen.getByRole("button", { name: "Yes" }))

    expect(helpers.removeRow).toHaveBeenCalledTimes(1)
  })

  it("lets a duplicate action add a new row through the helpers", async () => {
    const user = userEvent.setup()
    const helpers = makeHelpers()
    const actions: RowAction<Row>[] = [
      {
        id: "duplicate",
        label: "Duplicate task",
        icon: <span>+</span>,
        onSelect: (original, h) => h.addRow({ ...original, id: "r2" }),
      },
    ]
    render(<ActionsCell row={row} actions={actions} helpers={helpers} />)

    await user.click(screen.getByRole("button", { name: "Duplicate task" }))

    expect(helpers.addRow).toHaveBeenCalledWith({ id: "r2", name: "Sample" })
  })

  it("cancels a destructive action from No, the close X, or Escape", async () => {
    const user = userEvent.setup()
    const helpers = makeHelpers()
    const actions: RowAction<Row>[] = [
      {
        id: "delete",
        label: "Delete task",
        icon: <span>D</span>,
        variant: "destructive",
        onSelect: (_row, h) => h.removeRow(),
      },
    ]
    render(<ActionsCell row={row} actions={actions} helpers={helpers} />)

    for (const cancel of [
      () => user.click(screen.getByRole("button", { name: "No" })),
      () => user.click(screen.getByRole("button", { name: "Close" })),
      () => user.keyboard("{Escape}"),
    ]) {
      await user.click(screen.getByRole("button", { name: "Delete task" }))
      await cancel()
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
    }
    expect(helpers.removeRow).not.toHaveBeenCalled()
  })

  it("starts focus on No, and words the question per row when confirm is a function", async () => {
    const user = userEvent.setup()
    const helpers = makeHelpers()
    const onSelect = vi.fn()
    const actions: RowAction<Row>[] = [
      {
        id: "archive",
        label: "Archive",
        icon: <span>A</span>,
        confirm: (r) => `Archive ${(r as { id: string }).id}?`,
        onSelect,
      },
    ]
    render(<ActionsCell row={row} actions={actions} helpers={helpers} preview={<p>Preview card</p>} />)

    await user.click(screen.getByRole("button", { name: "Archive" }))

    expect(screen.getByRole("dialog", { name: `Archive ${(row as { id: string }).id}?` })).toBeInTheDocument()
    expect(screen.getByText("Preview card")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "No" })).toHaveFocus()
    expect(screen.getByRole("dialog")).not.toHaveAttribute("data-destructive")
    await user.click(screen.getByRole("button", { name: "Yes" }))
    expect(onSelect).toHaveBeenCalledTimes(1)
  })
})
