import { useCallback, useMemo, useRef, useState } from "react"
import {
  type VisibilityState,
  flexRender,
  getCoreRowModel,
  getFilteredRowModel,
  getSortedRowModel,
  useReactTable,
} from "@tanstack/react-table"
import { SwapVerticalIcon } from "@/components/icons"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { buildColumns, displayText } from "./build-columns"
import { ColumnFilter, DataTableToolbar } from "./toolbar"
import { EMPTY_VIEW_STATE, resolveUpdater } from "./view-state"
import type { DataTableColumn, DataTableProps, TableViewState } from "./types"

export function DataTable<T>({
  columns,
  data,
  getRowId,
  users = [],
  pickableUsers,
  title,
  titleIcon,
  toolbarActions,
  onChange,
  toolbarBelow,
  rowFilter,
  viewState: controlledViewState,
  onViewStateChange,
  onUpdateRow,
  onRemoveRow,
  onAddRow,
}: DataTableProps<T>) {
  const controlledRows =
    onUpdateRow !== undefined || onRemoveRow !== undefined || onAddRow !== undefined
  // Uncontrolled tables keep a copy of their starting data and edit that
  const [ownRows, setOwnRows] = useState(data)
  const rows = controlledRows ? data : ownRows
  // Sort, filters and search: the page's, when it passes them in; otherwise our own
  const [ownViewState, setOwnViewState] = useState<TableViewState>(EMPTY_VIEW_STATE)
  const viewState = controlledViewState ?? ownViewState
  const setViewState = (next: TableViewState) => {
    if (controlledViewState === undefined) setOwnViewState(next)
    onViewStateChange?.(next)
  }
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({})
  const [columnOrder, setColumnOrder] = useState<string[]>([])

  const commit = useCallback(
    (next: T[]) => {
      setOwnRows(next)
      onChange?.(next)
    },
    [onChange],
  )

  // Everything updateRow/removeRow/addRow need besides getRowId, read
  // through a ref rather than closed over. That keeps those three callbacks
  // — and so columnDefs below, and so every cell's component identity —
  // stable across a rows/onUpdateRow/onRemoveRow/onAddRow/commit change,
  // which otherwise remounted every cell (closing an open picker or delete
  // dialog) on every unrelated data change in controlled mode.
  const latest = useRef({ controlledRows, rows, onUpdateRow, onRemoveRow, onAddRow, commit, users, pickableUsers })
  latest.current = { controlledRows, rows, onUpdateRow, onRemoveRow, onAddRow, commit, users, pickableUsers }

  const updateRow = useCallback(
    (rowId: string, patch: Partial<T>) => {
      const { controlledRows, onUpdateRow, commit, rows } = latest.current
      if (controlledRows) onUpdateRow?.(rowId, patch)
      else commit(rows.map((row) => (getRowId(row) === rowId ? { ...row, ...patch } : row)))
    },
    [getRowId],
  )

  const removeRow = useCallback(
    (rowId: string) => {
      const { controlledRows, onRemoveRow, commit, rows } = latest.current
      if (controlledRows) onRemoveRow?.(rowId)
      else commit(rows.filter((row) => getRowId(row) !== rowId))
    },
    [getRowId],
  )

  const addRow = useCallback((row: T) => {
    const { controlledRows, onAddRow, commit, rows } = latest.current
    if (controlledRows) onAddRow?.(row)
    else commit([...rows, row])
  }, [])

  // Read through the ref for the same reason as the callbacks above: a page
  // rebuilds its per-row lists whenever crews load, and that must not
  // rebuild every column
  const pickableFor = useCallback((row: T) => {
    const { pickableUsers, users } = latest.current
    return pickableUsers ? pickableUsers(row) : users
  }, [])

  const columnDefs = useMemo(
    () => buildColumns(columns, { users, getRowId, updateRow, removeRow, addRow, pickableUsers: pickableFor }),
    [columns, users, getRowId, updateRow, removeRow, addRow, pickableFor],
  )

  // Schema lookup by column id, so globalFilterFn (which only gets a
  // columnId, not the schema) knows how to render each column's value —
  // same as buildColumns does per-column via `column` in its closure.
  const columnsByKey = useMemo(
    () => new Map(columns.map((column) => [column.key, column] as const)),
    [columns],
  )

  const visibleRows = useMemo(() => (rowFilter ? rows.filter(rowFilter) : rows), [rows, rowFilter])

  const table = useReactTable({
    data: visibleRows,
    columns: columnDefs,
    state: { ...viewState, columnVisibility, columnOrder },
    onSortingChange: (updater) =>
      setViewState({ ...viewState, sorting: resolveUpdater(updater, viewState.sorting) }),
    onColumnFiltersChange: (updater) =>
      setViewState({ ...viewState, columnFilters: resolveUpdater(updater, viewState.columnFilters) }),
    onGlobalFilterChange: (updater) =>
      setViewState({ ...viewState, globalFilter: resolveUpdater(updater, viewState.globalFilter) }),
    onColumnVisibilityChange: setColumnVisibility,
    onColumnOrderChange: setColumnOrder,
    getRowId: (row) => getRowId(row),
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    // Search visible cell text (the formatted date, the assignee's name,
    // the status label), not the raw accessor value — TanStack's default
    // (includesString) runs over the raw value, which the user never sees.
    globalFilterFn: (row, columnId, value: string) => {
      const column = columnsByKey.get(columnId)
      if (!column) return false
      return displayText(row.getValue(columnId), column, users)
        .toLowerCase()
        .includes(String(value).toLowerCase())
    },
    // Required alongside globalFilterFn above: TanStack's default
    // getColumnCanGlobalFilter excludes non-string/number columns
    // (select/date/user) regardless of a custom filter fn.
    getColumnCanGlobalFilter: (column) =>
      (column.columnDef.meta as { column: DataTableColumn<T> })?.column.type !== "actions",
  })

  return (
    <section className="flex w-full flex-col items-start gap-4">
      <DataTableToolbar
        table={table}
        title={title}
        titleIcon={titleIcon}
        toolbarActions={toolbarActions}
        addRow={addRow}
      />
      {toolbarBelow}
      {/* Mobile: bleeds past the page's 20px margin (same trick as
          ProjectQuickLinks — negative margin cancels the parent's padding,
          matching px re-adds it as scroll padding) so the table can use the
          full narrow width, and drops the rounded corners that would look
          odd flush against the viewport edge. Desktop: back to the normal
          70px-margin-respecting, rounded box. */}
      <div className="-mx-5 w-[calc(100%+40px)] overflow-hidden rounded-none lg:mx-0 lg:w-full lg:rounded-lg">
        <Table className="w-full min-w-250">
          <TableHeader>
            {table.getHeaderGroups().map((headerGroup) => (
              <TableRow key={headerGroup.id} className="border-b border-text-primary">
                {headerGroup.headers.map((header, headerIndex) => {
                  const canSort = header.column.getCanSort()
                  const sortState = header.column.getIsSorted()
                  return (
                    <TableHead
                      key={header.id}
                      onClick={canSort ? header.column.getToggleSortingHandler() : undefined}
                      // ARIA guidance: only sortable headers should carry
                      // aria-sort — non-sortable ones (e.g. actions) get none.
                      aria-sort={
                        canSort
                          ? sortState === "asc"
                            ? "ascending"
                            : sortState === "desc"
                              ? "descending"
                              : "none"
                          : undefined
                      }
                      className={[
                        (header.column.columnDef.meta as { column: DataTableColumn<T> }).column
                          .width,
                        "px-4",
                        canSort ? "cursor-pointer select-none" : "",
                        // First column stays put while the rest of the table
                        // scrolls horizontally on mobile — desktop has no
                        // horizontal scroll to begin with, so it's inert there.
                        headerIndex === 0
                          ? "sticky left-0 z-10 bg-background lg:static lg:bg-transparent"
                          : "",
                      ]
                        .filter(Boolean)
                        .join(" ")}
                    >
                      <span className="flex items-center gap-2.5 text-sm font-semibold text-text-primary">
                        {flexRender(header.column.columnDef.header, header.getContext())}
                        {canSort ? (
                          <span
                            data-sort-state={sortState || "none"}
                            className={
                              sortState === "asc"
                                ? "text-highlight"
                                : sortState === "desc"
                                  ? "rotate-180 text-highlight"
                                  : "text-text-primary"
                            }
                          >
                            <SwapVerticalIcon size={20} />
                          </span>
                        ) : null}
                        {header.column.getCanFilter() ? (
                          <ColumnFilter
                            column={header.column}
                            schema={
                              (header.column.columnDef.meta as { column: DataTableColumn<T> })
                                .column
                            }
                            users={users}
                          />
                        ) : null}
                      </span>
                    </TableHead>
                  )
                })}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {table.getRowModel().rows.map((row) => (
              // data-row-id / data-column-id let consumers find a specific
              // cell on screen (e.g. TaskList's done-status star burst).
              <TableRow key={row.id} data-row-id={row.id} className="border-b border-divider-line/40">
                {row.getVisibleCells().map((cell, cellIndex) => (
                  <TableCell
                    key={cell.id}
                    data-column-id={cell.column.id}
                    className={[
                      "px-4 py-3.5 text-sm text-text-primary",
                      cellIndex === 0
                        ? "sticky left-0 z-10 bg-background lg:static lg:bg-transparent"
                        : "",
                    ]
                      .filter(Boolean)
                      .join(" ")}
                  >
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </TableCell>
                ))}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </section>
  )
}
