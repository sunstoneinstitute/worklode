import type { FieldItem, FieldSchema, FormConfig, FormLayout, FormSchema, FormValues, LayoutItem, NoteBlock, ShowWhen } from "./types"

// What a schema-built form shows, requires, sends and accepts, as plain
// functions over its config and values. This interprets the closed subset of
// JSON Schema WL-REQ-1888 lists; the route's validation decides, this only
// saves a round trip.

export function toItem(item: LayoutItem): FieldItem | NoteBlock {
  return typeof item === "string" ? { field: item } : item
}

export function isBlock(item: FieldItem | NoteBlock): item is NoteBlock {
  return "block" in item
}

/** A React key for an item: its field, or its place for a block */
export function itemKey(item: FieldItem | NoteBlock, index: number) {
  return isBlock(item) ? `block-${index}` : item.field
}

/** Every item of every column, in the order the layout lists them */
export function allItems(layout: FormLayout) {
  return Object.values(layout.columns).flat().map(toItem)
}

export function fieldItems(layout: FormLayout) {
  return allItems(layout).filter((item): item is FieldItem => !isBlock(item))
}

export function isShown(showWhen: ShowWhen | undefined, values: FormValues) {
  if (!showWhen) return true
  const value = values[showWhen.field]
  return "in" in showWhen ? showWhen.in.includes(value) : value === showWhen.equals
}

/** The layout's columns with only the items shown for these values */
export function visibleColumns(layout: FormLayout, values: FormValues) {
  return Object.fromEntries(
    Object.entries(layout.columns).map(([name, items]) => [name, items.map(toItem).filter((item) => isShown(item.showWhen, values))]),
  )
}

/** Keys of the fields on screen for these values, in layout order */
export function visibleFields(layout: FormLayout, values: FormValues) {
  return fieldItems(layout)
    .filter((item) => isShown(item.showWhen, values))
    .map((item) => item.field)
}

function emptyValue(property: FieldSchema) {
  if (property.type === "array") return []
  return property.type === "boolean" ? false : ""
}

/** A form's opening values: the record's, then the defaults, then each type's empty value */
export function startingValues(config: FormConfig, record?: FormValues): FormValues {
  return Object.fromEntries(
    Object.entries(config.schema.properties).map(([field, property]) => [
      field,
      record?.[field] ?? config.defaults?.[field] ?? emptyValue(property),
    ]),
  )
}

function trimRow(row: unknown, dropped: string[]) {
  if (typeof row !== "object" || row === null) return row
  return Object.fromEntries(
    Object.entries(row)
      .filter(([key]) => !dropped.includes(key))
      .map(([key, cell]) => [key, typeof cell === "string" ? cell.trim() : cell]),
  )
}

/**
 * The body: every shown field that is not locked, text trimmed, an empty one
 * as "" or [] so an edit can clear it. A `sendWhenChanged` field is left out
 * while it equals `initial`, and a readOnly row property is never sent.
 */
export function submittedValues(layout: FormLayout, schema: FormSchema, values: FormValues, initial: FormValues, locked: Record<string, string> = {}) {
  const sent: FormValues = {}
  for (const item of fieldItems(layout)) {
    const { field } = item
    if (!isShown(item.showWhen, values) || field in locked) continue
    const value = values[field]
    if (item.sendWhenChanged && JSON.stringify(value) === JSON.stringify(initial[field])) continue
    const items = schema.properties[field]?.items
    if (items?.type === "object" && Array.isArray(value)) {
      const dropped = Object.entries(items.properties ?? {}).filter(([, property]) => property.readOnly).map(([key]) => key)
      sent[field] = value.map((row) => trimRow(row, dropped))
    } else {
      sent[field] = typeof value === "string" ? value.trim() : value
    }
  }
  return sent
}

function matches(condition: FormSchema["if"], values: FormValues) {
  return Object.entries(condition?.properties ?? {}).every(([field, test]) => values[field] === test.const)
}

/** The schema's `required`, plus each `if`'s `then` or `else`, top level and inside `allOf` */
export function requiredFields(schema: FormSchema, values: FormValues) {
  const required = new Set(schema.required ?? [])
  for (const rule of [schema, ...(schema.allOf ?? [])]) {
    if (!rule.if) continue
    const branch = matches(rule.if, values) ? rule.then : rule.else
    for (const field of branch?.required ?? []) required.add(field)
  }
  return required
}

function isDate(text: string) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(text)) return false
  const date = new Date(`${text}T00:00:00Z`)
  return !Number.isNaN(date.getTime()) && date.toISOString().startsWith(text)
}

function valueError(property: FieldSchema, value: unknown, required: boolean, label: string) {
  const empty = value === undefined || value === null || value === "" || (Array.isArray(value) && value.length === 0)
  if (empty) return required ? (property.errorMessage ?? `${label} is required.`) : undefined
  if (typeof value !== "string") return undefined
  if (property.minLength && value.length < property.minLength) return property.errorMessage ?? `${label} is too short.`
  if (property.format === "date" && !isDate(value)) return property.errorMessage ?? `${label} must be a date.`
  if (property.oneOf && !property.oneOf.some((option) => option.const === value)) {
    return property.errorMessage ?? `${label} must be one of the choices.`
  }
  return undefined
}

function rowErrors(field: string, property: FieldSchema, rows: FormValues[]) {
  const messages: Record<string, string> = {}
  const properties = Object.entries(property.items?.properties ?? {}).filter(([, cell]) => !cell.readOnly)
  rows.forEach((row, index) => {
    for (const [key, cell] of properties) {
      const message = valueError(cell, row[key], property.items?.required?.includes(key) ?? false, cell.title ?? key)
      if (message) messages[`${field}/${index}/${key}`] = message
    }
  })
  for (const [key, cell] of properties) {
    if (cell.maxContains !== undefined && rows.filter((row) => row[key] === true).length > cell.maxContains) {
      messages[field] = cell.errorMessage ?? `Only ${cell.maxContains} can be ${cell.title ?? key}.`
    }
  }
  return messages
}

/**
 * Per-field messages for the body `submittedValues` built, keyed by field or
 * "<field>/<index>/<property>" for a cell; {} when it is valid. What is not
 * sent is not checked, and an empty optional field passes.
 */
export function validate(schema: FormSchema, sent: FormValues) {
  const required = requiredFields(schema, sent)
  const messages: Record<string, string> = {}
  for (const [field, value] of Object.entries(sent)) {
    const property = schema.properties[field]
    if (!property) continue
    const message = valueError(property, value, required.has(field), property.title ?? field)
    if (message) messages[field] = message
    if (property.items?.type === "object" && Array.isArray(value)) Object.assign(messages, rowErrors(field, property, value as FormValues[]))
  }
  return messages
}

/** The cells' messages of one list field, keyed "<index>/<property>" */
export function cellErrorsFor(messages: Record<string, string>, field: string) {
  const prefix = `${field}/`
  return Object.fromEntries(
    Object.entries(messages)
      .filter(([key]) => key.startsWith(prefix))
      .map(([key, message]) => [key.slice(prefix.length), message]),
  )
}

/** Whether two sets of values differ. A missing value and "" count as the same. */
export function valuesDiffer(a: FormValues, b: FormValues) {
  return [...new Set([...Object.keys(a), ...Object.keys(b)])].some((field) => JSON.stringify(a[field] ?? "") !== JSON.stringify(b[field] ?? ""))
}

/** A value as one line of text, for a locked field and an older draft */
export function displayText(property: FieldSchema | undefined, value: unknown) {
  if (value === undefined || value === null || value === "") return ""
  if (Array.isArray(value)) return value.join(", ")
  return property?.oneOf?.find((option) => option.const === value)?.title ?? String(value)
}

/** Fills `{name}` placeholders from `values`, URL-encoded */
export function fillUrl(url: string, values: FormValues) {
  return url.replace(/\{(\w+)\}/g, (_, name: string) => encodeURIComponent(String(values[name] ?? "")))
}
