import { useState } from "react"
import { useOptions } from "@/api/queries"
import { AddIcon, CloseIcon } from "@/components/icons"
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import type { FieldSchema, RowColumn } from "../types"
import { type WidgetProps, asText } from "./types"

type Row = Record<string, unknown>

const ICON_BUTTON = "inline-flex size-8 cursor-pointer items-center justify-center rounded-full text-muted-foreground hover:text-text-primary"

function asRows(value: unknown): Row[] {
  return Array.isArray(value) ? value.filter((row): row is Row => typeof row === "object" && row !== null) : []
}

function emptyCell(schema: FieldSchema | undefined) {
  return schema?.type === "boolean" ? false : ""
}

/** The rows in order of the string at `by`; rows with none yet come last */
export function sortRows(rows: Row[], by: string) {
  const key = (row: Row) => (typeof row[by] === "string" ? row[by] : "")
  return rows.toSorted((a, b) => {
    if (key(a) === key(b)) return 0
    if (key(a) === "") return 1
    if (key(b) === "") return -1
    return key(a) < key(b) ? -1 : 1
  })
}

/**
 * A list of objects as a table, one column per `item.columns`. A radio column
 * holds at most one chosen row. `sortBy` keeps the rows in order, `addWith`
 * adds rows from a person search, and a row under `lockedWhen` has no Remove
 * and only its empty date cells stay editable (WL-REQ-1893).
 */
export function RowsWidget({ id, value, onChange, schema, item, readOnly, invalid, cellErrors = {}, fill }: WidgetProps) {
  const rows = asRows(value)
  const columns = item.columns ?? []
  const properties = schema.items?.properties ?? {}
  const noun = schema.items?.title ?? "row"
  const url = item.addWith ? properties[item.addWith]?.["x-options"] : columns.map((column) => properties[column.field]?.["x-options"]).find(Boolean)
  const people = useOptions(url ? fill(url) : undefined)
  const person = (actor: unknown) => people.data?.find((candidate) => candidate.id === actor)

  const isLocked = (row: Row) => item.lockedWhen !== undefined && row[item.lockedWhen] === true
  const labelOf = (row: Row, index: number) => {
    const named = columns.find((column) => column.display === "name")
    return (named && person(row[named.field])?.name) || `${noun} ${index + 1}`
  }
  const setCell = (index: number, field: string, cell: unknown) => {
    const next = rows.map((row, at) => (at === index ? { ...row, [field]: cell } : row))
    onChange(item.sortBy === field ? sortRows(next, field) : next)
  }
  const choose = (index: number, column: RowColumn) =>
    onChange(rows.map((row, at) => (at !== index ? { ...row, [column.field]: false } : { ...row, [column.field]: true, ...(column.notWith ? { [column.notWith]: false } : {}) })))
  const addRow = (extra: Row = {}) => {
    const blank = Object.fromEntries(columns.filter((column) => !column.display).map((column) => [column.field, emptyCell(properties[column.field])]))
    const next = [...rows, { ...blank, ...extra }]
    onChange(item.sortBy ? sortRows(next, item.sortBy) : next)
  }

  const renderCell = (row: Row, index: number, column: RowColumn) => {
    const name = `${column.title ?? column.field}: ${labelOf(row, index)}`
    const isDate = column.widget === "date" || properties[column.field]?.format === "date"
    const text = asText(row[column.field])
    if (column.widget === "radio") {
      return (
        <input
          type="radio"
          name={`${id}-${column.field}`}
          checked={row[column.field] === true}
          disabled={readOnly}
          onChange={() => choose(index, column)}
          aria-label={name}
          className="size-4 cursor-pointer"
        />
      )
    }
    if (column.display === "name") return person(row[column.field])?.name ?? text
    if (column.display === "role") return <span className="text-muted-foreground">{person(row[column.field])?.roles?.join(", ") || "—"}</span>
    if (readOnly || (isLocked(row) && !(isDate && text === ""))) return text
    return (
      <Input
        type={isDate ? "date" : "text"}
        aria-label={name}
        aria-invalid={cellErrors[`${index}/${column.field}`] ? true : undefined}
        value={text}
        onChange={(event) => setCell(index, column.field, event.target.value)}
      />
    )
  }

  const shownKeys = new Set(rows.flatMap((_, index) => columns.map((column) => `${index}/${column.field}`)))
  const leftover = Object.entries(cellErrors).filter(([key]) => !shownKeys.has(key))

  return (
    <div className="flex flex-col gap-4">
      {item.addWith && rows.length === 0 ? null : (
        <Table aria-invalid={invalid || undefined}>
          <TableHeader>
            <TableRow className="border-b border-text-primary">
              {columns.map((column, at) => (
                <TableHead key={at} scope="col" className="px-4">
                  {column.title ?? column.field}
                </TableHead>
              ))}
              {readOnly ? null : (
                <TableHead scope="col" className="w-10 px-4">
                  <span className="sr-only">Actions</span>
                </TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row, index) => (
              // Rows have no id of their own until they are saved: the position names them
              // oxlint-disable-next-line react/no-array-index-key
              <TableRow key={index} className="border-b border-divider-line/40 align-top">
                {columns.map((column, at) => {
                  const error = columns.findIndex((candidate) => candidate.field === column.field) === at ? cellErrors[`${index}/${column.field}`] : undefined
                  return (
                    <TableCell key={at} className="px-4 whitespace-normal">
                      {renderCell(row, index, column)}
                      {error ? <p className="mt-1 text-xs text-destructive">{error}</p> : null}
                    </TableCell>
                  )
                })}
                {readOnly ? null : (
                  <TableCell className="px-4 text-right">
                    {isLocked(row) ? null : (
                      <button type="button" aria-label={`Remove ${labelOf(row, index)}`} onClick={() => onChange(rows.filter((_, at) => at !== index))} className={ICON_BUTTON}>
                        <CloseIcon size={20} />
                      </button>
                    )}
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {leftover.length > 0 ? (
        <p role="alert" className="text-xs text-destructive">
          {[...new Set(leftover.map(([, message]) => message))].join(" ")}
        </p>
      ) : null}
      {readOnly ? null : item.addWith ? (
        <PersonSearch
          label={`Add ${noun}`}
          people={(people.data ?? []).filter((candidate) => !rows.some((row) => row[item.addWith!] === candidate.id))}
          onPick={(actor) => addRow({ [item.addWith!]: actor })}
        />
      ) : (
        <button type="button" onClick={() => addRow()} className="inline-flex cursor-pointer items-center gap-2 self-start text-sm">
          <AddIcon size={20} />
          {`Add ${noun}`}
        </button>
      )}
    </div>
  )
}

function PersonSearch({ label, people, onPick }: { label: string; people: { id: string; name: string }[]; onPick: (actor: string) => void }) {
  const [open, setOpen] = useState(false)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button type="button" aria-label={label} className="flex h-11 w-full cursor-pointer items-center justify-between rounded-md border border-input px-4 text-left text-sm">
          <span className="text-muted-foreground">Search people…</span>
          <AddIcon size={20} />
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-(--radix-popover-trigger-width) p-0" align="start">
        <Command>
          <CommandInput placeholder="Find someone..." />
          <CommandList>
            <CommandEmpty>No one found.</CommandEmpty>
            <CommandGroup>
              {people.map((person) => (
                <CommandItem
                  key={person.id}
                  value={person.name}
                  onSelect={() => {
                    onPick(person.id)
                    setOpen(false)
                  }}
                >
                  {person.name}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
