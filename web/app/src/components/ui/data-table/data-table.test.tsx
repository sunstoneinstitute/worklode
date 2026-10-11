import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { DataTable } from "./data-table"
import type { DataTableColumn, DataTableProps } from "./types"

type Task = { id: string; name: string; due: string; status: string; assignee: string }

const USERS = [
  { id: "u1", name: "Claudicia" },
  { id: "u2", name: "Marcus" },
]

const TASKS: Task[] = [
  { id: "t1", name: "Write copy", due: "2026-10-02", status: "todo", assignee: "u1" },
  { id: "t2", name: "Audit icons", due: "2026-09-15", status: "done", assignee: "u2" },
  { id: "t3", name: "Ship export", due: "2026-11-20", status: "todo", assignee: "u1" },
]

const COLUMNS: DataTableColumn<Task>[] = [
  { key: "name", label: "Task Name", type: "text" },
  { key: "assignee", label: "Assignee", type: "user" },
  { key: "due", label: "Due Date", type: "date" },
  {
    key: "status",
    label: "Status",
    type: "select",
    options: [
      { value: "todo", label: "To Do" },
      { value: "done", label: "Done", colorVar: "--color-accent-success" },
    ],
  },
]

function renderTable(overrides: Partial<DataTableProps<Task>> = {}) {
  return render(
    <DataTable
      columns={COLUMNS}
      data={TASKS}
      getRowId={(task) => task.id}
      users={USERS}
      {...overrides}
    />,
  )
}

function rowNames() {
  const [, ...bodyRows] = screen.getAllByRole("row")
  return bodyRows.map((row) => within(row).getAllByRole("cell")[0].textContent)
}

describe("DataTable rendering", () => {
  it("renders a header per schema column", () => {
    renderTable()

    for (const column of COLUMNS) {
      expect(screen.getByRole("columnheader", { name: new RegExp(column.label) })).toBeInTheDocument()
    }
  })

  it("renders a row per data item", () => {
    renderTable()

    expect(rowNames()).toEqual(["Write copy", "Audit icons", "Ship export"])
  })

  it("resolves user ids to @handles", () => {
    renderTable()

    expect(screen.getAllByText("@Claudicia")).toHaveLength(2)
  })
})

describe("sorting", () => {
  it("sorts ascending on first header click", async () => {
    const user = userEvent.setup()
    renderTable()

    await user.click(screen.getByRole("columnheader", { name: /Task Name/ }))

    expect(rowNames()).toEqual(["Audit icons", "Ship export", "Write copy"])
  })

  it("sorts descending on second click", async () => {
    const user = userEvent.setup()
    renderTable()

    const header = screen.getByRole("columnheader", { name: /Task Name/ })
    await user.click(header)
    await user.click(header)

    expect(rowNames()).toEqual(["Write copy", "Ship export", "Audit icons"])
  })

  it("clears sorting on third click", async () => {
    const user = userEvent.setup()
    renderTable()

    const header = screen.getByRole("columnheader", { name: /Task Name/ })
    await user.click(header)
    await user.click(header)
    await user.click(header)

    expect(rowNames()).toEqual(["Write copy", "Audit icons", "Ship export"])
  })

  it("sorts dates chronologically, not lexicographically", async () => {
    const user = userEvent.setup()
    renderTable()

    await user.click(screen.getByRole("columnheader", { name: /Due Date/ }))

    // Sept before Oct before Nov — the string sort would put Nov first.
    expect(rowNames()).toEqual(["Audit icons", "Write copy", "Ship export"])
  })

  it("does not sort an actions column", async () => {
    const user = userEvent.setup()
    renderTable({
      columns: [
        ...COLUMNS,
        { key: "actions", label: "Actions", type: "actions", actions: [] },
      ],
    })

    await user.click(screen.getByRole("columnheader", { name: /Actions/ }))

    expect(rowNames()).toEqual(["Write copy", "Audit icons", "Ship export"])
  })

  it("omits aria-sort on a non-sortable header but sets it on sortable ones", () => {
    renderTable({
      columns: [
        ...COLUMNS,
        { key: "actions", label: "Actions", type: "actions", actions: [] },
      ],
    })

    expect(
      screen.getByRole("columnheader", { name: /Task Name/ }).getAttribute("aria-sort"),
    ).toBe("none")
    expect(
      screen.getByRole("columnheader", { name: /Actions/ }).hasAttribute("aria-sort"),
    ).toBe(false)
  })
})

describe("sort indicator", () => {
  it("shows a distinct, DOM-inspectable state per click: neutral, asc, desc, neutral", async () => {
    const user = userEvent.setup()
    renderTable()

    const header = screen.getByRole("columnheader", { name: /Task Name/ })
    const indicatorState = () => header.querySelector("[data-sort-state]")?.getAttribute("data-sort-state")

    expect(indicatorState()).toBe("none")

    await user.click(header)
    expect(indicatorState()).toBe("asc")

    await user.click(header)
    expect(indicatorState()).toBe("desc")

    await user.click(header)
    expect(indicatorState()).toBe("none")
  })
})

describe("inline editing", () => {
  it("updates the row and fires onChange when a select value changes", async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderTable({ onChange })

    const [firstRow] = screen.getAllByRole("row").slice(1)
    await user.click(within(firstRow).getByRole("button", { name: "To Do" }))
    await user.click(screen.getByRole("option", { name: "Done" }))

    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange.mock.calls[0][0]).toContainEqual(
      expect.objectContaining({ id: "t1", status: "done" }),
    )
  })

  it("leaves other rows untouched", async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderTable({ onChange })

    const [firstRow] = screen.getAllByRole("row").slice(1)
    await user.click(within(firstRow).getByRole("button", { name: "To Do" }))
    await user.click(screen.getByRole("option", { name: "Done" }))

    expect(onChange.mock.calls[0][0]).toContainEqual(
      expect.objectContaining({ id: "t2", status: "done" }),
    )
    expect(onChange.mock.calls[0][0]).toContainEqual(
      expect.objectContaining({ id: "t3", status: "todo" }),
    )
  })
})

describe("global search", () => {
  // Guards the bug where TanStack's default global filter ran over the raw
  // accessor value (an internal enum string, a user id, a raw ISO date)
  // instead of what's actually rendered on screen.
  async function search(term: string) {
    const user = userEvent.setup()
    renderTable()
    await user.click(screen.getByRole("button", { name: /search/i }))
    await user.type(screen.getByRole("searchbox"), term)
  }

  it("finds rows by a select column's displayed option label", async () => {
    await search("To Do")

    expect(rowNames()).toEqual(["Write copy", "Ship export"])
  })

  it("finds rows by a user column's displayed name, not the raw id", async () => {
    await search("Marcus")

    expect(rowNames()).toEqual(["Audit icons"])
  })

  it("finds rows by a date column's formatted display text, not the raw ISO string", async () => {
    await search("Sep 15")

    expect(rowNames()).toEqual(["Audit icons"])
  })
})

describe("row actions", () => {
  it("removes the row a delete action was invoked on", async () => {
    const user = userEvent.setup()
    renderTable({
      columns: [
        ...COLUMNS,
        {
          key: "actions",
          label: "Actions",
          type: "actions",
          actions: [
            {
              id: "delete",
              label: "Delete",
              icon: <span>x</span>,
              onSelect: (_row, { removeRow }) => removeRow(),
            },
          ],
        },
      ],
    })

    const [firstRow] = screen.getAllByRole("row").slice(1)
    await user.click(within(firstRow).getByRole("button", { name: "Delete" }))

    expect(rowNames()).toEqual(["Audit icons", "Ship export"])
  })
})

describe("controlled view state", () => {
  const firstColumn = () =>
    screen
      .getAllByRole("row")
      .slice(1)
      .map((row) => within(row).getAllByRole("cell")[0].textContent)

  it("shows the sort it's given", () => {
    renderTable({
      viewState: { sorting: [{ id: "name", desc: false }], columnFilters: [], globalFilter: "" },
      onViewStateChange: () => {},
    })
    expect(firstColumn()).toEqual(["Audit icons", "Ship export", "Write copy"])
  })

  it("reports a header click instead of sorting by itself", async () => {
    const user = userEvent.setup()
    const onViewStateChange = vi.fn()
    renderTable({
      viewState: { sorting: [], columnFilters: [], globalFilter: "" },
      onViewStateChange,
    })

    await user.click(screen.getByRole("columnheader", { name: /Task Name/ }))

    expect(onViewStateChange).toHaveBeenCalledWith({
      sorting: [{ id: "name", desc: false }],
      columnFilters: [],
      globalFilter: "",
    })
    // Unchanged until the page passes the new state back in
    expect(firstColumn()).toEqual(["Write copy", "Audit icons", "Ship export"])
  })

  it("shows the filters and search it's given, with the search box open", () => {
    renderTable({
      viewState: { sorting: [], columnFilters: [{ id: "status", value: ["todo"] }], globalFilter: "Ship" },
      onViewStateChange: () => {},
    })
    expect(firstColumn()).toEqual(["Ship export"])
    expect(screen.getByRole("searchbox")).toHaveValue("Ship")
  })
})

describe("controlled rows", () => {
  const firstColumn = () =>
    screen
      .getAllByRole("row")
      .slice(1)
      .map((row) => within(row).getAllByRole("cell")[0].textContent)

  const DELETE_COLUMNS: DataTableColumn<Task>[] = [
    ...COLUMNS,
    {
      key: "actions",
      label: "",
      type: "actions",
      actions: [
        { id: "delete", label: "Delete task", icon: "×", confirm: false, onSelect: (_task, { removeRow }) => removeRow() },
      ],
    },
  ]

  it("reports an inline edit and shows data only as passed in", async () => {
    const user = userEvent.setup()
    const onUpdateRow = vi.fn()
    const { rerender } = render(
      <DataTable columns={COLUMNS} data={TASKS} getRowId={(task) => task.id} users={USERS} onUpdateRow={onUpdateRow} />,
    )

    const statusCell = screen.getByRole("row", { name: /Write copy/ }).querySelector('[data-column-id="status"]')!
    await user.click(within(statusCell as HTMLElement).getByRole("button"))
    await user.click(screen.getByRole("option", { name: "Done" }))

    expect(onUpdateRow).toHaveBeenCalledWith("t1", { status: "done" })
    // Still To Do: the page hasn't passed new data yet
    expect(within(statusCell as HTMLElement).getByText("To Do")).toBeInTheDocument()

    rerender(
      <DataTable
        columns={COLUMNS}
        data={TASKS.map((task) => (task.id === "t1" ? { ...task, status: "done" } : task))}
        getRowId={(task) => task.id}
        users={USERS}
        onUpdateRow={onUpdateRow}
      />,
    )
    const updated = screen.getByRole("row", { name: /Write copy/ }).querySelector('[data-column-id="status"]')!
    expect(within(updated as HTMLElement).getByText("Done")).toBeInTheDocument()
  })

  it("reports a delete without removing the row itself", async () => {
    const user = userEvent.setup()
    const onRemoveRow = vi.fn()
    render(
      <DataTable columns={DELETE_COLUMNS} data={TASKS} getRowId={(task) => task.id} users={USERS} onRemoveRow={onRemoveRow} />,
    )

    const row = screen.getByRole("row", { name: /Audit icons/ })
    await user.click(within(row).getByRole("button", { name: "Delete task" }))

    expect(onRemoveRow).toHaveBeenCalledWith("t2")
    expect(firstColumn()).toContain("Audit icons")
  })

  it("still works uncontrolled when no row callbacks are passed", async () => {
    const user = userEvent.setup()
    render(<DataTable columns={DELETE_COLUMNS} data={TASKS} getRowId={(task) => task.id} users={USERS} />)
    await user.click(within(screen.getByRole("row", { name: /Audit icons/ })).getByRole("button", { name: "Delete task" }))
    expect(firstColumn()).not.toContain("Audit icons")
  })
})
