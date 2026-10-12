import type { ReactNode } from "react"
import type { ColumnFiltersState, SortingState } from "@tanstack/react-table"

export type ColumnType =
  | "text"
  | "select"
  | "date"
  | "user"
  /** Several people from the roster — the value is an array of user ids */
  | "users"
  | "actions"
  | "custom"

export interface SelectOption {
  value: string
  label: string
  /** A CSS custom property name, e.g. "--color-accent-warn". Omit for a neutral badge. */
  colorVar?: string
}

export interface DataTableUser {
  id: string
  /** Full name, e.g. "Tobias Smith". */
  name: string
  /**
   * Username shown in the table as "@handle". Optional: without one it's
   * derived from `name` (see userHandle in cells.tsx), "TobiasSmith".
   */
  handle?: string
  avatarUrl?: string
}

/** Available to anything that can mutate the table, including the toolbar. */
export interface TableHelpers<T> {
  addRow: (row: T) => void
}

/**
 * Row actions get the table-scoped helpers plus two bound to their own row.
 * `addRow` is what makes a "duplicate" action possible — the consumer passes
 * the whole new row, because only they can mint an id.
 *
 * **Call exactly ONE of `addRow`/`removeRow`/`updateRow` per `onSelect`
 * invocation.** Each is backed by a `useCallback` in `data-table.tsx` that
 * closes over the current `rows` state and calls `setRows()` non-
 * functionally. Calling two of them in sequence within the same action
 * (e.g. `removeRow()` then `addRow(...)` to implement "replace") silently
 * loses the first call's effect — both closures capture the same stale
 * `rows` snapshot, so only the second call's change survives. Fixing this
 * would mean moving the state plumbing to functional updates or a ref; that
 * hasn't been done, so composing multiple helper calls in one action is not
 * supported yet. In controlled-rows mode each call goes straight to the
 * page's callback, so this limit applies only to uncontrolled tables.
 */
export interface RowActionHelpers<T> extends TableHelpers<T> {
  removeRow: () => void
  updateRow: (patch: Partial<T>) => void
}

export interface RowAction<T> {
  id: string
  /** Used as both aria-label and title. */
  label: string
  icon: ReactNode
  onSelect: (row: T, helpers: RowActionHelpers<T>) => void
  variant?: "default" | "destructive"
  /**
   * Ask before running onSelect, in a ConfirmDialog with this question
   * (plus a small preview of the row). Destructive actions confirm by
   * default ("Are you sure you want to delete this item?"); pass false to
   * skip it, a string to reword it, or a function to word it per row.
   */
  confirm?: string | false | ((row: T) => string)
  /** Show this action only on rows where this returns true (default: all rows) */
  when?: (row: T) => boolean
}

/** The parts of a table's state a page can own — for example, keep in the URL. */
export interface TableViewState {
  sorting: SortingState
  columnFilters: ColumnFiltersState
  globalFilter: string
}

export interface DataTableColumn<T> {
  /**
   * For data-backed types this keys into T. For "actions" and "custom"
   * there is no backing field, so it is just a unique column id.
   */
  key: string
  label: string
  type: ColumnType
  /** Tailwind width utility, e.g. "w-40". */
  width?: string
  /** Required for type "select". */
  options?: SelectOption[]
  /** Required for type "actions". */
  actions?: RowAction<T>[]
  /** Defaults to true, except "actions" which is always false. */
  sortable?: boolean
  /** Defaults to true, except "actions" which is always false. */
  filterable?: boolean
  /** Required for type "custom"; an optional override for any other type. */
  /** Gets the same helpers as row actions, so a custom cell can edit its row */
  render?: (row: T, helpers: RowActionHelpers<T>) => ReactNode
}

export interface DataTableProps<T> {
  columns: DataTableColumn<T>[]
  data: T[]
  getRowId: (row: T) => string
  /** Roster for "user"-type columns. */
  users?: DataTableUser[]
  /**
   * Who a "user" or "users" cell's picker offers, per row: for example the
   * crew of the row's project. `users` stays the list names are shown
   * from, so someone outside that list still shows where they're
   * assigned. Without this, pickers offer `users`.
   */
  pickableUsers?: (row: T) => DataTableUser[]
  title?: string
  titleIcon?: ReactNode
  /**
   * Content-type-specific toolbar buttons, e.g. "New Task". Takes a render
   * prop when the button needs to mutate the table — which "New" does.
   */
  toolbarActions?: ReactNode | ((helpers: TableHelpers<T>) => ReactNode)
  /** Fires after any inline edit or row-action mutation. */
  onChange?: (data: T[]) => void
  /**
   * Content rendered under the toolbar row, above the table — e.g. a chip
   * navigation that narrows what the table shows.
   */
  toolbarBelow?: ReactNode
  /**
   * Narrows which rows the table shows, on top of its own search/filters.
   * Mutations (edit, delete, add) still apply to the full data set, so
   * switching the filter back never loses rows.
   */
  rowFilter?: (row: T) => boolean
  /**
   * Sort, column filters and search, owned by the page. Omit both to let
   * the table keep its own. When set, the table shows exactly this and
   * reports every change through onViewStateChange.
   */
  viewState?: TableViewState
  onViewStateChange?: (next: TableViewState) => void
  /**
   * Controlled rows: pass these and the table shows `data` exactly as
   * given, reporting edits, deletes and adds here instead of changing its
   * own copy. The page updates `data` (usually through a query and a
   * mutation). Leave all three out and the table keeps its own copy.
   */
  onUpdateRow?: (rowId: string, patch: Partial<T>) => void
  onRemoveRow?: (rowId: string) => void
  /**
   * In controlled mode (any of `onUpdateRow`/`onRemoveRow`/`onAddRow`
   * passed), `addRow` — the toolbar's "New" and any row action's `addRow`
   * helper — does nothing unless this is also passed.
   */
  onAddRow?: (row: T) => void
}
