import type { ReactNode } from "react"
import { render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { buildColumns, displayText } from "./build-columns"
import type { DataTableColumn } from "./types"

type Row = { id: string; name: string; due: string; status: string }

const ctx = {
  users: [],
  getRowId: (row: Row) => row.id,
  updateRow: vi.fn(),
  removeRow: vi.fn(),
  addRow: vi.fn(),
  pickableUsers: () => [],
}

describe("buildColumns", () => {
  it("maps each schema column to a column def, preserving order", () => {
    const schema: DataTableColumn<Row>[] = [
      { key: "name", label: "Task Name", type: "text" },
      { key: "due", label: "Due Date", type: "date" },
    ]

    const defs = buildColumns(schema, ctx)

    expect(defs.map((def) => def.id)).toEqual(["name", "due"])
    expect(defs.map((def) => def.header)).toEqual(["Task Name", "Due Date"])
  })

  it("disables sorting and filtering on an actions column", () => {
    const schema: DataTableColumn<Row>[] = [
      { key: "actions", label: "", type: "actions", actions: [] },
    ]

    const [def] = buildColumns(schema, ctx)

    expect(def.enableSorting).toBe(false)
    expect(def.enableColumnFilter).toBe(false)
  })

  it("respects an explicit sortable: false on a data column", () => {
    const schema: DataTableColumn<Row>[] = [
      { key: "name", label: "Task Name", type: "text", sortable: false },
    ]

    expect(buildColumns(schema, ctx)[0].enableSorting).toBe(false)
  })

  it("sorts data columns by default", () => {
    const schema: DataTableColumn<Row>[] = [{ key: "name", label: "Name", type: "text" }]

    expect(buildColumns(schema, ctx)[0].enableSorting).toBe(true)
  })

  it("uses a custom column's own render function", () => {
    const schema: DataTableColumn<Row>[] = [
      {
        key: "badge",
        label: "Badge",
        type: "custom",
        render: (row) => <span>custom:{row.name}</span>,
      },
    ]

    const [def] = buildColumns(schema, ctx)
    const cell = def.cell as (ctx: { row: { original: Row } }) => ReactNode
    render(<>{cell({ row: { original: { id: "r1", name: "Ada", due: "", status: "" } } })}</>)

    expect(screen.getByText("custom:Ada")).toBeInTheDocument()
  })

  it("lets render override a built-in type", () => {
    const schema: DataTableColumn<Row>[] = [
      { key: "status", label: "Status", type: "select", options: [], render: () => <span>overridden</span> },
    ]

    const [def] = buildColumns(schema, ctx)
    const cell = def.cell as (ctx: { row: { original: Row } }) => ReactNode
    render(<>{cell({ row: { original: { id: "r1", name: "Ada", due: "", status: "todo" } } })}</>)

    expect(screen.getByText("overridden")).toBeInTheDocument()
  })

  it("defaults sorting and filtering to off on a custom column, which has no backing field", () => {
    const schema: DataTableColumn<Row>[] = [
      { key: "badge", label: "Badge", type: "custom", render: () => <span>badge</span> },
    ]

    const [def] = buildColumns(schema, ctx)

    expect(def.enableSorting).toBe(false)
    expect(def.enableColumnFilter).toBe(false)
  })

  it("respects an explicit sortable/filterable opt-in on a custom column", () => {
    const schema: DataTableColumn<Row>[] = [
      {
        key: "badge",
        label: "Badge",
        type: "custom",
        render: () => <span>badge</span>,
        sortable: true,
        filterable: true,
      },
    ]

    const [def] = buildColumns(schema, ctx)

    expect(def.enableSorting).toBe(true)
    expect(def.enableColumnFilter).toBe(true)
  })
})

describe("select column sorting", () => {
  // The bug this guards: with no sortingFn, TanStack's default string sort
  // applies to the raw value — "High, High, Low, Medium" alphabetically,
  // not Low/Medium/High as the schema's options declare.
  it("sorts by the schema's declared option order, not alphabetically", () => {
    const schema: DataTableColumn<Row>[] = [
      {
        key: "status",
        label: "Priority",
        type: "select",
        options: [
          { value: "low", label: "Low" },
          { value: "medium", label: "Medium" },
          { value: "high", label: "High" },
        ],
      },
    ]
    const [def] = buildColumns(schema, ctx)
    const sortingFn = def.sortingFn as (a: unknown, b: unknown, id: string) => number

    const rows = ["high", "high", "low", "medium"].map((status) => ({ getValue: () => status }))
    const sorted = [...rows].sort((a, b) => sortingFn(a, b, "status")).map((row) => row.getValue())

    expect(sorted).toEqual(["low", "medium", "high", "high"])
  })

  it("sorts an unmatched value last", () => {
    const schema: DataTableColumn<Row>[] = [
      {
        key: "status",
        label: "Priority",
        type: "select",
        options: [
          { value: "low", label: "Low" },
          { value: "high", label: "High" },
        ],
      },
    ]
    const [def] = buildColumns(schema, ctx)
    const sortingFn = def.sortingFn as (a: unknown, b: unknown, id: string) => number

    const rows = ["unknown", "high", "low"].map((status) => ({ getValue: () => status }))
    const sorted = [...rows].sort((a, b) => sortingFn(a, b, "status")).map((row) => row.getValue())

    expect(sorted).toEqual(["low", "high", "unknown"])
  })
})

describe("select column filtering", () => {
  // The multiselect filter UI stores an array of currently-checked option
  // values; a row matches if its value is in that array (OR semantics), not
  // a single exact match and not a substring match.
  function makeFilterFn() {
    const schema: DataTableColumn<Row>[] = [
      {
        key: "status",
        label: "Status",
        type: "select",
        options: [
          { value: "review", label: "Review" },
          { value: "in-review", label: "In Review" },
        ],
      },
    ]
    const [def] = buildColumns(schema, ctx)
    return def.filterFn as unknown as (
      row: { getValue: () => unknown },
      id: string,
      value: string[],
      addMeta: () => void,
    ) => boolean
  }

  it("matches a row whose value is in the selected set", () => {
    const filterFn = makeFilterFn()
    const review = { getValue: () => "review" }

    expect(filterFn(review, "status", ["review"], () => {})).toBe(true)
  })

  // The bug this guards: a naive substring check would match "review"
  // against the value "in-review".
  it("matches exactly, not as a substring of another option's value", () => {
    const filterFn = makeFilterFn()
    const inReview = { getValue: () => "in-review" }

    expect(filterFn(inReview, "status", ["review"], () => {})).toBe(false)
  })

  it("matches a row against ANY of several selected values", () => {
    const filterFn = makeFilterFn()
    const inReview = { getValue: () => "in-review" }

    expect(filterFn(inReview, "status", ["review", "in-review"], () => {})).toBe(true)
  })
})

describe("user column filtering", () => {
  type UserRow = { id: string; assignee: string }
  const userCtx = {
    users: [
      { id: "u1", name: "Ada" },
      { id: "u2", name: "Grace" },
    ],
    getRowId: (row: UserRow) => row.id,
    updateRow: vi.fn(),
    removeRow: vi.fn(),
    addRow: vi.fn(),
    pickableUsers: () => [],
  }

  // The multiselect filter UI picks specific people (by id) from the roster
  // rather than typing a name, so this is a direct id-membership check, not
  // a substring match against the resolved name.
  it("matches a row's assignee id against the selected id set", () => {
    const schema: DataTableColumn<UserRow>[] = [
      { key: "assignee", label: "Assignee", type: "user" },
    ]
    const [def] = buildColumns(schema, userCtx)
    const filterFn = def.filterFn as unknown as (
      row: { getValue: () => unknown },
      id: string,
      value: string[],
      addMeta: () => void,
    ) => boolean

    const adaRow = { getValue: () => "u1" }
    const graceRow = { getValue: () => "u2" }

    expect(filterFn(adaRow, "assignee", ["u1"], () => {})).toBe(true)
    expect(filterFn(graceRow, "assignee", ["u1"], () => {})).toBe(false)
    expect(filterFn(graceRow, "assignee", ["u1", "u2"], () => {})).toBe(true)
  })
})

describe("date column filtering", () => {
  function makeFilterFn() {
    const schema: DataTableColumn<Row>[] = [{ key: "due", label: "Due", type: "date" }]
    const [def] = buildColumns(schema, ctx)
    return def.filterFn as unknown as (
      row: { getValue: () => unknown },
      id: string,
      value: { from?: string; to?: string },
      addMeta: () => void,
    ) => boolean
  }

  it("matches a date within an inclusive from/to range", () => {
    const filterFn = makeFilterFn()
    const row = { getValue: () => "2026-09-20" }

    expect(filterFn(row, "due", { from: "2026-09-01", to: "2026-09-30" }, () => {})).toBe(true)
  })

  it("excludes a date before the range start", () => {
    const filterFn = makeFilterFn()
    const row = { getValue: () => "2026-08-31" }

    expect(filterFn(row, "due", { from: "2026-09-01", to: "2026-09-30" }, () => {})).toBe(false)
  })

  it("excludes a date after the range end", () => {
    const filterFn = makeFilterFn()
    const row = { getValue: () => "2026-10-01" }

    expect(filterFn(row, "due", { from: "2026-09-01", to: "2026-09-30" }, () => {})).toBe(false)
  })

  it("supports an open-ended range (from only, or to only)", () => {
    const filterFn = makeFilterFn()
    const row = { getValue: () => "2026-09-20" }

    expect(filterFn(row, "due", { from: "2026-09-01" }, () => {})).toBe(true)
    expect(filterFn(row, "due", { to: "2026-09-30" }, () => {})).toBe(true)
    expect(filterFn(row, "due", { from: "2026-10-01" }, () => {})).toBe(false)
  })

  // No due date can never fall within a range, regardless of bounds.
  it("never matches a row with no due date", () => {
    const filterFn = makeFilterFn()
    const row = { getValue: () => "" }

    expect(filterFn(row, "due", {}, () => {})).toBe(false)
  })
})

describe("date column sorting", () => {
  // The bug this guards: "Oct 2, 2026" sorts BEFORE "Sept 15, 2026"
  // lexicographically. ISO values must compare chronologically.
  function sortDates(values: string[]) {
    const schema: DataTableColumn<Row>[] = [{ key: "due", label: "Due", type: "date" }]
    const [def] = buildColumns(schema, ctx)
    const sortingFn = def.sortingFn as (a: unknown, b: unknown, id: string) => number

    const rows = values.map((due) => ({ getValue: () => due }))
    return [...rows].sort((a, b) => sortingFn(a, b, "due")).map((row) => row.getValue())
  }

  it("orders September before October", () => {
    expect(sortDates(["2026-10-02", "2026-09-15"])).toEqual(["2026-09-15", "2026-10-02"])
  })

  it("orders across year boundaries", () => {
    expect(sortDates(["2027-01-05", "2026-12-30"])).toEqual(["2026-12-30", "2027-01-05"])
  })

  it("puts empty dates last", () => {
    expect(sortDates(["", "2026-09-15"])).toEqual(["2026-09-15", ""])
  })
})

describe("displayText unknownLabel", () => {
  it("uses unknownLabel for a value that is not an option", () => {
    const column = { key: "p", label: "Project", type: "select", options: [], unknownLabel: "Deleted project" } as never
    expect(displayText("p-gone", column, [])).toBe("Deleted project")
    expect(displayText("", column, [])).toBe("")
  })
})
