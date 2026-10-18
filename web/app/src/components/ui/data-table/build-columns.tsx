import type { ColumnDef, Row } from "@tanstack/react-table"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import { initials } from "@/lib/initials"
import {
  ActionsCell,
  DateCell,
  SelectCell,
  TextCell,
  UserCell,
  UsersCell,
  badgeStyle,
  formatIsoDate,
  userHandle,
} from "./cells"
import type { DataTableColumn, DataTableUser } from "./types"

export interface BuildColumnsContext<T> {
  users: DataTableUser[]
  getRowId: (row: T) => string
  updateRow: (rowId: string, patch: Partial<T>) => void
  removeRow: (rowId: string) => void
  addRow: (row: T) => void
  /** Who the row's person pickers offer */
  pickableUsers: (row: T) => DataTableUser[]
}

function compareIsoDates<T>(a: Row<T>, b: Row<T>, columnId: string) {
  const left = a.getValue<string>(columnId)
  const right = b.getValue<string>(columnId)
  if (!left) return right ? 1 : 0
  if (!right) return -1
  return left < right ? -1 : left > right ? 1 : 0
}

/**
 * Produces exactly what's rendered on screen for a given column's value —
 * the display text a user actually sees and types, not the raw accessor
 * value (an internal enum string, a user id, a raw ISO date). Shared by the
 * per-column `filterFn`s below and by `DataTable`'s `globalFilterFn`, so
 * "what does this cell show" is defined in exactly one place.
 */
export function displayText<T>(
  value: unknown,
  column: DataTableColumn<T>,
  users: DataTableUser[],
): string {
  switch (column.type) {
    case "date":
      return formatIsoDate((value as string) ?? "")
    case "user": {
      const id = value as string
      const user = users.find((u) => u.id === id)
      return user ? userHandle(user) : ""
    }
    case "users": {
      const ids = (value as string[] | undefined) ?? []
      return users
        .filter((u) => ids.includes(u.id))
        .map(userHandle)
        .join(", ")
    }
    case "select": {
      const option = (column.options ?? []).find((o) => o.value === value)
      if (option) return option.label
      if (value == null || value === "") return ""
      return column.unknownLabel ?? String(value)
    }
    default:
      return value === null || value === undefined ? "" : String(value)
  }
}

function PreviewAvatar({ user }: { user: DataTableUser }) {
  return (
    <Avatar className="size-6" title={userHandle(user)}>
      {user.avatarUrl ? <AvatarImage src={user.avatarUrl} alt="" /> : null}
      <AvatarFallback className="text-xs">{initials(user.name)}</AvatarFallback>
    </Avatar>
  )
}

/**
 * One field of a RowPreview, styled the way the table shows it: option
 * values as the same chips, people as avatar + @handle, several people as
 * an avatar row. Dates and text keep a small "Label:" since a bare value
 * wouldn't say what it is. Null when the field is empty.
 */
function PreviewField<T>({
  value,
  column,
  users,
}: {
  value: unknown
  column: DataTableColumn<T>
  users: DataTableUser[]
}) {
  switch (column.type) {
    case "select": {
      const option = (column.options ?? []).find((o) => o.value === value)
      if (!option) return null
      const { className, style } = badgeStyle(option)
      return (
        <Badge className={className} style={style}>
          {option.label}
        </Badge>
      )
    }
    case "user": {
      const user = users.find((u) => u.id === value)
      if (!user) return null
      return (
        <span className="flex items-center gap-2 text-13">
          <PreviewAvatar user={user} />
          {userHandle(user)}
        </span>
      )
    }
    case "users": {
      const ids = (value as string[] | undefined) ?? []
      const people = users.filter((u) => ids.includes(u.id))
      if (!people.length) return null
      return (
        <span className="flex items-center gap-1">
          {people.map((user) => (
            <PreviewAvatar key={user.id} user={user} />
          ))}
        </span>
      )
    }
    default: {
      const text = displayText(value, column, users)
      if (!text) return null
      return (
        <span className="text-13">
          <span className="text-muted-foreground">{column.label}:</span> {text}
        </span>
      )
    }
  }
}

/**
 * A small summary card of one row, for the confirm dialog: the first
 * column's value as its title, then the other data columns as they look in
 * the table (see PreviewField) — the chips on one line, people and dates
 * on the next. Built from the same schema as the table,
 * so every consumer gets it for free.
 */
function RowPreview<T>({
  row,
  columns,
  users,
}: {
  row: T
  columns: DataTableColumn<T>[]
  users: DataTableUser[]
}) {
  const [titleColumn, ...rest] = columns.filter(
    (column) => column.type !== "actions" && column.type !== "custom",
  )
  if (!titleColumn) return null
  const value = (column: DataTableColumn<T>) => (row as Record<string, unknown>)[column.key]
  return (
    // No outline; a light red tint when the dialog is confirming a delete
    // (ConfirmDialog marks itself data-destructive)
    <div className="flex w-full max-w-md flex-col gap-2.5 rounded-card bg-surface px-5 py-3.5 text-left group-data-[destructive]/confirm:bg-destructive/10">
      <p className="text-15 font-medium">{displayText(value(titleColumn), titleColumn, users)}</p>
      {/* Chips together on one line, then people and dates on the next */}
      {[rest.filter((column) => column.type === "select"), rest.filter((column) => column.type !== "select")]
        .filter((group) => group.length > 0)
        .map((group, index) => (
          <div key={index} className="flex flex-wrap items-center gap-x-4 gap-y-2">
            {group.map((column) => (
              <PreviewField key={column.key} value={value(column)} column={column} users={users} />
            ))}
          </div>
        ))}
    </div>
  )
}

/** `options.findIndex`-based rank, unmatched values sort last (consistently). */
function optionRank(options: { value: string }[], value: unknown) {
  const index = options.findIndex((option) => option.value === value)
  return index === -1 ? options.length : index
}

export function buildColumns<T>(
  columns: DataTableColumn<T>[],
  ctx: BuildColumnsContext<T>,
): ColumnDef<T>[] {
  return columns.map((column) => {
    const isActions = column.type === "actions"
    // "custom" columns have no backing data field (their `key` is just a
    // unique column id — see types.ts), so `base.accessorFn` below always
    // reads undefined for them. Sorting/filtering on that silently does
    // nothing (or returns zero rows), so default both to off unless the
    // schema explicitly opts in — the consumer is then responsible for
    // having supplied a `render` that makes the affordance meaningful.
    const isCustom = column.type === "custom"
    const base = {
      id: column.key,
      header: column.label,
      accessorFn: (row: T) => (row as Record<string, unknown>)[column.key],
      enableSorting: isActions ? false : isCustom ? column.sortable === true : column.sortable !== false,
      enableColumnFilter: isActions
        ? false
        : isCustom
          ? column.filterable === true
          : column.filterable !== false,
      meta: { column },
    } satisfies Partial<ColumnDef<T>> & Record<string, unknown>

    // A render replaces the cell only, so the column keeps its type's
    // sorting and filtering
    const typed = typedColumn()
    if (!column.render) return typed
    return {
      ...typed,
      cell: ({ row }) => {
        const rowId = ctx.getRowId(row.original)
        return column.render?.(row.original, {
          addRow: ctx.addRow,
          removeRow: () => ctx.removeRow(rowId),
          updateRow: (patch) => ctx.updateRow(rowId, patch),
        })
      },
    } as ColumnDef<T>

    function typedColumn(): ColumnDef<T> {
      switch (column.type) {
        case "select":
          return {
            ...base,
            // The schema's `options` array is the authoritative order (e.g.
            // Priority: Low/Medium/High) — sort by that, not alphabetically.
            sortingFn: (a, b, columnId) => {
              const options = column.options ?? []
              return optionRank(options, a.getValue(columnId)) - optionRank(options, b.getValue(columnId))
            },
            // Membership, not substring — a row matches if its value is one of
            // the currently checked options (OR semantics). The toolbar's
            // multiselect filter UI normalizes an empty selection to
            // `undefined` rather than `[]`, so an empty array is never
            // expected to reach here.
            filterFn: (row, columnId, filterValue: string[]) =>
              filterValue.includes(row.getValue(columnId)),
            cell: ({ row }) => (
              <SelectCell
                value={row.getValue<string>(column.key) ?? ""}
                options={column.options ?? []}
                unknownLabel={column.unknownLabel}
                onChange={(next) =>
                  ctx.updateRow(ctx.getRowId(row.original), {
                    [column.key]: next,
                  } as Partial<T>)
                }
              />
            ),
          } as ColumnDef<T>

        case "date":
          return {
            ...base,
            sortingFn: compareIsoDates,
            // Range filter: ISO strings compare correctly lexicographically,
            // same principle as compareIsoDates above.
            filterFn: (row, columnId, filterValue: { from?: string; to?: string }) => {
              const value = row.getValue<string>(columnId)
              if (!value) return false // no due date can't fall within a range
              if (filterValue.from && value < filterValue.from) return false
              if (filterValue.to && value > filterValue.to) return false
              return true
            },
            cell: ({ row }) => (
              <DateCell
                value={row.getValue<string>(column.key) ?? ""}
                onChange={(next) =>
                  ctx.updateRow(ctx.getRowId(row.original), {
                    [column.key]: next,
                  } as Partial<T>)
                }
              />
            ),
          } as ColumnDef<T>

        case "user":
          return {
            ...base,
            // Sort and filter by the person's handle as displayed, never
            // their opaque id.
            sortingFn: (a, b, columnId) => {
              const name = (id: string) => {
                const user = ctx.users.find((u) => u.id === id)
                return user ? userHandle(user) : ""
              }
              return name(a.getValue(columnId)).localeCompare(name(b.getValue(columnId)))
            },
            // The multiselect filter UI picks specific people (by id) from the
            // roster rather than typing a name, so this is a direct id
            // membership check — unlike sortingFn above, which still needs to
            // resolve to the person's handle.
            filterFn: (row, columnId, filterValue: string[]) =>
              filterValue.includes(row.getValue(columnId)),
            cell: ({ row }) => (
              <UserCell
                value={row.getValue<string>(column.key) ?? ""}
                users={ctx.users}
                options={ctx.pickableUsers(row.original)}
                onChange={(next) =>
                  ctx.updateRow(ctx.getRowId(row.original), {
                    [column.key]: next,
                  } as Partial<T>)
                }
              />
            ),
          } as ColumnDef<T>

        case "users":
          return {
            ...base,
            // By headcount, then by the handles themselves
            sortingFn: (a, b, columnId) => {
              const left = a.getValue<string[]>(columnId) ?? []
              const right = b.getValue<string[]>(columnId) ?? []
              if (left.length !== right.length) return left.length - right.length
              return displayText(left, column, ctx.users).localeCompare(
                displayText(right, column, ctx.users),
              )
            },
            // Same people-picker filter as "user": a row matches if any of the
            // checked people is among its participants
            filterFn: (row, columnId, filterValue: string[]) =>
              (row.getValue<string[]>(columnId) ?? []).some((id) => filterValue.includes(id)),
            cell: ({ row }) => (
              <UsersCell
                value={row.getValue<string[]>(column.key) ?? []}
                users={ctx.users}
                options={ctx.pickableUsers(row.original)}
                onChange={(next) =>
                  ctx.updateRow(ctx.getRowId(row.original), {
                    [column.key]: next,
                  } as Partial<T>)
                }
              />
            ),
          } as ColumnDef<T>

        case "actions":
          return {
            ...base,
            accessorFn: undefined,
            cell: ({ row }) => {
              const rowId = ctx.getRowId(row.original)
              return (
                <ActionsCell
                  row={row.original}
                  actions={column.actions ?? []}
                  helpers={{
                    addRow: ctx.addRow,
                    removeRow: () => ctx.removeRow(rowId),
                    updateRow: (patch) => ctx.updateRow(rowId, patch),
                  }}
                  preview={<RowPreview row={row.original} columns={columns} users={ctx.users} />}
                />
              )
            },
          } as ColumnDef<T>

        default:
          return {
            ...base,
            cell: ({ row }) => <TextCell value={row.getValue(column.key)} />,
          } as ColumnDef<T>
      }
    }
  })
}
