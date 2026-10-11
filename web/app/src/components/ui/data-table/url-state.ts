import type { ColumnFiltersState, SortingState } from "@tanstack/react-table"
import type { DataTableColumn, TableViewState } from "./types"

// Converts a table's sort, filters and search to and from URL search
// params, so a page can keep them in the URL. See the format table in
// docs/superpowers/plans/2026-09-24-spa-1-toolchain-routing.md (Task 8).

const SORT_KEY = "sort"
const QUERY_KEY = "q"
/** Never used for a column: sort, search, and the chip category pages use */
const RESERVED = new Set([SORT_KEY, QUERY_KEY, "category"])
const ISO_DATE = /^\d{4}-\d{2}-\d{2}$/

type DateRange = { from?: string; to?: string }

function isFilterable<T>(column: DataTableColumn<T>) {
  return column.type !== "actions" && column.filterable !== false && !RESERVED.has(column.key)
}

function isSortable<T>(column: DataTableColumn<T>) {
  return column.type !== "actions" && column.sortable !== false
}

/**
 * TanStack Router parses `?q=123` as the number 123 and `?q=true` as a
 * boolean. Tables only deal in text, so turn those back into strings, and
 * drop anything that isn't a plain value.
 */
export function coerceSearch(raw: Record<string, unknown>): Record<string, string> {
  const search: Record<string, string> = {}
  for (const [key, value] of Object.entries(raw)) {
    if (typeof value === "string") search[key] = value
    else if (typeof value === "number" || typeof value === "boolean") search[key] = String(value)
  }
  return search
}

function filterFromText<T>(text: string, column: DataTableColumn<T>): unknown {
  switch (column.type) {
    case "select": {
      const allowed = new Set((column.options ?? []).map((option) => option.value))
      const values = text.split(",").filter((value) => allowed.has(value))
      return values.length > 0 ? values : undefined
    }
    case "user":
    case "users": {
      const values = text.split(",").filter(Boolean)
      return values.length > 0 ? values : undefined
    }
    case "date": {
      const [from = "", to = ""] = text.split("..")
      const range: DateRange = {}
      if (ISO_DATE.test(from)) range.from = from
      if (ISO_DATE.test(to)) range.to = to
      return range.from || range.to ? range : undefined
    }
    default:
      return text
  }
}

function filterToText<T>(value: unknown, column: DataTableColumn<T>): string {
  if (Array.isArray(value)) return value.join(",")
  if (column.type === "date" && value && typeof value === "object") {
    const { from, to } = value as DateRange
    return from || to ? `${from ?? ""}..${to ?? ""}` : ""
  }
  return typeof value === "string" ? value : ""
}

/** The table state a URL describes. Anything invalid is left out. */
export function viewStateFromSearch<T>(
  raw: Record<string, unknown>,
  columns: DataTableColumn<T>[],
): TableViewState {
  const search = coerceSearch(raw)

  const sorting: SortingState = []
  const sort = search[SORT_KEY]
  if (sort) {
    const dot = sort.lastIndexOf(".")
    const id = sort.slice(0, dot)
    const direction = sort.slice(dot + 1)
    const column = columns.find((candidate) => candidate.key === id)
    if (dot > 0 && column && isSortable(column) && (direction === "asc" || direction === "desc")) {
      sorting.push({ id, desc: direction === "desc" })
    }
  }

  const columnFilters: ColumnFiltersState = []
  for (const column of columns) {
    const text = search[column.key]
    if (!text || !isFilterable(column)) continue
    const value = filterFromText(text, column)
    if (value !== undefined) columnFilters.push({ id: column.key, value })
  }

  return { sorting, columnFilters, globalFilter: search[QUERY_KEY] ?? "" }
}

/** The search params for a table state; empty parts are left out. */
export function searchFromViewState<T>(
  state: TableViewState,
  columns: DataTableColumn<T>[],
): Record<string, string> {
  const search: Record<string, string> = {}
  // Only the first sort is kept (shift-click multi-sort isn't in the URL)
  const [sort] = state.sorting
  if (sort) search[SORT_KEY] = `${sort.id}.${sort.desc ? "desc" : "asc"}`
  if (state.globalFilter) search[QUERY_KEY] = state.globalFilter
  for (const filter of state.columnFilters) {
    const column = columns.find((candidate) => candidate.key === filter.id)
    if (!column || !isFilterable(column)) continue
    const text = filterToText(filter.value, column)
    if (text) search[column.key] = text
  }
  return search
}

/** Every search key a table with these columns may write */
export function tableSearchKeys<T>(columns: DataTableColumn<T>[]): string[] {
  return [SORT_KEY, QUERY_KEY, ...columns.filter(isFilterable).map((column) => column.key)]
}
