import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { DataTable } from "./data-table"
import type { DataTableColumn, DataTableUser } from "./types"

type Task = { id: string; name: string; status: string; due: string; assignee: string }

const USERS: DataTableUser[] = [
  { id: "u1", name: "Ada" },
  { id: "u2", name: "Grace" },
]

const TASKS: Task[] = [
  { id: "t1", name: "Write copy", status: "todo", due: "2026-09-10", assignee: "u1" },
  { id: "t2", name: "Audit icons", status: "done", due: "2026-09-20", assignee: "u2" },
  { id: "t3", name: "Ship export", status: "todo", due: "2026-09-30", assignee: "u1" },
]

const COLUMNS: DataTableColumn<Task>[] = [
  { key: "name", label: "Task Name", type: "text" },
  {
    key: "status",
    label: "Status",
    type: "select",
    options: [
      { value: "todo", label: "To Do" },
      { value: "done", label: "Done" },
    ],
  },
  { key: "due", label: "Due Date", type: "date" },
  { key: "assignee", label: "Assignee", type: "user" },
]

function renderTable() {
  return render(
    <DataTable
      columns={COLUMNS}
      data={TASKS}
      getRowId={(task) => task.id}
      users={USERS}
      title="Tasks"
      toolbarActions={<button type="button">New Task</button>}
    />,
  )
}

function rowNames() {
  const [, ...bodyRows] = screen.getAllByRole("row")
  return bodyRows.map((row) => within(row).getAllByRole("cell")[0].textContent)
}

describe("toolbar", () => {
  it("renders the title and the consumer's own toolbar actions", () => {
    renderTable()

    expect(screen.getByRole("heading", { name: "Tasks" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "New Task" })).toBeInTheDocument()
  })

  it("filters rows as you type in the search box", async () => {
    const user = userEvent.setup()
    renderTable()

    await user.click(screen.getByRole("button", { name: /search/i }))
    await user.type(screen.getByRole("searchbox"), "icons")

    expect(rowNames()).toEqual(["Audit icons"])
  })

  it("shows every row again when the search is cleared", async () => {
    const user = userEvent.setup()
    renderTable()

    await user.click(screen.getByRole("button", { name: /search/i }))
    await user.type(screen.getByRole("searchbox"), "icons")
    await user.clear(screen.getByRole("searchbox"))

    expect(rowNames()).toHaveLength(3)
  })

  it("shows a close button as soon as search opens, even when empty", async () => {
    const user = userEvent.setup()
    renderTable()

    await user.click(screen.getByRole("button", { name: /search/i }))

    expect(screen.getByRole("button", { name: "Close search" })).toBeInTheDocument()
  })

  it("clears the search and closes it from the close button", async () => {
    const user = userEvent.setup()
    renderTable()

    await user.click(screen.getByRole("button", { name: /search/i }))
    await user.type(screen.getByRole("searchbox"), "icons")
    await user.click(screen.getByRole("button", { name: "Close search" }))

    expect(screen.queryByRole("searchbox")).not.toBeInTheDocument()
    expect(rowNames()).toHaveLength(3)
  })

  it("renders a column-customize trigger", () => {
    renderTable()

    expect(screen.getByRole("button", { name: /columns/i })).toBeInTheDocument()
  })

  it("shows the searchbox whenever a controlled globalFilter is non-empty, without needing it opened", () => {
    const { rerender } = render(
      <DataTable
        columns={COLUMNS}
        data={TASKS}
        getRowId={(task) => task.id}
        users={USERS}
        title="Tasks"
        viewState={{ sorting: [], columnFilters: [], globalFilter: "" }}
        onViewStateChange={() => {}}
      />,
    )
    expect(screen.queryByRole("searchbox")).not.toBeInTheDocument()

    rerender(
      <DataTable
        columns={COLUMNS}
        data={TASKS}
        getRowId={(task) => task.id}
        users={USERS}
        title="Tasks"
        viewState={{ sorting: [], columnFilters: [], globalFilter: "icons" }}
        onViewStateChange={() => {}}
      />,
    )
    expect(screen.getByRole("searchbox")).toHaveValue("icons")
  })
})

describe("per-column filters", () => {
  it("narrows rows to a chosen select value", async () => {
    const user = userEvent.setup()
    renderTable()

    const header = screen.getByRole("columnheader", { name: /Status/ })
    await user.click(within(header).getByRole("button", { name: /filter/i }))
    await user.click(screen.getByRole("option", { name: "To Do" }))

    expect(rowNames()).toEqual(["Write copy", "Ship export"])
  })

  it("narrows rows by typed text on a text column", async () => {
    const user = userEvent.setup()
    renderTable()

    const header = screen.getByRole("columnheader", { name: /Task Name/ })
    await user.click(within(header).getByRole("button", { name: /filter/i }))
    await user.type(screen.getByRole("textbox"), "ship")

    expect(rowNames()).toEqual(["Ship export"])
  })

  it("does not offer a filter on an actions column", () => {
    render(
      <DataTable
        columns={[...COLUMNS, { key: "actions", label: "Actions", type: "actions", actions: [] }]}
        data={TASKS}
        getRowId={(task) => task.id}
      />,
    )

    const header = screen.getByRole("columnheader", { name: /Actions/ })

    expect(within(header).queryByRole("button", { name: /filter/i })).not.toBeInTheDocument()
  })
})

describe("select/user multiselect filter", () => {
  it("matches rows against multiple checked values (OR semantics)", async () => {
    const user = userEvent.setup()
    renderTable()

    const header = screen.getByRole("columnheader", { name: /Status/ })
    await user.click(within(header).getByRole("button", { name: /filter/i }))
    await user.click(screen.getByRole("option", { name: "To Do" }))
    await user.click(screen.getByRole("option", { name: "Done" }))

    expect(rowNames()).toEqual(["Write copy", "Audit icons", "Ship export"])
  })

  it("does not close the popover when an item is clicked", async () => {
    const user = userEvent.setup()
    renderTable()

    const header = screen.getByRole("columnheader", { name: /Status/ })
    await user.click(within(header).getByRole("button", { name: /filter/i }))
    await user.click(screen.getByRole("option", { name: "To Do" }))

    expect(screen.getByRole("option", { name: "Done" })).toBeInTheDocument()
  })

  it("clicking All clears the filter and shows every row again", async () => {
    const user = userEvent.setup()
    renderTable()

    const header = screen.getByRole("columnheader", { name: /Status/ })
    await user.click(within(header).getByRole("button", { name: /filter/i }))
    await user.click(screen.getByRole("option", { name: "To Do" }))
    expect(rowNames()).toEqual(["Write copy", "Ship export"])

    await user.click(screen.getByRole("option", { name: "All" }))

    expect(rowNames()).toHaveLength(3)
  })

  it("unchecking the last selected value clears the filter, same as All", async () => {
    const user = userEvent.setup()
    renderTable()

    const header = screen.getByRole("columnheader", { name: /Status/ })
    await user.click(within(header).getByRole("button", { name: /filter/i }))
    await user.click(screen.getByRole("option", { name: "To Do" }))
    expect(rowNames()).toEqual(["Write copy", "Ship export"])

    await user.click(screen.getByRole("option", { name: "To Do" }))

    expect(rowNames()).toHaveLength(3)
  })

  it("narrows rows to the selected people on a user column", async () => {
    const user = userEvent.setup()
    renderTable()

    const header = screen.getByRole("columnheader", { name: /Assignee/ })
    await user.click(within(header).getByRole("button", { name: /filter/i }))
    await user.click(screen.getByRole("option", { name: "@Ada" }))

    expect(rowNames()).toEqual(["Write copy", "Ship export"])
  })
})

describe("date range filter", () => {
  // Pin "today" so the Calendar (which defaults to the current month when
  // no filter value is set yet) reliably shows September 2026, matching the
  // fixture due dates below — without this, the test's pass/fail would
  // depend on which real-world month it happens to run in. Deliberately
  // vi.setSystemTime alone, NOT vi.useFakeTimers(): combining fake timers
  // with userEvent's internal pointer/timing here hung the test indefinitely
  // (verified in isolation) — setSystemTime mocks `Date` without touching
  // setTimeout/rAF, which is all the Calendar's month default needs.
  beforeEach(() => {
    vi.setSystemTime(new Date("2026-09-15T12:00:00Z"))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  function dayButton(day: string) {
    return screen.getAllByRole("button").find((button) => button.textContent === day)
  }

  it("narrows rows to a picked date range", async () => {
    const user = userEvent.setup()
    renderTable()

    const header = screen.getByRole("columnheader", { name: /Due Date/ })
    await user.click(within(header).getByRole("button", { name: /filter/i }))

    await user.click(dayButton("10")!)
    await user.click(dayButton("20")!)
    // The Calendar's own <table>/<tr>/<td> markup (rendered into the
    // popover) also picks up implicit row/cell roles, which would otherwise
    // collide with rowNames()'s query of the data table's rows — close the
    // popover (Escape, per its documented outside-click/Escape behavior)
    // before reading the table back.
    await user.keyboard("{Escape}")

    expect(rowNames()).toEqual(["Write copy", "Audit icons"])
  })

  it("clears the range filter via the Clear control", async () => {
    const user = userEvent.setup()
    renderTable()

    const header = screen.getByRole("columnheader", { name: /Due Date/ })
    await user.click(within(header).getByRole("button", { name: /filter/i }))
    await user.click(dayButton("10")!)
    await user.click(dayButton("20")!)

    await user.click(screen.getByRole("button", { name: /clear/i }))
    await user.keyboard("{Escape}")

    expect(rowNames()).toHaveLength(3)
  })
})

describe("column settings", () => {
  function headerNames() {
    return screen.getAllByRole("columnheader").map((header) => header.textContent)
  }

  it("reveals one pill per column when the columns button is clicked, and hides them again", async () => {
    const user = userEvent.setup()
    renderTable()
    const trigger = screen.getByRole("button", { name: /configure columns/i })

    expect(screen.queryByRole("list", { name: "Columns" })).not.toBeInTheDocument()
    await user.click(trigger)
    expect(trigger).toHaveAttribute("aria-expanded", "true")
    const pills = within(screen.getByRole("list", { name: "Columns" })).getAllByRole("listitem")
    expect(pills.map((pill) => pill.textContent)).toEqual([
      "Task Name",
      "Status",
      "Due Date",
      "Assignee",
    ])

    await user.click(trigger)
    expect(screen.queryByRole("list", { name: "Columns" })).not.toBeInTheDocument()
  })

  it("closes when you click outside it, or press Escape", async () => {
    const user = userEvent.setup()
    renderTable()
    const trigger = screen.getByRole("button", { name: /configure columns/i })

    await user.click(trigger)
    await user.click(screen.getByRole("button", { name: "Hide Status" }))
    expect(screen.getByRole("list", { name: "Columns" })).toBeInTheDocument()

    await user.click(screen.getByRole("heading", { name: "Tasks" }))
    expect(screen.queryByRole("list", { name: "Columns" })).not.toBeInTheDocument()
    expect(trigger).toHaveAttribute("aria-expanded", "false")

    await user.click(trigger)
    await user.keyboard("{Escape}")
    expect(screen.queryByRole("list", { name: "Columns" })).not.toBeInTheDocument()
  })

  it("hides and re-shows a column with its eye button", async () => {
    const user = userEvent.setup()
    renderTable()

    await user.click(screen.getByRole("button", { name: /configure columns/i }))
    await user.click(screen.getByRole("button", { name: "Hide Status" }))
    expect(headerNames()).toEqual(["Task Name", "Due Date", "Assignee"])

    await user.click(screen.getByRole("button", { name: "Show Status" }))
    expect(headerNames()).toEqual(["Task Name", "Status", "Due Date", "Assignee"])
  })

  it("offers Reset only once something has changed, and restores every column", async () => {
    const user = userEvent.setup()
    renderTable()

    await user.click(screen.getByRole("button", { name: /configure columns/i }))
    expect(screen.queryByRole("button", { name: "Reset" })).not.toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Hide Status" }))
    await user.click(screen.getByRole("button", { name: "Hide Due Date" }))
    await user.click(screen.getByRole("button", { name: "Reset" }))

    expect(headerNames()).toEqual(["Task Name", "Status", "Due Date", "Assignee"])
    expect(screen.queryByRole("button", { name: "Reset" })).not.toBeInTheDocument()
  })

  it("won't hide the last visible column", async () => {
    const user = userEvent.setup()
    renderTable()

    await user.click(screen.getByRole("button", { name: /configure columns/i }))
    for (const label of ["Status", "Due Date", "Assignee"]) {
      await user.click(screen.getByRole("button", { name: `Hide ${label}` }))
    }

    expect(screen.getByRole("button", { name: "Hide Task Name" })).toBeDisabled()
  })

  it("leaves the actions column out of the pills", async () => {
    const user = userEvent.setup()
    render(
      <DataTable
        columns={[...COLUMNS, { key: "actions", label: "Actions", type: "actions", actions: [] }]}
        data={TASKS}
        getRowId={(task) => task.id}
      />,
    )

    await user.click(screen.getByRole("button", { name: /configure columns/i }))

    expect(screen.queryByRole("button", { name: "Hide Actions" })).not.toBeInTheDocument()
  })
})
