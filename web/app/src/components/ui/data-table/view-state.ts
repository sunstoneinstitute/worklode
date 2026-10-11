import type { Updater } from "@tanstack/react-table"
import type { TableViewState } from "./types"

export const EMPTY_VIEW_STATE: TableViewState = { sorting: [], columnFilters: [], globalFilter: "" }

/** TanStack hands state changes over as a value or a function of the old value */
export function resolveUpdater<V>(updater: Updater<V>, current: V): V {
  return typeof updater === "function" ? (updater as (old: V) => V)(current) : updater
}
