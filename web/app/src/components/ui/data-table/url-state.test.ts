import { describe, expect, it } from "vitest"
import type { DataTableColumn } from "./types"
import { coerceSearch, searchFromViewState, tableSearchKeys, viewStateFromSearch } from "./url-state"

type Row = { id: string; name: string; status: string; owner: string; due: string }

const COLUMNS: DataTableColumn<Row>[] = [
  { key: "name", label: "Name", type: "text", filterable: false },
  {
    key: "status",
    label: "Status",
    type: "select",
    options: [
      { value: "todo", label: "To Do" },
      { value: "blocked", label: "Blocked" },
    ],
  },
  { key: "owner", label: "Owner", type: "user" },
  { key: "due", label: "Due", type: "date" },
  { key: "actions", label: "", type: "actions", actions: [] },
]

describe("coerceSearch", () => {
  it("keeps strings, turns numbers and booleans into strings, drops everything else", () => {
    expect(coerceSearch({ a: "x", b: 123, c: true, d: { x: 1 }, e: ["y"], f: null })).toEqual({
      a: "x",
      b: "123",
      c: "true",
    })
  })
})

describe("viewStateFromSearch", () => {
  it("is empty for an empty search", () => {
    expect(viewStateFromSearch({}, COLUMNS)).toEqual({ sorting: [], columnFilters: [], globalFilter: "" })
  })

  it("reads sort, search and filters of every kind", () => {
    const state = viewStateFromSearch(
      { sort: "due.desc", q: "launch", status: "blocked,todo", owner: "u1,u2", due: "2026-09-01..2026-09-30" },
      COLUMNS,
    )
    expect(state).toEqual({
      sorting: [{ id: "due", desc: true }],
      globalFilter: "launch",
      columnFilters: [
        { id: "status", value: ["blocked", "todo"] },
        { id: "owner", value: ["u1", "u2"] },
        { id: "due", value: { from: "2026-09-01", to: "2026-09-30" } },
      ],
    })
  })

  it("accepts a date range open at either end", () => {
    expect(viewStateFromSearch({ due: "..2026-09-30" }, COLUMNS).columnFilters).toEqual([
      { id: "due", value: { to: "2026-09-30" } },
    ])
    expect(viewStateFromSearch({ due: "2026-09-01.." }, COLUMNS).columnFilters).toEqual([
      { id: "due", value: { from: "2026-09-01" } },
    ])
  })

  it("coerces a number or boolean typed into the URL into search text", () => {
    expect(viewStateFromSearch({ q: 123 }, COLUMNS).globalFilter).toBe("123")
    expect(viewStateFromSearch({ q: true }, COLUMNS).globalFilter).toBe("true")
  })

  it("drops invalid values instead of failing", () => {
    const state = viewStateFromSearch(
      {
        sort: "nope.asc",
        status: "blocked,bogus",
        due: "yesterday..tomorrow",
        name: "filtered", // name isn't filterable
        actions: "x", // nor is actions
        participants: "u1", // not a column here
      },
      COLUMNS,
    )
    expect(state).toEqual({ sorting: [], globalFilter: "", columnFilters: [{ id: "status", value: ["blocked"] }] })
    expect(viewStateFromSearch({ sort: "due.sideways" }, COLUMNS).sorting).toEqual([])
    expect(viewStateFromSearch({ sort: "due" }, COLUMNS).sorting).toEqual([])
    expect(viewStateFromSearch({ status: "bogus" }, COLUMNS).columnFilters).toEqual([])
  })
})

describe("searchFromViewState", () => {
  it("writes only what is set", () => {
    expect(searchFromViewState({ sorting: [], columnFilters: [], globalFilter: "" }, COLUMNS)).toEqual({})
  })

  it("writes sort, search and filters", () => {
    expect(
      searchFromViewState(
        {
          sorting: [{ id: "name", desc: false }],
          globalFilter: "launch",
          columnFilters: [
            { id: "status", value: ["blocked"] },
            { id: "due", value: { to: "2026-09-30" } },
          ],
        },
        COLUMNS,
      ),
    ).toEqual({ sort: "name.asc", q: "launch", status: "blocked", due: "..2026-09-30" })
  })

  it("round-trips", () => {
    const search = { sort: "due.desc", q: "x", status: "todo", owner: "u2", due: "2026-09-01.." }
    expect(searchFromViewState(viewStateFromSearch(search, COLUMNS), COLUMNS)).toEqual(search)
  })
})

describe("tableSearchKeys", () => {
  it("lists sort, q and every filterable column", () => {
    expect(tableSearchKeys(COLUMNS)).toEqual(["sort", "q", "status", "owner", "due"])
  })
})
