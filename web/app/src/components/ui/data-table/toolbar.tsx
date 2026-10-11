import { type ReactNode, useEffect, useRef, useState } from "react"
import type { Column, Table } from "@tanstack/react-table"
import type { DateRange } from "react-day-picker"
import { CheckIcon, CloseIcon, FilterIcon, SearchIcon, ViewColumnIcon } from "@/components/icons"
import { Calendar } from "@/components/ui/calendar"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { toIsoDate, userHandle } from "./cells"
import { ColumnSettings } from "./column-settings"
import type { DataTableColumn, DataTableUser, TableHelpers } from "./types"

export function DataTableToolbar<T>({
  table,
  title,
  titleIcon,
  toolbarActions,
  addRow,
}: {
  table: Table<T>
  title?: string
  titleIcon?: ReactNode
  toolbarActions?: ReactNode | ((helpers: TableHelpers<T>) => ReactNode)
  addRow: (row: T) => void
}) {
  // Open from the start when there's already a search (from the URL, say)
  const [searchOpen, setSearchOpen] = useState(() => Boolean(table.getState().globalFilter))
  const [columnsOpen, setColumnsOpen] = useState(false)
  const columnsButtonRef = useRef<HTMLButtonElement>(null)
  const columnsPanelRef = useRef<HTMLDivElement>(null)

  // Close the column settings on a click/tap anywhere outside the pills and
  // their toggle button (the button handles its own toggling), or on Escape.
  useEffect(() => {
    if (!columnsOpen) return
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target as Node
      if (columnsPanelRef.current?.contains(target) || columnsButtonRef.current?.contains(target))
        return
      setColumnsOpen(false)
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setColumnsOpen(false)
    }
    document.addEventListener("pointerdown", onPointerDown)
    document.addEventListener("keydown", onKeyDown)
    return () => {
      document.removeEventListener("pointerdown", onPointerDown)
      document.removeEventListener("keydown", onKeyDown)
    }
  }, [columnsOpen])
  const actions =
    typeof toolbarActions === "function" ? toolbarActions({ addRow }) : toolbarActions

  return (
    <div className="flex w-full flex-col gap-3">
      <div className="flex w-full items-center gap-4">
        <div className="flex flex-1 items-center gap-5">
          {title ? (
            <div className="flex items-center gap-2.5">
              {titleIcon}
              <h2 className="text-xl text-text-primary md:text-22">{title}</h2>
            </div>
          ) : null}

          {/* Search and column settings sit closer to each other than to the title */}
          <div className="flex items-center gap-3">
            {searchOpen || Boolean(table.getState().globalFilter) ? (
              <div className="flex items-center gap-1 border-b border-divider-line">
                <input
                  type="search"
                  autoFocus
                  aria-label="Search"
                  placeholder="Search..."
                  value={table.getState().globalFilter ?? ""}
                  onChange={(event) => table.setGlobalFilter(event.target.value)}
                  onBlur={(event) => {
                    if (!event.target.value) setSearchOpen(false)
                  }}
                  className="w-50 bg-transparent py-1 text-sm text-text-primary outline-none [&::-webkit-search-cancel-button]:hidden"
                />
                <button
                  type="button"
                  aria-label="Close search"
                  // preventDefault keeps the input from blurring first, which
                  // would close an empty search out from under this click.
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => {
                    table.setGlobalFilter("")
                    setSearchOpen(false)
                  }}
                  className="flex cursor-pointer items-center text-text-primary transition-colors hover:text-highlight"
                >
                  <CloseIcon size={18} />
                </button>
              </div>
            ) : (
              <button
                type="button"
                aria-label="Search"
                onClick={() => setSearchOpen(true)}
                className="flex cursor-pointer items-center text-text-primary transition-colors hover:text-highlight"
              >
                <SearchIcon size={24} />
              </button>
            )}

            <button
              ref={columnsButtonRef}
              type="button"
              aria-label="Configure columns"
              title="Configure columns"
              aria-expanded={columnsOpen}
              onClick={() => setColumnsOpen((open) => !open)}
              className={`flex cursor-pointer items-center transition-colors hover:text-highlight ${columnsOpen ? "text-highlight" : "text-text-primary"}`}
            >
              <ViewColumnIcon size={24} />
            </button>
          </div>
        </div>

        {actions}
      </div>
      {columnsOpen ? (
        <div ref={columnsPanelRef} className="w-full">
          <ColumnSettings table={table} />
        </div>
      ) : null}
    </div>
  )
}

/**
 * Checkbox-style multiselect for a Command list, shared by `select`-type
 * columns (items from `schema.options`) and `user`-type columns (items from
 * the users roster, mapped to `{value: user.id, label: user.name}`) — same
 * value/label shape either way, so one component covers both instead of
 * duplicating the Command-list wiring.
 *
 * Deliberately does not close the popover on item click (unlike the old
 * single-select version) — the user needs to pick several before dismissing
 * it. It still closes via the Popover's own outside-click/Escape handling.
 */
function MultiSelectFilter<T>({
  column,
  items,
}: {
  column: Column<T, unknown>
  items: { value: string; label: string }[]
}) {
  const selected = (column.getFilterValue() as string[] | undefined) ?? []

  const toggle = (value: string) => {
    const next = selected.includes(value)
      ? selected.filter((v) => v !== value)
      : [...selected, value]
    // Never store an empty array — "nothing selected" is represented the
    // same way as "All": an undefined filter value.
    column.setFilterValue(next.length === 0 ? undefined : next)
  }

  return (
    <Command>
      <CommandList>
        <CommandEmpty>No options.</CommandEmpty>
        <CommandGroup>
          <CommandItem value="__all__" onSelect={() => column.setFilterValue(undefined)}>
            <span className="flex w-4 items-center justify-center">
              {selected.length === 0 ? <CheckIcon size={14} /> : null}
            </span>
            All
          </CommandItem>
          {items.map((item) => (
            <CommandItem key={item.value} value={item.value} onSelect={() => toggle(item.value)}>
              <span className="flex w-4 items-center justify-center">
                {selected.includes(item.value) ? <CheckIcon size={14} /> : null}
              </span>
              {item.label}
            </CommandItem>
          ))}
        </CommandGroup>
      </CommandList>
    </Command>
  )
}

/**
 * Range picker for `date`-type columns: a react-day-picker `Calendar` in
 * `mode="range"`, plus a "Clear" control. Live-applies on every selection
 * change (no "Apply" button) and stores ISO date strings, parsed/serialized
 * via `toIsoDate` (exported from `cells.tsx`) so this shares the exact same
 * UTC-safe convention as `DateCell`/`formatIsoDate` rather than duplicating it.
 */
function DateRangeFilter<T>({ column }: { column: Column<T, unknown> }) {
  const filterValue = column.getFilterValue() as { from?: string; to?: string } | undefined
  const selected: DateRange | undefined = filterValue
    ? {
        from: filterValue.from ? new Date(`${filterValue.from}T00:00:00Z`) : undefined,
        to: filterValue.to ? new Date(`${filterValue.to}T00:00:00Z`) : undefined,
      }
    : undefined

  return (
    <div className="flex flex-col gap-1 p-2">
      <Calendar
        mode="range"
        selected={selected}
        onSelect={(range) => {
          if (!range?.from && !range?.to) {
            column.setFilterValue(undefined)
            return
          }
          column.setFilterValue({
            from: range.from ? toIsoDate(range.from) : undefined,
            to: range.to ? toIsoDate(range.to) : undefined,
          })
        }}
      />
      <button
        type="button"
        onClick={() => column.setFilterValue(undefined)}
        className="cursor-pointer self-start px-2 pb-1 text-sm text-text-primary underline"
      >
        Clear
      </button>
    </div>
  )
}

export function ColumnFilter<T>({
  column,
  schema,
  users = [],
}: {
  column: Column<T, unknown>
  schema: DataTableColumn<T>
  users?: DataTableUser[]
}) {
  const [open, setOpen] = useState(false)
  const filterValue = column.getFilterValue()
  const active = Array.isArray(filterValue)
    ? filterValue.length > 0
    : filterValue != null && filterValue !== ""

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={`Filter ${schema.label}`}
          onClick={(event) => event.stopPropagation()}
          className={active ? "cursor-pointer text-[var(--color-accent)]" : "cursor-pointer text-text-primary transition-colors hover:text-highlight"}
        >
          <FilterIcon size={16} />
        </button>
      </PopoverTrigger>
      <PopoverContent
        className={schema.type === "date" ? "w-auto p-0" : "w-48 p-0"}
        align="start"
        onClick={(e) => e.stopPropagation()}
      >
        {schema.type === "select" ? (
          <MultiSelectFilter column={column} items={schema.options ?? []} />
        ) : schema.type === "user" || schema.type === "users" ? (
          <MultiSelectFilter
            column={column}
            items={users.map((user) => ({ value: user.id, label: userHandle(user) }))}
          />
        ) : schema.type === "date" ? (
          <DateRangeFilter column={column} />
        ) : (
          <input
            type="text"
            autoFocus
            aria-label={`Filter ${schema.label}`}
            value={(column.getFilterValue() as string) ?? ""}
            onChange={(event) => column.setFilterValue(event.target.value)}
            className="w-full bg-transparent p-2 text-sm text-text-primary outline-none"
          />
        )}
      </PopoverContent>
    </Popover>
  )
}
